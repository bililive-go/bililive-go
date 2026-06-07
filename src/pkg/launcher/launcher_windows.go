//go:build windows

package launcher

import "os/exec"

// setProcAttr is a no-op on Windows; process group management via Setpgid is Unix-only.
func setProcAttr(cmd *exec.Cmd) {}

// killProcessGroup is a no-op on Windows; the caller will fall through to Process.Kill().
func killProcessGroup(pid int) {}
