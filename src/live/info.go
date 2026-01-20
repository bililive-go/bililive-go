package live

import (
	"encoding/json"

	"github.com/bililive-go/bililive-go/src/types"
)

// AvailableStreamInfo 可用流信息（用于API展示）
type AvailableStreamInfo struct {
	Format      string  `json:"format"`                 // 格式: flv, hls, rtmp
	Quality     string  `json:"quality"`                // 清晰度标识: 1080p, 720p, 原画
	QualityName string  `json:"quality_name,omitempty"` // 清晰度汉字名称: 蓝光, 超清, 高清
	Description string  `json:"description,omitempty"`  // 平台提供的清晰度名称
	Width       int     `json:"width,omitempty"`        // 宽度
	Height      int     `json:"height,omitempty"`       // 高度
	Bitrate     int     `json:"bitrate,omitempty"`      // 码率 (kbps)
	FrameRate   float64 `json:"frame_rate,omitempty"`   // 帧率
	Codec       string  `json:"codec,omitempty"`        // 视频编码: h264, h265
	AudioCodec  string  `json:"audio_codec,omitempty"`  // 音频编码

	// 用于前端流选择的属性组合
	AttributesForStreamSelect map[string]string `json:"attributes_for_stream_select,omitempty"`
}

// QualityNameMap 分辨率到汉字名称的映射
var QualityNameMap = map[string]string{
	"原画":       "原画",
	"蓝光":       "蓝光",
	"超清":       "超清",
	"高清":       "高清",
	"流畅":       "流畅",
	"4K":       "4K",
	"1080p":    "蓝光",
	"1080":     "蓝光",
	"720p":     "超清",
	"720":      "超清",
	"480p":     "高清",
	"480":      "高清",
	"360p":     "流畅",
	"360":      "流畅",
	"original": "原画",
	"OD":       "原画",
}

// GetQualityName 获取清晰度的汉字名称
func GetQualityName(quality string) string {
	if name, ok := QualityNameMap[quality]; ok {
		return name
	}
	return quality
}

type Info struct {
	Live                 Live
	HostName, RoomName   string
	Status               bool // means isLiving, maybe better to rename it
	Listening, Recording bool
	Initializing         bool
	CustomLiveId         string
	AudioOnly            bool
	// 可用流列表（最近一次获取的）
	AvailableStreams []*AvailableStreamInfo
	// 可用流更新时间
	AvailableStreamsUpdatedAt int64
}

type InfoCookie struct {
	Platform_cn_name string
	Host             string
	Cookie           string
}

func (i *Info) MarshalJSON() ([]byte, error) {
	t := struct {
		Id                        types.LiveID           `json:"id"`
		LiveUrl                   string                 `json:"live_url"`
		PlatformCNName            string                 `json:"platform_cn_name"`
		HostName                  string                 `json:"host_name"`
		RoomName                  string                 `json:"room_name"`
		Status                    bool                   `json:"status"`
		Listening                 bool                   `json:"listening"`
		Recording                 bool                   `json:"recording"`
		Initializing              bool                   `json:"initializing"`
		LastStartTime             string                 `json:"last_start_time,omitempty"`
		LastStartTimeUnix         int64                  `json:"last_start_time_unix,omitempty"`
		AudioOnly                 bool                   `json:"audio_only"`
		NickName                  string                 `json:"nick_name"`
		AvailableStreams          []*AvailableStreamInfo `json:"available_streams,omitempty"`
		AvailableStreamsUpdatedAt int64                  `json:"available_streams_updated_at,omitempty"`
	}{
		Id:                        i.Live.GetLiveId(),
		LiveUrl:                   i.Live.GetRawUrl(),
		PlatformCNName:            i.Live.GetPlatformCNName(),
		HostName:                  i.HostName,
		RoomName:                  i.RoomName,
		Status:                    i.Status,
		Listening:                 i.Listening,
		Recording:                 i.Recording,
		Initializing:              i.Initializing,
		AudioOnly:                 i.AudioOnly,
		NickName:                  i.Live.GetOptions().NickName,
		AvailableStreams:          i.AvailableStreams,
		AvailableStreamsUpdatedAt: i.AvailableStreamsUpdatedAt,
	}
	if !i.Live.GetLastStartTime().IsZero() {
		t.LastStartTime = i.Live.GetLastStartTime().Format("2006-01-02 15:04:05")
		t.LastStartTimeUnix = i.Live.GetLastStartTime().Unix()
	}
	return json.Marshal(t)
}
