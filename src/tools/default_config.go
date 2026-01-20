//go:build !dev && !release

package tools

import (
	"os"
)

// getConfigData 从文件系统加载配置数据（默认构建）
func getConfigData() (data []byte, err error) {
	return os.ReadFile("src/tools/remote-tools-config.json")
}
