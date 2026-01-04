package task

import (
	"github.com/bililive-go/bililive-go/src/pkg/migration"
)

// TaskDatabaseSchema 任务数据库模式定义
var TaskDatabaseSchema = &migration.DatabaseSchema{
	Type:            migration.DatabaseTypeMetadata,
	Category:        migration.CategoryCritical,
	MigrationSource: GetMigrationSource(),
	Description:     "任务队列数据库，存储转码任务、弹幕转换任务等",
}

func init() {
	// 注册任务数据库模式
	migration.MustRegisterSchema(TaskDatabaseSchema)
}
