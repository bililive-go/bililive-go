//go:build dev

package tools

import (
	"os"
)

func getConfigData() (data []byte, err error) {
	// 允许测试通过环境变量覆盖 remotetools 配置路径
	if override := os.Getenv("REMOTETOOLS_CONFIG"); override != "" {
		return os.ReadFile(override)
	}
	return os.ReadFile("src/tools/remote-tools-config.json")
}
