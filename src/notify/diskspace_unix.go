//go:build !windows

package notify

import "syscall"

// getDiskFreeSpace 获取指定路径所在磁盘的剩余可用空间（字节）
func getDiskFreeSpace(path string) (uint64, error) {
	var stat syscall.Statfs_t
	if err := syscall.Statfs(path, &stat); err != nil {
		return 0, err
	}
	return stat.Bavail * uint64(stat.Bsize), nil
}
