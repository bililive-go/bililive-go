package diagnostics

import (
	"archive/tar"
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const maxViewerEvents = 100_000

type viewerBundle struct {
	Schema         string         `json:"schema"`
	Manifest       map[string]any `json:"manifest"`
	Configuration  map[string]any `json:"configuration"`
	Entities       []any          `json:"entities"`
	Incident       map[string]any `json:"incident"`
	Events         []Event        `json:"events"`
	Metrics        []any          `json:"metrics"`
	RuntimeSamples []any          `json:"runtime_samples"`
	RuntimeSlices  []any          `json:"runtime_slices"`
}

type loadedEvents struct {
	Events      []Event
	Invalid     uint64
	Dropped     uint64
	SourceFiles []string
}

// BuildViewerBundle 生成可直接交给 bililive.diagnostic-bundle/v1 Viewer 的稳定 JSON 文件。
func (m *Manager) BuildViewerBundle(runID string) (Artifact, error) {
	return m.BuildViewerBundleContext(context.Background(), runID)
}

// BuildViewerBundleContext 生成稳定 Viewer JSON，并允许尚在排队或读取证据的
// HTTP 请求在客户端断开后取消。业务事件锁只用于固定文件描述符与长度。
func (m *Manager) BuildViewerBundleContext(
	ctx context.Context,
	runID string,
) (Artifact, error) {
	if m == nil {
		return Artifact{}, ErrNotInitialized
	}
	release, err := m.acquireExport(ctx)
	if err != nil {
		return Artifact{}, err
	}
	defer release()
	snapshot, err := m.freezeRun(ctx, runID, false, false)
	if err != nil {
		return Artifact{}, err
	}
	defer snapshot.Close()
	data, err := m.buildViewerBytes(ctx, snapshot)
	if err != nil {
		return Artifact{}, err
	}
	if err = ctx.Err(); err != nil {
		return Artifact{}, err
	}
	path, err := m.newExportPath(runID, "viewer", ".json")
	if err != nil {
		return Artifact{}, err
	}
	if err = atomicWriteFile(path, 0o600, false, func(writer io.Writer) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		return writeAll(writer, data)
	}); err != nil {
		return Artifact{}, err
	}
	return artifactForPath(path, "application/json")
}

func (m *Manager) newExportPath(runID, kind, suffix string) (string, error) {
	if err := validateRunID(runID); err != nil {
		return "", err
	}
	if err := cleanupStaleExports(m.exportsDir, time.Now()); err != nil {
		// 清理失败不应阻止用户导出当前证据，但要通过 Manager.Err 暴露给
		// 运维排查；正常请求仍会尝试删除自己创建的副本。
		m.setError(err)
	}
	random, err := randomHex(8)
	if err != nil {
		return "", err
	}
	name := fmt.Sprintf("bililive-go-%s-%s-%s%s", kind, runID, random, suffix)
	return filepath.Join(m.exportsDir, name), nil
}

