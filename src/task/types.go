// Package task 实现后处理任务队列系统
package task

import (
	"context"
	"time"

	"github.com/bililive-go/bililive-go/src/pkg/events"
	"github.com/bililive-go/bililive-go/src/types"
)

// TaskUpdateEvent 任务更新事件
const TaskUpdateEvent events.EventType = "TaskUpdate"

// TaskType 任务类型
type TaskType string

const (
	// TaskTypeFixFlv 修复FLV文件
	TaskTypeFixFlv TaskType = "fix_flv"
	// TaskTypeConvertMp4 转换为MP4格式
	TaskTypeConvertMp4 TaskType = "convert_mp4"
)

// TaskStatus 任务状态
type TaskStatus string

const (
	// TaskStatusPending 等待执行
	TaskStatusPending TaskStatus = "pending"
	// TaskStatusRunning 正在执行
	TaskStatusRunning TaskStatus = "running"
	// TaskStatusCompleted 已完成
	TaskStatusCompleted TaskStatus = "completed"
	// TaskStatusFailed 执行失败
	TaskStatusFailed TaskStatus = "failed"
	// TaskStatusCancelled 已取消
	TaskStatusCancelled TaskStatus = "cancelled"
)

// Task 任务信息
type Task struct {
	ID           int64                  `json:"id"`            // 任务ID
	Type         TaskType               `json:"type"`          // 任务类型
	Status       TaskStatus             `json:"status"`        // 任务状态
	Priority     int                    `json:"priority"`      // 优先级（数值越大越优先）
	InputFile    string                 `json:"input_file"`    // 输入文件路径
	OutputFile   string                 `json:"output_file"`   // 输出文件路径
	TempFiles    []string               `json:"temp_files"`    // 临时文件列表（用于清理）
	LiveID       types.LiveID           `json:"live_id"`       // 关联的直播间ID
	RoomName     string                 `json:"room_name"`     // 直播间名称
	HostName     string                 `json:"host_name"`     // 主播名称
	Platform     string                 `json:"platform"`      // 平台名称
	PreTaskID    *int64                 `json:"pre_task_id"`   // 前置任务ID
	PostTaskID   *int64                 `json:"post_task_id"`  // 后置任务ID
	Metadata     map[string]interface{} `json:"metadata"`      // 额外元数据
	CreatedAt    time.Time              `json:"created_at"`    // 创建时间
	StartedAt    *time.Time             `json:"started_at"`    // 开始执行时间
	CompletedAt  *time.Time             `json:"completed_at"`  // 完成时间
	ErrorMessage string                 `json:"error_message"` // 错误信息
	Progress     int                    `json:"progress"`      // 进度（0-100）
	CanRequeue   bool                   `json:"can_requeue"`   // 是否可以重新排队（输入文件是否还存在）
	Commands     []string               `json:"commands"`      // 执行的命令列表
	Logs         string                 `json:"logs"`          // 执行日志/备注信息
}

// Executor 任务执行器接口
type Executor interface {
	// Execute 执行任务
	// ctx 用于取消任务
	// onProgress 用于报告进度
	Execute(ctx context.Context, task *Task, onProgress func(progress int)) error

	// Cleanup 清理任务产生的临时文件
	Cleanup(task *Task)
}

// TaskQueueConfig 任务队列配置
type TaskQueueConfig struct {
	// MaxConcurrent 最大并发任务数
	MaxConcurrent int `yaml:"max_concurrent" json:"max_concurrent"`
}

// DefaultTaskQueueConfig 返回默认配置
func DefaultTaskQueueConfig() TaskQueueConfig {
	return TaskQueueConfig{
		MaxConcurrent: 3,
	}
}
