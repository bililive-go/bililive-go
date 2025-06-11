package system

import (
	"net/url"

	"github.com/bililive-go/bililive-go/src/live"
	"github.com/bililive-go/bililive-go/src/live/internal"
)

func init() {
	live.InitializingLiveBuilderInstance = new(builder)
}

type builder struct{}

func (b *builder) Build(live live.Live, url *url.URL) (live.Live, error) {
	return &InitializingLive{
		BaseLive:     internal.NewBaseLive(url),
		OriginalLive: live,
	}, nil
}

type InitializingLive struct {
	internal.BaseLive
	OriginalLive live.Live
}

func (l *InitializingLive) GetInfo() (info *live.Info, err error) {
	err = nil
	info = &live.Info{
		Live:         l,
		HostName:     "",
		RoomName:     l.GetRawUrl(),
		Status:       false,
		Initializing: true,
	}
	return
}

func (l *InitializingLive) GetStreamUrls() (us []*url.URL, err error) {
	us = make([]*url.URL, 0)
	err = nil
	return
}

func (l *InitializingLive) GetPlatformCNName() string {
	return ""
}
