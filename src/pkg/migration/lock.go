package migration

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

const (
	// LockFileExtension 锁文件扩展名
	LockFileExtension = ".migration.lock"
)

// LockManager 锁管理器
type LockManager struct {
	dbPath   string
	lockPath string
}

// NewLockManager 创建锁管理器
func NewLockManager(dbPath string) *LockManager {
	return &LockManager{
		dbPath:   dbPath,
		lockPath: dbPath + LockFileExtension,
	}
}

// GetLockPath 获取锁文件路径
func (m *LockManager) GetLockPath() string {
	return m.lockPath
}

// Acquire 获取锁
func (m *LockManager) Acquire(info *LockInfo) error {
	// 检查是否存在锁文件
	if m.IsLocked() {
		existingInfo, err := m.GetLockInfo()
		if err != nil {
			return fmt.Errorf("lock file exists but cannot be read: %w", err)
		}
		return fmt.Errorf("database is locked by migration started at %s (PID: %d)",
			existingInfo.StartTime, existingInfo.PID)
	}

	// 确保目录存在
	if err := os.MkdirAll(filepath.Dir(m.lockPath), 0755); err != nil {
		return fmt.Errorf("failed to create lock file directory: %w", err)
	}

	// 写入锁文件
	data, err := json.MarshalIndent(info, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal lock info: %w", err)
	}

	if err := os.WriteFile(m.lockPath, data, 0644); err != nil {
		return fmt.Errorf("failed to write lock file: %w", err)
	}

	return nil
}

// Release 释放锁
func (m *LockManager) Release() error {
	if !m.IsLocked() {
		return nil
	}
	if err := os.Remove(m.lockPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to remove lock file: %w", err)
	}
	return nil
}

// IsLocked 检查是否被锁定
func (m *LockManager) IsLocked() bool {
	_, err := os.Stat(m.lockPath)
	return err == nil
}

// GetLockInfo 获取锁信息
func (m *LockManager) GetLockInfo() (*LockInfo, error) {
	data, err := os.ReadFile(m.lockPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read lock file: %w", err)
	}

	var info LockInfo
	if err := json.Unmarshal(data, &info); err != nil {
		return nil, fmt.Errorf("failed to unmarshal lock info: %w", err)
	}

	return &info, nil
}

// CreateLockInfo 创建锁信息
func CreateLockInfo(dbPath, backupPath string, fromVersion uint, schemaType DatabaseType) *LockInfo {
	return &LockInfo{
		DBPath:        dbPath,
		BackupPath:    backupPath,
		StartTime:     time.Now().Format(time.RFC3339),
		FromVersion:   fromVersion,
		TargetVersion: 0, // 将在迁移时更新
		PID:           os.Getpid(),
		SchemaType:    schemaType,
	}
}