func (m *Manager) buildViewerBytes(
	ctx context.Context,
	snapshot *frozenRunSnapshot,
) ([]byte, error) {
	runID := snapshot.runID
	var manifest RunManifest
	manifestErr := snapshot.readMetadata("run.json", &manifest)
	if manifestErr == nil &&
		(manifest.Schema != RunSchema || manifest.RunID != runID) {
		manifestErr = errors.New("run manifest 与目录不匹配")
	}
	if manifestErr != nil {
		manifest = RunManifest{
			Schema:    RunSchema,
			RunID:     runID,
			StartedAt: time.Now().UTC(),
			State:     "unknown",
			TraceMode: "diagnostic",
		}
		if file := snapshot.files["run.json"]; file != nil {
			manifest.StartedAt = time.Unix(0, file.modTime).UTC()
		}
	}
	loaded, err := loadFrozenEvents(ctx, snapshot)
	if err != nil {
		return nil, err
	}
	active := snapshot.active
	hasPanic := snapshot.has("panic.json") || snapshot.has("panic.stack")
	var cleanMarker cleanMarker
	cleanMarkerOK := snapshot.readMetadata("clean.json", &cleanMarker) == nil &&
		cleanMarker.Schema == "bililive.diagnostics-clean/v1" &&
		cleanMarker.RunID != "" &&
		!cleanMarker.CreatedAt.IsZero()
	clean := cleanMarkerOK && cleanMarker.RunID == runID && !hasPanic
	var heartbeat heartbeatFile
	heartbeatOK := snapshot.readMetadata("heartbeat.json", &heartbeat) == nil &&
		heartbeat.Schema == "bililive.diagnostics-heartbeat/v1" &&
		heartbeat.RunID == runID
	loaded.Events = addCrashInference(
		loaded.Events,
		manifest,
		heartbeat,
		heartbeatOK,
		active,
		clean,
		hasPanic,
	)
	sort.SliceStable(loaded.Events, func(i, j int) bool {
		if loaded.Events[i].Seq == loaded.Events[j].Seq {
			return loaded.Events[i].TS < loaded.Events[j].TS
		}
		return loaded.Events[i].Seq < loaded.Events[j].Seq
	})
	if len(loaded.Events) > maxViewerEvents {
		excess := len(loaded.Events) - maxViewerEvents
		loaded.Dropped += uint64(excess)
		loaded.Events = loaded.Events[excess:]
	}

	captureStart, captureEnd := eventWindow(loaded.Events)
	completeness := "complete"
	if active || !clean || manifestErr != nil || loaded.Invalid > 0 || loaded.Dropped > 0 {
		completeness = "partial"
	}
	bundleID := "bundle_" + newIDFor(m, "export")
	incident := buildIncident(runID, loaded.Events, manifest.Configuration, active, clean, hasPanic)
	sourceFiles := append([]string{"run.json", "heartbeat.json"}, loaded.SourceFiles...)
	if hasPanic {
		sourceFiles = append(sourceFiles, "panic.json", "panic.stack")
	}
	bundle := viewerBundle{
		Schema: BundleSchema,
		Manifest: map[string]any{
			"bundle_id":                   bundleID,
			"schema_version":              BundleSchema,
			"generated_at":                time.Now().UTC(),
			"run_id":                      runID,
			"time_origin":                 manifest.StartedAt,
			"capture_start_ms":            captureStart,
			"capture_end_ms":              captureEnd,
			"actual_window_ms":            captureEnd - captureStart,
			"evidence_capture_started_at": snapshot.captureStartedAt,
			"evidence_capture_ended_at":   snapshot.captureEndedAt,
			"evidence_capture_skew_ms": float64(
				snapshot.captureEndedAt.Sub(snapshot.captureStartedAt),
			) / float64(time.Millisecond),
			"synthetic":         false,
			"app_version":       manifest.AppVersion,
			"commit":            manifest.Commit,
			"go_version":        manifest.GoVersion,
			"os":                manifest.OS,
			"arch":              manifest.Arch,
			"platform":          strings.Trim(strings.Join([]string{manifest.OS, manifest.Arch}, "/"), "/"),
			"trace_mode":        manifest.TraceMode,
			"completeness":      completeness,
			"dropped_events":    loaded.Dropped + loaded.Invalid,
			"redaction_version": "hmac-run-v1",
			"sequence_scope":    "process_global_observation_order",
			"source_files":      sourceFiles,
		},
		Configuration:  nonNilMap(manifest.Configuration),
		Entities:       []any{},
		Incident:       incident,
		Events:         loaded.Events,
		Metrics:        deriveViewerMetrics(loaded.Events),
		RuntimeSamples: []any{},
		RuntimeSlices:  []any{},
	}
	return appendJSONNewline(bundle)
}

func nonNilMap(value map[string]any) map[string]any {
	if value == nil {
		return map[string]any{}
	}
	return value
}

func appendJSONNewline(value any) ([]byte, error) {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(data, '\n'), nil
}

