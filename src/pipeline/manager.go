package pipeline

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/bililive-go/bililive-go/src/configs"
	"github.com/bililive-go/bililive-go/src/instance"
	"github.com/bililive-go/bililive-go/src/live"
	"github.com/bililive-go/bililive-go/src/notify"
	"github.com/bililive-go/bililive-go/src/pkg/events"
	"github.com/bililive-go/bililive-go/src/pkg/livelogger"
	bilisentry "github.com/bililive-go/bililive-go/src/pkg/sentry"
	"github.com/bililive-go/bililive-go/src/types"
	"github.com/sirupsen/logrus"
)

// Manager 管道任务管理器
// 负责管理任务队列、持久化存储、并发执行控制
type Manager struct {
	ctx           context.Context
	cancel        context.CancelFunc
	store         Store
	executor      *Executor
	config        *ManagerConfig
	runningTasks  map[int64]context.CancelFunc
	taskCallbacks map[int64]func(*PipelineTask) // 按 task.ID 索引的完成回调（不经过持久化）
	mu            sync.RWMutex
	wg            sync.WaitGroup
	eventDispatch events.Dispatcher
	ticker        *time.Ticker
}

// ManagerConfig 管理器配置
type ManagerConfig struct {
	MaxConcurrent int           `yaml:"max_concurrent" json:"max_concurrent"` // 最大并发数
	PollInterval  time.Duration `yaml:"poll_interval" json:"poll_interval"`   // 轮询间隔
}

// DefaultManagerConfig 返回默认配置
func DefaultManagerConfig() *ManagerConfig {
	return &ManagerConfig{
		MaxConcurrent: 3,
		PollInterval:  5 * time.Second,
	}
}

// NewManager 创建管道管理器
func NewManager(ctx context.Context, store Store, config *ManagerConfig, dispatcher events.Dispatcher) *Manager {
	if config == nil {
		config = DefaultManagerConfig()
	}
	if config.MaxConcurrent <= 0 {
		config.MaxConcurrent = 3
	}
	if config.PollInterval <= 0 {
		config.PollInterval = 5 * time.Second
	}

	managerCtx, cancel := context.WithCancel(ctx)

	m := &Manager{
		ctx:           managerCtx,
		cancel:        cancel,
		store:         store,
		executor:      NewExecutor(logrus.StandardLogger()),
		config:        config,
		runningTasks:  make(map[int64]context.CancelFunc),
		taskCallbacks: make(map[int64]func(*PipelineTask)),
		eventDispatch: dispatcher,
	}

	return m
}

// RegisterStage 注册阶段工厂
func (m *Manager) RegisterStage(name string, factory StageFactory) {
	m.executor.RegisterStage(name, factory)
}

// Start 启动管理器（实现 Module 接口）
func (m *Manager) Start(ctx context.Context) error {
	// 重置所有运行中的任务（处理程序非正常退出的情况）
	if err := m.store.ResetRunningTasks(m.ctx); err != nil {
		logrus.WithError(err).Warn("failed to reset running pipeline tasks")
	}

	// 启动轮询调度
	m.ticker = time.NewTicker(m.config.PollInterval)
	m.wg.Add(1)
	bilisentry.Go(func() { m.pollLoop() })

	logrus.Info("pipeline manager started")
	return nil
}

// Close 停止管理器（实现 Module 接口）
func (m *Manager) Close(ctx context.Context) {
	m.cancel()
	if m.ticker != nil {
		m.ticker.Stop()
	}

	// 等待所有任务完成
	m.wg.Wait()
	m.store.Close()
}

// pollLoop 轮询循环
func (m *Manager) pollLoop() {
	defer m.wg.Done()

	// 首次立即检查
	m.scheduleNextTasks()

	for {
		select {
		case <-m.ctx.Done():
			return
		case <-m.ticker.C:
			m.scheduleNextTasks()
		}
	}
}

// scheduleNextTasks 调度下一批任务
func (m *Manager) scheduleNextTasks() {
	m.mu.RLock()
	runningCount := len(m.runningTasks)
	maxConcurrent := m.config.MaxConcurrent
	m.mu.RUnlock()

	// 检查是否还有空余槽位
	availableSlots := maxConcurrent - runningCount
	if availableSlots <= 0 {
		return
	}

	// 获取待执行的任务
	tasks, err := m.store.GetPendingTasks(m.ctx, availableSlots)
	if err != nil {
		logrus.WithError(err).Error("failed to get pending pipeline tasks")
		return
	}

	for _, task := range tasks {
		m.startTask(task)
	}
}

