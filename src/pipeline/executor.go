package pipeline

import (
	"context"
	"fmt"
	"sync"
	"time"

	bilisentry "github.com/bililive-go/bililive-go/src/pkg/sentry"
	"github.com/sirupsen/logrus"
)

// Executor 管道执行器
type Executor struct {
	factories map[string]StageFactory // 已注册的阶段工厂
	mu        sync.RWMutex
	logger    logrus.FieldLogger
}

// NewExecutor 创建管道执行器
func NewExecutor(logger logrus.FieldLogger) *Executor {
	if logger == nil {
		logger = logrus.StandardLogger()
	}
	return &Executor{
		factories: make(map[string]StageFactory),
		logger:    logger,
	}
}

// RegisterStage 注册阶段工厂
func (e *Executor) RegisterStage(name string, factory StageFactory) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.factories[name] = factory
	e.logger.WithField("stage", name).Debug("registered pipeline stage")
}

// getFactory 获取阶段工厂
func (e *Executor) getFactory(name string) (StageFactory, bool) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	f, ok := e.factories[name]
	return f, ok
}

// Execute 执行管道
func (e *Executor) Execute(
	ctx *PipelineContext,
	config *PipelineConfig,
	initialFiles []FileInfo,
	onProgress func(stageIndex int, stageName string, status StageStatus),
) ([]StageResult, error) {
	if config == nil || len(config.Stages) == 0 {
		e.logger.Debug("pipeline config is empty, skipping")
		return nil, nil
	}

	files := initialFiles
	results := make([]StageResult, 0, len(config.Stages))
	stageIndex := 0

	for i, stageCfg := range config.Stages {
		// 检查上下文是否已取消
		if ctx.Ctx.Err() != nil {
			return results, ctx.Ctx.Err()
		}

		// 检查是否启用
		if !stageCfg.IsEnabled() {
			e.logger.WithField("stage", stageCfg.Name).Debug("stage disabled, skipping")
			continue
		}

		// 记录开始
		if onProgress != nil {
			onProgress(stageIndex, stageCfg.Name, StageStatusRunning)
		}

		var output []FileInfo
		var err error
		var commands []string
		var logs string

		// 并行阶段处理
		if stageCfg.IsParallel() {
			e.logger.WithField("stage_index", i).Debug("executing parallel stages")
			output, commands, logs, err = e.executeParallel(ctx, stageCfg.Parallel, files)
		} else {
			e.logger.WithFields(logrus.Fields{
				"stage_index": i,
				"stage_name":  stageCfg.Name,
				"input_count": len(files),
			}).Debug("executing stage")
			output, commands, logs, err = e.executeStage(ctx, stageCfg, files)
		}

		// 记录结果
		result := StageResult{
			StageName:  stageCfg.Name,
			StageIndex: stageIndex,
			InputFiles: files,
			Commands:   commands,
			Logs:       logs,
		}
		result.StartedAt = getTimeNow()

		if err != nil {
			result.Status = StageStatusFailed
			result.ErrorMessage = err.Error()
			now := getTimeNow()
			result.CompletedAt = &now
			results = append(results, result)

			if onProgress != nil {
				onProgress(stageIndex, stageCfg.Name, StageStatusFailed)
			}

			return results, fmt.Errorf("stage %s failed: %w", stageCfg.Name, err)
		}

		result.Status = StageStatusCompleted
		result.OutputFiles = output
		now := getTimeNow()
		result.CompletedAt = &now
		results = append(results, result)

		if onProgress != nil {
			onProgress(stageIndex, stageCfg.Name, StageStatusCompleted)
		}

		// 更新文件列表给下一阶段
		files = output
		stageIndex++

		e.logger.WithFields(logrus.Fields{
			"stage_name":   stageCfg.Name,
			"output_count": len(output),
		}).Debug("stage completed")
	}

	return results, nil
}

// executeStage 执行单个阶段
func (e *Executor) executeStage(
	ctx *PipelineContext,
	stageCfg StageConfig,
	input []FileInfo,
) (output []FileInfo, commands []string, logs string, err error) {
	// 获取工厂
	factory, ok := e.getFactory(stageCfg.Name)
	if !ok {
		return nil, nil, "", fmt.Errorf("unknown stage: %s", stageCfg.Name)
	}

	// 创建阶段实例
	stage, err := factory(stageCfg)
	if err != nil {
		return nil, nil, "", fmt.Errorf("failed to create stage %s: %w", stageCfg.Name, err)
	}

	// 执行阶段
	output, err = stage.Execute(ctx, input)
	if err != nil {
		return nil, nil, "", err
	}

	// 如果阶段实现了 CommandRecorder 接口，获取命令记录
	if cr, ok := stage.(CommandRecorder); ok {
		commands = cr.GetCommands()
		logs = cr.GetLogs()
	}

	return output, commands, logs, nil
}

