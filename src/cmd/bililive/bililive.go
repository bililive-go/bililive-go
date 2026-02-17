package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"sync"
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
	"github.com/bililive-go/bililive-go/src/livestate"
	"github.com/bililive-go/bililive-go/src/log"
	"github.com/bililive-go/bililive-go/src/metrics"
	"github.com/bililive-go/bililive-go/src/pipeline"
	"github.com/bililive-go/bililive-go/src/pipeline/stages"
	"github.com/bililive-go/bililive-go/src/pkg/events"
	"github.com/bililive-go/bililive-go/src/pkg/iostats"
	"github.com/bililive-go/bililive-go/src/pkg/kliveproxy"
	"github.com/bililive-go/bililive-go/src/pkg/launcher"
	"github.com/bililive-go/bililive-go/src/pkg/livelogger"
	"github.com/bililive-go/bililive-go/src/pkg/metadata"
	"github.com/bililive-go/bililive-go/src/pkg/openlist"
	"github.com/bililive-go/bililive-go/src/pkg/ratelimit"
	bilisentryPkg "github.com/bililive-go/bililive-go/src/pkg/sentry"
	"github.com/bililive-go/bililive-go/src/pkg/telemetry"
	"github.com/bililive-go/bililive-go/src/pkg/update"
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

// shouldRunAsLauncher 检查是否需要进入 launcher 模式
// 如果需要，运行 launcher 并返回 true
// 如果不需要或出错，返回 false（继续正常启动）
func shouldRunAsLauncher() bool {
	// 获取当前可执行文件路径
	exePath, err := os.Executable()
	if err != nil {
		fmt.Fprintf(os.Stderr, "[Launcher] 获取可执行文件路径失败: %v\n", err)
		return false
	}

	// 确定 appdata 路径
	// 优先使用配置文件中的路径，否则使用可执行文件同目录下的 .appdata
	var appDataPath string
	if *flag.Conf != "" {
		// 尝试从配置文件读取 appdata 路径
		if cfg, err := configs.NewConfigWithFile(*flag.Conf); err == nil {
			appDataPath = cfg.AppDataPath
		} else {
			fmt.Fprintf(os.Stderr, "[Launcher] 从配置文件 %s 读取 appdata 路径失败: %v\n", *flag.Conf, err)
		}
	}
	if appDataPath == "" {
		appDataPath = filepath.Join(filepath.Dir(exePath), ".appdata")
	}
	// 确保 appDataPath 是绝对路径，与 ApplyUpdateSelfHosted 写入的路径一致
	if absAppData, err := filepath.Abs(appDataPath); err == nil {
		appDataPath = absAppData
	}

	fmt.Fprintf(os.Stderr, "[Launcher] 诊断信息: exePath=%s, appDataPath=%s, appVersion=%q, flag.Conf=%q\n",
		exePath, appDataPath, consts.AppVersion, *flag.Conf)

	// 检查是否需要进入 launcher 模式
	result, err := launcher.Check(appDataPath, consts.AppVersion, exePath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[Launcher] 检查启动器状态失败: %v\n", err)
		return false
	}

	if !result.ShouldBeLauncher {
		fmt.Fprintf(os.Stderr, "[Launcher] Check 结果: ShouldBeLauncher=false (state=%+v)\n", result.State)
		return false
	}

	// 进入 launcher 模式
	fmt.Printf("[Launcher] 检测到更新版本 %s，进入启动器模式...\n", result.TargetVersion)

	// 创建启动器运行器
	instanceID := "default"
	if id := os.Getenv("BILILIVE_INSTANCE_ID"); id != "" {
		instanceID = id
	}

	runner := launcher.NewRunner(result.State, result.StatePath, result.TargetBinaryPath, instanceID)

	// 收集要传递给子进程的参数（跳过程序名）
	args := os.Args[1:]

	// 运行 launcher
	ctx := context.Background()
	if err := runner.Run(ctx, args); err != nil {
		fmt.Fprintf(os.Stderr, "[Launcher] 运行失败: %v\n", err)
		// launcher 模式运行失败，不再尝试正常启动
		// 因为用户期望运行的是更新版本
		os.Exit(1)
	}

	return true
}

var (
	// SentryDSN Sentry DSN (编译时注入，请勿在源代码中硬编码)
	// 使用 -ldflags="-X main.SentryDSN=your_dsn" 在编译时注入
	// 或设置环境变量 SENTRY_DSN
	SentryDSN = ""
	// SentryEnv Sentry Environment (编译时注入)
	SentryEnv = "production"
)

