package diagnostics

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func testConfig(appDataPath string) Config {
	return Config{
		AppDataPath:       appDataPath,
		AppVersion:        "test-version",
		Commit:            "test-commit",
		HeartbeatInterval: time.Hour,
		EventSyncInterval: time.Hour,
		EventSegmentBytes: 8 << 10,
		MaxEventSegments:  4,
		MaxRuns:           20,
		Configuration: map[string]any{
			"room_check_interval_ms": 20_000,
			"authorization":          "不应写盘",
		},
		Flight: FlightConfig{Enabled: false},
	}
}

func initTestManager(t *testing.T, cfg Config) *Manager {
	t.Helper()
	manager, err := Init(cfg)
	require.NoError(t, err)
	t.Cleanup(func() {
		if !manager.closed.Load() {
			_ = manager.Abort()
		}
	})
	return manager
}

func TestUncleanRestartPreservesEvidenceAndAcknowledgement(t *testing.T) {
	appDataPath := t.TempDir()
	first := initTestManager(t, testConfig(appDataPath))
	firstRunID := first.RunID()
	first.Record(context.Background(), "monitor.started", Fields{
		"component":     "listener",
		"lane":          "直播检测",
		"room_scope_id": first.ScopeID("room-123"),
		"flow_id":       first.NewID("flow"),
		"message":       "第一次运行证据",
	})
	require.NoError(t, first.Abort())

	oldEventFiles, err := eventSegmentPaths(filepath.Join(first.runDir, "events"))
	require.NoError(t, err)
	require.NotEmpty(t, oldEventFiles)
	oldEventBytes, err := os.ReadFile(oldEventFiles[0])
	require.NoError(t, err)

	second := initTestManager(t, testConfig(appDataPath))
	report := second.StartupStatus()
	require.NotNil(t, report.PreviousRun)
	require.Equal(t, firstRunID, report.PreviousRun.RunID)
	require.False(t, report.PreviousRun.Clean)
	require.False(t, report.PreviousRun.Acknowledged)
	require.Condition(t, func() bool {
		for _, run := range report.UncleanRuns {
			if run.RunID == firstRunID {
				return true
			}
		}
		return false
	})

	bundleArtifact, err := second.BuildViewerBundle(firstRunID)
	require.NoError(t, err)
	bundleData, err := os.ReadFile(bundleArtifact.Path)
	require.NoError(t, err)
	var bundle viewerBundle
	require.NoError(t, json.Unmarshal(bundleData, &bundle))
	require.Equal(t, BundleSchema, bundle.Schema)
	require.Condition(t, func() bool {
		for _, event := range bundle.Events {
			if event.Name == "monitor.started" && event.Attrs["message"] == "第一次运行证据" {
				return true
			}
		}
		return false
	})

	require.NoError(t, second.Acknowledge(firstRunID))
	ackBefore, err := os.ReadFile(filepath.Join(first.runDir, "ack.json"))
	require.NoError(t, err)
	require.NoError(t, second.Acknowledge(firstRunID))
	ackAfter, err := os.ReadFile(filepath.Join(first.runDir, "ack.json"))
	require.NoError(t, err)
	require.Equal(t, ackBefore, ackAfter, "重复 ACK 不应覆盖第一次确认时间")
	require.True(t, second.StartupStatus().PreviousRun.Acknowledged)

	oldEventBytesAfter, err := os.ReadFile(oldEventFiles[0])
	require.NoError(t, err)
	require.Equal(t, oldEventBytes, oldEventBytesAfter, "重启和 ACK 都不能改写旧事件")
}

