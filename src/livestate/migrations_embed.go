//go:build !dev

package livestate

import (
	"embed"
	"io/fs"

	"github.com/bililive-go/bililive-go/src/pkg/migration"
)

//go:embed migrations/*.sql
var embeddedMigrations embed.FS

// liveStateMigrationSource 直播间状态数据库迁移源（release模式）
type liveStateMigrationSource struct{}

// GetFS 返回迁移文件目录的文件系统（release模式使用嵌入文件）
func (s *liveStateMigrationSource) GetFS() (fs.FS, error) {
	return embeddedMigrations, nil
}

// GetSubDir 返回迁移文件在FS中的子目录
func (s *liveStateMigrationSource) GetSubDir() string {
	return "migrations"
}

// IsEmbedded 返回迁移文件是否嵌入
func (s *liveStateMigrationSource) IsEmbedded() bool {
	return true
}

// GetMigrationSource 获取直播间状态数据库迁移源
func GetMigrationSource() migration.MigrationSource {
	return &liveStateMigrationSource{}
}
