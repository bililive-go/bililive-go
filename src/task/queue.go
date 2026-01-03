package task

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/bililive-go/bililive-go/src/instance"
	"github.com/bililive-go/bililive-go/src/pkg/events"
	"github.com/sirupsen/logrus"
)

// QueueManager 任务队列管理器
type QueueManager struct {
	ctx           context.Context
	cancel        context.CancelFunc
	store         Store
	config        *QueueConfig
	executors     map[TaskType]Executor
	runningTasks  map[int64]context.CancelFunc
	mu            sync.RWMutex
	wg            sync.WaitGroup
	eventDispatch events.Dispatcher
	ticker        *time.Ticker
}

// QueueConfig 队列配置
type QueueConfig struct {
	MaxConcurrent int           `yaml:"max_concurrent" mapstructure:"max_concurrent"` // 最大并发数
	PollInterval  time.Duration `yaml:"poll_interval" mapstructure:"poll_interval"`   // 轮询间隔
}

// DefaultQueueConfig 返回默认配置
func DefaultQueueConfig() *QueueConfig {
	return &QueueConfig{
		MaxConcurrent: 3,
		PollInterval:  5 * time.Second,
	}
}

// NewQueueManager 创建队列管理器
func NewQueueManager(ctx context.Context, store Store, config *QueueConfig, dispatcher events.Dispatcher) *QueueManager {
	if config == nil {
		config = DefaultQueueConfig()
	}
	if config.MaxConcurrent <= 0 {
		config.MaxConcurrent = 3
	}
	if config.PollInterval <= 0 {
		config.PollInterval = 5 * time.Second
	}

	managerCtx, cancel := context.WithCancel(ctx)

	qm := &QueueManager{
		ctx:           managerCtx,
		cancel:        cancel,
		store:         store,
		config:        config,
		executors:     make(map[TaskType]Executor),
		runningTasks:  make(map[int64]context.CancelFunc),
		eventDispatch: dispatcher,
	}

	return qm
}

// RegisterExecutor 注册任务执行器
func (qm *QueueManager) RegisterExecutor(taskType TaskType, executor Executor) {
	qm.mu.Lock()
	defer qm.mu.Unlock()
	qm.executors[taskType] = executor
}

// Start 启动队列管理器 (实现 Module 接口)
func (qm *QueueManager) Start(ctx context.Context) error {
	// 重置所有运行中的任务（处理程序非正常退出的情况）
	if err := qm.store.ResetRunningTasks(qm.ctx); err != nil {
		logrus.WithError(err).Warn("failed to reset running tasks")
	}

	// 启动轮询调度
	qm.ticker = time.NewTicker(qm.config.PollInterval)
	qm.wg.Add(1)
	go qm.pollLoop()

	logrus.Info("task queue manager started")
	return nil
}

// Close 停止队列管理器 (实现 Module 接口)
func (qm *QueueManager) Close(ctx context.Context) {
	qm.cancel()
	if qm.ticker != nil {
		qm.ticker.Stop()
	}

	// 等待所有任务完成
	qm.wg.Wait()
	qm.store.Close()
}

// pollLoop 轮询循环
func (qm *QueueManager) pollLoop() {
	defer qm.wg.Done()

	// 首次立即检查
	qm.scheduleNextTasks()

	for {
		select {
		case <-qm.ctx.Done():
			return
		case <-qm.ticker.C:
			qm.scheduleNextTasks()
		}
	}
}

// scheduleNextTasks 调度下一批任务
func (qm *QueueManager) scheduleNextTasks() {
	qm.mu.RLock()
	runningCount := len(qm.runningTasks)
	maxConcurrent := qm.config.MaxConcurrent
	qm.mu.RUnlock()

	// 检查是否还有空余槽位
	availableSlots := maxConcurrent - runningCount
	if availableSlots <= 0 {
		return
	}

	// 获取待执行的任务
	tasks, err := qm.store.GetPendingTasks(qm.ctx, availableSlots)
	if err != nil {
		logrus.WithError(err).Error("failed to get pending tasks")
		return
	}

	for _, task := range tasks {
		// 检查前置任务是否完成
		if task.PreTaskID != nil {
			preTask, err := qm.store.GetTask(qm.ctx, *task.PreTaskID)
			if err != nil || preTask.Status != TaskStatusCompleted {
				continue // 前置任务未完成，跳过
			}
		}

		qm.startTask(task)
	}
}

