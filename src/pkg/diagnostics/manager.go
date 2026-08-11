package diagnostics

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"sync"
	"sync/atomic"
	"time"
)

type heartbeatFile struct {
	Schema        string    `json:"schema"`
	RunID         string    `json:"run_id"`
	At            time.Time `json:"at"`
	State         string    `json:"state"`
	LatestSeq     uint64    `json:"latest_seq"`
	DroppedEvents uint64    `json:"dropped_events"`
	Panicked      bool      `json:"panicked"`
}

type cleanMarker struct {
	Schema    string    `json:"schema"`
	RunID     string    `json:"run_id"`
	CreatedAt time.Time `json:"created_at"`
	FinalSeq  uint64    `json:"final_seq"`
}

type panicMarker struct {
	Schema    string    `json:"schema"`
	RunID     string    `json:"run_id"`
	CreatedAt time.Time `json:"created_at"`
	Value     string    `json:"value"`
	ValueType string    `json:"value_type"`
	EventSeq  uint64    `json:"event_seq"`
	StackFile string    `json:"stack_file"`
}

// Manager 管理一个独立的 diagnostics run。
type Manager struct {
	cfg        Config
	root       string
	runsDir    string
	exportsDir string
	runDir     string
	runID      string
	startedAt  time.Time
	scopeKey   []byte
	ownerID    string
	ownerPID   int
	// ownerStartedAtMS 与 PID 一起识别进程，避免 PID 被复用后把陈旧 run
	// 误判为仍在运行。
	ownerStartedAtMS int64
	leaseDuration    time.Duration
	leaseLock        *leaseFileLock
	manifest         RunManifest
	startup          StartupReport
	startupMu        sync.RWMutex

	eventMu sync.Mutex
	writer  *eventWriter

	markerMu sync.Mutex
	// panicGate 把 panic marker 的持久化和正常终态提交之间建立明确边界。
	// RecordPanic 在任何锁之前立即发布 panicked，再取得读锁写 marker；
	// Close 则取得写锁后提交终态。即使 Close 已持有写锁，后来进入的 panic
	// 也能先让 panicked 可见，最坏缺少 marker，但绝不会被误判为 clean。
	panicGate  sync.RWMutex
	terminalMu sync.Mutex // clean 与 panic 终态发布互斥，panic 永远优先
	// exportGate 串行化可能占用较多 CPU/磁盘的导出，同时允许尚未开始的
	// HTTP 请求在客户端断开后立即放弃排队。不能用普通 Mutex，否则取消
	// 的请求仍会一直等到前一个大包压缩完成。
	exportGate chan struct{}
	livenessMu sync.Mutex
	leaseMu    sync.Mutex

	stopping atomic.Bool
	closed   atomic.Bool
	panicked atomic.Bool
	lastSeq  atomic.Uint64
	dropped  atomic.Uint64
	idSeq    atomic.Uint64

	closeOnce sync.Once
	closeDone chan struct{}
	closeErr  error
	cleanExit atomic.Bool

	heartbeatStop chan struct{}
	heartbeatDone chan struct{}
	flight        *flightRecorder

	errMu   sync.Mutex
	lastErr error
}

func normalizeConfig(cfg Config) (Config, error) {
	if cfg.AppDataPath == "" {
		return cfg, fmt.Errorf("diagnostics AppDataPath 不能为空")
	}
	if cfg.TraceMode == "" {
		cfg.TraceMode = "diagnostic"
	}
	if cfg.HeartbeatInterval <= 0 {
		cfg.HeartbeatInterval = 5 * time.Second
	}
	if cfg.EventSyncInterval <= 0 {
		cfg.EventSyncInterval = time.Second
	}
	if cfg.EventSegmentBytes <= 0 {
		cfg.EventSegmentBytes = defaultEventSegmentBytes
	}
	if cfg.MaxEventSegments <= 0 {
		cfg.MaxEventSegments = defaultMaxEventSegments
	}
	if cfg.MaxRuns <= 0 {
		cfg.MaxRuns = defaultMaxRuns
	}
	if cfg.Flight.SnapshotInterval <= 0 {
		cfg.Flight.SnapshotInterval = 30 * time.Second
	}
	if cfg.Flight.MinAge <= 0 {
		cfg.Flight.MinAge = 20 * time.Second
	}
	if cfg.Flight.MaxBytes == 0 {
		cfg.Flight.MaxBytes = 16 << 20
	}
	if cfg.Flight.KeepSnapshots <= 0 {
		cfg.Flight.KeepSnapshots = 2
	}
	return cfg, nil
}

