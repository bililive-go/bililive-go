//go:build dev

package iostats

import (
	"io/fs"
	"os"
	"path/filepath"
	"runtime"

	"github.com/bililive-go/bililive-go/src/pkg/migration"
)

type ioStatsMigrationSource struct{}

// GetFS 返回迁移文件目录的文件系统（dev 模式使用实际文件）
func (s *ioStatsMigrationSource) GetFS() (fs.FS, error) {
	_, currentFile, _, _ := runtime.Caller(0)
	migrationsDir := filepath.Join(filepath.Dir(currentFile), "migrations")
	return os.DirFS(migrationsDir), nil
}

// GetSubDir 返回迁移文件在 FS 中的子目录
func (s *ioStatsMigrationSource) GetSubDir() string {
	return "."
}

// IsEmbedded 返回迁移文件是否嵌入
func (s *ioStatsMigrationSource) IsEmbedded() bool {
	return false
}

// GetMigrationSource 获取 IO 统计数据库迁移源
func GetMigrationSource() migration.MigrationSource {
	return &ioStatsMigrationSource{}
}
