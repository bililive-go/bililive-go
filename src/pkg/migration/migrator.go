package migration

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/golang-migrate/migrate/v4"
	"github.com/golang-migrate/migrate/v4/database/sqlite"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	"github.com/sirupsen/logrus"
)

var (
	// ErrMigrationFailed 迁移失败错误
	ErrMigrationFailed = errors.New("migration failed")
	// ErrRollbackFailed 回滚失败错误
	ErrRollbackFailed = errors.New("rollback failed")
	// ErrLocked 数据库被锁定错误
	ErrLocked = errors.New("database is locked by another migration")
	// ErrNoBackup 无备份可回滚错误
	ErrNoBackup = errors.New("no backup available for rollback")
)

// Migrator 数据库迁移器
type Migrator struct {
	config        *MigrationConfig
	lockManager   *LockManager
	backupManager *BackupManager
	logger        *logrus.Entry
}

// NewMigrator 创建迁移器
func NewMigrator(config *MigrationConfig) (*Migrator, error) {
	if config == nil {
		return nil, fmt.Errorf("config cannot be nil")
	}
	if config.DBPath == "" {
		return nil, fmt.Errorf("database path cannot be empty")
	}
	if config.Schema == nil {
		return nil, fmt.Errorf("schema cannot be nil")
	}

	return &Migrator{
		config:        config,
		lockManager:   NewLockManager(config.DBPath),
		backupManager: NewBackupManager(config.DBPath),
		logger: logrus.WithFields(logrus.Fields{
			"db_path":     config.DBPath,
			"schema_type": config.Schema.Type,
		}),
	}, nil
}

// shouldBackup 判断是否需要备份
func (m *Migrator) shouldBackup() bool {
	// 如果显式指定了ForceBackup，使用指定值
	if m.config.ForceBackup != nil {
		return *m.config.ForceBackup
	}
	// 否则根据数据库分类决定
	return m.config.Schema.Category == CategoryCritical
}

// Run 执行迁移
func (m *Migrator) Run() (*MigrationResult, error) {
	result := &MigrationResult{}

	// 检查是否已被锁定
	if m.lockManager.IsLocked() {
		lockInfo, err := m.lockManager.GetLockInfo()
		if err != nil {
			return nil, fmt.Errorf("%w: cannot read lock info: %v", ErrLocked, err)
		}
		return nil, fmt.Errorf("%w: started at %s (PID: %d)",
			ErrLocked, lockInfo.StartTime, lockInfo.PID)
	}

	// 确保数据库目录存在
	if err := os.MkdirAll(filepath.Dir(m.config.DBPath), 0755); err != nil {
		return nil, fmt.Errorf("failed to create database directory: %w", err)
	}

	// 打开数据库连接
	db := m.config.DB
	var ownDB bool
	if db == nil {
		var err error
		db, err = sql.Open("sqlite", m.config.DBPath)
		if err != nil {
			return nil, fmt.Errorf("failed to open database: %w", err)
		}
		ownDB = true
		defer func() {
			if ownDB {
				db.Close()
			}
		}()
	}

	// 获取迁移源
	migrationsFS, err := m.config.Schema.MigrationSource.GetFS()
	if err != nil {
		return nil, fmt.Errorf("failed to get migrations fs: %w", err)
	}

	// 创建 iofs source
	subDir := m.config.Schema.MigrationSource.GetSubDir()
	if subDir == "" {
		subDir = "."
	}
	sourceDriver, err := iofs.New(migrationsFS, subDir)
	if err != nil {
		return nil, fmt.Errorf("failed to create iofs source: %w", err)
	}

	// 创建 sqlite database driver
	dbDriver, err := sqlite.WithInstance(db, &sqlite.Config{})
	if err != nil {
		return nil, fmt.Errorf("failed to create sqlite driver: %w", err)
	}

	// 创建 migrate 实例
	mig, err := migrate.NewWithInstance("iofs", sourceDriver, "sqlite", dbDriver)
	if err != nil {
		return nil, fmt.Errorf("failed to create migrate instance: %w", err)
	}

	// 获取当前版本
	currentVersion, dirty, _ := mig.Version()
	result.FromVersion = currentVersion
	result.WasDirty = dirty

	// 检查是否需要迁移
	// 先尝试获取目标版本（最新版本）
	// migrate库没有直接获取最新版本的方法，我们通过尝试迁移来判断

	// 如果需要备份，先创建备份和锁
	var backupPath string
	if m.shouldBackup() {
		// 创建备份
		backupPath, err = m.backupManager.CreateBackup()
		if err != nil {
			return nil, fmt.Errorf("failed to create backup: %w", err)
		}
		result.BackupPath = backupPath

		if backupPath != "" {
			// 创建锁文件
			lockInfo := CreateLockInfo(m.config.DBPath, backupPath, currentVersion, m.config.Schema.Type)
			if err := m.lockManager.Acquire(lockInfo); err != nil {
				// 删除刚创建的备份
				m.backupManager.RemoveBackup(backupPath)
				return nil, fmt.Errorf("failed to acquire lock: %w", err)
			}
			defer m.lockManager.Release()
		}
	}

	// 执行迁移
	if err := mig.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		result.Error = err

		// 如果有备份，尝试回滚
		if backupPath != "" && m.shouldBackup() {
			m.logger.WithError(err).Error("migration failed, attempting rollback")
			if rollbackErr := m.backupManager.RestoreBackup(backupPath); rollbackErr != nil {
				m.logger.WithError(rollbackErr).Error("rollback failed")
				return result, fmt.Errorf("%w: %v (rollback also failed: %v)",
					ErrMigrationFailed, err, rollbackErr)
			}
			m.logger.Info("rollback completed successfully")
		}

		return result, fmt.Errorf("%w: %v", ErrMigrationFailed, err)
	}

	// 获取迁移后版本
	newVersion, _, _ := mig.Version()
	result.ToVersion = newVersion
	result.Success = true

	// 记录日志
	if currentVersion != newVersion {
		m.logger.WithFields(logrus.Fields{
			"from_version": currentVersion,
			"to_version":   newVersion,
			"was_dirty":    dirty,
			"backup_path":  backupPath,
			"embedded":     m.config.Schema.MigrationSource.IsEmbedded(),
		}).Info("database migration completed")
	} else {
		m.logger.WithFields(logrus.Fields{
			"version":  newVersion,
			"embedded": m.config.Schema.MigrationSource.IsEmbedded(),
		}).Debug("database schema is up to date")
	}

	// 迁移成功后，删除备份（可选，这里保留备份以防万一）
	// 如果不需要保留备份，可以取消下面的注释
	// if backupPath != "" {
	//     m.backupManager.RemoveBackup(backupPath)
	// }

	return result, nil
}

