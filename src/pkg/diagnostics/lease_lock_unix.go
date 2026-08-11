//go:build !windows

package diagnostics

import (
	"errors"
	"os"

	"golang.org/x/sys/unix"
)

// tryLockLeaseFile 尝试对整个租约锁文件取得独占 advisory lock。
// acquired=false 且 err=nil 表示另一个进程仍持有锁。
func tryLockLeaseFile(file *os.File) (acquired bool, err error) {
	err = unix.Flock(int(file.Fd()), unix.LOCK_EX|unix.LOCK_NB)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, unix.EWOULDBLOCK) || errors.Is(err, unix.EAGAIN) {
		return false, nil
	}
	return false, err
}

func unlockLeaseFile(file *os.File) error {
	return unix.Flock(int(file.Fd()), unix.LOCK_UN)
}
