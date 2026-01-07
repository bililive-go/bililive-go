package sooplive

import (
	"context"
	"fmt"
	"net/url"
	"regexp"
	"strings"

	"github.com/hr3lxphr6j/requests"
	"github.com/tidwall/gjson"

	"github.com/bililive-go/bililive-go/src/configs"
	"github.com/bililive-go/bililive-go/src/live"
	"github.com/bililive-go/bililive-go/src/live/internal"
)

const (
	domain    = "play.sooplive.co.kr"
	domainOld = "play.afreecatv.com"
	cnName    = "SoopLive"

	statusApi   = "https://st.sooplive.co.kr/api/get_station_status.php"
	playerApi   = "https://live.sooplive.co.kr/afreeca/player_live_api.php"
	commonAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36"
)

var (
	bnoRegex = regexp.MustCompile(`window\.nBroadNo\s*=\s*(\d+);`)
)

func init() {
	live.Register(domain, new(builder))
	live.Register(domainOld, new(builder))
	live.Register("www.sooplive.co.kr", new(builder))
	live.Register("www.afreecatv.com", new(builder))
}

type builder struct{}

func (b *builder) Build(url *url.URL) (live.Live, error) {
	return &Live{
		BaseLive: internal.NewBaseLive(url),
	}, nil
}

type Live struct {
	internal.BaseLive
	bjID string
}

func (l *Live) UpdateLiveOptionsbyConfig(ctx context.Context, room *configs.LiveRoom) error {
	l.BaseLive.UpdateLiveOptionsbyConfig(ctx, room)
	// 尝试加载统一的 SoopLive Cookie
	cfg := configs.GetCurrentConfig()
	if cfg != nil {
		if cookie, ok := cfg.Cookies["sooplive.co.kr"]; ok {
			l.Options = live.MustNewOptions(
				live.WithKVStringCookies(l.Url, cookie),
				live.WithQuality(room.Quality),
				live.WithAudioOnly(room.AudioOnly),
				live.WithNickName(room.NickName),
			)
		}
	}
	return nil
}

func (l *Live) parseBjId() string {
	if l.bjID != "" {
		return l.bjID
	}
	// Path example: /lilpa0309/290537786 or /lilpa0309
	paths := strings.Split(strings.Trim(l.Url.Path, "/"), "/")
	if len(paths) > 0 {
		l.bjID = paths[0]
	}
	return l.bjID
}

func (l *Live) getBnoFromUrl() string {
	paths := strings.Split(strings.Trim(l.Url.Path, "/"), "/")
	if len(paths) > 1 {
		return paths[1]
	}
	return ""
}

func (l *Live) GetInfo() (*live.Info, error) {
	bjId := l.parseBjId()
	if bjId == "" {
		return nil, live.ErrRoomUrlIncorrect
	}

	// 1. 优先尝试从播放器 API 获取精确的直播信息（标题、状态、昵称）
	// 这个接口在开播时会返回当前直播的真实标题
	cookieKVs := l.getCookieMap()
	resp, err := l.RequestSession.Post(playerApi,
		requests.UserAgent(commonAgent),
		requests.Cookies(cookieKVs),
		requests.Form(map[string]string{
			"bid":  bjId,
			"type": "live",
		}),
	)
	if err == nil {
		body, err := resp.Bytes()
		if err != nil {
			return nil, err
		}
		res := gjson.ParseBytes(body)
		if res.Get("CHANNEL.RESULT").Int() == 1 {
			return &live.Info{
				Live:     l,
				HostName: res.Get("CHANNEL.BJNICK").String(),
				RoomName: res.Get("CHANNEL.TITLE").String(),
				Status:   true,
			}, nil
		}
	}

	// 2. 如果房间未开播或 API 失败，回退到站点状态 API 获取基本信息
	resp, err = l.RequestSession.Get(statusApi, requests.UserAgent(commonAgent), requests.Query("szBjId", bjId))
	if err != nil {
		return nil, err
	}
	body, err := resp.Bytes()
	if err != nil {
		return nil, err
	}
	res := gjson.ParseBytes(body)

	if res.Get("RESULT").Int() != 1 {
		return nil, live.ErrRoomNotExist
	}

	return &live.Info{
		Live:     l,
		HostName: res.Get("DATA.user_nick").String(),
		RoomName: res.Get("DATA.station_title").String(), // 使用站点标题作为回退
		Status:   res.Get("DATA.broad_start").String() != "",
	}, nil
}

