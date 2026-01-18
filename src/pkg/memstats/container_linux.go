//go:build linux

package memstats

import (
	"os"
	"strconv"
	"strings"
)

// ContainerMemoryStats 容器内存统计
type ContainerMemoryStats struct {
	Limit uint64 `json:"limit"` // 内存限制 (bytes)
	Used  uint64 `json:"used"`  // 已使用内存 (bytes)
}

// IsInContainer 检测是否在容器环境中
func IsInContainer() bool {
	// 方法1: 检查 /.dockerenv 文件
	if _, err := os.Stat("/.dockerenv"); err == nil {
		return true
	}

	// 方法2: 检查 /proc/1/cgroup 是否包含 docker/lxc/kubepods
	data, err := os.ReadFile("/proc/1/cgroup")
	if err == nil {
		content := string(data)
		if strings.Contains(content, "docker") ||
			strings.Contains(content, "lxc") ||
			strings.Contains(content, "kubepods") ||
			strings.Contains(content, "containerd") {
			return true
		}
	}

	// 方法3: 检查 cgroup v2 的 /proc/1/mountinfo
	data, err = os.ReadFile("/proc/1/mountinfo")
	if err == nil {
		content := string(data)
		if strings.Contains(content, "/docker/") ||
			strings.Contains(content, "/lxc/") ||
			strings.Contains(content, "/kubepods/") {
			return true
		}
	}

	return false
}

// GetContainerMemory 获取容器内存统计
// 支持 cgroup v1 和 v2
func GetContainerMemory() (*ContainerMemoryStats, error) {
	stats := &ContainerMemoryStats{}

	// 尝试 cgroup v2 路径
	if limit, err := readCgroupV2MemoryLimit(); err == nil {
		stats.Limit = limit
		if used, err := readCgroupV2MemoryUsage(); err == nil {
			stats.Used = used
		}
		return stats, nil
	}

	// 回退到 cgroup v1 路径
	if limit, err := readCgroupV1MemoryLimit(); err == nil {
		stats.Limit = limit
		if used, err := readCgroupV1MemoryUsage(); err == nil {
			stats.Used = used
		}
		return stats, nil
	}

	return nil, os.ErrNotExist
}

// cgroup v2 路径
func readCgroupV2MemoryLimit() (uint64, error) {
	return readMemoryFile("/sys/fs/cgroup/memory.max")
}

func readCgroupV2MemoryUsage() (uint64, error) {
	return readMemoryFile("/sys/fs/cgroup/memory.current")
}

// cgroup v1 路径
func readCgroupV1MemoryLimit() (uint64, error) {
	return readMemoryFile("/sys/fs/cgroup/memory/memory.limit_in_bytes")
}

func readCgroupV1MemoryUsage() (uint64, error) {
	return readMemoryFile("/sys/fs/cgroup/memory/memory.usage_in_bytes")
}

// readMemoryFile 读取内存文件并解析为 uint64
func readMemoryFile(path string) (uint64, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}

	content := strings.TrimSpace(string(data))

	// cgroup v2 中 "max" 表示没有限制
	if content == "max" {
		return 0, nil
	}

	value, err := strconv.ParseUint(content, 10, 64)
	if err != nil {
		return 0, err
	}

	// 如果值过大（>= 2^62），认为没有设置限制
	if value >= 1<<62 {
		return 0, nil
	}

	return value, nil
}