func main() {
	// 程序退出时刷新 Sentry 事件队列
	defer bilisentryPkg.Flush(2 * time.Second)
	// 捕获主 goroutine 的 panic
	defer bilisentryPkg.Recover()

	// 如果提供了 --sync-built-in-tools-to-path，则进行同步（下载容器内置工具并清理其他版本/其他工具）后退出
	if flag.SyncBuiltInToolsToPath != nil && *flag.SyncBuiltInToolsToPath != "" {
		if err := tools.SyncBuiltInTools(*flag.SyncBuiltInToolsToPath); err != nil {
			fmt.Fprint(os.Stderr, err.Error())
			os.Exit(1)
		}
		os.Exit(0)
	}

	// 如果已经是由 launcher 模式启动的，跳过 launcher 检查
	// 这通过环境变量 BILILIVE_LAUNCHER=1 来标识
	if os.Getenv("BILILIVE_LAUNCHER") == "" {
		// 检查是否需要进入 launcher 模式
		if shouldRunAsLauncher() {
			return // launcher 模式已完成执行
		}
	}

	config, err := getConfig()
	if err != nil {
		fmt.Fprint(os.Stderr, err.Error())
		os.Exit(1)
	}

	configs.SetCurrentConfig(config)

	// 初始化元数据存储（用于存储设备 ID、升级状态等关键信息）
	if err := metadata.Init(filepath.Join(config.AppDataPath, "db")); err != nil {
		fmt.Fprintf(os.Stderr, "警告: 元数据存储初始化失败: %v\n", err)
	}
	defer metadata.Close()

	// 初始化 Sentry 错误监控
	// DSN 来源优先级：编译时注入 > 环境变量 SENTRY_DSN
	sentryDSN := SentryDSN
	if sentryDSN == "" {
		sentryDSN = os.Getenv("SENTRY_DSN")
	}
	if config.Sentry.Enable && sentryDSN != "" {
		environment := SentryEnv
		// 允许 debug 模式覆盖环境配置
		if config.Debug {
			environment = "development"
		}
		if err := bilisentryPkg.Init(sentryDSN, environment, consts.AppVersion); err != nil {
			// Sentry 初始化失败不影响程序运行，仅记录警告
			fmt.Fprintf(os.Stderr, "警告: Sentry 初始化失败: %v\n", err)
		} else {
			fmt.Println("Sentry 初始化成功")
		}
	}

	// 初始化匿名遥测（用于统计各版本的使用情况）
	// 仅发送版本号、平台和架构信息，不收集任何个人数据
	// 默认启用，用户可通过 telemetry.GetInstance().SetEnabled(false) 禁用
	telemetry.Init(consts.AppVersion, true)

	inst := new(instance.Instance)
	// TODO: Replace gcache with hashmap.
	// LRU seems not necessary here.
	inst.Cache = gcache.New(4096).LRU().Build()

	// 创建可取消的根 context，所有 goroutine 都应该使用派生自此 context 的子 context
	// 这样可以通过取消根 context 来优雅地关闭所有 goroutine
	rootCtx, rootCancel := context.WithCancel(context.Background())
	ctx := context.WithValue(rootCtx, instance.Key, inst)

	logger := log.New(ctx)
	logger.Infof("%s Version: %s Link Start", consts.AppName, consts.AppVersion)

	// 发送启动统计（异步）
	telemetry.GetInstance().SendStartup(ctx)

	if config.File != "" {
		logger.Debugf("config path: %s.", config.File)
		logger.Debugf("other flags have been ignored.")
	} else {
		logger.Debugf("config file is not used.")
		logger.Debugf("flag: %s used.", os.Args)
	}
	logger.Debugf("%+v", consts.GetAppInfo())
	logger.Debugf("%+v", configs.GetCurrentConfig())

	// 初始化更新管理器并尝试连接到启动器（如果由启动器启动）
	var updateManager *update.Manager
	if os.Getenv("BILILIVE_LAUNCHER") != "" {
		logger.Info("检测到由启动器启动，正在连接到启动器...")

		// 从环境变量读取启动器的 PID 和路径，保存到全局状态
		// 用于在"系统状态"和"更新"页面展示启动器信息
		launcherPID := 0
		if pidStr := os.Getenv("BILILIVE_LAUNCHER_PID"); pidStr != "" {
			if pid, err := strconv.Atoi(pidStr); err == nil {
				launcherPID = pid
			} else {
				logger.Warnf("无法解析 BILILIVE_LAUNCHER_PID: %v", err)
			}
		}
		launcherExePath := os.Getenv("BILILIVE_LAUNCHER_EXE")
		consts.SetLauncherInfo(launcherPID, launcherExePath)
		logger.Infof("启动器信息: PID=%d, Path=%s", launcherPID, launcherExePath)

		instanceID := os.Getenv("BILILIVE_INSTANCE_ID")
		if instanceID == "" {
			instanceID = "default"
		}
		updateManager = update.NewManager(update.ManagerConfig{
			CurrentVersion: consts.AppVersion,
			InstanceID:     instanceID,
		})
		if err := updateManager.ConnectToLauncher(rootCtx); err != nil {
			logger.WithError(err).Warn("连接到启动器失败，自动更新功能将不可用")
		} else {
			logger.Info("已连接到启动器")
			// 设置关闭请求处理
			updateManager.OnShutdownRequest(func(gracePeriod int) {
				logger.Infof("收到启动器关闭请求，优雅期 %d 秒", gracePeriod)
				// 发送关闭确认
				updateManager.AckShutdown()
				// 触发主程序关闭
				rootCancel()
			})
		}
	}

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

	ed := inst.EventDispatcher.(events.Dispatcher)

	// 如果启用了云上传功能，初始化 OpenList 管理器
	var openlistManager *openlist.Manager
	if config.OnRecordFinished.CloudUpload.Enable {
		// 获取 OpenList 数据目录
		openlistDataPath := config.OpenList.DataPath
		if openlistDataPath == "" {
			openlistDataPath = filepath.Join(config.AppDataPath, "openlist")
		}
		openlistPort := config.OpenList.Port
		if openlistPort == 0 {
			openlistPort = 5244
		}

		// 创建 OpenList 管理器
		openlistManager = openlist.NewManager(openlistDataPath, openlistPort)

		// 在后台启动 OpenList
		bilisentryPkg.Go(func() {
			if err := openlistManager.Start(rootCtx); err != nil {
				logger.WithError(err).Error("启动 OpenList 失败，云上传功能将不可用")
			}
		})

		// 设置全局 OpenList 管理器供 API 和 Pipeline 使用
		servers.SetOpenListManager(openlistManager)

		logger.Info("云上传功能已启用")
	}

	// 初始化 Pipeline 管道管理器
	pipelineDbPath := filepath.Join(config.AppDataPath, "db", "pipeline.db")
	pipelineStore, err := pipeline.NewSQLiteStore(pipelineDbPath)
	if err != nil {
		logger.WithError(err).Fatal("初始化 Pipeline 数据库失败")
	}
	pipelineConfig := &pipeline.ManagerConfig{
		MaxConcurrent: config.TaskQueue.MaxConcurrent,
	}
	pipelineManager := pipeline.NewManager(ctx, pipelineStore, pipelineConfig, ed)
	// 注册所有内置阶段
	stages.RegisterBuiltinStagesToManager(pipelineManager)
	inst.PipelineManager = pipelineManager

	// 初始化直播间状态管理器
	liveStateDbPath := filepath.Join(config.AppDataPath, "db", "lives.db")
	liveStateManager, err := livestate.NewManager(liveStateDbPath)
	if err != nil {
		logger.WithError(err).Warn("初始化直播间状态管理器失败，状态持久化功能将不可用")
	} else {
		inst.LiveStateManager = liveStateManager
		inst.LiveStateStore = liveStateManager.GetStore() // 保存 store 引用供其他模块使用
		if err := liveStateManager.Start(); err != nil {
			logger.WithError(err).Warn("启动直播间状态管理器失败")
		}
	}

	// 先初始化 manager（不启动），因为 server 依赖它们
	lm := listeners.NewManager(ctx)
	rm := recorders.NewManager(ctx)

	// 尽早启动 HTTP 服务器，让用户可以快速访问 Web 界面
	// 即使 live rooms 还在初始化，用户也能看到页面
	if cfg := configs.GetCurrentConfig(); cfg != nil && cfg.RPC.Enable {
		if err = servers.NewServer(ctx).Start(ctx); err != nil {
			logger.WithError(err).Fatalf("failed to init server")
		}
		// 注册 SSE 事件监听器
		servers.RegisterSSEEventListeners(inst)
		// 注册直播间状态持久化事件监听器
		if liveStateManager != nil {
			livestate.RegisterEventListeners(ed, liveStateManager, inst.Cache)
		}
		// 设置日志回调，将日志推送到 SSE
		livelogger.SetLogCallback(func(roomID string, logLine string) {
			servers.GetSSEHub().BroadcastLog(types.LiveID(roomID), logLine)
		})
		logger.Info("HTTP server started, initializing live rooms...")

		// 通知启动器主程序已成功启动
		if updateManager != nil && updateManager.IsLauncherConnected() {
			if err := updateManager.NotifyStartup(true, "", os.Getpid()); err != nil {
				logger.WithError(err).Warn("通知启动器启动成功失败")
			} else {
				logger.Debug("已通知启动器启动成功")
			}
		}

		// 启动 klive 工具（klive 自己管理远程访问设置）
		kliveManager := kliveproxy.NewManager()
		if err := kliveManager.Start(ctx, config.RPC.Bind); err != nil {
			logger.WithError(err).Warn("启动 klive 工具失败")
		} else {
			logger.Info("klive 工具已启动")
		}

		// 启动自动更新器（如果配置启用）
		if config.Update.AutoCheck {
			servers.StartAutoUpdater(ctx)
			logger.Info("自动更新检查器已启动")
		}
	}

	// 启动 manager
	if err = lm.Start(ctx); err != nil {
		logger.Fatalf("failed to init listener manager, error: %s", err)
	}
	if err = rm.Start(ctx); err != nil {
		logger.Fatalf("failed to init recorder manager, error: %s", err)
	}

	// 启动 Pipeline 管道管理器
	if err = pipelineManager.Start(ctx); err != nil {
		logger.Fatalf("failed to init pipeline manager, error: %s", err)
	}

	if err = metrics.NewCollector(ctx).Start(ctx); err != nil {
		logger.Fatalf("failed to init metrics collector, error: %s", err)
	}

	// 初始化 IO 统计模块
	iostatsConfig := iostats.DefaultConfig()
	if iostatsModule, err := iostats.NewModule(ctx, iostatsConfig); err != nil {
		logger.WithError(err).Warn("初始化 IO 统计模块失败，统计功能将不可用")
	} else {
		inst.IOStatsModule = iostatsModule
		if err := iostatsModule.Start(ctx); err != nil {
			logger.WithError(err).Warn("启动 IO 统计模块失败")
		}

		// 设置请求状态追踪回调（从 live 包调用，避免循环依赖）
		live.SetRequestStatusCallback(func(liveID, platform string, success bool, errMsg string) {
			if success {
				iostats.TrackRequestSuccess(liveID, platform)
			} else {
				iostats.TrackRequestFailure(liveID, platform, errMsg)
			}
		})

		// 设置录制器状态提供者（用于收集录制写入速度）
		iostats.SetRecorderStatusProvider(func() []iostats.RecorderStatus {
			if inst.RecorderManager == nil {
				return nil
			}
			rm, ok := inst.RecorderManager.(recorders.Manager)
			if !ok {
				return nil
			}

			var statuses []iostats.RecorderStatus
			for liveID, l := range inst.Lives {
				status, err := rm.GetRecorderStatus(context.Background(), liveID)
				if err != nil {
					continue
				}

				rs := iostats.RecorderStatus{
					LiveID:   string(liveID),
					Platform: l.GetPlatformCNName(),
				}

				if totalSizeVal, ok := status["total_size"]; ok {
					if totalSizeStr, ok := totalSizeVal.(string); ok {
						var n int64
						fmt.Sscanf(totalSizeStr, "%d", &n)
						rs.TotalSize = n
					}
				}
				if fileSizeVal, ok := status["file_size"]; ok {
					if fileSizeStr, ok := fileSizeVal.(string); ok {
						var n int64
						fmt.Sscanf(fileSizeStr, "%d", &n)
						rs.FileSize = n
					}
				}

				statuses = append(statuses, rs)
			}
			return statuses
		})
	}

	// 初始化 live rooms
	// 第一步：立即为所有配置的直播间创建 InitializingLive，让前端可以看到
	inst.Lives = make(map[types.LiveID]live.Live)
	cfg := configs.GetCurrentConfig()

	// 确保所有平台都有最小访问限制（用于控制并行初始化时的请求速度）
	for _, room := range cfg.LiveRooms {
		platformKey := configs.GetPlatformKeyFromUrl(room.Url)
		if platformKey != "" {
			minInterval := cfg.GetPlatformMinAccessInterval(platformKey)
			ratelimit.GetGlobalRateLimiter().SetPlatformLimit(platformKey, minInterval)
		}
	}

	// 分两批处理：监听中的直播间和非监听的直播间
	var listeningRooms []live.Live
	var nonListeningRooms []live.Live

	// 创建初始化完成的回调函数
	// 当 InitializingLive.GetInfo() 成功获取真实信息时，会自动调用此回调
	onInitFinished := func(initializingLive live.Live, originalLive live.Live, info *live.Info) {
		// 触发 RoomInitializingFinished 事件，让 manager 处理后续逻辑
		ed.DispatchEvent(events.NewEvent(listeners.RoomInitializingFinished, live.InitializingFinishedParam{
			InitializingLive: initializingLive,
			Live:             originalLive,
			Info:             info,
		}))
	}

	for index := range cfg.LiveRooms {
		room := cfg.LiveRooms[index]

		// 先创建 InitializingLive，状态为初始化中，让前端立即可见
		// 传入回调函数，当 GetInfo() 成功时会自动触发事件
		l, liveErr := live.NewInitializing(ctx, &room, inst.Cache, onInitFinished)
		if liveErr != nil {
			logger.WithField("url", room).Error(liveErr.Error())
			continue
		}
		if _, ok := inst.Lives[l.GetLiveId()]; ok {
			logger.Errorf("%v is exist!", room)
			continue
		}
		inst.Lives[l.GetLiveId()] = l
		configs.SetLiveRoomId(room.Url, l.GetLiveId())

		// 从数据库加载缓存的直播间信息，用于在初始化完成前显示
		if liveStateManager != nil {
			if cachedRoom := liveStateManager.GetCachedInfo(string(l.GetLiveId())); cachedRoom != nil {
				// 1. 设置 InitializingLive 的缓存信息（用于 GetInfo 返回）
				if wrappedLive, ok := l.(*live.WrappedLive); ok {
					if setter, ok := wrappedLive.Live.(live.CachedInfoSetter); ok {
						setter.SetCachedInfo(cachedRoom.HostName, cachedRoom.RoomName)
					}
				}

				// 2. 将缓存信息存入 inst.Cache（用于 API 返回）
				cachedInfo := &live.Info{
					Live:         l,
					HostName:     cachedRoom.HostName,
					RoomName:     cachedRoom.RoomName,
					Status:       false,
					Initializing: true,
				}
				if cachedRoom.HostName != "" || cachedRoom.RoomName != "" {
					inst.Cache.Set(l, cachedInfo)
					logger.WithFields(map[string]any{
						"live_id":   l.GetLiveId(),
						"host_name": cachedRoom.HostName,
						"room_name": cachedRoom.RoomName,
					}).Debug("已加载缓存的直播间信息")
				}
			}
		}

		// 分类直播间
		if room.IsListening {
			listeningRooms = append(listeningRooms, l)
		} else {
			nonListeningRooms = append(nonListeningRooms, l)
		}
	}

	// 优先为监听中的直播间添加 Listener（它们会自动调用 GetInfo）
	for _, l := range listeningRooms {
		if err := lm.AddListener(ctx, l); err != nil {
			logger.WithFields(map[string]any{"url": l.GetRawUrl()}).Error(err)
		}
	}

	// 在后台为非监听的直播间循环请求信息（结束初始化状态）
	// 每个直播间启动一个 goroutine，这样不同平台的直播间可以并行
	// 同一平台的直播间会被平台速率限制自然串行化
	// 同一直播间的多次请求会被 WrappedLive 的调度器合并
	var initWg sync.WaitGroup
	for _, l := range nonListeningRooms {
		initWg.Add(1)
		l := l
		bilisentryPkg.GoWithContext(ctx, func(ctx context.Context) {
			defer initWg.Done()

			for {
				// 检查是否已完成初始化
				if wrappedLive, ok := l.(*live.WrappedLive); ok {
					if setter, ok := wrappedLive.Live.(live.InitializingLiveSetter); ok {
						if setter.IsFinished() {
							// 已完成初始化，退出循环
							return
						}
					}
				}

				// 使用 GetInfoWithInterval 等待间隔后发送请求
				// 由于每个直播间有自己的调度器，不同平台的直播间会并行
				// 同一平台的直播间会被平台速率限制自然串行化
				// 使用 ctx（派生自 rootCtx），当 rootCancel() 被调用时会自动取消
				_, err := l.GetInfoWithInterval(ctx)
				if err != nil {
					// 如果是 context 取消导致的错误，说明程序正在退出
					if ctx.Err() != nil {
						return
					}
					logger.WithFields(map[string]any{"url": l.GetRawUrl()}).Warn("failed to initialize non-listening room: " + err.Error())
					// 继续重试
					continue
				}

				// GetInfo 成功后，InitializingLive 会自动触发回调完成初始化
				// 检查是否真的完成了初始化
				if wrappedLive, ok := l.(*live.WrappedLive); ok {
					if setter, ok := wrappedLive.Live.(live.InitializingLiveSetter); ok {
						if setter.IsFinished() {
							// 初始化完成，退出循环
							return
						}
					}
				}
				// 如果还没完成，继续循环重试
			}
		})
	}

	// 在另一个 goroutine 中等待所有初始化完成
	bilisentryPkg.Go(func() {
		initWg.Wait()
		logger.Info("all non-listening rooms initialized")
	})

	logger.Infof("Created %d live rooms (%d listening, %d not listening)",
		len(inst.Lives), len(listeningRooms), len(nonListeningRooms))

	c := make(chan os.Signal, 1)
	// 使用 os.Interrupt 更跨平台，在 Windows 上 SIGHUP 可能不被支持
	signal.Notify(c, os.Interrupt, syscall.SIGTERM)
	msgChan := c

	// 注册关闭回调，供更新系统在热重启时触发优雅关闭
	servers.SetShutdownFunc(func() {
		c <- os.Interrupt
	})
	bilisentryPkg.Go(func() {
		<-msgChan
		logger.Info("Received shutdown signal, closing...")
		// 取消根 context，这会导致所有派生的 context 被取消
		// 包括：WrappedLive 的调度器、非监听直播间的初始化循环等
		rootCancel()
		// 关闭 HTTP 服务器
		if cfg := configs.GetCurrentConfig(); cfg != nil && cfg.RPC.Enable {
			inst.Server.Close(ctx)
		}
		// 关闭管理器
		inst.ListenerManager.Close(ctx)
		inst.RecorderManager.Close(ctx)
		// 关闭 Pipeline 管道管理器
		if inst.PipelineManager != nil {
			inst.PipelineManager.Close(ctx)
		}
		// 关闭直播间状态管理器
		if liveStateManager != nil {
			liveStateManager.Close()
		}
		// 关闭 IO 统计模块
		if inst.IOStatsModule != nil {
			inst.IOStatsModule.Close(ctx)
		}
		// 关闭 OpenList 管理器
		if openlistManager != nil {
			openlistManager.Stop()
		}
		// 停止自动更新器
		servers.StopAutoUpdater()
		logger.Info("Shutdown complete")
	})

	inst.WaitGroup.Wait()

	// 检查是否需要就地切换到 launcher 模式
	// 如果用户在前端点击了"立即更新"，doApplyUpdate 会设置此标志并触发服务关闭
	// 所有 bgo 服务已关闭（端口释放），但进程仍然存活
	// 现在重新执行 launcher 检查：launcher-state.json 已经被更新，
	// shouldRunAsLauncher() 会启动 launcher.Runner 来运行新版本 bgo 子进程
	// launcher.Runner.Run() 阻塞运行，直到新版本退出后才返回
	// 这样进程不会退出，Docker 容器不会重启
	if servers.PendingLauncherTransition() {
		logger.Infof("====== 版本切换: 所有服务已关闭，正在进入 Launcher 模式 (AppDataPath=%s) ======", configs.GetCurrentConfig().AppDataPath)
		// 取消根 context，确保日志文件句柄被关闭（避免新版本清理时文件冲突）
		rootCancel()
		// 在进入 launcher 模式前，终止所有子进程（btools、klive 等）并关闭 remotetools WebUI
		// 确保端口被释放，否则新版本 bgo 启动时会遇到 EADDRINUSE
		tools.Cleanup()

		// 如果当前进程是由父 Launcher 启动的（BILILIVE_LAUNCHER=1），
		// 不能自己再变成 launcher——否则两个 launcher 会争抢同一个 Named Pipe。
		// 正确做法：直接退出，让父 Launcher 的 Run() 循环重新读取
		// launcher-state.json 并启动新版本。
		if os.Getenv("BILILIVE_LAUNCHER") == "1" {
			logger.Info("由父 Launcher 管理，直接退出，由父 Launcher 启动新版本")
			return
		}

		// 独立运行模式（非 launcher 子进程）：自己变成 launcher
		if shouldRunAsLauncher() {
			logger.Info("Launcher 模式执行完毕")
			return
		}
		logger.Warn("Launcher 检查未通过，程序即将退出")
	}

	logger.Info("Bye~")
}
