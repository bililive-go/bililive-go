// Package openlist 提供 OpenList 服务管理和 API 客户端
package openlist

import (
	"bufio"
	"context"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/bililive-go/bililive-go/src/configs"
	bilisentry "github.com/bililive-go/bililive-go/src/pkg/sentry"
	bililiveTools "github.com/bililive-go/bililive-go/src/tools"
	"github.com/kira1928/remotetools/pkg/tools"
	"github.com/kira1928/remotetools/pkg/webui"
	"github.com/sirupsen/logrus"
)

// 全局 OpenList 管理器（供 pipeline 等组件使用）
var (
	globalManager *Manager
	globalMu      sync.RWMutex
)

// SetGlobalManager 设置全局 OpenList 管理器
func SetGlobalManager(m *Manager) {
	globalMu.Lock()
	defer globalMu.Unlock()
	globalManager = m
}

// ClearGlobalManagerIf 仅当全局管理器等于 expected 时才清除（CAS 语义）
// 用于异步清理路径，避免误清已被新实例替换的管理器
// 返回是否成功清除
func ClearGlobalManagerIf(expected *Manager) bool {
	globalMu.Lock()
	defer globalMu.Unlock()
	if globalManager == expected {
		globalManager = nil
		return true
	}
	return false
}

// GetGlobalManager 获取全局 OpenList 管理器
func GetGlobalManager() *Manager {
	globalMu.RLock()
	defer globalMu.RUnlock()
	return globalManager
}

// Manager OpenList 进程管理器
type Manager struct {
	dataPath    string
	port        int
	apiEndpoint string
	external    bool
	process     *exec.Cmd
	logFile     *os.File // 日志文件句柄，用于关闭时释放

	mu      sync.Mutex
	running bool
	stopCh  chan struct{}
}

// NewRemoteManager 创建连接已有 OpenList 服务的管理器。
func NewRemoteManager(endpoint string) *Manager {
	return &Manager{
		apiEndpoint: strings.TrimRight(endpoint, "/"),
		external:    true,
	}
}

// NewManager 创建 OpenList 管理器
func NewManager(dataPath string, port int) *Manager {
	if port == 0 {
		port = 5244
	}
	return &Manager{
		dataPath:    dataPath,
		port:        port,
		apiEndpoint: fmt.Sprintf("http://127.0.0.1:%d", port),
	}
}

