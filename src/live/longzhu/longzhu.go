package longzhu

import (
	"fmt"
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
	domain = "star.longzhu.com"
	cnName = "龙珠"

	mobileUrl  = "http://m.longzhu.com/"
	roomApiUrl = "http://roomapicdn.longzhu.com/room/roomstatus"
	liveApiUrl = "https://livestream.longzhu.com/live/getlivePlayurl"
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
	realId string
}

func (l *Live) parseRealId() error {
	paths := strings.Split(l.Url.Path, "/")
	if len(paths) < 2 {
		return live.ErrRoomUrlIncorrect
	}
	resp, err := requests.Get(fmt.Sprintf("%s%s", mobileUrl, paths[1]), live.CommonUserAgent)
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusOK {
		return live.ErrRoomNotExist
	}
	body, err := resp.Text()
	if err != nil {
		return err
	}
	if l.realId = utils.Match1(`var\s*roomId\s*=\s*(\d+);`, body); l.realId == "" {
		return live.ErrRoomNotExist
	}
	return nil
}

func (l *Live) GetInfo() (info *live.Info, err error) {
	if l.realId == "" {
		if err := l.parseRealId(); err != nil {
			return nil, err
		}
	}
	resp, err := requests.Get(l.Url.String(), live.CommonUserAgent)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, live.ErrRoomNotExist
	}
	dom, err := resp.Text()
	if err != nil {
		return nil, err
	}
	hostname := utils.Match1(`"username":"(.*?)"`, dom)

	resp, err = requests.Get(roomApiUrl, requests.Query("roomId", l.realId), live.CommonUserAgent)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, live.ErrRoomNotExist
	}
	body, err := resp.Bytes()
	if err != nil {
		return nil, err
	}
	info = &live.Info{
		Live:     l,
		HostName: hostname,
		RoomName: gjson.GetBytes(body, "Broadcast.Title").String(),
		Status:   gjson.GetBytes(body, "Broadcast.LiveType").Int() > 0,
	}
	return info, nil

}

func (l *Live) GetStreamUrls() (us []*url.URL, err error) {
	if l.realId == "" {
		if err := l.parseRealId(); err != nil {
			return nil, err
		}
	}
	resp, err := requests.Get(liveApiUrl, live.CommonUserAgent, requests.Query("roomId", l.realId))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, live.ErrRoomNotExist
	}
	body, err := resp.Bytes()
	urls := make([]string, 0, 0)
	gjson.GetBytes(body, "playLines.0.urls.#.securityUrl").ForEach(func(key, value gjson.Result) bool {
		urls = append(urls, value.String())
		return true
	})
	return utils.GenUrls(urls...)
}

func (l *Live) GetPlatformCNName() string {
	return cnName
}
