package acfun

import (
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/hr3lxphr6j/requests"
	"github.com/tidwall/gjson"

	"github.com/bililive-go/bililive-go/src/live"
	"github.com/bililive-go/bililive-go/src/live/internal"
	"github.com/bililive-go/bililive-go/src/pkg/utils"
)

const (
	domain = "live.acfun.cn"
	cnName = "acfun"

	roomInfoApi = "https://live.acfun.cn/api/live/info"
	loginApi    = "https://id.app.acfun.cn/rest/app/visitor/login"
	liveInfoApi = "https://api.kuaishouzt.com/rest/zt/live/web/startPlay"
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
	resp, err := requests.Get(roomInfoApi, live.CommonUserAgent, requests.Query("authorId", paths[2]))
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
	res := gjson.ParseBytes(body)
	return &live.Info{
		Live:     l,
		HostName: res.Get("user.name").String(),
		RoomName: res.Get("title").String(),
		Status:   res.Get("liveId").Exists(),
	}, nil
}

func (l *Live) GetStreamUrls() (us []*url.URL, err error) {
	did := "web_" + utils.GenRandomName(16)
	resp, err := requests.Post(
		loginApi,
		live.CommonUserAgent,
		requests.Form(map[string]string{"sid": "acfun.api.visitor"}),
		requests.Cookie("_did", did),
	)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, live.ErrInternalError
	}
	body, err := resp.Bytes()
	if err != nil {
		return nil, err
	}
	res := gjson.ParseBytes(body)
	userId := res.Get("userId").Int()
	visitorSt := res.Get(`acfun\.api\.visitor_st`).String()
	resp, err = requests.Post(liveInfoApi,
		live.CommonUserAgent,
		requests.Queries(map[string]string{
			"subBiz":               "mainApp",
			"kpn":                  "ACFUN_APP",
			"kpf":                  "PC_WEB",
			"userId":               strconv.FormatInt(userId, 10),
			"did":                  did,
			"acfun.api.visitor_st": visitorSt,
		}),
		requests.Form(map[string]string{
			"authorId":       strings.Split(l.Url.Path, "/")[2],
			"pullStreamType": "FLV",
		}),
		requests.Referer(l.GetRawUrl()),
	)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, live.ErrInternalError
	}
	body, err = resp.Bytes()
	if err != nil {
		return nil, err
	}
	representationsJSON := gjson.Get(
		gjson.GetBytes(body, "data.videoPlayRes").String(),
		"liveAdaptiveManifest.0.adaptationSet.representation").String()
	rs, err := newRepresentationsFromJSON(representationsJSON)
	if err != nil {
		return nil, err
	}
	return rs.GenUrls()
}

func (l *Live) GetPlatformCNName() string {
	return cnName
}
