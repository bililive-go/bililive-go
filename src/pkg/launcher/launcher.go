// Package launcher 提供 bgo 的自托管启动器功能
// 当 bgo 检测到本地有更新版本时，它会进入 launcher 模式，启动更新版本的 bgo
package launcher

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/bililive-go/bililive-go/src/pkg/ipc"
	bilisentry "github.com/bililive-go/bililive-go/src/pkg/sentry"
)

// State 启动器状态（存储在 appdata/launcher-state.json）
type State struct {
	// 当前应该使用的 bgo 版本
	ActiveVersion string `json:"active_version"`
	// 当前应该使用的 bgo 可执行文件路径（可以是绝对路径，或相对于 appdata）
	ActiveBinaryPath string `json:"active_binary_path,omitempty"`
	// 备份版本号
	BackupVersion string `json:"backup_version,omitempty"`
	// 备份可执行文件路径
	BackupBinaryPath string `json:"backup_binary_path,omitempty"`
	// 优先使用入口二进制（Docker 入口 / 用户部署的原始版本）
	// 当为 true 时忽略 active_version，直接运行入口程序
	PreferEntryBinary bool `json:"prefer_entry_binary,omitempty"`
	// 启动超时时间（秒）
	StartupTimeout int `json:"startup_timeout"`
	// 最大重试次数
	MaxRetries int `json:"max_retries"`
	// 上次更新时间
	LastUpdateTime int64 `json:"last_update_time,omitempty"`
	// 启动失败计数（用于决定是否回滚）
	FailureCount int `json:"failure_count,omitempty"`
}

// DefaultState 返回默认状态
func DefaultState() *State {
	return &State{
		StartupTimeout: 60,
		MaxRetries:     3,
	}
}

// LoadState 从文件加载状态
func LoadState(path string) (*State, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	state := DefaultState()
	if err := json.Unmarshal(data, state); err != nil {
		return nil, err
	}

	return state, nil
}

// Save 保存状态到文件
func (s *State) Save(path string) error {
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0644)
}

// CheckResult 检查结果
type CheckResult struct {
	// 是否需要进入 launcher 模式
	ShouldBeLauncher bool
	// 要启动的 bgo 可执行文件路径
	TargetBinaryPath string
	// 当前 bgo 的版本
	CurrentVersion string
	// 目标版本
	TargetVersion string
	// 状态文件路径
	StatePath string
	// 加载的状态
	State *State
}

