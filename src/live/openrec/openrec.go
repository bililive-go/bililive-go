package openrec

import (
	"net/http"
	"net/url"
	"strings"

	"github.com/bililive-go/bililive-go/src/live"
	"github.com/bililive-go/bililive-go/src/live/internal"
	"github.com/bililive-go/bililive-go/src/pkg/utils"
)

const (
	domain = "www.openrec.tv"
	cnName = "openrec"
)

type Live struct {
	internal.BaseLive
}

func init() {
	live.Register(domain, new(builder))
}

type builder struct{}

func (b *builder) Build(url *url.URL) (live.Live, error) {
	return &Live{
		BaseLive: internal.NewBaseLive(url),
	}, nil
}

func (l *Live) GetInfo() (info *live.Info, err error) {
	resp, err := l.RequestSession.Get(l.Url.String(), live.CommonUserAgent)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, live.ErrRoomNotExist
	}
	body, err := resp.Text()
	if err != nil {
		return nil, err
	}
	var (
		roomName = utils.ParseString(
			utils.Match1(`"title":"([^:]*)",`, body),
			utils.StringFilterFunc(strings.TrimSpace),
			utils.UnescapeHTMLEntity,
		)
		hostName = utils.ParseString(
			utils.Match1(`"name":"([^:]*)",`, body),
			utils.ParseUnicode,
			utils.UnescapeHTMLEntity,
		)
		status = utils.Match1(`"onairStatus":(\d),`, body)
	)
	if roomName == "" || hostName == "" || status == "" {
		return nil, live.ErrInternalError
	}
	info = &live.Info{
		Live:     l,
		RoomName: roomName,
		HostName: hostName,
		Status:   status == "1",
	}
	return info, nil
}

func (l *Live) GetStreamUrls() (us []*url.URL, err error) {
	resp, err := l.RequestSession.Get(l.Url.String(), live.CommonUserAgent)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, live.ErrRoomNotExist
	}
	body, err := resp.Text()
	if err != nil {
		return nil, err
	}
	return utils.GenUrls(utils.Match1(`{"url":"(\S*m3u8)",`, body))
}

func (l *Live) GetPlatformCNName() string {
	return cnName
}