// executeParallel 并行执行多个阶段
func (e *Executor) executeParallel(
	ctx *PipelineContext,
	stages []StageConfig,
	input []FileInfo,
) (output []FileInfo, commands []string, logs string, err error) {
	if len(stages) == 0 {
		return input, nil, "", nil
	}

	type parallelResult struct {
		index    int
		output   []FileInfo
		commands []string
		logs     string
		err      error
	}

	results := make(chan parallelResult, len(stages))
	var wg sync.WaitGroup

	for i, stageCfg := range stages {
		if !stageCfg.IsEnabled() {
			continue
		}

		wg.Add(1)
		bilisentry.Go(func() {
			defer wg.Done()

			// 每个并行分支使用相同的输入
			out, cmds, lg, err := e.executeStage(ctx, stageCfg, input)
			results <- parallelResult{
				index:    i,
				output:   out,
				commands: cmds,
				logs:     lg,
				err:      err,
			}
		})
	}

	// 等待所有并行阶段完成
	bilisentry.Go(func() {
		wg.Wait()
		close(results)
	})

	// 收集结果
	var allOutputs []FileInfo
	var allCommands []string
	var allLogs string
	var firstErr error

	for result := range results {
		if result.err != nil && firstErr == nil {
			firstErr = result.err
			e.logger.WithError(result.err).WithField("parallel_index", result.index).Error("parallel stage failed")
		}
		allOutputs = append(allOutputs, result.output...)
		allCommands = append(allCommands, result.commands...)
		if result.logs != "" {
			if allLogs != "" {
				allLogs += "\n---\n"
			}
			allLogs += result.logs
		}
	}

	if firstErr != nil {
		return nil, allCommands, allLogs, firstErr
	}

	// 去重输出文件（并行阶段可能输入相同的文件）
	output = deduplicateFiles(allOutputs)

	return output, allCommands, allLogs, nil
}

// CommandRecorder 命令记录接口（可选实现）
type CommandRecorder interface {
	GetCommands() []string
	GetLogs() string
}

// deduplicateFiles 去重文件列表
func deduplicateFiles(files []FileInfo) []FileInfo {
	seen := make(map[string]bool)
	result := make([]FileInfo, 0, len(files))
	for _, f := range files {
		if !seen[f.Path] {
			seen[f.Path] = true
			result = append(result, f)
		}
	}
	return result
}

// getTimeNow 获取当前时间（可用于测试时 mock）
var getTimeNow = func() time.Time {
	return time.Now()
}

// ExecuteAsync 异步执行管道（返回立即，结果通过回调获取）
func (e *Executor) ExecuteAsync(
	ctx *PipelineContext,
	config *PipelineConfig,
	initialFiles []FileInfo,
	onProgress func(stageIndex int, stageName string, status StageStatus),
	onComplete func(results []StageResult, err error),
) {
	bilisentry.GoWithContext(ctx.Ctx, func(goCtx context.Context) {
		// 更新上下文
		ctx.Ctx = goCtx
		results, err := e.Execute(ctx, config, initialFiles, onProgress)
		if onComplete != nil {
			onComplete(results, err)
		}
	})
}

// ValidateConfig 验证管道配置
func (e *Executor) ValidateConfig(config *PipelineConfig) error {
	if config == nil {
		return nil
	}

	for i, stage := range config.Stages {
		if stage.IsParallel() {
			// 验证并行阶段
			for j, ps := range stage.Parallel {
				if ps.Name == "" {
					return fmt.Errorf("parallel stage[%d][%d] has no name", i, j)
				}
				if _, ok := e.getFactory(ps.Name); !ok {
					return fmt.Errorf("unknown parallel stage[%d][%d]: %s", i, j, ps.Name)
				}
			}
		} else {
			if stage.Name == "" {
				return fmt.Errorf("stage[%d] has no name", i)
			}
			if _, ok := e.getFactory(stage.Name); !ok {
				return fmt.Errorf("unknown stage[%d]: %s", i, stage.Name)
			}
		}
	}

	return nil
}
