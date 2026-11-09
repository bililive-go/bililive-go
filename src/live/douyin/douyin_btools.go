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
// 本地接口还会等待抖音上游及端点回退；15 秒是整次调用的预算。
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
		return nil, readBToolsError(resp)
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
	Stream string `json:"stream"`
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

func (l *btoolsLive) fetchStreamInfo() (streamInfo streamInfoResp, err error) {
	if l.roomId == "" {
		err = l.updateChannelInfo()
		if err != nil {
			return
		}
	}

	endpoint := fmt.Sprintf("http://127.0.0.1:%d/bgo/stream-info?platform=douyin&roomId=%s", btoolsConsts.port, url.QueryEscape(l.roomId))
	body, err := doBToolsRequest(endpoint)
	if err != nil {
		return streamInfo, err
	}
	if err = json.Unmarshal(body, &streamInfo); err != nil {
		return streamInfo, err
	}
	return streamInfo, nil
}

func (l *btoolsLive) GetInfo() (info *live.Info, err error) {
	ret := &live.Info{
		Live:     l.Live,
		HostName: l.hostName,
		RoomName: l.roomName,
		Status:   false,
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
	streamInfo, err = l.fetchStreamInfo()
	if err != nil {
		return
	}
	u, parseErr := url.Parse(streamInfo.Stream)
	if parseErr != nil {
		err = parseErr
		return
	}

	return []*live.StreamUrlInfo{
		{
			Url: u,
		},
	}, nil
}
