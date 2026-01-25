// Package danmaku 弹幕数据库示例
// 本文件展示如何为弹幕类数据库配置迁移（作为多数据库类型的示例）
//
// 弹幕数据库特点：
// - 每个视频文件可能对应一个同名的弹幕数据库文件
// - 属于日志类数据，不需要强制备份
// - 迁移失败时可以选择重建而非回滚
package danmaku

/*
使用示例：

1. 定义弹幕数据库迁移源（类似于 task 包的做法）

package danmaku

import (
	"embed"
	"io/fs"
	"github.com/bililive-go/bililive-go/src/pkg/migration"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

type danmakuMigrationSource struct{}

func (s *danmakuMigrationSource) GetFS() (fs.FS, error) {
	return fs.Sub(migrationsFS, "migrations")
}

func (s *danmakuMigrationSource) GetSubDir() string {
	return "."
}

func (s *danmakuMigrationSource) IsEmbedded() bool {
	return true
}

// DanmakuDatabaseSchema 弹幕数据库模式定义
var DanmakuDatabaseSchema = &migration.DatabaseSchema{
	Type:            migration.DatabaseTypeDanmaku,
	Category:        migration.CategoryDisposable, // 可丢弃数据，迁移失败可重建
	MigrationSource: &danmakuMigrationSource{},
	Description:     "弹幕数据库，存储视频弹幕数据",
}

func init() {
	// 注册弹幕数据库模式
	migration.MustRegisterSchema(DanmakuDatabaseSchema)
}

2. 迁移单个弹幕数据库：

dbPath := "/path/to/video.danmaku.db"
result, err := migration.MigrateDatabaseByType(dbPath, migration.DatabaseTypeDanmaku)

3. 批量迁移多个弹幕数据库（例如在启动时扫描所有弹幕文件）：

func MigrateAllDanmakuDatabases(outputDir string) error {
	// 查找所有弹幕数据库文件
	pattern := filepath.Join(outputDir, "*.danmaku.db")
	matches, err := filepath.Glob(pattern)
	if err != nil {
		return err
	}

	// 创建批量迁移器
	batcher := migration.NewBatchMigrator()
	schema, _ := migration.GetSchema(migration.DatabaseTypeDanmaku)

	for _, dbPath := range matches {
		batcher.Add(&migration.MigrationConfig{
			DBPath: dbPath,
			Schema: schema,
		})
	}

	// 并行执行迁移
	result := batcher.Run(true)
	if !result.Success {
		for _, err := range result.Errors {
			logrus.WithError(err).Warn("danmaku database migration error")
		}
	}

	return nil
}

4. 针对特定视频创建/迁移弹幕数据库：

func GetDanmakuDB(videoPath string) (*sql.DB, error) {
	// 弹幕数据库路径与视频文件同名，扩展名为 .danmaku.db
	dbPath := strings.TrimSuffix(videoPath, filepath.Ext(videoPath)) + ".danmaku.db"

	// 迁移数据库
	schema, _ := migration.GetSchema(migration.DatabaseTypeDanmaku)
	config := &migration.MigrationConfig{
		DBPath:      dbPath,
		Schema:      schema,
		ForceBackup: boolPtr(false), // 显式禁用备份
	}

	if _, err := migration.MigrateDatabase(config); err != nil {
		return nil, err
	}

	// 打开数据库
	return sql.Open("sqlite", dbPath)
}
*/
