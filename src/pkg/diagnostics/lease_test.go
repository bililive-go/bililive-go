package diagnostics

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func newIsolatedManager(t *testing.T, cfg Config) *Manager {
	t.Helper()
	manager, err := newManager(cfg)
	require.NoError(t, err)
	t.Cleanup(func() {
		if !manager.closed.Load() {
			_ = manager.Abort()
		}
	})
	return manager
}

func findRunInfo(t *testing.T, manager *Manager, runID string) RunInfo {
	t.Helper()
	runs, err := manager.ListRuns()
	require.NoError(t, err)
	for _, run := range runs {
		if run.RunID == runID {
			return run
		}
	}
	t.Fatalf("找不到 run %s", runID)
	return RunInfo{}
}

func TestSharedAppDataKeepsRemoteActiveRunAndRejectsAcknowledge(t *testing.T) {
	cfg := testConfig(t.TempDir())
	cfg.MaxRuns = 1
	first := newIsolatedManager(t, cfg)
	firstRunID := first.RunID()

	// 即使 heartbeat 与文件租约时间都已经很旧，只要 owner 仍持有内核
	// 文件锁，就可能只是进程长时间暂停，绝不能 ACK 或 prune。
	old := time.Now().UTC().Add(-24 * time.Hour)
	heartbeat, ok := readHeartbeat(filepath.Join(first.runDir, "heartbeat.json"))
	require.True(t, ok)
	heartbeat.At = old
	require.NoError(t, atomicWriteJSON(
		filepath.Join(first.runDir, "heartbeat.json"),
		heartbeat,
		true,
	))
	lease, ok := readRunLease(filepath.Join(first.runDir, runLeaseFileName))
	require.True(t, ok)
	lease.RenewedAt = old
	lease.ExpiresAt = old.Add(time.Minute)
	require.NoError(t, atomicWriteJSON(
		filepath.Join(first.runDir, runLeaseFileName),
		lease,
		true,
	))

	second := newIsolatedManager(t, cfg)
	report := second.StartupStatus()
	require.NotNil(t, report.PreviousRun)
	require.Equal(t, firstRunID, report.PreviousRun.RunID)
	require.True(t, report.PreviousRun.Active)
	require.Equal(t, "active", report.PreviousRun.Status)
	require.Equal(t, "lease_lock_held", report.PreviousRun.ActiveReason)
	require.Equal(t, os.Getpid(), report.PreviousRun.OwnerPID)
	require.Empty(t, report.UncleanRuns)
	require.Len(t, report.ActiveRuns, 1)
	require.Equal(t, firstRunID, report.ActiveRuns[0].RunID)

	remote := findRunInfo(t, second, firstRunID)
	require.True(t, remote.Active)
	require.False(t, remote.Current, "另一个实例的 run 是 active，但不是本 Manager 的 current")
	require.ErrorIs(t, second.Acknowledge(firstRunID), ErrRunActive)
	require.NoFileExists(t, filepath.Join(first.runDir, "ack.json"))
	require.DirExists(t, first.runDir, "MaxRuns prune 绝不能删除仍持锁的 run")
}

func TestReleasedOwnerLockMakesRecentRunImmediatelyInvestigable(t *testing.T) {
	cfg := testConfig(t.TempDir())
	first := newIsolatedManager(t, cfg)
	firstRunID := first.RunID()

	// 模拟 SIGKILL：磁盘上仍是 running、heartbeat/租约仍新鲜、PID 甚至
	// 还是当前测试进程，但内核已释放 owner lock。
	require.NotNil(t, first.leaseLock)
	require.NoError(t, first.leaseLock.close())
	first.leaseLock = nil

	second := newIsolatedManager(t, cfg)
	report := second.StartupStatus()
	require.NotNil(t, report.PreviousRun)
	require.Equal(t, firstRunID, report.PreviousRun.RunID)
	require.False(t, report.PreviousRun.Active)
	require.Equal(t, "unclean", report.PreviousRun.Status)
	require.Empty(t, report.ActiveRuns)
	require.Len(t, report.UncleanRuns, 1)
	require.Equal(t, firstRunID, report.UncleanRuns[0].RunID)

	require.NoError(t, second.Acknowledge(firstRunID))
	require.FileExists(t, filepath.Join(first.runDir, "ack.json"))
}

