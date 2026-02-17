// Package update 提供 bililive-go 的自动更新功能
package update

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync"

	rtconfig "github.com/kira1928/remotetools/pkg/config"
	remotetools "github.com/kira1928/remotetools/pkg/tools"

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

// DownloadProgress 下载进度信息
// 注意：当前下载由 remotetools 管理，此结构体仅用于 API 兼容
type DownloadProgress struct {
	TotalBytes      int64   `json:"total_bytes"`
	DownloadedBytes int64   `json:"downloaded_bytes"`
	Percentage      float64 `json:"percentage"`
	Speed           float64 `json:"speed_bytes_per_second"`
	ETA             int     `json:"eta_seconds"`
}

// Manager 更新管理器，协调版本检查、下载和更新通知
type Manager struct {
	checker     *Checker
	notifier    *Notifier
	appDataPath string
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
	InstanceID     string
	// AppDataPath 应用数据目录（用于自托管 launcher 模式）
	AppDataPath string
	// VersionAPIURL 自定义版本检测 API URL（留空使用默认值）
	// 可设置为本地 HTTP 服务器地址用于测试自动升级逻辑
	VersionAPIURL string
}

// NewManager 创建新的更新管理器
func NewManager(config ManagerConfig) *Manager {
	if config.InstanceID == "" {
		config.InstanceID = ipc.GetInstanceID()
	}

	checker := NewChecker(config.CurrentVersion)

	// 版本检测 API 地址优先级：配置值 > 环境变量 > 默认值
	if config.VersionAPIURL != "" {
		checker.SetVersionAPIURL(config.VersionAPIURL)
	} else if envAPIURL := os.Getenv("VERSION_API_URL"); envAPIURL != "" {
		checker.SetVersionAPIURL(envAPIURL)
	}

	return &Manager{
		checker:     checker,
		notifier:    NewNotifier(config.InstanceID),
		appDataPath: config.AppDataPath,
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
// 注意：当前下载由 remotetools 管理，此方法仅返回空值以保持 API 兼容
func (m *Manager) GetDownloadProgress() DownloadProgress {
	return DownloadProgress{}
}

// GetLastError 获取最后一次错误
func (m *Manager) GetLastError() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.lastError
}

// SetProgressCallback 设置下载进度回调
// 注意：当前下载由 remotetools 管理，此方法仅保存通道引用以保持 API 兼容
func (m *Manager) SetProgressCallback(ch chan DownloadProgress) {
	m.mu.Lock()
	m.progressChan = ch
	m.mu.Unlock()
}

// CheckForUpdate 检查是否有新版本
// 优先使用 bililive-go.com API，失败时回退到 GitHub API
func (m *Manager) CheckForUpdate(ctx context.Context, includePrerelease bool) (*ReleaseInfo, error) {
	m.mu.Lock()
	m.state = UpdateStateChecking
	m.lastError = ""
	m.mu.Unlock()

	// 使用带回退的版本检查方法
	info, err := m.checker.CheckForUpdateWithFallback(includePrerelease)
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

	// 发现可用更新时，立即将版本信息注入 remotetools
	// 这样用户可以在 remotetools WebUI 中看到可用的 bgo 版本
	// 注入失败不影响检查结果，仅记录日志
	if info != nil && len(info.DownloadURLs) > 0 {
		tool, err := m.mergeVersionToRemotetools(info)
		if err != nil {
			fmt.Fprintf(os.Stderr, "[update.Manager] 注入版本信息到 remotetools 失败: %v\n", err)
		} else if tool != nil && tool.DoesToolExist() {
			// 版本已在 remotetools 中下载完成，直接标记为 Ready
			m.mu.Lock()
			m.state = UpdateStateReady
			m.downloadPath = tool.GetToolPath()
			m.mu.Unlock()
			fmt.Fprintf(os.Stderr, "[update.Manager] 版本 %s 已在 remotetools 中存在，直接标记为 Ready\n", info.Version)
		}
	}

	return info, nil
}

// DownloadUpdate 下载可用的更新
// 通过 remotetools 管理下载：构造 config JSON 并 merge 进 remotetools 实例
func (m *Manager) DownloadUpdate(ctx context.Context) error {
	m.mu.RLock()
	info := m.availableInfo
	m.mu.RUnlock()

	if info == nil {
		return fmt.Errorf("没有可用的更新")
	}

	if len(info.DownloadURLs) == 0 {
		return fmt.Errorf("没有可用的下载链接")
	}

	m.mu.Lock()
	m.state = UpdateStateDownloading
	m.lastError = ""
	m.mu.Unlock()

	// 将版本信息合并到 remotetools
	tool, err := m.mergeVersionToRemotetools(info)
	if err != nil {
		m.mu.Lock()
		m.state = UpdateStateFailed
		m.lastError = fmt.Sprintf("构造 remotetools 配置失败: %v", err)
		m.mu.Unlock()
		return err
	}

	// 如果已下载则跳过
	if tool.DoesToolExist() {
		m.mu.Lock()
		m.state = UpdateStateReady
		m.downloadPath = tool.GetToolPath()
		m.mu.Unlock()
		return nil
	}

	// 通过 remotetools 下载（内部处理多镜像重试、断点续传）
	if err := tool.Install(); err != nil {
		m.mu.Lock()
		m.state = UpdateStateFailed
		m.lastError = fmt.Sprintf("下载失败: %v", err)
		m.mu.Unlock()
		return fmt.Errorf("下载失败: %w", err)
	}

	m.mu.Lock()
	m.state = UpdateStateReady
	m.downloadPath = tool.GetToolPath()
	m.mu.Unlock()

	return nil
}

// CancelDownload 取消下载
// 注意：当前下载由 remotetools 管理，此方法仅保持 API 兼容
func (m *Manager) CancelDownload() {
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
	return m.notifier.Disconnect()
}

// IsLauncherConnected 检查是否连接到启动器
func (m *Manager) IsLauncherConnected() bool {
	return m.notifier.IsConnected()
}

// ApplyUpdateSelfHosted 应用更新（自托管 launcher 模式）
// remotetools 已完成下载和解压，直接从 tool 实例获取二进制路径
// 然后更新 launcher-state.json 指向新版本
func (m *Manager) ApplyUpdateSelfHosted(ctx context.Context) error {
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

	if m.appDataPath == "" {
		return fmt.Errorf("未配置应用数据目录，无法使用自托管 launcher 模式")
	}

	m.mu.Lock()
	m.state = UpdateStateApplying
	m.mu.Unlock()

	// downloadPath 已经是 remotetools 解压后的二进制路径
	// 必须转换为绝对路径，因为 launcher.Check() 会对非绝对路径拼接 appDataPath 前缀
	// 如果 downloadPath 本身已经包含 appDataPath（如 ".appdata/external_tools/..."），
	// 拼接后会导致路径重复（如 ".appdata/.appdata/external_tools/..."）
	newBinaryPath, err := filepath.Abs(downloadPath)
	if err != nil {
		m.mu.Lock()
		m.state = UpdateStateFailed
		m.lastError = fmt.Sprintf("解析更新文件路径失败: %v", err)
		m.mu.Unlock()
		return fmt.Errorf("解析更新文件路径失败: %w", err)
	}

	// 验证文件存在
	if _, err := os.Stat(newBinaryPath); err != nil {
		m.mu.Lock()
		m.state = UpdateStateFailed
		m.lastError = fmt.Sprintf("更新文件不存在: %v", err)
		m.mu.Unlock()
		return fmt.Errorf("更新文件不存在: %w", err)
	}

	// 加载或创建 launcher-state.json
	statePath := filepath.Join(m.appDataPath, "launcher-state.json")
	state_data := &launcherState{
		ActiveVersion:    info.Version,
		ActiveBinaryPath: newBinaryPath,
		BackupVersion:    m.currentVer,
		StartupTimeout:   60,
		MaxRetries:       3,
	}

	// 保留当前版本作为备份
	if exePath, err := os.Executable(); err == nil {
		state_data.BackupBinaryPath = exePath
	}

	fmt.Fprintf(os.Stderr, "[ApplyUpdateSelfHosted] 准备写入 launcher-state.json: path=%s, activeVersion=%s, activeBinary=%s, backupVersion=%s, backupBinary=%s\n",
		statePath, state_data.ActiveVersion, state_data.ActiveBinaryPath, state_data.BackupVersion, state_data.BackupBinaryPath)

	// 保存状态
	if err := saveLauncherState(statePath, state_data); err != nil {
		m.mu.Lock()
		m.state = UpdateStateFailed
		m.lastError = fmt.Sprintf("保存启动器状态失败: %v", err)
		m.mu.Unlock()
		return fmt.Errorf("保存启动器状态失败: %w", err)
	}

	m.mu.Lock()
	m.state = UpdateStateReady // 保持 Ready 状态，等待重启
	m.mu.Unlock()

	return nil
}

const bgoToolName = "bililive-go"

// bgoEntryName 返回当前平台的 bgo 二进制文件名
// 命名格式与构建脚本一致：bililive-{GOOS}-{GOARCH}{.exe}
func bgoEntryName() string {
	ext := ""
	if runtime.GOOS == "windows" {
		ext = ".exe"
	}
	return fmt.Sprintf("bililive-%s-%s%s", runtime.GOOS, runtime.GOARCH, ext)
}

// mergeVersionToRemotetools 将 ReleaseInfo 注册为 remotetools 工具配置
// 返回对应版本的 Tool 实例，可直接调用 Install() 下载
func (m *Manager) mergeVersionToRemotetools(info *ReleaseInfo) (remotetools.Tool, error) {
	api := remotetools.Get()
	if api == nil {
		return nil, fmt.Errorf("remotetools API 未初始化")
	}

	// 直接使用 remotetools 导出的配置结构体，无需 JSON 序列化
	cfg := &rtconfig.ToolConfig{
		ToolName:     bgoToolName,
		Version:      info.Version,
		DownloadURL:  rtconfig.OsArchSpecificString{Values: info.DownloadURLs},
		PathToEntry:  rtconfig.OsArchSpecificString{Values: []string{bgoEntryName()}},
		IsExecutable: true,
		PrintInfoCmd: rtconfig.StringArray{"--version"},
	}

	api.AddToolConfig(cfg)

	// 获取对应版本的 Tool
	tool, err := api.GetToolWithVersion(bgoToolName, info.Version)
	if err != nil {
		return nil, fmt.Errorf("获取 tool 实例失败: %w", err)
	}

	return tool, nil
}

// launcherState 启动器状态（与 launcher 包保持一致）
type launcherState struct {
	ActiveVersion    string `json:"active_version"`
	ActiveBinaryPath string `json:"active_binary_path,omitempty"`
	BackupVersion    string `json:"backup_version,omitempty"`
	BackupBinaryPath string `json:"backup_binary_path,omitempty"`
	StartupTimeout   int    `json:"startup_timeout"`
	MaxRetries       int    `json:"max_retries"`
}

// saveLauncherState 保存启动器状态
func saveLauncherState(path string, state *launcherState) error {
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0644)
}
