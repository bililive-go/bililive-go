package task

import (
	"context"
	"fmt"
	"os"

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
		return fmt.Errorf("input file is required")
	}

	// 检查输入文件是否存在
	if _, err := os.Stat(task.InputFile); os.IsNotExist(err) {
		return fmt.Errorf("input file does not exist: %s", task.InputFile)
	}

	logrus.WithFields(logrus.Fields{
		"task_id": task.ID,
		"input":   task.InputFile,
	}).Info("starting FLV fix using BililiveRecorder")

	progressCallback(10)

	// 使用 BililiveRecorder 修复 FLV
	outputFiles, err := tools.FixFlvByBililiveRecorder(ctx, task.InputFile)
	if err != nil {
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
		}
	}

	progressCallback(100)

	logrus.WithFields(logrus.Fields{
		"task_id":      task.ID,
		"output_files": outputFiles,
	}).Info("FLV fix completed")

	return nil
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
