// Package memstats 提供内存统计功能
// 用于收集 bililive-go 自身、子进程和 Docker 容器的内存使用信息
package memstats

// MemoryStats 内存统计信息
type MemoryStats struct {
	// 自身内存 (Go runtime)
	SelfMemory SelfMemoryStats `json:"self_memory"`
	// 子进程内存 (通过 PID 列表获取)
	ChildProcessMemory []ProcessMemoryStats `json:"child_process_memory"`
	// 容器内存 (仅 Linux 容器环境)
	ContainerMemory *ContainerMemoryStats `json:"container_memory,omitempty"`
}

// GetMemoryStats 获取完整内存统计
func GetMemoryStats(pids []int) (*MemoryStats, error) {
	stats := &MemoryStats{
		SelfMemory:         GetSelfMemory(),
		ChildProcessMemory: make([]ProcessMemoryStats, 0),
	}

	// 获取子进程内存
	for _, pid := range pids {
		if pid <= 0 {
			continue
		}
		if procMem, err := GetProcessMemory(pid); err == nil && procMem != nil {
			stats.ChildProcessMemory = append(stats.ChildProcessMemory, *procMem)
		}
	}

	// 获取容器内存 (仅 Linux 容器环境)
	if IsInContainer() {
		if containerMem, err := GetContainerMemory(); err == nil {
			stats.ContainerMemory = containerMem
		}
	}

	return stats, nil
}
