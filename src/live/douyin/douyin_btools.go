package douyin

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	urlpkg "net/url"

	"github.com/bililive-go/bililive-go/src/live"
)

var btoolsConsts = struct {
	port      int
	authToken string
}{
	port:      18110,
	authToken: "Basic YTph",
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
	// 使用自定义请求以便添加认证Header
	endpoint := fmt.Sprintf("http://127.0.0.1:%d/bgo/channel-info?url=%s", btoolsConsts.port, urlpkg.QueryEscape(l.Url.String()))
	req, reqErr := http.NewRequest(http.MethodGet, endpoint, nil)
	if reqErr != nil {
		err = reqErr
		return
	}
	req.Header.Set("Authorization", btoolsConsts.authToken)

	resp, doErr := http.DefaultClient.Do(req)
	if doErr != nil {
		err = doErr
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		err = fmt.Errorf("请求失败: %s", resp.Status)
		return
	}

	if err = json.NewDecoder(resp.Body).Decode(&channelInfo); err != nil {
		return
	}
	return
}

func (l *btoolsLive) fetchLiveInfo() (liveInfo liveInfoResp, err error) {
	if l.roomId == "" {
		err = l.updateChannelInfo()
		if err != nil {
			return
		}
	}

	endpoint := fmt.Sprintf("http://127.0.0.1:%d/bgo/live-info?platform=douyin&roomId=%s", btoolsConsts.port, urlpkg.QueryEscape(l.roomId))
	req, reqErr := http.NewRequest(http.MethodGet, endpoint, nil)
	if reqErr != nil {
		return liveInfo, reqErr
	}
	req.Header.Set("Authorization", btoolsConsts.authToken)

	resp, doErr := http.DefaultClient.Do(req)
	if doErr != nil {
		return liveInfo, doErr
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return liveInfo, fmt.Errorf("请求失败: %s", resp.Status)
	}
	if err = json.NewDecoder(resp.Body).Decode(&liveInfo); err != nil {
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

	endpoint := fmt.Sprintf("http://127.0.0.1:%d/bgo/stream-info?platform=douyin&roomId=%s", btoolsConsts.port, urlpkg.QueryEscape(l.roomId))
	req, reqErr := http.NewRequest(http.MethodGet, endpoint, nil)
	if reqErr != nil {
		return streamInfo, reqErr
	}
	req.Header.Set("Authorization", btoolsConsts.authToken)

	resp, doErr := http.DefaultClient.Do(req)
	if doErr != nil {
		return streamInfo, doErr
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return streamInfo, fmt.Errorf("请求失败: %s", resp.Status)
	}
	if err = json.NewDecoder(resp.Body).Decode(&streamInfo); err != nil {
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
