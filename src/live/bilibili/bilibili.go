package bilibili

import (
	"fmt"
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
	domain = "live.bilibili.com"
	cnName = "哔哩哔哩"

	roomInitUrl     = "https://api.live.bilibili.com/room/v1/Room/room_init"
	roomApiUrl      = "https://api.live.bilibili.com/room/v1/Room/get_info"
	userApiUrl      = "https://api.live.bilibili.com/live_user/v1/UserInfo/get_anchor_in_room"
	liveApiUrlv2    = "https://api.live.bilibili.com/xlive/web-room/v2/index/getRoomPlayInfo"
	appLiveApiUrlv2 = "https://api.live.bilibili.com/xlive/app-room/v2/index/getRoomPlayInfo"
	biliAppAgent    = "Bilibili Freedoooooom/MarkII BiliDroid/5.49.0 os/android model/MuMu mobi_app/android build/5490400 channel/dw090 innerVer/5490400 osVer/6.0.1 network/2"
	biliWebAgent    = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_12_6) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/59.0.3071.115 Safari/537.36"
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
	realID string
}

func (l *Live) parseRealId() error {
	paths := strings.Split(l.Url.Path, "/")
	if len(paths) < 2 {
		return live.ErrRoomUrlIncorrect
	}
	cookies := l.Options.Cookies.Cookies(l.Url)
	cookieKVs := make(map[string]string)
	for _, item := range cookies {
		cookieKVs[item.Name] = item.Value
	}
	resp, err := requests.Get(roomInitUrl, live.CommonUserAgent, requests.Query("id", paths[1]), requests.Cookies(cookieKVs))
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusOK {
		return live.ErrRoomNotExist
	}
	body, err := resp.Bytes()
	if err != nil || gjson.GetBytes(body, "code").Int() != 0 {
		return live.ErrRoomNotExist
	}
	l.realID = gjson.GetBytes(body, "data.room_id").String()
	return nil
}

func (l *Live) GetInfo() (info *live.Info, err error) {
	// Parse the short id from URL to full id
	if l.realID == "" {
		if err := l.parseRealId(); err != nil {
			return nil, err
		}
	}
	cookies := l.Options.Cookies.Cookies(l.Url)
	cookieKVs := make(map[string]string)
	for _, item := range cookies {
		cookieKVs[item.Name] = item.Value
	}
	resp, err := requests.Get(
		roomApiUrl,
		live.CommonUserAgent,
		requests.Query("room_id", l.realID),
		requests.Query("from", "room"),
		requests.Cookies(cookieKVs),
	)
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
	if gjson.GetBytes(body, "code").Int() != 0 {
		return nil, live.ErrRoomNotExist
	}

	info = &live.Info{
		Live:      l,
		RoomName:  gjson.GetBytes(body, "data.title").String(),
		Status:    gjson.GetBytes(body, "data.live_status").Int() == 1,
		AudioOnly: l.Options.AudioOnly,
	}

	resp, err = requests.Get(userApiUrl, live.CommonUserAgent, requests.Query("roomid", l.realID))
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
	if gjson.GetBytes(body, "code").Int() != 0 {
		return nil, live.ErrInternalError
	}

	info.HostName = gjson.GetBytes(body, "data.info.uname").String()
	return info, nil
}

func (l *Live) GetStreamInfos() (infos []*live.StreamUrlInfo, err error) {
	if l.realID == "" {
		if err := l.parseRealId(); err != nil {
			return nil, err
		}
	}
	cookies := l.Options.Cookies.Cookies(l.Url)
	cookieKVs := make(map[string]string)
	for _, item := range cookies {
		cookieKVs[item.Name] = item.Value
	}
	apiUrl := liveApiUrlv2
	query := fmt.Sprintf("?room_id=%s&protocol=0,1&format=0,1,2&codec=0,1&qn=10000&platform=web&ptype=8&dolby=5&panorama=1", l.realID)
	agent := live.CommonUserAgent
	// for audio only use android api
	if l.Options.AudioOnly {
		params := map[string]string{"appkey": "iVGUTjsxvpLeuDCf",
			"build":       "6310200",
			"codec":       "0,1",
			"device":      "android",
			"device_name": "ONEPLUS",
			"dolby":       "5",
			"format":      "0,2",
			"only_audio":  "1",
			"platform":    "android",
			"protocol":    "0,1",
			"room_id":     l.realID,
			"qn":          strconv.Itoa(l.Options.Quality),
		}
		values := url.Values{}
		for key, value := range params {
			values.Add(key, value)
		}
		query = "?" + values.Encode()
		apiUrl = appLiveApiUrlv2
		agent = requests.UserAgent(biliAppAgent)
	}
	resp, err := requests.Get(apiUrl+query, agent, requests.Cookies(cookieKVs))
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
	urlStrings := make([]string, 0, 4)
	addr := ""

	if l.Options.Quality == 0 && gjson.GetBytes(body, "data.playurl_info.playurl.stream.1.format.1.codec.#").Int() > 1 {
		addr = "data.playurl_info.playurl.stream.1.format.1.codec.1" // hevc m3u8
	} else {
		addr = "data.playurl_info.playurl.stream.0.format.0.codec.0" // avc flv
	}

	baseURL := gjson.GetBytes(body, addr+".base_url").String()
	gjson.GetBytes(body, addr+".url_info").ForEach(func(_, value gjson.Result) bool {
		hosts := gjson.Get(value.String(), "host").String()
		queries := gjson.Get(value.String(), "extra").String()
		urlStrings = append(urlStrings, hosts+baseURL+queries)
		return true
	})

	urls, err := utils.GenUrls(urlStrings...)
	if err != nil {
		return nil, err
	}
	infos = utils.GenUrlInfos(urls, l.getHeadersForDownloader())
	return
}

func (l *Live) GetPlatformCNName() string {
	return cnName
}

func (l *Live) getHeadersForDownloader() map[string]string {
	agent := biliWebAgent
	referer := l.GetRawUrl()
	if l.Options.AudioOnly {
		agent = biliAppAgent
		referer = ""
	}
	return map[string]string{
		"User-Agent": agent,
		"Referer":    referer,
	}
}
