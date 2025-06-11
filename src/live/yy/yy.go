package yy

import (
	"bytes"
	"math/rand"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"text/template"
	"time"

	"github.com/hr3lxphr6j/requests"
	"github.com/tidwall/gjson"

	"github.com/bililive-go/bililive-go/src/live"
	"github.com/bililive-go/bililive-go/src/live/internal"
	"github.com/bililive-go/bililive-go/src/pkg/utils"
)

const (
	domain = "www.yy.com"
	cnName = "YY直播"

	roomInitUrl    = "https://www.yy.com/api/liveInfoDetail/{{.Id}}/{{.Id}}/{{.Uid}}"
	roomInitBakUrl = "https://www.yy.com/yyweb/live/not/living/recommend/"
	livereqUrl     = "https://stream-manager.yy.com/v3/channel/streams?uid=0&cid={{.Id}}&sid={{.Id}}&appid=0&sequence={{.Seq}}&encode=json"
	rawdata        = `
	{
		"head": {
			"seq": {{.Seq}},
			"appidstr": "0",
			"bidstr": "121",
			"cidstr": "{{.Id}}",
			"sidstr": "{{.Id}}",
			"uid64": 0,
			"client_type": 108,
			"client_ver": "5.14.13",
			"stream_sys_ver": 1,
			"app": "yylive_web",
			"playersdk_ver": "5.14.13",
			"thundersdk_ver": "0",
			"streamsdk_ver": "5.14.13"
		},
		"client_attribute": {
			"client": "web",
			"model": "",
			"cpu": "",
			"graphics_card": "",
			"os": "chrome",
			"vsdk_version": "",
			"app_identify": "",
			"app_version": "",
			"business": "",
			"scale": "",
			"client_type": 8,
			"h265": 1
		},
		"avp_parameter": {
			"version": 1,
			"client_type": 8,
			"service_type": 0,
			"imsi": 0,
			"send_time": {{.Seq}},
			"line_seq": -1,
			"gear": 4,
			"ssl": 1,
			"stream_format": 0
		}
	}	
	`
)

type data struct {
	Id  string
	Uid string
	Seq string
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

type Live struct {
	internal.BaseLive
	roomID string
}

func (l *Live) getRoomInfo() ([]byte, bool, error) {
	paths := strings.Split(l.Url.Path, "/")
	if len(paths) < 1 {
		return nil, false, live.ErrRoomUrlIncorrect
	}
	roomid := paths[1]
	l.roomID = roomid

	uid := int64(1125e4*rand.Int()) + int64(4283717296)
	tmp := &data{Id: roomid, Uid: strconv.FormatInt(int64(uid), 10)}

	tmpl, err := template.New("roomurlteml").Parse(roomInitUrl)
	if err != nil {
		return nil, false, err
	}
	buf := new(bytes.Buffer)
	err = tmpl.Execute(buf, tmp)
	if err != nil {
		return nil, false, err
	}
	resp, err := requests.Get(buf.String())
	if err != nil {
		return nil, false, err
	}

	/**
	未开播主播 roomInitUrl 接口 返回 {"resultCode":0,"data":null}
	yy直播接口未返回开播状态，通过 roomInitUrl 返回 data是否有数据判断是否开播
	*/

	if resp.StatusCode != http.StatusOK {
		return nil, false, live.ErrRoomNotExist
	}
	body, err := resp.Bytes()
	if err != nil || gjson.GetBytes(body, "resultCode").Int() != 0 {
		return nil, false, live.ErrRoomNotExist
	}
	if gjson.Get(string(body), "data").Type == gjson.Null {
		//返回无data，则停播，从其他接口获取直播间信息
		resp, err = requests.Get(roomInitBakUrl + roomid)
		if err != nil {
			return nil, false, err
		}
		if resp.StatusCode != http.StatusOK {
			return nil, false, live.ErrRoomNotExist
		}
		body, err = resp.Bytes()
		return body, false, nil
	}

	return body, true, nil
}

func (l *Live) GetInfo() (info *live.Info, err error) {
	body, islive, err := l.getRoomInfo()
	if err != nil {
		return nil, live.ErrRoomNotExist
	}
	if islive {
		info = &live.Info{
			Live:         l,
			HostName:     gjson.GetBytes(body, "data.name").String(),
			RoomName:     gjson.GetBytes(body, "data.desc").String(),
			Status:       islive,
			CustomLiveId: "yy/" + l.roomID,
		}
	} else {
		info = &live.Info{
			Live:         l,
			HostName:     gjson.GetBytes(body, "data.video.name").String(),
			RoomName:     gjson.GetBytes(body, "data.video.title").String(),
			Status:       islive,
			CustomLiveId: "yy/" + l.roomID,
		}
	}
	return info, nil
}

func (l *Live) GetStreamUrls() (us []*url.URL, err error) {
	tmpl1, _ := template.New("liverequrl").Parse(livereqUrl)
	tmpdata := &data{Id: l.roomID, Seq: strconv.FormatInt(time.Now().Unix(), 10)}
	liveurl := new(bytes.Buffer)
	err = tmpl1.Execute(liveurl, tmpdata)
	if err != nil {
		return nil, err
	}

	rawbuf := new(bytes.Buffer)
	tmplraw, _ := template.New("raw").Parse(rawdata)
	err = tmplraw.Execute(rawbuf, tmpdata)
	if err != nil {
		return nil, err
	}
	resp, err := requests.Post(liveurl.String(), requests.Body(strings.NewReader(rawbuf.String())))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, live.ErrInternalError
	}
	body, err := resp.Bytes()
	if err != nil {
		return nil, err
	}
	if gjson.GetBytes(body, "avp_info_res").Type == gjson.Null {
		return nil, live.ErrRoomNotExist
	}
	streamKey := gjson.GetBytes(body, "channel_stream_info.streams.#.stream_key").Array()[0].String()
	streamurl := gjson.GetBytes(body, "avp_info_res.stream_line_addr."+streamKey+".cdn_info.url").String()
	return utils.GenUrls(streamurl)
}

func (l *Live) GetPlatformCNName() string {
	return cnName
}
