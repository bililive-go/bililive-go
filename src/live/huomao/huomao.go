package huomao

import (
	"fmt"
	"net/http"
	"net/url"
	"time"

	"github.com/hr3lxphr6j/requests"
	"github.com/tidwall/gjson"

	"github.com/bililive-go/bililive-go/src/live"
	"github.com/bililive-go/bililive-go/src/live/internal"
	"github.com/bililive-go/bililive-go/src/pkg/utils"
)

const (
	domain = "www.huomao.com"
	cnName = "火猫"

	liveApiUrl = "http://www.huomao.com/swf/live_data"
	salt       = "6FE26D855E1AEAE090E243EB1AF73685"
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
	isDuanbo bool
}

func (l *Live) GetInfo() (info *live.Info, err error) {
	resp, err := requests.Get(l.Url.String(), live.CommonUserAgent)
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
	l.isDuanbo = utils.Match1(`face_label\s?=\s?(\d*);`, body) == "1"
	var (
		hostNameReg = `"nickname":"([^"]*)",`
		roomNameReg = `"channel":"([^"]*)"`
		statusReg   = `"is_live":"?(\d*)"?,`
	)
	if l.isDuanbo {
		hostNameReg = `live_yz_h_nickName\s?=\s?"([^"]*)";`
		roomNameReg = `live_yz_h_channelName\s?=\s?"([^"]*)";`
		statusReg = `is_live\s?=\s?"?(\d*)"?;`
	}
	var (
		hostName = utils.ParseUnicode(utils.Match1(hostNameReg, body))
		roomName = utils.ParseUnicode(utils.Match1(roomNameReg, body))
		status   = utils.Match1(statusReg, body)
	)
	if hostName == "" || roomName == "" || status == "" {
		return nil, live.ErrInternalError
	}
	info = &live.Info{
		Live:     l,
		HostName: hostName,
		RoomName: roomName,
		Status:   status == "1",
	}
	return info, nil
}

func (l *Live) GetStreamUrls() ([]*url.URL, error) {
	resp, err := requests.Get(l.Url.String(), live.CommonUserAgent)
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
	streamReg := `getFlash\("\d*","([^"]*)","\d*"\);`
	if !l.isDuanbo {
		streamReg = `"stream":"([^"]*)"`
	}
	var (
		from     = "huomaoh5room"
		t        = fmt.Sprintf("%d", time.Now().Unix())
		streamID = utils.Match1(streamReg, body)
		token    = utils.GetMd5String([]byte(fmt.Sprintf("%s%s%s%s", streamID, from, t, salt)))
	)
	resp, err = requests.Post(liveApiUrl, live.CommonUserAgent, requests.Queries(map[string]string{
		"VideoIDS":   streamID,
		"streamtype": "live",
		"cdns":       "1",
		"from":       from,
		"time":       t,
		"token":      token,
	}))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, live.ErrRoomNotExist
	}
	body, err = resp.Text()
	if err != nil {
		return nil, err
	}
	urls := make([]string, 0, 0)
	gjson.Get(body, "streamList.#.list.#.url").ForEach(func(key, value gjson.Result) bool {
		for _, u := range value.Array() {
			urls = append(urls, u.String())
		}
		return true
	})
	return utils.GenUrls(urls...)
}

func (l *Live) GetPlatformCNName() string {
	return cnName
}
