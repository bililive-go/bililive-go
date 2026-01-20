//go:build !windows

package tools

import "os/exec"

// Non-Windows: just run as-is
func runWithKillOnClose(cmd *exec.Cmd) error {
	return cmd.Run()
}

// runWithKillOnCloseAndGetPID 与 runWithKillOnClose 相同，但在进程启动后通过回调传递 PID
func runWithKillOnCloseAndGetPID(cmd *exec.Cmd, onPID func(pid int)) error {
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
