package iostats

// MemoryStat 内存统计数据点
type MemoryStat struct {
	ID        int64  `json:"id"`
	Timestamp int64  `json:"timestamp"`        // Unix 毫秒
	Category  string `json:"category"`         // 类别: self, ffmpeg, bililive-tools, klive, bililive-recorder, container
	RSS       uint64 `json:"rss"`              // Resident Set Size (bytes)
	VMS       uint64 `json:"vms,omitempty"`    // Virtual Memory Size (bytes)，仅进程有
	Alloc     uint64 `json:"alloc,omitempty"`  // Go Heap Alloc (bytes)，仅 self 有
	Sys       uint64 `json:"sys,omitempty"`    // Go Sys Memory (bytes)，仅 self 有
	NumGC     uint32 `json:"num_gc,omitempty"` // GC 次数，仅 self 有
}

// MemoryStatsQuery 内存统计查询参数
type MemoryStatsQuery struct {
	StartTime   int64    `json:"start_time"`            // 开始时间 Unix 毫秒
	EndTime     int64    `json:"end_time"`              // 结束时间 Unix 毫秒
	Categories  []string `json:"categories,omitempty"`  // 类别列表（空表示全部）
	Aggregation string   `json:"aggregation,omitempty"` // 聚合粒度: none/minute/hour
}

// MemoryStatsResponse 内存统计响应
type MemoryStatsResponse struct {
	// Stats 按时间排序的统计数据
	Stats []MemoryStat `json:"stats"`
	// GroupedStats 按类别分组的统计数据（用于曲线图）
	GroupedStats map[string][]MemoryStat `json:"grouped_stats,omitempty"`
}

// 预定义的内存统计类别
const (
	// MemoryCategorySelf 主进程（Go 运行时）
	MemoryCategorySelf = "self"
	// MemoryCategoryFFmpeg FFmpeg 子进程
	MemoryCategoryFFmpeg = "ffmpeg"
	// MemoryCategoryBTools bililive-tools 子进程
	MemoryCategoryBTools = "bililive-tools"
	// MemoryCategoryKlive klive 子进程
	MemoryCategoryKlive = "klive"
	// MemoryCategoryRecorder BililiveRecorder 子进程
	MemoryCategoryRecorder = "bililive-recorder"
	// MemoryCategoryContainer 容器内存
	MemoryCategoryContainer = "container"
	// MemoryCategoryTotal 总内存（self + 所有子进程）
	MemoryCategoryTotal = "total"
)