// startTask 启动任务执行
func (m *Manager) startTask(task *PipelineTask) {
	m.mu.Lock()
	// 检查是否已经在运行
	if _, exists := m.runningTasks[task.ID]; exists {
		m.mu.Unlock()
		return
	}

	// 创建任务上下文
	taskCtx, cancel := context.WithCancel(m.ctx)
	m.runningTasks[task.ID] = cancel
	m.mu.Unlock()

	// 更新任务状态为运行中
	task.MarkStarted()
	if err := m.store.UpdateTask(m.ctx, task); err != nil {
		logrus.WithError(err).Error("failed to update pipeline task status")
	}

	// 广播任务状态变化
	m.broadcastTaskUpdate(task)

	// 异步执行任务
	m.wg.Add(1)
	bilisentry.Go(func() {
		defer m.wg.Done()
		m.executeTask(taskCtx, task)
	})
}

// executeTask 执行任务
func (m *Manager) executeTask(ctx context.Context, task *PipelineTask) {
	defer func() {
		m.mu.Lock()
		delete(m.runningTasks, task.ID)
		m.mu.Unlock()
	}()

	logrus.WithFields(logrus.Fields{
		"task_id":       task.ID,
		"initial_files": len(task.InitialFiles),
	}).Info("starting pipeline task execution")

	// 构建执行上下文
	pipelineCtx := &PipelineContext{
		Ctx:        ctx,
		RecordInfo: task.RecordInfo,
		Logger: livelogger.New(livelogger.DefaultBufferSize, logrus.Fields{
			"platform": task.RecordInfo.Platform,
			"host":     task.RecordInfo.HostName,
			"room":     task.RecordInfo.RoomName,
		}),
		WorkDir: "", // 后续可以从配置获取
	}

	// 执行管道（从 startStage 开始，支持从指定阶段重试）
	startStage := task.CurrentStage

	// 保留目标阶段之前的结果（用于合并）
	var preservedResults []StageResult
	if startStage > 0 && len(task.StageResults) >= startStage {
		preservedResults = make([]StageResult, startStage)
		copy(preservedResults, task.StageResults[:startStage])
	}

	results, err := m.executor.Execute(
		pipelineCtx,
		task.PipelineConfig,
		task.CurrentFiles,
		startStage,
		func(stageIndex int, stageName string, status StageStatus) {
			// 更新任务进度
			task.CurrentStage = stageIndex
			task.UpdateProgress()
			if err := m.store.UpdateTask(ctx, task); err != nil {
				logrus.WithError(err).Warn("failed to update pipeline task progress")
			}
			m.broadcastTaskUpdate(task)
		},
		// 阶段完成后持久化结果，确保崩溃恢复时阶段记录完整
		func(stageIndex int, result StageResult) {
			task.CurrentFiles = result.OutputFiles
			// 追加/更新 StageResults，让崩溃恢复后 preservedResults 合并条件成立
			for len(task.StageResults) <= stageIndex {
				task.StageResults = append(task.StageResults, StageResult{})
			}
			task.StageResults[stageIndex] = result
			if err := m.store.UpdateTask(ctx, task); err != nil {
				logrus.WithError(err).Warn("failed to persist stage result")
			}
		},
	)

	// 合并：保留之前的结果 + 新执行的结果
	if preservedResults != nil {
		task.StageResults = append(preservedResults, results...)
	} else {
		task.StageResults = results
	}

	if err != nil {
		if ctx.Err() == context.Canceled {
			task.MarkCancelled()
			logrus.WithField("task_id", task.ID).Info("pipeline task cancelled")
		} else {
			task.MarkFailed(err)
			logrus.WithError(err).WithField("task_id", task.ID).Error("pipeline task failed")
		}
	} else {
		task.MarkCompleted()
		// 更新最终文件列表
		if len(results) > 0 {
			lastResult := results[len(results)-1]
			task.CurrentFiles = lastResult.OutputFiles
		}
		logrus.WithField("task_id", task.ID).Info("pipeline task completed successfully")
	}

	// 保存最后阶段输出的快照（清理前），用于回调提取上传文件详情
	task.LastStageFiles = pipelineCtx.LastStageFiles

	// 更新任务状态
	if err := m.store.UpdateTask(m.ctx, task); err != nil {
		logrus.WithError(err).Error("failed to update pipeline task status after execution")
	}

	// 广播任务状态变化
	m.broadcastTaskUpdate(task)

	// 原子地取出并删除回调，确保每个任务的回调至多触发一次
	// 避免 executeTask 和 CancelTask 并发时双发回调
	m.mu.Lock()
	callback := m.taskCallbacks[task.ID]
	delete(m.taskCallbacks, task.ID)
	m.mu.Unlock()
	if callback != nil {
		callback(task)
	}
}

