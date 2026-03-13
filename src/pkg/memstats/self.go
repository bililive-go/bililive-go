package memstats

import (
	"os"
	"runtime"

	"github.com/shirou/gopsutil/v3/process"
)

// SelfMemoryStats Go 运行时内存统计 + 进程级内存统计
type SelfMemoryStats struct {
	Alloc        uint64 `json:"alloc"`         // 当前堆内存使用 (bytes)
	TotalAlloc   uint64 `json:"total_alloc"`   // 累计分配内存 (bytes)
	Sys          uint64 `json:"sys"`           // Go 运行时从系统获取的虚拟地址空间 (bytes)
	NumGC        uint32 `json:"num_gc"`        // GC 累计次数
	RSS          uint64 `json:"rss"`           // 实际物理内存 (Resident Set Size, bytes)
	VMS          uint64 `json:"vms"`           // 虚拟内存 (Virtual Memory Size, bytes)
	NumGoroutine int    `json:"num_goroutine"` // 当前 Goroutine 数量
}

// GetSelfMemory 获取 Go 运行时内存统计 + 进程级内存统计
func GetSelfMemory() SelfMemoryStats {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)

	stats := SelfMemoryStats{
		Alloc:        m.Alloc,
		TotalAlloc:   m.TotalAlloc,
		Sys:          m.Sys,
		NumGC:        m.NumGC,
		NumGoroutine: runtime.NumGoroutine(),
	}

	// 通过 gopsutil 获取自身进程的真实 RSS/VMS
	pid := int32(os.Getpid())
	if p, err := process.NewProcess(pid); err == nil {
		if memInfo, err := p.MemoryInfo(); err == nil {
			stats.RSS = memInfo.RSS
			stats.VMS = memInfo.VMS
		}
	}

	// 如果获取 RSS/VMS 失败，使用 Go runtime 的 Sys 作为兜底
	if stats.RSS == 0 {
		stats.RSS = m.Sys
	}

	return stats
}