func TestRunAndEvidenceSymlinksCannotEscapeDiagnosticsRoot(t *testing.T) {
	manager := newIsolatedManager(t, testConfig(t.TempDir()))
	outside := t.TempDir()

	runLinkID := "run-symlink-outside"
	if err := os.Symlink(outside, filepath.Join(manager.runsDir, runLinkID)); err != nil {
		t.Skipf("当前平台不能创建符号链接: %v", err)
	}
	_, err := manager.runPath(runLinkID)
	require.ErrorIs(t, err, ErrRunNotFound,
		"有效格式的 run ID 也不能通过目录符号链接跳出 runs 根目录")

	nestedRunID := "run-nested-evidence-symlink"
	nestedRunDir := filepath.Join(manager.runsDir, nestedRunID)
	require.NoError(t, os.Mkdir(nestedRunDir, 0o700))
	require.NoError(t, atomicWriteJSON(filepath.Join(nestedRunDir, "run.json"), RunManifest{
		Schema:    RunSchema,
		RunID:     nestedRunID,
		StartedAt: time.Now().UTC(),
		State:     "aborted",
	}, false))
	externalEvents := filepath.Join(outside, "events")
	require.NoError(t, os.Mkdir(externalEvents, 0o700))
	require.NoError(t, os.WriteFile(
		filepath.Join(externalEvents, "events-000001.jsonl"),
		[]byte(`{"seq":1,"name":"outside-secret"}`+"\n"),
		0o600,
	))
	require.NoError(t, os.Symlink(externalEvents, filepath.Join(nestedRunDir, "events")))

	_, err = manager.BuildViewerBundle(nestedRunID)
	require.Error(t, err, "Viewer 不得跟随 run 内的 events 目录符号链接")
}

func TestInvalidAcknowledgeMarkerCannotAuthorizeEvidencePruning(t *testing.T) {
	manager := newIsolatedManager(t, testConfig(t.TempDir()))
	require.NoError(t, manager.Abort())
	ackPath := filepath.Join(manager.runDir, "ack.json")
	require.NoError(t, os.WriteFile(ackPath, []byte("{broken"), 0o600))

	info := findRunInfo(t, manager, manager.RunID())
	require.False(t, info.Acknowledged, "损坏的 ack marker 不能视为用户确认")
	require.Error(t, manager.Acknowledge(manager.RunID()),
		"不可覆盖 marker 必须拒绝覆盖损坏的既有 ack")
	require.Equal(t, "{broken", string(requireReadFile(t, ackPath)),
		"拒绝 ACK 时不能改写原始现场")
}

func requireReadFile(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	return data
}

func TestCleanShutdownIsNotReportedAsUnclean(t *testing.T) {
	appDataPath := t.TempDir()
	first := initTestManager(t, testConfig(appDataPath))
	firstRunID := first.RunID()
	first.Record(context.Background(), "application.ready", Fields{"component": "main"})
	require.NoError(t, first.Close())
	require.FileExists(t, filepath.Join(first.runDir, "clean.json"))

	second := initTestManager(t, testConfig(appDataPath))
	report := second.StartupStatus()
	require.NotNil(t, report.PreviousRun)
	require.Equal(t, firstRunID, report.PreviousRun.RunID)
	require.True(t, report.PreviousRun.Clean)
	for _, run := range report.UncleanRuns {
		require.NotEqual(t, firstRunID, run.RunID)
	}
}

func TestRunIDsAreUniqueAndUnacknowledgedRunsAreNotPruned(t *testing.T) {
	appDataPath := t.TempDir()
	cfg := testConfig(appDataPath)
	cfg.MaxRuns = 2
	runIDs := map[string]struct{}{}
	for i := 0; i < 6; i++ {
		manager := initTestManager(t, cfg)
		_, duplicate := runIDs[manager.RunID()]
		require.False(t, duplicate)
		runIDs[manager.RunID()] = struct{}{}
		manager.Record(context.Background(), "test.event", Fields{"iteration": i})
		require.NoError(t, manager.Abort())
	}
	require.Len(t, runIDs, 6)
	for runID := range runIDs {
		require.DirExists(t, filepath.Join(appDataPath, "diagnostics", "runs", runID),
			"未 ACK 的异常 run 即使超过 MaxRuns 也不得自动删除")
	}
}

