//go:build !linux

package memstats

// ContainerMemoryStats 容器内存统计
// 非 Linux 平台的空实现
type ContainerMemoryStats struct {
	Limit uint64 `json:"limit"` // 内存限制 (bytes)
	Used  uint64 `json:"used"`  // 已使用内存 (bytes)
}

// IsInContainer 检测是否在容器环境中
// 非 Linux 平台始终返回 false
func IsInContainer() bool {
	return false
}

// GetContainerMemory 获取容器内存统计
// 非 Linux 平台始终返回 nil
func GetContainerMemory() (*ContainerMemoryStats, error) {
	return nil, nil
}
