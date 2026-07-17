package tools

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/bililive-go/bililive-go/src/configs"
	blog "github.com/bililive-go/bililive-go/src/log"
	bilisentry "github.com/bililive-go/bililive-go/src/pkg/sentry"
	"github.com/bililive-go/bililive-go/src/pkg/utils"
	"github.com/tidwall/gjson"

	"github.com/kira1928/remotetools/pkg/tools"
)

type toolStatusValue int32

const (
	toolStatusValueNotInitialized toolStatusValue = iota
	toolStatusValueInitializing
	toolStatusValueInitialized
)

var currentToolStatus atomic.Int32

// toolsInitDone 在 Init() 首次完成（无论成功或失败）时关闭，之后永远可读。
// toolsInitError 记录最新一次 Init() 的结果，在 Init() 重试成功后会被更新为 nil。
// 读写 toolsInitError 以及关闭 toolsInitDone 均在 toolsInitMu 下进行，
// 确保 WaitForToolsInit 始终读到对应调用时刻最新的结果。
var (
	toolsInitDone   = make(chan struct{})
	toolsInitMu     sync.Mutex
	toolsInitClosed bool
	toolsInitError  error
)

func signalToolsReady(err error) {
	toolsInitMu.Lock()
	defer toolsInitMu.Unlock()
	toolsInitError = err // 每次都更新，包括重试成功后覆盖旧的失败错误
	if !toolsInitClosed {
		toolsInitClosed = true
		close(toolsInitDone)
	}
}

