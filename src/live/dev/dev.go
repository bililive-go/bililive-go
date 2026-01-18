//go:build dev

package dev

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/bililive-go/bililive-go/src/live"
	"github.com/bililive-go/bililive-go/src/live/internal"
)

const (
	domain = "localhost"
	cnName = "开发测试"
)

func init() {
	// 注册多个端口以便测试
	live.Register("localhost:8080", new(builder))
	live.Register("localhost:8888", new(builder))
	live.Register("127.0.0.1:8080", new(builder))
	live.Register("127.0.0.1:8888", new(builder))
	live.Register("127.0.0.1:8081", new(builder))
}

type builder struct{}

func (b *builder) Build(url *url.URL) (live.Live, error) {
	return &Live{
		BaseLive: internal.NewBaseLive(url),
	}, nil
}

// Live 开发测试用的 Live 实现
// 用于与 osrp-stream-tester 配合进行自动化测试
type Live struct {
	internal.BaseLive
}

func (l *Live) GetPlatformCNName() string {
	return cnName
}

// GetInfo 获取直播间信息
// 从测试服务器的 API 获取当前状态
func (l *Live) GetInfo() (*live.Info, error) {
	info := &live.Info{
		Live:   l,
		Status: true, // 测试服务器始终"在线"
	}

	// 从URL路径解析房间名
	roomName := strings.TrimPrefix(l.Url.Path, "/live/")
	roomName = strings.TrimPrefix(roomName, "/files/")
	roomName = strings.TrimSuffix(roomName, ".flv")
	roomName = strings.TrimSuffix(roomName, ".m3u8")

	info.RoomName = roomName
	info.HostName = "测试主播"

	// 尝试从测试服务器获取更多信息
	apiURL := fmt.Sprintf("%s://%s/api/streams/%s", l.Url.Scheme, l.Url.Host, roomName)
	if l.Url.Scheme == "" {
		apiURL = fmt.Sprintf("http://%s/api/streams/%s", l.Url.Host, roomName)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "GET", apiURL, nil)
	if err == nil {
		resp, err := http.DefaultClient.Do(req)
		if err == nil {
			defer resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				var streamInfo StreamAPIResponse
				if json.NewDecoder(resp.Body).Decode(&streamInfo) == nil {
					if streamInfo.Title != "" {
						info.RoomName = streamInfo.Title
					}
					if streamInfo.Streamer != "" {
						info.HostName = streamInfo.Streamer
					}
					info.Status = streamInfo.Live
				}
			}
		}
	}

	return info, nil
}

// StreamAPIResponse osrp-stream-tester API 响应
type StreamAPIResponse struct {
	ID       string `json:"id"`
	Title    string `json:"title"`
	Streamer string `json:"streamer"`
	Live     bool   `json:"live"`
	Format   string `json:"format"`
	Codec    string `json:"codec"`
	Duration string `json:"duration"`
}

// GetStreamInfos 获取所有可用流
// 支持多格式、多分辨率测试
func (l *Live) GetStreamInfos() ([]*live.StreamUrlInfo, error) {
	infos := make([]*live.StreamUrlInfo, 0)

	// 解析路径
	path := l.Url.Path

	// 判断是直接请求流还是请求房间
	if strings.HasSuffix(path, ".flv") || strings.HasSuffix(path, ".m3u8") {
		// 直接是流URL，返回单个流
		format := "flv"
		if strings.HasSuffix(path, ".m3u8") {
			format = "hls"
		}

		codec := "h264"
		if codecParam := l.Url.Query().Get("codec"); codecParam != "" {
			codec = codecParam
		}

		quality := "1080p"
		if qParam := l.Url.Query().Get("quality"); qParam != "" {
			quality = qParam
		}

		width, height := parseQuality(quality)

		info := &live.StreamUrlInfo{
			Url:         l.Url,
			Name:        fmt.Sprintf("%s - %s", quality, format),
			Description: fmt.Sprintf("测试流 %s %s (%s)", quality, format, codec),
			Quality:     quality,
			Format:      format,
			Codec:       normalizeCodec(codec),
			Width:       width,
			Height:      height,
			HeadersForDownloader: map[string]string{
				"User-Agent": "bililive-go-test",
			},
		}

		infos = append(infos, info)
	} else {
		// 尝试从API获取所有可用流
		roomName := strings.TrimPrefix(path, "/live/")
		roomName = strings.TrimPrefix(roomName, "/")

		apiStreams, err := l.fetchAvailableStreams(roomName)
		if err == nil && len(apiStreams) > 0 {
			infos = apiStreams
		} else {
			// Fallback: 生成标准测试流
			infos = l.generateTestStreams(roomName)
		}
	}

	return infos, nil
}

