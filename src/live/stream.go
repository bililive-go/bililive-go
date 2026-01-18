package live

// StreamInfo 直播流信息
type StreamInfo struct {
	Quality     string  `json:"quality"`     // 分辨率标识: "1080p", "720p", "原画"
	Width       int     `json:"width"`       // 宽度: 1920, 1280
	Height      int     `json:"height"`      // 高度: 1080, 720
	Format      string  `json:"format"`      // 格式: "flv", "hls", "rtmp"
	URL         string  `json:"url"`         // 流地址
	Bitrate     int     `json:"bitrate"`     // 码率 (kbps)
	FrameRate   float64 `json:"frame_rate"`  // 帧率
	Codec       string  `json:"codec"`       // 视频编码: "h264", "h265"
	AudioCodec  string  `json:"audio_codec"` // 音频编码: "aac"
	Description string  `json:"description"` // 描述（平台提供的清晰度名称）
}

// StreamMetadata 流元数据（从平台获取）
type StreamMetadata struct {
	RoomID      string       `json:"room_id"`
	Title       string       `json:"title"`
	Streamer    string       `json:"streamer"`
	Available   []StreamInfo `json:"available_streams"` // 所有可用流
	Recommended *StreamInfo  `json:"recommended"`       // 平台推荐流（可选）
}

// StreamPreference 用户流偏好设置
type StreamPreference struct {
	Formats     []string `json:"formats"`      // 格式优先级: ["flv", "hls"]
	Qualities   []string `json:"qualities"`    // 分辨率优先级: ["1080p", "720p", "原画"]
	MaxBitrate  int      `json:"max_bitrate"`  // 最大码率限制 (kbps), 0表示不限制
	MinBitrate  int      `json:"min_bitrate"`  // 最小码率限制 (kbps), 0表示不限制
	AllowH265   bool     `json:"allow_h265"`   // 是否允许H.265编码
	Prefer60FPS bool     `json:"prefer_60fps"` // 是否优先60fps
}

// DefaultStreamPreference 默认流偏好
var DefaultStreamPreference = StreamPreference{
	Formats:     []string{"flv", "hls"},
	Qualities:   []string{"1080p", "720p", "480p"},
	MaxBitrate:  0, // 不限制
	MinBitrate:  0,
	AllowH265:   true,
	Prefer60FPS: false,
}

// ActiveStreamInfo 当前活跃流信息（用于运行时展示）
type ActiveStreamInfo struct {
	URL           string  `json:"url"`                   // 流URL
	Format        string  `json:"format"`                // 格式
	Quality       string  `json:"quality"`               // 分辨率标识
	Width         int     `json:"width,omitempty"`       // 宽度
	Height        int     `json:"height,omitempty"`      // 高度
	Bitrate       int     `json:"bitrate,omitempty"`     // 码率
	FrameRate     float64 `json:"frame_rate,omitempty"`  // 帧率
	Codec         string  `json:"codec,omitempty"`       // 编码
	AudioCodec    string  `json:"audio_codec,omitempty"` // 音频编码
	InfoAvailable bool    `json:"info_available"`        // 信息是否已成功获取
	ErrorMessage  string  `json:"error,omitempty"`       // 获取失败原因
}

// NewActiveStreamInfo 从StreamInfo创建ActiveStreamInfo
func NewActiveStreamInfo(stream *StreamInfo) *ActiveStreamInfo {
	if stream == nil {
		return nil
	}

	return &ActiveStreamInfo{
		URL:           stream.URL,
		Format:        stream.Format,
		Quality:       stream.Quality,
		Width:         stream.Width,
		Height:        stream.Height,
		Bitrate:       stream.Bitrate,
		FrameRate:     stream.FrameRate,
		Codec:         stream.Codec,
		AudioCodec:    stream.AudioCodec,
		InfoAvailable: true,
	}
}

// LiveWithStreams 支持多流的直播平台接口扩展
type LiveWithStreams interface {
	Live // 继承现有Live接口

	// GetStreams 获取所有可用的直播流
	// 返回流元数据，包含所有可用的流信息
	GetStreams(roomID string) (*StreamMetadata, error)
}

// 质量标识规范化映射
var QualityNormalization = map[string]string{
	// 通用中文质量标识
	"原画": "original",
	"4K": "4k",
	"蓝光": "1080p",
	"超清": "720p",
	"高清": "480p",
	"流畅": "360p",

	// 斗鱼特有
	"OD": "original",

	// 分辨率标准化
	"1920x1080": "1080p",
	"1280x720":  "720p",
	"854x480":   "480p",
	"640x360":   "360p",
}

// NormalizeQuality 规范化质量标识
func NormalizeQuality(quality string) string {
	if normalized, ok := QualityNormalization[quality]; ok {
		return normalized
	}
	return quality
}

// ParseResolution 从质量标识解析分辨率
func ParseResolution(quality string) (width, height int) {
	resolutions := map[string][2]int{
		"4k":       {3840, 2160},
		"original": {1920, 1080}, // 默认原画为1080p
		"1080p":    {1920, 1080},
		"720p":     {1280, 720},
		"480p":     {854, 480},
		"360p":     {640, 360},
	}

	normalized := NormalizeQuality(quality)
	if res, ok := resolutions[normalized]; ok {
		return res[0], res[1]
	}

	return 0, 0
}
