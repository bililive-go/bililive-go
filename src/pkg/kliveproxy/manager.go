// Package kliveproxy 管理 klive 工具的启动
// klive 是一个通过 remotetools 管理的外部工具，提供用户认证和远程访问功能
// bgo 只需要告诉 klive 本地前端地址，klive 自己管理所有其他配置
package kliveproxy

import (
	"context"
	"fmt"
	"os/exec"
	"sync"
	"time"

	"github.com/bililive-go/bililive-go/src/log"
	bilisentry "github.com/bililive-go/bililive-go/src/pkg/sentry"
	bgotools "github.com/bililive-go/bililive-go/src/tools"
	"github.com/kira1928/remotetools/pkg/tools"
	"github.com/kira1928/remotetools/pkg/webui"
)

const (
	// ToolName klive 工具名称
	ToolName = "klive"

	// KliveDefaultPort klive 默认端口
	KliveDefaultPort = 8090
)

// Manager klive 工具管理器
type Manager struct {
	ctx       context.Context
	cancel    context.CancelFunc
	cmd       *exec.Cmd
	klivePort int
	mu        sync.Mutex
}

// NewManager 创建 klive 工具管理器
func NewManager() *Manager {
	return &Manager{
		klivePort: KliveDefaultPort,
	}
}

// Start 启动 klive 工具
// bgoAddr: 本地 bgo Web UI 地址（如 :8080 或 localhost:8080）
func (m *Manager) Start(parentCtx context.Context, bgoAddr string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	logger := log.GetLogger()

	// 获取 klive 工具
	kliveTool, err := tools.Get().GetTool(ToolName)
	if err != nil {
		// 工具不存在不是错误，可能用户没有安装 klive
		logger.Debugf("klive 工具未配置或不可用: %v", err)
		return nil
	}

	// 检查工具是否存在
	if !kliveTool.DoesToolExist() {
		logger.Info("klive 工具不存在，尝试安装...")
		if err := kliveTool.Install(); err != nil {
			logger.WithError(err).Debug("klive 工具安装失败，远程访问功能不可用")
			return nil
		}
		logger.Info("klive 工具安装完成")
	}

	m.ctx, m.cancel = context.WithCancel(parentCtx)

	// 注册 klive WebUI 到 remotetools 的反向代理
	// 这样用户可以通过 /tools/tool/klive/ 访问 klive 的前端
	kliveURL := fmt.Sprintf("http://localhost:%d", m.klivePort)
	if err := webui.RegisterToolWebUI(ToolName, kliveURL); err != nil {
		logger.WithError(err).Warn("注册 klive WebUI 代理失败")
	} else {
		logger.Infof("klive WebUI 已注册到 /tool/%s/", ToolName)
	}

	// 启动 klive 进程
	bilisentry.Go(func() { m.runLoop(bgoAddr) })

	return nil
}

// runLoop 运行循环，自动重启
func (m *Manager) runLoop(bgoAddr string) {
	logger := log.GetLogger()

	for {
		select {
		case <-m.ctx.Done():
			logger.Debug("klive 工具管理器：上下文已取消，退出")
			// 取消注册 WebUI
			webui.UnregisterToolWebUI(ToolName)
			return
		default:
		}

		// 获取 klive 工具
		kliveTool, err := tools.Get().GetTool(ToolName)
		if err != nil {
			logger.Debugf("获取 klive 工具失败: %v，10秒后重试", err)
			time.Sleep(10 * time.Second)
			continue
		}

		if !kliveTool.DoesToolExist() {
			logger.Debug("klive 工具不存在，10秒后重试")
			time.Sleep(10 * time.Second)
			continue
		}

		// 创建执行命令
		// klive --bgo-addr <bgoAddr>
		cmd, err := kliveTool.CreateExecuteCmd("--bgo-addr", bgoAddr)
		if err != nil {
			logger.Debugf("创建 klive 命令失败: %v，10秒后重试", err)
			time.Sleep(10 * time.Second)
			continue
		}

		// 设置上下文
		m.mu.Lock()
		m.cmd = cmd
		m.mu.Unlock()

		logger.Infof("启动 klive 工具 (bgo-addr=%s, klive-port=%d)", bgoAddr, m.klivePort)

		// 运行命令（使用 Start + Wait 以便获取 PID）
		if err := cmd.Start(); err != nil {
			logger.Debugf("启动 klive 进程失败: %v，10秒后重试", err)
			time.Sleep(10 * time.Second)
			continue
		}

		// 注册进程 PID 到通用跟踪器
		if cmd.Process != nil {
			bgotools.RegisterProcess("klive", cmd.Process.Pid, bgotools.ProcessCategoryKlive)
			logger.Debugf("klive 进程已启动，PID: %d", cmd.Process.Pid)
		}

		// 等待进程退出
		err = cmd.Wait()

		// 进程退出后取消注册
		bgotools.UnregisterProcess("klive")

		if err != nil {
			// 检查是否是上下文取消导致的
			if m.ctx.Err() != nil {
				return
			}
			logger.Debugf("klive 进程退出: %v，10秒后重启", err)
		} else {
			logger.Debug("klive 进程正常退出")
		}

		time.Sleep(10 * time.Second)
	}
}

// Stop 停止 klive 工具
func (m *Manager) Stop() {
	m.mu.Lock()
	defer m.mu.Unlock()

	// 取消注册 WebUI
	webui.UnregisterToolWebUI(ToolName)

	if m.cancel != nil {
		m.cancel()
	}

	if m.cmd != nil && m.cmd.Process != nil {
		_ = m.cmd.Process.Kill()
	}
}

// IsRunning 检查是否正在运行
func (m *Manager) IsRunning() bool {
	m.mu.Lock()
	defer m.mu.Unlock()

	return m.cmd != nil && m.cmd.Process != nil && m.cmd.ProcessState == nil
}

// GetKlivePort 获取 klive 端口
func (m *Manager) GetKlivePort() int {
	return m.klivePort
}
