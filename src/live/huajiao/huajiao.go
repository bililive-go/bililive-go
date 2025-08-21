package huajiao

import (
	"net/http"
	"net/url"

	"github.com/hr3lxphr6j/requests"
	"github.com/tidwall/gjson"

	"github.com/bililive-go/bililive-go/src/live"
	"github.com/bililive-go/bililive-go/src/live/internal"
	"github.com/bililive-go/bililive-go/src/pkg/utils"
)

const (
	domain = "www.huajiao.com"
	cnName = "花椒"

	apiUserInfo  = "https://webh.huajiao.com/User/getUserInfo"
	apiUserFeeds = "https://webh.huajiao.com/User/getUserFeeds"
	apiStream    = "https://live.huajiao.com/live/substream"
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
	uid string
	internal.BaseLive
}

func (l *Live) getUid() (string, error) {
	if l.uid != "" {
		return l.uid, nil
	}

	var uid string
	if uid = utils.Match1(`https?:\/\/www.huajiao.com\/user\/(\d+)`, l.GetRawUrl()); uid != "" {
		// nothing to do
	} else if liveId := utils.Match1(`https?:\/\/www.huajiao.com\/l\/(\d+)`, l.GetRawUrl()); liveId != "" {
		resp, err := l.RequestSession.Get(l.GetRawUrl(), live.CommonUserAgent)
		if err != nil {
			return "", err
		}
		if resp.StatusCode != http.StatusOK {
			return "", live.ErrRoomNotExist
		}
		body, err := resp.Text()
		if err != nil {
			return "", err
		}
		uid = utils.Match1(`<span class="js-author-id">(\d+)</span>`, body)
		// if uid == "" {
		// 	TODO: error log
		// }
	}

	if uid != "" && uid != "0" {
		l.uid = uid
		return l.uid, nil
	} else {
		return "", live.ErrRoomUrlIncorrect
	}
}

func (l *Live) getNickname(uid string) (string, error) {
	resp, err := l.RequestSession.Get(apiUserInfo, live.CommonUserAgent, requests.Query("fmt", "json"), requests.Query("uid", uid))
	if err != nil {
		return "", err
	}
	if resp.StatusCode != http.StatusOK {
		return "", live.ErrRoomNotExist
	}
	body, err := resp.Bytes()
	if err != nil {
		return "", err
	}
	if errno := gjson.GetBytes(body, "errno").Int(); errno != 0 {
		return "", live.ErrRoomNotExist
	}
	return gjson.GetBytes(body, "data.nickname").String(), nil
}

func (l *Live) getLiveFeeds(uid string) ([]gjson.Result, error) {
	resp, err := l.RequestSession.Get(apiUserFeeds, live.CommonUserAgent, requests.Query("fmt", "json"), requests.Query("uid", uid))
	if err != nil {
		return nil, err
	}
	feedsData, err := resp.Bytes()
	if err != nil {
		return nil, err
	}
	return gjson.GetBytes(feedsData, "data.feeds.#(type==1)#").Array(), nil
}

func (l *Live) GetInfo() (info *live.Info, err error) {
	uid, err := l.getUid()
	if err != nil {
		return nil, err
	}

	info = &live.Info{
		Live:         l,
		HostName:     "",
		RoomName:     "",
		Status:       false,
		CustomLiveId: "huajiao/" + uid,
	}
	nickname, err := l.getNickname(uid)
	if err != nil {
		return nil, err
	}
	info.HostName = nickname

	feeds, err := l.getLiveFeeds(uid)
	if err != nil {
		return nil, err
	}
	if len(feeds) == 0 {
		return info, nil
	}

	info.RoomName = feeds[0].Get("feed.title").String()
	info.Status = true
	return info, nil
}

func (l *Live) GetStreamUrls() (us []*url.URL, err error) {
	uid, err := l.getUid()
	if err != nil {
		return nil, err
	}
	feeds, err := l.getLiveFeeds(uid)
	if err != nil {
		return nil, err
	}
	if len(feeds) == 0 {
		return nil, live.ErrInternalError
	}
	var (
		sn     = feeds[0].Get("feed.sn").String()
		liveID = feeds[0].Get("feed.relateid").String()
	)
	resp, err := l.RequestSession.Get(apiStream, live.CommonUserAgent, requests.Queries(map[string]string{
		"sn":     sn,
		"uid":    uid,
		"liveid": liveID,
		"encode": "h264",
	}))
	if err != nil {
		return nil, err
	}
	body, err := resp.Bytes()
	if err != nil {
		return nil, err
	}
	if errno := gjson.GetBytes(body, "errno").Int(); errno != 0 {
		return nil, live.ErrInternalError
	}

	return utils.GenUrls(gjson.GetBytes(body, "data.main").String())
}

func (l *Live) GetPlatformCNName() string {
	return cnName
}