func newManager(cfg Config) (*Manager, error) {
	cfg, err := normalizeConfig(cfg)
	if err != nil {
		return nil, err
	}
	root := filepath.Join(cfg.AppDataPath, "diagnostics")
	runsDir := filepath.Join(root, "runs")
	exportsDir := filepath.Join(root, "exports")
	for _, dir := range []string{root, runsDir, exportsDir} {
		if err = os.MkdirAll(dir, 0o700); err != nil {
			return nil, err
		}
		if err = ensureDirectoryNoSymlink(dir); err != nil {
			return nil, err
		}
	}
	// 导出副本不是调查证据本体。正常 HTTP 请求会在发送后删除；这里只
	// 清理异常退出遗留且已超过保护窗口的副本，避免 exports 无限增长。
	_ = cleanupStaleExports(exportsDir, time.Now())

	existing, err := scanRunInfos(runsDir, "")
	if err != nil {
		return nil, err
	}
	existing = pruneEligibleRuns(existing, cfg.MaxRuns)

	runID, runDir, err := createUniqueRunDir(runsDir)
	if err != nil {
		return nil, err
	}
	ownerID, err := randomHex(16)
	if err != nil {
		return nil, err
	}
	ownerPID := os.Getpid()
	ownerObservation, ownerErr := observeProcess(ownerPID)
	scopeKey := make([]byte, 32)
	if _, err = cryptoRead(scopeKey); err != nil {
		return nil, err
	}
	startedAt := time.Now()
	m := &Manager{
		cfg:           cfg,
		root:          root,
		runsDir:       runsDir,
		exportsDir:    exportsDir,
		runDir:        runDir,
		runID:         runID,
		startedAt:     startedAt,
		scopeKey:      scopeKey,
		ownerID:       ownerID,
		ownerPID:      ownerPID,
		leaseDuration: runLeaseDuration(cfg.HeartbeatInterval),
		exportGate:    make(chan struct{}, 1),
		closeDone:     make(chan struct{}),
		heartbeatStop: make(chan struct{}),
		heartbeatDone: make(chan struct{}),
	}
	m.exportGate <- struct{}{}
	if ownerErr == nil && ownerObservation.Exists {
		m.ownerStartedAtMS = ownerObservation.StartedAtMS
	}
	// 先取得内核持有的文件锁，再发布 lease.json。这样即使 owner 被
	// SIGKILL，其他实例也能立即通过“锁已释放”识别陈旧 run，而不必等
	// heartbeat 超时。文件系统不支持锁时仍保留 PID 身份与有期限租约。
	m.leaseLock, err = acquireLeaseFileLock(filepath.Join(runDir, runLeaseLockFileName))
	if err != nil && !errors.Is(err, ErrRunActive) {
		m.setError(err)
		m.leaseLock = nil
	} else if err != nil {
		return nil, err
	}
	if err = m.writeLease("starting"); err != nil {
		_ = m.leaseLock.close()
		return nil, err
	}
	m.startup = buildStartupReport(existing, runID)
	m.manifest = RunManifest{
		Schema:        RunSchema,
		RunID:         runID,
		StartedAt:     startedAt.UTC(),
		State:         "starting",
		PID:           os.Getpid(),
		AppVersion:    cfg.AppVersion,
		Commit:        cfg.Commit,
		GoVersion:     runtime.Version(),
		OS:            runtime.GOOS,
		Arch:          runtime.GOARCH,
		TraceMode:     cfg.TraceMode,
		Configuration: sanitizeMap(m, cfg.Configuration),
		FlightEnabled: cfg.Flight.Enabled,
	}
	if err = atomicWriteJSON(filepath.Join(runDir, "run.json"), m.manifest, false); err != nil {
		_ = m.releaseLease("aborted")
		return nil, err
	}

	m.writer, err = newEventWriter(
		filepath.Join(runDir, "events"),
		cfg.EventSegmentBytes,
		cfg.MaxEventSegments,
		cfg.EventSyncInterval,
	)
	if err != nil {
		_ = m.releaseLease("aborted")
		return nil, err
	}
	m.flight = newFlightRecorder(filepath.Join(runDir, "flight"), cfg.Flight)
	m.flight.onError = m.setError
	if cfg.Flight.Enabled {
		if startErr := m.flight.Start(); startErr != nil {
			m.manifest.FlightEnabled = false
			m.manifest.FlightError = startErr.Error()
			m.setError(startErr)
		}
	}
	m.manifest.State = "running"
	if err = m.persistManifest(); err != nil {
		_ = m.writer.Close()
		m.flight.Stop()
		_ = m.releaseLease("aborted")
		return nil, err
	}
	m.recordRuntimeSample()
	if err = m.writeHeartbeat("running"); err != nil {
		_ = m.writer.Close()
		m.flight.Stop()
		_ = m.releaseLease("aborted")
		return nil, err
	}
	go m.heartbeatLoop()
	m.flight.StartPeriodic()
	return m, nil
}

