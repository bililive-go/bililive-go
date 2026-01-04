// Package migration 提供通用的数据库迁移框架，支持多种数据库类型和多个数据库文件
package migration

import (
	"database/sql"
	"io/fs"
)

// DatabaseType 数据库类型
type DatabaseType string

const (
	// DatabaseTypeMetadata 元数据类数据库（任务、配置等），需要强制备份
	DatabaseTypeMetadata DatabaseType = "metadata"
	// DatabaseTypeDanmaku 弹幕类数据库（日志类数据），不需要强制备份
	DatabaseTypeDanmaku DatabaseType = "danmaku"
	// DatabaseTypeCustom 自定义类型数据库
	DatabaseTypeCustom DatabaseType = "custom"
)

// DatabaseCategory 数据库分类，决定迁移时的行为
type DatabaseCategory int

const (
	// CategoryCritical 关键数据，迁移时强制备份，失败时回滚
	CategoryCritical DatabaseCategory = iota
	// CategoryNormal 普通数据，迁移时可选备份
	CategoryNormal
	// CategoryDisposable 可丢弃数据（如日志），迁移失败可重建
	CategoryDisposable
)

// MigrationSource 迁移源（SQL文件来源）
type MigrationSource interface {
	// GetFS 返回迁移文件系统
	GetFS() (fs.FS, error)
	// GetSubDir 返回迁移文件在FS中的子目录（如果有）
	GetSubDir() string
	// IsEmbedded 返回迁移文件是否嵌入
	IsEmbedded() bool
}

// DatabaseSchema 数据库模式定义
type DatabaseSchema struct {
	// Type 数据库类型标识
	Type DatabaseType
	// Category 数据库分类，决定迁移行为
	Category DatabaseCategory
	// MigrationSource 迁移SQL文件来源
	MigrationSource MigrationSource
	// Description 数据库描述
	Description string
}

// MigrationConfig 迁移配置
type MigrationConfig struct {
	// DBPath 数据库文件路径
	DBPath string
	// Schema 数据库模式
	Schema *DatabaseSchema
	// ForceBackup 是否强制备份（覆盖Schema的默认行为）
	ForceBackup *bool
	// DB 可选的已打开数据库连接（如果为nil，则自动打开）
	DB *sql.DB
}

// MigrationResult 迁移结果
type MigrationResult struct {
	// Success 是否成功
	Success bool
	// FromVersion 迁移前版本
	FromVersion uint
	// ToVersion 迁移后版本
	ToVersion uint
	// BackupPath 备份文件路径（如果有）
	BackupPath string
	// Error 错误信息
	Error error
	// WasDirty 迁移前是否处于脏状态
	WasDirty bool
}

// LockInfo 锁文件信息
type LockInfo struct {
	// DBPath 正在迁移的数据库路径
	DBPath string `json:"db_path"`
	// BackupPath 备份文件路径
	BackupPath string `json:"backup_path"`
	// StartTime 迁移开始时间
	StartTime string `json:"start_time"`
	// FromVersion 迁移前版本
	FromVersion uint `json:"from_version"`
	// TargetVersion 目标版本
	TargetVersion uint `json:"target_version"`
	// PID 进程ID
	PID int `json:"pid"`
	// SchemaType 数据库类型
	SchemaType DatabaseType `json:"schema_type"`
}
