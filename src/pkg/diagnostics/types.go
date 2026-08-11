// Package diagnostics 持久化一次进程运行中的结构化业务轨迹和 Go Flight Recorder
// 快照。包内所有原始证据都按 run 隔离，导出文件只是这些证据的稳定副本。
package diagnostics

import (
	"errors"
	"time"
)

const (
	// RunSchema 是 run.json 的格式版本。
	RunSchema = "bililive.diagnostics-run/v1"
	// BundleSchema 是离线 Viewer 使用的格式版本。
	BundleSchema = "bililive.diagnostic-bundle/v1"

	defaultEventSegmentBytes = int64(4 << 20)
	defaultMaxEventSegments  = 8
	defaultMaxRuns           = 20
)

var (
	ErrNotInitialized     = errors.New("diagnostics 尚未初始化")
	ErrAlreadyInitialized = errors.New("diagnostics 已经初始化且仍在运行")
	ErrClosed             = errors.New("diagnostics 已关闭")
	ErrRunNotFound        = errors.New("找不到 diagnostics run")
	ErrInvalidRunID       = errors.New("非法 diagnostics run ID")
	ErrRunActive          = errors.New("diagnostics run 仍由活跃进程持有")
	ErrFlightUnavailable  = errors.New("flight recorder 未启用或没有可用快照")
)

// Fields 是插桩 API 使用的字段集合。
//
// component、lane、severity、room_scope_id、generation、flow_id、task_id、
// span_id、parent_span_id、status、duration_ms、kind、category 会成为事件顶层字段；
// attrs 中的字段和其余字段会进入 attrs。包含 URL、Cookie、Token 等敏感值的字段
// 会在写盘前被脱敏。
type Fields map[string]any

// Config 控制 diagnostics 的本地持久化。AppDataPath 必须是应用数据目录，
// 包会在其下创建 diagnostics/runs、diagnostics/exports。
type Config struct {
	AppDataPath string
	AppVersion  string
	Commit      string
	TraceMode   string

	// Configuration 是明确允许写入 Viewer bundle 的配置摘要。它仍会经过脱敏，
	// 不应直接传入完整配置对象。
	Configuration map[string]any

	HeartbeatInterval time.Duration
	EventSyncInterval time.Duration
	EventSegmentBytes int64
	MaxEventSegments  int
	MaxRuns           int
	Flight            FlightConfig
}

// FlightConfig 控制 runtime/trace Flight Recorder。Enabled=false 时，业务事件、
// heartbeat、bundle 和 archive 功能仍然正常工作。
type FlightConfig struct {
	Enabled          bool
	SnapshotInterval time.Duration
	MinAge           time.Duration
	MaxBytes         uint64
	KeepSnapshots    int
}

// Artifact 是已经完整写入、fsync 并原子发布的稳定文件。
// 调用方可以用 Path 打开并通过 http.ServeContent 提供下载；导出文件发送完成后可删除。
type Artifact struct {
	Name        string    `json:"name"`
	Path        string    `json:"-"`
	ContentType string    `json:"content_type"`
	Size        int64     `json:"size"`
	ModTime     time.Time `json:"mod_time"`
}

// Snapshot 描述一次主动刷盘的结果。Flight Recorder 禁用时 Flight 为 nil。
type Snapshot struct {
	RunID          string    `json:"run_id"`
	CapturedAt     time.Time `json:"captured_at"`
	LatestEventSeq uint64    `json:"latest_event_seq"`
	DroppedEvents  uint64    `json:"dropped_events"`
	Flight         *Artifact `json:"flight,omitempty"`
}

// RunInfo 是管理 API 返回的单次运行摘要。Active 是通过跨进程租约判断
// “仍可能写入”；Current 只表示它是否属于当前 Manager。因此共享 AppData
// 时允许 Active=true、Current=false。
type RunInfo struct {
	RunID                   string     `json:"run_id"`
	Path                    string     `json:"-"`
	StartedAt               time.Time  `json:"started_at"`
	LastHeartbeat           *time.Time `json:"last_heartbeat,omitempty"`
	LeaseRenewedAt          *time.Time `json:"lease_renewed_at,omitempty"`
	LeaseExpiresAt          *time.Time `json:"lease_expires_at,omitempty"`
	EndedAt                 *time.Time `json:"ended_at,omitempty"`
	Status                  string     `json:"status"`
	Active                  bool       `json:"active"`
	ActiveReason            string     `json:"active_reason,omitempty"`
	Current                 bool       `json:"current"`
	OwnerPID                int        `json:"owner_pid,omitempty"`
	Clean                   bool       `json:"clean"`
	Acknowledged            bool       `json:"acknowledged"`
	HasPanic                bool       `json:"has_panic"`
	EventCount              uint64     `json:"event_count"`
	EventSegments           int        `json:"event_segments"`
	FlightRecorderAvailable bool       `json:"flight_recorder_available"`
	SizeBytes               int64      `json:"size_bytes"`
}

// StartupReport 是 Init 时看到的既有运行状态。ActiveRuns 与 UncleanRuns
// 均不包含刚创建的当前 run，且二者互斥；活跃 run 不应作为异常退出告警。
type StartupReport struct {
	CurrentRunID string    `json:"current_run_id"`
	PreviousRun  *RunInfo  `json:"previous_run,omitempty"`
	ActiveRuns   []RunInfo `json:"active_runs"`
	UncleanRuns  []RunInfo `json:"unclean_runs"`
}

// RunManifest 是 run.json 的内容。Clean 必须以 clean.json 是否存在为准，
// 不能只根据这里的 State 推断。
type RunManifest struct {
	Schema        string         `json:"schema"`
	RunID         string         `json:"run_id"`
	StartedAt     time.Time      `json:"started_at"`
	EndedAt       *time.Time     `json:"ended_at,omitempty"`
	State         string         `json:"state"`
	PID           int            `json:"pid"`
	AppVersion    string         `json:"app_version,omitempty"`
	Commit        string         `json:"commit,omitempty"`
	GoVersion     string         `json:"go_version"`
	OS            string         `json:"os"`
	Arch          string         `json:"arch"`
	TraceMode     string         `json:"trace_mode"`
	Configuration map[string]any `json:"configuration,omitempty"`
	FlightEnabled bool           `json:"flight_enabled"`
	FlightError   string         `json:"flight_error,omitempty"`
	DroppedEvents uint64         `json:"dropped_events,omitempty"`
}

// Event 是 JSONL 中的稳定事件格式。关键关联字段即使为空也会写出，便于崩溃后
// 使用简单工具直接检查不完整事件。
type Event struct {
	ID           string         `json:"id"`
	TS           float64        `json:"ts"`
	WallTime     time.Time      `json:"wall_time"`
	Seq          uint64         `json:"seq"`
	GlobalSeq    uint64         `json:"global_seq"`
	Name         string         `json:"name"`
	Kind         string         `json:"kind"`
	Category     string         `json:"category"`
	Component    string         `json:"component"`
	Lane         string         `json:"lane"`
	Severity     string         `json:"severity"`
	RoomScopeID  string         `json:"room_scope_id"`
	Generation   int64          `json:"generation"`
	FlowID       string         `json:"flow_id"`
	TaskID       string         `json:"task_id"`
	SpanID       string         `json:"span_id"`
	ParentSpanID string         `json:"parent_span_id"`
	Status       string         `json:"status"`
	DurationMS   float64        `json:"duration_ms"`
	Attrs        map[string]any `json:"attrs"`
}
