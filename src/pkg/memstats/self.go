package memstats

import "runtime"

// SelfMemoryStats Go 运行时内存统计
type SelfMemoryStats struct {
	Alloc      uint64 `json:"alloc"`       // 当前堆内存使用 (bytes)
	TotalAlloc uint64 `json:"total_alloc"` // 累计分配内存 (bytes)
	Sys        uint64 `json:"sys"`         // 从系统获取的内存 (bytes)
	NumGC      uint32 `json:"num_gc"`      // GC 次数
}

// GetSelfMemory 获取 Go 运行时内存统计
func GetSelfMemory() SelfMemoryStats {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)

	return SelfMemoryStats{
		Alloc:      m.Alloc,
		TotalAlloc: m.TotalAlloc,
		Sys:        m.Sys,
		NumGC:      m.NumGC,
	}
}
