//go:build dev
// +build dev

// Package kliveproxy 开发模式初始化
// 在开发模式下，从环境变量加载 klive 工具的本地路径
package kliveproxy

import (
	"github.com/bililive-go/bililive-go/src/log"
	"github.com/kira1928/remotetools/pkg/tools"
)

func init() {
	// 从环境变量加载开发工具覆盖
	// 例如: REMOTETOOLS_DEV_KLIVE=/path/to/klive.exe
	tools.LoadDevToolOverridesFromEnv()

	if path := tools.GetDevToolOverride(ToolName); path != "" {
		log.GetLogger().Infof("开发模式：使用本地 klive 工具: %s", path)
	}
}
