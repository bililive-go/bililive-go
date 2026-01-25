package pipeline

import (
	"github.com/bililive-go/bililive-go/src/configs"
)

// 内置阶段名称常量
const (
	StageNameFixFlv       = "fix_flv"
	StageNameConvertMp4   = "convert_mp4"
	StageNameExtractCover = "extract_cover"
	StageNameCloudUpload  = "cloud_upload"
	StageNameCustomCmd    = "custom_command"
)

// 阶段选项键常量
const (
	// OptionDeleteSource 是否删除源文件
	OptionDeleteSource = "delete_source"
	// OptionStorage 云存储名称
	OptionStorage = "storage"
	// OptionPathTemplate 上传路径模板
	OptionPathTemplate = "path_template"
	// OptionDeleteAfter 上传后是否删除
	OptionDeleteAfter = "delete_after"
	// OptionCommand 自定义命令
	OptionCommand = "command"
	// OptionFileTypes 处理的文件类型过滤
	OptionFileTypes = "file_types"
)

// OnRecordFinishedPipeline 扩展版的录制完成后配置
// 支持新的 Pipeline 配置格式
type OnRecordFinishedPipeline struct {
	// 旧格式字段（向后兼容）
	ConvertToMp4          bool                 `yaml:"convert_to_mp4,omitempty" json:"convert_to_mp4,omitempty"`
	DeleteFlvAfterConvert bool                 `yaml:"delete_flv_after_convert,omitempty" json:"delete_flv_after_convert,omitempty"`
	CustomCommandline     string               `yaml:"custom_commandline,omitempty" json:"custom_commandline,omitempty"`
	FixFlvAtFirst         bool                 `yaml:"fix_flv_at_first,omitempty" json:"fix_flv_at_first,omitempty"`
	SaveCover             bool                 `yaml:"save_cover,omitempty" json:"save_cover,omitempty"`
	CloudUpload           configs.CloudUpload  `yaml:"cloud_upload,omitempty" json:"cloud_upload,omitempty"`
	UploadTiming          configs.UploadTiming `yaml:"upload_timing,omitempty" json:"upload_timing,omitempty"`

	// 新格式字段
	Pipeline *PipelineConfig `yaml:"pipeline,omitempty" json:"pipeline,omitempty"`
}

// ConvertLegacyConfig 将旧配置格式转换为 Pipeline 配置
// 这是自动迁移的核心逻辑
func ConvertLegacyConfig(legacy *configs.OnRecordFinished) *PipelineConfig {
	if legacy == nil {
		return &PipelineConfig{Stages: []StageConfig{}}
	}

	var stages []StageConfig

	// 1. FLV 修复（在转换之前）
	if legacy.FixFlvAtFirst {
		stages = append(stages, StageConfig{
			Name: StageNameFixFlv,
		})
	}

	// 2. MP4 转换
	if legacy.ConvertToMp4 {
		stages = append(stages, StageConfig{
			Name: StageNameConvertMp4,
			Options: map[string]any{
				OptionDeleteSource: legacy.DeleteFlvAfterConvert,
			},
		})
	}

	// 3. 封面提取
	if legacy.SaveCover {
		stages = append(stages, StageConfig{
			Name: StageNameExtractCover,
		})
	}

	// 4. 云上传
	if legacy.CloudUpload.Enable && legacy.CloudUpload.StorageName != "" {
		stages = append(stages, StageConfig{
			Name: StageNameCloudUpload,
			Options: map[string]any{
				OptionStorage:      legacy.CloudUpload.StorageName,
				OptionPathTemplate: legacy.CloudUpload.UploadPathTmpl,
				OptionDeleteAfter:  legacy.CloudUpload.DeleteAfterUpload,
			},
		})
	}

	// 5. 自定义命令（在最后执行）
	if legacy.CustomCommandline != "" {
		stages = append(stages, StageConfig{
			Name: StageNameCustomCmd,
			Options: map[string]any{
				OptionCommand: legacy.CustomCommandline,
			},
		})
	}

	return &PipelineConfig{Stages: stages}
}

// GetEffectivePipelineConfig 获取有效的 Pipeline 配置
// 如果配置了新格式的 pipeline，使用新格式
// 否则自动转换旧格式
func GetEffectivePipelineConfig(config *configs.OnRecordFinished) *PipelineConfig {
	// 旧格式，需要转换
	return ConvertLegacyConfig(config)
}

// IsLegacyConfig 检查是否为旧配置格式
// 旧格式：任何传统字段被设置
// 新格式：pipeline 字段被设置
func IsLegacyConfig(config *configs.OnRecordFinished) bool {
	// 目前总是返回 true，因为当前版本只有旧格式
	// 后续可以添加 pipeline 字段检测
	return true
}

// BuildDefaultPipelineConfig 构建默认的 Pipeline 配置
// 用于新安装或重置配置
func BuildDefaultPipelineConfig() *PipelineConfig {
	return &PipelineConfig{
		Stages: []StageConfig{
			{Name: StageNameFixFlv},
			{
				Name: StageNameConvertMp4,
				Options: map[string]any{
					OptionDeleteSource: false,
				},
			},
			{Name: StageNameExtractCover},
		},
	}
}

// MergePipelineConfigs 合并两个 Pipeline 配置
// base 是基础配置，override 是覆盖配置
// 返回合并后的配置
func MergePipelineConfigs(base, override *PipelineConfig) *PipelineConfig {
	if override == nil {
		return base
	}
	if base == nil {
		return override
	}

	// 目前简单实现：override 完全替换 base
	// 后续可以实现更智能的合并逻辑
	return override
}

// ClonePipelineConfig 克隆 Pipeline 配置
func ClonePipelineConfig(config *PipelineConfig) *PipelineConfig {
	if config == nil {
		return nil
	}

	cloned := &PipelineConfig{
		Stages: make([]StageConfig, len(config.Stages)),
	}

	for i, stage := range config.Stages {
		cloned.Stages[i] = cloneStageConfig(stage)
	}

	return cloned
}

// cloneStageConfig 克隆阶段配置
func cloneStageConfig(stage StageConfig) StageConfig {
	cloned := StageConfig{
		Name:    stage.Name,
		Enabled: stage.Enabled,
	}

	// 克隆 Options
	if stage.Options != nil {
		cloned.Options = make(map[string]any, len(stage.Options))
		for k, v := range stage.Options {
			cloned.Options[k] = v
		}
	}

	// 克隆 Parallel
	if len(stage.Parallel) > 0 {
		cloned.Parallel = make([]StageConfig, len(stage.Parallel))
		for i, ps := range stage.Parallel {
			cloned.Parallel[i] = cloneStageConfig(ps)
		}
	}

	return cloned
}

// EnabledPtr 返回布尔值的指针（用于配置）
func EnabledPtr(v bool) *bool {
	return &v
}