// startTask 启动任务执行
func (qm *QueueManager) startTask(task *Task) {
	qm.mu.Lock()
	// 检查是否已经在运行
	if _, exists := qm.runningTasks[task.ID]; exists {
		qm.mu.Unlock()
		return
	}

	// 获取执行器
	executor, exists := qm.executors[task.Type]
	if !exists {
		qm.mu.Unlock()
		logrus.WithField("task_type", task.Type).Error("no executor registered for task type")
		return
	}

	// 创建任务上下文
	taskCtx, cancel := context.WithCancel(qm.ctx)
	qm.runningTasks[task.ID] = cancel
	qm.mu.Unlock()

	// 更新任务状态为运行中
	now := time.Now()
	task.Status = TaskStatusRunning
	task.StartedAt = &now
	if err := qm.store.UpdateTask(qm.ctx, task); err != nil {
		logrus.WithError(err).Error("failed to update task status")
	}

	// 广播任务状态变化
	qm.broadcastTaskUpdate(task)

	// 异步执行任务
	qm.wg.Add(1)
	go func() {
		defer qm.wg.Done()
		qm.executeTask(taskCtx, task, executor)
	}()
}

// executeTask 执行任务
func (qm *QueueManager) executeTask(ctx context.Context, task *Task, executor Executor) {
	defer func() {
		qm.mu.Lock()
		delete(qm.runningTasks, task.ID)
		qm.mu.Unlock()
	}()

	logrus.WithFields(logrus.Fields{
		"task_id":   task.ID,
		"task_type": task.Type,
		"input":     task.InputFile,
	}).Info("starting task execution")

	// 执行任务
	err := executor.Execute(ctx, task, func(progress int) {
		task.Progress = progress
		qm.store.UpdateTask(ctx, task)
		qm.broadcastTaskUpdate(task)
	})

	now := time.Now()
	task.CompletedAt = &now

	if err != nil {
		if ctx.Err() == context.Canceled {
			task.Status = TaskStatusCancelled
			logrus.WithField("task_id", task.ID).Info("task cancelled")
		} else {
			task.Status = TaskStatusFailed
			task.ErrorMessage = err.Error()
			logrus.WithError(err).WithField("task_id", task.ID).Error("task failed")
		}
	} else {
		task.Status = TaskStatusCompleted
		task.Progress = 100
		logrus.WithField("task_id", task.ID).Info("task completed successfully")
	}

	// 更新任务状态
	if err := qm.store.UpdateTask(qm.ctx, task); err != nil {
		logrus.WithError(err).Error("failed to update task status after execution")
	}

	// 广播任务状态变化
	qm.broadcastTaskUpdate(task)

	// 清理临时文件
	if task.Status == TaskStatusCompleted || task.Status == TaskStatusFailed {
		executor.Cleanup(task)
	}
}

// broadcastTaskUpdate 广播任务更新事件
func (qm *QueueManager) broadcastTaskUpdate(task *Task) {
	if qm.eventDispatch != nil {
		qm.eventDispatch.DispatchEvent(events.NewEvent(TaskUpdateEvent, task))
	}
}

// EnqueueTask 添加任务到队列
func (qm *QueueManager) EnqueueTask(task *Task) error {
	if task.Status == "" {
		task.Status = TaskStatusPending
	}
	if task.Priority == 0 {
		task.Priority = 0 // 默认优先级
	}
	task.CanRequeue = true

	if err := qm.store.CreateTask(qm.ctx, task); err != nil {
		return fmt.Errorf("failed to create task: %w", err)
	}

	logrus.WithFields(logrus.Fields{
		"task_id":   task.ID,
		"task_type": task.Type,
		"input":     task.InputFile,
	}).Info("task enqueued")

	// 广播新任务事件
	qm.broadcastTaskUpdate(task)

	// 立即尝试调度
	go qm.scheduleNextTasks()

	return nil
}

// CancelTask 取消任务
func (qm *QueueManager) CancelTask(taskID int64) error {
	task, err := qm.store.GetTask(qm.ctx, taskID)
	if err != nil {
		return err
	}

	qm.mu.Lock()
	cancel, isRunning := qm.runningTasks[taskID]
	qm.mu.Unlock()

	if isRunning {
		// 取消正在运行的任务
		cancel()
		// 状态更新会在 executeTask 中完成
	} else if task.Status == TaskStatusPending {
		// 取消待执行的任务
		task.Status = TaskStatusCancelled
		now := time.Now()
		task.CompletedAt = &now
		if err := qm.store.UpdateTask(qm.ctx, task); err != nil {
			return err
		}
		qm.broadcastTaskUpdate(task)
	}

	return nil
}