func TestEventRotationAndAtomicFilesNeverOverwrite(t *testing.T) {
	eventDir := filepath.Join(t.TempDir(), "events")
	writer, err := newEventWriter(eventDir, 300, 2, time.Hour)
	require.NoError(t, err)
	t.Cleanup(func() { _ = writer.Close() })

	first := &Event{
		ID: "evt_first", Name: "first", Component: "test", Lane: "test",
		Severity: "info", Attrs: map[string]any{"payload": strings.Repeat("a", 400)},
	}
	require.NoError(t, writer.Write(first))
	nextPath := filepath.Join(eventDir, "events-000002.jsonl")
	require.NoError(t, os.WriteFile(nextPath, []byte("不可覆盖"), 0o600))
	second := &Event{
		ID: "evt_second", Name: "second", Component: "test", Lane: "test",
		Severity: "info", Attrs: map[string]any{"payload": strings.Repeat("b", 400)},
	}
	require.Error(t, writer.Write(second))
	sentinel, err := os.ReadFile(nextPath)
	require.NoError(t, err)
	require.Equal(t, "不可覆盖", string(sentinel))

	marker := filepath.Join(t.TempDir(), "marker.json")
	require.NoError(t, atomicWriteBytes(marker, []byte("first"), false))
	require.Error(t, atomicWriteBytes(marker, []byte("second"), false))
	markerData, err := os.ReadFile(marker)
	require.NoError(t, err)
	require.Equal(t, "first", string(markerData))
}

func TestStaleExportCleanupOnlyRemovesManagedDisposableFiles(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	stale := filepath.Join(dir, "bililive-go-diagnostics-run-old.tar.gz")
	staleTemp := filepath.Join(dir, ".bililive-go-viewer-run-old.json.tmp-dead")
	young := filepath.Join(dir, "bililive-go-logs-current.tar.gz")
	unrelated := filepath.Join(dir, "用户保留的调查说明.txt")
	for _, path := range []string{stale, staleTemp, young, unrelated} {
		require.NoError(t, os.WriteFile(path, []byte(filepath.Base(path)), 0o600))
	}
	old := now.Add(-staleExportRetention - time.Minute)
	require.NoError(t, os.Chtimes(stale, old, old))
	require.NoError(t, os.Chtimes(staleTemp, old, old))
	require.NoError(t, os.Chtimes(unrelated, old, old))

	require.NoError(t, cleanupStaleExports(dir, now))
	require.NoFileExists(t, stale)
	require.NoFileExists(t, staleTemp)
	require.FileExists(t, young)
	require.FileExists(t, unrelated, "清理器不得删除非 diagnostics 管理的文件")
}

func TestCleanMarkerPostPublishErrorKeepsOneConsistentTerminalResult(t *testing.T) {
	path := filepath.Join(t.TempDir(), "clean.json")
	expected := cleanMarker{
		Schema:    "bililive.diagnostics-clean/v1",
		RunID:     "run-clean-marker-test",
		CreatedAt: time.Now().UTC(),
		FinalSeq:  42,
	}
	require.NoError(t, atomicWriteJSON(path, expected, false))

	// 模拟 Link/Rename 已经发布目标文件，但随后删除临时文件或 fsync 目录
	// 报错。只要目标能完整读回，就不能再把同一 run 改判为 aborted。
	require.True(t, cleanMarkerPublishSucceeded(
		path,
		expected,
		errors.New("模拟发布后的目录同步失败"),
	))

	wrong := expected
	wrong.FinalSeq++
	require.False(t, cleanMarkerPublishSucceeded(
		path,
		wrong,
		errors.New("目标内容不属于本次终态"),
	))

	require.NoError(t, os.WriteFile(path, []byte("{broken"), 0o600))
	_, ok := readCleanMarker(path)
	require.False(t, ok, "损坏的 clean marker 不能证明正常退出")
}