// broadcastTaskUpdate 广播任务更新事件
func (m *Manager) broadcastTaskUpdate(task *PipelineTask) {
	if m.eventDispatch != nil {
		m.eventDispatch.DispatchEvent(events.NewEvent(PipelineTaskUpdateEvent, task))
	}
}

// FileInfoToDetails 将 FileInfo 切片转换为 RecordingFileDetail 切片
// 使用 FileInfo 中预存的 Size，无需再次 stat 文件
// 仅包含视频和其他类型文件，排除封面（封面不在录制摘要中展示）
func FileInfoToDetails(files []FileInfo) []notify.RecordingFileDetail {
	details := make([]notify.RecordingFileDetail, 0, len(files))
	for _, f := range files {
		if f.Size > 0 && f.Type != FileTypeCover {
			details = append(details, notify.RecordingFileDetail{
				Name: filepath.Base(f.Path),
				Size: f.Size,
			})
		}
	}
	return details
}

// EnqueueTask 添加任务到队列
// onTaskComplete: 任务完成回调（可选），在任务对调度器可见前注册
func (m *Manager) EnqueueTask(task *PipelineTask, onTaskComplete func(*PipelineTask)) error {
	// 持锁覆盖 CreateTask + 回调注册，阻塞 pollLoop 的 scheduleNextTasks（需 RLock）
	// 确保任务对调度器可见时回调已就绪
	m.mu.Lock()
	if err := m.store.CreateTask(m.ctx, task); err != nil {
		m.mu.Unlock()
		return fmt.Errorf("failed to create pipeline task: %w", err)
	}
	if onTaskComplete != nil {
		m.taskCallbacks[task.ID] = onTaskComplete
	}
	m.mu.Unlock()

	logrus.WithFields(logrus.Fields{
		"task_id":       task.ID,
		"initial_files": len(task.InitialFiles),
		"total_stages":  task.TotalStages,
	}).Info("pipeline task enqueued")

	// 广播新任务事件
	m.broadcastTaskUpdate(task)

	// 立即尝试调度
	bilisentry.Go(func() { m.scheduleNextTasks() })

	return nil
}

// EnqueueRecordingTask 创建并入队录制完成后的处理任务
// 这是 recorder.go 调用的主要入口
// onTaskComplete: 任务完成回调（可选），用于通知 recorder 任务结束
func (m *Manager) EnqueueRecordingTask(
	info *live.Info,
	pipelineConfig *PipelineConfig,
	outputFiles []string,
	onTaskComplete func(task *PipelineTask),
) error {
	// 构建文件信息列表
	files := make([]FileInfo, len(outputFiles))
	for i, path := range outputFiles {
		files[i] = NewVideoFileInfo(path)
	}

	// 创建任务
	task := NewPipelineTask(NewRecordInfo(info), pipelineConfig, files)

	// 入队并注册回调（EnqueueTask 内部在异步调度前完成注册，避免竞态窗口）
	return m.EnqueueTask(task, onTaskComplete)
}

// CancelTask 取消任务
func (m *Manager) CancelTask(taskID int64) error {
	task, err := m.store.GetTask(m.ctx, taskID)
	if err != nil {
		return err
	}

	m.mu.Lock()
	cancel, isRunning := m.runningTasks[taskID]
	m.mu.Unlock()

	if isRunning {
		// 取消正在运行的任务
		cancel()
		// 状态更新会在 executeTask 中完成
	} else if task.Status == PipelineStatusPending {
		// 取消待执行的任务
		task.MarkCancelled()
		if err := m.store.UpdateTask(m.ctx, task); err != nil {
			return err
		}
		m.broadcastTaskUpdate(task)

		// 原子地取出并删除回调，避免与 executeTask 并发时双发
		m.mu.Lock()
		callback := m.taskCallbacks[taskID]
		delete(m.taskCallbacks, taskID)
		m.mu.Unlock()
		if callback != nil {
			callback(task)
		}
	}

	return nil
}

