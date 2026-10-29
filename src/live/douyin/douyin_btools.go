package douyin

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/bililive-go/bililive-go/src/live"
	"github.com/bililive-go/bililive-go/src/tools"
)

var btoolsConsts = struct {
	port      int
	authToken string
}{
	port:      tools.BToolsPort,
	authToken: tools.BToolsAuthToken,
}

// btoolsClient 访问本地 bililive-tools 服务的专用客户端。
//
// 不能用 http.DefaultClient：它没有超时，本地服务一旦卡住（而不是报错），
// 调用方会永久阻塞，直播间轮询 goroutine 会不断堆积。
// 这里的请求全部打到 127.0.0.1，正常应在毫秒级返回，给 15 秒已经非常宽松。
var btoolsClient = &http.Client{
	Timeout: 15 * time.Second,
}

// doBToolsRequest 向本地 bililive-tools 服务发起一次带鉴权的 GET 请求。
// 返回的 body 已经读取完毕并关闭，调用方直接解析即可。
func doBToolsRequest(endpoint string) ([]byte, error) {
	req, err := http.NewRequest(http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", btoolsConsts.authToken)

	resp, err := btoolsClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		// 读掉 body 才能让连接回到连接池复用，否则每次失败都会新建一条 TCP 连接，
		// 大量直播间同时失败时会迅速耗尽本地端口
		_, _ = io.Copy(io.Discard, resp.Body)
		return nil, fmt.Errorf("请求失败: %s", resp.Status)
	}

	return io.ReadAll(resp.Body)
}

type ChannelInfo struct {
	Id     string `json:"id"`
	Title  string `json:"title"`
	Owner  string `json:"owner"`
	Avatar string `json:"avatar"`
	Uid    string `json:"uid"`
}

type liveInfoResp struct {
	Title  string `json:"title"`
	Owner  string `json:"owner"`
	Living bool   `json:"living"`
}

type streamInfoResp struct {
	Stream        string `json:"stream"`
	VideoFallback string `json:"-"`
	// bgo.1/bgo.2 的 dev 响应保留平台返回的所有线路，供纯音频模式选择。
	Dev struct {
		Sources []struct {
			Streams []struct {
				Quality string `json:"quality"`
				FLV     string `json:"flv"`
				HLS     string `json:"hls"`
			} `json:"streams"`
		} `json:"sources"`
	} `json:"dev"`
}

func NewBtoolsLive(live *Live) btoolsLive {
	return btoolsLive{
		Live:     live,
		roomId:   "",
		hostName: "",
		roomName: "",
	}
}

type btoolsLive struct {
	*Live
	roomId   string
	hostName string
	roomName string
}

func (l *btoolsLive) updateChannelInfo() (err error) {
	var channelInfo ChannelInfo
	channelInfo, err = l.fetchChannelInfo()
	if err != nil {
		return
	}
	if channelInfo.Id == "" {
		err = fmt.Errorf("无法获取频道信息")
		return
	}
	l.hostName = channelInfo.Owner
	l.roomName = channelInfo.Title
	l.roomId = channelInfo.Id
	return
}

func (l *btoolsLive) fetchChannelInfo() (channelInfo ChannelInfo, err error) {
	endpoint := fmt.Sprintf("http://127.0.0.1:%d/bgo/channel-info?url=%s", btoolsConsts.port, url.QueryEscape(l.Url.String()))
	body, err := doBToolsRequest(endpoint)
	if err != nil {
		return
	}
	err = json.Unmarshal(body, &channelInfo)
	return
}

func (l *btoolsLive) fetchLiveInfo() (liveInfo liveInfoResp, err error) {
	if l.roomId == "" {
		err = l.updateChannelInfo()
		if err != nil {
			return
		}
	}

	endpoint := fmt.Sprintf("http://127.0.0.1:%d/bgo/live-info?platform=douyin&roomId=%s", btoolsConsts.port, url.QueryEscape(l.roomId))
	body, err := doBToolsRequest(endpoint)
	if err != nil {
		return liveInfo, err
	}
	if err = json.Unmarshal(body, &liveInfo); err != nil {
		return liveInfo, err
	}
	return liveInfo, nil
}

