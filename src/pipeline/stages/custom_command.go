package stages

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"text/template"

	"github.com/bililive-go/bililive-go/src/pipeline"
	"github.com/bililive-go/bililive-go/src/pkg/utils"
)

// CustomCommandStage 自定义命令阶段
type CustomCommandStage struct {
	config        pipeline.StageConfig
	commandTmpl   string
	outputPattern string // 输出文件模式（用于识别命令产生的新文件）
	commands      []string
	logs          string
}

// NewCustomCommandStage 创建自定义命令阶段工厂
func NewCustomCommandStage(config pipeline.StageConfig) (pipeline.Stage, error) {
	commandTmpl := config.GetStringOption(pipeline.OptionCommand, "")
	if commandTmpl == "" {
		return nil, fmt.Errorf("custom_command stage requires 'command' option")
	}

	return &CustomCommandStage{
		config:        config,
		commandTmpl:   commandTmpl,
		outputPattern: config.GetStringOption("output_pattern", ""),
	}, nil
}

func (s *CustomCommandStage) Name() string {
	return pipeline.StageNameCustomCmd
}

func (s *CustomCommandStage) Execute(ctx *pipeline.PipelineContext, input []pipeline.FileInfo) ([]pipeline.FileInfo, error) {
	if len(input) == 0 {
		s.logs = "没有输入文件"
		return input, nil
	}

	var output []pipeline.FileInfo

	for _, file := range input {
		// 渲染命令模板
		cmdStr, err := s.renderCommand(ctx, file)
		if err != nil {
			s.logs += fmt.Sprintf("渲染命令模板失败: %s\n", err.Error())
			return nil, fmt.Errorf("failed to render command template: %w", err)
		}

		ctx.Logger.Infof("执行自定义命令: %s", cmdStr)
		s.commands = append(s.commands, cmdStr)

		// 执行命令
		err = s.executeCommand(ctx, cmdStr)
		if err != nil {
			s.logs += fmt.Sprintf("命令执行失败: %s\n", err.Error())
			return nil, fmt.Errorf("custom command failed: %w", err)
		}

		s.logs += "命令执行成功\n"

		// 如果没有指定输出模式，保留输入文件
		output = append(output, file)
	}

	return output, nil
}

// renderCommand 渲染命令模板
func (s *CustomCommandStage) renderCommand(ctx *pipeline.PipelineContext, file pipeline.FileInfo) (string, error) {
	// 构建模板数据
	data := struct {
		InputFile string
		Platform  string
		HostName  string
		RoomName  string
		FileName  string
		Dir       string
		Ext       string
		FFmpeg    string
	}{
		InputFile: file.Path,
		Platform:  ctx.RecordInfo.Platform,
		HostName:  ctx.RecordInfo.HostName,
		RoomName:  ctx.RecordInfo.RoomName,
		FileName:  filepath.Base(file.Path),
		Dir:       filepath.Dir(file.Path),
		Ext:       filepath.Ext(file.Path),
		FFmpeg:    ctx.FFmpegPath,
	}

	// 如果 FFmpeg 路径为空，尝试获取
	if data.FFmpeg == "" {
		if ffmpegPath, err := utils.GetFFmpegPath(ctx.Ctx); err == nil {
			data.FFmpeg = ffmpegPath
		}
	}

	// 解析并执行模板
	tmpl, err := template.New("command").Parse(s.commandTmpl)
	if err != nil {
		return "", fmt.Errorf("failed to parse command template: %w", err)
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("failed to execute command template: %w", err)
	}

	return strings.TrimSpace(buf.String()), nil
}

// executeCommand 执行命令
func (s *CustomCommandStage) executeCommand(ctx *pipeline.PipelineContext, cmdStr string) error {
	var shell string
	var args []string

	switch runtime.GOOS {
	case "windows":
		shell = "cmd"
		args = []string{"/C", cmdStr}
	default:
		shell = "sh"
		args = []string{"-c", cmdStr}
	}

	cmd := exec.CommandContext(ctx.Ctx, shell, args...)

	// 捕获输出
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()

	// 记录输出
	if stdout.Len() > 0 {
		s.logs += fmt.Sprintf("stdout:\n%s\n", stdout.String())
	}
	if stderr.Len() > 0 {
		s.logs += fmt.Sprintf("stderr:\n%s\n", stderr.String())
	}

	if err != nil {
		return fmt.Errorf("%w: %s", err, stderr.String())
	}

	return nil
}

func (s *CustomCommandStage) GetCommands() []string {
	return s.commands
}

func (s *CustomCommandStage) GetLogs() string {
	return s.logs
}

// PassthroughStage 直通阶段（用于测试或占位）
type PassthroughStage struct {
	config pipeline.StageConfig
}

// NewPassthroughStage 创建直通阶段工厂
func NewPassthroughStage(config pipeline.StageConfig) (pipeline.Stage, error) {
	return &PassthroughStage{config: config}, nil
}

func (s *PassthroughStage) Name() string {
	return "passthrough"
}

func (s *PassthroughStage) Execute(ctx *pipeline.PipelineContext, input []pipeline.FileInfo) ([]pipeline.FileInfo, error) {
	return input, nil
}

// DeleteSourceStage 删除源文件阶段
type DeleteSourceStage struct {
	config    pipeline.StageConfig
	fileTypes []string
	logs      string
}

// NewDeleteSourceStage 创建删除源文件阶段工厂
func NewDeleteSourceStage(config pipeline.StageConfig) (pipeline.Stage, error) {
	return &DeleteSourceStage{
		config:    config,
		fileTypes: config.GetStringSliceOption(pipeline.OptionFileTypes),
	}, nil
}

func (s *DeleteSourceStage) Name() string {
	return "delete_source"
}

func (s *DeleteSourceStage) Execute(ctx *pipeline.PipelineContext, input []pipeline.FileInfo) ([]pipeline.FileInfo, error) {
	var output []pipeline.FileInfo

	for _, file := range input {
		// 文件类型过滤
		if len(s.fileTypes) > 0 {
			matched := false
			for _, ft := range s.fileTypes {
				if strings.EqualFold(ft, string(file.Type)) {
					matched = true
					break
				}
			}
			if !matched {
				output = append(output, file)
				continue
			}
		}

		// 删除文件
		if err := os.Remove(file.Path); err != nil && !os.IsNotExist(err) {
			s.logs += fmt.Sprintf("删除失败: %s - %s\n", file.Path, err.Error())
			ctx.Logger.Warnf("删除文件失败: %s - %s", file.Path, err)
			output = append(output, file) // 删除失败，保留在输出中
		} else {
			s.logs += fmt.Sprintf("已删除: %s\n", file.Path)
			ctx.Logger.Infof("已删除文件: %s", file.Path)
			// 文件已删除，不添加到输出
		}
	}

	return output, nil
}

func (s *DeleteSourceStage) GetLogs() string {
	return s.logs
}
