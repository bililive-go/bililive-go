package diagnostics

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

const (
	crashHelperEnv     = "BILILIVE_DIAGNOSTICS_CRASH_HELPER"
	crashHelperPathEnv = "BILILIVE_DIAGNOSTICS_CRASH_APP_DATA"
)

func crashRestartTestConfig(appDataPath string) Config {
	cfg := testConfig(appDataPath)
	cfg.HeartbeatInterval = 40 * time.Millisecond
	cfg.EventSyncInterval = 40 * time.Millisecond
	cfg.MaxRuns = 4
	return cfg
}

// TestDiagnosticsSIGKILLHelperProcess 只由下面的父测试作为独立进程启动。
// 它故意不注册 Close/Abort，确保 Process.Kill 留下真实的 running manifest、
// 新鲜 heartbeat 和被内核释放的 lease.lock。
func TestDiagnosticsSIGKILLHelperProcess(t *testing.T) {
	if os.Getenv(crashHelperEnv) != "1" {
		t.Skip("仅作为 SIGKILL 子进程 helper 运行")
	}
	appDataPath := os.Getenv(crashHelperPathEnv)
	if appDataPath == "" {
		t.Fatal("缺少子进程 AppDataPath")
	}
	manager, err := newManager(crashRestartTestConfig(appDataPath))
	require.NoError(t, err)
	manager.Record(context.Background(), "subprocess.before_kill", Fields{
		"component": "test",
		"lane":      "Process",
		"evidence":  "must survive restart",
	})
	_, err = manager.SnapshotCurrent()
	require.NoError(t, err)
	fmt.Fprintf(os.Stdout, "DIAGNOSTICS_READY %s\n", manager.RunID())

	// heartbeat goroutine 保持运行；父进程会直接 Kill，不会执行任何 defer。
	select {}
}