// WaitForToolsInit 等待 Init() 完成，ctx 取消时返回错误。
// 若 Init() 曾失败后又被重试，此函数会等待当前这一轮重试结束后再返回其结果，
// 避免在重试进行中读到上一轮的旧错误。
func WaitForToolsInit(ctx context.Context) error {
	select {
	case <-toolsInitDone:
	case <-ctx.Done():
		return ctx.Err()
	}
	// toolsInitDone 关闭后可能有新一轮 Init() 重试正在进行（currentToolStatus 重新
	// 进入 initializing），此时轮询等待本轮结束，确保读到的是最新一轮的结果。
	// Init() 的 defer 中先调用 signalToolsReady 更新 toolsInitError、再更新
	// currentToolStatus，因此观察到状态离开 initializing 时错误值一定已是本轮的。
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for toolStatusValue(currentToolStatus.Load()) == toolStatusValueInitializing {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
	toolsInitMu.Lock()
	err := toolsInitError
	toolsInitMu.Unlock()
	return err
}

// FFmpegStatus FFmpeg 就绪状态
type FFmpegStatus struct {
	State   string `json:"state"`             // checking / downloading / ready / not_found / error
	Message string `json:"message,omitempty"` // 人读消息
	Source  string `json:"source,omitempty"`  // system / remotetools
}

var (
	ffmpegStatusVal atomic.Value // stores FFmpegStatus
	// ffmpegStatusMu 串行化 setFFmpegStatus 的整个流程（存储、变化通知、回调执行），
	// 保证并发更新时订阅者（如 SSE 广播）看到的状态顺序与实际更新顺序一致
	ffmpegStatusMu  sync.Mutex
	ffmpegCbMu      sync.RWMutex
	ffmpegCallbacks []func(FFmpegStatus)

	// ffmpegStatusChangedCh 在每次状态变化时被关闭并重建，供等待方感知状态变化
	ffmpegStatusChangedMu sync.Mutex
	ffmpegStatusChangedCh = make(chan struct{})

	// ffmpegInitRunning 确保同一时间只有一个 FFmpegAsyncInit 流程在运行，
	// 避免并发调用（如 e2e 测试多次触发 reinit）导致并发 tool.Install()
	ffmpegInitRunning atomic.Bool
)

func init() {
	ffmpegStatusVal.Store(FFmpegStatus{State: "checking"})
}

func setFFmpegStatus(s FFmpegStatus) {
	ffmpegStatusMu.Lock()
	defer ffmpegStatusMu.Unlock()

	ffmpegStatusVal.Store(s)
	ffmpegStatusChangedMu.Lock()
	close(ffmpegStatusChangedCh)
	ffmpegStatusChangedCh = make(chan struct{})
	ffmpegStatusChangedMu.Unlock()
	ffmpegCbMu.RLock()
	cbs := make([]func(FFmpegStatus), len(ffmpegCallbacks))
	copy(cbs, ffmpegCallbacks)
	ffmpegCbMu.RUnlock()
	for _, cb := range cbs {
		// 逐个回调隔离 panic：回调是对外暴露的扩展点（OnFFmpegStatusChange），
		// 单个订阅者（如 SSE 广播）panic 不应打崩整个进程或阻断其余订阅者
		func(cb func(FFmpegStatus)) {
			defer bilisentry.Recover()
			cb(s)
		}(cb)
	}
}

// GetFFmpegStatus 返回当前 FFmpeg 状态
func GetFFmpegStatus() FFmpegStatus {
	v := ffmpegStatusVal.Load()
	if v == nil {
		return FFmpegStatus{State: "checking"}
	}
	return v.(FFmpegStatus)
}

// IsFFmpegReady 返回 FFmpeg 是否已就绪
func IsFFmpegReady() bool {
	return GetFFmpegStatus().State == "ready"
}

// FFmpegStatusChanged 返回一个在下一次 FFmpeg 状态变化时关闭的 channel。
// 配合 GetFFmpegStatus 使用时应先取 channel 再读状态，以避免漏掉两步之间的变化。
func FFmpegStatusChanged() <-chan struct{} {
	ffmpegStatusChangedMu.Lock()
	defer ffmpegStatusChangedMu.Unlock()
	return ffmpegStatusChangedCh
}

// WaitFFmpegAsyncInitDone 在 FFmpeg 异步初始化仍在进行（checking/downloading）时阻塞，
// 直到其进入终态（ready/not_found/error）。用于录制、后处理等依赖 FFmpeg 的路径
// 在后台下载完成后立即继续，而非陷入失败重试循环。
// stop 可为 nil，用于额外的中断信号（如 parser 的 Stop()）。
// 到达终态返回 nil（是否可用由调用方随后的路径查找决定，错误语义与原有逻辑一致）；
// 因 ctx 取消或 stop 关闭而中断时返回相应错误。
func WaitFFmpegAsyncInitDone(ctx context.Context, stop <-chan struct{}) error {
	logged := false
	for {
		// 先取变化通知 channel 再读状态，避免漏掉两步之间的状态变化
		changed := FFmpegStatusChanged()
		state := GetFFmpegStatus().State
		if state != "checking" && state != "downloading" {
			return nil
		}
		if !logged {
			blog.GetLogger().Infof("FFmpeg 尚未就绪（%s），等待后台初始化完成...", state)
			logged = true
		}
		select {
		case <-changed:
		case <-ctx.Done():
			return ctx.Err()
		case <-stop: // stop 为 nil 时此分支永远不会触发
			return errors.New("等待 FFmpeg 就绪时被中断")
		}
	}
}

// OnFFmpegStatusChange 注册 FFmpeg 状态变化回调，在状态改变时被调用
func OnFFmpegStatusChange(fn func(FFmpegStatus)) {
	ffmpegCbMu.Lock()
	ffmpegCallbacks = append(ffmpegCallbacks, fn)
	ffmpegCbMu.Unlock()
}

// ForceFFmpegStatus 强制设置 FFmpeg 状态，仅用于测试目的。
func ForceFFmpegStatus(s FFmpegStatus) {
	setFFmpegStatus(s)
}

// FFmpegAsyncInit 异步检测并（按需）下载 FFmpeg，期间通过状态回调通知进度。
// 可在 WebUI 启动后立即调用，不会阻塞主流程。
// 并发调用时只有第一个调用生效，其余直接返回（避免并发 Install）。
func FFmpegAsyncInit(ctx context.Context) {
	if !ffmpegInitRunning.CompareAndSwap(false, true) {
		return
	}
	setFFmpegStatus(FFmpegStatus{State: "checking"})
	bilisentry.GoWithContext(ctx, func(ctx context.Context) {
		defer ffmpegInitRunning.Store(false)

		// 配置显式指定了 ffmpeg_path 时以其为准：实际录制查找（utils.GetFFmpegPath）
		// 对非空配置路径只做存在性检查、不回退 remotetools/PATH，
		// 因此路径无效时下载也无济于事，直接提示用户修正配置
		if cfg := configs.GetCurrentConfig(); cfg != nil && cfg.FfmpegPath != "" {
			// 需为存在且非目录的文件：目录无法执行，os.Stat 成功也不代表可用，
			// 否则会误报 ready，实际录制时才失败
			if fi, err := os.Stat(cfg.FfmpegPath); err == nil && !fi.IsDir() {
				blog.GetLogger().Infof("FFmpeg found at configured path: %s", cfg.FfmpegPath)
				setFFmpegStatus(FFmpegStatus{State: "ready", Source: "system"})
			} else {
				blog.GetLogger().Warnf("configured ffmpeg_path does not exist or is a directory: %s", cfg.FfmpegPath)
				setFFmpegStatus(FFmpegStatus{
					State:   "not_found",
					Message: "配置的 ffmpeg_path 不存在或不是文件: " + cfg.FfmpegPath,
				})
			}
			return
		}

		// 检查系统 PATH（含 ./ffmpeg 兜底），不触碰 remotetools（此时 Init() 尚未完成）
		if _, err := utils.LookupSystemFFmpeg(); err == nil {
			blog.GetLogger().Info("FFmpeg found in system PATH")
			setFFmpegStatus(FFmpegStatus{State: "ready", Source: "system"})
			return
		}

		// 触发 remotetools 初始化（可能是 no-op，但确保 goroutine 在跑）
		_ = Init()

		// 等待 Init() 真正完成。WaitForToolsInit 在 Init() 失败时返回该错误，
		// 在 ctx 取消时返回 ctx.Err()，需分别处理。
		if err := WaitForToolsInit(ctx); err != nil {
			if ctx.Err() != nil {
				return // 程序退出，不更新状态
			}
			// Init() 本身失败（如配置读取错误或 remotetools WebUI 启动失败）
			blog.GetLogger().WithError(err).Warn("remotetools init failed, FFmpeg not available")
			setFFmpegStatus(FFmpegStatus{
				State:   "not_found",
				Message: "FFmpeg 未找到，且 remotetools 初始化失败: " + err.Error(),
			})
			return
		}

		api := tools.Get()
		if api == nil {
			setFFmpegStatus(FFmpegStatus{State: "not_found", Message: "未找到 FFmpeg"})
			return
		}

		tool, err := api.GetTool("ffmpeg")
		if err != nil {
			setFFmpegStatus(FFmpegStatus{State: "not_found", Message: "无法获取 FFmpeg 工具信息: " + err.Error()})
			return
		}

		if tool.DoesToolExist() {
			blog.GetLogger().Infof("FFmpeg found from remotetools: %s", tool.GetToolPath())
			setFFmpegStatus(FFmpegStatus{State: "ready", Source: "remotetools"})
			return
		}

		// 需要下载；程序若已在退出流程中则不再发起耗时下载
		if ctx.Err() != nil {
			return
		}
		source := tool.GetInstallSource()
		blog.GetLogger().Infof("FFmpeg not found locally, downloading from %s...", source)
		setFFmpegStatus(FFmpegStatus{
			State:   "downloading",
			Message: "正在下载 FFmpeg...",
			Source:  source,
		})

		if err := tool.Install(); err != nil {
			blog.GetLogger().WithError(err).Error("FFmpeg download failed")
			setFFmpegStatus(FFmpegStatus{
				State:   "error",
				Message: "FFmpeg 下载失败: " + err.Error(),
			})
			return
		}

		blog.GetLogger().Infof("FFmpeg downloaded successfully: %s", tool.GetToolPath())
		setFFmpegStatus(FFmpegStatus{State: "ready", Source: "remotetools"})
	})
}

// bililive-tools 状态跟踪
type btoolsStatusValue int32

const (
	BToolsStatusNotStarted btoolsStatusValue = iota
	BToolsStatusStarting
	BToolsStatusReady
	BToolsStatusFailed
)

var currentBToolsStatus atomic.Int32

// IsBToolsReady 检查 bililive-tools 是否已就绪
func IsBToolsReady() bool {
	return btoolsStatusValue(currentBToolsStatus.Load()) == BToolsStatusReady
}

// IsBToolsStarting 检查 bililive-tools 是否正在启动
func IsBToolsStarting() bool {
	status := btoolsStatusValue(currentBToolsStatus.Load())
	return status == BToolsStatusStarting || status == BToolsStatusNotStarted
}

// Cleanup 关闭所有通过 tools 包管理的资源：
// - 终止所有已注册的子进程（btools、klive 等）
// - 关闭 remotetools WebUI 服务器
// 在进入 launcher 模式前调用，确保端口被释放以供新版本使用。
func Cleanup() {
	logger := blog.GetLogger()

	// 1. 终止所有已注册的子进程
	KillAllProcesses()

	// 2. 关闭 remotetools WebUI 服务器
	api := tools.Get()
	if api != nil {
		if err := api.StopWebUI(); err != nil {
			logger.Warnf("关闭 RemoteTools WebUI 失败: %v", err)
		} else {
			logger.Info("RemoteTools WebUI 已关闭")
		}
	}
}

// DownloaderAvailability 包含各下载器的可用状态
type DownloaderAvailability struct {
	FFmpegAvailable           bool   `json:"ffmpeg_available"`
	FFmpegPath                string `json:"ffmpeg_path,omitempty"`
	NativeAvailable           bool   `json:"native_available"` // 内置解析器永远可用
	BililiveRecorderAvailable bool   `json:"bililive_recorder_available"`
	BililiveRecorderPath      string `json:"bililive_recorder_path,omitempty"`
}

// GetDownloaderAvailability 返回所有下载器的可用状态
func GetDownloaderAvailability() DownloaderAvailability {
	result := DownloaderAvailability{
		NativeAvailable: true, // 内置解析器永远可用
	}

	// 检查 FFmpeg —— 复用 utils.GetFFmpegPath 保持与录制器实际使用的查找逻辑一致
	// （配置文件指定路径 → remotetools → 系统 PATH）
	if ffmpegPath, err := utils.GetFFmpegPath(context.Background()); err == nil {
		result.FFmpegAvailable = true
		result.FFmpegPath = ffmpegPath
	}

	// 检查 BililiveRecorder CLI
	api := tools.Get()
	if api != nil {
		dotnet, err := api.GetTool("dotnet")
		if err == nil && dotnet.DoesToolExist() {
			recorder, err := api.GetTool("bililive-recorder-cli")
			if err == nil && recorder.DoesToolExist() {
				result.BililiveRecorderAvailable = true
				result.BililiveRecorderPath = recorder.GetToolPath()
			}
		}
	}

	return result
}

func AsyncInit() {
	bilisentry.Go(func() {
		err := Init()
		if err != nil {
			blog.GetLogger().Errorln("Failed to initialize RemoteTools:", err)
		}
	})
}

func SyncBuiltInTools(targetToolFolder string) (err error) {
	// 初始化 remotetools API 配置，避免未加载配置时获取工具失败
	api := tools.Get()
	if api == nil {
		return errors.New("failed to get remotetools API instance")
	}
	cfgData, cfgErr := getConfigData()
	if cfgErr != nil || cfgData == nil {
		if cfgErr == nil {
			cfgErr = errors.New("failed to get config data")
		}
		return cfgErr
	}
	if err = api.LoadConfigFromBytes(cfgData); err != nil {
		return err
	}

	tools.SetRootFolder(targetToolFolder)
	toolsToKeep := []tools.Tool{}
	for _, toolName := range []string{
		"ffmpeg",
		"dotnet",
		"bililive-recorder",
		"node",
		"biliLive-tools",
	} {
		var tool tools.Tool
		tool, err = api.GetTool(toolName)
		if err != nil {
			blog.GetLogger().WithError(err).Warn("failed to get built-in tool:", toolName)
			continue
		}
		if !tool.DoesToolExist() {
			blog.GetLogger().Infoln("Installing built-in tool:", toolName)
			err = tool.Install()
			if err != nil {
				return err
			}
		}
		blog.GetLogger().Infoln("Built-in tool is ready:", toolName, "version:", tool.GetVersion())
		toolsToKeep = append(toolsToKeep, tool)
	}

	_, err = api.DeleteAllExceptToolsInRoot(toolsToKeep)
	if err != nil {
		blog.GetLogger().WithError(err).Warn("failed to clean up unused built-in tools")
		return
	}
	blog.GetLogger().Infoln("Built-in tools synchronized to", targetToolFolder)

	return err
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
		// 先发布本轮结果再更新状态机，保证 WaitForToolsInit 观察到
		// currentToolStatus 离开 initializing 时 toolsInitError 已是本轮的值
		signalToolsReady(err)
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

	// 配置只读工具目录（若有），并设置可写工具目录
	if ro := strings.TrimSpace(appConfig.ReadOnlyToolFolder); ro != "" {
		tools.SetReadOnlyRootFolders([]string{ro})
	}

	preferredWritable := strings.TrimSpace(appConfig.ToolRootFolder)
	if preferredWritable == "" {
		preferredWritable = filepath.Join(appConfig.AppDataPath, "external_tools")
	}

	// 始终使用持久化目录作为存储目录（即便其不可执行），运行时由 remotetools 复制到临时目录执行
	if mkErr := os.MkdirAll(preferredWritable, 0o755); mkErr != nil {
		blog.GetLogger().WithError(mkErr).Warnf("无法创建工具目录 %s，外部工具功能可能受限", preferredWritable)
		logDirectoryPermissionDiagnostics(preferredWritable)
	}
	tools.SetRootFolder(preferredWritable)
	// 为不可执行场景指定临时执行目录（容器内目录，具备执行权限）
	execTmp := filepath.Join(string(os.PathSeparator), "opt", "bililive", "tmp_for_exec")
	if mkErr := os.MkdirAll(execTmp, 0o755); mkErr != nil {
		blog.GetLogger().WithError(mkErr).Warnf("无法创建临时执行目录 %s，某些外部工具可能无法运行", execTmp)
		logDirectoryPermissionDiagnostics(execTmp)
	}
	tools.SetTmpRootFolderForExecPermission(execTmp)

	err = api.StartWebUI(0)
	if err != nil {
		return
	}
	blog.GetLogger().Infoln("RemoteTools Web UI started")

	for _, toolName := range []string{
		"dotnet",
		"bililive-recorder",
	} {
		AsyncDownloadIfNecessary(toolName)
	}
	bilisentry.Go(func() {
		err := startBTools()
		if err != nil {
			blog.GetLogger().WithError(err).Errorln("Failed to start bililive-tools")
		}
	})
	bilisentry.Go(func() {
		if cfg := configs.GetCurrentConfig(); cfg != nil && cfg.RPC.Enable {
			startScheduler()
		}
	})

	return nil
}

func startBTools() error {
	// 设置状态为正在启动
	currentBToolsStatus.Store(int32(BToolsStatusStarting))

	// bililive-tools 依赖 node 环境
	err := DownloadIfNecessary("node")
	if err != nil {
		currentBToolsStatus.Store(int32(BToolsStatusFailed))
		return fmt.Errorf("failed to install node: %w", err)
	}
	api := tools.Get()
	if api == nil {
		currentBToolsStatus.Store(int32(BToolsStatusFailed))
		return errors.New("failed to get remotetools API instance")
	}

	node, err := api.GetTool("node")
	if err != nil {
		currentBToolsStatus.Store(int32(BToolsStatusFailed))
		return err
	}
	if !node.DoesToolExist() {
		err = node.Install()
		if err != nil {
			currentBToolsStatus.Store(int32(BToolsStatusFailed))
			return err
		}
	}

	btools, err := api.GetTool("biliLive-tools")
	if err != nil {
		currentBToolsStatus.Store(int32(BToolsStatusFailed))
		return err
	}
	if !btools.DoesToolExist() {
		err = btools.Install()
		if err != nil {
			currentBToolsStatus.Store(int32(BToolsStatusFailed))
			return err
		}
	}

	nodeFolder := filepath.Dir(node.GetToolPath())
	btoolsFolder := filepath.Dir(btools.GetToolPath())
	env := []string{
		"PATH=" + nodeFolder + string(os.PathListSeparator) + os.Getenv("PATH"),
	}
	nodePath, err := filepath.Abs(node.GetToolPath())
	if err != nil {
		currentBToolsStatus.Store(int32(BToolsStatusFailed))
		return err
	}
	cmd := exec.Command(
		nodePath,
		"./index.cjs",
		"server",
		"-c",
		"./appConfig.json",
	)
	cmd.Dir = btoolsFolder
	cmd.Env = env
	// 动态决定是否输出，保留错误信息
	cmd.Stdout = utils.NewDebugControlledWriter(os.Stdout)
	cmd.Stderr = utils.NewLogFilterWriter(os.Stderr)

	blog.GetLogger().Infoln("Starting bililive-tools server…")

	// 在 Windows 下使用 Job Object，确保主进程退出时子进程被一并终止
	// 使用 runWithKillOnCloseAndGetPID 来获取进程 PID
	err = runWithKillOnCloseAndGetPID(cmd, func(pid int) {
		// 使用通用的进程跟踪器注册子进程
		RegisterProcess("bililive-tools", pid, ProcessCategoryBTools)
		blog.GetLogger().Infof("bililive-tools process started with PID: %d", pid)
		if readyErr := waitForBToolsHTTPReady(15 * time.Second); readyErr != nil {
			currentBToolsStatus.Store(int32(BToolsStatusFailed))
			blog.GetLogger().WithError(readyErr).Error("bililive-tools server did not become ready")
			if cmd.Process != nil {
				_ = cmd.Process.Kill()
			}
			return
		}
		currentBToolsStatus.Store(int32(BToolsStatusReady))
		blog.GetLogger().Info("bililive-tools server is ready")
	})
	if err != nil {
		currentBToolsStatus.Store(int32(BToolsStatusFailed))
	}
	return err
}

func waitForBToolsHTTPReady(timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	client := &http.Client{Timeout: time.Second}
	endpoint := "http://127.0.0.1:18110/"
	var lastErr error
	for time.Now().Before(deadline) {
		resp, err := client.Get(endpoint)
		if err == nil {
			_, _ = io.Copy(io.Discard, resp.Body)
			_ = resp.Body.Close()
			if resp.StatusCode >= http.StatusOK && resp.StatusCode < http.StatusInternalServerError {
				return nil
			}
			lastErr = fmt.Errorf("unexpected status: %s", resp.Status)
		} else {
			lastErr = err
		}
		time.Sleep(200 * time.Millisecond)
	}
	if lastErr == nil {
		lastErr = context.DeadlineExceeded
	}
	return lastErr
}

func AsyncDownloadIfNecessary(toolName string) {
	bilisentry.Go(func() {
		err := DownloadIfNecessary(toolName)
		if err != nil {
			blog.GetLogger().Errorln("Failed to download", toolName, "tool:", err)
		}
	})
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
	blog.GetLogger().Infoln(toolName, "tool is ready to use, version:", tool.GetVersion())
	return nil
}

func GetWebUIPort() int {
	return tools.Get().GetWebUIPort()
}

var schedulerPort atomic.Int32

func GetSchedulerPort() int {
	return int(schedulerPort.Load())
}

func startScheduler() {
	api := tools.Get()
	if api == nil {
		blog.GetLogger().Errorln("Failed to get remotetools API instance for scheduler")
		return
	}

	if err := DownloadIfNecessary("bililive-scheduler"); err != nil {
		blog.GetLogger().WithError(err).Errorln("Failed to download bililive-scheduler")
		return
	}

	tool, err := api.GetTool("bililive-scheduler")
	if err != nil {
		blog.GetLogger().WithError(err).Errorln("Failed to get bililive-scheduler tool")
		return
	}

	cfg := configs.GetCurrentConfig()
	bind := cfg.RPC.Bind
	if bind == "" {
		bind = ":8080"
	}
	// Extract port from bind address (":8080" → "8080", "0.0.0.0:8080" → "8080")
	host, port, err := net.SplitHostPort(bind)
	if err != nil {
		// Fallback: try treating bind as ":port"
		host, port, err = net.SplitHostPort(":" + bind)
		if err != nil {
			blog.GetLogger().WithError(err).Errorln("Failed to parse RPC bind address for scheduler")
			return
		}
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "localhost"
	}
	apiURL := "http://" + net.JoinHostPort(host, port)
	dbPath := filepath.Join(cfg.AppDataPath, "db", "scheduler.db")

	// Clean up stale port file from previous run
	portFile := filepath.Join(filepath.Dir(dbPath), "scheduler.port")
	os.Remove(portFile)

	cmd := exec.Command(
		tool.GetToolPath(),
		"--api-url", apiURL,
		"--db-path", dbPath,
		"--port", "0",
	)
	cmd.Stdout = utils.NewDebugControlledWriter(os.Stdout)
	cmd.Stderr = utils.NewLogFilterWriter(os.Stderr)

	blog.GetLogger().Infoln("Starting bililive-scheduler...")

	// done channel signals when the port-polling goroutine should stop
	done := make(chan struct{})

	schedulerRegistered := false
	defer func() {
		if schedulerRegistered {
			UnregisterProcess("bililive-scheduler")
		}
	}()

	if err := runWithKillOnCloseAndGetPID(cmd, func(pid int) {
		RegisterProcess("bililive-scheduler", pid, ProcessCategoryScheduler)
		schedulerRegistered = true
		blog.GetLogger().Infof("bililive-scheduler started with PID: %d", pid)

		// Poll port file with cancellation support
		go func() {
			for i := 0; i < 30; i++ {
				select {
				case <-done:
					return
				case <-time.After(1 * time.Second):
				}
				data, err := os.ReadFile(portFile)
				if err != nil {
					continue
				}
				var p int
				if _, err := fmt.Sscanf(string(data), "%d", &p); err == nil && p > 0 {
					schedulerPort.Store(int32(p))
					blog.GetLogger().Infof("bililive-scheduler listening on port %d", p)
					return
				}
			}
			blog.GetLogger().Warnln("Failed to read bililive-scheduler port file")
		}()
	}); err != nil {
		close(done)
		blog.GetLogger().WithError(err).Errorln("Failed to start bililive-scheduler")
		schedulerPort.Store(0)
		return
	}

	// Process exited - clean up
	close(done)
	schedulerPort.Store(0)
	blog.GetLogger().Warnln("bililive-scheduler process exited")
}

func Get() *tools.API {
	return tools.Get()
}

func FixFlvByBililiveRecorder(ctx context.Context, fileName string) (outputFiles []string, err error) {
	return FixFlvByBililiveRecorderWithPID(ctx, fileName, nil)
}

// FixFlvByBililiveRecorderWithPID 使用 BililiveRecorder 修复 FLV 文件
// onPID 可选回调函数，在子进程启动后立即调用，传递子进程 PID
func FixFlvByBililiveRecorderWithPID(ctx context.Context, fileName string, onPID func(pid int)) (outputFiles []string, err error) {
	defer func() {
		if err != nil {
			blog.GetLogger().WithError(err).Warn("failed to fix flv file by bililive-recorder")
		}
	}()

	outputFiles = []string{fileName}

	// 仅处理 .flv 文件，其他类型直接跳过
	if strings.ToLower(filepath.Ext(fileName)) != ".flv" {
		return
	}

	api := tools.Get()
	if api == nil {
		err = errors.New("failed to get remotetools API instance")
		return
	}

	dotnet, err := api.GetTool("dotnet")
	if err != nil {
		return
	}
	if !dotnet.DoesToolExist() {
		err = errors.New("dotnet tool not exist")
		return
	}

	bililiveRecorder, err := api.GetTool("bililive-recorder")
	if err != nil {
		return
	}
	if !bililiveRecorder.DoesToolExist() {
		return
	}

	var cmd *exec.Cmd
	cmd, err = dotnet.CreateExecuteCmd(
		bililiveRecorder.GetToolPath(),
		"tool",
		"fix",
		fileName,
		fileName,
		"--json-indented",
	)
	if err != nil {
		return
	}

	// 使用 cmd.Start() 非阻塞启动，以便获取 PID
	stdout, pipeErr := cmd.StdoutPipe()
	if pipeErr != nil {
		err = fmt.Errorf("failed to create stdout pipe: %w", pipeErr)
		return
	}

	if err = cmd.Start(); err != nil {
		return
	}

	// 回调通知调用方子进程 PID
	if onPID != nil && cmd.Process != nil {
		onPID(cmd.Process.Pid)
	}

	// 读取 stdout
	out, readErr := io.ReadAll(stdout)
	if readErr != nil {
		// 仍需等待进程结束
		cmd.Wait()
		err = fmt.Errorf("failed to read stdout: %w", readErr)
		return
	}

	// 等待进程结束
	if err = cmd.Wait(); err != nil {
		return
	}

	outJson := gjson.ParseBytes(out)
	if !outJson.Exists() {
		err = fmt.Errorf("bililive-recorder returned no json: %s", string(out))
		return
	}
	if status := outJson.Get("Status").String(); strings.ToUpper(status) != "OK" {
		err = fmt.Errorf("bililive-recorder failed: %s", string(out))
		return
	}

	// 原始文件尺寸
	origStat, statErr := os.Stat(fileName)
	if statErr != nil {
		err = fmt.Errorf("stat original file failed: %w", statErr)
		return
	}
	origSize := origStat.Size()

	dir := filepath.Dir(fileName)
	base := strings.TrimSuffix(filepath.Base(fileName), filepath.Ext(fileName))
	ext := filepath.Ext(fileName)

	// 获取输出文件列表：优先使用 JSON 数组 Data.OutputFiles；没有则按命名规则回退
	var outFiles []string
	if of := outJson.Get("Data.OutputFiles"); of.Exists() {
		for _, v := range of.Array() {
			p := v.String()
			if p == "" {
				continue
			}
			if !filepath.IsAbs(p) {
				p = filepath.Join(dir, p)
			}
			outFiles = append(outFiles, p)
		}
	} else {
		cnt := int(outJson.Get("Data.OutputFileCount").Int())
		for i := 1; i <= cnt; i++ {
			name := fmt.Sprintf("%s.fix_p%03d%s", base, i, ext)
			outFiles = append(outFiles, filepath.Join(dir, name))
		}
	}

	if len(outFiles) == 0 {
		err = fmt.Errorf("no output files were generated for %s", fileName)
		return
	}

	// 计算输出文件总大小；若有任何不存在，则按失败处理
	var total int64
	var missing []string
	for _, f := range outFiles {
		st, e := os.Stat(f)
		if e != nil {
			if os.IsNotExist(e) {
				missing = append(missing, f)
				continue
			}
			// 其他错误也视为失败
			missing = append(missing, f+" ("+e.Error()+")")
			continue
		}
		total += st.Size()
	}

	if len(missing) > 0 {
		// 有缺失的分段，清理已生成的分段并报错
		for _, f := range outFiles {
			_ = os.Remove(f)
		}
		err = fmt.Errorf("some output parts are missing: %v", missing)
		return
	}

	// 判定：分段总和 >= 原始大小的 90%
	if total*10 >= origSize*9 {
		// 成功：删除原始文件
		if remErr := os.Remove(fileName); remErr != nil {
			blog.GetLogger().WithError(remErr).Warnf("failed to remove original file: %s", fileName)
		}
		// 重命名输出文件, 去掉中间的 .fix_p 部分
		// 如果输出文件只有一个，则直接使用原文件名
		if len(outFiles) == 1 {
			os.Rename(outFiles[0], fileName)
		} else {
			outputFiles = []string{}
			for _, f := range outFiles {
				newName := strings.ReplaceAll(f, ".fix_p", "")
				os.Rename(f, newName)
				outputFiles = append(outputFiles, newName)
			}
		}
		return
	}

	// 失败：删除输出分段，并返回错误
	for _, f := range outFiles {
		_ = os.Remove(f)
	}
	err = fmt.Errorf("sum of fixed parts (%d) < 90%% of original (%d)", total, origSize)
	return
}
