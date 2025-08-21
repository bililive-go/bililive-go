package douyin

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/bililive-go/bililive-go/src/live"
	"github.com/hr3lxphr6j/requests"
	"github.com/sirupsen/logrus"
	"github.com/tidwall/gjson"
)

func (l *Live) getDouYinAppStreamData() (info *live.Info,
	streamUrlInfo, originUrlList map[string]interface{}, err error) {
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

	var roomData map[string]interface{}
	var anchorName, title string
	webRid := ""
	if parts := strings.SplitN(l.Url.String(), "live.douyin.com/", 2); len(parts) > 1 {
		logrus.Debug("[getDouYinAppStreamData][1]" + l.Url.String())
		webRid = strings.SplitN(parts[1], "?", 2)[0]
		params := url.Values{
			"aid":              {"6383"},
			"app_name":         {"douyin_web"},
			"live_id":          {"1"},
			"device_platform":  {"web"},
			"language":         {"zh-CN"},
			"browser_language": {"zh-CN"},
			"browser_platform": {"Win32"},
			"browser_name":     {"Chrome"},
			"browser_version":  {"116.0.0.0"},
			"web_rid":          {webRid},
		}

		api := fmt.Sprintf("https://live.douyin.com/webcast/room/web/enter/?%s", params.Encode())

		var resp *requests.Response
		resp, err = l.asyncReq(api, localHeaders, 0)
		if err != nil {
			return
		}

		if resp.StatusCode != http.StatusOK {
			err = fmt.Errorf("failed to get page, code: %d, %w",
				resp.StatusCode, live.ErrInternalError)
			return
		}

		var jsonStr string
		jsonStr, err = resp.Text()
		if err != nil {
			return
		}

		logrus.Debug("[getDouYinAppStreamData][2]" + jsonStr)

		anchorName = gjson.Get(jsonStr, "anchor_name").String()
		title = gjson.Get(jsonStr, "title").String()
	} else {
		var roomID, secUID string
		roomID, secUID, err = l.getSecUserID(localHeaders)
		if err == nil {
			roomData, err = l.getAppData(roomID, secUID, localHeaders)
			if err != nil {
				return
			}

			var ok bool
			anchorName, ok = roomData["anchor_name"].(string)
			if !ok {
				err = errors.New("error: roomData[anchor_name] is not a string")
				return
			}

			title, ok = roomData["title"].(string)
			if !ok {
				err = fmt.Errorf("error: roomData[title] is not a string")
				return
			}
		} else if errors.Is(err, errUnsupportedUrl) {
			var uniqueID string
			uniqueID, err = l.getUniqueID(localHeaders)
			if err != nil {
				return
			}

			if !l.isReTrying {
				l.isReTrying = true
				return l.getDouYinStreamData(fmt.Sprintf("https://live.douyin.com/%s", uniqueID))
			}
			return
		} else {
			return
		}
	}

	var ok bool
	isStreaming := false
	if status, ok := roomData["status"].(float64); ok && int(status) == 2 {
		isStreaming = true
	}

	info = &live.Info{
		Live:     l,
		HostName: anchorName,
		RoomName: title,
		Status:   isStreaming,
	}

	// 如果直播中
	if isStreaming {
		streamUrlInfo, ok = roomData["stream_url"].(map[string]interface{})
		if !ok {
			err = errors.New("stream_url is not valid")
			return
		}

		// live_core_sdk_data
		liveCoreSdkData, _ := streamUrlInfo["live_core_sdk_data"].(map[string]interface{})
		pullDatas, _ := streamUrlInfo["pull_datas"].(map[string]interface{})
		if liveCoreSdkData != nil {
			var jsonStr string
			if len(pullDatas) > 0 {
				for _, v := range pullDatas {
					if pd, ok := v.(map[string]interface{}); ok {
						jsonStr = pd["stream_data"].(string)
						break
					}
				}
			} else if pd, ok := liveCoreSdkData["pull_data"].(map[string]interface{}); ok {
				jsonStr = pd["stream_data"].(string)
			}
			var jsonData struct {
				Data struct {
					Origin struct {
						Main map[string]interface{} `json:"main"`
					} `json:"origin"`
				} `json:"data"`
			}
			if err := json.Unmarshal([]byte(jsonStr), &jsonData); err == nil {
				if mainInfo, ok := jsonData.Data.Origin.Main["sdk_params"].(string); ok {
					var sdkParams map[string]interface{}
					_ = json.Unmarshal([]byte(mainInfo), &sdkParams)
					// originHlsCodec, _ := sdkParams["VCodec"].(string)
					originUrlList = jsonData.Data.Origin.Main
					// originM3u8 := map[string]interface{}{
					// 	"ORIGIN": originUrlList["hls"].(string) + "&codec=" + originHlsCodec,
					// }
					// originFlv := map[string]interface{}{
					// 	"ORIGIN": originUrlList["flv"].(string) + "&codec=" + originHlsCodec,
					// }
					// hlsPullUrlMap, _ := streamUrl["hls_pull_url_map"].(map[string]interface{})
					// flvPullUrl, _ := streamUrl["flv_pull_url"].(map[string]interface{})
					// for k, v := range originM3u8 {
					// 	hlsPullUrlMap[k] = v
					// }
					// for k, v := range originFlv {
					// 	flvPullUrl[k] = v
					// }
					// streamUrl["hls_pull_url_map"] = hlsPullUrlMap
					// streamUrl["flv_pull_url"] = flvPullUrl
					// roomData["stream_url"] = streamUrl
				}
			}
		}
	}
	return
}