func TestLegacyRecentHeartbeatUsesOwnerPIDButStaleHeartbeatDoesNot(t *testing.T) {
	appDataPath := t.TempDir()
	runsDir := filepath.Join(appDataPath, "diagnostics", "runs")
	require.NoError(t, os.MkdirAll(runsDir, 0o700))
	runID := "run-legacy-current-pid"
	runDir := filepath.Join(runsDir, runID)
	require.NoError(t, os.Mkdir(runDir, 0o700))
	startedAt := time.Now().UTC().Add(-time.Minute)
	require.NoError(t, atomicWriteJSON(filepath.Join(runDir, "run.json"), RunManifest{
		Schema:    RunSchema,
		RunID:     runID,
		StartedAt: startedAt,
		State:     "running",
		PID:       os.Getpid(),
	}, false))
	heartbeat := heartbeatFile{
		Schema: "bililive.diagnostics-heartbeat/v1",
		RunID:  runID,
		At:     time.Now().UTC(),
		State:  "running",
	}
	require.NoError(t, atomicWriteJSON(filepath.Join(runDir, "heartbeat.json"), heartbeat, false))

	runs, err := scanRunInfos(runsDir, "")
	require.NoError(t, err)
	require.Len(t, runs, 1)
	require.True(t, runs[0].Active)
	require.Equal(t, "legacy_owner_alive", runs[0].ActiveReason)

	heartbeat.At = time.Now().UTC().Add(-legacyHeartbeatFreshFor - time.Second)
	require.NoError(t, atomicWriteJSON(filepath.Join(runDir, "heartbeat.json"), heartbeat, true))
	runs, err = scanRunInfos(runsDir, "")
	require.NoError(t, err)
	require.Len(t, runs, 1)
	require.False(t, runs[0].Active)
	require.Equal(t, "unclean", runs[0].Status)
}

func TestPanickedButContinuingOwnerKeepsFallbackLeaseActive(t *testing.T) {
	appDataPath := t.TempDir()
	runsDir := filepath.Join(appDataPath, "diagnostics", "runs")
	require.NoError(t, os.MkdirAll(runsDir, 0o700))
	runID := "run-panicked-continuing"
	runDir := filepath.Join(runsDir, runID)
	require.NoError(t, os.Mkdir(runDir, 0o700))
	owner, err := observeProcess(os.Getpid())
	require.NoError(t, err)
	require.True(t, owner.Exists)
	startedAt := time.Now().UTC().Add(-time.Minute)
	require.NoError(t, atomicWriteJSON(filepath.Join(runDir, "run.json"), RunManifest{
		Schema:    RunSchema,
		RunID:     runID,
		StartedAt: startedAt,
		State:     "running",
		PID:       os.Getpid(),
	}, false))
	require.NoError(t, atomicWriteJSON(filepath.Join(runDir, "panic.json"), panicMarker{
		Schema:    "bililive.diagnostics-panic/v1",
		RunID:     runID,
		CreatedAt: time.Now().UTC(),
		Value:     "recovered background panic",
	}, false))
	lease := runLease{
		Schema:           runLeaseSchema,
		RunID:            runID,
		OwnerID:          "owner-without-file-lock",
		OwnerPID:         os.Getpid(),
		OwnerStartedAtMS: owner.StartedAtMS,
		State:            "panicked",
		RenewedAt:        time.Now().UTC().Add(-time.Hour),
		ExpiresAt:        time.Now().UTC().Add(-time.Minute),
		LockAcquired:     false,
	}
	require.NoError(t, atomicWriteJSON(filepath.Join(runDir, runLeaseFileName), lease, false))

	runs, err := scanRunInfos(runsDir, "")
	require.NoError(t, err)
	require.Len(t, runs, 1)
	require.True(t, runs[0].Active, "无文件锁降级时，仍存活的 panic owner 必须继续续租")
	require.Equal(t, "panic", runs[0].Status, "panic 状态优先，但 Active 仍单独表达")
	require.Equal(t, "owner_identity_alive", runs[0].ActiveReason)

	originalObserveProcess := observeProcess
	observeProcess = func(int) (processObservation, error) {
		return processObservation{}, errors.New("权限拒绝")
	}
	runs, err = scanRunInfos(runsDir, "")
	observeProcess = originalObserveProcess
	require.NoError(t, err)
	require.True(t, runs[0].Active)
	require.Equal(t, "owner_unverified", runs[0].ActiveReason,
		"无法查询 owner 时即使租约过期，也不能 prune 可能恢复的进程")

	lease.State = "panicked_closed"
	lease.RenewedAt = time.Now().UTC()
	lease.ExpiresAt = lease.RenewedAt.Add(time.Minute)
	require.NoError(t, atomicWriteJSON(filepath.Join(runDir, runLeaseFileName), lease, true))
	runs, err = scanRunInfos(runsDir, "")
	require.NoError(t, err)
	require.Len(t, runs, 1)
	require.False(t, runs[0].Active, "终态发布后，即使 launcher 进程仍在也不能误判 active")
	require.Equal(t, "panic", runs[0].Status)
}

