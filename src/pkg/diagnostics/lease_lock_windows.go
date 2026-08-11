//go:build windows

package diagnostics

import (
	"errors"
	"os"

	"golang.org/x/sys/windows"
)

// tryLockLeaseFile 尝试锁住文件的第一个字节。锁跟随文件句柄，在进程异常
// 退出时由 Windows 内核自动释放。
func tryLockLeaseFile(file *os.File) (acquired bool, err error) {
	var overlapped windows.Overlapped
	err = windows.LockFileEx(
		windows.Handle(file.Fd()),
		windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY,
		0,
		1,
		0,
		&overlapped,
	)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, windows.ERROR_LOCK_VIOLATION) {
		return false, nil
	}
	return false, err
}

func unlockLeaseFile(file *os.File) error {
	var overlapped windows.Overlapped
	return windows.UnlockFileEx(windows.Handle(file.Fd()), 0, 1, 0, &overlapped)
}
