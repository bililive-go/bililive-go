package iostats

import (
	"github.com/bililive-go/bililive-go/src/pkg/migration"
)

// DatabaseTypeIOStats IO 统计数据库类型
const DatabaseTypeIOStats migration.DatabaseType = "iostats"

// IOStatsDatabaseSchema IO 统计数据库模式定义
var IOStatsDatabaseSchema = &migration.DatabaseSchema{
	Type:            DatabaseTypeIOStats,
	Category:        migration.CategoryDisposable,
	MigrationSource: GetMigrationSource(),
	Description:     "IO 统计数据库，存储 IO、请求状态、磁盘 IO 和内存统计数据",
}

func init() {
	migration.MustRegisterSchema(IOStatsDatabaseSchema)
}
