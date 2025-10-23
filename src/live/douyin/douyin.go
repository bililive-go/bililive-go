package douyin

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strings"

	"github.com/hr3lxphr6j/requests"

	"github.com/bililive-go/bililive-go/src/live"
	"github.com/bililive-go/bililive-go/src/live/internal"
)

const (
	domain       = "live.douyin.com"
	domainForApp = "v.douyin.com"
	cnName       = "抖音"
)

var errUnsupportedUrl = errors.New("the redirect URL does not contain 'reflow/'")

var headers = map[string]interface{}{
	"User-Agent":      "Mozilla/5.0 (Windows NT 10.0; Win64; x64; rv:109.0) Gecko/20100101 Firefox/115.0",
	"Accept-Language": "zh-CN,zh;q=0.8,zh-TW;q=0.7,zh-HK;q=0.5,en-US;q=0.3,en;q=0.2",
	"Referer":         "https://live.douyin.com/",
	"Cookie":          "ttwid=1%7CB1qls3GdnZhUov9o2NxOMxxYS2ff6OSvEWbv0ytbES4%7C1680522049%7C280d802d6d478e3e78d0c807f7c487e7ffec0ae4e5fdd6a0fe74c3c6af149511; my_rd=1; passport_csrf_token=3ab34460fa656183fccfb904b16ff742; passport_csrf_token_default=3ab34460fa656183fccfb904b16ff742; d_ticket=9f562383ac0547d0b561904513229d76c9c21; n_mh=hvnJEQ4Q5eiH74-84kTFUyv4VK8xtSrpRZG1AhCeFNI; store-region=cn-fj; store-region-src=uid; LOGIN_STATUS=1; __security_server_data_status=1; FORCE_LOGIN=%7B%22videoConsumedRemainSeconds%22%3A180%7D; pwa2=%223%7C0%7C3%7C0%22; download_guide=%223%2F20230729%2F0%22; volume_info=%7B%22isUserMute%22%3Afalse%2C%22isMute%22%3Afalse%2C%22volume%22%3A0.6%7D; strategyABtestKey=%221690824679.923%22; stream_recommend_feed_params=%22%7B%5C%22cookie_enabled%5C%22%3Atrue%2C%5C%22screen_width%5C%22%3A1536%2C%5C%22screen_height%5C%22%3A864%2C%5C%22browser_online%5C%22%3Atrue%2C%5C%22cpu_core_num%5C%22%3A8%2C%5C%22device_memory%5C%22%3A8%2C%5C%22downlink%5C%22%3A10%2C%5C%22effective_type%5C%22%3A%5C%224g%5C%22%2C%5C%22round_trip_time%5C%22%3A150%7D%22; VIDEO_FILTER_MEMO_SELECT=%7B%22expireTime%22%3A1691443863751%2C%22type%22%3Anull%7D; home_can_add_dy_2_desktop=%221%22; __live_version__=%221.1.1.2169%22; device_web_cpu_core=8; device_web_memory_size=8; xgplayer_user_id=346045893336; csrf_session_id=2e00356b5cd8544d17a0e66484946f28; odin_tt=724eb4dd23bc6ffaed9a1571ac4c757ef597768a70c75fef695b95845b7ffcd8b1524278c2ac31c2587996d058e03414595f0a4e856c53bd0d5e5f56dc6d82e24004dc77773e6b83ced6f80f1bb70627; __ac_nonce=064caded4009deafd8b89; __ac_signature=_02B4Z6wo00f01HLUuwwAAIDBh6tRkVLvBQBy9L-AAHiHf7; ttcid=2e9619ebbb8449eaa3d5a42d8ce88ec835; webcast_leading_last_show_time=1691016922379; webcast_leading_total_show_times=1; webcast_local_quality=sd; live_can_add_dy_2_desktop=%221%22; msToken=1JDHnVPw_9yTvzIrwb7cQj8dCMNOoesXbA_IooV8cezcOdpe4pzusZE7NB7tZn9TBXPr0ylxmv-KMs5rqbNUBHP4P7VBFUu0ZAht_BEylqrLpzgt3y5ne_38hXDOX8o=; msToken=jV_yeN1IQKUd9PlNtpL7k5vthGKcHo0dEh_QPUQhr8G3cuYv-Jbb4NnIxGDmhVOkZOCSihNpA2kvYtHiTW25XNNX_yrsv5FN8O6zm3qmCIXcEe0LywLn7oBO2gITEeg=; tt_scid=mYfqpfbDjqXrIGJuQ7q-DlQJfUSG51qG.KUdzztuGP83OjuVLXnQHjsz-BRHRJu4e986",
}