func TestSegmentRotationLimitsCurrentRunDiskAndKeepsValidJSONL(t *testing.T) {
	cfg := testConfig(t.TempDir())
	cfg.EventSegmentBytes = 900
	cfg.MaxEventSegments = 2
	manager := initTestManager(t, cfg)
	for i := 0; i < 80; i++ {
		manager.Record(context.Background(), "rotation.event", Fields{
			"component": "test",
			"index":     i,
			"payload":   strings.Repeat("x", 160),
		})
	}
	_, err := manager.SnapshotCurrent()
	require.NoError(t, err)
	paths, err := eventSegmentPaths(filepath.Join(manager.runDir, "events"))
	require.NoError(t, err)
	require.LessOrEqual(t, len(paths), 2)
	index := readEventIndex(filepath.Join(manager.runDir, "events"))
	require.Greater(t, index.DroppedEvents, uint64(0))

	loaded, err := loadEvents(manager.runDir)
	require.NoError(t, err)
	require.NotEmpty(t, loaded.Events)
	for _, event := range loaded.Events {
		require.NotZero(t, event.Seq)
		require.NotEmpty(t, event.ID)
	}
	require.NoError(t, filepath.WalkDir(manager.runDir, func(path string, entry os.DirEntry, walkErr error) error {
		require.NoError(t, walkErr)
		require.NotContains(t, entry.Name(), ".tmp-", "原子写入完成后不得遗留临时文件")
		return nil
	}))
}

func TestViewerBundleAndArchiveAreReadableAndRedacted(t *testing.T) {
	cfg := testConfig(t.TempDir())
	manager := initTestManager(t, cfg)
	rawURL := "https://live.example.invalid/play.flv?token=very-secret"
	roomID := manager.ScopeID("raw-room-9527")
	flowID := manager.NewID("flow")
	ctx := WithFields(context.Background(), Fields{
		"component":     "listener",
		"lane":          "直播检测",
		"room_scope_id": roomID,
		"flow_id":       flowID,
	})
	manager.Record(ctx, "monitor.started", Fields{
		"configured_interval_ms": 20_000,
		"raw_url":                rawURL,
	})
	spanCtx, end := manager.StartSpan(ctx, "listener.poll", Fields{"severity": "debug"})
	manager.Record(spanCtx, "listener.poll.observed", Fields{"live": true})
	end(Fields{"status": "ok", "live": true})
	manager.Record(ctx, "recorder.session.start", Fields{"status": "accepted", "session_id": "session_test"})
	manager.Record(ctx, "segment.first_byte", Fields{"size_bytes": 64 << 10})

	bundleArtifact, err := manager.BuildViewerBundle(manager.RunID())
	require.NoError(t, err)
	bundleData, err := os.ReadFile(bundleArtifact.Path)
	require.NoError(t, err)
	require.NotContains(t, string(bundleData), rawURL)
	require.NotContains(t, string(bundleData), "very-secret")
	require.NotContains(t, string(bundleData), "不应写盘")
	require.Contains(t, string(bundleData), "scope_")
	var bundle map[string]any
	require.NoError(t, json.Unmarshal(bundleData, &bundle))
	require.Equal(t, BundleSchema, bundle["schema"])
	require.IsType(t, []any{}, bundle["events"])
	require.IsType(t, []any{}, bundle["metrics"])
	require.IsType(t, []any{}, bundle["runtime_samples"])

	archiveArtifact, err := manager.BuildArchive(manager.RunID())
	require.NoError(t, err)
	entries := readArchiveEntries(t, archiveArtifact.Path)
	require.Contains(t, entries, "viewer.json")
	require.Contains(t, entries, "bundle.json")
	require.Contains(t, entries, "run/run.json")
	require.Condition(t, func() bool {
		for name := range entries {
			if strings.HasPrefix(name, "run/events/events-") && strings.HasSuffix(name, ".jsonl") {
				return true
			}
		}
		return false
	})
	var archivedBundle map[string]any
	require.NoError(t, json.Unmarshal(entries["viewer.json"], &archivedBundle))
	require.Equal(t, BundleSchema, archivedBundle["schema"])
}

func TestLiveIDAttributesAreScopedBeforeWriting(t *testing.T) {
	manager := initTestManager(t, testConfig(t.TempDir()))
	manager.Record(context.Background(), "privacy.live_id", Fields{
		"component":   "test",
		"live_id":     "raw-live-9527",
		"old_live_id": "raw-live-old",
		"new_live_id": "raw-live-new",
		// 已显式命名为 scope 的字段不得被重复 HMAC。
		"live_id_scope": manager.ScopeID("already-scoped"),
	})
	_, err := manager.SnapshotCurrent()
	require.NoError(t, err)

	loaded, err := loadEvents(manager.runDir)
	require.NoError(t, err)
	var attrs map[string]any
	for _, event := range loaded.Events {
		if event.Name == "privacy.live_id" {
			attrs = event.Attrs
			break
		}
	}
	require.NotNil(t, attrs)
	require.NotEqual(t, "raw-live-9527", attrs["live_id"])
	require.NotEqual(t, "raw-live-old", attrs["old_live_id"])
	require.NotEqual(t, "raw-live-new", attrs["new_live_id"])
	require.Contains(t, attrs["live_id"], "scope_")
	require.Contains(t, attrs["old_live_id"], "scope_")
	require.Contains(t, attrs["new_live_id"], "scope_")
	require.Equal(t, manager.ScopeID("already-scoped"), attrs["live_id_scope"])
}