func loadFrozenEvents(
	ctx context.Context,
	snapshot *frozenRunSnapshot,
) (loadedEvents, error) {
	var index eventIndex
	_ = snapshot.readMetadata("events/index.json", &index)
	result := loadedEvents{
		Events:  make([]Event, 0),
		Dropped: index.DroppedEvents,
	}
	for _, relativePath := range snapshot.order {
		if !strings.HasPrefix(relativePath, "events/events-") ||
			!strings.HasSuffix(relativePath, ".jsonl") {
			continue
		}
		if err := ctx.Err(); err != nil {
			return result, err
		}
		source, ok := snapshot.reader(relativePath)
		if !ok {
			result.Invalid++
			continue
		}
		result.SourceFiles = append(result.SourceFiles, relativePath)
		reader := bufio.NewReaderSize(source, 64<<10)
		for {
			if err := ctx.Err(); err != nil {
				return result, err
			}
			line, readErr := reader.ReadBytes('\n')
			if len(line) > 0 {
				var event Event
				if unmarshalErr := json.Unmarshal(line, &event); unmarshalErr != nil {
					result.Invalid++
				} else {
					if event.ID == "" {
						event.ID = fmt.Sprintf("recovered_event_%d", event.Seq)
					}
					if event.Attrs == nil {
						event.Attrs = map[string]any{}
					}
					result.Events = append(result.Events, event)
				}
			}
			if readErr != nil {
				if !errors.Is(readErr, io.EOF) {
					return result, readErr
				}
				break
			}
		}
	}
	// index.json 可能在删除旧分段后、更新 dropped_events 前遇到进程崩溃。
	// 用实际 seq 缺口做下限，避免把已经丢失的事件误报为完整。
	if len(result.Events) > 0 {
		sort.SliceStable(result.Events, func(i, j int) bool {
			return result.Events[i].Seq < result.Events[j].Seq
		})
		var inferredDropped uint64
		expected := uint64(1)
		for _, event := range result.Events {
			if event.Seq > expected {
				inferredDropped += event.Seq - expected
			}
			if event.Seq >= expected {
				expected = event.Seq + 1
			}
		}
		if inferredDropped > result.Dropped {
			result.Dropped = inferredDropped
		}
	}
	return result, nil
}

// loadEvents 保留给包内恢复测试和离线检查使用；导出路径使用
// loadFrozenEvents，以免在解析大 JSONL 时占用 writer 锁。
func loadEvents(runDir string) (loadedEvents, error) {
	snapshot, err := openFrozenRunFiles(
		context.Background(),
		runDir,
		filepath.Base(runDir),
		false,
		false,
		frozenEventFiles,
	)
	if err != nil {
		return loadedEvents{}, err
	}
	defer snapshot.Close()
	return loadFrozenEvents(context.Background(), snapshot)
}

func addCrashInference(
	events []Event,
	manifest RunManifest,
	heartbeat heartbeatFile,
	heartbeatOK bool,
	active bool,
	clean bool,
	hasPanic bool,
) []Event {
	if active || clean {
		return events
	}
	var latestSeq uint64
	var latestTS float64
	hasPanicEvent := false
	for _, event := range events {
		if event.Seq > latestSeq {
			latestSeq = event.Seq
		}
		if event.TS > latestTS {
			latestTS = event.TS
		}
		if event.Name == "diagnostics.panic" {
			hasPanicEvent = true
		}
	}
	at := time.Now().UTC()
	if heartbeatOK {
		at = heartbeat.At
		if !manifest.StartedAt.IsZero() {
			latestTS = maxFloat(latestTS, float64(at.Sub(manifest.StartedAt))/float64(time.Millisecond))
		}
	}
	name := "process.exit.unobserved"
	status := "unclean"
	severity := "warn"
	attrs := map[string]any{
		"inferred":             true,
		"evidence":             "clean_marker_missing",
		"last_heartbeat_known": heartbeatOK,
	}
	if hasPanic {
		if hasPanicEvent {
			return events
		}
		name = "diagnostics.panic.marker"
		status = "panic"
		severity = "error"
		attrs["evidence"] = "panic_marker_present"
	}
	return append(events, Event{
		ID:           fmt.Sprintf("recovered_terminal_%d", latestSeq+1),
		TS:           latestTS,
		WallTime:     at,
		Seq:          latestSeq + 1,
		GlobalSeq:    latestSeq + 1,
		Name:         name,
		Kind:         "state.transition",
		Category:     "runtime",
		Component:    "runtime",
		Lane:         "Runtime",
		Severity:     severity,
		RoomScopeID:  "",
		Generation:   0,
		FlowID:       "",
		TaskID:       "",
		SpanID:       "",
		ParentSpanID: "",
		Status:       status,
		DurationMS:   0,
		Attrs:        attrs,
	})
}

