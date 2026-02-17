package consts

import (
	"fmt"
	"os"
	"path/filepath"
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

	// 启动器相关信息（运行时设置）
	IsLauncherManaged bool   `json:"is_launcher_managed"`         // 是否由启动器管理
	LauncherPID       int    `json:"launcher_pid,omitempty"`      // 启动器进程 PID
	LauncherExePath   string `json:"launcher_exe_path,omitempty"` // 启动器可执行文件绝对路径
	BgoExePath        string `json:"bgo_exe_path"`                // 当前 bgo 可执行文件绝对路径
}

var (
	BuildTime  string
	AppVersion string
	GitHash    string
)

// GetAppInfo 返回应用信息
// 注意：必须使用函数而非变量，因为 AppVersion 等字段是通过 -ldflags 在链接阶段注入的，
// 如果使用变量初始化，会在编译阶段求值，此时这些字段还是空字符串
var (
	// launcherPID 启动器进程 PID（由主程序在启动时设置）
	launcherPID int
	// launcherExePath 启动器可执行文件路径（由主程序在启动时设置）
	launcherExePath string
)

// SetLauncherInfo 设置启动器相关信息（由主程序在启动时调用）
func SetLauncherInfo(pid int, exePath string) {
	launcherPID = pid
	launcherExePath = exePath
}

func GetAppInfo() Info {
	// 获取当前 bgo 可执行文件的绝对路径
	bgoExePath := ""
	if exePath, err := os.Executable(); err == nil {
		if absPath, err := filepath.Abs(exePath); err == nil {
			bgoExePath = absPath
		} else {
			bgoExePath = exePath
		}
	}

	isLauncherManaged := os.Getenv("BILILIVE_LAUNCHER") != ""

	return Info{
		AppName:           AppName,
		AppVersion:        AppVersion,
		BuildTime:         BuildTime,
		GitHash:           GitHash,
		Pid:               os.Getpid(),
		Platform:          fmt.Sprintf("%s/%s", runtime.GOOS, runtime.GOARCH),
		GoVersion:         runtime.Version(),
		IsDocker:          os.Getenv("IS_DOCKER"),
		PUID:              os.Getenv("PUID"),
		PGID:              os.Getenv("PGID"),
		UMASK:             os.Getenv("UMASK"),
		IsLauncherManaged: isLauncherManaged,
		LauncherPID:       launcherPID,
		LauncherExePath:   launcherExePath,
		BgoExePath:        bgoExePath,
	}
}
