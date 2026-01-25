package migration

import (
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBackupManager_CreateBackup(t *testing.T) {
	// 创建临时目录
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	// 创建测试数据库文件
	err := os.WriteFile(dbPath, []byte("test database content"), 0644)
	require.NoError(t, err)

	// 创建备份管理器
	bm := NewBackupManager(dbPath)

	// 创建备份
	backupPath, err := bm.CreateBackup()
	require.NoError(t, err)
	assert.NotEmpty(t, backupPath)
	assert.FileExists(t, backupPath)

	// 验证备份内容
	content, err := os.ReadFile(backupPath)
	require.NoError(t, err)
	assert.Equal(t, "test database content", string(content))
}

func TestBackupManager_CreateBackup_NoFile(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "nonexistent.db")

	bm := NewBackupManager(dbPath)

	// 不存在的文件不需要备份
	backupPath, err := bm.CreateBackup()
	require.NoError(t, err)
	assert.Empty(t, backupPath)
}

func TestBackupManager_RestoreBackup(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")
	backupPath := filepath.Join(tmpDir, "test.db.backup_20260101_000000")

	// 创建备份文件
	err := os.WriteFile(backupPath, []byte("backup content"), 0644)
	require.NoError(t, err)

	// 创建当前数据库文件
	err = os.WriteFile(dbPath, []byte("current content"), 0644)
	require.NoError(t, err)

	// 恢复备份
	bm := NewBackupManager(dbPath)
	err = bm.RestoreBackup(backupPath)
	require.NoError(t, err)

	// 验证恢复的内容
	content, err := os.ReadFile(dbPath)
	require.NoError(t, err)
	assert.Equal(t, "backup content", string(content))
}

func TestBackupManager_ListBackups(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	// 创建多个备份文件
	backups := []string{
		dbPath + ".backup_20260101_000001",
		dbPath + ".backup_20260101_000002",
		dbPath + ".backup_20260101_000003",
	}
	for _, backup := range backups {
		err := os.WriteFile(backup, []byte("backup"), 0644)
		require.NoError(t, err)
	}

	bm := NewBackupManager(dbPath)
	list, err := bm.ListBackups()
	require.NoError(t, err)
	assert.Len(t, list, 3)

	// 验证排序（最新的在前）
	assert.Equal(t, backups[2], list[0])
	assert.Equal(t, backups[1], list[1])
	assert.Equal(t, backups[0], list[2])
}

func TestBackupManager_CleanupOldBackups(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	// 创建超过MaxBackupCount的备份文件
	for i := 0; i < MaxBackupCount+3; i++ {
		backup := dbPath + ".backup_2026010100000" + string(rune('0'+i))
		err := os.WriteFile(backup, []byte("backup"), 0644)
		require.NoError(t, err)
	}

	bm := NewBackupManager(dbPath)

	// 验证有超过MaxBackupCount个备份
	list, err := bm.ListBackups()
	require.NoError(t, err)
	assert.Greater(t, len(list), MaxBackupCount)

	// 清理
	err = bm.CleanupOldBackups()
	require.NoError(t, err)

	// 验证只剩MaxBackupCount个备份
	list, err = bm.ListBackups()
	require.NoError(t, err)
	assert.Equal(t, MaxBackupCount, len(list))
}

func TestLockManager(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	lm := NewLockManager(dbPath)

	// 初始状态不锁定
	assert.False(t, lm.IsLocked())

	// 获取锁
	lockInfo := CreateLockInfo(dbPath, dbPath+".backup", 1, DatabaseTypeMetadata)
	err := lm.Acquire(lockInfo)
	require.NoError(t, err)

	// 验证已锁定
	assert.True(t, lm.IsLocked())

	// 读取锁信息
	info, err := lm.GetLockInfo()
	require.NoError(t, err)
	assert.Equal(t, dbPath, info.DBPath)
	assert.Equal(t, uint(1), info.FromVersion)

	// 尝试再次获取锁应失败
	err = lm.Acquire(lockInfo)
	assert.Error(t, err)

	// 释放锁
	err = lm.Release()
	require.NoError(t, err)

	// 验证已解锁
	assert.False(t, lm.IsLocked())
}

func TestSchemaRegistry(t *testing.T) {
	// 创建新的注册表（不使用全局的）
	registry := &SchemaRegistry{
		schemas: make(map[DatabaseType]*DatabaseSchema),
	}

	// 创建测试迁移源
	source := &testMigrationSource{}

	// 注册模式
	schema := &DatabaseSchema{
		Type:            "test_db",
		Category:        CategoryNormal,
		MigrationSource: source,
		Description:     "测试数据库",
	}

	err := registry.Register(schema)
	require.NoError(t, err)

	// 获取模式
	got, err := registry.Get("test_db")
	require.NoError(t, err)
	assert.Equal(t, schema, got)

	// 重复注册应失败
	err = registry.Register(schema)
	assert.Error(t, err)

	// 获取不存在的模式应失败
	_, err = registry.Get("nonexistent")
	assert.Error(t, err)

	// 列出所有模式
	types := registry.List()
	assert.Contains(t, types, DatabaseType("test_db"))
}

// testMigrationSource 测试用迁移源
type testMigrationSource struct{}

func (s *testMigrationSource) GetFS() (fs.FS, error) {
	return os.DirFS("."), nil
}

func (s *testMigrationSource) GetSubDir() string {
	return "."
}

func (s *testMigrationSource) IsEmbedded() bool {
	return false
}