func (l *btoolsLive) fetchStreamInfo(audioOnly bool) (streamInfo streamInfoResp, err error) {
	if l.roomId == "" {
		err = l.updateChannelInfo()
		if err != nil {
			return
		}
	}

	endpoint := fmt.Sprintf("http://127.0.0.1:%d/bgo/stream-info?platform=douyin&roomId=%s", btoolsConsts.port, url.QueryEscape(l.roomId))
	if audioOnly {
		// 只取解析元数据，不下载默认的视频流，也不需要修改配套工具。
		endpoint += "&dev=1"
	}
	body, err := doBToolsRequest(endpoint)
	if err == nil {
		err = json.Unmarshal(body, &streamInfo)
	}
	if err != nil && audioOnly {
		// 兼容不支持 dev 响应的旧工具；重新走原来的视频取流接口。
		endpoint = fmt.Sprintf("http://127.0.0.1:%d/bgo/stream-info?platform=douyin&roomId=%s", btoolsConsts.port, url.QueryEscape(l.roomId))
		body, err = doBToolsRequest(endpoint)
		if err == nil {
			err = json.Unmarshal(body, &streamInfo)
		}
	}
	if err != nil {
		return streamInfo, err
	}
	if audioOnly {
		audioURL, err := streamInfo.audioStreamURL()
		if err == nil {
			streamInfo.VideoFallback = streamInfo.Stream
			streamInfo.Stream = audioURL
		} else if logger := l.GetLogger(); logger != nil {
			logger.Info("纯音频线路不可用，使用原视频流由 FFmpeg 提取音频")
		}
	}
	return streamInfo, nil
}

// audioStreamURL 仅接受平台返回并明确标记的纯音频线路。
// 不拼接签名参数；没有可靠的纯音频地址时由调用方回退到视频提取音频。
func (info *streamInfoResp) audioStreamURL() (string, error) {
	for _, source := range info.Dev.Sources {
		for _, stream := range source.Streams {
			if stream.Quality != "ao" {
				continue
			}
			for _, rawURL := range []string{stream.FLV, stream.HLS} {
				u, err := url.Parse(rawURL)
				if err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host != "" && u.Query().Get("only_audio") == "1" {
					return rawURL, nil
				}
			}
		}
	}
	return "", fmt.Errorf("抖音未返回可用的 ao 纯音频流（only_audio=1），将使用原视频流提取音频")
}

func (l *btoolsLive) GetInfo() (info *live.Info, err error) {
	ret := &live.Info{
		Live:      l.Live,
		HostName:  l.hostName,
		RoomName:  l.roomName,
		Status:    false,
		AudioOnly: live.RecordingAudioOnly(l, nil),
	}

	var liveInfo liveInfoResp
	liveInfo, err = l.fetchLiveInfo()
	if err != nil {
		return
	}
	ret.Status = liveInfo.Living
	ret.HostName = liveInfo.Owner
	ret.RoomName = liveInfo.Title

	return ret, nil
}

func (l *btoolsLive) GetStreamInfos() (us []*live.StreamUrlInfo, err error) {
	if l.roomId == "" {
		err = l.updateChannelInfo()
		if err != nil {
			return
		}
	}
	var streamInfo streamInfoResp
	audioOnly := live.RecordingAudioOnly(l, nil)
	streamInfo, err = l.fetchStreamInfo(audioOnly)
	if err != nil {
		return
	}
	u, parseErr := url.Parse(streamInfo.Stream)
	if parseErr != nil {
		err = parseErr
		return
	}

	var fallback *url.URL
	if streamInfo.VideoFallback != "" {
		candidate, err := url.Parse(streamInfo.VideoFallback)
		if err == nil && (candidate.Scheme == "https" || candidate.Scheme == "http") && candidate.Host != "" && candidate.Query().Get("only_audio") != "1" {
			fallback = candidate
		}
	}
	return []*live.StreamUrlInfo{
		{
			Url:                u,
			AudioFallbackURL:   fallback,
			RecordingAudioOnly: &audioOnly,
		},
	}, nil
}