// Check 检查当前 bgo 是否应该进入 launcher 模式
// appDataPath: 应用数据目录路径
// currentVersion: 当前 bgo 版本
// currentExePath: 当前 bgo 可执行文件路径
func Check(appDataPath, currentVersion, currentExePath string) (*CheckResult, error) {
	result := &CheckResult{
		CurrentVersion: currentVersion,
		StatePath:      filepath.Join(appDataPath, "launcher-state.json"),
	}

	fmt.Fprintf(os.Stderr, "[Launcher.Check] statePath=%s, currentVersion=%q, currentExePath=%s\n",
		result.StatePath, currentVersion, currentExePath)

	// 尝试加载状态文件
	state, err := LoadState(result.StatePath)
	if err != nil {
		if os.IsNotExist(err) {
			// 状态文件不存在，正常启动
			fmt.Fprintf(os.Stderr, "[Launcher.Check] 状态文件不存在，正常启动\n")
			return result, nil
		}
		return nil, fmt.Errorf("加载启动器状态失败: %w", err)
	}
	result.State = state

	fmt.Fprintf(os.Stderr, "[Launcher.Check] 加载状态: ActiveVersion=%q, ActiveBinaryPath=%q, BackupVersion=%q, PreferEntryBinary=%v\n",
		state.ActiveVersion, state.ActiveBinaryPath, state.BackupVersion, state.PreferEntryBinary)

	// 用户偏好使用入口二进制（如 Docker 入口或原始部署版本）
	if state.PreferEntryBinary {
		fmt.Fprintf(os.Stderr, "[Launcher.Check] PreferEntryBinary=true，使用入口二进制，正常启动\n")
		return result, nil
	}

	// 检查是否有更新版本需要启动
	if state.ActiveVersion == "" || state.ActiveBinaryPath == "" {
		// 没有指定活动版本，正常启动
		fmt.Fprintf(os.Stderr, "[Launcher.Check] ActiveVersion 或 ActiveBinaryPath 为空，正常启动\n")
		return result, nil
	}

	// 检查活动版本是否与当前版本相同
	if state.ActiveVersion == currentVersion {
		// 当前版本就是活动版本，正常启动
		fmt.Fprintf(os.Stderr, "[Launcher.Check] ActiveVersion(%q) == currentVersion(%q)，正常启动\n",
			state.ActiveVersion, currentVersion)
		return result, nil
	}

	// 构建完整的二进制路径
	targetPath := state.ActiveBinaryPath
	if !filepath.IsAbs(targetPath) {
		targetPath = filepath.Join(appDataPath, targetPath)
	}

	fmt.Fprintf(os.Stderr, "[Launcher.Check] targetPath=%s\n", targetPath)

	// 检查目标二进制文件是否存在
	if _, err := os.Stat(targetPath); os.IsNotExist(err) {
		// 目标文件不存在，正常启动（可能是更新失败）
		fmt.Fprintf(os.Stderr, "[Launcher.Check] 目标文件不存在: %s\n", targetPath)
		return result, nil
	}

	// 检查目标文件是否与当前文件相同
	if sameFile(currentExePath, targetPath) {
		// 同一个文件，正常启动
		fmt.Fprintf(os.Stderr, "[Launcher.Check] sameFile=true (currentExe=%s, target=%s)，正常启动\n",
			currentExePath, targetPath)
		return result, nil
	}

	// 需要进入 launcher 模式
	fmt.Fprintf(os.Stderr, "[Launcher.Check] 需要进入 launcher 模式！targetVersion=%s, targetPath=%s\n",
		state.ActiveVersion, targetPath)
	result.ShouldBeLauncher = true
	result.TargetBinaryPath = targetPath
	result.TargetVersion = state.ActiveVersion

	return result, nil
}

// sameFile 检查两个路径是否指向同一个文件
func sameFile(path1, path2 string) bool {
	abs1, err1 := filepath.Abs(path1)
	abs2, err2 := filepath.Abs(path2)
	if err1 != nil || err2 != nil {
		return false
	}
	return abs1 == abs2
}

// Runner 启动器运行器（当进入 launcher 模式时使用）
type Runner struct {
	state       *State
	statePath   string
	targetPath  string
	instanceID  string
	server      ipc.Server
	mainProcess *exec.Cmd
	mainPID     int
	startupOK   bool
	startupCh   chan struct{} // startup_success 收到时关闭此 channel
	verbose     bool
}

// NewRunner 创建启动器运行器
func NewRunner(state *State, statePath, targetPath, instanceID string) *Runner {
	return &Runner{
		state:      state,
		statePath:  statePath,
		targetPath: targetPath,
		instanceID: instanceID,
		verbose:    true, // 默认启用详细日志
	}
}