// Start 启动 OpenList 服务
func (m *Manager) Start(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.running {
		return nil
	}
	if m.external {
		if err := m.waitForReady(ctx, 10*time.Second); err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			logrus.WithError(err).Warn("外部 OpenList 当前不可用，将在后续请求时重试")
		}
		if err := webui.RegisterToolWebUI("openlist", m.apiEndpoint); err != nil {
			logrus.WithError(err).Warn("注册外部 OpenList Web UI 代理失败")
		}
		m.running = true
		logrus.WithField("url", m.apiEndpoint).Info("已连接外部 OpenList")
		return nil
	}

	// 0. 等待 remotetools 初始化完成
	if err := bililiveTools.WaitForToolsInit(ctx); err != nil {
		return fmt.Errorf("等待 remotetools 初始化失败: %w", err)
	}

	// 1. 获取 remotetools API
	api := tools.Get()
	if api == nil {
		return fmt.Errorf("remotetools API 未初始化")
	}

	// 2. 获取 OpenList 工具
	openlistTool, err := api.GetTool("openlist")
	if err != nil {
		return fmt.Errorf("OpenList 工具未配置: %w", err)
	}

	// 3. 确保工具已安装
	if !openlistTool.DoesToolExist() {
		logrus.Info("正在下载 OpenList...")
		if err := openlistTool.Install(); err != nil {
			return fmt.Errorf("下载 OpenList 失败: %w", err)
		}
	}

	toolPath := openlistTool.GetToolPath()
	if toolPath == "" {
		return fmt.Errorf("无法获取 OpenList 可执行文件路径")
	}

	// 转换为绝对路径（remotetools 返回的可能是相对路径）
	if !filepath.IsAbs(toolPath) {
		absPath, err := filepath.Abs(toolPath)
		if err != nil {
			return fmt.Errorf("转换绝对路径失败: %w", err)
		}
		toolPath = absPath
	}

	// 4. 确保数据目录存在
	if err := os.MkdirAll(m.dataPath, 0755); err != nil {
		return fmt.Errorf("创建数据目录失败: %w", err)
	}

	// 5. 启动 OpenList 进程
	m.stopCh = make(chan struct{})
	m.process = exec.CommandContext(ctx, toolPath, "server",
		"--data", m.dataPath,
		"--no-prefix",
	)
	m.process.Dir = m.dataPath
	m.process.Env = append(os.Environ(), fmt.Sprintf("ALIST_PORT=%d", m.port))

	// 设置输出
	logFile := filepath.Join(m.dataPath, "openlist.log")
	if f, err := os.OpenFile(logFile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644); err == nil {
		m.logFile = f
		m.process.Stdout = f
		m.process.Stderr = f
	} else {
		logrus.WithError(err).Warn("无法打开 OpenList 日志文件")
	}

	if err := m.process.Start(); err != nil {
		return fmt.Errorf("启动 OpenList 失败: %w", err)
	}

	m.running = true

	// 6. 等待服务就绪
	if err := m.waitForReady(ctx, 30*time.Second); err != nil {
		m.stopInternal()
		return err
	}

	// 7. 检查是否首次启动，如果是则从日志中读取初始密码并保存到配置
	m.saveInitialCredentials(logFile)

	// 8. 注册反向代理
	if err := webui.RegisterToolWebUI("openlist", m.apiEndpoint); err != nil {
		logrus.WithError(err).Warn("注册 OpenList Web UI 代理失败")
	}

	logrus.WithField("port", m.port).Info("OpenList 已启动")

	// 9. 监控进程
	bilisentry.Go(m.watchProcess)

	return nil
}

// waitForReady 等待 OpenList 服务就绪
func (m *Manager) waitForReady(ctx context.Context, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	client := &http.Client{Timeout: 2 * time.Second}

	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		resp, err := client.Get(m.apiEndpoint + "/api/public/settings")
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == 200 {
				return nil
			}
		}
		time.Sleep(500 * time.Millisecond)
	}
	return fmt.Errorf("OpenList 服务启动超时")
}

// saveInitialCredentials 从日志中读取首次启动的初始密码并保存到配置
func (m *Manager) saveInitialCredentials(logFile string) {
	config := configs.GetCurrentConfig()
	if config == nil {
		return
	}

	// 如果已经配置了用户名密码，验证有效性
	if config.OpenList.Username != "" && config.OpenList.Password != "" {
		// 用现有凭据尝试登录，验证是否有效
		testClient := NewClient(m.apiEndpoint, "")
		if _, err := testClient.GetToken(context.Background(), config.OpenList.Username, config.OpenList.Password); err == nil {
			return // 凭据有效，无需更新
		}
		// 凭据无效（如占位值 root/root），继续从日志读取真实密码
	}

	// 读取日志文件查找初始密码
	file, err := os.Open(logFile)
	if err != nil {
		return
	}
	defer file.Close()

	var initialPassword string
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.Contains(line, "initial password is:") {
			// 格式: "Successfully created the admin user and the initial password is: xxx"
			parts := strings.Split(line, "initial password is:")
			if len(parts) == 2 {
				initialPassword = strings.TrimSpace(parts[1])
			}
		}
	}

	if initialPassword == "" {
		return
	}

	// 验证密码有效性：日志是追加模式，旧密码可能已被用户修改
	// 只有实际能登录时才保存
	client := NewClient(m.apiEndpoint, "")
	if _, err := client.GetToken(context.Background(), "admin", initialPassword); err != nil {
		logrus.WithError(err).Debug("初始密码验证失败，跳过保存（可能已被修改）")
		return
	}

	// 保存到配置
	if _, err := configs.UpdateWithRetry(func(c *configs.Config) error {
		c.OpenList.Username = "admin"
		c.OpenList.Password = initialPassword
		return nil
	}, 3, 10*time.Millisecond); err != nil {
		logrus.WithError(err).Warn("保存 OpenList 初始密码到配置文件失败")
	} else {
		logrus.Info("OpenList 首次启动，初始密码已保存到配置文件")
	}
}