func maxFloat(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
}

func eventWindow(events []Event) (float64, float64) {
	if len(events) == 0 {
		return 0, 0
	}
	start, end := events[0].TS, events[0].TS
	for _, event := range events[1:] {
		if event.TS < start {
			start = event.TS
		}
		if event.TS > end {
			end = event.TS
		}
	}
	return start, end
}

func buildIncident(
	runID string,
	events []Event,
	configuration map[string]any,
	active, clean, hasPanic bool,
) map[string]any {
	if chain, ok := findRecordingStartIncident(events); ok {
		return map[string]any{
			"id":                                "incident_" + runID,
			"type":                              "recording_start_slow",
			"title":                             "从开始监控到首次观察到非空文件",
			"summary":                           "Viewer 已按 room_scope_id、generation 和 flow_id 选择同一条完整因果链。",
			"severity":                          "warning",
			"trigger":                           "recording_start_slow",
			"anchor_entity_id":                  chain.RoomScopeID,
			"target_room_id":                    chain.RoomScopeID,
			"target_generation":                 chain.Generation,
			"anchor_start_event_id":             chain.Start.ID,
			"focus_start_event_id":              chain.Start.ID,
			"first_live_event_id":               chain.Live.ID,
			"recorder_start_event_id":           chain.Recorder.ID,
			"goal_event_id":                     chain.Goal.ID,
			"trigger_event_id":                  chain.Goal.ID,
			"expected_detection_interval_ms":    detectionInterval(configuration),
			"observed_monitor_to_first_byte_ms": chain.Goal.TS - chain.Start.TS,
		}
	}

	title := "运行诊断轨迹"
	severity := "info"
	trigger := "manual"
	switch {
	case hasPanic:
		title = "程序发生 panic"
		severity = "error"
		trigger = "panic"
	case !clean && !active:
		title = "上一次运行没有正常关闭标记"
		severity = "warning"
		trigger = "unclean_restart"
	case active:
		title = "当前运行诊断快照"
		severity = "warning"
		trigger = "snapshot"
	}
	var firstID, lastID, roomID string
	var monitorTS, firstByteTS *float64
	for index := range events {
		event := &events[index]
		if firstID == "" {
			firstID = event.ID
		}
		lastID = event.ID
		if roomID == "" && event.RoomScopeID != "" {
			roomID = event.RoomScopeID
		}
		if event.Name == "monitor.started" && monitorTS == nil {
			value := event.TS
			monitorTS = &value
		}
		if event.Name == "segment.first_byte" && firstByteTS == nil {
			value := event.TS
			firstByteTS = &value
		}
	}
	observed := float64(0)
	if monitorTS != nil && firstByteTS != nil && *firstByteTS >= *monitorTS {
		observed = *firstByteTS - *monitorTS
	}
	return map[string]any{
		"id":                                "incident_" + runID,
		"type":                              trigger,
		"title":                             title,
		"summary":                           "由本地结构化业务轨迹生成；缺失 clean marker 只表示无法证明正常关闭。",
		"severity":                          severity,
		"trigger":                           trigger,
		"anchor_entity_id":                  roomID,
		"target_room_id":                    roomID,
		"anchor_start_event_id":             firstID,
		"goal_event_id":                     lastID,
		"expected_detection_interval_ms":    detectionInterval(configuration),
		"observed_monitor_to_first_byte_ms": observed,
	}
}

type recordingIncidentChain struct {
	Start       Event
	Live        Event
	Recorder    Event
	Goal        Event
	RoomScopeID string
	Generation  int64
}

