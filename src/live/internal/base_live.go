package internal

import (
	"context"
	"fmt"
	"net/url"
	"time"

	"github.com/bililive-go/bililive-go/src/configs"
	"github.com/bililive-go/bililive-go/src/instance"
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
	requestSession := requests.DefaultSession
	config := configs.GetCurrentConfig()
	if config != nil && config.Debug {
		client, _ := utils.CreateConnCounterClient()
		requestSession = requests.NewSession(client)
	}
	return BaseLive{
		Url:            url,
		LiveId:         genLiveId(url),
		RequestSession: requestSession,
	}
}

func (a *BaseLive) UpdateLiveOptionsbyConfig(ctx context.Context, room *configs.LiveRoom) (err error) {
	inst := instance.GetInstance(ctx)
	url, err := url.Parse(room.Url)
	if err != nil {
		return
	}
	opts := make([]live.Option, 0)
	if v, ok := inst.Config.Cookies[url.Host]; ok {
		opts = append(opts, live.WithKVStringCookies(url, v))
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
