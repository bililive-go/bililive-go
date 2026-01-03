//go:build !dev

package task

import (
	"embed"
	"io/fs"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// GetMigrationsFS 返回嵌入的迁移文件系统
func GetMigrationsFS() (fs.FS, error) {
	return fs.Sub(migrationsFS, "migrations")
}

// IsMigrationsEmbedded 返回迁移文件是否嵌入
func IsMigrationsEmbedded() bool {
	return true
}
