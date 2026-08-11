package diagnostics

import (
	"context"
	"runtime"
)

// recordRuntimeSample 保存低成本的进程级运行时采样。它不能替代 Flight
// Recorder 的 goroutine 调度片段，但即使 trace 文件无法解析，也能在手机
// WebUI 中判断 goroutine 激增、堆增长或 GC 长暂停是否与业务延迟同时发生。
func (m *Manager) recordRuntimeSample() {
	if m == nil || m.stopping.Load() || m.closed.Load() {
		return
	}
	var memory runtime.MemStats
	runtime.ReadMemStats(&memory)
	m.Record(context.Background(), "runtime.sample", Fields{
		"component":             "runtime",
		"lane":                  "Runtime",
		"category":              "runtime",
		"severity":              "debug",
		"goroutines":            runtime.NumGoroutine(),
		"gomaxprocs":            runtime.GOMAXPROCS(0),
		"heap_alloc_bytes":      memory.HeapAlloc,
		"heap_inuse_bytes":      memory.HeapInuse,
		"heap_objects":          memory.HeapObjects,
		"gc_cycles_total":       memory.NumGC,
		"gc_pause_total_ms":     float64(memory.PauseTotalNs) / 1e6,
		"gc_last_pause_ms":      float64(latestGCPause(memory)) / 1e6,
		"process_sys_bytes":     memory.Sys,
		"process_total_alloc":   memory.TotalAlloc,
		"runtime_sample_source": "runtime.MemStats",
	})
}

func latestGCPause(memory runtime.MemStats) uint64 {
	if memory.NumGC == 0 {
		return 0
	}
	index := (memory.NumGC + uint32(len(memory.PauseNs)) - 1) % uint32(len(memory.PauseNs))
	return memory.PauseNs[index]
}