// RequeueTask 重新排队任务
func (qm *QueueManager) RequeueTask(taskID int64) error {
	task, err := qm.store.GetTask(qm.ctx, taskID)
	if err != nil {
		return err
	}

	if !task.CanRequeue {
		return fmt.Errorf("task cannot be requeued")
	}

	// 如果任务正在运行，先取消
	qm.mu.Lock()
	cancel, isRunning := qm.runningTasks[taskID]
	qm.mu.Unlock()

	if isRunning {
		cancel()
		// 等待任务结束
		time.Sleep(100 * time.Millisecond)
	}

	// 重置任务状态
	task.Status = TaskStatusPending
	task.StartedAt = nil
	task.CompletedAt = nil
	task.ErrorMessage = ""
	task.Progress = 0

	if err := qm.store.UpdateTask(qm.ctx, task); err != nil {
		return err
	}

	qm.broadcastTaskUpdate(task)

	// 立即尝试调度
	go qm.scheduleNextTasks()

	return nil
}

// UpdatePriority 更新任务优先级
func (qm *QueueManager) UpdatePriority(taskID int64, priority int) error {
	task, err := qm.store.GetTask(qm.ctx, taskID)
	if err != nil {
		return err
	}

	if task.Status != TaskStatusPending {
		return fmt.Errorf("can only change priority of pending tasks")
	}

	if err := qm.store.UpdateTaskPriority(qm.ctx, taskID, priority); err != nil {
		return err
	}

	task.Priority = priority
	qm.broadcastTaskUpdate(task)

	return nil
}

// GetTask 获取任务详情
func (qm *QueueManager) GetTask(taskID int64) (*Task, error) {
	return qm.store.GetTask(qm.ctx, taskID)
}

// ListTasks 列出任务
func (qm *QueueManager) ListTasks(filter TaskFilter) ([]*Task, error) {
	return qm.store.ListTasks(qm.ctx, filter)
}

// DeleteTask 删除任务
func (qm *QueueManager) DeleteTask(taskID int64) error {
	// 验证任务存在
	_, err := qm.store.GetTask(qm.ctx, taskID)
	if err != nil {
		return err
	}

	// 不能删除运行中的任务
	qm.mu.RLock()
	_, isRunning := qm.runningTasks[taskID]
	qm.mu.RUnlock()

	if isRunning {
		return fmt.Errorf("cannot delete running task")
	}

	return qm.store.DeleteTask(qm.ctx, taskID)
}

// GetStats 获取队列统计信息
func (qm *QueueManager) GetStats() (*QueueStats, error) {
	stats := &QueueStats{
		MaxConcurrent: qm.config.MaxConcurrent,
	}

	qm.mu.RLock()
	stats.RunningCount = len(qm.runningTasks)
	qm.mu.RUnlock()

	// 获取各状态的任务数
	for _, status := range []TaskStatus{TaskStatusPending, TaskStatusCompleted, TaskStatusFailed, TaskStatusCancelled} {
		tasks, err := qm.store.ListTasks(qm.ctx, TaskFilter{Status: &status})
		if err != nil {
			return nil, err
		}
		switch status {
		case TaskStatusPending:
			stats.PendingCount = len(tasks)
		case TaskStatusCompleted:
			stats.CompletedCount = len(tasks)
		case TaskStatusFailed:
			stats.FailedCount = len(tasks)
		case TaskStatusCancelled:
			stats.CancelledCount = len(tasks)
		}
	}

	return stats, nil
}

// QueueStats 队列统计
type QueueStats struct {
	MaxConcurrent  int `json:"max_concurrent"`
	RunningCount   int `json:"running_count"`
	PendingCount   int `json:"pending_count"`
	CompletedCount int `json:"completed_count"`
	FailedCount    int `json:"failed_count"`
	CancelledCount int `json:"cancelled_count"`
}

// GetQueueManager 从实例获取队列管理器
func GetQueueManager(inst *instance.Instance) *QueueManager {
	if inst == nil || inst.TaskQueueManager == nil {
		return nil
	}
	return inst.TaskQueueManager.(*QueueManager)
}

// EnqueueFixFlvTask 添加 FLV 修复任务（实现 TaskEnqueuer 接口）
func (qm *QueueManager) EnqueueFixFlvTask(inputFile string) error {
	task := &Task{
		Type:      TaskTypeFixFlv,
		InputFile: inputFile,
		Status:    TaskStatusPending,
	}
	return qm.EnqueueTask(task)
}

// EnqueueConvertMp4Task 添加 MP4 转换任务（实现 TaskEnqueuer 接口）
func (qm *QueueManager) EnqueueConvertMp4Task(inputFile string, deleteOriginal bool) error {
	task := &Task{
		Type:      TaskTypeConvertMp4,
		InputFile: inputFile,
		Status:    TaskStatusPending,
		Metadata: map[string]interface{}{
			"delete_original": deleteOriginal,
		},
	}
	return qm.EnqueueTask(task)
}
