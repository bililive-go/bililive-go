package custom

import (
	"net/url"

	"github.com/bililive-go/bililive-go/src/live"
	"github.com/bililive-go/bililive-go/src/live/internal"
	"github.com/bililive-go/bililive-go/src/pkg/utils"
)

const (
	domain = "custom.m3u8"
	cnName = "自定义M3U8"
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

func (l *Live) GetInfo() (info *live.Info, err error) {
	// Parse the actual M3U8 URL and platform name from query parameters
	params := l.Url.Query()
	actualURL := params.Get("url")
	platformName := params.Get("name")
	
	if actualURL == "" {
		return nil, live.ErrRoomUrlIncorrect
	}
	
	// Use custom name from URL parameter or fallback to options or default
	if platformName == "" {
		if l.Options != nil && l.Options.NickName != "" {
			platformName = l.Options.NickName
		} else {
			platformName = cnName
		}
	}
	
	roomName := platformName + " Stream"
	hostName := platformName + " Host"

	info = &live.Info{
		Live:     l,
		RoomName: roomName,
		HostName: hostName,
		Status:   true, // Always assume it's live for custom streams
	}
	return info, nil
}

func (l *Live) GetStreamUrls() (us []*url.URL, err error) {
	// Extract the actual M3U8 URL from query parameters
	params := l.Url.Query()
	actualURL := params.Get("url")
	
	if actualURL == "" {
		return nil, live.ErrRoomUrlIncorrect
	}
	
	// Return the actual M3U8 URL
	return utils.GenUrls(actualURL)
}

func (l *Live) GetStreamInfos() ([]*live.StreamUrlInfo, error) {
	urls, err := l.GetStreamUrls()
	if err != nil {
		return nil, err
	}
	
	// Get platform name for description
	params := l.Url.Query()
	platformName := params.Get("name")
	if platformName == "" {
		if l.Options != nil && l.Options.NickName != "" {
			platformName = l.Options.NickName
		} else {
			platformName = cnName
		}
	}
	
	streamInfos := make([]*live.StreamUrlInfo, len(urls))
	for i, u := range urls {
		streamInfos[i] = &live.StreamUrlInfo{
			Url:         u,
			Name:        "custom",
			Description: platformName + " M3U8 Stream",
		}
	}
	return streamInfos, nil
}

func (l *Live) GetPlatformCNName() string {
	// Use custom name from URL parameter or options
	params := l.Url.Query()
	platformName := params.Get("name")
	
	if platformName != "" {
		return platformName
	}
	
	if l.Options != nil && l.Options.NickName != "" {
		return l.Options.NickName
	}
	
	return cnName
}