package twitch

import (
	"fmt"
	"math/rand"
	"net/http"
	"net/url"
	"strings"

	"github.com/hr3lxphr6j/requests"
	"github.com/tidwall/gjson"

	"github.com/bililive-go/bililive-go/src/live"
	"github.com/bililive-go/bililive-go/src/live/internal"
)

const (
	domain = "www.twitch.tv"
	cnName = "twitch"

	clientId      = "jzkbprff40iqj646a697cyrvl0zt2m6"
	channelApiUrl = "https://api.twitch.tv/kraken/channels/%s"
	liveBaseUrl   = "https://usher.ttvnw.net/api/channel/hls/%s.m3u8"
	streamApiUrl  = "https://api.twitch.tv/kraken/streams/%s"
	tokenApiUrl   = "https://api.twitch.tv/api/channels/%s/access_token"

	v5Header   = "application/vnd.twitchtv.v5+json"
	userApiUrl = "https://api.twitch.tv/kraken/users?login=%s"
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

var headers = map[string]string{"client-id": clientId}

type Live struct {
	internal.BaseLive
	userId, hostName, roomName string
}

// 在hostName, roomName为空执行，在live有效时再从steam api解析
func (l *Live) parseInfo() error {
	paths := strings.Split(l.Url.Path, "/")
	if len(paths) < 2 {
		return live.ErrRoomUrlIncorrect
	}
	chanId := paths[1]
	resp, err := requests.Get(fmt.Sprintf(userApiUrl, chanId), live.CommonUserAgent,
		requests.Header("client-id", clientId), requests.Header("Accept", v5Header))
	if err != nil {
		return err
	}
	body, err := resp.Bytes()
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusOK {
		return live.ErrRoomNotExist
	}
	if gjson.GetBytes(body, "_total").Int() < 1 {
		return live.ErrRoomNotExist
	}
	l.userId = gjson.GetBytes(body, "users").Array()[0].Get("_id").String()

	resp, err = requests.Get(fmt.Sprintf(channelApiUrl, l.userId), live.CommonUserAgent,
		requests.Header("client-id", clientId), requests.Header("Accept", v5Header))
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusOK {
		return live.ErrRoomNotExist
	}
	body, err = resp.Bytes()
	if err != nil {
		return err
	}
	l.hostName = gjson.GetBytes(body, "name").String()
	l.roomName = gjson.GetBytes(body, "status").String()
	return nil
}

func (l *Live) GetInfo() (info *live.Info, err error) {
	if l.hostName == "" || l.roomName == "" || l.userId == "" {
		if err := l.parseInfo(); err != nil {
			return nil, err
		}
	}
	resp, err := requests.Get(fmt.Sprintf(streamApiUrl, l.userId), live.CommonUserAgent,
		requests.Header("client-id", clientId), requests.Header("Accept", v5Header))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, live.ErrRoomNotExist
	}
	body, err := resp.Bytes()
	if err != nil {
		return nil, err
	}
	status := gjson.GetBytes(body, "stream").String() != ""
	if status {
		l.roomName = gjson.GetBytes(body, "stream.channel.status").String()
	}
	info = &live.Info{
		Live:     l,
		HostName: l.hostName,
		RoomName: l.roomName,
		Status:   status,
	}
	return info, nil
}

func (l *Live) GetStreamUrls() (us []*url.URL, err error) {
	if l.hostName == "" || l.roomName == "" {
		if err := l.parseInfo(); err != nil {
			return nil, err
		}
	}
	resp, err := requests.Get(fmt.Sprintf(tokenApiUrl, l.hostName), live.CommonUserAgent, requests.Header("client-id", clientId))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, live.ErrRoomNotExist
	}
	body, err := resp.Bytes()
	if err != nil {
		return nil, err
	}
	var (
		token = gjson.GetBytes(body, "token").String()
		sig   = gjson.GetBytes(body, "sig").String()
		p     = fmt.Sprintf("%d", rand.Intn(9000000)+1000000)
	)
	u, err := url.Parse(fmt.Sprintf(liveBaseUrl, l.hostName))
	if err != nil {
		return nil, err
	}
	v := url.Values{}
	v.Add("allow_source", "true")
	v.Add("allow_audio_only", "true")
	v.Add("allow_spectre", "true")
	v.Add("p", p)
	v.Add("player", "twitchweb")
	v.Add("segment_preference", "4")
	v.Add("sig", sig)
	v.Add("token", token)
	u.RawQuery = v.Encode()
	return []*url.URL{u}, nil
}

func (l *Live) GetPlatformCNName() string {
	return cnName
}
