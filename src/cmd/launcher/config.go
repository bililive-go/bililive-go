package main

import (
	"encoding/json"
	"os"
)

// LauncherConfig 启动器配置
type LauncherConfig struct {
	// 当前版本号
	CurrentVersion string `json:"current_version"`
	// 当前主程序路径
	CurrentBinaryPath string `json:"current_binary_path"`
	// 备份版本号
	BackupVersion string `json:"backup_version,omitempty"`
	// 备份主程序路径
	BackupBinaryPath string `json:"backup_binary_path,omitempty"`
	// 应用数据目录路径（用于备份数据库）
	AppDataPath string `json:"app_data_path,omitempty"`
	// 备份数据库目录路径
	BackupDbPath string `json:"backup_db_path,omitempty"`
	// 上次更新时间（Unix 时间戳）
	LastUpdateTime int64 `json:"last_update_time,omitempty"`
	// 启动超时时间（秒），主程序需在此时间内报告启动成功
	StartupTimeout int `json:"startup_timeout"`
	// 最大重试次数
	MaxRetries int `json:"max_retries"`
	// 是否自动检查更新
	AutoCheckUpdate bool `json:"auto_check_update"`
	// 更新检查间隔（小时）
	UpdateCheckInterval int `json:"update_check_interval_hours"`
	// 版本检测 API URL（留空使用默认值 https://bililive-go.com/api/versions）
	// 可设置为本地 HTTP 服务器地址用于测试自动升级逻辑
	VersionAPIURL string `json:"version_api_url,omitempty"`
}

// DefaultConfig 返回默认配置
func DefaultConfig() *LauncherConfig {
	return &LauncherConfig{
		StartupTimeout:      60,
		MaxRetries:          3,
		AutoCheckUpdate:     false,
		UpdateCheckInterval: 24,
	}
}

// LoadConfig 从文件加载配置
func LoadConfig(path string) (*LauncherConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	config := DefaultConfig()
	if err := json.Unmarshal(data, config); err != nil {
		return nil, err
	}

	return config, nil
}

// Save 保存配置到文件
func (c *LauncherConfig) Save(path string) error {
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0644)
}