// fetchAvailableStreams 从测试服务器获取可用流列表
func (l *Live) fetchAvailableStreams(roomName string) ([]*live.StreamUrlInfo, error) {
	apiURL := fmt.Sprintf("http://%s/api/streams/%s/available", l.Url.Host, roomName)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "GET", apiURL, nil)
	if err != nil {
		return nil, err
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("API returned %d", resp.StatusCode)
	}

	var availableStreams []struct {
		URL     string `json:"url"`
		Format  string `json:"format"`
		Quality string `json:"quality"`
		Codec   string `json:"codec"`
		Width   int    `json:"width"`
		Height  int    `json:"height"`
		Bitrate int    `json:"bitrate"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&availableStreams); err != nil {
		return nil, err
	}

	infos := make([]*live.StreamUrlInfo, 0, len(availableStreams))
	for _, s := range availableStreams {
		u, err := url.Parse(s.URL)
		if err != nil {
			continue
		}

		infos = append(infos, &live.StreamUrlInfo{
			Url:         u,
			Name:        fmt.Sprintf("%s - %s", s.Quality, s.Format),
			Description: fmt.Sprintf("%s %s (%s)", s.Quality, s.Format, s.Codec),
			Quality:     s.Quality,
			Format:      s.Format,
			Codec:       s.Codec,
			Width:       s.Width,
			Height:      s.Height,
			Bitrate:     s.Bitrate,
			HeadersForDownloader: map[string]string{
				"User-Agent": "bililive-go-test",
			},
		})
	}

	return infos, nil
}

// generateTestStreams 生成标准测试流列表
// 用于当API不可用时提供默认的多流测试
func (l *Live) generateTestStreams(roomName string) []*live.StreamUrlInfo {
	baseURL := fmt.Sprintf("http://%s/live/", l.Url.Host)

	testConfigs := []struct {
		quality string
		format  string
		codec   string
		width   int
		height  int
		bitrate int
	}{
		// FLV 流
		{"1080p", "flv", "h264", 1920, 1080, 6000},
		{"1080p", "flv", "h265", 1920, 1080, 4000},
		{"720p", "flv", "h264", 1280, 720, 3000},
		{"480p", "flv", "h264", 854, 480, 1500},

		// HLS 流
		{"1080p", "hls", "h264", 1920, 1080, 6000},
		{"720p", "hls", "h264", 1280, 720, 3000},

		// 特殊测试流
		{"1080p", "flv", "hevc-annexb", 1920, 1080, 4000}, // Annex B HEVC
	}

	infos := make([]*live.StreamUrlInfo, 0, len(testConfigs))

	for _, cfg := range testConfigs {
		ext := ".flv"
		if cfg.format == "hls" {
			ext = ".m3u8"
		}

		streamURL := fmt.Sprintf("%s%s%s?codec=%s&quality=%s",
			baseURL, roomName, ext, cfg.codec, cfg.quality)

		u, _ := url.Parse(streamURL)

		infos = append(infos, &live.StreamUrlInfo{
			Url:         u,
			Name:        fmt.Sprintf("%s - %s", cfg.quality, cfg.format),
			Description: fmt.Sprintf("测试流 %s %s (%s, %dkbps)", cfg.quality, cfg.format, cfg.codec, cfg.bitrate),
			Quality:     cfg.quality,
			Format:      cfg.format,
			Codec:       normalizeCodec(cfg.codec),
			Width:       cfg.width,
			Height:      cfg.height,
			Bitrate:     cfg.bitrate,
			HeadersForDownloader: map[string]string{
				"User-Agent": "bililive-go-test",
			},
		})
	}

	return infos
}

// parseQuality 解析清晰度获取分辨率
func parseQuality(quality string) (width, height int) {
	qualityMap := map[string][2]int{
		"4k":    {3840, 2160},
		"1080p": {1920, 1080},
		"720p":  {1280, 720},
		"480p":  {854, 480},
		"360p":  {640, 360},
		"原画":    {1920, 1080},
	}

	if res, ok := qualityMap[strings.ToLower(quality)]; ok {
		return res[0], res[1]
	}

	return 1920, 1080 // 默认
}

// normalizeCodec 规范化编码名称
func normalizeCodec(codec string) string {
	codecMap := map[string]string{
		"avc":         "h264",
		"h264":        "h264",
		"hevc":        "h265",
		"h265":        "h265",
		"hevc-annexb": "h265",
	}

	if normalized, ok := codecMap[strings.ToLower(codec)]; ok {
		return normalized
	}

	return codec
}
