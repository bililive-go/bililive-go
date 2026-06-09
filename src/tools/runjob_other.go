//go:build !windows

package tools

import (
	"os/exec"
	"syscall"
)

// runWithKillOnCloseAndGetPID 在非 Windows 平台上直接运行命令，并在进程启动后通过回调传递 PID
func runWithKillOnCloseAndGetPID(cmd *exec.Cmd, onPID func(pid int)) error {
	// 设置独立进程组，使 killProcessTree 能通过 syscall.Kill(-pid, SIGKILL) 杀死整个进程树
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setpgid = true

	// Start the process first so we can get its PID
	if err := cmd.Start(); err != nil {
		return err
	}

	// 回调通知 PID
	if onPID != nil && cmd.Process != nil {
		onPID(cmd.Process.Pid)
	}

	// Wait until process exits
	return cmd.Wait()
}
