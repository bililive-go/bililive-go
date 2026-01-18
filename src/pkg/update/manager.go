// Package update 提供 bililive-go 的自动更新功能
package update

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/bililive-go/bililive-go/src/pkg/ipc"
)

// UpdateState 更新状态
type UpdateState string

const (
	UpdateStateIdle        UpdateState = "idle"
	UpdateStateChecking    UpdateState = "checking"
	UpdateStateAvailable   UpdateState = "available"
	UpdateStateDownloading UpdateState = "downloading"
	UpdateStateReady       UpdateState = "ready"
	UpdateStateApplying    UpdateState = "applying"
	UpdateStateFailed      UpdateState = "failed"
)

// Manager 更新管理器，协调版本检查、下载和更新通知
type Manager struct {
	checker     *Checker
	downloader  *Downloader
	notifier    *Notifier
	downloadDir string
	currentVer  string

	mu            sync.RWMutex
	state         UpdateState
	availableInfo *ReleaseInfo
	downloadPath  string
	lastError     string
	progressChan  chan DownloadProgress
}

// ManagerConfig 更新管理器配置
type ManagerConfig struct {
	CurrentVersion string
	DownloadDir    string
	InstanceID     string
}

// NewManager 创建新的更新管理器
func NewManager(config ManagerConfig) *Manager {
	if config.DownloadDir == "" {
		config.DownloadDir = filepath.Join(os.TempDir(), "bililive-go-updates")
	}
	if config.InstanceID == "" {
		config.InstanceID = ipc.GetInstanceID()
	}

	return &Manager{
		checker:     NewChecker(config.CurrentVersion),
		downloader:  NewDownloader(config.DownloadDir),
		notifier:    NewNotifier(config.InstanceID),
		downloadDir: config.DownloadDir,
		currentVer:  config.CurrentVersion,
		state:       UpdateStateIdle,
	}
}

// GetState 获取当前更新状态
func (m *Manager) GetState() UpdateState {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.state
}

// GetAvailableInfo 获取可用更新信息
func (m *Manager) GetAvailableInfo() *ReleaseInfo {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.availableInfo
}

// GetDownloadProgress 获取下载进度
func (m *Manager) GetDownloadProgress() DownloadProgress {
	return m.downloader.GetProgress()
}

// GetLastError 获取最后一次错误
func (m *Manager) GetLastError() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.lastError
}

// SetProgressCallback 设置下载进度回调
func (m *Manager) SetProgressCallback(ch chan DownloadProgress) {
	m.mu.Lock()
	m.progressChan = ch
	m.mu.Unlock()
	m.downloader.SetProgressCallback(ch)
}

// CheckForUpdate 检查是否有新版本
func (m *Manager) CheckForUpdate(ctx context.Context, includePrerelease bool) (*ReleaseInfo, error) {
	m.mu.Lock()
	m.state = UpdateStateChecking
	m.lastError = ""
	m.mu.Unlock()

	info, err := m.checker.CheckForUpdate(includePrerelease)
	if err != nil {
		m.mu.Lock()
		m.state = UpdateStateFailed
		m.lastError = err.Error()
		m.mu.Unlock()
		return nil, err
	}

	m.mu.Lock()
	if info != nil {
		m.state = UpdateStateAvailable
		m.availableInfo = info
	} else {
		m.state = UpdateStateIdle
		m.availableInfo = nil
	}
	m.mu.Unlock()

	return info, nil
}

// DownloadUpdate 下载可用的更新
func (m *Manager) DownloadUpdate(ctx context.Context) error {
	m.mu.RLock()
	info := m.availableInfo
	m.mu.RUnlock()

	if info == nil {
		return fmt.Errorf("没有可用的更新")
	}

	m.mu.Lock()
	m.state = UpdateStateDownloading
	m.lastError = ""
	m.mu.Unlock()

	result, err := m.downloader.Download(ctx, info.DownloadURL, info.SHA256)
	if err != nil {
		m.mu.Lock()
		m.state = UpdateStateFailed
		m.lastError = err.Error()
		m.mu.Unlock()
		return err
	}

	if result.Status == DownloadStatusCancelled {
		m.mu.Lock()
		m.state = UpdateStateAvailable
		m.mu.Unlock()
		return nil
	}

	m.mu.Lock()
	m.state = UpdateStateReady
	m.downloadPath = result.FilePath
	// 如果 ReleaseInfo 没有预设 SHA256，使用下载时计算的
	if m.availableInfo.SHA256 == "" {
		m.availableInfo.SHA256 = result.SHA256
	}
	m.mu.Unlock()

	return nil
}

// CancelDownload 取消下载
func (m *Manager) CancelDownload() {
	m.downloader.Cancel()
}

// ApplyUpdate 应用更新（通知启动器）
func (m *Manager) ApplyUpdate(ctx context.Context) error {
	m.mu.RLock()
	state := m.state
	info := m.availableInfo
	downloadPath := m.downloadPath
	m.mu.RUnlock()

	if state != UpdateStateReady {
		return fmt.Errorf("更新尚未准备好，当前状态: %s", state)
	}

	if info == nil || downloadPath == "" {
		return fmt.Errorf("更新信息不完整")
	}

	m.mu.Lock()
	m.state = UpdateStateApplying
	m.mu.Unlock()

	// 连接到启动器
	if !m.notifier.IsConnected() {
		if err := m.notifier.Connect(ctx); err != nil {
			m.mu.Lock()
			m.state = UpdateStateFailed
			m.lastError = fmt.Sprintf("无法连接到启动器: %v", err)
			m.mu.Unlock()
			return fmt.Errorf("无法连接到启动器: %w", err)
		}
	}

	// 发送更新请求
	if err := m.notifier.RequestUpdate(info.Version, downloadPath, info.SHA256); err != nil {
		m.mu.Lock()
		m.state = UpdateStateFailed
		m.lastError = fmt.Sprintf("发送更新请求失败: %v", err)
		m.mu.Unlock()
		return fmt.Errorf("发送更新请求失败: %w", err)
	}

	// 更新请求已发送，等待启动器处理
	// 主程序应该准备好接收关闭信号
	return nil
}

// ConnectToLauncher 连接到启动器（如果可用）
func (m *Manager) ConnectToLauncher(ctx context.Context) error {
	return m.notifier.Connect(ctx)
}

// NotifyStartup 通知启动器启动状态
func (m *Manager) NotifyStartup(success bool, errMsg string, pid int) error {
	if !m.notifier.IsConnected() {
		return nil // 没有启动器，忽略
	}

	if success {
		return m.notifier.NotifyStartupSuccess(m.currentVer, pid)
	}
	return m.notifier.NotifyStartupFailed(m.currentVer, pid, errMsg)
}

// OnShutdownRequest 设置收到关闭请求时的回调
func (m *Manager) OnShutdownRequest(handler func(gracePeriod int)) {
	m.notifier.OnMessage(func(msg *ipc.Message) {
		if msg.Type == ipc.MsgTypeShutdown {
			var payload ipc.ShutdownPayload
			if err := msg.ParsePayload(&payload); err == nil {
				handler(payload.GracePeriod)
			}
		}
	})
}

// AckShutdown 确认关闭请求
func (m *Manager) AckShutdown() error {
	return m.notifier.SendShutdownAck()
}

// Close 关闭管理器
func (m *Manager) Close() error {
	m.downloader.Cancel()
	return m.notifier.Disconnect()
}

// IsLauncherConnected 检查是否连接到启动器
func (m *Manager) IsLauncherConnected() bool {
	return m.notifier.IsConnected()
}
