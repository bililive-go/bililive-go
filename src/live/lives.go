//go:generate mockgen -package mock -destination mock/mock.go github.com/bililive-go/bililive-go/src/live Live
package live

import (
	"context"
	"errors"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"time"

	"github.com/bililive-go/bililive-go/src/configs"
	"github.com/bililive-go/bililive-go/src/types"
	"github.com/bluele/gcache"
)

var (
	m                               = make(map[string]Builder)
	InitializingLiveBuilderInstance InitializingLiveBuilder
)

func Register(domain string, b Builder) {
	m[domain] = b
}

func getBuilder(domain string) (Builder, bool) {
	builder, ok := m[domain]
	return builder, ok
}

type Builder interface {
	Build(*url.URL) (Live, error)
}

type InitializingLiveBuilder interface {
	Build(Live, *url.URL) (Live, error)
}

type InitializingFinishedParam struct {
	InitializingLive Live
	Live             Live
	Info             *Info
}

type Options struct {
	Cookies   *cookiejar.Jar
	Quality   int
	AudioOnly bool
}

func NewOptions(opts ...Option) (*Options, error) {
	cookieJar, err := cookiejar.New(&cookiejar.Options{})
	if err != nil {
		return nil, err
	}
	options := &Options{Cookies: cookieJar, Quality: 0}
	for _, opt := range opts {
		opt(options)
	}
	return options, nil
}

func MustNewOptions(opts ...Option) *Options {
	options, err := NewOptions(opts...)
	if err != nil {
		panic(err)
	}
	return options
}

type Option func(*Options)

func WithKVStringCookies(u *url.URL, cookies string) Option {
	return func(opts *Options) {
		cookiesList := make([]*http.Cookie, 0)
		for _, pairStr := range strings.Split(cookies, ";") {
			pairs := strings.SplitN(pairStr, "=", 2)
			if len(pairs) != 2 {
				continue
			}
			cookiesList = append(cookiesList, &http.Cookie{
				Name:  strings.TrimSpace(pairs[0]),
				Value: strings.TrimSpace(pairs[1]),
			})
		}
		opts.Cookies.SetCookies(u, cookiesList)
	}
}

func WithQuality(quality int) Option {
	return func(opts *Options) {
		opts.Quality = quality
	}
}

func WithAudioOnly(audioOnly bool) Option {
	return func(opts *Options) {
		opts.AudioOnly = audioOnly
	}
}

type StreamUrlInfo struct {
	Url                  *url.URL
	Name                 string
	Description          string
	Resolution           int
	Vbitrate             int
	HeadersForDownloader map[string]string
}

type Live interface {
	SetLiveIdByString(string)
	GetLiveId() types.LiveID
	GetRawUrl() string
	GetInfo() (*Info, error)
	// Deprecated: GetStreamUrls is deprecated, using GetStreamInfos instead
	GetStreamUrls() ([]*url.URL, error)
	GetStreamInfos() ([]*StreamUrlInfo, error)
	GetPlatformCNName() string
	GetLastStartTime() time.Time
	SetLastStartTime(time.Time)
	UpdateLiveOptionsbyConfig(context.Context, *configs.LiveRoom) error
}

type WrappedLive struct {
	Live
	cache gcache.Cache
}

func newWrappedLive(live Live, cache gcache.Cache) Live {
	return &WrappedLive{
		Live:  live,
		cache: cache,
	}
}

func (w *WrappedLive) GetInfo() (*Info, error) {
	i, err := w.Live.GetInfo()
	if err != nil {
		if info, err2 := w.cache.Get(w); err2 == nil {
			info.(*Info).RoomName = err.Error()
		}
		return nil, err
	}
	if w.cache != nil {
		w.cache.Set(w, i)
	}
	return i, nil
}

func New(ctx context.Context, room *configs.LiveRoom, cache gcache.Cache) (live Live, err error) {
	url, err := url.Parse(room.Url)
	if err != nil {
		return nil, err
	}
	builder, ok := getBuilder(url.Host)
	if !ok {
		return nil, errors.New("not support this url")
	}
	live, err = builder.Build(url)
	if err != nil {
		return
	}
	live.UpdateLiveOptionsbyConfig(ctx, room)
	live = newWrappedLive(live, cache)
	for i := 0; i < 3; i++ {
		var info *Info
		if info, err = live.GetInfo(); err == nil {
			if info.CustomLiveId != "" {
				live.SetLiveIdByString(info.CustomLiveId)
			}
			return
		}
		time.Sleep(1 * time.Second)
	}

	// when room initializaion is failed
	live, err = InitializingLiveBuilderInstance.Build(live, url)
	if err != nil {
		return nil, err
	}
	live.UpdateLiveOptionsbyConfig(ctx, room)
	live = newWrappedLive(live, cache)
	live.GetInfo() // dummy call to initialize cache inside wrappedLive
	return
}
