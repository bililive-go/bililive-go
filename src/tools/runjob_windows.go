//go:build windows

package tools

import (
	"fmt"
	"os/exec"
	"unsafe"

	"golang.org/x/sys/windows"
)

// runWithKillOnClose starts the command and attaches it to a Job Object
// with JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE so that when the parent process
// exits (and the job handle is closed), the child process is terminated by OS.
func runWithKillOnClose(cmd *exec.Cmd) error {
	// Start the process first so we can get its PID/handle
	if err := cmd.Start(); err != nil {
		return err
	}

	// Open process handle from PID
	proc, err := windows.OpenProcess(windows.PROCESS_ALL_ACCESS, false, uint32(cmd.Process.Pid))
	if err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return fmt.Errorf("open process failed: %w", err)
	}
	defer windows.CloseHandle(proc)

	// Create Job Object
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return fmt.Errorf("create job object failed: %w", err)
	}
	defer windows.CloseHandle(job)

	// Set KILL_ON_JOB_CLOSE
	var info windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION
	info.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	size := uint32(unsafe.Sizeof(info))
	if _, err := windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation, uintptr(unsafe.Pointer(&info)), size); err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return fmt.Errorf("set job info failed: %w", err)
	}

	// Assign process to job
	if err := windows.AssignProcessToJobObject(job, proc); err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return fmt.Errorf("assign process to job failed: %w", err)
	}

	// Wait until process exits
	return cmd.Wait()
}
