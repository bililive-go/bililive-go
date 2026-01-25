// Package metadata 提供程序元数据的持久化存储
// 用于存储设备标识、配置信息、升级状态等关键数据
// 这些数据需要在程序中断后仍能保持完整
package metadata

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	_ "modernc.org/sqlite"
)

var (
	// globalStore 全局元数据存储实例
	globalStore *Store
	// storeMu 保护全局存储实例
	storeMu sync.RWMutex
)

// Store 元数据存储
type Store struct {
	db     *sql.DB
	dbPath string
	mu     sync.RWMutex
}

// Init 初始化全局元数据存储
// dbDir 应该是 AppDataPath/db 目录
func Init(dbDir string) error {
	storeMu.Lock()
	defer storeMu.Unlock()

	if globalStore != nil {
		return nil // 已经初始化
	}

	dbPath := filepath.Join(dbDir, "metadata.db")

	// 确保目录存在
	if err := os.MkdirAll(dbDir, 0755); err != nil {
		return fmt.Errorf("创建目录失败: %w", err)
	}

	// 打开数据库
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return fmt.Errorf("打开数据库失败: %w", err)
	}

	// 设置 SQLite 优化参数
	_, _ = db.Exec("PRAGMA journal_mode=WAL")
	_, _ = db.Exec("PRAGMA synchronous=NORMAL")

	// 创建 key-value 表
	_, err = db.Exec(`
		CREATE TABLE IF NOT EXISTS metadata (
			namespace TEXT NOT NULL,
			key TEXT NOT NULL,
			value TEXT NOT NULL,
			created_at INTEGER DEFAULT (strftime('%s', 'now')),
			updated_at INTEGER DEFAULT (strftime('%s', 'now')),
			PRIMARY KEY (namespace, key)
		)
	`)
	if err != nil {
		db.Close()
		return fmt.Errorf("创建表失败: %w", err)
	}

	globalStore = &Store{
		db:     db,
		dbPath: dbPath,
	}

	return nil
}

// GetStore 获取全局元数据存储实例
// 如果未初始化，返回 nil
func GetStore() *Store {
	storeMu.RLock()
	defer storeMu.RUnlock()
	return globalStore
}

// Close 关闭全局元数据存储
func Close() error {
	storeMu.Lock()
	defer storeMu.Unlock()

	if globalStore == nil {
		return nil
	}

	err := globalStore.db.Close()
	globalStore = nil
	return err
}

// Get 从指定命名空间获取值
func (s *Store) Get(ctx context.Context, namespace, key string) (string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var value string
	err := s.db.QueryRowContext(ctx,
		"SELECT value FROM metadata WHERE namespace = ? AND key = ?",
		namespace, key,
	).Scan(&value)

	if err == sql.ErrNoRows {
		return "", nil // 键不存在返回空字符串
	}
	if err != nil {
		return "", fmt.Errorf("查询失败: %w", err)
	}
	return value, nil
}

// Set 在指定命名空间设置值
func (s *Store) Set(ctx context.Context, namespace, key, value string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	_, err := s.db.ExecContext(ctx,
		`INSERT INTO metadata (namespace, key, value, updated_at) 
		 VALUES (?, ?, ?, strftime('%s', 'now'))
		 ON CONFLICT(namespace, key) DO UPDATE SET 
		 value = excluded.value, 
		 updated_at = strftime('%s', 'now')`,
		namespace, key, value,
	)
	if err != nil {
		return fmt.Errorf("保存失败: %w", err)
	}
	return nil
}

// Delete 从指定命名空间删除键
func (s *Store) Delete(ctx context.Context, namespace, key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	_, err := s.db.ExecContext(ctx,
		"DELETE FROM metadata WHERE namespace = ? AND key = ?",
		namespace, key,
	)
	if err != nil {
		return fmt.Errorf("删除失败: %w", err)
	}
	return nil
}

// GetAll 获取指定命名空间的所有键值对
func (s *Store) GetAll(ctx context.Context, namespace string) (map[string]string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	rows, err := s.db.QueryContext(ctx,
		"SELECT key, value FROM metadata WHERE namespace = ?",
		namespace,
	)
	if err != nil {
		return nil, fmt.Errorf("查询失败: %w", err)
	}
	defer rows.Close()

	result := make(map[string]string)
	for rows.Next() {
		var key, value string
		if err := rows.Scan(&key, &value); err != nil {
			return nil, fmt.Errorf("读取行失败: %w", err)
		}
		result[key] = value
	}

	return result, rows.Err()
}

// DeleteNamespace 删除整个命名空间
func (s *Store) DeleteNamespace(ctx context.Context, namespace string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	_, err := s.db.ExecContext(ctx,
		"DELETE FROM metadata WHERE namespace = ?",
		namespace,
	)
	if err != nil {
		return fmt.Errorf("删除命名空间失败: %w", err)
	}
	return nil
}

// 预定义的命名空间常量
const (
	// NamespaceDevice 设备相关信息（如 Sentry 设备 ID）
	NamespaceDevice = "device"
	// NamespaceConfig 配置信息
	NamespaceConfig = "config"
	// NamespaceUpdate 升级相关状态
	NamespaceUpdate = "update"
	// NamespaceMigration 数据库迁移状态
	NamespaceMigration = "migration"
)

// 预定义的键常量
const (
	// KeyDeviceID Sentry 设备标识
	KeyDeviceID = "sentry_device_id"
)