func init() {
	live.Register(domain, new(builder))
	live.Register(domainForApp, new(builder))
}

type builder struct{}

func (b *builder) Build(url *url.URL) (live.Live, error) {
	ret := &Live{
		BaseLive: internal.NewBaseLive(url),
	}
	ret.bgoLive = NewBgoLive(ret)
	ret.btoolsLive = NewBtoolsLive(ret)
	return ret, nil
}

type streamData struct {
	streamUrlInfo map[string]interface{}
	originUrlList map[string]interface{}
}

type Live struct {
	internal.BaseLive
	LastAvailableStreamData streamData
	isReTrying              bool
	bgoLive                 bgoLive
	btoolsLive              btoolsLive
}

func (l *Live) getDouYinStreamData(url string) (info *live.Info,
	streamUrlInfo, originUrlList map[string]interface{}, err error) {
	defer func() {
		if err != nil && !l.isReTrying {
			l.isReTrying = true
			info, streamUrlInfo, originUrlList, err = l.getDouYinAppStreamData()
			if err != nil {
				return
			}
		}
	}()

	localHeaders := headers
	// 检查是否有自定义cookie
	var finalCookie string
	if l.Options.Cookies != nil {
		// 如果有自定义cookie，优先使用自定义cookie
		customCookies := l.Options.Cookies.Cookies(l.Url)
		if len(customCookies) > 0 {
			// 构建自定义cookie字符串
			var cookieParts []string
			for _, cookie := range customCookies {
				cookieParts = append(cookieParts, fmt.Sprintf("%s=%s", cookie.Name, cookie.Value))
			}
			finalCookie = strings.Join(cookieParts, "; ")
			localHeaders["Cookie"] = finalCookie
		}
	}

	var resp *requests.Response
	resp, err = l.asyncReq(url, localHeaders, 0)
	if err != nil {
		return
	}

	if resp.StatusCode != http.StatusOK {
		err = fmt.Errorf("failed to get page, code: %v, %w", resp.StatusCode, live.ErrInternalError)
		return
	}

	body, err := resp.Text()
	if err != nil {
		return
	}

	return l.parseRoomInfo(body)
}

