package lang

import (
	"net/http"
	"net/url"
	"strings"

	"github.com/hr3lxphr6j/requests"
	"github.com/tidwall/gjson"

	"github.com/bililive-go/bililive-go/src/live"
	"github.com/bililive-go/bililive-go/src/live/internal"
	"github.com/bililive-go/bililive-go/src/pkg/utils"
)

const (
	liveDomain = "www.lang.live"
	cnName     = "浪live"

	liveInfoAPIUrl = "https://api.lang.live/langweb/v1/room/liveinfo"
)

func init() {
	live.Register(liveDomain, new(builder))
}

type builder struct{}

func (b *builder) Build(url *url.URL) (live.Live, error) {
	return &Live{
		BaseLive: internal.NewBaseLive(url),
	}, nil
}

type Live struct {
	internal.BaseLive
}

func (l *Live) getData() (*gjson.Result, error) {
	paths := strings.Split(l.Url.Path, "/")
	if len(paths) < 3 {
		return nil, live.ErrRoomUrlIncorrect
	}
	roomID := paths[2]
	resp, err := requests.Get(liveInfoAPIUrl, live.CommonUserAgent, requests.Query("room_id", roomID))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, live.ErrRoomNotExist
	}
	body, err := resp.Bytes()
	if err != nil || gjson.GetBytes(body, "ret_code").Int() != 0 {
		return nil, live.ErrRoomNotExist
	}
	data := gjson.GetBytes(body, "data")
	return &data, nil
}

func (l *Live) GetInfo() (info *live.Info, err error) {
	data, err := l.getData()
	if err != nil {
		return nil, err
	}

	var (
		hostNamePath = "live_info.nickname"
		roomNamePath = "live_info.pretty_id"
		statusPath   = "live_info.live_status"
	)

	return &live.Info{
		Live:     l,
		HostName: data.Get(hostNamePath).String(),
		RoomName: data.Get(roomNamePath).String(),
		Status:   data.Get(statusPath).Int() == 1,
	}, nil
}

func (l *Live) GetStreamUrls() (us []*url.URL, err error) {
	data, err := l.getData()
	if err != nil {
		return nil, err
	}
	urls := make([]string, 0)
	if u := data.Get("live_info.liveurl").String(); u != "" {
		urls = append(urls, u)
	}
	if u := data.Get("live_info.liveurl_hls").String(); u != "" {
		urls = append(urls, u)
	}
	return utils.GenUrls(urls...)
}

func (l *Live) GetPlatformCNName() string {
	return cnName
}