func TestViewerMetricsAreDerivedFromRealBusinessAndRuntimeEvents(t *testing.T) {
	metrics := deriveViewerMetrics([]Event{
		{TS: 1, Name: "scheduler.rate_limit.in_flight.wait.start", Attrs: map[string]any{}},
		{TS: 2, Name: "scheduler.rate_limit.in_flight.wait.start", Attrs: map[string]any{}},
		{TS: 3, Name: "scheduler.rate_limit.in_flight.wait.end", Attrs: map[string]any{}},
		{TS: 4, Name: "scheduler.rate_limit.wait.start", Attrs: map[string]any{}},
		{TS: 5, Name: "listener.poll.start", Attrs: map[string]any{}},
		{TS: 6, Name: "runtime.sample", Attrs: map[string]any{
			"goroutines":       float64(123),
			"heap_alloc_bytes": float64(456),
		}},
		{TS: 7, Name: "scheduler.rate_limit.wait.end", Attrs: map[string]any{}},
		{TS: 8, Name: "scheduler.rate_limit.in_flight.wait.end", Attrs: map[string]any{}},
		{TS: 9, Name: "listener.poll.end", Attrs: map[string]any{}},
	})
	encoded, err := json.Marshal(metrics)
	require.NoError(t, err)
	text := string(encoded)
	require.Contains(t, text, "platform.rate_limiter.waiting_rooms")
	require.Contains(t, text, "platform.rate_limiter.in_flight_waiting_rooms")
	require.Contains(t, text, "scheduler.poll.in_flight")
	require.Contains(t, text, "runtime.goroutines")
	require.Contains(t, text, "runtime.heap_alloc_bytes")
	require.Contains(t, text, `"name":"platform.rate_limiter.waiting_rooms"`)
	require.Contains(t, text, `"points":[[4,1],[7,0]]`)
	require.Contains(t, text, `"name":"platform.rate_limiter.in_flight_waiting_rooms"`)
	require.Contains(t, text, `"points":[[1,1],[2,2],[3,1],[8,0]]`)
}

func TestArchiveViewerAndRawEventsShareOneFrozenBoundary(t *testing.T) {
	cfg := testConfig(t.TempDir())
	cfg.EventSegmentBytes = 32 << 20
	manager := initTestManager(t, cfg)

	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		for index := 0; ; index++ {
			select {
			case <-stop:
				return
			default:
				manager.Record(context.Background(), "concurrent.event", Fields{
					"component": "test",
					"index":     index,
				})
			}
		}
	}()

	require.Eventually(t, func() bool {
		return manager.lastSeq.Load() >= 20
	}, time.Second, time.Millisecond)
	artifact, err := manager.BuildArchive(manager.RunID())
	require.NoError(t, err)
	close(stop)
	<-done

	entries := readArchiveEntries(t, artifact.Path)
	var viewer viewerBundle
	require.NoError(t, json.Unmarshal(entries["viewer.json"], &viewer))
	var viewerMax uint64
	for _, event := range viewer.Events {
		if event.Seq > viewerMax {
			viewerMax = event.Seq
		}
	}
	var rawMax uint64
	for name, data := range entries {
		if !strings.HasPrefix(name, "run/events/events-") || !strings.HasSuffix(name, ".jsonl") {
			continue
		}
		for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
			if line == "" {
				continue
			}
			var event Event
			require.NoError(t, json.Unmarshal([]byte(line), &event))
			if event.Seq > rawMax {
				rawMax = event.Seq
			}
		}
	}
	require.NotZero(t, viewerMax)
	require.Equal(t, viewerMax, rawMax,
		"viewer.json 与归档中的原始 JSONL 必须停在同一个事件序号")
}

