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
)

type BaseLive struct {
	Url           *url.URL
	LastStartTime time.Time
	LiveId        types.LiveID
	Options       *live.Options
}

func genLiveId(url *url.URL) types.LiveID {
	return genLiveIdByString(fmt.Sprintf("%s%s", url.Host, url.Path))
}

func genLiveIdByString(value string) types.LiveID {
	return types.LiveID(utils.GetMd5String([]byte(value)))
}

func NewBaseLive(url *url.URL) BaseLive {
	return BaseLive{
		Url:    url,
		LiveId: genLiveId(url),
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

// TODO: remove this method
func (a *BaseLive) GetStreamUrls() ([]*url.URL, error) {
	return nil, live.ErrNotImplemented
}

// TODO: remove this method
func (a *BaseLive) GetStreamInfos() ([]*live.StreamUrlInfo, error) {
	return nil, live.ErrNotImplemented
}
