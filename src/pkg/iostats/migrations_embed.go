//go:build !dev

package iostats

import (
	"embed"
	"io/fs"

	"github.com/bililive-go/bililive-go/src/pkg/migration"
)

//go:embed migrations/*.sql
var embeddedMigrations embed.FS

type ioStatsMigrationSource struct{}

// GetFS 返回迁移文件目录的文件系统（release 模式使用嵌入文件）
func (s *ioStatsMigrationSource) GetFS() (fs.FS, error) {
	return embeddedMigrations, nil
}

// GetSubDir 返回迁移文件在 FS 中的子目录
func (s *ioStatsMigrationSource) GetSubDir() string {
	return "migrations"
}

// IsEmbedded 返回迁移文件是否嵌入
func (s *ioStatsMigrationSource) IsEmbedded() bool {
	return true
}

// GetMigrationSource 获取 IO 统计数据库迁移源
func GetMigrationSource() migration.MigrationSource {
	return &ioStatsMigrationSource{}
}
