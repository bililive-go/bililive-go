//go:build dev

package task

import (
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
)

// GetMigrationsFS 返回迁移文件目录的文件系统（dev模式使用实际文件）
func GetMigrationsFS() (fs.FS, error) {
	// 获取当前源文件所在目录
	_, currentFile, _, _ := runtime.Caller(0)
	migrationsDir := filepath.Join(filepath.Dir(currentFile), "migrations")
	return os.DirFS(migrationsDir), nil
}

// IsMigrationsEmbedded 返回迁移文件是否嵌入
func IsMigrationsEmbedded() bool {
	return false
}
