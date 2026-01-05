package task

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/bililive-go/bililive-go/src/tools"
	"github.com/sirupsen/logrus"
)

// FixFLVExecutor FLV修复执行器
type FixFLVExecutor struct {
}

// NewFixFLVExecutor 创建FLV修复执行器
func NewFixFLVExecutor() *FixFLVExecutor {
	return &FixFLVExecutor{}
}

// Execute 执行FLV修复
// 使用 BililiveRecorder 工具修复 FLV 文件
func (e *FixFLVExecutor) Execute(ctx context.Context, task *Task, progressCallback func(int)) error {
	if task.InputFile == "" {
		task.Logs = "输入文件路径为空"
		return fmt.Errorf("input file is required")
	}

	// 检查输入文件是否存在
	if _, err := os.Stat(task.InputFile); os.IsNotExist(err) {
		task.Logs = fmt.Sprintf("输入文件不存在: %s", task.InputFile)
		return fmt.Errorf("input file does not exist: %s", task.InputFile)
	}

	// 检查文件扩展名，只处理 FLV 文件
	ext := strings.ToLower(filepath.Ext(task.InputFile))
	if ext != ".flv" {
		task.Logs = fmt.Sprintf("输入文件不是FLV格式（扩展名: %s），跳过修复任务。", ext)
		task.OutputFile = task.InputFile
		task.Status = TaskStatusSkipped // 设置为跳过状态
		logrus.WithFields(logrus.Fields{
			"task_id": task.ID,
			"input":   task.InputFile,
			"ext":     ext,
		}).Info("skipping FLV fix: not a FLV file")
		progressCallback(100)
		return nil
	}

	logrus.WithFields(logrus.Fields{
		"task_id": task.ID,
		"input":   task.InputFile,
	}).Info("starting FLV fix using BililiveRecorder")

	progressCallback(10)

	// 构建命令信息
	command := e.buildCommand(task.InputFile)
	if command != "" {
		task.Commands = append(task.Commands, command)
	}

	// 使用 BililiveRecorder 修复 FLV
	outputFiles, err := tools.FixFlvByBililiveRecorder(ctx, task.InputFile)
	if err != nil {
		task.Logs = fmt.Sprintf("修复失败: %s", err.Error())
		return fmt.Errorf("fix FLV failed: %w", err)
	}

	progressCallback(90)

	// 设置输出文件（可能是多个分段）
	if len(outputFiles) > 0 {
		task.OutputFile = outputFiles[0]
		// 如果有多个输出文件，保存在 metadata 中
		if len(outputFiles) > 1 {
			if task.Metadata == nil {
				task.Metadata = make(map[string]interface{})
			}
			task.Metadata["output_files"] = outputFiles
			task.Logs = fmt.Sprintf("修复完成，生成 %d 个分段文件。", len(outputFiles))
		} else {
			task.Logs = "修复完成。"
		}
	}

	progressCallback(100)

	logrus.WithFields(logrus.Fields{
		"task_id":      task.ID,
		"output_files": outputFiles,
	}).Info("FLV fix completed")

	return nil
}

// buildCommand 构建命令字符串（用于记录）
func (e *FixFLVExecutor) buildCommand(inputFile string) string {
	api := tools.Get()
	if api == nil {
		return ""
	}

	dotnet, err := api.GetTool("dotnet")
	if err != nil || !dotnet.DoesToolExist() {
		return ""
	}

	bililiveRecorder, err := api.GetTool("bililive-recorder")
	if err != nil || !bililiveRecorder.DoesToolExist() {
		return ""
	}

	// 构建命令字符串
	return fmt.Sprintf("%s %s tool fix \"%s\" \"%s\" --json-indented",
		dotnet.GetToolPath(),
		bililiveRecorder.GetToolPath(),
		inputFile,
		inputFile,
	)
}

// Cleanup 清理临时文件
func (e *FixFLVExecutor) Cleanup(task *Task) {
	for _, tempFile := range task.TempFiles {
		if tempFile != "" {
			if err := os.Remove(tempFile); err != nil && !os.IsNotExist(err) {
				logrus.WithError(err).WithField("file", tempFile).Warn("failed to cleanup temp file")
			}
		}
	}
}
