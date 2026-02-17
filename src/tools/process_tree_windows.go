//go:build windows

package tools

import (
	"fmt"
	"os/exec"
	"strconv"
)

// killProcessTree 终止指定 PID 的进程及其所有子进程。
// Windows 实现：使用 taskkill /T /F /PID 命令杀掉整个进程树。
// 这可以避免循环依赖：子进程的子进程持有管道 → cmd.Wait() 等管道关闭 →
// Job Object 等 cmd.Wait() 返回 → 子进程的子进程等 Job Object 关闭。
func killProcessTree(pid int) error {
	cmd := exec.Command("taskkill", "/T", "/F", "/PID", strconv.Itoa(pid))
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("taskkill 失败: %w, output: %s", err, string(output))
	}
	return nil
}