func (l *Live) GetStreamInfos() (infos []*live.StreamUrlInfo, err error) {
	bjId := l.parseBjId()
	bnoFromUrl := l.getBnoFromUrl()

	// 1. Get Room Metadata (type=live)
	cookieKVs := l.getCookieMap()

	// If no bno in URL, try to get it from page
	if bnoFromUrl == "" {
		resp, err := l.RequestSession.Get(l.GetRawUrl(), requests.UserAgent(commonAgent), requests.Cookies(cookieKVs))
		if err == nil {
			body, err := resp.Text()
			if err != nil {
				return nil, err
			}
			match := bnoRegex.FindStringSubmatch(body)
			if len(match) > 1 {
				bnoFromUrl = match[1]
			}
		}
	}

	resp, err := l.RequestSession.Post(playerApi,
		requests.UserAgent(commonAgent),
		requests.Referer(l.GetRawUrl()),
		requests.Cookies(cookieKVs),
		requests.Form(map[string]string{
			"bid":         bjId,
			"bno":         bnoFromUrl,
			"type":        "live",
			"pwd":         "",
			"player_type": "html5",
			"stream_type": "common",
			"mode":        "landing",
			"from_api":    "0",
		}),
	)
	if err != nil {
		return nil, err
	}

	body, err := resp.Bytes()
	if err != nil {
		return nil, err
	}
	res := gjson.ParseBytes(body)
	channel := res.Get("CHANNEL")

	if channel.Get("RESULT").Int() != 1 {
		code := channel.Get("RESULT").Int()
		if code == -4 || code == -6 {
			return nil, fmt.Errorf("该房间需要登录及 19+ 验证，请在配置文件的 cookies 中添加 sooplive.co.kr 的 Cookie (错误码: %d)", code)
		}
		return nil, fmt.Errorf("获取频道信息失败 (代码: %d)", code)
	}

	bno := channel.Get("BNO").String()
	rmd := channel.Get("RMD").String()
	cdn := channel.Get("CDN").String()

	// 画质处理
	presets := channel.Get("VIEWPRESET").Array()
	availableQualities := make(map[string]bool)
	var firstQuality string
	for i, p := range presets {
		name := p.Get("name").String()
		availableQualities[name] = true
		if i == 0 {
			firstQuality = name
		}
	}

	// 画质映射
	qualityStr := "original"
	switch l.Options.Quality {
	case 1:
		qualityStr = "hd8k"
	case 2:
		qualityStr = "hd4k"
	case 3:
		qualityStr = "hd"
	case 4:
		qualityStr = "sd"
	default:
		qualityStr = "original"
	}

	// 画质回退逻辑：如果请求的画质不可用，尝试回退到 original 或第一个可用画质
	if !availableQualities[qualityStr] {
		if availableQualities["original"] {
			qualityStr = "original"
		} else if firstQuality != "" {
			qualityStr = firstQuality
		}
	}

	// 2. Get AID (type=aid)
	resp, err = l.RequestSession.Post(playerApi,
		requests.UserAgent(commonAgent),
		requests.Referer(l.GetRawUrl()),
		requests.Cookies(cookieKVs),
		requests.Form(map[string]string{
			"bid":         bjId,
			"bno":         bno,
			"type":        "aid",
			"pwd":         "",
			"quality":     qualityStr,
			"player_type": "html5",
			"stream_type": "common",
		}),
	)
	if err != nil {
		return nil, err
	}
	body, err = resp.Bytes()
	if err != nil {
		return nil, err
	}
	aid := gjson.GetBytes(body, "CHANNEL.AID").String()
	if aid == "" {
		return nil, fmt.Errorf("无法获取 AID 鉴权令牌")
	}

	// 3. Get Stream Assignment
	cdnType := cdn
	if strings.Contains(cdn, "gs_cdn") {
		cdnType = "gs_cdn_pc_web"
	} else if strings.Contains(cdn, "lg_cdn") {
		cdnType = "lg_cdn_pc_web"
	}

	assignUrl := fmt.Sprintf("%s/broad_stream_assign.html", rmd)
	resp, err = l.RequestSession.Get(assignUrl,
		requests.UserAgent(commonAgent),
		requests.Query("return_type", cdnType),
		requests.Query("broad_key", fmt.Sprintf("%s-common-%s-hls", bno, qualityStr)),
	)
	if err != nil {
		return nil, err
	}

	body, err = resp.Bytes()
	if err != nil {
		return nil, err
	}
	viewUrl := gjson.GetBytes(body, "view_url").String()
	if viewUrl == "" {
		return nil, fmt.Errorf("流服务器分配失败: %s", gjson.GetBytes(body, "stream_status").String())
	}

	// 4. Final Finalizing
	u, err := url.Parse(viewUrl)
	if err != nil {
		return nil, err
	}

	// Add AID to URL
	q := u.Query()
	q.Set("aid", aid)
	u.RawQuery = q.Encode()

	infos = append(infos, &live.StreamUrlInfo{
		Url:  u,
		Name: qualityStr,
		HeadersForDownloader: map[string]string{
			"User-Agent": commonAgent,
			"Referer":    "https://play.sooplive.co.kr/",
		},
	})

	return infos, nil
}

func (l *Live) GetPlatformCNName() string {
	return cnName
}

func (l *Live) getCookieMap() map[string]string {
	cookies := l.Options.Cookies.Cookies(l.Url)
	cookieKVs := make(map[string]string)
	for _, item := range cookies {
		cookieKVs[item.Name] = item.Value
	}
	return cookieKVs
}
