//go:build windows

package configs

// PermissionDiagnostics 包含权限诊断信息
// 在 Windows 上，我们提供一个简化实现
type PermissionDiagnostics struct {
	Suggestions []string
}

// DiagnoseFilePermission 诊断文件权限问题
// Windows 上不执行详细的 Unix 权限检查
func DiagnoseFilePermission(filePath string) *PermissionDiagnostics {
	return &PermissionDiagnostics{}
}

// FormatError 格式化权限诊断为用户友好的错误信息
func (d *PermissionDiagnostics) FormatError() string {
	return ""
}
