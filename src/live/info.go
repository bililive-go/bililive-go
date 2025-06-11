package live

import (
	"encoding/json"

	"github.com/bililive-go/bililive-go/src/types"
)

type Info struct {
	Live                 Live
	HostName, RoomName   string
	Status               bool // means isLiving, maybe better to rename it
	Listening, Recording bool
	Initializing         bool
	CustomLiveId         string
	AudioOnly            bool
}

type InfoCookie struct {
	Platform_cn_name string
	Host             string
	Cookie           string
}

func (i *Info) MarshalJSON() ([]byte, error) {
	t := struct {
		Id                types.LiveID `json:"id"`
		LiveUrl           string       `json:"live_url"`
		PlatformCNName    string       `json:"platform_cn_name"`
		HostName          string       `json:"host_name"`
		RoomName          string       `json:"room_name"`
		Status            bool         `json:"status"`
		Listening         bool         `json:"listening"`
		Recording         bool         `json:"recording"`
		Initializing      bool         `json:"initializing"`
		LastStartTime     string       `json:"last_start_time,omitempty"`
		LastStartTimeUnix int64        `json:"last_start_time_unix,omitempty"`
		AudioOnly         bool         `json:"audio_only"`
	}{
		Id:             i.Live.GetLiveId(),
		LiveUrl:        i.Live.GetRawUrl(),
		PlatformCNName: i.Live.GetPlatformCNName(),
		HostName:       i.HostName,
		RoomName:       i.RoomName,
		Status:         i.Status,
		Listening:      i.Listening,
		Recording:      i.Recording,
		Initializing:   i.Initializing,
		AudioOnly:      i.AudioOnly,
	}
	if !i.Live.GetLastStartTime().IsZero() {
		t.LastStartTime = i.Live.GetLastStartTime().Format("2006-01-02 15:04:05")
		t.LastStartTimeUnix = i.Live.GetLastStartTime().Unix()
	}
	return json.Marshal(t)
}
