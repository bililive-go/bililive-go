package internal

import (
	"context"
	"fmt"
	"net/url"
	"time"

	"github.com/sirupsen/logrus"

	"github.com/bililive-go/bililive-go/src/configs"
	"github.com/bililive-go/bililive-go/src/live"
	"github.com/bililive-go/bililive-go/src/pkg/livelogger"
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
	Logger         *livelogger.LiveLogger
}

func genLiveId(url *url.URL) types.LiveID {
	return genLiveIdByString(fmt.Sprintf("%s%s", url.Host, url.Path))
}

func genLiveIdByString(value string) types.LiveID {
	return types.LiveID(utils.GetMd5String([]byte(value)))
}

func NewBaseLive(url *url.URL) BaseLive {
	requestSession := requests.DefaultSession
	config := configs.GetCurrentConfig()
	if config != nil && config.Debug {
		client, _ := utils.CreateConnCounterClient()
		requestSession = requests.NewSession(client)
	}

	// 先生成 LiveId
	liveId := genLiveId(url)

	// 创建直播间专属的 logger（使用默认 64KB 缓冲区，并绑定 roomID）
	logger := livelogger.NewWithRoomID(0, logrus.Fields{
		"host": url.Host,
		"room": url.Path,
	}, string(liveId))

	return BaseLive{
		Url:            url,
		LiveId:         liveId,
		RequestSession: requestSession,
		Logger:         logger,
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

// GetLogger 返回直播间专属的日志记录器
func (a *BaseLive) GetLogger() *livelogger.LiveLogger {
	return a.Logger
}

// GetInfoWithInterval 默认实现，直接返回错误
// 实际的实现应该在 WrappedLive 中
func (a *BaseLive) GetInfoWithInterval(ctx context.Context) (*live.Info, error) {
	return nil, live.ErrNotImplemented
}

// Close 默认实现，不做任何事情
// 实际的资源释放在 WrappedLive 中处理
func (a *BaseLive) Close() {}

// TODO: remove this method
func (a *BaseLive) GetStreamUrls() ([]*url.URL, error) {
	return nil, live.ErrNotImplemented
}

// TODO: remove this method
func (a *BaseLive) GetStreamInfos() ([]*live.StreamUrlInfo, error) {
	return nil, live.ErrNotImplemented
}
