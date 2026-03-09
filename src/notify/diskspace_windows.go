//go:build windows

package notify

import (
	"syscall"
	"unsafe"
)

// getDiskFreeSpace 获取指定路径所在磁盘的剩余可用空间（字节）
func getDiskFreeSpace(path string) (uint64, error) {
	kernel32 := syscall.NewLazyDLL("kernel32.dll")
	proc := kernel32.NewProc("GetDiskFreeSpaceExW")

	pathPtr, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return 0, err
	}

	var freeBytesAvailable uint64
	ret, _, err := proc.Call(
		uintptr(unsafe.Pointer(pathPtr)),
		uintptr(unsafe.Pointer(&freeBytesAvailable)),
		0, 0,
	)
	if ret == 0 {
		return 0, err
	}
	return freeBytesAvailable, nil
}
