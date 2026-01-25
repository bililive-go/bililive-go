// Package openlist 提供 OpenList 服务管理和 API 客户端
package openlist

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"

	bilisentry "github.com/bililive-go/bililive-go/src/pkg/sentry"
	"github.com/kira1928/remotetools/pkg/tools"
	"github.com/kira1928/remotetools/pkg/webui"
	"github.com/sirupsen/logrus"
)

// Manager OpenList 进程管理器
type Manager struct {
	dataPath    string
	port        int
	apiEndpoint string
	process     *exec.Cmd

	mu      sync.Mutex
	running bool
	stopCh  chan struct{}
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
		m.process.Stdout = f
		m.process.Stderr = f
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

	// 7. 注册反向代理
	if err := webui.RegisterToolWebUI("openlist", m.apiEndpoint); err != nil {
		logrus.WithError(err).Warn("注册 OpenList Web UI 代理失败")
	}

	logrus.WithField("port", m.port).Info("OpenList 已启动")

	// 8. 监控进程
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
		// 正常停止
	default:
		// 异常退出
		if wasRunning {
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

	close(m.stopCh)

	// 取消注册代理
	webui.UnregisterToolWebUI("openlist")

	if m.process != nil && m.process.Process != nil {
		m.process.Process.Kill()
	}

	m.running = false
	logrus.Info("OpenList 已停止")
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
