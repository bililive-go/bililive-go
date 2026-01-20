package consts

import (
	"fmt"
	"os"
	"runtime"
)

const (
	AppName = "BiliLive-go"
)

const (
	LiveStatusStart = "start"
	LiveStatusStop  = "stop"
)

type Info struct {
	AppName    string `json:"app_name"`
	AppVersion string `json:"app_version"`
	BuildTime  string `json:"build_time"`
	GitHash    string `json:"git_hash"`
	Pid        int    `json:"pid"`
	Platform   string `json:"platform"`
	GoVersion  string `json:"go_version"`
	IsDocker   string `json:"is_docker"`
	PUID       string `json:"puid"`
	PGID       string `json:"pgid"`
	UMASK      string `json:"umask"`
}

var (
	BuildTime  string
	AppVersion string
	GitHash    string
	AppInfo    = Info{
		AppName:    AppName,
		AppVersion: AppVersion,
		BuildTime:  BuildTime,
		GitHash:    GitHash,
		Pid:        os.Getpid(),
		Platform:   fmt.Sprintf("%s/%s", runtime.GOOS, runtime.GOARCH),
		GoVersion:  runtime.Version(),
		IsDocker:   os.Getenv("IS_DOCKER"),
		PUID:       os.Getenv("PUID"),
		PGID:       os.Getenv("PGID"),
		UMASK:      os.Getenv("UMASK"),
	}
)
