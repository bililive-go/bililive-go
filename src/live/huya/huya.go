package huya

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/bililive-go/bililive-go/src/live"
	"github.com/bililive-go/bililive-go/src/live/internal"
	"github.com/bililive-go/bililive-go/src/pkg/utils"
)

const (
	domain = "www.huya.com"
	cnName = "虎牙"
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
	getInfoMethodIndex        int
	getStreamInfosMethodIndex int
	LastCdnIndex              int
}

type GetInfoMethod func(l *Live, body string) (*live.Info, error)
type GetStreamInfosMethod func(l *Live) ([]*live.StreamUrlInfo, error)

var GetInfoMethodList = []GetInfoMethod{
	GetInfo_ForXingXiu,
	GetInfo_ForLol,
}

var GetStreamInfosMethodList = []GetStreamInfosMethod{
	GetStreamInfos_ForXingXiu,
	GetStreamInfos_ForLol,
}

func (l *Live) GetHtmlBody() (htmlBody string, err error) {
	html, err := l.RequestSession.Get(l.Url.String(), live.CommonUserAgent)
	if err != nil {
		return
	}
	if html.StatusCode != http.StatusOK {
		err = fmt.Errorf("status code: %d", html.StatusCode)
		return
	}
	htmlBody, err = html.Text()
	return
}

func (l *Live) GetInfo() (info *live.Info, err error) {
	body, err := l.GetHtmlBody()
	if err != nil {
		return nil, err
	}

	if res := utils.Match1("哎呀，虎牙君找不到这个主播，要不搜索看看？", body); res != "" {
		return nil, live.ErrRoomNotExist
	}

	if strings.Contains(body, "该主播涉嫌违规，正在整改中") {
		return &live.Info{
			Live:     l,
			HostName: "该主播涉嫌违规，正在整改中",
			RoomName: "该主播涉嫌违规，正在整改中",
			Status:   false,
		}, nil
	}

	getInfoMethodCount := len(GetInfoMethodList)
	if getInfoMethodCount == 0 {
		return nil, fmt.Errorf("no GetInfoMethod")
	}

	if l.getInfoMethodIndex >= getInfoMethodCount {
		l.getInfoMethodIndex = 0
	}

	info, err = GetInfoMethodList[l.getInfoMethodIndex](l, body)
	l.getInfoMethodIndex++
	return
}

func (l *Live) GetStreamInfos() (infos []*live.StreamUrlInfo, err error) {
	getStreamUrlsMethodCount := len(GetStreamInfosMethodList)
	if getStreamUrlsMethodCount == 0 {
		return nil, fmt.Errorf("no GetStreamUrlsMethod")
	}

	if l.getStreamInfosMethodIndex >= getStreamUrlsMethodCount {
		l.getStreamInfosMethodIndex = 0
	}

	infos, err = GetStreamInfosMethodList[l.getStreamInfosMethodIndex](l)
	l.getStreamInfosMethodIndex++
	return
}

func (l *Live) GetPlatformCNName() string {
	return cnName
}

func getGeneralHeadersForDownloader() map[string]string {
	return map[string]string{
		"Accept":          `text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8`,
		"Accept-Encoding": `gzip, deflate`,
		"Accept-Language": `zh-CN,zh;q=0.8,en-US;q=0.5,en;q=0.3`,
	}
}
