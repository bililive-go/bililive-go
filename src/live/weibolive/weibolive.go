package weibolive

import (
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"

	"github.com/bililive-go/bililive-go/src/pkg/utils"
	"github.com/hr3lxphr6j/requests"
	"github.com/tidwall/gjson"

	"github.com/bililive-go/bililive-go/src/live"
	"github.com/bililive-go/bililive-go/src/live/internal"
)

const (
	domain = "weibo.com"
	cnName = "微博直播"

	liveurl = "https://weibo.com/l/!/2/wblive/room/show_pc_live.json?live_id="
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
	roomID string
}

func (l *Live) getRoomInfo() ([]byte, error) {
	paths := strings.Split(l.Url.Path, "/")
	if len(paths) < 5 {
		return nil, live.ErrRoomUrlIncorrect
	}
	roomid := paths[5]
	l.roomID = roomid

	resp, err := l.RequestSession.Get(liveurl+roomid,
		live.CommonUserAgent,
		requests.Headers(map[string]any{
			"Referer": l.Url,
		}))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, live.ErrRoomNotExist
	}
	body, err := resp.Bytes()
	if err != nil || gjson.GetBytes(body, "error_code").Int() != 0 {
		return nil, live.ErrRoomNotExist
	}
	return body, nil
}

func (l *Live) GetInfo() (info *live.Info, err error) {
	body, err := l.getRoomInfo()
	if err != nil {
		return nil, live.ErrRoomNotExist
	}
	info = &live.Info{
		Live:         l,
		HostName:     gjson.GetBytes(body, "data.user.screenName").String(),
		RoomName:     gjson.GetBytes(body, "data.title").String(),
		Status:       gjson.GetBytes(body, "data.status").String() == "1",
		CustomLiveId: "weibolive/" + l.roomID,
	}
	return info, nil
}

func (l *Live) GetStreamUrls() (us []*url.URL, err error) {
	body, err := l.getRoomInfo()
	if err != nil {
		return nil, live.ErrRoomNotExist
	}

	streamurl := gjson.GetBytes(body, "data.live_origin_flv_url").String()
	queryParams := l.Url.Query()
	quality := queryParams.Get("q")
	if quality != "" {
		targetQuality := "_wb" + quality + "avc.flv"
		reg, err := regexp.Compile(`_wb[\d]+avc\.flv`)
		if err == nil && reg.MatchString(streamurl) {
			streamurl = reg.ReplaceAllString(streamurl, targetQuality)
		} else {
			streamurl = strings.ReplaceAll(streamurl, ".flv", targetQuality)
		}
		fmt.Println("weibo stream quality fixed: " + streamurl)
	}

	return utils.GenUrls(streamurl)
}

func (l *Live) GetPlatformCNName() string {
	return cnName
}