// 检查URL可用性的函数
func (l *Live) checkUrlAvailability(urlStr string) bool {
	// 简单的HEAD请求检查
	client := &http.Client{Timeout: 5 * 1000000000} // 5秒超时
	resp, err := client.Head(urlStr)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

// 获取质量索引
func getQualityIndex(quality string) (string, int) {
	qualityMap := map[string]int{
		"origin": 0,
		"uhd":    1,
		"hd":     2,
		"sd":     3,
		"ld":     4,
	}
	if index, exists := qualityMap[quality]; exists {
		return quality, index
	}
	return "hd", 2 // 默认返回hd质量
}

func (l *Live) parseRoomInfo(body string) (info *live.Info,
	streamUrlInfo, originUrlList map[string]interface{}, err error) {
	const errorMessageForErrorf = "getDouYinStreamUrl() failed on step %d"
	stepNumberForLog := 1

	// 使用Python一样的复杂解析逻辑
	var jsonStr string

	// 尝试第一个正则表达式
	reg1, err := regexp.Compile(`(\{\\"state\\":.*?)]\\n"]\)`)
	if err != nil {
		return
	}
	match1 := reg1.FindStringSubmatch(body)
	if len(match1) > 1 {
		jsonStr = match1[1]
	} else {
		// 尝试第二个正则表达式
		var reg2 *regexp.Regexp
		reg2, err = regexp.Compile(`(\{\\"common\\":.*?)]\\n"]\)</script><div hidden`)
		if err != nil {
			return
		}
		match2 := reg2.FindStringSubmatch(body)
		if len(match2) < 2 {
			err = fmt.Errorf(errorMessageForErrorf+". No match found for regex patterns", stepNumberForLog)
			return
		}
		jsonStr = match2[1]
	}

	// 清理JSON字符串
	cleanedString := strings.ReplaceAll(jsonStr, "\\", "")
	cleanedString = strings.ReplaceAll(cleanedString, "u0026", "&")

	// 提取roomStore信息
	roomStoreRegex := regexp.MustCompile(`"roomStore":(.*?),"linkmicStore"`)
	roomStoreMatch := roomStoreRegex.FindStringSubmatch(cleanedString)
	if len(roomStoreMatch) < 2 {
		err = fmt.Errorf(errorMessageForErrorf+". Failed to extract roomStore", stepNumberForLog)
		return
	}

	roomStore := roomStoreMatch[1]

	// 提取主播名称
	anchorNameRegex := regexp.MustCompile(`"nickname":"(.*?)","avatar_thumb`)
	anchorNameMatch := anchorNameRegex.FindStringSubmatch(roomStore)
	if len(anchorNameMatch) < 2 {
		err = fmt.Errorf(errorMessageForErrorf+". Failed to extract anchor name", stepNumberForLog)
		return
	}
	anchorName := anchorNameMatch[1]

	// 构建完整的roomStore JSON
	if strings.Contains(roomStore, `has_commerce_goods`) {
		roomStore = strings.Split(roomStore, `,"has_commerce_goods"`)[0] + "}}}"
	} else {
		// 解析JSON数据
		var roomData map[string]interface{}
		if err = json.Unmarshal([]byte(roomStore), &roomData); err != nil {
			err = fmt.Errorf(errorMessageForErrorf+". Failed to parse roomStore JSON: %v", stepNumberForLog, err)
			return
		}
		info = &live.Info{
			Live:     l,
			HostName: anchorName,
			RoomName: "无法获取直播间名称，可能久未开播",
			Status:   false,
		}
		return
	}

	// 解析JSON数据
	var roomData map[string]interface{}
	if err = json.Unmarshal([]byte(roomStore), &roomData); err != nil {
		err = fmt.Errorf(errorMessageForErrorf+". Failed to parse roomStore JSON: %v", stepNumberForLog, err)
		return
	}

	roomInfo, ok := roomData["roomInfo"].(map[string]interface{})
	if !ok {
		err = fmt.Errorf(errorMessageForErrorf+". Failed to get roomInfo", stepNumberForLog)
		return
	}

	room, ok := roomInfo["room"].(map[string]interface{})
	if !ok {
		err = fmt.Errorf(errorMessageForErrorf+". Failed to get room", stepNumberForLog)
		return
	}

	// 检查直播状态
	status, _ := room["status"].(float64)
	isStreaming := status == 2

	title, _ := room["title"].(string)
	if title == "" {
		title = anchorName
	}

	info = &live.Info{
		Live:     l,
		HostName: anchorName,
		RoomName: title,
		Status:   isStreaming,
	}

	if !isStreaming {
		return
	}
	stepNumberForLog++

	// 获取stream_url
	streamUrlInfo, ok = room["stream_url"].(map[string]interface{})
	if !ok {
		err = fmt.Errorf(errorMessageForErrorf+". Failed to get stream_url", stepNumberForLog)
		return
	}

	// 获取stream_orientation
	streamOrientation, _ := streamUrlInfo["stream_orientation"].(float64)

	// 尝试从HTML中提取origin信息 - 方法1
	originRegex1 := regexp.MustCompile(`"origin":\{"main":(.*?),"dash"`)
	originMatch1 := originRegex1.FindStringSubmatch(body)
	if len(originMatch1) > 1 {
		originJsonStr := originMatch1[1] + "}"
		originJsonStr = strings.ReplaceAll(originJsonStr, "\\", "")
		originJsonStr = strings.ReplaceAll(originJsonStr, "u0026", "&")

		if err = json.Unmarshal([]byte(originJsonStr), &originUrlList); err != nil {
			return
		}
	}

	// 如果方法1失败，尝试方法2 - 查找common数据
	if originUrlList == nil {
		// 查找common数据
		commonRegex := regexp.MustCompile(`"(\{\\"common\\":.*?)"]\)</script><script nonce=`)
		commonMatches := commonRegex.FindAllStringSubmatch(body, -1)
		if len(commonMatches) > 0 {
			var jsonStr2 string
			if streamOrientation == 1 && len(commonMatches) > 0 {
				jsonStr2 = commonMatches[0][1]
			} else if len(commonMatches) > 1 {
				jsonStr2 = commonMatches[1][1]
			}

			if jsonStr2 != "" {
				cleanedJsonStr2 := strings.ReplaceAll(jsonStr2, "\\", "")
				cleanedJsonStr2 = strings.ReplaceAll(cleanedJsonStr2, "\"{", "{")
				cleanedJsonStr2 = strings.ReplaceAll(cleanedJsonStr2, "}\"", "}")
				cleanedJsonStr2 = strings.ReplaceAll(cleanedJsonStr2, "u0026", "&")

				var commonData map[string]interface{}
				if err := json.Unmarshal([]byte(cleanedJsonStr2), &commonData); err == nil {
					if data, ok := commonData["data"].(map[string]interface{}); ok {
						if origin, ok := data["origin"].(map[string]interface{}); ok {
							if main, ok := origin["main"].(map[string]interface{}); ok {
								originUrlList = main
							}
						}
					}
				}
			}
		}
	}
	return
}

func (l *Live) createStreamUrlInfos(streamUrlInfo, originUrlList map[string]interface{}) ([]live.StreamUrlInfo, error) {
	// 构建流URL信息
	streamUrlInfos := make([]live.StreamUrlInfo, 0, 10)

	// 处理FLV URL
	if flvPullUrl, ok := streamUrlInfo["flv_pull_url"].(map[string]interface{}); ok {
		var flvUrls []string
		var flvQualities []string

		// 如果有origin URL，添加到开头
		if originUrlList != nil {
			if originFlv, ok := originUrlList["flv"].(string); ok {
				// 添加codec参数
				originFlvWithCodec := originFlv
				if sdkParams, ok := originUrlList["sdk_params"].(map[string]interface{}); ok {
					if vCodec, ok := sdkParams["VCodec"].(string); ok {
						originFlvWithCodec += "&codec=" + vCodec
					}
				}
				flvUrls = append(flvUrls, originFlvWithCodec)
				flvQualities = append(flvQualities, "ORIGIN")
			}
		}

		// 添加其他FLV流
		for quality, urlStr := range flvPullUrl {
			if urlStrStr, ok := urlStr.(string); ok {
				flvUrls = append(flvUrls, urlStrStr)
				flvQualities = append(flvQualities, quality)
			}
		}

		// 补齐逻辑：如果FLV URL数量少于5个，用最后一个补齐
		for len(flvUrls) < 5 {
			if len(flvUrls) > 0 {
				flvUrls = append(flvUrls, flvUrls[len(flvUrls)-1])
				flvQualities = append(flvQualities, flvQualities[len(flvQualities)-1])
			}
		}

		// 将补齐后的URL添加到streamUrlInfos
		for i, urlStr := range flvUrls {
			url, err := url.Parse(urlStr)
			if err != nil {
				continue
			}
			quality := flvQualities[i]
			streamUrlInfos = append(streamUrlInfos, live.StreamUrlInfo{
				Name:        quality,
				Description: fmt.Sprintf("FLV Stream - %s", quality),
				Url:         url,
				Resolution:  0,
				Vbitrate:    0,
			})
		}
	}

	// 处理HLS URL
	if hlsPullUrlMap, ok := streamUrlInfo["hls_pull_url_map"].(map[string]interface{}); ok {
		var hlsUrls []string
		var hlsQualities []string

		// 如果有origin URL，添加到开头
		if originUrlList != nil {
			if originHls, ok := originUrlList["hls"].(string); ok {
				// 添加codec参数
				originHlsWithCodec := originHls
				if sdkParams, ok := originUrlList["sdk_params"].(map[string]interface{}); ok {
					if vCodec, ok := sdkParams["VCodec"].(string); ok {
						originHlsWithCodec += "&codec=" + vCodec
					}
				}
				hlsUrls = append(hlsUrls, originHlsWithCodec)
				hlsQualities = append(hlsQualities, "ORIGIN")
			}
		}

		// 添加其他HLS流
		for quality, urlStr := range hlsPullUrlMap {
			if urlStrStr, ok := urlStr.(string); ok {
				hlsUrls = append(hlsUrls, urlStrStr)
				hlsQualities = append(hlsQualities, quality)
			}
		}

		// 补齐逻辑：如果HLS URL数量少于5个，用最后一个补齐
		for len(hlsUrls) < 5 {
			if len(hlsUrls) > 0 {
				hlsUrls = append(hlsUrls, hlsUrls[len(hlsUrls)-1])
				hlsQualities = append(hlsQualities, hlsQualities[len(hlsQualities)-1])
			}
		}

		// 将补齐后的URL添加到streamUrlInfos
		for i, urlStr := range hlsUrls {
			url, err := url.Parse(urlStr)
			if err != nil {
				continue
			}
			quality := hlsQualities[i]
			streamUrlInfos = append(streamUrlInfos, live.StreamUrlInfo{
				Name:        quality + "_HLS",
				Description: fmt.Sprintf("HLS Stream - %s", quality),
				Url:         url,
				Resolution:  0,
				Vbitrate:    0,
			})
		}
	}

	// 按分辨率排序（如果有的话）
	sort.Slice(streamUrlInfos, func(i, j int) bool {
		if streamUrlInfos[i].Resolution != streamUrlInfos[j].Resolution {
			return streamUrlInfos[i].Resolution > streamUrlInfos[j].Resolution
		} else {
			return streamUrlInfos[i].Vbitrate > streamUrlInfos[j].Vbitrate
		}
	})
	// TODO: fix inefficient code
	//nolint:ineffassign

	return streamUrlInfos, nil
}

func (l *Live) GetInfo() (info *live.Info, err error) {
	return l.btoolsLive.GetInfo()
}

func (l *Live) _GetInfo() (info *live.Info, err error) {
	l.isReTrying = false
	var streamUrlInfo, originUrlList map[string]interface{}
	if l.Url.Host == domainForApp { // APP
		info, streamUrlInfo, _, err = l.getDouYinAppStreamData()
		if err == nil && info.HostName != "" && info.RoomName != "" {
			l.LastAvailableStreamData = streamData{
				streamUrlInfo: streamUrlInfo,
			}
			return
		}
	}
	info, streamUrlInfo, originUrlList, err = l.getDouYinStreamData(l.Url.String())
	if err == nil && info.HostName != "" && info.RoomName != "" {
		l.LastAvailableStreamData = streamData{
			streamUrlInfo: streamUrlInfo,
			originUrlList: originUrlList,
		}
	} else {
		l.LastAvailableStreamData.streamUrlInfo = nil
		l.LastAvailableStreamData.originUrlList = nil
		info, err = l.bgoLive.GetInfo()
	}

	return
}

func (l *Live) GetStreamInfos() (us []*live.StreamUrlInfo, err error) {
	return l.btoolsLive.GetStreamInfos()
}

// 新增：支持质量选择的GetStreamUrls方法
func (l *Live) GetStreamUrls() (us []*url.URL, err error) {
	quality := "origin"
	if l.LastAvailableStreamData.streamUrlInfo == nil {
		us, err = l.bgoLive.GetStreamUrls()
		return
	}
	res, err := l.createStreamUrlInfos(l.LastAvailableStreamData.streamUrlInfo,
		l.LastAvailableStreamData.originUrlList)
	if err != nil {
		return nil, fmt.Errorf("failed to get stream URL for quality:  ")
	}

	qualityName, qualityIndex := getQualityIndex(quality)

	// 获取指定质量的URL
	if qualityIndex < len(res) {
		selectedUrl := res[qualityIndex].Url

		// 检查URL可用性
		if l.checkUrlAvailability(selectedUrl.String()) {
			return []*url.URL{selectedUrl}, nil
		} else {
			// 如果当前质量不可用，尝试下一个质量
			nextIndex := qualityIndex + 1
			if nextIndex >= len(res) {
				nextIndex = qualityIndex - 1
			}
			if nextIndex >= 0 && nextIndex < len(res) {
				fallbackUrl := res[nextIndex].Url
				return []*url.URL{fallbackUrl}, nil
			}
		}
	}

	return nil, fmt.Errorf("failed to get stream URL for quality: %s", qualityName)
}

func (l *Live) GetPlatformCNName() string {
	return cnName
}
