package livestate

import (
	"github.com/bililive-go/bililive-go/src/pkg/migration"
)

// DatabaseTypeLiveState 直播间状态数据库类型
const DatabaseTypeLiveState migration.DatabaseType = "livestate"

// LiveStateDatabaseSchema 直播间状态数据库模式定义
var LiveStateDatabaseSchema = &migration.DatabaseSchema{
	Type:            DatabaseTypeLiveState,
	Category:        migration.CategoryNormal,
	MigrationSource: GetMigrationSource(),
	Description:     "直播间状态数据库，存储直播间信息、开播/下播历史、名称变更历史",
}

func init() {
	// 注册直播间状态数据库模式
	migration.MustRegisterSchema(LiveStateDatabaseSchema)
}