// RetryTask 重试失败的任务
func (m *Manager) RetryTask(taskID int64) error {
	task, err := m.store.GetTask(m.ctx, taskID)
	if err != nil {
		return err
	}

	if !task.CanRetry {
		return fmt.Errorf("task cannot be retried")
	}

	// 允许 Failed/Cancelled/Completed 状态
	if task.Status != PipelineStatusFailed && task.Status != PipelineStatusCancelled && task.Status != PipelineStatusCompleted {
		return fmt.Errorf("only failed, cancelled or completed tasks can be retried")
	}

	// 校验初始文件存在（Completed 任务的文件可能已被 deleteMarkedFiles 清理）
	for _, f := range task.InitialFiles {
		if _, err := os.Stat(f.Path); os.IsNotExist(err) {
			return fmt.Errorf("initial file not found, cannot retry (may have been deleted after upload): %s", f.Path)
		}
	}

	// 重置任务状态
	task.Status = PipelineStatusPending
	task.StartedAt = nil
	task.CompletedAt = nil
	task.ErrorMessage = ""
	task.CurrentStage = 0
	task.CurrentFiles = task.InitialFiles // 重置为初始文件，从头开始重试
	task.StageResults = nil
	task.Progress = 0

	if err := m.store.UpdateTask(m.ctx, task); err != nil {
		return err
	}

	m.broadcastTaskUpdate(task)

	// 立即尝试调度
	bilisentry.Go(func() { m.scheduleNextTasks() })

	return nil
}

// RetryTaskFromStage 从指定阶段开始重试任务
// 允许 Failed/Cancelled/Completed 状态的任务
// stageName: 要从哪个阶段开始重试（如 "cloud_upload"）
func (m *Manager) RetryTaskFromStage(taskID int64, stageName string) error {
	task, err := m.store.GetTask(m.ctx, taskID)
	if err != nil {
		return err
	}

	if !task.CanRetry {
		return fmt.Errorf("task cannot be retried")
	}

	// 允许 Failed/Cancelled/Completed 状态
	if task.Status != PipelineStatusFailed &&
		task.Status != PipelineStatusCancelled &&
		task.Status != PipelineStatusCompleted {
		return fmt.Errorf("only failed, cancelled or completed tasks can be retried")
	}

	// 找到目标阶段在启用阶段中的索引
	targetStageIndex := -1
	stageIndex := 0
	for _, stageCfg := range task.PipelineConfig.Stages {
		if !stageCfg.IsEnabled() {
			continue
		}
		if stageCfg.Name == stageName {
			targetStageIndex = stageIndex
			break
		}
		stageIndex++
	}
	if targetStageIndex == -1 {
		return fmt.Errorf("stage %s not found in pipeline", stageName)
	}

	// 校验：目标阶段之前的所有阶段必须已完成
	if targetStageIndex > 0 {
		for i := 0; i < targetStageIndex; i++ {
			if i >= len(task.StageResults) {
				return fmt.Errorf("stage %d has not been executed, cannot retry from stage %s", i, stageName)
			}
			if task.StageResults[i].Status != StageStatusCompleted {
				return fmt.Errorf("stage %d (%s) is not completed (status: %s), cannot retry from stage %s",
					i, task.StageResults[i].StageName, task.StageResults[i].Status, stageName)
			}
		}
	}

	// 获取目标阶段的输入文件
	var inputFiles []FileInfo
	if targetStageIndex < len(task.StageResults) {
		// 从已有的 StageResult 中取输入文件
		inputFiles = task.StageResults[targetStageIndex].InputFiles
	} else if targetStageIndex > 0 && targetStageIndex-1 < len(task.StageResults) {
		// 使用上一阶段的输出文件
		inputFiles = task.StageResults[targetStageIndex-1].OutputFiles
	} else if targetStageIndex == 0 {
		// 从头开始，使用初始文件
		inputFiles = task.InitialFiles
	}

	// 过滤已清理的中间文件，保留仍在磁盘上的文件
	// deleteMarkedFiles 仅在管道全部成功后执行，失败/取消时文件仍在磁盘但带有标记
	// 因此需要结合磁盘实际存在性判断：标记但仍在 → 保留（管道未清理），标记且已删 → 跳过
	var activeFiles []FileInfo
	for _, f := range inputFiles {
		isDeletable := f.Deletable
		isUploaded := false
		if f.Metadata != nil {
			if uploaded, ok := f.Metadata["uploaded"].(bool); ok && uploaded {
				isUploaded = true
			}
		}

		if _, err := os.Stat(f.Path); os.IsNotExist(err) {
			if isDeletable || isUploaded {
				continue // 已被 deleteMarkedFiles 正常清理，跳过
			}
			return fmt.Errorf("input file not found, cannot retry: %s", f.Path)
		}
		activeFiles = append(activeFiles, f)
	}
	inputFiles = activeFiles

	// 校验输入文件不为空
	if len(inputFiles) == 0 {
		return fmt.Errorf("no input files available for stage %s, cannot retry", stageName)
	}

	// 重置任务状态
	task.Status = PipelineStatusPending
	task.StartedAt = nil
	task.CompletedAt = nil
	task.ErrorMessage = ""
	task.CurrentStage = targetStageIndex
	task.CurrentFiles = inputFiles
	// 保留目标阶段之前的结果，清除目标阶段及之后的结果
	if targetStageIndex < len(task.StageResults) {
		task.StageResults = task.StageResults[:targetStageIndex]
	}
	task.UpdateProgress()

	if err := m.store.UpdateTask(m.ctx, task); err != nil {
		return err
	}

	m.broadcastTaskUpdate(task)

	// 立即尝试调度
	bilisentry.Go(func() { m.scheduleNextTasks() })

	return nil
}

