package memstats

import (
	"github.com/shirou/gopsutil/v3/process"
)

// ProcessIOStats 进程 I/O 统计
type ProcessIOStats struct {
	PID        int32  `json:"pid"`
	Name       string `json:"name"`
	ReadCount  uint64 `json:"read_count"`  // 读操作次数
	WriteCount uint64 `json:"write_count"` // 写操作次数
	ReadBytes  uint64 `json:"read_bytes"`  // 实际读取字节数
	WriteBytes uint64 `json:"write_bytes"` // 实际写入字节数
}

// GetProcessIO 获取指定进程的 I/O 统计
// 在 Windows 和 Linux 上均可用，无需特殊权限（可读取自身和子进程）
func GetProcessIO(pid int) (*ProcessIOStats, error) {
	p, err := process.NewProcess(int32(pid))
	if err != nil {
		return nil, err
	}

	name, _ := p.Name()
	ioCounters, err := p.IOCounters()
	if err != nil {
		return nil, err
	}

	return &ProcessIOStats{
		PID:        int32(pid),
		Name:       name,
		ReadCount:  ioCounters.ReadCount,
		WriteCount: ioCounters.WriteCount,
		ReadBytes:  ioCounters.ReadBytes,
		WriteBytes: ioCounters.WriteBytes,
	}, nil
}