func findRecordingStartIncident(events []Event) (recordingIncidentChain, bool) {
	firstGoals := map[string]Event{}
	for _, event := range events {
		if event.Name != "segment.first_nonzero_observed" && event.Name != "segment.first_byte" {
			continue
		}
		key := eventIdentityAttr(event, "session_id")
		if key == "" {
			key = event.FlowID + "|" + event.RoomScopeID + "|" + fmt.Sprint(event.Generation)
		}
		if existing, exists := firstGoals[key]; !exists || event.TS < existing.TS {
			firstGoals[key] = event
		}
	}
	var best recordingIncidentChain
	found := false
	for _, goal := range firstGoals {
		roomID, generation := inferGoalScope(events, goal)
		if roomID == "" {
			continue
		}
		start, hasStart := findLifecycleStart(events, goal, roomID, generation)
		if !hasStart {
			continue
		}
		liveEvent, hasLive := findFirstLive(events, start, goal, roomID, generation)
		if !hasLive {
			continue
		}
		recorderEvent, hasRecorder := findRecorderStart(events, liveEvent, goal, roomID, generation)
		if !hasRecorder {
			continue
		}
		candidate := recordingIncidentChain{
			Start:       start,
			Live:        liveEvent,
			Recorder:    recorderEvent,
			Goal:        goal,
			RoomScopeID: roomID,
			Generation:  generation,
		}
		candidateDuration := candidate.Goal.TS - candidate.Start.TS
		bestDuration := best.Goal.TS - best.Start.TS
		if !found || candidateDuration > bestDuration ||
			(candidateDuration == bestDuration && candidate.Goal.TS > best.Goal.TS) {
			best = candidate
			found = true
		}
	}
	return best, found
}

func eventIdentityAttr(event Event, key string) string {
	value, ok := event.Attrs[key]
	if !ok || value == nil {
		return ""
	}
	if result, ok := value.(string); ok {
		return result
	}
	return fmt.Sprint(value)
}

func inferGoalScope(events []Event, goal Event) (string, int64) {
	roomID := goal.RoomScopeID
	generation := goal.Generation
	for index := len(events) - 1; index >= 0 && (roomID == "" || generation == 0); index-- {
		event := events[index]
		if event.TS > goal.TS || goal.FlowID == "" || event.FlowID != goal.FlowID {
			continue
		}
		if roomID == "" {
			roomID = event.RoomScopeID
		}
		if generation == 0 {
			generation = event.Generation
		}
	}
	return roomID, generation
}

func findLifecycleStart(events []Event, goal Event, roomID string, generation int64) (Event, bool) {
	startNames := map[string]struct{}{
		"listener.start.accepted": {},
		"monitor.started":         {},
		"monitor.add.requested":   {},
	}
	var result Event
	found := false
	for _, event := range events {
		if event.TS > goal.TS {
			continue
		}
		if _, ok := startNames[event.Name]; !ok || !sameIncidentScope(event, goal, roomID, generation) {
			continue
		}
		if !found || event.TS > result.TS {
			result = event
			found = true
		}
	}
	return result, found
}

func findFirstLive(events []Event, start, goal Event, roomID string, generation int64) (Event, bool) {
	var result Event
	found := false
	for _, event := range events {
		if event.TS < start.TS || event.TS > goal.TS ||
			(event.Name != "listener.poll.end" && event.Name != "live.refresh.end") ||
			!sameIncidentScope(event, goal, roomID, generation) ||
			!eventAttrBool(event, "live") {
			continue
		}
		if !found || event.TS < result.TS {
			result = event
			found = true
		}
	}
	return result, found
}

func findRecorderStart(events []Event, liveEvent, goal Event, roomID string, generation int64) (Event, bool) {
	var result Event
	found := false
	for _, event := range events {
		if event.TS < liveEvent.TS || event.TS > goal.TS ||
			(event.Name != "recorder.session.start" && event.Name != "recorder.session.created") ||
			!sameIncidentScope(event, goal, roomID, generation) {
			continue
		}
		if !found || event.TS < result.TS {
			result = event
			found = true
		}
	}
	return result, found
}

func sameIncidentScope(event, goal Event, roomID string, generation int64) bool {
	if event.RoomScopeID != "" && event.RoomScopeID == roomID {
		return generation == 0 || event.Generation == 0 || event.Generation == generation
	}
	return goal.FlowID != "" && event.FlowID == goal.FlowID
}

func eventAttrBool(event Event, key string) bool {
	value, ok := event.Attrs[key]
	if !ok {
		return false
	}
	switch typed := value.(type) {
	case bool:
		return typed
	case string:
		return strings.EqualFold(typed, "true")
	default:
		return false
	}
}