func createUniqueRunDir(runsDir string) (string, string, error) {
	now := time.Now().UTC()
	for i := 0; i < 32; i++ {
		random, err := randomHex(10)
		if err != nil {
			return "", "", err
		}
		runID := fmt.Sprintf("run-%s-p%d-%s", now.Format("20060102T150405.000000000Z"), os.Getpid(), random)
		dir := filepath.Join(runsDir, runID)
		if err = os.Mkdir(dir, 0o700); err == nil {
			if syncErr := syncDir(runsDir); syncErr != nil {
				return "", "", syncErr
			}
			return runID, dir, nil
		} else if !os.IsExist(err) {
			return "", "", err
		}
	}
	return "", "", fmt.Errorf("无法创建唯一 diagnostics run")
}

func buildStartupReport(existing []RunInfo, currentID string) StartupReport {
	report := StartupReport{
		CurrentRunID: currentID,
		ActiveRuns:   make([]RunInfo, 0),
		UncleanRuns:  make([]RunInfo, 0),
	}
	if len(existing) > 0 {
		previous := existing[0]
		report.PreviousRun = &previous
	}
	for _, info := range existing {
		if info.Active {
			report.ActiveRuns = append(report.ActiveRuns, info)
		} else if !info.Clean {
			report.UncleanRuns = append(report.UncleanRuns, info)
		}
	}
	return report
}

func (m *Manager) heartbeatLoop() {
	defer close(m.heartbeatDone)
	ticker := time.NewTicker(m.cfg.HeartbeatInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			m.recordRuntimeSample()
			m.eventMu.Lock()
			if m.writer != nil {
				if err := m.writer.Sync(); err != nil {
					m.setError(err)
				}
				m.lastSeq.Store(m.writer.latestSeq)
				m.dropped.Store(m.writer.dropped)
			}
			m.eventMu.Unlock()
			if err := m.writeHeartbeat(snapshotState(m)); err != nil {
				m.setError(err)
			}
		case <-m.heartbeatStop:
			return
		}
	}
}

func (m *Manager) writeHeartbeat(state string) error {
	m.livenessMu.Lock()
	defer m.livenessMu.Unlock()
	if leaseStateIsActive(state) {
		// 调用方可能在终态切换前算出了旧 state；锁内重新读取，确保
		// closed/aborted/panicked heartbeat 不会再被 stale running 覆盖。
		state = snapshotState(m)
	}
	err := atomicWriteJSON(filepath.Join(m.runDir, "heartbeat.json"), heartbeatFile{
		Schema:        "bililive.diagnostics-heartbeat/v1",
		RunID:         m.runID,
		At:            time.Now().UTC(),
		State:         state,
		LatestSeq:     m.lastSeq.Load(),
		DroppedEvents: m.dropped.Load(),
		Panicked:      m.panicked.Load(),
	}, true)
	if err != nil {
		return err
	}
	if leaseStateIsActive(state) {
		return m.writeLease(state)
	}
	return nil
}

func (m *Manager) persistManifest() error {
	m.manifest.DroppedEvents = m.dropped.Load()
	return atomicWriteJSON(filepath.Join(m.runDir, "run.json"), m.manifest, true)
}

func (m *Manager) setError(err error) {
	if err == nil {
		return
	}
	m.errMu.Lock()
	m.lastErr = err
	m.errMu.Unlock()
}

// Err 返回最近一次后台持久化错误。
func (m *Manager) Err() error {
	if m == nil {
		return ErrNotInitialized
	}
	m.errMu.Lock()
	defer m.errMu.Unlock()
	return m.lastErr
}