func TestIncidentChoosesSlowestCompleteRoomGenerationChain(t *testing.T) {
	events := []Event{
		{ID: "other-start", Seq: 1, TS: 0, Name: "listener.start.accepted", RoomScopeID: "room-other", Generation: 1, FlowID: "flow-other", Attrs: map[string]any{}},
		{ID: "other-live", Seq: 2, TS: 100, Name: "listener.poll.end", RoomScopeID: "room-other", Generation: 1, FlowID: "flow-other", Attrs: map[string]any{"live": true}},
		{ID: "other-recorder", Seq: 3, TS: 120, Name: "recorder.session.start", RoomScopeID: "room-other", Generation: 1, FlowID: "flow-other", Attrs: map[string]any{"session_id": "other-session"}},
		{ID: "other-goal", Seq: 4, TS: 500, Name: "segment.first_nonzero_observed", RoomScopeID: "room-other", Generation: 1, FlowID: "flow-other", Attrs: map[string]any{"session_id": "other-session"}},
		{ID: "target-old-start", Seq: 5, TS: 1_000, Name: "listener.start.accepted", RoomScopeID: "room-target", Generation: 1, Attrs: map[string]any{}},
		{ID: "target-start", Seq: 6, TS: 2_000, Name: "listener.start.accepted", RoomScopeID: "room-target", Generation: 2, Attrs: map[string]any{}},
		{ID: "target-live", Seq: 7, TS: 2_500, Name: "listener.poll.end", RoomScopeID: "room-target", Generation: 2, FlowID: "flow-target", Attrs: map[string]any{"live": true}},
		{ID: "target-recorder", Seq: 8, TS: 2_700, Name: "recorder.session.start", RoomScopeID: "room-target", Generation: 2, FlowID: "flow-target", Attrs: map[string]any{"session_id": "target-session"}},
		{ID: "target-goal", Seq: 9, TS: 52_000, Name: "segment.first_nonzero_observed", RoomScopeID: "room-target", Generation: 2, FlowID: "flow-target", Attrs: map[string]any{"session_id": "target-session"}},
		// 同一 session 的后续分段不能把“首次非零”错误拉长。
		{ID: "target-later-segment", Seq: 10, TS: 90_000, Name: "segment.first_nonzero_observed", RoomScopeID: "room-target", Generation: 2, FlowID: "flow-target", Attrs: map[string]any{"session_id": "target-session"}},
	}
	incident := buildIncident("run-test", events, map[string]any{"room_check_interval_ms": 20_000}, false, true, false)
	require.Equal(t, "room-target", incident["target_room_id"])
	require.Equal(t, int64(2), incident["target_generation"])
	require.Equal(t, "target-start", incident["focus_start_event_id"])
	require.Equal(t, "target-live", incident["first_live_event_id"])
	require.Equal(t, "target-recorder", incident["recorder_start_event_id"])
	require.Equal(t, "target-goal", incident["goal_event_id"])
	require.Equal(t, float64(50_000), incident["observed_monitor_to_first_byte_ms"])
}

func readArchiveEntries(t *testing.T, path string) map[string][]byte {
	t.Helper()
	file, err := os.Open(path)
	require.NoError(t, err)
	defer file.Close()
	gzipReader, err := gzip.NewReader(file)
	require.NoError(t, err)
	defer gzipReader.Close()
	tarReader := tar.NewReader(gzipReader)
	entries := map[string][]byte{}
	for {
		header, nextErr := tarReader.Next()
		if errors.Is(nextErr, io.EOF) {
			break
		}
		require.NoError(t, nextErr)
		data, readErr := io.ReadAll(tarReader)
		require.NoError(t, readErr)
		entries[header.Name] = data
	}
	return entries
}

