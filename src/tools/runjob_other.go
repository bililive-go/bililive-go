//go:build !windows

package tools

import "os/exec"

// Non-Windows: just run as-is
func runWithKillOnClose(cmd *exec.Cmd) error {
	return cmd.Run()
}
