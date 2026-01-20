package livestate

import "time"

// LiveRoom 直播间状态记录
type LiveRoom struct {
	ID            int64     `json:"id"`
	LiveID        string    `json:"live_id"`         // 直播间ID
	URL           string    `json:"url"`             // 直播间URL
	Platform      string    `json:"platform"`        // 平台标识
	HostName      string    `json:"host_name"`       // 主播名称
	RoomName      string    `json:"room_name"`       // 直播间名称
	LastStartTime time.Time `json:"last_start_time"` // 上次开播时间
	LastEndTime   time.Time `json:"last_end_time"`   // 上次关播时间
	IsRecording   bool      `json:"is_recording"`    // 上次关闭时是否正在录制
	LastHeartbeat time.Time `json:"last_heartbeat"`  // 录制心跳时间戳
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

// LiveSession 直播会话记录（每次开播到下播为一个会话）
type LiveSession struct {
	ID        int64     `json:"id"`
	LiveID    string    `json:"live_id"`
	HostName  string    `json:"host_name"`  // 主播名称（会话开始时）
	RoomName  string    `json:"room_name"`  // 直播间名称（会话开始时）
	StartTime time.Time `json:"start_time"` // 开播时间
	EndTime   time.Time `json:"end_time"`   // 下播时间，零值表示仍在直播或崩溃未记录
	EndReason string    `json:"end_reason"` // 结束原因
	CreatedAt time.Time `json:"created_at"`
}

// NameChange 名称变更记录
type NameChange struct {
	ID        int64     `json:"id"`
	LiveID    string    `json:"live_id"`
	NameType  string    `json:"name_type"`  // host_name 或 room_name
	OldValue  string    `json:"old_value"`  // 旧值
	NewValue  string    `json:"new_value"`  // 新值
	ChangedAt time.Time `json:"changed_at"` // 变更时间
}

// 名称类型常量
const (
	NameTypeHost = "host_name"
	NameTypeRoom = "room_name"
)

// 会话结束原因常量
const (
	EndReasonNormal   = "normal"    // 主播正常下播
	EndReasonUserStop = "user_stop" // 用户主动停止监控
	EndReasonCrash    = "crash"     // 程序崩溃
	EndReasonError    = "error"     // 录制异常中断
	EndReasonUnknown  = "unknown"   // 未知原因
)

// AvailableStream 可用流信息记录（存储在数据库中）
type AvailableStream struct {
	ID          int64             `json:"id"`
	LiveID      string            `json:"live_id"`      // 直播间ID
	StreamIndex int               `json:"stream_index"` // 流序号
	Quality     string            `json:"quality"`      // 清晰度标识（唯一固定字段）
	Attributes  map[string]string `json:"attributes"`   // 流属性键值对（如 "format": "flv", "codec": "h264"）
	UpdatedAt   time.Time         `json:"updated_at"`   // 更新时间
}
