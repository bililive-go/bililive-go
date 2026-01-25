package bilibili

import (
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/hr3lxphr6j/requests"
	"github.com/tidwall/gjson"

	"github.com/bililive-go/bililive-go/src/configs"
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
	resp, err := l.RequestSession.Get(roomInitUrl, live.CommonUserAgent, requests.Query("id", paths[1]), requests.Cookies(cookieKVs))
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
	resp, err := l.RequestSession.Get(
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

	resp, err = l.RequestSession.Get(userApiUrl, live.CommonUserAgent, requests.Query("roomid", l.realID))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("response code %d from user api", resp.StatusCode)
	}
	body, err = resp.Bytes()
	if err != nil {
		return nil, err
	}
	if gjson.GetBytes(body, "code").Int() != 0 {
		return nil, fmt.Errorf("error code %d from user api", gjson.GetBytes(body, "code").Int())
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

	config := configs.GetCurrentConfig()
	if config == nil {
		return nil, live.ErrRoomNotExist
	}

	qn := 10000
	resolvedConfig := config.GetEffectiveConfigForRoom(l.GetRawUrl())
	if resolvedConfig.StreamPreference.Quality != nil {
		preferredQn := getQnFromQuality(*resolvedConfig.StreamPreference.Quality)
		if preferredQn > 0 {
			qn = preferredQn
		}
	}
	apiUrl := liveApiUrlv2
	query := fmt.Sprintf("?room_id=%s&protocol=0,1&format=0,1,2&codec=0,1&qn=%d&platform=web&ptype=8&dolby=5&panorama=1", l.realID, qn)
	agent := live.CommonUserAgent

	// for audio only use android api
	if l.Options.AudioOnly {
		params := map[string]string{
			"appkey":      "iVGUTjsxvpLeuDCf",
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

	resp, err := l.RequestSession.Get(apiUrl+query, agent, requests.Cookies(cookieKVs))
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

	// 获取所有可用流
	infos = make([]*live.StreamUrlInfo, 0)

	// 解析所有stream、format、codec组合
	gjson.GetBytes(body, "data.playurl_info.playurl.stream").ForEach(func(_, streamValue gjson.Result) bool {
		protocolName := streamValue.Get("protocol_name").String() // "http_stream" or "http_hls"

		streamValue.Get("format").ForEach(func(_, formatValue gjson.Result) bool {
			formatName := formatValue.Get("format_name").String() // "flv" "ts" or "fmp4"
			_ = formatValue.Get("master_url").String()            // 可能为空（HLS没有此字段）

			formatValue.Get("codec").ForEach(func(_, codecValue gjson.Result) bool {
				codecName := codecValue.Get("codec_name").String() // "avc" or "hevc"
				currentQn := int(codecValue.Get("current_qn").Int())
				acceptQns := codecValue.Get("accept_qn").Array() // 可用的其他清晰度
				baseURL := codecValue.Get("base_url").String()
				urlInfos := codecValue.Get("url_info").Array()

				if len(urlInfos) == 0 {
					return true // 继续下一个codec
				}

				// 确定格式类型
				format := ""
				if protocolName == "http_hls" || formatName == "fmp4" {
					format = "hls"
				} else if formatName == "flv" {
					format = "flv"
				}

				// 解析编码
				var codec string
				switch codecName {
				case "avc":
					codec = "h264"
				case "hevc":
					codec = "h265"
				}

				// 构建URLs
				urlStrings := make([]string, 0, len(urlInfos))
				for _, urlInfo := range urlInfos {
					host := urlInfo.Get("host").String()
					extra := urlInfo.Get("extra").String()
					if host != "" && baseURL != "" {
						fullURL := host + baseURL + extra
						urlStrings = append(urlStrings, fullURL)
					}
				}

				if len(urlStrings) == 0 {
					return true
				}

				// 生成URL
				urls, err := utils.GenUrls(urlStrings...)
				if err != nil {
					return true
				}

				// 创建StreamUrlInfo
				for urlIndex, u := range urls {
					for _, acceptQnObj := range acceptQns {
						acceptQn := int(acceptQnObj.Int())
						// 确定清晰度
						quality := getQualityName(acceptQn)
						info := &live.StreamUrlInfo{
							Url:                  u,
							Name:                 fmt.Sprintf("%s - %s", quality, format),
							Quality:              quality,
							Format:               format,
							Codec:                codec,
							HeadersForDownloader: l.getHeadersForDownloader(),

							// 填充用于前端流选择的属性
							AttributesForStreamSelect: map[string]string{
								"画质":          quality,
								"协议":          protocolName,
								"codec":       codec,
								"format_name": formatName,
								"index":       strconv.Itoa(urlIndex),
							},

							IsPlaceHolder: acceptQn != currentQn,
						}

						// 添加到结果
						infos = append(infos, info)
					}
				}

				return true // 继续下一个codec
			})
			return true // 继续下一个format
		})
		return true // 继续下一个stream
	})

	// 如果没有获取到任何流，使用旧的单流逻辑作为fallback
	if len(infos) == 0 {
		l.GetLogger().Warn("主解析逻辑未获取到任何流，使用 fallback 逻辑")
		urlStrings := make([]string, 0, 4)
		addr := ""

		if l.Options.Quality == 0 && gjson.GetBytes(body, "data.playurl_info.playurl.stream.1.format.1.codec.#").Int() > 1 {
			addr = "data.playurl_info.playurl.stream.1.format.1.codec.1" // hevc m3u8
			l.GetLogger().Debug("fallback: 选择 HEVC M3U8 流")
		} else {
			addr = "data.playurl_info.playurl.stream.0.format.0.codec.0" // avc flv
			l.GetLogger().Debug("fallback: 选择 AVC FLV 流")
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
	}

	return
}

// getQualityName 根据清晰度代码返回清晰度名称
func getQualityName(qn int) string {
	qualityMap := map[int]string{
		30000: "杜比",
		20000: "4K",
		15000: "2K",
		10000: "原画",
		400:   "蓝光",
		250:   "超清",
		150:   "高清",
		80:    "流畅",
	}

	if name, ok := qualityMap[qn]; ok {
		return name
	}
	return fmt.Sprintf("qn%d", qn)
}

func getQnFromQuality(quality string) int {
	qnMap := map[string]int{
		"杜比": 30000,
		"4K": 20000,
		"2K": 15000,
		"原画": 10000,
		"蓝光": 400,
		"超清": 250,
		"高清": 150,
		"流畅": 80,
	}

	if qn, ok := qnMap[quality]; ok {
		return qn
	}
	return 0
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
