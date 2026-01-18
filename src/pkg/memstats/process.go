package memstats

import (
	"github.com/shirou/gopsutil/v3/process"
)

// ProcessMemoryStats 进程内存统计
type ProcessMemoryStats struct {
	PID  int32  `json:"pid"`
	Name string `json:"name"`
	RSS  uint64 `json:"rss"` // Resident Set Size (bytes)
	VMS  uint64 `json:"vms"` // Virtual Memory Size (bytes)
}

// GetProcessMemory 获取指定进程的内存统计
func GetProcessMemory(pid int) (*ProcessMemoryStats, error) {
	p, err := process.NewProcess(int32(pid))
	if err != nil {
		return nil, err
	}

	name, _ := p.Name()
	memInfo, err := p.MemoryInfo()
	if err != nil {
		return nil, err
	}

	return &ProcessMemoryStats{
		PID:  int32(pid),
		Name: name,
		RSS:  memInfo.RSS,
		VMS:  memInfo.VMS,
	}, nil
}
