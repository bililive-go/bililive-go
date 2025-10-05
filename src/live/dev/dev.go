//go:build dev

package dev

import (
	"net/url"
	"strings"

	"github.com/bililive-go/bililive-go/src/live"
	"github.com/bililive-go/bililive-go/src/live/internal"
	"github.com/bililive-go/bililive-go/src/pkg/utils"
)

const (
	domain = "localhost:8080"
	cnName = "dev"
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

func (l *Live) GetPlatformCNName() string {
	return cnName
}

func (l *Live) GetInfo() (*live.Info, error) {
	return &live.Info{
		Live:     l,
		HostName: "dev",
		RoomName: strings.TrimPrefix(l.Url.Path, "/files/"),
		Status:   true,
	}, nil
}

func (l *Live) GetStreamInfos() ([]*live.StreamUrlInfo, error) {
	return utils.GenUrlInfos(
		[]*url.URL{l.Url},
		make(map[string]string),
	), nil
}