func detectionInterval(configuration map[string]any) float64 {
	for _, key := range []string{"room_check_interval_ms", "interval_ms", "check_interval_ms"} {
		if value, ok := configuration[key]; ok {
			return numericValue(value)
		}
	}
	for _, key := range []string{"detection_interval_s", "room_check_interval_s", "interval_s"} {
		if value, ok := configuration[key]; ok {
			return numericValue(value) * 1000
		}
	}
	return 0
}

func numericValue(value any) float64 {
	switch typed := value.(type) {
	case int:
		return float64(typed)
	case int64:
		return float64(typed)
	case uint64:
		return float64(typed)
	case float64:
		return typed
	case json.Number:
		result, _ := typed.Float64()
		return result
	}
	return 0
}

// BuildArchive 生成包含 viewer.json、run marker、JSONL 和 Flight trace 的稳定 tar.gz。
func (m *Manager) BuildArchive(runID string) (Artifact, error) {
	return m.BuildArchiveContext(context.Background(), runID)
}

// BuildArchiveContext 从已固定的文件描述符构建调查包。JSON 解析和 gzip
// 不持有业务事件锁，并在每个数据块之间检查请求取消。
func (m *Manager) BuildArchiveContext(
	ctx context.Context,
	runID string,
) (Artifact, error) {
	if m == nil {
		return Artifact{}, ErrNotInitialized
	}
	release, err := m.acquireExport(ctx)
	if err != nil {
		return Artifact{}, err
	}
	defer release()
	snapshot, err := m.freezeRun(ctx, runID, true, true)
	if err != nil {
		return Artifact{}, err
	}
	defer snapshot.Close()
	viewer, err := m.buildViewerBytes(ctx, snapshot)
	if err != nil {
		return Artifact{}, err
	}
	path, err := m.newExportPath(runID, "diagnostics", ".tar.gz")
	if err != nil {
		return Artifact{}, err
	}

	err = atomicWriteFile(path, 0o600, false, func(output io.Writer) error {
		gzipWriter := gzip.NewWriter(output)
		tarWriter := tar.NewWriter(gzipWriter)
		if writeErr := writeTarBytesContext(
			ctx,
			tarWriter,
			"viewer.json",
			viewer,
			time.Now(),
		); writeErr != nil {
			_ = tarWriter.Close()
			_ = gzipWriter.Close()
			return writeErr
		}
		if writeErr := writeTarBytesContext(
			ctx,
			tarWriter,
			"bundle.json",
			viewer,
			time.Now(),
		); writeErr != nil {
			_ = tarWriter.Close()
			_ = gzipWriter.Close()
			return writeErr
		}
		for _, relativePath := range snapshot.order {
			file := snapshot.files[relativePath]
			if file == nil {
				continue
			}
			if writeErr := writeTarFrozenFile(ctx, tarWriter, file); writeErr != nil {
				_ = tarWriter.Close()
				_ = gzipWriter.Close()
				return writeErr
			}
		}
		if closeErr := tarWriter.Close(); closeErr != nil {
			_ = gzipWriter.Close()
			return closeErr
		}
		return gzipWriter.Close()
	})
	if err != nil {
		return Artifact{}, err
	}
	return artifactForPath(path, "application/gzip")
}

func writeTarBytesContext(
	ctx context.Context,
	writer *tar.Writer,
	name string,
	data []byte,
	modTime time.Time,
) error {
	header := &tar.Header{
		Name:    name,
		Mode:    0o600,
		Size:    int64(len(data)),
		ModTime: modTime.UTC(),
	}
	if err := writer.WriteHeader(header); err != nil {
		return err
	}
	_, err := copyNWithContext(ctx, writer, bytes.NewReader(data), int64(len(data)))
	return err
}

func writeTarFrozenFile(
	ctx context.Context,
	writer *tar.Writer,
	file *frozenEvidenceFile,
) error {
	header := &tar.Header{
		Name:    filepath.ToSlash(filepath.Join("run", file.relativePath)),
		Mode:    0o600,
		Size:    file.size,
		ModTime: time.Unix(0, file.modTime).UTC(),
	}
	if err := writer.WriteHeader(header); err != nil {
		return err
	}
	_, err := copyNWithContext(
		ctx,
		writer,
		io.NewSectionReader(file.file, 0, file.size),
		file.size,
	)
	return err
}
