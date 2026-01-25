// Package stages 提供内置的 Pipeline 阶段实现
package stages

import "github.com/bililive-go/bililive-go/src/pipeline"

// RegisterBuiltinStages 注册所有内置阶段到执行器
func RegisterBuiltinStages(executor *pipeline.Executor) {
	// FLV 修复
	executor.RegisterStage(pipeline.StageNameFixFlv, NewFixFlvStage)

	// MP4 转换
	executor.RegisterStage(pipeline.StageNameConvertMp4, NewConvertMp4Stage)

	// 封面提取
	executor.RegisterStage(pipeline.StageNameExtractCover, NewExtractCoverStage)

	// 云上传
	executor.RegisterStage(pipeline.StageNameCloudUpload, NewCloudUploadStage)

	// 自定义命令
	executor.RegisterStage(pipeline.StageNameCustomCmd, NewCustomCommandStage)

	// 直通（用于测试）
	executor.RegisterStage("passthrough", NewPassthroughStage)

	// 删除源文件
	executor.RegisterStage("delete_source", NewDeleteSourceStage)
}

// RegisterBuiltinStagesToManager 注册所有内置阶段到管理器
func RegisterBuiltinStagesToManager(manager *pipeline.Manager) {
	// FLV 修复
	manager.RegisterStage(pipeline.StageNameFixFlv, NewFixFlvStage)

	// MP4 转换
	manager.RegisterStage(pipeline.StageNameConvertMp4, NewConvertMp4Stage)

	// 封面提取
	manager.RegisterStage(pipeline.StageNameExtractCover, NewExtractCoverStage)

	// 云上传
	manager.RegisterStage(pipeline.StageNameCloudUpload, NewCloudUploadStage)

	// 自定义命令
	manager.RegisterStage(pipeline.StageNameCustomCmd, NewCustomCommandStage)

	// 直通（用于测试）
	manager.RegisterStage("passthrough", NewPassthroughStage)

	// 删除源文件
	manager.RegisterStage("delete_source", NewDeleteSourceStage)
}