// GetTask 获取任务详情
func (m *Manager) GetTask(taskID int64) (*PipelineTask, error) {
	return m.store.GetTask(m.ctx, taskID)
}

// ListTasks 列出任务
func (m *Manager) ListTasks(filter TaskFilter) ([]*PipelineTask, error) {
	return m.store.ListTasks(m.ctx, filter)
}

// DeleteTask 删除任务
func (m *Manager) DeleteTask(taskID int64) error {
	// 不能删除运行中的任务
	m.mu.RLock()
	_, isRunning := m.runningTasks[taskID]
	m.mu.RUnlock()

	if isRunning {
		return fmt.Errorf("cannot delete running task")
	}

	return m.store.DeleteTask(m.ctx, taskID)
}

// ClearCompletedTasks 清除所有已完成的任务
func (m *Manager) ClearCompletedTasks() (int, error) {
	return m.store.DeleteTasksByStatus(m.ctx, PipelineStatusCompleted)
}

// GetStats 获取队列统计信息
func (m *Manager) GetStats() (*ManagerStats, error) {
	stats := &ManagerStats{
		MaxConcurrent: m.config.MaxConcurrent,
	}

	m.mu.RLock()
	stats.RunningCount = len(m.runningTasks)
	m.mu.RUnlock()

	// 获取各状态的任务数
	for _, status := range []PipelineStatus{
		PipelineStatusPending,
		PipelineStatusCompleted,
		PipelineStatusFailed,
		PipelineStatusCancelled,
	} {
		tasks, err := m.store.ListTasks(m.ctx, TaskFilter{Status: &status})
		if err != nil {
			return nil, err
		}
		switch status {
		case PipelineStatusPending:
			stats.PendingCount = len(tasks)
		case PipelineStatusCompleted:
			stats.CompletedCount = len(tasks)
		case PipelineStatusFailed:
			stats.FailedCount = len(tasks)
		case PipelineStatusCancelled:
			stats.CancelledCount = len(tasks)
		}
	}

	return stats, nil
}

// ManagerStats 管理器统计信息
type ManagerStats struct {
	MaxConcurrent  int `json:"max_concurrent"`
	RunningCount   int `json:"running_count"`
	PendingCount   int `json:"pending_count"`
	CompletedCount int `json:"completed_count"`
	FailedCount    int `json:"failed_count"`
	CancelledCount int `json:"cancelled_count"`
}

