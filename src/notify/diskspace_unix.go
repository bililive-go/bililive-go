//go:build !windows

package notify

import "syscall"

// getDiskFreeSpace 获取指定路径所在磁盘的剩余可用空间（字节）
func getDiskFreeSpace(path string) (uint64, error) {
	var stat syscall.Statfs_t
	if err := syscall.Statfs(path, &stat); err != nil {
		return 0, err
	}
	// Bavail/Bsize 在 FreeBSD 上为 int64，在 Linux 上为 uint64。
	// 当文件系统保留空间被超额使用时 Bavail 可能为负，此时返回 0。
	if stat.Bavail <= 0 || stat.Bsize <= 0 {
		return 0, nil
	}
	return uint64(stat.Bavail) * uint64(stat.Bsize), nil
}
