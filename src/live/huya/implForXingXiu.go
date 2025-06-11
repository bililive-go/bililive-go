package huya

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/bililive-go/bililive-go/src/live"
	"github.com/bililive-go/bililive-go/src/pkg/utils"
	"github.com/hr3lxphr6j/requests"
	"github.com/tidwall/gjson"
)

const uaForXingXiu = "Mozilla/5.0 (iPhone; CPU iPhone OS 17_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Mobile/15E148 MicroMessenger/8.0.49(0x18003137) NetType/WIFI Language/zh_CN WeChat/8.0.49.33 CFNetwork/1474 Darwin/23.0.0"

var downloaderHeadersForXingXiu = func() map[string]string {
	headers := getGeneralHeadersForDownloader()
	headers["User-Agent"] = uaForXingXiu
	return headers
}()

func GetInfo_ForXingXiu(l *Live, body string) (info *live.Info, err error) {
	res, err := getJsonFromBody(body)
	if err != nil {
		return nil, err
	}

	if res := utils.Match1("该主播不存在！", res.String()); res != "" {
		return nil, live.ErrRoomNotExist
	}

	var (
		hostName = res.Get("data.liveData.nick").String()
		roomName = res.Get("data.liveData.introduction").String()
		status   = res.Get("data.realLiveStatus").String()
	)

	if hostName == "" || roomName == "" || status == "" {
		return nil, live.ErrInternalError
	}

	info = &live.Info{
		Live:     l,
		HostName: hostName,
		RoomName: roomName,
		Status:   status == "ON",
	}
	return info, nil
}

func GetStreamInfos_ForXingXiu(l *Live) (infos []*live.StreamUrlInfo, err error) {
	body, err := l.GetHtmlBody()
	if err != nil {
		return nil, err
	}

	data, err := getJsonFromBody(body)
	if err != nil {
		return nil, err
	}

	streamInfoList := data.Get("data.stream.baseSteamInfoList").Array()
	if len(streamInfoList) == 0 {
		return nil, fmt.Errorf("streamInfoList 为空")
	}
	streamInfoObj := streamInfoList[0]
	for _, info := range streamInfoList {
		// 优先使用 TX cdn
		if info.Get("sCdnType").String() == "TX" {
			streamInfoObj = info
			break
		}
	}
	sFlvUrl := streamInfoObj.Get("sFlvUrl").String()
	sStreamName := streamInfoObj.Get("sStreamName").String()
	sFlvUrlSuffix := streamInfoObj.Get("sFlvUrlSuffix").String()
	sFlvAntiCode := streamInfoObj.Get("sFlvAntiCode").String()
	streamUrl := fmt.Sprintf("%s/%s.%s?%s", sFlvUrl, sStreamName, sFlvUrlSuffix, sFlvAntiCode)

	// 如果选择的是 TX，执行额外的字符串替换
	if streamInfoObj.Get("sCdnType").String() == "TX" {
		streamUrl = strings.Replace(streamUrl, "&ctype=tars_mp", "&ctype=huya_webh5", 1)
		streamUrl = strings.Replace(streamUrl, "&fs=bhct", "&fs=bgct", 1)
	}

	res, err := utils.GenUrls(streamUrl)
	if err != nil {
		return nil, err
	}
	infos = utils.GenUrlInfos(res, downloaderHeadersForXingXiu)
	return infos, nil
}

func getJsonFromBody(htmlBody string) (result *gjson.Result, err error) {
	strFilter := utils.NewStringFilterChain(utils.ParseUnicode, utils.UnescapeHTMLEntity)
	rjson := strFilter.Do(utils.Match1(`stream: (\{"data".*?),"iWebDefaultBitRate"`, htmlBody)) + "}"
	gj := gjson.Parse(rjson)

	roomId := gj.Get("data.0.gameLiveInfo.profileRoom").String()
	params := make(map[string]string)
	params["m"] = "Live"
	params["do"] = "profileRoom"
	params["roomid"] = roomId
	params["showSecret"] = "1"

	headers := make(map[string]interface{})
	headers["User-Agent"] = uaForXingXiu
	headers["xweb_xhr"] = "1"
	headers["referer"] = "https://servicewechat.com/wx74767bf0b684f7d3/301/page-frame.html"
	headers["accept-language"] = "zh-CN,zh;q=0.9"
	resp, err := requests.Get("https://mp.huya.com/cache.php", requests.Headers(headers), requests.Queries(params), requests.UserAgent(uaForXingXiu))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, live.ErrRoomNotExist
	}
	body, err := resp.Text()
	if err != nil {
		return nil, err
	}
	res := gjson.Parse(body)
	return &res, nil
}
