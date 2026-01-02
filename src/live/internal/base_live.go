package internal

import (
	"context"
	"fmt"
	"net/url"
	"time"

	"github.com/bililive-go/bililive-go/src/configs"
	"github.com/bililive-go/bililive-go/src/live"
	"github.com/bililive-go/bililive-go/src/pkg/utils"
	"github.com/bililive-go/bililive-go/src/types"
	"github.com/hr3lxphr6j/requests"
)

type BaseLive struct {
	Url            *url.URL
	LastStartTime  time.Time
	LiveId         types.LiveID
	Options        *live.Options
	RequestSession *requests.Session
}

func genLiveId(url *url.URL) types.LiveID {
	return genLiveIdByString(fmt.Sprintf("%s%s", url.Host, url.Path))
}

func genLiveIdByString(value string) types.LiveID {
	return types.LiveID(utils.GetMd5String([]byte(value)))
}

func NewBaseLive(url *url.URL) BaseLive {
	var requestSession *requests.Session
	config := configs.GetCurrentConfig()
	if config != nil && config.Debug {
		client, _ := utils.CreateConnCounterClient()
		requestSession = requests.NewSession(client)
	} else {
		// 注意：这里刻意改变了非调试模式下的默认行为。
		// 之前：非调试模式直接使用 requests.DefaultSession（内部使用默认 HTTP 客户端，
		//       也就是 Go 标准库的 http.DefaultClient 和默认 TLS 拨号逻辑）。
		// 现在：非调试模式也改为使用自定义 HTTP 客户端（utils.CreateDefaultClient），
		//       以便兼容 edgesrv.com 的 TLS 配置 / 握手要求。
		// 影响：所有非调试模式下发起的 HTTPS 请求（不仅限于访问 edgesrv.com）都会经过
		//       自定义 TLS 拨号逻辑，而不再走全局默认的 TLS 配置。这是一个全局性行为变更，
		//       如果后续遇到 TLS/网络相关问题，请注意排查这里的自定义客户端配置。
		client := utils.CreateDefaultClient()
		requestSession = requests.NewSession(client)
	}
	return BaseLive{
		Url:            url,
		LiveId:         genLiveId(url),
		RequestSession: requestSession,
	}
}

func (a *BaseLive) UpdateLiveOptionsbyConfig(ctx context.Context, room *configs.LiveRoom) (err error) {
	url, err := url.Parse(room.Url)
	if err != nil {
		return
	}
	opts := make([]live.Option, 0)
	if cfg := configs.GetCurrentConfig(); cfg != nil {
		if v, ok := cfg.Cookies[url.Host]; ok {
			opts = append(opts, live.WithKVStringCookies(url, v))
		}
	}
	opts = append(opts, live.WithQuality(room.Quality))
	opts = append(opts, live.WithAudioOnly(room.AudioOnly))
	opts = append(opts, live.WithNickName(room.NickName))
	a.Options = live.MustNewOptions(opts...)
	return
}

func (a *BaseLive) SetLiveIdByString(value string) {
	a.LiveId = genLiveIdByString(value)
}

func (a *BaseLive) GetLiveId() types.LiveID {
	return a.LiveId
}

func (a *BaseLive) GetRawUrl() string {
	return a.Url.String()
}

func (a *BaseLive) GetLastStartTime() time.Time {
	return a.LastStartTime
}

func (a *BaseLive) SetLastStartTime(time time.Time) {
	a.LastStartTime = time
}

func (a *BaseLive) GetOptions() *live.Options {
	return a.Options
}

// TODO: remove this method
func (a *BaseLive) GetStreamUrls() ([]*url.URL, error) {
	return nil, live.ErrNotImplemented
}

// TODO: remove this method
func (a *BaseLive) GetStreamInfos() ([]*live.StreamUrlInfo, error) {
	return nil, live.ErrNotImplemented
}