// RunID 返回当前 run ID。
func (m *Manager) RunID() string {
	if m == nil {
		return ""
	}
	return m.runID
}

// StartupStatus 返回 Init 时识别出的既有运行状态。
func (m *Manager) StartupStatus() StartupReport {
	if m == nil {
		return StartupReport{}
	}
	m.startupMu.RLock()
	defer m.startupMu.RUnlock()
	result := m.startup
	result.ActiveRuns = append([]RunInfo(nil), m.startup.ActiveRuns...)
	result.UncleanRuns = append([]RunInfo(nil), m.startup.UncleanRuns...)
	if m.startup.PreviousRun != nil {
		previous := *m.startup.PreviousRun
		result.PreviousRun = &previous
	}
	return result
}

// SnapshotCurrent 立即 fsync 事件和 heartbeat，并在启用 Flight Recorder 时发布新快照。
func (m *Manager) SnapshotCurrent() (Snapshot, error) {
	if m == nil {
		return Snapshot{}, ErrNotInitialized
	}
	m.eventMu.Lock()
	if m.writer != nil {
		if err := m.writer.Sync(); err != nil {
			m.eventMu.Unlock()
			return Snapshot{}, err
		}
		m.lastSeq.Store(m.writer.latestSeq)
		m.dropped.Store(m.writer.dropped)
	}
	m.eventMu.Unlock()
	if err := m.writeHeartbeat(snapshotState(m)); err != nil {
		return Snapshot{}, err
	}
	snapshot := Snapshot{
		RunID:          m.runID,
		CapturedAt:     time.Now().UTC(),
		LatestEventSeq: m.lastSeq.Load(),
		DroppedEvents:  m.dropped.Load(),
	}
	artifact, err := m.flight.Snapshot()
	if err != nil {
		return snapshot, err
	}
	snapshot.Flight = artifact
	return snapshot, nil
}

func snapshotState(m *Manager) string {
	if m.panicked.Load() {
		return "panicked"
	}
	if m.closed.Load() {
		if m.cleanExit.Load() {
			return "closed"
		}
		return "aborted"
	}
	if m.stopping.Load() {
		return "stopping"
	}
	return "running"
}

// Close 停止后台写入并在最后写 clean.json。若此前记录过 panic，Close 会自动
// 降级为 Abort，绝不会把 panic 运行误标为正常关闭。
func (m *Manager) Close() error {
	return m.shutdown(true)
}

// Abort 停止后台写入但不写 clean.json，用于启动失败或未完成统一 shutdown 的路径。
func (m *Manager) Abort() error {
	return m.shutdown(false)
}

