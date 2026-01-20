package migration

import (
	"fmt"
	"sync"

	bilisentry "github.com/bililive-go/bililive-go/src/pkg/sentry"
	"github.com/sirupsen/logrus"
)

// BatchMigrator 批量迁移器，用于迁移多个数据库文件
type BatchMigrator struct {
	configs []*MigrationConfig
	logger  *logrus.Entry
	mu      sync.Mutex
}

// BatchMigrationResult 批量迁移结果
type BatchMigrationResult struct {
	Results map[string]*MigrationResult
	Success bool
	Errors  []error
}

// NewBatchMigrator 创建批量迁移器
func NewBatchMigrator() *BatchMigrator {
	return &BatchMigrator{
		configs: make([]*MigrationConfig, 0),
		logger:  logrus.WithField("component", "batch_migrator"),
	}
}

// Add 添加迁移配置
func (b *BatchMigrator) Add(config *MigrationConfig) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.configs = append(b.configs, config)
}

// AddMultiple 添加多个迁移配置
func (b *BatchMigrator) AddMultiple(configs []*MigrationConfig) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.configs = append(b.configs, configs...)
}

// Run 执行所有迁移
// parallel 参数指定是否并行执行（对于不同文件的迁移）
func (b *BatchMigrator) Run(parallel bool) *BatchMigrationResult {
	b.mu.Lock()
	defer b.mu.Unlock()

	result := &BatchMigrationResult{
		Results: make(map[string]*MigrationResult),
		Success: true,
	}

	if len(b.configs) == 0 {
		return result
	}

	if parallel {
		return b.runParallel()
	}
	return b.runSequential()
}

// runSequential 顺序执行迁移
func (b *BatchMigrator) runSequential() *BatchMigrationResult {
	result := &BatchMigrationResult{
		Results: make(map[string]*MigrationResult),
		Success: true,
	}

	for _, config := range b.configs {
		migrator, err := NewMigrator(config)
		if err != nil {
			result.Success = false
			result.Errors = append(result.Errors, fmt.Errorf("failed to create migrator for %s: %w", config.DBPath, err))
			continue
		}

		// 先检查是否需要恢复
		recovered, err := migrator.CheckAndRecover()
		if err != nil {
			b.logger.WithError(err).WithField("db_path", config.DBPath).Warn("recovery check failed")
		}
		if recovered {
			b.logger.WithField("db_path", config.DBPath).Info("recovered from incomplete migration")
		}

		// 执行迁移
		migResult, err := migrator.Run()
		result.Results[config.DBPath] = migResult
		if err != nil {
			result.Success = false
			result.Errors = append(result.Errors, fmt.Errorf("migration failed for %s: %w", config.DBPath, err))
		}
	}

	return result
}

// runParallel 并行执行迁移
func (b *BatchMigrator) runParallel() *BatchMigrationResult {
	result := &BatchMigrationResult{
		Results: make(map[string]*MigrationResult),
		Success: true,
	}

	var wg sync.WaitGroup
	var mu sync.Mutex

	for _, config := range b.configs {
		wg.Add(1)
		bilisentry.Go(func() {
			defer wg.Done()

			migrator, err := NewMigrator(config)
			if err != nil {
				mu.Lock()
				result.Success = false
				result.Errors = append(result.Errors, fmt.Errorf("failed to create migrator for %s: %w", config.DBPath, err))
				mu.Unlock()
				return
			}

			// 先检查是否需要恢复
			recovered, err := migrator.CheckAndRecover()
			if err != nil {
				b.logger.WithError(err).WithField("db_path", config.DBPath).Warn("recovery check failed")
			}
			if recovered {
				b.logger.WithField("db_path", config.DBPath).Info("recovered from incomplete migration")
			}

			// 执行迁移
			migResult, err := migrator.Run()
			mu.Lock()
			result.Results[config.DBPath] = migResult
			if err != nil {
				result.Success = false
				result.Errors = append(result.Errors, fmt.Errorf("migration failed for %s: %w", config.DBPath, err))
			}
			mu.Unlock()
		})
	}

	wg.Wait()
	return result
}

// Clear 清空迁移配置
func (b *BatchMigrator) Clear() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.configs = make([]*MigrationConfig, 0)
}

// MigrateDatabase 便捷函数：迁移单个数据库
func MigrateDatabase(config *MigrationConfig) (*MigrationResult, error) {
	migrator, err := NewMigrator(config)
	if err != nil {
		return nil, err
	}

	// 先检查是否需要恢复
	if _, err := migrator.CheckAndRecover(); err != nil {
		logrus.WithError(err).WithField("db_path", config.DBPath).Warn("recovery check failed")
	}

	return migrator.Run()
}

// MigrateDatabaseByType 便捷函数：根据类型迁移数据库
func MigrateDatabaseByType(dbPath string, dbType DatabaseType) (*MigrationResult, error) {
	schema, err := GetSchema(dbType)
	if err != nil {
		return nil, err
	}

	config := &MigrationConfig{
		DBPath: dbPath,
		Schema: schema,
	}

	return MigrateDatabase(config)
}
