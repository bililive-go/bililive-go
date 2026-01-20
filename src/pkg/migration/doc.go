// Package migration 提供通用的数据库迁移框架
//
// 本包支持多种数据库类型和多个数据库文件的迁移，主要特性包括：
//
// 1. 多数据库类型支持：通过 DatabaseType 和 DatabaseSchema 定义不同类型的数据库
// 2. 模式注册：使用 SchemaRegistry 管理所有数据库类型的模式定义
// 3. 备份与回滚：针对关键数据库（CategoryCritical）自动备份，迁移失败时可回滚
// 4. 锁管理：使用 lockfile 防止并发迁移，记录迁移状态
// 5. 批量迁移：支持同时迁移多个数据库文件
//
// 基本使用示例：
//
//	// 1. 定义迁移源
//	type MyMigrationSource struct{}
//	func (s *MyMigrationSource) GetFS() (fs.FS, error) { ... }
//	func (s *MyMigrationSource) GetSubDir() string { return "." }
//	func (s *MyMigrationSource) IsEmbedded() bool { return true }
//
//	// 2. 注册数据库模式
//	migration.RegisterSchema(&migration.DatabaseSchema{
//	    Type:            migration.DatabaseTypeMetadata,
//	    Category:        migration.CategoryCritical,
//	    MigrationSource: &MyMigrationSource{},
//	    Description:     "主数据库",
//	})
//
//	// 3. 执行迁移
//	result, err := migration.MigrateDatabaseByType("/path/to/db.sqlite", migration.DatabaseTypeMetadata)
//
// 批量迁移示例：
//
//	batcher := migration.NewBatchMigrator()
//	batcher.Add(&migration.MigrationConfig{DBPath: "/path/to/db1.sqlite", Schema: schema1})
//	batcher.Add(&migration.MigrationConfig{DBPath: "/path/to/db2.sqlite", Schema: schema2})
//	result := batcher.Run(true) // 并行执行
package migration
