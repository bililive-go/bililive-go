//go:build windows

package tools

import (
	blog "github.com/bililive-go/bililive-go/src/log"
)

// logDirectoryPermissionDiagnostics 在 Windows 上输出简化的诊断信息
func logDirectoryPermissionDiagnostics(dirPath string) {
	logger := blog.GetLogger()
	logger.Warnf("无法创建目录: %s", dirPath)
	logger.Warnf("请检查目录权限，确保当前用户对目录有读写权限")
}
