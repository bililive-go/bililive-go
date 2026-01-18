package stages

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/bililive-go/bililive-go/src/pipeline"
	"github.com/bililive-go/bililive-go/src/tools"
)

// ExtractCoverStage 封面提取阶段
type ExtractCoverStage struct {
	config   pipeline.StageConfig
	commands []string
	logs     string
}

// NewExtractCoverStage 创建封面提取阶段工厂
func NewExtractCoverStage(config pipeline.StageConfig) (pipeline.Stage, error) {
	return &ExtractCoverStage{
		config: config,
	}, nil
}

func (s *ExtractCoverStage) Name() string {
	return pipeline.StageNameExtractCover
}

func (s *ExtractCoverStage) Execute(ctx *pipeline.PipelineContext, input []pipeline.FileInfo) ([]pipeline.FileInfo, error) {
	if len(input) == 0 {
		s.logs = "没有输入文件"
		return input, nil
	}

	var output []pipeline.FileInfo

	// 先添加所有输入文件到输出
	output = append(output, input...)

	// 对每个视频文件提取封面
	for _, file := range input {
		// 只处理视频文件
		if file.Type != pipeline.FileTypeVideo {
			continue
		}

		// 检查文件是否存在
		if _, err := os.Stat(file.Path); os.IsNotExist(err) {
			s.logs += fmt.Sprintf("文件不存在: %s\n", file.Path)
			continue
		}

		ctx.Logger.Infof("提取封面: %s", file.Path)

		// 提取封面
		coverPath, err := tools.ExtractCover(ctx.Ctx, file.Path)
		if err != nil {
			s.logs += fmt.Sprintf("提取封面失败: %s - %s\n", filepath.Base(file.Path), err.Error())
			ctx.Logger.Warnf("提取封面失败: %s - %s", file.Path, err)
			continue
		}

		// 添加封面文件到输出
		output = append(output, pipeline.FileInfo{
			Path:       coverPath,
			Type:       pipeline.FileTypeCover,
			SourcePath: file.Path,
		})

		s.logs += fmt.Sprintf("封面已保存: %s\n", filepath.Base(coverPath))
		ctx.Logger.Infof("封面已保存: %s", coverPath)
	}

	return output, nil
}

func (s *ExtractCoverStage) GetCommands() []string {
	return s.commands
}

func (s *ExtractCoverStage) GetLogs() string {
	return s.logs
}

// CloudUploadStage 云上传阶段
type CloudUploadStage struct {
	config       pipeline.StageConfig
	storageName  string
	pathTemplate string
	deleteAfter  bool
	fileTypes    []string // 过滤的文件类型，空表示所有
	commands     []string
	logs         string
}

// NewCloudUploadStage 创建云上传阶段工厂
func NewCloudUploadStage(config pipeline.StageConfig) (pipeline.Stage, error) {
	return &CloudUploadStage{
		config:       config,
		storageName:  config.GetStringOption(pipeline.OptionStorage, ""),
		pathTemplate: config.GetStringOption(pipeline.OptionPathTemplate, ""),
		deleteAfter:  config.GetBoolOption(pipeline.OptionDeleteAfter, false),
		fileTypes:    config.GetStringSliceOption(pipeline.OptionFileTypes),
	}, nil
}

func (s *CloudUploadStage) Name() string {
	return pipeline.StageNameCloudUpload
}

func (s *CloudUploadStage) Execute(ctx *pipeline.PipelineContext, input []pipeline.FileInfo) ([]pipeline.FileInfo, error) {
	if len(input) == 0 {
		s.logs = "没有输入文件"
		return input, nil
	}

	if s.storageName == "" {
		s.logs = "未配置存储名称，跳过上传"
		return input, nil
	}

	var output []pipeline.FileInfo

	for _, file := range input {
		// 文件类型过滤
		if len(s.fileTypes) > 0 && !s.matchFileType(file.Type) {
			output = append(output, file)
			continue
		}

		// 检查文件是否存在
		if _, err := os.Stat(file.Path); os.IsNotExist(err) {
			s.logs += fmt.Sprintf("文件不存在: %s\n", file.Path)
			continue
		}

		// 渲染目标路径
		targetPath := s.renderTargetPath(ctx, file)
		if targetPath == "" {
			s.logs += fmt.Sprintf("无法生成目标路径: %s\n", file.Path)
			output = append(output, file)
			continue
		}

		ctx.Logger.Infof("上传文件: %s -> %s", file.Path, targetPath)

		// TODO: 调用 OpenList API 进行上传
		// 当前先记录命令，实际上传逻辑需要集成 OpenList
		s.commands = append(s.commands, fmt.Sprintf("upload %s to %s/%s", file.Path, s.storageName, targetPath))
		s.logs += fmt.Sprintf("上传任务已创建: %s -> %s/%s\n", filepath.Base(file.Path), s.storageName, targetPath)

		// 如果不删除，保留文件在输出中
		if !s.deleteAfter {
			output = append(output, file)
		} else {
			s.logs += fmt.Sprintf("上传后删除: %s\n", filepath.Base(file.Path))
		}
	}

	return output, nil
}

// matchFileType 检查文件类型是否匹配
func (s *CloudUploadStage) matchFileType(fileType pipeline.FileType) bool {
	for _, ft := range s.fileTypes {
		if strings.EqualFold(ft, string(fileType)) {
			return true
		}
	}
	return false
}

// renderTargetPath 渲染目标路径
func (s *CloudUploadStage) renderTargetPath(ctx *pipeline.PipelineContext, file pipeline.FileInfo) string {
	if s.pathTemplate == "" {
		// 默认路径：/录播归档/{平台}/{主播名}/{文件名}
		return fmt.Sprintf("/录播归档/%s/%s/%s",
			ctx.RecordInfo.Platform,
			ctx.RecordInfo.HostName,
			filepath.Base(file.Path),
		)
	}

	// 简单的模板替换
	path := s.pathTemplate
	path = strings.ReplaceAll(path, "{{ .Platform }}", ctx.RecordInfo.Platform)
	path = strings.ReplaceAll(path, "{{.Platform}}", ctx.RecordInfo.Platform)
	path = strings.ReplaceAll(path, "{{ .HostName }}", ctx.RecordInfo.HostName)
	path = strings.ReplaceAll(path, "{{.HostName}}", ctx.RecordInfo.HostName)
	path = strings.ReplaceAll(path, "{{ .RoomName }}", ctx.RecordInfo.RoomName)
	path = strings.ReplaceAll(path, "{{.RoomName}}", ctx.RecordInfo.RoomName)
	path = strings.ReplaceAll(path, "{{ .FileName }}", filepath.Base(file.Path))
	path = strings.ReplaceAll(path, "{{.FileName}}", filepath.Base(file.Path))

	// 获取扩展名
	ext := filepath.Ext(file.Path)
	if len(ext) > 0 && ext[0] == '.' {
		ext = ext[1:]
	}
	path = strings.ReplaceAll(path, "{{ .Ext }}", ext)
	path = strings.ReplaceAll(path, "{{.Ext}}", ext)

	return path
}

func (s *CloudUploadStage) GetCommands() []string {
	return s.commands
}

func (s *CloudUploadStage) GetLogs() string {
	return s.logs
}