// EnqueueUploadTask 创建手动上传的 Pipeline 任务并入队
// 行为与自动上传（CloudUploadStage）完全一致：使用相同的路径模板、配置选项
// 每个文件创建一个独立的单阶段 cloud_upload 任务
// absPaths: 已校验的绝对路径列表（调用方应先通过 getSafePath 校验）
func (m *Manager) EnqueueUploadTask(absPaths []string) (enqueued int, skipped []string, taskIDs []int64, err error) {
	config := configs.GetCurrentConfig()
	if config == nil {
		return 0, nil, nil, fmt.Errorf("配置未初始化")
	}

	cu := config.OnRecordFinished.CloudUpload
	if !cu.Enable {
		return 0, nil, nil, fmt.Errorf("云上传功能未启用，请先在设置中开启")
	}
	if cu.StorageName == "" {
		return 0, nil, nil, fmt.Errorf("未配置存储名称")
	}

	// 构建单阶段 cloud_upload PipelineConfig，与 ConvertLegacyConfig 一致
	uploadConfig := &PipelineConfig{
		Stages: []StageConfig{
			{
				Name: StageNameCloudUpload,
				Options: map[string]any{
					OptionStorage:         cu.StorageName,
					OptionPathTemplate:    cu.UploadPathTmpl,
					OptionDeleteAfter:     cu.DeleteAfterUpload,
					OptionDeleteAllAfter:  cu.DeleteAllAfterUpload,
					OptionUploadTiming:    "after_process", // 手动上传无后续处理阶段，始终允许删除标记（不继承全局 upload_timing）
					OptionFileTypes:       []string{string(FileTypeVideo), string(FileTypeCover)},
					OptionUploadSubtitles: cu.UploadSubtitles,
				},
			},
		},
	}

	outputPath, _ := filepath.Abs(config.OutPutPath)
	skipped = []string{}

	for _, absPath := range absPaths {
		// 检查文件存在
		info, statErr := os.Stat(absPath)
		if statErr != nil {
			skipped = append(skipped, filepath.Base(absPath)+" - 文件不存在或无法访问")
			continue
		}
		if info.IsDir() {
			skipped = append(skipped, filepath.Base(absPath)+" - 不支持上传文件夹")
			continue
		}

		// 从相对路径推断 RecordInfo（用于上传路径模板渲染）
		relPath, _ := filepath.Rel(outputPath, absPath)
		platform, hostName := inferUploadPathInfo(relPath)

		recordInfo := RecordInfo{
			LiveID:      types.LiveID(ManualUploadLiveID),
			Platform:    platform,               // 用于上传路径模板：{{ .Platform }}
			HostName:    hostName,               // 用于上传路径模板：{{ .HostName }}
			RoomName:    "",                     // 手动上传无房间名，不影响路径模板
			DisplayName: filepath.Base(absPath), // 任务列表展示文件名
			StartTime:   info.ModTime(),
		}

		files := []FileInfo{NewVideoFileInfo(absPath)}
		task := NewPipelineTask(recordInfo, uploadConfig, files)

		if enqueueErr := m.EnqueueTask(task, nil); enqueueErr != nil {
			skipped = append(skipped, filepath.Base(absPath)+" - 入队失败: "+enqueueErr.Error())
			continue
		}

		enqueued++
		taskIDs = append(taskIDs, task.ID)
	}

	return enqueued, skipped, taskIDs, nil
}

// bracketPattern 匹配文件名中的 [xxx] 模式
var bracketPattern = regexp.MustCompile(`\[([^\]]*)\]`)

// inferUploadPathInfo 从文件相对路径推断平台和主播名
// 两级策略：优先从文件名解析（[date][HostName][RoomName].flv），回退到目录结构推断
func inferUploadPathInfo(relPath string) (platform, hostName string) {
	slashPath := filepath.ToSlash(relPath)

	// 策略 1：从文件名解析 HostName（文件名格式通常是 [date][HostName][RoomName].flv）
	fileName := filepath.Base(slashPath)
	brackets := bracketPattern.FindAllStringSubmatch(fileName, -1)
	if len(brackets) >= 2 {
		hostName = brackets[1][1] // 第二个方括号通常是主播名
	}

	// 策略 2：从目录结构推断 Platform，以及作为 HostName 的兜底
	parts := strings.Split(slashPath, "/")
	if len(parts) >= 2 {
		platform = parts[0]
		if hostName == "" {
			hostName = parts[1]
		}
	}

	return platform, hostName
}

// GetManager 从实例获取管道管理器
func GetManager(inst *instance.Instance) *Manager {
	if inst == nil || inst.PipelineManager == nil {
		return nil
	}
	return inst.PipelineManager.(*Manager)
}
