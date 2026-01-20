package iostats

// StatType 统计类型
type StatType string

const (
	// StatTypeNetworkDownload 网络下载速度
	StatTypeNetworkDownload StatType = "network_download"
	// StatTypeDiskRecordWrite 录制时的磁盘写入速度
	StatTypeDiskRecordWrite StatType = "disk_record_write"
	// StatTypeDiskFixRead FLV修复时的读取速度
	StatTypeDiskFixRead StatType = "disk_fix_read"
	// StatTypeDiskFixWrite FLV修复时的写入速度
	StatTypeDiskFixWrite StatType = "disk_fix_write"
	// StatTypeDiskConvertRead MP4转换时的读取速度
	StatTypeDiskConvertRead StatType = "disk_convert_read"
	// StatTypeDiskConvertWrite MP4转换时的写入速度
	StatTypeDiskConvertWrite StatType = "disk_convert_write"
	// StatTypeDiskSystemIO 系统级磁盘 I/O 统计
	StatTypeDiskSystemIO StatType = "disk_system_io"
)

// IOStat IO 统计数据点
type IOStat struct {
	ID         int64    `json:"id"`
	Timestamp  int64    `json:"timestamp"`             // Unix 毫秒
	StatType   StatType `json:"stat_type"`             // 统计类型
	LiveID     string   `json:"live_id,omitempty"`     // 直播间 ID（空表示全局）
	Platform   string   `json:"platform,omitempty"`    // 平台名称
	Speed      int64    `json:"speed"`                 // 速度 bytes/s
	TotalBytes int64    `json:"total_bytes,omitempty"` // 累计字节数
}

// RequestStatus 请求状态记录
type RequestStatus struct {
	ID           int64  `json:"id"`
	Timestamp    int64  `json:"timestamp"`               // Unix 毫秒
	LiveID       string `json:"live_id"`                 // 直播间 ID
	Platform     string `json:"platform"`                // 平台名称
	Success      bool   `json:"success"`                 // 是否成功
	ErrorMessage string `json:"error_message,omitempty"` // 失败时的错误信息
}

// RequestStatusSegment 请求状态时间段（用于横条图展示）
type RequestStatusSegment struct {
	StartTime int64 `json:"start_time"` // 开始时间 Unix 毫秒
	EndTime   int64 `json:"end_time"`   // 结束时间 Unix 毫秒
	Success   bool  `json:"success"`    // 是否成功
	Count     int   `json:"count"`      // 该时段内的请求次数
}

// ViewMode 查看模式
type ViewMode string

const (
	// ViewModeByLive 按直播间查看
	ViewModeByLive ViewMode = "by_live"
	// ViewModeByPlatform 按平台查看
	ViewModeByPlatform ViewMode = "by_platform"
	// ViewModeGlobal 全局查看
	ViewModeGlobal ViewMode = "global"
)

// IOStatsQuery IO 统计查询参数
type IOStatsQuery struct {
	StartTime   int64      `json:"start_time"`            // 开始时间 Unix 毫秒
	EndTime     int64      `json:"end_time"`              // 结束时间 Unix 毫秒
	StatTypes   []StatType `json:"stat_types,omitempty"`  // 统计类型列表
	LiveID      string     `json:"live_id,omitempty"`     // 直播间 ID
	Platform    string     `json:"platform,omitempty"`    // 平台名称
	Aggregation string     `json:"aggregation,omitempty"` // 聚合粒度: none/minute/hour
}

// RequestStatusQuery 请求状态查询参数
type RequestStatusQuery struct {
	StartTime int64    `json:"start_time"`         // 开始时间 Unix 毫秒
	EndTime   int64    `json:"end_time"`           // 结束时间 Unix 毫秒
	ViewMode  ViewMode `json:"view_mode"`          // 查看模式
	LiveID    string   `json:"live_id,omitempty"`  // 直播间 ID（ViewModeByLive 时使用）
	Platform  string   `json:"platform,omitempty"` // 平台名称（ViewModeByPlatform 时使用）
}

// FiltersResponse 筛选器响应
type FiltersResponse struct {
	LiveIDs   []string `json:"live_ids"`  // 可选的直播间 ID 列表
	Platforms []string `json:"platforms"` // 可选的平台列表
}

// IOStatsResponse IO 统计响应
type IOStatsResponse struct {
	Stats []IOStat `json:"stats"`
}

// RequestStatusResponse 请求状态响应
type RequestStatusResponse struct {
	// Segments 按时间排序的状态段列表
	Segments []RequestStatusSegment `json:"segments"`
	// GroupedSegments 分组的状态段（用于 by_live 和 by_platform 模式）
	// key 为 live_id 或 platform
	GroupedSegments map[string][]RequestStatusSegment `json:"grouped_segments,omitempty"`
}

// DiskIOStat 系统级磁盘 I/O 统计
type DiskIOStat struct {
	ID              int64   `json:"id"`
	Timestamp       int64   `json:"timestamp"`            // Unix 毫秒
	DeviceName      string  `json:"device_name"`          // 磁盘设备名
	ReadCount       uint64  `json:"read_count"`           // 读操作次数（采样周期内）
	WriteCount      uint64  `json:"write_count"`          // 写操作次数（采样周期内）
	ReadBytes       uint64  `json:"read_bytes"`           // 读取字节数（采样周期内）
	WriteBytes      uint64  `json:"write_bytes"`          // 写入字节数（采样周期内）
	ReadTimeMs      uint64  `json:"read_time_ms"`         // 读取耗时（毫秒，采样周期内）
	WriteTimeMs     uint64  `json:"write_time_ms"`        // 写入耗时（毫秒，采样周期内）
	AvgReadLatency  float64 `json:"avg_read_latency_ms"`  // 平均读延迟（毫秒/次）
	AvgWriteLatency float64 `json:"avg_write_latency_ms"` // 平均写延迟（毫秒/次）
	ReadSpeed       int64   `json:"read_speed"`           // 读取速度 bytes/s
	WriteSpeed      int64   `json:"write_speed"`          // 写入速度 bytes/s
}

// DiskIOQuery 磁盘 I/O 查询参数
type DiskIOQuery struct {
	StartTime  int64  `json:"start_time"`            // 开始时间 Unix 毫秒
	EndTime    int64  `json:"end_time"`              // 结束时间 Unix 毫秒
	DeviceName string `json:"device_name,omitempty"` // 设备名称（可选）
}

// Config IO 统计配置
type Config struct {
	Enabled         bool `yaml:"enabled" json:"enabled"`                   // 是否启用
	CollectInterval int  `yaml:"collect_interval" json:"collect_interval"` // 采集间隔（秒），默认 5
	RetentionDays   int  `yaml:"retention_days" json:"retention_days"`     // 数据保留天数，默认 7
}

// DefaultConfig 返回默认配置
func DefaultConfig() Config {
	return Config{
		Enabled:         true,
		CollectInterval: 5,
		RetentionDays:   7,
	}
}