// Run 运行启动器
func (r *Runner) Run(ctx context.Context, args []string) error {
	// 启动 IPC 服务器
	r.server = ipc.NewServer(r.instanceID)
	r.server.OnMessage(r.handleMessage)
	r.server.OnConnect(func(conn ipc.Conn) {
		r.log("主程序已连接")
	})
	r.server.OnDisconnect(func(conn ipc.Conn, err error) {
		r.log("主程序断开连接: %v", err)
	})

	if err := r.server.Start(ctx); err != nil {
		return fmt.Errorf("启动 IPC 服务器失败: %w", err)
	}
	defer r.server.Stop()

	r.log("IPC 服务器已启动，准备启动主程序: %s", r.targetPath)

	// 设置信号处理
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

	runCtx, cancel := context.WithCancel(ctx)
	go func() {
		defer bilisentry.Recover()
		<-sigChan
		r.log("收到退出信号，正在关闭...")
		cancel()
	}()

	// 启动主程序循环
	for {
		select {
		case <-runCtx.Done():
			r.stopMainProgram()
			r.log("启动器退出")
			return nil
		default:
		}

		// 启动主程序
		if err := r.startMainProgram(runCtx, args); err != nil {
			r.log("启动主程序失败: %v", err)

			// 如果有备份版本，尝试回滚
			if r.state.BackupBinaryPath != "" {
				r.log("尝试回滚到备份版本...")
				if err := r.rollback(); err != nil {
					r.log("回滚失败: %v", err)
					return fmt.Errorf("主程序启动失败且无法回滚: %w", err)
				}
				continue // 回滚后重新启动
			}
			return err
		}

		// 等待主程序确认启动或退出
		// 注意：必须有 startupCh case，否则即使子进程秒回 startup_success，
		// 也要等 StartupTimeout（60秒）才能进入 waitForMainProgram
		r.startupCh = make(chan struct{})
		startupTimer := time.NewTimer(time.Duration(r.state.StartupTimeout) * time.Second)

		select {
		case <-runCtx.Done():
			startupTimer.Stop()
			r.stopMainProgram()
			return nil
		case <-r.startupCh:
			// 启动成功确认，立即进入等待阶段
			startupTimer.Stop()
			r.log("收到启动确认，进入运行等待")
		case <-startupTimer.C:
			if !r.startupOK {
				r.log("主程序启动超时")
				r.stopMainProgram()

				// 增加失败计数
				r.state.FailureCount++
				r.state.Save(r.statePath)

				// 如果失败次数超过限制，尝试回滚
				if r.state.FailureCount >= r.state.MaxRetries && r.state.BackupBinaryPath != "" {
					r.log("启动失败次数过多，尝试回滚...")
					if err := r.rollback(); err != nil {
						r.log("回滚失败: %v", err)
					}
				}
				continue
			}
		}

		startupTimer.Stop()

		// 启动成功，重置失败计数
		if r.startupOK && r.state.FailureCount > 0 {
			r.state.FailureCount = 0
			r.state.Save(r.statePath)
		}

		// 等待主程序退出
		r.waitForMainProgram()

		// 子进程可能通过 ApplyUpdateSelfHosted 自行写入了 launcher-state.json
		// 重新加载状态文件，检查版本是否有变更
		if newState, err := LoadState(r.statePath); err == nil {
			if newState.ActiveVersion != r.state.ActiveVersion ||
				newState.ActiveBinaryPath != r.state.ActiveBinaryPath ||
				newState.PreferEntryBinary != r.state.PreferEntryBinary {
				r.log("检测到 launcher-state.json 变更: %s -> %s", r.state.ActiveVersion, newState.ActiveVersion)
				r.state = newState
				r.targetPath = newState.ActiveBinaryPath
				r.startupOK = false
				continue // 启动新版本
			}
		}

		// 主程序正常退出且无版本变更，退出启动器
		r.log("主程序正常退出，启动器也退出")
		break
	}

	return nil
}

// startMainProgram 启动主程序
func (r *Runner) startMainProgram(ctx context.Context, args []string) error {
	if _, err := os.Stat(r.targetPath); os.IsNotExist(err) {
		return fmt.Errorf("主程序不存在: %s", r.targetPath)
	}

	r.log("启动主程序: %s", r.targetPath)

	r.mainProcess = exec.CommandContext(ctx, r.targetPath, args...)
	r.mainProcess.Stdout = os.Stdout
	r.mainProcess.Stderr = os.Stderr
	r.mainProcess.Stdin = os.Stdin

	// 设置环境变量告知主程序由启动器启动
	launcherExe, _ := os.Executable()
	r.mainProcess.Env = append(os.Environ(),
		"BILILIVE_LAUNCHER=1",
		fmt.Sprintf("BILILIVE_INSTANCE_ID=%s", r.instanceID),
		fmt.Sprintf("BILILIVE_LAUNCHER_PID=%d", os.Getpid()),
		fmt.Sprintf("BILILIVE_LAUNCHER_EXE=%s", launcherExe),
	)

	if err := r.mainProcess.Start(); err != nil {
		return fmt.Errorf("启动进程失败: %w", err)
	}

	r.mainPID = r.mainProcess.Process.Pid
	r.startupOK = false
	r.log("主程序已启动，PID: %d", r.mainPID)

	return nil
}

