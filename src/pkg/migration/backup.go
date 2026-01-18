package migration

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const (
	// BackupSuffix 备份文件后缀格式
	BackupSuffix = ".backup_%s"
	// MaxBackupCount 最大保留备份数量
	MaxBackupCount = 5
)

// BackupManager 备份管理器
type BackupManager struct {
	dbPath string
}

// NewBackupManager 创建备份管理器
func NewBackupManager(dbPath string) *BackupManager {
	return &BackupManager{
		dbPath: dbPath,
	}
}

// CreateBackup 创建数据库备份
func (m *BackupManager) CreateBackup() (string, error) {
	// 检查源文件是否存在
	if _, err := os.Stat(m.dbPath); os.IsNotExist(err) {
		return "", nil // 新数据库不需要备份
	}

	timestamp := time.Now().Format("20060102_150405")
	backupPath := m.dbPath + fmt.Sprintf(BackupSuffix, timestamp)

	// 确保备份目录存在
	if err := os.MkdirAll(filepath.Dir(backupPath), 0755); err != nil {
		return "", fmt.Errorf("failed to create backup directory: %w", err)
	}

	// 复制文件
	if err := copyFile(m.dbPath, backupPath); err != nil {
		return "", fmt.Errorf("failed to create backup: %w", err)
	}

	// 清理旧备份（清理失败不影响主流程）
	_ = m.CleanupOldBackups()

	return backupPath, nil
}

// RestoreBackup 从备份恢复数据库
func (m *BackupManager) RestoreBackup(backupPath string) error {
	if backupPath == "" {
		return fmt.Errorf("backup path is empty")
	}

	// 检查备份文件是否存在
	if _, err := os.Stat(backupPath); os.IsNotExist(err) {
		return fmt.Errorf("backup file not found: %s", backupPath)
	}

	// 删除当前数据库文件（如果存在）
	if _, err := os.Stat(m.dbPath); err == nil {
		if err := os.Remove(m.dbPath); err != nil {
			return fmt.Errorf("failed to remove current database: %w", err)
		}
	}

	// 从备份恢复
	if err := copyFile(backupPath, m.dbPath); err != nil {
		return fmt.Errorf("failed to restore from backup: %w", err)
	}

	return nil
}

// RemoveBackup 删除备份文件
func (m *BackupManager) RemoveBackup(backupPath string) error {
	if backupPath == "" {
		return nil
	}
	if err := os.Remove(backupPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to remove backup: %w", err)
	}
	return nil
}

// ListBackups 列出所有备份文件
func (m *BackupManager) ListBackups() ([]string, error) {
	dir := filepath.Dir(m.dbPath)
	base := filepath.Base(m.dbPath)
	pattern := base + ".backup_"

	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to read directory: %w", err)
	}

	var backups []string
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasPrefix(entry.Name(), pattern) {
			backups = append(backups, filepath.Join(dir, entry.Name()))
		}
	}

	// 按时间排序（最新的在前）
	sort.Slice(backups, func(i, j int) bool {
		return backups[i] > backups[j]
	})

	return backups, nil
}

// CleanupOldBackups 清理旧备份，保留最近的MaxBackupCount个
func (m *BackupManager) CleanupOldBackups() error {
	backups, err := m.ListBackups()
	if err != nil {
		return err
	}

	if len(backups) <= MaxBackupCount {
		return nil
	}

	// 删除超出数量的旧备份
	for _, backup := range backups[MaxBackupCount:] {
		if err := os.Remove(backup); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("failed to remove old backup %s: %w", backup, err)
		}
	}

	return nil
}

// GetLatestBackup 获取最新的备份文件
func (m *BackupManager) GetLatestBackup() (string, error) {
	backups, err := m.ListBackups()
	if err != nil {
		return "", err
	}
	if len(backups) == 0 {
		return "", nil
	}
	return backups[0], nil
}

// copyFile 复制文件
func copyFile(src, dst string) error {
	srcFile, err := os.Open(src)
	if err != nil {
		return err
	}
	defer srcFile.Close()

	dstFile, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer dstFile.Close()

	if _, err := io.Copy(dstFile, srcFile); err != nil {
		os.Remove(dst)
		return err
	}

	return dstFile.Sync()
}
