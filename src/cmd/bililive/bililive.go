package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/bluele/gcache"
	kiratools "github.com/kira1928/remotetools/pkg/tools"

	_ "github.com/bililive-go/bililive-go/src/cmd/bililive/internal"
	"github.com/bililive-go/bililive-go/src/cmd/bililive/internal/flag"
	"github.com/bililive-go/bililive-go/src/configs"
	"github.com/bililive-go/bililive-go/src/consts"
	"github.com/bililive-go/bililive-go/src/instance"
	"github.com/bililive-go/bililive-go/src/listeners"
	"github.com/bililive-go/bililive-go/src/live"
	"github.com/bililive-go/bililive-go/src/log"
	"github.com/bililive-go/bililive-go/src/metrics"
	"github.com/bililive-go/bililive-go/src/pkg/events"
	"github.com/bililive-go/bililive-go/src/pkg/utils"
	"github.com/bililive-go/bililive-go/src/recorders"
	"github.com/bililive-go/bililive-go/src/servers"
	"github.com/bililive-go/bililive-go/src/tools"
	"github.com/bililive-go/bililive-go/src/types"
)

func getConfig() (*configs.Config, error) {
	var config *configs.Config
	if *flag.Conf != "" {
		c, err := configs.NewConfigWithFile(*flag.Conf)
		if err != nil {
			return nil, err
		}
		config = c
	} else {
		config = flag.GenConfigFromFlags()
	}
	if !config.RPC.Enable && len(config.LiveRooms) == 0 {
		// if config is invalid, try using the config.yml file besides the executable file.
		config, err := getConfigBesidesExecutable()
		if err == nil {
			return config, config.Verify()
		}
	}
	return config, config.Verify()
}

func getConfigBesidesExecutable() (*configs.Config, error) {
	exePath, err := os.Executable()
	if err != nil {
		return nil, err
	}
	configPath := filepath.Join(filepath.Dir(exePath), "config.yml")
	config, err := configs.NewConfigWithFile(configPath)
	if err != nil {
		return nil, err
	}
	return config, nil
}

func main() {
	config, err := getConfig()
	if err != nil {
		fmt.Fprint(os.Stderr, err.Error())
		os.Exit(1)
	}

	configs.SetCurrentConfig(config)

	inst := new(instance.Instance)
	inst.Config = config
	// TODO: Replace gcache with hashmap.
	// LRU seems not necessary here.
	inst.Cache = gcache.New(4096).LRU().Build()
	ctx := context.WithValue(context.Background(), instance.Key, inst)

	logger := log.New(ctx)
	logger.Infof("%s Version: %s Link Start", consts.AppName, consts.AppVersion)
	if config.File != "" {
		logger.Debugf("config path: %s.", config.File)
		logger.Debugf("other flags have been ignored.")
	} else {
		logger.Debugf("config file is not used.")
		logger.Debugf("flag: %s used.", os.Args)
	}
	logger.Debugf("%+v", consts.AppInfo)
	logger.Debugf("%+v", inst.Config)

	if !utils.IsFFmpegExist(ctx) {
		hasFoundFfmpeg := false
		// try to get from remotetools
		if err = tools.Init(); err == nil {
			var toolFfmpeg kiratools.Tool
			if toolFfmpeg, err = tools.Get().GetTool("ffmpeg"); err == nil {
				if toolFfmpeg.DoesToolExist() {
					logger.Infof("FFmpeg found from remotetools: %s", toolFfmpeg.GetToolPath())
					hasFoundFfmpeg = true
				} else {
					if err = toolFfmpeg.Install(); err != nil {
						logger.Fatalln(err.Error() + "\nFFmpeg binary not found and install failed from " + toolFfmpeg.GetInstallSource() + ", Please Check.")
					} else {
						logger.Infof("FFmpeg found from remotetools: %s", toolFfmpeg.GetToolPath())
						hasFoundFfmpeg = true
					}
				}
			}
		}
		if !hasFoundFfmpeg {
			logger.Fatalln("FFmpeg binary not found, Please Check.")
		}
	}
	tools.AsyncInit()

	events.NewDispatcher(ctx)

	inst.Lives = make(map[types.LiveID]live.Live)
	for index := range inst.Config.LiveRooms {
		room := &inst.Config.LiveRooms[index]

		l, liveErr := live.New(ctx, room, inst.Cache)
		if liveErr != nil {
			logger.WithField("url", room).Error(liveErr.Error())
			continue
		}
		if _, ok := inst.Lives[l.GetLiveId()]; ok {
			logger.Errorf("%v is exist!", room)
			continue
		}
		inst.Lives[l.GetLiveId()] = l
		room.LiveId = l.GetLiveId()
	}

	lm := listeners.NewManager(ctx)
	rm := recorders.NewManager(ctx)
	if err = lm.Start(ctx); err != nil {
		logger.Fatalf("failed to init listener manager, error: %s", err)
	}
	if err = rm.Start(ctx); err != nil {
		logger.Fatalf("failed to init recorder manager, error: %s", err)
	}

	if err = metrics.NewCollector(ctx).Start(ctx); err != nil {
		logger.Fatalf("failed to init metrics collector, error: %s", err)
	}

	// 启动 server 要在上面的 manager 初始化之后，否则可能会出现空指针异常
	if inst.Config.RPC.Enable {
		if err = servers.NewServer(ctx).Start(ctx); err != nil {
			logger.WithError(err).Fatalf("failed to init server")
		}
	}

	for _, _live := range inst.Lives {
		room, err := inst.Config.GetLiveRoomByUrl(_live.GetRawUrl())
		if err != nil {
			logger.WithFields(map[string]any{"room": _live.GetRawUrl()}).Error(err)
			panic(err)
		}
		if room.IsListening {
			if err := lm.AddListener(ctx, _live); err != nil {
				logger.WithFields(map[string]any{"url": _live.GetRawUrl()}).Error(err)
			}
		}
		time.Sleep(time.Second * 1)
	}

	c := make(chan os.Signal, 1)
	signal.Notify(c, syscall.SIGHUP, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-c
		if inst.Config.RPC.Enable {
			inst.Server.Close(ctx)
		}
		inst.ListenerManager.Close(ctx)
		inst.RecorderManager.Close(ctx)
	}()

	if inst.Config.Debug {
		go func() {
			for {
				time.Sleep(time.Second * 30)
				utils.ConnCounterManager.PrintMap()
			}
		}()
	}
	inst.WaitGroup.Wait()
	logger.Info("Bye~")
}
