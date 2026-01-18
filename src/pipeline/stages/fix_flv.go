package stages

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/bililive-go/bililive-go/src/pipeline"
	"github.com/bililive-go/bililive-go/src/tools"
)

// FixFlvStage FLV 修复阶段
type FixFlvStage struct {
	config   pipeline.StageConfig
	commands []string
	logs     string
}

// NewFixFlvStage 创建 FLV 修复阶段工厂
func NewFixFlvStage(config pipeline.StageConfig) (pipeline.Stage, error) {
	return &FixFlvStage{
		config: config,
	}, nil
}

func (s *FixFlvStage) Name() string {
	return pipeline.StageNameFixFlv
}

func (s *FixFlvStage) Execute(ctx *pipeline.PipelineContext, input []pipeline.FileInfo) ([]pipeline.FileInfo, error) {
	if len(input) == 0 {
		s.logs = "没有输入文件"
		return input, nil
	}

	var output []pipeline.FileInfo

	for _, file := range input {
		// 只处理视频文件
		if file.Type != pipeline.FileTypeVideo {
			output = append(output, file)
			continue
		}

		// 检查文件扩展名，只处理 FLV 文件
		ext := strings.ToLower(filepath.Ext(file.Path))
		if ext != ".flv" {
			s.logs += fmt.Sprintf("文件 %s 不是 FLV 格式，跳过修复。\n", filepath.Base(file.Path))
			output = append(output, file)
			continue
		}

		// 检查文件是否存在
		if _, err := os.Stat(file.Path); os.IsNotExist(err) {
			s.logs += fmt.Sprintf("文件不存在: %s\n", file.Path)
			continue
		}

		ctx.Logger.Infof("使用 BililiveRecorder 修复 FLV 文件: %s", file.Path)

		// 记录命令
		command := s.buildCommand(file.Path)
		if command != "" {
			s.commands = append(s.commands, command)
		}

		// 执行修复
		outputFiles, err := tools.FixFlvByBililiveRecorder(ctx.Ctx, file.Path)
		if err != nil {
			s.logs += fmt.Sprintf("修复失败: %s - %s\n", file.Path, err.Error())
			return nil, fmt.Errorf("fix FLV failed for %s: %w", file.Path, err)
		}

		// 添加输出文件
		for _, outPath := range outputFiles {
			output = append(output, pipeline.FileInfo{
				Path:       outPath,
				Type:       pipeline.FileTypeVideo,
				SourcePath: file.Path,
			})
		}

		if len(outputFiles) > 1 {
			s.logs += fmt.Sprintf("修复完成: %s -> %d 个分段文件\n", filepath.Base(file.Path), len(outputFiles))
		} else if len(outputFiles) == 1 {
			s.logs += fmt.Sprintf("修复完成: %s\n", filepath.Base(file.Path))
		}

		ctx.Logger.Infof("FLV 修复完成: %s -> %d 个文件", file.Path, len(outputFiles))
	}

	return output, nil
}

// buildCommand 构建命令字符串（用于记录）
func (s *FixFlvStage) buildCommand(inputFile string) string {
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

	return fmt.Sprintf("%s %s tool fix \"%s\" \"%s\" --json-indented",
		dotnet.GetToolPath(),
		bililiveRecorder.GetToolPath(),
		inputFile,
		inputFile,
	)
}

func (s *FixFlvStage) GetCommands() []string {
	return s.commands
}

func (s *FixFlvStage) GetLogs() string {
	return s.logs
}

// FixFlvByBililiveRecorder 的包装函数，忽略 PID 回调
func init() {
	// 确保 tools 包中有不带 PID 回调的版本
}
