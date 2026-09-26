package pipeline

import (
	"github.com/bililive-go/bililive-go/src/configs"
)

// 内置阶段名称常量
const (
	StageNameFixFlv        = "fix_flv"
	StageNameConvertMp4    = "convert_mp4"
	StageNameExtractCover  = "extract_cover"
	StageNameCloudUpload   = "cloud_upload"
	StageNameCustomCmd     = "custom_command"
	StageNameBurnSubtitles = "burn_subtitles"
)

// 阶段选项键常量
const (
	// OptionDeleteSource 是否删除源文件
	OptionDeleteSource = "delete_source"
	// OptionStorage 云存储名称
	OptionStorage = "storage"
	// OptionPathTemplate 上传路径模板
	OptionPathTemplate = "path_template"
	// OptionDeleteAfter 上传后是否删除已上传的文件
	OptionDeleteAfter = "delete_after"
	// OptionDeleteAllAfter 上传后是否删除全部文件（含中间产物）
	OptionDeleteAllAfter = "delete_all_after"
	// OptionUploadTiming 上传时机（immediate/after_process）
	OptionUploadTiming = "upload_timing"
	// OptionCommand 自定义命令
	OptionCommand = "command"
	// OptionFileTypes 处理的文件类型过滤
	OptionFileTypes = "file_types"
	// OptionUploadSubtitles 是否上传关联的 .ass 弹幕字幕文件
	OptionUploadSubtitles = "upload_subtitles"
	// OptionCodec 视频编码器
	OptionCodec = "codec"
	// OptionCrf CRF 质量值
	OptionCrf = "crf"
	// OptionPreset 编码预设
	OptionPreset = "preset"
	// OptionBurnDeleteAss 烧录后是否删除 ASS 文件
	OptionBurnDeleteAss = "burn_delete_ass"
	// OptionBurnDeleteSource 烧录后是否删除源视频文件
	OptionBurnDeleteSource = "burn_delete_source"
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
	BurnSubtitles         bool                 `yaml:"burn_subtitles,omitempty" json:"burn_subtitles,omitempty"`
	BurnSubtitlesCodec    string               `yaml:"burn_subtitles_codec,omitempty" json:"burn_subtitles_codec,omitempty"`
	BurnSubtitlesCrf      string               `yaml:"burn_subtitles_crf,omitempty" json:"burn_subtitles_crf,omitempty"`
	BurnSubtitlesPreset   string               `yaml:"burn_subtitles_preset,omitempty" json:"burn_subtitles_preset,omitempty"`
	BurnDeleteAss         bool                 `yaml:"burn_delete_ass,omitempty" json:"burn_delete_ass,omitempty"`
	BurnDeleteSource      bool                 `yaml:"burn_delete_source,omitempty" json:"burn_delete_source,omitempty"`

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

	// 构建云上传阶段配置
	cloudUploadStage := StageConfig{
		Name: StageNameCloudUpload,
		Options: map[string]any{
			OptionStorage:         legacy.CloudUpload.StorageName,
			OptionPathTemplate:    legacy.CloudUpload.UploadPathTmpl,
			OptionDeleteAfter:     legacy.CloudUpload.DeleteAfterUpload,
			OptionDeleteAllAfter:  legacy.CloudUpload.DeleteAllAfterUpload,
			OptionUploadTiming:    string(legacy.UploadTiming),
			OptionFileTypes:       []string{string(FileTypeVideo), string(FileTypeCover)},
			OptionUploadSubtitles: legacy.CloudUpload.UploadSubtitles,
		},
	}
	hasCloudUpload := legacy.CloudUpload.Enable && legacy.CloudUpload.StorageName != ""

	// 立即上传模式：上传在所有处理之前
	if hasCloudUpload && legacy.UploadTiming == configs.UploadTimingImmediate {
		stages = append(stages, cloudUploadStage)
	}

	// FLV 修复（在转换之前）
	if legacy.FixFlvAtFirst {
		stages = append(stages, StageConfig{
			Name: StageNameFixFlv,
		})
	}

	// MP4 转换
	if legacy.ConvertToMp4 {
		stages = append(stages, StageConfig{
			Name: StageNameConvertMp4,
			Options: map[string]any{
				OptionDeleteSource: legacy.DeleteFlvAfterConvert,
			},
		})
	}

	// 弹幕字幕烧录（在 MP4 转换之后，封面提取之前）
	if legacy.BurnSubtitles {
		stages = append(stages, StageConfig{
			Name: StageNameBurnSubtitles,
			Options: map[string]any{
				OptionCodec:            legacy.BurnSubtitlesCodec,
				OptionCrf:              legacy.BurnSubtitlesCrf,
				OptionPreset:           legacy.BurnSubtitlesPreset,
				OptionBurnDeleteAss:    legacy.BurnDeleteAss,
				OptionBurnDeleteSource: legacy.BurnDeleteSource,
			},
		})
	}

	// 封面提取
	if legacy.SaveCover {
		stages = append(stages, StageConfig{
			Name: StageNameExtractCover,
		})
	}

	// 后处理完成后上传模式：上传在所有处理之后（默认行为）
	if hasCloudUpload && legacy.UploadTiming != configs.UploadTimingImmediate {
		stages = append(stages, cloudUploadStage)
	}

	// 自定义命令（在最后执行）
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