// Rollback 从备份回滚数据库
func (m *Migrator) Rollback() error {
	// 检查是否有锁文件
	if m.lockManager.IsLocked() {
		lockInfo, err := m.lockManager.GetLockInfo()
		if err != nil {
			return fmt.Errorf("failed to read lock info: %w", err)
		}

		// 使用锁文件中记录的备份路径进行回滚
		if lockInfo.BackupPath == "" {
			return ErrNoBackup
		}

		m.logger.WithField("backup_path", lockInfo.BackupPath).Info("rolling back from lock file info")
		if err := m.backupManager.RestoreBackup(lockInfo.BackupPath); err != nil {
			return fmt.Errorf("%w: %v", ErrRollbackFailed, err)
		}

		// 回滚成功，释放锁
		m.lockManager.Release()
		return nil
	}

	// 没有锁文件，尝试使用最新备份
	latestBackup, err := m.backupManager.GetLatestBackup()
	if err != nil {
		return fmt.Errorf("failed to get latest backup: %w", err)
	}
	if latestBackup == "" {
		return ErrNoBackup
	}

	m.logger.WithField("backup_path", latestBackup).Info("rolling back from latest backup")
	if err := m.backupManager.RestoreBackup(latestBackup); err != nil {
		return fmt.Errorf("%w: %v", ErrRollbackFailed, err)
	}

	return nil
}

// CheckAndRecover 检查并恢复未完成的迁移
// 如果发现锁文件存在（表示上次迁移未正常完成），则尝试回滚
func (m *Migrator) CheckAndRecover() (bool, error) {
	if !m.lockManager.IsLocked() {
		return false, nil
	}

	lockInfo, err := m.lockManager.GetLockInfo()
	if err != nil {
		return false, fmt.Errorf("failed to read lock info: %w", err)
	}

	m.logger.WithFields(logrus.Fields{
		"start_time":   lockInfo.StartTime,
		"pid":          lockInfo.PID,
		"from_version": lockInfo.FromVersion,
		"backup_path":  lockInfo.BackupPath,
	}).Warn("detected incomplete migration, attempting recovery")

	// 如果数据库分类是关键数据，进行回滚
	if m.config.Schema.Category == CategoryCritical {
		if lockInfo.BackupPath != "" {
			if err := m.backupManager.RestoreBackup(lockInfo.BackupPath); err != nil {
				return true, fmt.Errorf("recovery failed: %w", err)
			}
			m.logger.Info("database recovered from backup")
		}
	}

	// 释放锁
	m.lockManager.Release()
	return true, nil
}

// GetVersion 获取当前数据库版本
func (m *Migrator) GetVersion() (uint, bool, error) {
	// 打开数据库连接
	db := m.config.DB
	var ownDB bool
	if db == nil {
		var err error
		db, err = sql.Open("sqlite", m.config.DBPath)
		if err != nil {
			return 0, false, fmt.Errorf("failed to open database: %w", err)
		}
		ownDB = true
		defer func() {
			if ownDB {
				db.Close()
			}
		}()
	}

	// 获取迁移源
	migrationsFS, err := m.config.Schema.MigrationSource.GetFS()
	if err != nil {
		return 0, false, fmt.Errorf("failed to get migrations fs: %w", err)
	}

	// 创建 iofs source
	subDir := m.config.Schema.MigrationSource.GetSubDir()
	if subDir == "" {
		subDir = "."
	}
	sourceDriver, err := iofs.New(migrationsFS, subDir)
	if err != nil {
		return 0, false, fmt.Errorf("failed to create iofs source: %w", err)
	}

	// 创建 sqlite database driver
	dbDriver, err := sqlite.WithInstance(db, &sqlite.Config{})
	if err != nil {
		return 0, false, fmt.Errorf("failed to create sqlite driver: %w", err)
	}

	// 创建 migrate 实例
	mig, err := migrate.NewWithInstance("iofs", sourceDriver, "sqlite", dbDriver)
	if err != nil {
		return 0, false, fmt.Errorf("failed to create migrate instance: %w", err)
	}

	version, dirty, err := mig.Version()
	if errors.Is(err, migrate.ErrNilVersion) {
		return 0, false, nil
	}
	return version, dirty, err
}
