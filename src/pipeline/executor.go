package pipeline

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/bililive-go/bililive-go/src/configs"
	"github.com/bililive-go/bililive-go/src/pkg/metadata"
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
// startStage: 从第几个启用的阶段开始执行（0=全部执行，用于重试指定阶段）
// onStageComplete: 每个阶段成功完成后的回调，用于持久化进度（崩溃恢复的关键）
func (e *Executor) Execute(
	ctx *PipelineContext,
	config *PipelineConfig,
	initialFiles []FileInfo,
	startStage int,
	onProgress func(stageIndex int, stageName string, status StageStatus),
	onStageComplete func(stageIndex int, result StageResult),
) ([]StageResult, error) {
	if config == nil || len(config.Stages) == 0 {
		e.logger.Debug("pipeline config is empty, skipping")
		return nil, nil
	}

	files := initialFiles
	results := make([]StageResult, 0, len(config.Stages))
	ctx.UploadedPaths = make(map[string]bool)
	stageIndex := 0

	for i, stageCfg := range config.Stages {
		// 检查上下文是否已取消
		if ctx.Ctx.Err() != nil {
			e.logger.Warnf("pipeline cancelled at stage %s, preserving all files", stageCfg.Name)
			return results, ctx.Ctx.Err()
		}

		// 检查是否启用
		if !stageCfg.IsEnabled() {
			e.logger.WithField("stage", stageCfg.Name).Debug("stage disabled, skipping")
			continue
		}

		// 跳过 startStage 之前的阶段（用于从指定阶段重试）
		if stageIndex < startStage {
			stageIndex++
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

		// 记录阶段开始时间
		stageStartTime := getTimeNow()

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

		// 回填新产出文件的 Size（阶段内构造 FileInfo 时可能未 stat）
		backfillFileSizes(output)

		// 记录结果
		result := StageResult{
			StageName:  stageCfg.Name,
			StageIndex: stageIndex,
			InputFiles: files,
			Commands:   commands,
			Logs:       logs,
		}
		result.StartedAt = stageStartTime

		if err != nil {
			result.Status = StageStatusFailed
			result.ErrorMessage = err.Error()
			now := getTimeNow()
			result.CompletedAt = &now
			results = append(results, result)

			if onProgress != nil {
				onProgress(stageIndex, stageCfg.Name, StageStatusFailed)
			}

			e.logger.Warnf("pipeline failed at stage %s, preserving all files", stageCfg.Name)
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

		// 通知外部持久化阶段完成状态（用于崩溃恢复）
		if onStageComplete != nil {
			onStageComplete(stageIndex, result)
		}

		// 更新文件列表给下一阶段
		files = output
		stageIndex++

		e.logger.WithFields(logrus.Fields{
			"stage_name":   stageCfg.Name,
			"output_count": len(output),
		}).Debug("stage completed")
	}

	// 去重：多个阶段可能添加同名文件（如 cloud_upload 上传 .ass 后，burn 又添加 Deletable 副本）
	// 保留第一个出现的（保留最早阶段的 Metadata，如 uploaded 标记）
	seen := map[string]int{}
	var deduped []FileInfo
	for _, f := range files {
		if idx, exists := seen[f.Path]; exists {
			// 合并 Deletable 标记到已有条目
			if f.Deletable && !deduped[idx].Deletable {
				deduped[idx].Deletable = true
			}
			continue
		}
		seen[f.Path] = len(deduped)
		deduped = append(deduped, f)
	}
	files = deduped

	// 重新标记被后续阶段替换的已上传文件
	// immediate 模式下 cloud_upload 最先执行，后续阶段（fix_flv）
	// 创建新 FileInfo 替换原始文件，丢失 Metadata["uploaded"]。
	// 通过 UploadedPaths 按基名+扩展名匹配，为同类型的替换文件恢复上传标记。
	// 不匹配不同扩展名的派生文件（如 .flv 上传后生成的 .mp4/.mkv 不应标记为 uploaded）。
	if len(ctx.UploadedPaths) > 0 {
		for uploadedPath := range ctx.UploadedPaths {
			uploadedBase := strings.TrimSuffix(filepath.Base(uploadedPath), filepath.Ext(uploadedPath))
			uploadedExt := strings.ToLower(filepath.Ext(uploadedPath))
			for i, f := range files {
				if f.Metadata != nil {
					if u, ok := f.Metadata["uploaded"].(bool); ok && u {
						continue // 已有标记，跳过
					}
				}
				ext := strings.ToLower(filepath.Ext(f.Path))
				if ext != uploadedExt {
					continue // 不同扩展名，不是直接替换
				}
				fileBase := strings.TrimSuffix(filepath.Base(f.Path), ext)
				if fileBase == uploadedBase {
					if files[i].Metadata == nil {
						files[i].Metadata = map[string]any{}
					}
					files[i].Metadata["uploaded"] = true
					e.logger.Infof("恢复上传标记: %s (匹配 %s)", f.Path, filepath.Base(uploadedPath))
				}
			}
		}
	}

	// 全部成功：执行延迟删除，并让最终结果反映磁盘上的文件
	// 如果上下文已取消，跳过删除以保留所有文件，返回取消错误让任务标记为 Cancelled
	if ctx.Ctx.Err() != nil {
		e.logger.Warn("pipeline cancelled after completion, skipping file cleanup")
		if len(results) > 0 {
			results[len(results)-1].OutputFiles = files
		}
		return results, ctx.Ctx.Err()
	}
	// 保存最后阶段输出的快照（用于回调提取上传文件详情），再执行清理
	// 必须深拷贝 Metadata，因为 deleteMarkedFiles 会清除 Metadata["uploaded"]，
	// FileInfo.Metadata 是 map（引用类型），浅拷贝会共享同一 map
	snapshot := make([]FileInfo, len(files))
	for i, f := range files {
		cp := f
		if f.Metadata != nil {
			cp.Metadata = make(map[string]any, len(f.Metadata))
			for k, v := range f.Metadata {
				cp.Metadata[k] = v
			}
		}
		snapshot[i] = cp
	}
	ctx.LastStageFiles = snapshot
	keptFiles := e.deleteMarkedFiles(files)
	if len(results) > 0 {
		results[len(results)-1].OutputFiles = keptFiles
	}

	return results, nil
}

// deleteMarkedFiles 删除标记为 Deletable 或 Metadata["uploaded"] 的文件，返回保留的文件列表
func (e *Executor) deleteMarkedFiles(files []FileInfo) []FileInfo {
	var kept []FileInfo
	deleteAll := false   // 标记是否为 deleteAll 模式
	deleteAfter := false // 标记是否为 deleteAfter 模式（仅删除已上传文件）

	// 获取输出路径用于计算相对路径（清理 DB 上传标记）
	cfg := configs.GetCurrentConfig()

	for _, f := range files {
		// 检查删除模式（CloudUploadStage 标记）
		if f.Metadata != nil {
			if da, ok := f.Metadata["delete_all"].(bool); ok && da {
				deleteAll = true
			}
			if uploaded, ok := f.Metadata["uploaded"].(bool); ok && uploaded {
				deleteAfter = true
			}
		}

		// 判断是否应删除：Deletable 标记 或 CloudUploadStage 标记的已上传文件
		shouldDelete := f.Deletable
		if !shouldDelete && f.Metadata != nil {
			if uploaded, ok := f.Metadata["uploaded"].(bool); ok && uploaded {
				shouldDelete = true
			}
		}

		if shouldDelete {
			if err := os.Remove(f.Path); err != nil {
				if !os.IsNotExist(err) {
					e.logger.Warnf("pipeline cleanup: 删除文件失败 %s: %v", f.Path, err)
					kept = append(kept, f) // 删除失败，文件仍在磁盘，保留记录
				}
			} else {
				e.logger.Infof("pipeline cleanup: 已删除 %s", f.Path)
				// 清除 DB 中的上传标记
				if cfg != nil {
					outPutPath, _ := filepath.Abs(cfg.OutPutPath)
					absFilePath, _ := filepath.Abs(f.Path)
					if relPath, relErr := filepath.Rel(outPutPath, absFilePath); relErr == nil {
						if delErr := metadata.GetStore().Delete(context.Background(), metadata.NamespaceUploaded, filepath.ToSlash(relPath)); delErr != nil {
							e.logger.Warnf("pipeline cleanup: 清除上传标记失败 %s: %v", f.Path, delErr)
						}
					}
				}
			}
		} else {
			kept = append(kept, f)
		}
	}

	// 清理无关联视频文件的孤立 .ass 文件
	// deleteAll 模式下删除所有文件（含 .ass），deleteAfter 模式下删除已上传视频后也应清理残留 .ass
	if deleteAll || deleteAfter {
		kept = e.cleanupOrphanedAssFiles(files, kept)
	}

	return kept
}

// cleanupOrphanedAssFiles 清理无关联视频文件的 .ass 文件
// allFiles: 原始文件列表（含已删除的视频），用于确定扫描目录
// keptFiles: 保留的文件列表，用于检查 Pipeline 内的 .ass 文件
func (e *Executor) cleanupOrphanedAssFiles(allFiles []FileInfo, keptFiles []FileInfo) []FileInfo {
	// 从原始文件列表收集所有视频文件目录（即使视频已被删除）
	videoBaseNames := map[string]bool{}
	dirs := map[string]bool{}
	for _, f := range allFiles {
		ext := strings.ToLower(filepath.Ext(f.Path))
		if ext == ".flv" || ext == ".mp4" || ext == ".mkv" || ext == ".ts" {
			dir := filepath.Dir(f.Path)
			base := strings.TrimSuffix(filepath.Base(f.Path), ext)
			videoBaseNames[filepath.Join(dir, base)] = true
			dirs[dir] = true
		}
	}

	// 收集 Pipeline 中已知的 .ass 文件路径
	knownAssFiles := map[string]bool{}
	for _, f := range keptFiles {
		if strings.ToLower(filepath.Ext(f.Path)) == ".ass" {
			knownAssFiles[f.Path] = true
		}
	}

	// 扫描目录中的 .ass 文件
	// 通过视频基名匹配判断 .ass 是否属于本任务，避免误删其他任务或用户放置的字幕
	for dir := range dirs {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			if entry.IsDir() {
				continue
			}
			if strings.ToLower(filepath.Ext(entry.Name())) != ".ass" {
				continue
			}
			assPath := filepath.Join(dir, entry.Name())
			if knownAssFiles[assPath] {
				continue // 已在 Pipeline 中处理
			}
			// 通过基名匹配判断是否属于本任务的视频文件
			// （.ass 文件可能未被传入 Pipeline，不能依赖文件列表过滤）
			base := strings.TrimSuffix(entry.Name(), ".ass")
			key := filepath.Join(dir, base)
			if !videoBaseNames[key] {
				continue // 基名不匹配任何本任务视频，跳过
			}
			// 检查磁盘上是否有同名视频文件
			hasVideo := false
			for _, ext := range []string{".flv", ".mp4", ".mkv", ".ts"} {
				if _, err := os.Stat(key + ext); err == nil {
					hasVideo = true
					break
				}
			}
			if !hasVideo {
				if err := os.Remove(assPath); err != nil {
					if !os.IsNotExist(err) {
						e.logger.Warnf("pipeline cleanup: 删除孤立 .ass 文件失败 %s: %v", assPath, err)
					}
				} else {
					e.logger.Infof("pipeline cleanup: 已删除孤立 .ass 文件 %s（无关联视频）", assPath)
				}
			}
		}
	}

	// 处理 Pipeline 中的 .ass 文件
	var kept []FileInfo
	for _, f := range keptFiles {
		ext := strings.ToLower(filepath.Ext(f.Path))
		if ext == ".ass" {
			// 先检查 .ass 是否还在磁盘上（可能已被 deleteMarkedFiles 删除）
			if _, err := os.Stat(f.Path); os.IsNotExist(err) {
				continue // 已被删除，不加入 kept
			}
			dir := filepath.Dir(f.Path)
			base := strings.TrimSuffix(filepath.Base(f.Path), ".ass")
			key := filepath.Join(dir, base)
			if !videoBaseNames[key] {
				// 无关联视频文件，删除 .ass
				if err := os.Remove(f.Path); err != nil {
					if !os.IsNotExist(err) {
						e.logger.Warnf("pipeline cleanup: 删除孤立 .ass 文件失败 %s: %v", f.Path, err)
					}
					kept = append(kept, f) // 删除失败则保留
				} else {
					e.logger.Infof("pipeline cleanup: 已删除孤立 .ass 文件 %s（无关联视频）", f.Path)
				}
			} else {
				kept = append(kept, f)
			}
		} else {
			kept = append(kept, f)
		}
	}

	return kept
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

// backfillFileSizes 对 Size 为 0 的文件执行 os.Stat 回填大小
// 阶段内构造 FileInfo 时可能未 stat，导致摘要通知漏掉这些文件
func backfillFileSizes(files []FileInfo) {
	for i := range files {
		if files[i].Size == 0 {
			if fi, err := os.Stat(files[i].Path); err == nil {
				files[i].Size = fi.Size()
			}
		}
	}
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
		results, err := e.Execute(ctx, config, initialFiles, 0, onProgress, nil)
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
