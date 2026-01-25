//go:build dev

package livestate

import (
	"io/fs"
	"os"
	"path/filepath"
	"runtime"

	"github.com/bililive-go/bililive-go/src/pkg/migration"
)

// liveStateMigrationSource 直播间状态数据库迁移源（dev模式）
type liveStateMigrationSource struct{}

// GetFS 返回迁移文件目录的文件系统（dev模式使用实际文件）
func (s *liveStateMigrationSource) GetFS() (fs.FS, error) {
	// 获取当前源文件所在目录
	_, currentFile, _, _ := runtime.Caller(0)
	migrationsDir := filepath.Join(filepath.Dir(currentFile), "migrations")
	return os.DirFS(migrationsDir), nil
}

// GetSubDir 返回迁移文件在FS中的子目录
func (s *liveStateMigrationSource) GetSubDir() string {
	return "."
}

// IsEmbedded 返回迁移文件是否嵌入
func (s *liveStateMigrationSource) IsEmbedded() bool {
	return false
}

// GetMigrationSource 获取直播间状态数据库迁移源
func GetMigrationSource() migration.MigrationSource {
	return &liveStateMigrationSource{}
}