// watchProcess 监控进程状态
func (m *Manager) watchProcess() {
	if m.process == nil {
		return
	}

	err := m.process.Wait()

	m.mu.Lock()
	wasRunning := m.running
	m.running = false
	m.mu.Unlock()

	select {
	case <-m.stopCh:
		// 正常停止（stopInternal 已负责关闭 m.logFile）
	default:
		// 异常退出：关闭日志文件句柄，避免 fd 泄漏
		if wasRunning {
			m.mu.Lock()
			if m.logFile != nil {
				m.logFile.Close()
				m.logFile = nil
			}
			m.mu.Unlock()
			logrus.WithError(err).Warn("OpenList 进程异常退出")
		}
	}
}

// Stop 停止 OpenList 服务
func (m *Manager) Stop() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.stopInternal()
}

// stopInternal 内部停止方法（需要持有锁）
func (m *Manager) stopInternal() error {
	if !m.running {
		return nil
	}

	if m.stopCh != nil {
		close(m.stopCh)
	}

	// 取消注册代理
	webui.UnregisterToolWebUI("openlist")

	if m.process != nil && m.process.Process != nil {
		m.process.Process.Kill()
	}

	// 关闭日志文件句柄
	if m.logFile != nil {
		m.logFile.Close()
		m.logFile = nil
	}

	m.running = false
	if m.external {
		logrus.Info("已断开外部 OpenList")
	} else {
		logrus.Info("OpenList 已停止")
	}
	return nil
}

// IsRunning 检查是否运行中
func (m *Manager) IsRunning() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.running
}

// GetAPIEndpoint 获取 API 地址
func (m *Manager) GetAPIEndpoint() string {
	return m.apiEndpoint
}

// GetWebUIPath 获取 Web UI 访问路径（通过反向代理）
func (m *Manager) GetWebUIPath() string {
	return "/remotetools/tool/openlist/"
}

// GetPort 获取端口
func (m *Manager) GetPort() int {
	return m.port
}

// GetDataPath 获取数据目录
func (m *Manager) GetDataPath() string {
	return m.dataPath
}

// IsExternal 返回是否连接已有的外部 OpenList。
func (m *Manager) IsExternal() bool {
	return m.external
}

// GetClient 获取已认证的 API 客户端
// 如果提供了 token，直接使用；否则使用用户名密码登录获取 token
// token 有效性由 withRetry 在实际 API 调用时自动处理（401/403 时自动刷新）
func (m *Manager) GetClient(ctx context.Context, token, username, password string) (*Client, error) {
	client := NewClient(m.apiEndpoint, "")

	// 设置凭据（用于 token 自动刷新）
	if username != "" && password != "" {
		client.SetCredentials(username, password)
	}

	// 有 token 时直接使用，不预验证（避免需要管理员权限的 API 调用）
	// withRetry 会在实际调用遇到 401/403 时自动用密码刷新 token
	if token != "" {
		client.SetToken(token)
		return client, nil
	}

	// 无 token，使用用户名密码登录
	if username != "" && password != "" {
		newToken, err := client.GetToken(ctx, username, password)
		if err != nil {
			return nil, fmt.Errorf("OpenList 登录失败: %w", err)
		}
		client.SetToken(newToken)
		return client, nil
	}

	// 无凭据，返回无 token 的客户端（部分 API 可能不需要认证）
	return client, nil
}