func (m *Manager) shutdown(requestClean bool) error {
	if m == nil {
		return ErrNotInitialized
	}
	m.closeOnce.Do(func() {
		m.stopping.Store(true)
		close(m.heartbeatStop)
		m.flight.StopPeriodic()
		<-m.heartbeatDone

		// 停止 recorder 前保留最后一个稳定 Flight 快照；失败不会删除旧快照。
		if _, err := m.flight.Snapshot(); err != nil {
			m.setError(err)
		}
		m.flight.Stop()

		m.eventMu.Lock()
		if m.writer != nil {
			if err := m.writer.Close(); err != nil && m.closeErr == nil {
				m.closeErr = err
			}
			m.lastSeq.Store(m.writer.latestSeq)
			m.dropped.Store(m.writer.dropped)
			m.writer = nil
		}
		m.eventMu.Unlock()

		// 等待已经取得读锁的 RecordPanic 把 marker 写完。尚未取得读锁但已经
		// 发布 panicked 的调用会阻塞在 gate，下面仍能据此拒绝 clean。
		// 锁序固定为 panicGate -> terminalMu。
		m.panicGate.Lock()
		m.terminalMu.Lock()
		clean := requestClean && !m.panicked.Load() && m.closeErr == nil
		now := time.Now().UTC()
		m.manifest.EndedAt = &now
		if clean {
			m.manifest.State = "closed"
		} else if m.panicked.Load() {
			m.manifest.State = "panicked"
		} else {
			m.manifest.State = "aborted"
		}
		if err := m.persistManifest(); err != nil && m.closeErr == nil {
			m.closeErr = err
			clean = false
		}
		state := m.manifest.State
		if err := m.writeHeartbeat(state); err != nil && m.closeErr == nil {
			m.closeErr = err
			clean = false
		}
		cleanPath := filepath.Join(m.runDir, "clean.json")
		// panicked 可以在 panicGate 写锁持有期间由 RecordPanic 无锁发布。
		// 在真正发布 marker 前重新读取，避免沿用临界区开头的陈旧判断。
		if clean && m.panicked.Load() {
			clean = false
		}
		if clean {
			marker := cleanMarker{
				Schema:    "bililive.diagnostics-clean/v1",
				RunID:     m.runID,
				CreatedAt: now,
				FinalSeq:  m.lastSeq.Load(),
			}
			if err := atomicWriteJSON(cleanPath, marker, false); err != nil {
				// 硬链接/rename 已经原子发布 marker 后，清理临时文件或
				// fsync 目录仍可能报错。此时只要目标文件可完整读回，它就是
				// 本次终态的稳定可见证据；不能一边留下 clean.json，一边又
				// 把内存和 manifest 改成 aborted，导致下次启动误判。
				if cleanMarkerPublishSucceeded(cleanPath, marker, err) {
					m.setError(err)
				} else {
					m.closeErr = err
					clean = false
				}
			}
		}
		revokeLateClean := func() {
			if !clean || !m.panicked.Load() {
				return
			}
			clean = false
			if err := os.Remove(cleanPath); err != nil && !os.IsNotExist(err) {
				m.setError(err)
				if m.closeErr == nil {
					m.closeErr = err
				}
			} else if err == nil {
				if syncErr := syncDir(m.runDir); syncErr != nil {
					m.setError(syncErr)
					if m.closeErr == nil {
						m.closeErr = syncErr
					}
				}
			}
		}
		// RecordPanic 可能恰好在 atomicWriteJSON 期间进入；发布后再检查一次，
		// 已写出的 clean 会被立刻撤销。
		revokeLateClean()
		normalizeNonCleanState := func() {
			// clean marker 才是正常退出的最终判据。如果此前任一步失败，把
			// 仅在内存中预设的 closed 状态也纠正为 aborted/panicked。
			if !clean && m.manifest.State == "closed" {
				if m.panicked.Load() {
					m.manifest.State = "panicked"
				} else {
					m.manifest.State = "aborted"
				}
				if err := m.persistManifest(); err != nil && m.closeErr == nil {
					m.closeErr = err
				}
				if err := m.writeHeartbeat(m.manifest.State); err != nil && m.closeErr == nil {
					m.closeErr = err
				}
			}
		}
		normalizeNonCleanState()
		// 把终态发布前的最后一个无锁 panic intent 也纳入判断。此处之后即使
		// 又有 panic 进入，RecordPanic 仍会在 Close 解锁后按“panic after
		// close”路径撤销 marker。
		revokeLateClean()
		normalizeNonCleanState()
		m.cleanExit.Store(clean)
		m.closed.Store(true)
		// closed 标志发布后再写一次最终 heartbeat。任何与 shutdown 并发
		// 的 Snapshot 都会在同一个 livenessMu 边界上看到相同终态。
		if err := m.writeHeartbeat(snapshotState(m)); err != nil {
			m.setError(err)
			if m.closeErr == nil {
				m.closeErr = err
			}
		}
		leaseState := m.manifest.State
		if leaseState == "panicked" {
			leaseState = "panicked_closed"
		}
		if err := m.releaseLease(leaseState); err != nil {
			m.setError(err)
		}
		m.terminalMu.Unlock()
		m.panicGate.Unlock()
		close(m.closeDone)
	})
	<-m.closeDone
	return m.closeErr
}

