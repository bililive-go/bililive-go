//go:build !windows

package tools

import (
	"os"
	"syscall"
)

// killProcessTree 终止指定 PID 的进程及其所有子进程。
// Unix 实现：发送 SIGKILL 到进程组（负 PID）。
func killProcessTree(pid int) error {
	// 尝试杀掉整个进程组
	if err := syscall.Kill(-pid, syscall.SIGKILL); err != nil {
		// 回退到杀单个进程
		p, findErr := os.FindProcess(pid)
		if findErr != nil {
			return findErr
		}
		return p.Kill()
	}
	return nil
}