func TestActualProcessKillThenRestartPreservesAndExportsPreviousRun(t *testing.T) {
	appDataPath := t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	command := exec.CommandContext(
		ctx,
		os.Args[0],
		"-test.run=^TestDiagnosticsSIGKILLHelperProcess$",
		"-test.v=false",
	)
	command.Env = append(
		os.Environ(),
		crashHelperEnv+"=1",
		crashHelperPathEnv+"="+appDataPath,
	)
	stdout, err := command.StdoutPipe()
	require.NoError(t, err)
	var stderr bytes.Buffer
	command.Stderr = &stderr
	require.NoError(t, command.Start())
	waited := false
	t.Cleanup(func() {
		if waited {
			return
		}
		_ = command.Process.Kill()
		_ = command.Wait()
	})

	reader := bufio.NewReader(stdout)
	readyLine, err := reader.ReadString('\n')
	if err != nil {
		_ = command.Wait()
		waited = true
		t.Fatalf("SIGKILL helper 未就绪：%v\nstderr:\n%s", err, stderr.String())
	}
	parts := strings.Fields(readyLine)
	require.Len(t, parts, 2, "无法解析 helper 输出：%q", readyLine)
	require.Equal(t, "DIAGNOSTICS_READY", parts[0])
	previousRunID := parts[1]
	require.NoError(t, validateRunID(previousRunID))
	runsDir := filepath.Join(appDataPath, "diagnostics", "runs")
	previousRunDir := filepath.Join(runsDir, previousRunID)
	require.DirExists(t, previousRunDir)

	// Kill 前先证明另一个进程持有的 run 能被当前进程识别为 remote active。
	beforeKill, err := scanRunInfos(runsDir, "")
	require.NoError(t, err)
	require.Len(t, beforeKill, 1)
	require.True(t, beforeKill[0].Active)
	require.False(t, beforeKill[0].Current)
	require.Equal(t, "active", beforeKill[0].Status)
	require.Equal(t, "lease_lock_held", beforeKill[0].ActiveReason)

	require.NoError(t, command.Process.Kill())
	_ = command.Wait() // 被 Kill 的非零退出属于预期。
	waited = true

	// 不等待租约超时：内核锁已经释放，所以必须立即成为可调查的 unclean。
	immediatelyAfterKill, err := scanRunInfos(runsDir, "")
	require.NoError(t, err)
	require.Len(t, immediatelyAfterKill, 1)
	require.Equal(t, previousRunID, immediatelyAfterKill[0].RunID)
	require.False(t, immediatelyAfterKill[0].Active)
	require.False(t, immediatelyAfterKill[0].Clean)
	require.Equal(t, "unclean", immediatelyAfterKill[0].Status)
	require.NoFileExists(t, filepath.Join(previousRunDir, "clean.json"))

	oldEventPaths, err := eventSegmentPaths(filepath.Join(previousRunDir, "events"))
	require.NoError(t, err)
	require.NotEmpty(t, oldEventPaths)
	oldEventBytes := make(map[string][]byte, len(oldEventPaths))
	for _, eventPath := range oldEventPaths {
		data, readErr := os.ReadFile(eventPath)
		require.NoError(t, readErr)
		oldEventBytes[eventPath] = data
	}

	// 使用完全相同的配置和 AppData 重启。新 run 必须使用新目录，Startup
	// 报告则立即指向刚才被 Kill 的 run。
	restarted := newIsolatedManager(t, crashRestartTestConfig(appDataPath))
	require.NotEqual(t, previousRunID, restarted.RunID())
	require.DirExists(t, previousRunDir)
	require.DirExists(t, restarted.runDir)
	startup := restarted.StartupStatus()
	require.NotNil(t, startup.PreviousRun)
	require.Equal(t, previousRunID, startup.PreviousRun.RunID)
	require.Equal(t, "unclean", startup.PreviousRun.Status)
	require.False(t, startup.PreviousRun.Active)
	require.Empty(t, startup.ActiveRuns)
	require.Condition(t, func() bool {
		for _, run := range startup.UncleanRuns {
			if run.RunID == previousRunID {
				return true
			}
		}
		return false
	})

	restarted.Record(context.Background(), "restarted.current.run", Fields{
		"component": "test",
	})
	_, err = restarted.SnapshotCurrent()
	require.NoError(t, err)
	for eventPath, before := range oldEventBytes {
		after, readErr := os.ReadFile(eventPath)
		require.NoError(t, readErr)
		require.Equal(t, before, after, "新 run 不得覆盖旧 run 的事件证据")
	}

	viewerArtifact, err := restarted.BuildViewerBundle(previousRunID)
	require.NoError(t, err)
	viewerData, err := os.ReadFile(viewerArtifact.Path)
	require.NoError(t, err)
	var bundle viewerBundle
	require.NoError(t, json.Unmarshal(viewerData, &bundle))
	require.Condition(t, func() bool {
		for _, event := range bundle.Events {
			if event.Name == "subprocess.before_kill" &&
				event.Attrs["evidence"] == "must survive restart" {
				return true
			}
		}
		return false
	}, "Viewer bundle 必须包含 Kill 前已刷盘的业务证据")
	require.Condition(t, func() bool {
		for _, event := range bundle.Events {
			if event.Name == "process.exit.unobserved" {
				return true
			}
		}
		return false
	}, "Viewer 应从缺少 clean marker 推断异常退出")
	for _, event := range bundle.Events {
		require.NotEqual(t, "restarted.current.run", event.Name,
			"旧 run 的 bundle 不能混入重启后当前 run 的事件")
	}

	archiveArtifact, err := restarted.BuildArchive(previousRunID)
	require.NoError(t, err)
	entries := readArchiveEntries(t, archiveArtifact.Path)
	require.Contains(t, entries, "viewer.json")
	require.Contains(t, entries, "bundle.json")
	require.Contains(t, entries, "run/run.json")
	require.Contains(t, entries, "run/lease.json")
	require.Condition(t, func() bool {
		for name := range entries {
			if strings.HasPrefix(name, "run/events/events-") &&
				strings.HasSuffix(name, ".jsonl") {
				return true
			}
		}
		return false
	}, "归档必须包含旧 run 的原始 JSONL 事件证据")
	require.NoError(t, restarted.Acknowledge(previousRunID),
		"锁已释放的异常 run 应立即允许 ACK")
}
