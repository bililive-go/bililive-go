package cc

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"

	"github.com/hr3lxphr6j/requests"
	"github.com/tidwall/gjson"

	"github.com/bililive-go/bililive-go/src/live"
	"github.com/bililive-go/bililive-go/src/live/internal"
	"github.com/bililive-go/bililive-go/src/pkg/utils"
)

const (
	domain = "cc.163.com"
	cnName = "CC直播"

	apiUrl = "http://cgi.v.cc.163.com/video_play_url/"
	dataRe = `<script id="__NEXT_DATA__" type="application/json" crossorigin="anonymous">(.*?)</script>`
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

func (l *Live) getData() (*gjson.Result, error) {
	resp, err := requests.Get(l.Url.String(), live.CommonUserAgent)
	if err != nil {
		return nil, err
	}
	body, err := resp.Bytes()
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, live.ErrRoomNotExist
	}
	data := utils.UnescapeHTMLEntity(utils.Match1(dataRe, string(body)))
	if data == "" {
		return nil, errors.New("data is empty")
	}
	result := gjson.Parse(data)
	return &result, nil
}

func (l *Live) getCcID() (string, error) {
	data, err := l.getData()
	if err != nil {
		return "", err
	}
	return data.Get("props.pageProps.roomInfoInitData.micfirst.ccid").String(), nil
}

func (l *Live) GetInfo() (info *live.Info, err error) {
	data, err := l.getData()
	if err != nil {
		return nil, err
	}
	var (
		hostName = data.Get("props.pageProps.roomInfoInitData.micfirst.nickname").String()
		roomName = data.Get("props.pageProps.roomInfoInitData.live.title").String()
	)

	if hostName == "" || roomName == "" {
		return nil, errors.New("failed to parse host`s name and room`s name")
	}

	info = &live.Info{
		Live:     l,
		HostName: hostName,
		RoomName: roomName,
		Status:   data.Get("props.pageProps.roomInfoInitData.live.swf").String() != "",
	}
	return info, nil
}

func (l *Live) GetStreamUrls() (us []*url.URL, err error) {
	ccid, err := l.getCcID()
	if err != nil {
		return nil, err
	}
	resp, err := requests.Get(fmt.Sprintf("%s%s", apiUrl, ccid), live.CommonUserAgent)
	if err != nil {
		return nil, err
	}
	body, err := resp.Bytes()
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, live.ErrRoomNotExist
	}
	return utils.GenUrls(
		gjson.GetBytes(body, "videourl").String(),
		gjson.GetBytes(body, "bakvideourl").String(),
	)
}

func (l *Live) GetPlatformCNName() string {
	return cnName
}