func TestConcurrentRecordsHaveUniqueSequenceAndIDs(t *testing.T) {
	cfg := testConfig(t.TempDir())
	cfg.EventSegmentBytes = 1 << 20
	manager := initTestManager(t, cfg)
	const count = 100
	var wait sync.WaitGroup
	wait.Add(count)
	for i := 0; i < count; i++ {
		go func(index int) {
			defer wait.Done()
			manager.Record(context.Background(), "concurrent.event", Fields{"index": index})
		}(i)
	}
	wait.Wait()
	_, err := manager.SnapshotCurrent()
	require.NoError(t, err)
	loaded, err := loadEvents(manager.runDir)
	require.NoError(t, err)
	require.Len(t, loaded.Events, count+1, "初始化时还会写入一条 runtime.sample")
	ids := map[string]struct{}{}
	sequences := make([]int, 0, count+1)
	concurrentEvents := 0
	for _, event := range loaded.Events {
		ids[event.ID] = struct{}{}
		sequences = append(sequences, int(event.Seq))
		if event.Name == "concurrent.event" {
			concurrentEvents++
		}
	}
	require.Equal(t, count, concurrentEvents)
	require.Len(t, ids, count+1)
	sort.Ints(sequences)
	for index, sequence := range sequences {
		require.Equal(t, index+1, sequence)
	}
}

func TestFlightDisabledStillSupportsSnapshotAndExport(t *testing.T) {
	manager := initTestManager(t, testConfig(t.TempDir()))
	snapshot, err := manager.SnapshotCurrent()
	require.NoError(t, err)
	require.Nil(t, snapshot.Flight)
	_, err = manager.LatestFlightPath(manager.RunID())
	require.ErrorIs(t, err, ErrFlightUnavailable)
	_, err = manager.BuildViewerBundle(manager.RunID())
	require.NoError(t, err)
	_, err = manager.BuildArchive(manager.RunID())
	require.NoError(t, err)
}

func TestFlightRecorderPublishesStableVersionedCopy(t *testing.T) {
	cfg := testConfig(t.TempDir())
	cfg.Flight = FlightConfig{
		Enabled:          true,
		SnapshotInterval: time.Hour,
		MinAge:           time.Second,
		MaxBytes:         2 << 20,
		KeepSnapshots:    2,
	}
	manager := initTestManager(t, cfg)
	manager.Record(context.Background(), "flight.test", Fields{"component": "test"})
	snapshot, err := manager.SnapshotCurrent()
	require.NoError(t, err)
	require.NotNil(t, snapshot.Flight)
	require.FileExists(t, snapshot.Flight.Path)
	require.Contains(t, filepath.Base(snapshot.Flight.Path), "flight-v1-")

	exported, err := manager.LatestFlightPath(manager.RunID())
	require.NoError(t, err)
	require.FileExists(t, exported.Path)
	require.Equal(t, manager.exportsDir, filepath.Dir(exported.Path))
	traceHeader, err := os.ReadFile(exported.Path)
	require.NoError(t, err)
	require.Greater(t, len(traceHeader), 16)
	require.Contains(t, string(traceHeader[:16]), "trace")
	require.NoError(t, os.Remove(exported.Path))
	paths, err := flightSnapshotPaths(filepath.Join(manager.runDir, "flight"))
	require.NoError(t, err)
	require.NotEmpty(t, paths, "删除下载副本不能删除 run 内原始 Flight 证据")
}

func TestPanicMarkerPreventsCleanMarkerAndIsIdempotent(t *testing.T) {
	manager := initTestManager(t, testConfig(t.TempDir()))
	require.NoError(t, manager.RecordPanic(context.Background(), "synthetic panic"))
	require.NoError(t, manager.RecordPanic(context.Background(), "duplicate panic"))
	require.NoError(t, manager.Close())
	require.FileExists(t, filepath.Join(manager.runDir, "panic.json"))
	require.FileExists(t, filepath.Join(manager.runDir, "panic.stack"))
	require.NoFileExists(t, filepath.Join(manager.runDir, "clean.json"))

	loaded, err := loadEvents(manager.runDir)
	require.NoError(t, err)
	panicEvents := 0
	for _, event := range loaded.Events {
		if event.Name == "diagnostics.panic" {
			panicEvents++
		}
	}
	require.Equal(t, 1, panicEvents)
}
