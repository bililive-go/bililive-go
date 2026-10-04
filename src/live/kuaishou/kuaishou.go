package kuaishou

import (
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
	domain = "live.kuaishou.com"
	cnName = "快手"

	regRenderData = `window\.__INITIAL_STATE__ *= *(.*?) *; *\(`
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
	cookies := l.Options.Cookies.Cookies(l.Url)
	cookieKVs := make(map[string]string)
	for _, item := range cookies {
		cookieKVs[item.Name] = item.Value
	}
	resp, err := l.RequestSession.Get(l.Url.String(), live.CommonUserAgent, requests.Cookies(cookieKVs))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	switch code := resp.StatusCode; code {
	case http.StatusOK:
	case http.StatusNotFound:
		return nil, live.ErrRoomNotExist
	default:
		return nil, fmt.Errorf("failed to get page, code: %v, %w", code, live.ErrInternalError)
	}

	body, err := resp.Text()
	if err != nil {
		return nil, err
	}
	rawData := utils.Match1(regRenderData, body)
	if rawData == "" {
		return nil, fmt.Errorf("failed to get RENDER_DATA from page, %w", live.ErrInternalError)
	}
	unescapedRawData, err := url.QueryUnescape(rawData)
	if err != nil {
		return nil, err
	}
	result := gjson.Parse(unescapedRawData)
	return &result, nil
}

func (l *Live) GetInfo() (info *live.Info, err error) {
	data, err := l.getData()
	if err != nil {
		return nil, err
	}
	info = &live.Info{
		Live:     l,
		HostName: data.Get("liveroom.playList.0.author.name").String(),
		RoomName: data.Get("liveroom.playList.0.liveStream.caption").String(),
		Status:   data.Get("liveroom.playList.0.isLiving").Bool(),
	}
	return
}

func (l *Live) GetStreamUrls() (us []*url.URL, err error) {
	data, err := l.getData()
	if err != nil {
		return nil, err
	}
	var urls []string

	addr := ""
	// playUrls 是字典结构，包含 h264 和 hevc 两种编码
	// 优先使用 h264（兼容性更好）
	addr = "liveroom.playList.0.liveStream.playUrls.h264.adaptationSet.representation.0.url"

	// 由于更高清晰度需要cookie，暂时无法传，先注释
	//maxQuality := len(data.Get("liveroom.playList.0.liveStream.playUrls.h264.adaptationSet.representation").Array()) - 1
	//if l.Options.Quality != 0 && maxQuality >= l.Options.Quality {
	//	addr = "liveroom.playList.0.liveStream.playUrls.h264.adaptationSet.representation." + strconv.Itoa(l.Options.Quality) + ".url"
	//} else if l.Options.Quality != 0 {
	//	addr = "liveroom.playList.0.liveStream.playUrls.h264.adaptationSet.representation." + strconv.Itoa(maxQuality) + ".url"
	//} else {
	//	addr = "liveroom.playList.0.liveStream.playUrls.h264.adaptationSet.representation.0.url"
	//}

	data.Get(addr).ForEach(func(key, value gjson.Result) bool {
		urls = append(urls, value.String())
		return true
	})
	return utils.GenUrls(urls...)
}

func (l *Live) GetPlatformCNName() string {
	return cnName
}