// stopMainProgram 停止主程序
func (r *Runner) stopMainProgram() {
	if r.mainProcess == nil || r.mainProcess.Process == nil {
		return
	}

	// 发送关闭请求
	msg, _ := ipc.NewMessage(ipc.MsgTypeShutdown, ipc.ShutdownPayload{
		Reason:      "launcher_shutdown",
		GracePeriod: 30,
	})
	r.server.Broadcast(msg)

	// 等待进程退出
	done := make(chan struct{})
	go func() {
		defer bilisentry.Recover()
		r.mainProcess.Wait()
		close(done)
	}()

	select {
	case <-done:
		r.log("主程序已正常退出")
	case <-time.After(35 * time.Second):
		r.log("主程序未响应，强制终止")
		r.mainProcess.Process.Kill()
	}
}

// waitForMainProgram 等待主程序退出
func (r *Runner) waitForMainProgram() {
	if r.mainProcess == nil {
		return
	}
	r.mainProcess.Wait()
	r.log("主程序已退出")
}

// handleMessage 处理来自主程序的 IPC 消息
func (r *Runner) handleMessage(conn ipc.Conn, msg *ipc.Message) {
	r.log("收到消息: %s", msg.Type)

	switch msg.Type {
	case ipc.MsgTypeStartupSuccess:
		var payload ipc.StartupSuccessPayload
		if err := msg.ParsePayload(&payload); err == nil {
			r.log("主程序启动成功: 版本 %s, PID %d", payload.Version, payload.PID)
			r.startupOK = true
			// 通知 Run() 循环中的 select 解除阻塞
			if r.startupCh != nil {
				close(r.startupCh)
			}

			// 更新状态
			r.state.ActiveVersion = payload.Version
			r.state.LastUpdateTime = time.Now().Unix()
			r.state.FailureCount = 0
			r.state.Save(r.statePath)
		}

	case ipc.MsgTypeStartupFailed:
		var payload ipc.StartupFailedPayload
		if err := msg.ParsePayload(&payload); err == nil {
			r.log("主程序启动失败: %s", payload.Error)
			r.startupOK = false
		}

	case ipc.MsgTypeUpdateRequest:
		// 旧的 IPC 更新路径已废弃，更新由 ApplyUpdateSelfHosted 写入 launcher-state.json 处理
		r.log("收到 IPC 更新请求（已废弃，忽略）")

	case ipc.MsgTypeShutdownAck:
		r.log("主程序确认关闭")

	case ipc.MsgTypeHeartbeat:
		ackMsg, _ := ipc.NewMessage(ipc.MsgTypeHeartbeatAck, nil)
		conn.Send(ackMsg)
	}
}

// rollback 回滚到备份版本
func (r *Runner) rollback() error {
	if r.state.BackupBinaryPath == "" {
		return fmt.Errorf("没有可用的备份版本")
	}

	if _, err := os.Stat(r.state.BackupBinaryPath); os.IsNotExist(err) {
		return fmt.Errorf("备份文件不存在: %s", r.state.BackupBinaryPath)
	}

	r.log("回滚到备份版本: %s", r.state.BackupVersion)

	// 更新状态
	r.state.ActiveVersion = r.state.BackupVersion
	r.state.ActiveBinaryPath = r.state.BackupBinaryPath
	r.state.FailureCount = 0
	r.state.Save(r.statePath)

	// 更新目标路径
	r.targetPath = r.state.BackupBinaryPath

	r.log("已回滚到版本: %s", r.state.BackupVersion)
	return nil
}

// log 输出日志
func (r *Runner) log(format string, args ...any) {
	if r.verbose {
		timestamp := time.Now().Format("2006-01-02 15:04:05")
		fmt.Printf("[Launcher %s] "+format+"\n", append([]any{timestamp}, args...)...)
	}
}