func (l *Live) getSecUserID(headers map[string]interface{}) (
	roomID, secUserID string, err error) {
	var resp *requests.Response
	resp, err = l.asyncReq(l.Url.String(), headers, 15)
	if err != nil {
		return
	}

	if resp.StatusCode != http.StatusOK {
		err = fmt.Errorf("failed to get page, code: %d, %w", resp.StatusCode, live.ErrInternalError)
		return
	}

	defer resp.Body.Close()
	redirectURL := resp.Request.URL.String()
	if strings.Contains(redirectURL, "reflow/") {
		re := regexp.MustCompile(`sec_user_id=([\w_\-]+)&`)
		match := re.FindStringSubmatch(redirectURL)
		if len(match) >= 2 {
			secUserID = match[1]
			sp := strings.Split(strings.SplitN(redirectURL, "?", 2)[0], "/")
			roomID = sp[len(sp)-1]
			return roomID, secUserID, nil
		}
		return "", "", errors.New("could not find sec_user_id in the URL")
	}
	return "", "", errUnsupportedUrl
}

func (l *Live) getAppData(roomID, secUID string, headers map[string]interface{}) (
	map[string]interface{}, error) {
	appParams := url.Values{
		"verifyFp":     {"verify_lxj5zv70_7szNlAB7_pxNY_48Vh_ALKF_GA1Uf3yteoOY"},
		"type_id":      {"0"},
		"live_id":      {"1"},
		"room_id":      {roomID},
		"sec_user_id":  {secUID},
		"version_code": {"99.99.99"},
		"app_id":       {"1128"},
	}
	api := fmt.Sprintf("https://webcast.amemv.com/webcast/room/reflow/info/?%s", appParams.Encode())
	resp, err := l.asyncReq(api, headers, 0)
	if err != nil {
		return nil, err
	}

	jsonStr, err := resp.Text()
	if err != nil {
		return nil, err
	}
	var apiResp struct {
		Data struct {
			Room map[string]interface{} `json:"room"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(jsonStr), &apiResp); err != nil {
		return nil, err
	}
	room := apiResp.Data.Room
	if owner, ok := room["owner"].(map[string]interface{}); ok {
		room["anchor_name"] = owner["nickname"]
	}
	return room, nil
}

func (l *Live) getUniqueID(headers map[string]interface{}) (string, error) {
	logrus.Debug("[getUniqueID][1]")
	resp, err := l.asyncReq(l.Url.String(), headers, 0)
	if err != nil {
		return "", err
	}

	redirectURL := resp.Request.URL.String()
	logrus.Debug("[getUniqueID][2]" + redirectURL)
	if strings.Contains(redirectURL, "reflow/") {
		return "", errUnsupportedUrl
	}
	secUserID := strings.Split(strings.SplitN(redirectURL, "?", 2)[0], "/")
	secID := secUserID[len(secUserID)-1]

	localHeaders := headers
	localHeaders["Cookie"] = "ttwid=1%7C4ejCkU2bKY76IySQENJwvGhg1IQZrgGEupSyTKKfuyk%7C1740470403%7Cbc9ad2ee341f1a162f9e27f4641778030d1ae91e31f9df6553a8f2efa3bdb7b4; __ac_nonce=0683e59f3009cc48fbab0; __ac_signature=_02B4Z6wo00f01mG6waQAAIDB9JUCzFb6.TZhmsUAAPBf34; __ac_referer=__ac_blank"
	resp2, err := l.asyncReq(fmt.Sprintf("https://www.iesdouyin.com/share/user/%s", secID), localHeaders, 0)
	if err != nil {
		return "", err
	}

	body, err := resp2.Text()
	if err != nil {
		return "", err
	}

	// 使用正则表达式提取 unique_id
	re := regexp.MustCompile(`unique_id":"(.*?)","verification_type`)
	matches := re.FindAllStringSubmatch(body, -1)
	if len(matches) > 0 {
		return matches[len(matches)-1][1], nil
	} else {
		return "", errors.New("could not find unique_id in the response")
	}
}

func (l *Live) asyncReq(url string, headers map[string]interface{}, timeOut int) (resp *requests.Response, err error) {
	opts := []requests.RequestOption{
		requests.Headers(headers),
	}
	if timeOut > 0 {
		opts = append(opts, requests.Deadline(time.Now().Add(time.Duration(timeOut)*time.Second)))
	}
	req, err := requests.NewRequest(
		http.MethodGet,
		url,
		opts...,
	)

	if err != nil {
		return
	}

	resp, err = l.RequestSession.Do(req)
	if err != nil {
		return
	}

	return
}