// RecordPanic 将 panic 值和所有 goroutine 栈写入不可覆盖的 marker。
// 记录成功与否都把本 run 标为异常，后续 Close 不会产生 clean marker。
func (m *Manager) RecordPanic(ctx context.Context, recovered any) error {
	if m == nil {
		return ErrNotInitialized
	}
	if recovered == nil {
		return nil
	}
	// 必须在任何锁之前发布 panicked。否则 Close 若正持有 panicGate 或
	// terminalMu，可能先写 clean 并返回，主 goroutine 随即退出，使 panic
	// hook 永远没有机会撤销错误的 clean 结论。
	if m.panicked.Swap(true) {
		return nil
	}
	m.panicGate.RLock()
	defer m.panicGate.RUnlock()
	m.terminalMu.Lock()
	defer m.terminalMu.Unlock()

	// Close 可能刚刚已经发布 clean marker。panic 的证据优先级更高：先删
	// clean 并 fsync 目录，使任何随后发生的崩溃都至少表现为 unclean，
	// 而不会留下“clean + panic”的错误正常退出结论。
	var result error
	cleanPath := filepath.Join(m.runDir, "clean.json")
	if err := os.Remove(cleanPath); err != nil && !os.IsNotExist(err) {
		m.setError(err)
		result = err
	} else if err == nil {
		if syncErr := syncDir(m.runDir); syncErr != nil {
			m.setError(syncErr)
			result = syncErr
		}
	}

	m.Record(ctx, "diagnostics.panic", Fields{
		"component":  "runtime",
		"lane":       "Runtime",
		"severity":   "error",
		"status":     "panic",
		"panic_type": fmt.Sprintf("%T", recovered),
		"panic":      fmt.Sprint(recovered),
	})

	stack := captureAllStacks()
	m.markerMu.Lock()
	stackPath := filepath.Join(m.runDir, "panic.stack")
	if err := atomicWriteBytes(stackPath, stack, false); err != nil && !os.IsExist(err) {
		m.setError(err)
		if result == nil {
			result = err
		}
	}
	marker := panicMarker{
		Schema:    "bililive.diagnostics-panic/v1",
		RunID:     m.runID,
		CreatedAt: time.Now().UTC(),
		Value:     sanitizeString(m, "panic", fmt.Sprint(recovered)),
		ValueType: fmt.Sprintf("%T", recovered),
		EventSeq:  m.lastSeq.Load(),
		StackFile: filepath.Base(stackPath),
	}
	if err := atomicWriteJSON(filepath.Join(m.runDir, "panic.json"), marker, false); err != nil && !os.IsExist(err) {
		m.setError(err)
		if result == nil {
			result = err
		}
	}
	m.markerMu.Unlock()
	if _, err := m.SnapshotCurrent(); err != nil {
		m.setError(err)
		if result == nil {
			result = err
		}
	}

	// RecordPanic 也允许在 Close 返回后调用（例如退出边界上的并发 panic）。
	// 此时重写终态并发布 panicked heartbeat/lease，保证下次启动不会继续把
	// 该 run 当作 clean。
	if m.closed.Load() {
		now := time.Now().UTC()
		m.manifest.EndedAt = &now
		m.manifest.State = "panicked"
		if err := m.persistManifest(); err != nil {
			m.setError(err)
			if result == nil {
				result = err
			}
		}
		if err := m.writeHeartbeat("panicked"); err != nil {
			m.setError(err)
			if result == nil {
				result = err
			}
		}
		if err := m.releaseLease("panicked_closed"); err != nil {
			m.setError(err)
			if result == nil {
				result = err
			}
		}
		m.cleanExit.Store(false)
	}
	return result
}

func captureAllStacks() []byte {
	size := 256 << 10
	for size <= 16<<20 {
		buf := make([]byte, size)
		n := runtime.Stack(buf, true)
		if n < len(buf) {
			current := debug.Stack()
			return append(append([]byte("current goroutine:\n"), current...), append([]byte("\nall goroutines:\n"), buf[:n]...)...)
		}
		size *= 2
	}
	return debug.Stack()
}

func readManifest(path string) (RunManifest, error) {
	var manifest RunManifest
	data, err := readRegularFileNoSymlink(path)
	if err != nil {
		return manifest, err
	}
	err = json.Unmarshal(data, &manifest)
	return manifest, err
}

func readCleanMarker(path string) (cleanMarker, bool) {
	var marker cleanMarker
	data, err := readRegularFileNoSymlink(path)
	if err != nil || json.Unmarshal(data, &marker) != nil {
		return marker, false
	}
	if marker.Schema != "bililive.diagnostics-clean/v1" ||
		marker.RunID == "" ||
		marker.CreatedAt.IsZero() {
		return marker, false
	}
	return marker, true
}

func cleanMarkerMatches(path string, expected cleanMarker) bool {
	actual, ok := readCleanMarker(path)
	return ok &&
		actual.Schema == expected.Schema &&
		actual.RunID == expected.RunID &&
		actual.CreatedAt.Equal(expected.CreatedAt) &&
		actual.FinalSeq == expected.FinalSeq
}

func cleanMarkerPublishSucceeded(path string, expected cleanMarker, writeErr error) bool {
	return writeErr == nil || cleanMarkerMatches(path, expected)
}
