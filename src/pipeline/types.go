// Package pipeline 实现可配置的后处理管道系统
// 用户可以自由调整后处理阶段的顺序，在任意位置插入自定义命令，并支持并行分支处理
package pipeline

import (
	"context"
	"time"

	"github.com/bililive-go/bililive-go/src/live"
	"github.com/bililive-go/bililive-go/src/pkg/events"
	"github.com/bililive-go/bililive-go/src/pkg/livelogger"
	"github.com/bililive-go/bililive-go/src/types"
)

// PipelineTaskUpdateEvent 管道任务更新事件
const PipelineTaskUpdateEvent events.EventType = "PipelineTaskUpdate"

// FileType 文件类型
type FileType string

const (
	// FileTypeVideo 视频文件
	FileTypeVideo FileType = "video"
	// FileTypeCover 封面文件
	FileTypeCover FileType = "cover"
	// FileTypeOther 其他文件
	FileTypeOther FileType = "other"
)

// FileInfo 表示管道中流转的文件信息
type FileInfo struct {
	Path       string         `json:"path"`                  // 文件绝对路径
	Type       FileType       `json:"type"`                  // 文件类型
	SourcePath string         `json:"source_path,omitempty"` // 来源文件路径（用于追踪转换链）
	Metadata   map[string]any `json:"metadata,omitempty"`    // 额外元数据
}

// NewVideoFileInfo 创建视频文件信息
func NewVideoFileInfo(path string) FileInfo {
	return FileInfo{
		Path: path,
		Type: FileTypeVideo,
	}
}

// NewCoverFileInfo 创建封面文件信息
func NewCoverFileInfo(path, sourcePath string) FileInfo {
	return FileInfo{
		Path:       path,
		Type:       FileTypeCover,
		SourcePath: sourcePath,
	}
}

// RecordInfo 录制信息
type RecordInfo struct {
	LiveID    types.LiveID `json:"live_id"`
	Platform  string       `json:"platform"`
	HostName  string       `json:"host_name"`
	RoomName  string       `json:"room_name"`
	StartTime time.Time    `json:"start_time"`
}

// NewRecordInfo 从 live.Info 创建录制信息
func NewRecordInfo(info *live.Info) RecordInfo {
	return RecordInfo{
		LiveID:    info.Live.GetLiveId(),
		Platform:  info.Live.GetPlatformCNName(),
		HostName:  info.HostName,
		RoomName:  info.RoomName,
		StartTime: time.Now(),
	}
}

// PipelineContext 管道执行上下文
type PipelineContext struct {
	Ctx        context.Context        // 取消控制
	RecordInfo RecordInfo             // 录制信息
	Logger     *livelogger.LiveLogger // 日志记录器
	WorkDir    string                 // 工作目录
	TempDir    string                 // 临时文件目录

	// FFmpegPath 是 ffmpeg 可执行文件的路径
	FFmpegPath string
}

// Stage 管道阶段接口
type Stage interface {
	// Name 返回阶段的唯一标识符
	Name() string

	// Execute 执行阶段处理
	// 输入：上一阶段的输出文件列表
	// 输出：本阶段的输出文件列表（传递给下一阶段）
	Execute(ctx *PipelineContext, input []FileInfo) (output []FileInfo, err error)
}

// StageFactory 阶段工厂函数
type StageFactory func(config StageConfig) (Stage, error)

// StageConfig 阶段配置（用于 YAML/JSON 配置）
type StageConfig struct {
	Name     string         `yaml:"name" json:"name"`                   // 阶段名称
	Enabled  *bool          `yaml:"enabled,omitempty" json:"enabled"`   // 是否启用（nil 表示 true）
	Parallel []StageConfig  `yaml:"parallel,omitempty" json:"parallel"` // 并行执行的子阶段
	Options  map[string]any `yaml:"options,omitempty" json:"options"`   // 阶段特定选项
}

// IsEnabled 检查阶段是否启用
func (sc *StageConfig) IsEnabled() bool {
	if sc.Enabled == nil {
		return true
	}
	return *sc.Enabled
}

// IsParallel 检查是否为并行阶段
func (sc *StageConfig) IsParallel() bool {
	return len(sc.Parallel) > 0
}

// GetOption 获取选项值
func (sc *StageConfig) GetOption(key string) (any, bool) {
	if sc.Options == nil {
		return nil, false
	}
	v, ok := sc.Options[key]
	return v, ok
}

// GetBoolOption 获取布尔类型选项
func (sc *StageConfig) GetBoolOption(key string, defaultValue bool) bool {
	v, ok := sc.GetOption(key)
	if !ok {
		return defaultValue
	}
	if b, ok := v.(bool); ok {
		return b
	}
	return defaultValue
}

// GetStringOption 获取字符串类型选项
func (sc *StageConfig) GetStringOption(key string, defaultValue string) string {
	v, ok := sc.GetOption(key)
	if !ok {
		return defaultValue
	}
	if s, ok := v.(string); ok {
		return s
	}
	return defaultValue
}

// GetStringSliceOption 获取字符串切片类型选项
func (sc *StageConfig) GetStringSliceOption(key string) []string {
	v, ok := sc.GetOption(key)
	if !ok {
		return nil
	}
	switch val := v.(type) {
	case []string:
		return val
	case []any:
		result := make([]string, 0, len(val))
		for _, item := range val {
			if s, ok := item.(string); ok {
				result = append(result, s)
			}
		}
		return result
	}
	return nil
}