func TestPruneNeverDeletesActiveRunEvenIfAlreadyAcknowledged(t *testing.T) {
	root := t.TempDir()
	activePath := filepath.Join(root, "active")
	cleanPath := filepath.Join(root, "clean")
	acknowledgedPath := filepath.Join(root, "acknowledged")
	for _, path := range []string{activePath, cleanPath, acknowledgedPath} {
		require.NoError(t, os.Mkdir(path, 0o700))
	}
	runs := []RunInfo{
		{RunID: "run-active", Path: activePath, Active: true, Acknowledged: true},
		{RunID: "run-clean", Path: cleanPath, Clean: true},
		{RunID: "run-acknowledged", Path: acknowledgedPath, Acknowledged: true},
	}
	kept := pruneEligibleRuns(runs, 1)
	require.Len(t, kept, 1)
	require.Equal(t, "run-active", kept[0].RunID)
	require.DirExists(t, activePath)
	require.NoDirExists(t, cleanPath)
	require.NoDirExists(t, acknowledgedPath)
}

func TestRecordPanicAfterCloseRevokesCleanTerminalState(t *testing.T) {
	manager := newIsolatedManager(t, testConfig(t.TempDir()))
	require.NoError(t, manager.Close())
	require.FileExists(t, filepath.Join(manager.runDir, "clean.json"))

	require.NoError(t, manager.RecordPanic(context.Background(), "panic after close"))
	require.NoFileExists(t, filepath.Join(manager.runDir, "clean.json"))
	require.FileExists(t, filepath.Join(manager.runDir, "panic.json"))
	require.FileExists(t, filepath.Join(manager.runDir, "panic.stack"))

	manifest, err := readManifest(filepath.Join(manager.runDir, "run.json"))
	require.NoError(t, err)
	require.Equal(t, "panicked", manifest.State)
	heartbeat, ok := readHeartbeat(filepath.Join(manager.runDir, "heartbeat.json"))
	require.True(t, ok)
	require.Equal(t, "panicked", heartbeat.State)
	require.True(t, heartbeat.Panicked)

	info := findRunInfo(t, manager, manager.RunID())
	require.False(t, info.Clean)
	require.True(t, info.HasPanic)
	require.False(t, info.Active)
	require.Equal(t, "panic", info.Status)
}

