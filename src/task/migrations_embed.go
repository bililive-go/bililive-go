//go:build !dev

package task

import (
	"embed"
	"io/fs"

	"github.com/bililive-go/bililive-go/src/pkg/migration"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// taskMigrationSource 任务数据库迁移源（release模式）
type taskMigrationSource struct{}

// GetFS 返回嵌入的迁移文件系统
func (s *taskMigrationSource) GetFS() (fs.FS, error) {
	return fs.Sub(migrationsFS, "migrations")
}

// GetSubDir 返回迁移文件在FS中的子目录
func (s *taskMigrationSource) GetSubDir() string {
	return "."
}

// IsEmbedded 返回迁移文件是否嵌入
func (s *taskMigrationSource) IsEmbedded() bool {
	return true
}

// GetMigrationSource 获取任务数据库迁移源
func GetMigrationSource() migration.MigrationSource {
	return &taskMigrationSource{}
}

// IsMigrationsEmbedded 返回迁移文件是否嵌入（兼容旧API）
func IsMigrationsEmbedded() bool {
	return true
}

// GetMigrationsFS 返回嵌入的迁移文件系统（兼容旧API）
func GetMigrationsFS() (fs.FS, error) {
	return GetMigrationSource().GetFS()
}