// PipelineConfig 管道配置
type PipelineConfig struct {
	Stages []StageConfig `yaml:"stages" json:"stages"` // 阶段列表
}

// PipelineStatus 管道任务状态
type PipelineStatus string

const (
	// PipelineStatusPending 等待执行
	PipelineStatusPending PipelineStatus = "pending"
	// PipelineStatusRunning 正在执行
	PipelineStatusRunning PipelineStatus = "running"
	// PipelineStatusCompleted 已完成
	PipelineStatusCompleted PipelineStatus = "completed"
	// PipelineStatusFailed 执行失败
	PipelineStatusFailed PipelineStatus = "failed"
	// PipelineStatusCancelled 已取消
	PipelineStatusCancelled PipelineStatus = "cancelled"
)

// StageStatus 阶段状态
type StageStatus string

const (
	// StageStatusPending 等待执行
	StageStatusPending StageStatus = "pending"
	// StageStatusRunning 正在执行
	StageStatusRunning StageStatus = "running"
	// StageStatusCompleted 已完成
	StageStatusCompleted StageStatus = "completed"
	// StageStatusFailed 执行失败
	StageStatusFailed StageStatus = "failed"
	// StageStatusSkipped 已跳过
	StageStatusSkipped StageStatus = "skipped"
)

// StageResult 阶段执行结果
type StageResult struct {
	StageName    string      `json:"stage_name"`
	StageIndex   int         `json:"stage_index"`
	Status       StageStatus `json:"status"`
	InputFiles   []FileInfo  `json:"input_files,omitempty"`
	OutputFiles  []FileInfo  `json:"output_files,omitempty"`
	StartedAt    time.Time   `json:"started_at"`
	CompletedAt  *time.Time  `json:"completed_at,omitempty"`
	Commands     []string    `json:"commands,omitempty"` // 执行的命令
	Logs         string      `json:"logs,omitempty"`     // 执行日志
	ErrorMessage string      `json:"error_message,omitempty"`
}

// PipelineTask 管道任务（持久化到数据库）
type PipelineTask struct {
	ID             int64           `json:"id"`
	Status         PipelineStatus  `json:"status"`
	RecordInfo     RecordInfo      `json:"record_info"`
	PipelineConfig *PipelineConfig `json:"pipeline_config"` // 使用的管道配置
	InitialFiles   []FileInfo      `json:"initial_files"`   // 初始输入文件
	CurrentFiles   []FileInfo      `json:"current_files"`   // 当前文件列表
	CurrentStage   int             `json:"current_stage"`   // 当前执行到第几个阶段（0-indexed）
	TotalStages    int             `json:"total_stages"`    // 总阶段数
	StageResults   []StageResult   `json:"stage_results"`   // 各阶段执行结果
	Progress       int             `json:"progress"`        // 整体进度 (0-100)
	CreatedAt      time.Time       `json:"created_at"`
	StartedAt      *time.Time      `json:"started_at,omitempty"`
	CompletedAt    *time.Time      `json:"completed_at,omitempty"`
	ErrorMessage   string          `json:"error_message,omitempty"`
	CanRetry       bool            `json:"can_retry"` // 是否可以重试
}

// NewPipelineTask 创建新的管道任务
func NewPipelineTask(recordInfo RecordInfo, config *PipelineConfig, initialFiles []FileInfo) *PipelineTask {
	// 计算启用的阶段数
	totalStages := 0
	for _, stage := range config.Stages {
		if stage.IsEnabled() {
			totalStages++
		}
	}

	return &PipelineTask{
		Status:         PipelineStatusPending,
		RecordInfo:     recordInfo,
		PipelineConfig: config,
		InitialFiles:   initialFiles,
		CurrentFiles:   initialFiles,
		CurrentStage:   0,
		TotalStages:    totalStages,
		StageResults:   make([]StageResult, 0),
		Progress:       0,
		CreatedAt:      time.Now(),
		CanRetry:       true,
	}
}

// UpdateProgress 更新任务进度
func (pt *PipelineTask) UpdateProgress() {
	if pt.TotalStages == 0 {
		pt.Progress = 100
		return
	}
	pt.Progress = (pt.CurrentStage * 100) / pt.TotalStages
}

// MarkStarted 标记任务开始
func (pt *PipelineTask) MarkStarted() {
	now := time.Now()
	pt.Status = PipelineStatusRunning
	pt.StartedAt = &now
}

// MarkCompleted 标记任务完成
func (pt *PipelineTask) MarkCompleted() {
	now := time.Now()
	pt.Status = PipelineStatusCompleted
	pt.CompletedAt = &now
	pt.Progress = 100
}

// MarkFailed 标记任务失败
func (pt *PipelineTask) MarkFailed(err error) {
	now := time.Now()
	pt.Status = PipelineStatusFailed
	pt.CompletedAt = &now
	if err != nil {
		pt.ErrorMessage = err.Error()
	}
}

// MarkCancelled 标记任务取消
func (pt *PipelineTask) MarkCancelled() {
	now := time.Now()
	pt.Status = PipelineStatusCancelled
	pt.CompletedAt = &now
}

// AddStageResult 添加阶段结果
func (pt *PipelineTask) AddStageResult(result StageResult) {
	pt.StageResults = append(pt.StageResults, result)
}

// GetLastStageResult 获取最后一个阶段结果
func (pt *PipelineTask) GetLastStageResult() *StageResult {
	if len(pt.StageResults) == 0 {
		return nil
	}
	return &pt.StageResults[len(pt.StageResults)-1]
}