func TestConcurrentCloseAndRecordPanicNeverPublishesCleanResult(t *testing.T) {
	for iteration := 0; iteration < 8; iteration++ {
		manager := newIsolatedManager(t, testConfig(t.TempDir()))
		start := make(chan struct{})
		var wait sync.WaitGroup
		wait.Add(2)
		var closeErr, panicErr error
		go func() {
			defer wait.Done()
			<-start
			closeErr = manager.Close()
		}()
		go func() {
			defer wait.Done()
			<-start
			panicErr = manager.RecordPanic(context.Background(), "concurrent panic")
		}()
		close(start)
		wait.Wait()
		require.NoError(t, closeErr)
		require.NoError(t, panicErr)
		require.NoFileExists(t, filepath.Join(manager.runDir, "clean.json"))
		require.FileExists(t, filepath.Join(manager.runDir, "panic.json"))

		info := findRunInfo(t, manager, manager.RunID())
		require.False(t, info.Clean)
		require.True(t, info.HasPanic)
		require.Equal(t, "panic", info.Status)
	}
}

func TestRecordPanicPublishesAbnormalStateBeforeWaitingForTerminalLock(t *testing.T) {
	manager := newIsolatedManager(t, testConfig(t.TempDir()))

	// 精确模拟 Close 正在终态临界区中：panic hook 已经进入，但暂时无法取得
	// terminalMu。panicked 必须在等待锁之前就可见，否则主 goroutine 可能先
	// 发布 clean、从 Close 返回并终止整个进程。
	manager.terminalMu.Lock()
	terminalLocked := true
	defer func() {
		if terminalLocked {
			manager.terminalMu.Unlock()
		}
	}()

	panicDone := make(chan error, 1)
	go func() {
		panicDone <- manager.RecordPanic(context.Background(), "blocked panic hook")
	}()
	require.Eventually(t, manager.panicked.Load, time.Second, time.Millisecond,
		"RecordPanic 等待 terminalMu 时必须已经发布 panicked")

	closeDone := make(chan error, 1)
	go func() {
		closeDone <- manager.Close()
	}()

	manager.terminalMu.Unlock()
	terminalLocked = false
	require.NoError(t, <-panicDone)
	require.NoError(t, <-closeDone)
	require.NoFileExists(t, filepath.Join(manager.runDir, "clean.json"))
	require.FileExists(t, filepath.Join(manager.runDir, "panic.json"))
	require.Equal(t, "panic", findRunInfo(t, manager, manager.RunID()).Status)
}

func TestRecordPanicPublishesAbnormalStateBeforeWaitingForPanicGate(t *testing.T) {
	manager := newIsolatedManager(t, testConfig(t.TempDir()))

	// 模拟 Close 已经取得 panicGate 写锁、尚未决定是否发布 clean。panic
	// hook 进入后即使被写锁挡住，也必须先让 panicked 可见。
	manager.panicGate.Lock()
	gateLocked := true
	defer func() {
		if gateLocked {
			manager.panicGate.Unlock()
		}
	}()

	panicDone := make(chan error, 1)
	go func() {
		panicDone <- manager.RecordPanic(context.Background(), "blocked by close gate")
	}()
	require.Eventually(t, manager.panicked.Load, time.Second, time.Millisecond,
		"RecordPanic 等待 panicGate 时必须已经发布 panicked")

	manager.panicGate.Unlock()
	gateLocked = false
	require.NoError(t, <-panicDone)
	require.NoError(t, manager.Close())
	require.NoFileExists(t, filepath.Join(manager.runDir, "clean.json"))
	require.FileExists(t, filepath.Join(manager.runDir, "panic.json"))
}

func TestScanAlwaysGivesPanicMarkerPriorityOverCleanMarker(t *testing.T) {
	manager := newIsolatedManager(t, testConfig(t.TempDir()))
	require.NoError(t, manager.RecordPanic(context.Background(), "panic marker wins"))
	require.NoError(t, manager.Close())
	require.NoError(t, atomicWriteJSON(filepath.Join(manager.runDir, "clean.json"), cleanMarker{
		Schema:    "bililive.diagnostics-clean/v1",
		RunID:     manager.RunID(),
		CreatedAt: time.Now().UTC(),
	}, false))

	info := findRunInfo(t, manager, manager.RunID())
	require.True(t, info.HasPanic)
	require.False(t, info.Clean)
	require.Equal(t, "panic", info.Status)
}
