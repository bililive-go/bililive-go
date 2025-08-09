package qq

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
	domain    = "egame.qq.com"
	cnName    = "企鹅电竞"
	mobileUrl = "https://m.egame.qq.com/live"
)

func init() {
	live.Register(domain, new(builder))
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

func (l *Live) GetInfo() (info *live.Info, err error) {
	paths := strings.Split(l.Url.Path, "/")
	if len(paths) < 2 {
		return nil, live.ErrRoomUrlIncorrect
	}
	anchorID := paths[1]
	resp, err := l.RequestSession.Get(mobileUrl, live.CommonUserAgent, requests.Query("anchorid", anchorID))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, live.ErrRoomNotExist
	}
	body, err := resp.Text()
	if err != nil {
		return nil, err
	}
	roomName := utils.ParseString(utils.Match1(`"title":"([^"\{\}]*)"`, body), utils.ParseUnicode)
	hostName := utils.ParseString(utils.Match1(`"nickName":"([^"]+)"`, body), utils.ParseUnicode)
	isLive := utils.Match1(`"isLive":(\d+)`, body)
	if roomName == "" || hostName == "" || isLive == "" {
		return nil, live.ErrInternalError
	}
	info = &live.Info{
		Live:     l,
		RoomName: roomName,
		HostName: hostName,
		Status:   isLive == "1",
	}
	return info, nil
}

func (l *Live) GetStreamUrls() (us []*url.URL, err error) {
	resp, err := l.RequestSession.Get(l.Url.String(), live.CommonUserAgent)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, live.ErrRoomNotExist
	}
	body, err := resp.Text()
	if err != nil {
		return nil, err
	}
	result := utils.Match1(`"urlArray":(\[[^\]]+\])`, body)
	if result == "" {
		return nil, live.ErrInternalError
	}
	return utils.GenUrls(gjson.Get(result, "#[bitrate==0].playUrl").String())
}

func (l *Live) GetPlatformCNName() string {
	return cnName
}
