package tools

import (
	_ "embed"
	"errors"
	"sync/atomic"

	"github.com/bililive-go/bililive-go/src/configs"
	"github.com/sirupsen/logrus"

	"github.com/kira1928/remotetools/pkg/tools"
)

type toolStatusValue int32

const (
	toolStatusValueNotInitialized toolStatusValue = iota
	toolStatusValueInitializing
	toolStatusValueInitialized
)

var currentToolStatus atomic.Int32

func AsyncInit() {
	go func() {
		err := Init()
		if err != nil {
			logrus.Errorln("Failed to initialize RemoteTools:", err)
		}
	}()
}

func Init() (err error) {
	// 已初始化直接返回
	if toolStatusValue(currentToolStatus.Load()) == toolStatusValueInitialized {
		return
	}

	// CAS 抢占初始化权；失败表示已在初始化或已初始化，视为无操作
	if !currentToolStatus.CompareAndSwap(int32(toolStatusValueNotInitialized), int32(toolStatusValueInitializing)) {
		return
	}

	defer func() {
		if err != nil {
			currentToolStatus.Store(int32(toolStatusValueNotInitialized))
		} else {
			currentToolStatus.Store(int32(toolStatusValueInitialized))
		}
	}()

	api := tools.Get()
	if api == nil {
		return errors.New("failed to get remotetools API instance")
	}
	configData, err := getConfigData()
	if configData == nil {
		return errors.New("failed to get config data")
	}

	if err = api.LoadConfigFromBytes(configData); err != nil {
		return
	}

	appConfig := configs.GetCurrentConfig()
	if appConfig == nil {
		return errors.New("failed to get app config")
	}

	tools.SetToolFolder(appConfig.OutPutPath + "/external_tools")

	err = api.StartWebUI(0)
	if err != nil {
		return
	}
	logrus.Infoln("RemoteTools Web UI started")

	for _, toolName := range []string{
		"ffmpeg",
		"dotnet",
		"bililive-recorder",
	} {
		AsyncDownloadIfNecessary(toolName)
	}

	return nil
}

func AsyncDownloadIfNecessary(toolName string) {
	go func() {
		err := DownloadIfNecessary(toolName)
		if err != nil {
			logrus.Errorln("Failed to download", toolName, "tool:", err)
		}
	}()
}

func DownloadIfNecessary(toolName string) (err error) {
	api := tools.Get()
	if api == nil {
		return errors.New("failed to get remotetools API instance")
	}

	tool, err := api.GetTool(toolName)
	if err != nil {
		return
	}
	if !tool.DoesToolExist() {
		err = tool.Install()
		if err != nil {
			return err
		}
	}
	logrus.Infoln(toolName, "tool is ready to use, version:", tool.GetVersion())
	return nil
}

func GetWebUIPort() int {
	return tools.Get().GetWebUIPort()
}

func Get() *tools.API {
	return tools.Get()
}
