//go:generate go run go.uber.org/mock/mockgen -package recorders -destination mock_test.go github.com/bililive-go/bililive-go/src/recorders Recorder,Manager
package recorders

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"text/template"
	"time"

	"github.com/bluele/gcache"
	"github.com/sirupsen/logrus"

	"github.com/bililive-go/bililive-go/src/configs"
	"github.com/bililive-go/bililive-go/src/instance"
	"github.com/bililive-go/bililive-go/src/listeners"
	"github.com/bililive-go/bililive-go/src/live"
	"github.com/bililive-go/bililive-go/src/notify"
	"github.com/bililive-go/bililive-go/src/pipeline"
	"github.com/bililive-go/bililive-go/src/pkg/diagnostics"
	"github.com/bililive-go/bililive-go/src/pkg/events"
	"github.com/bililive-go/bililive-go/src/pkg/hlsproxy"
	"github.com/bililive-go/bililive-go/src/pkg/livelogger"
	"github.com/bililive-go/bililive-go/src/pkg/parser"
	"github.com/bililive-go/bililive-go/src/pkg/parser/bililive_recorder"
	"github.com/bililive-go/bililive-go/src/pkg/parser/ffmpeg"
	"github.com/bililive-go/bililive-go/src/pkg/parser/native/flv"
	bilisentry "github.com/bililive-go/bililive-go/src/pkg/sentry"
	"github.com/bililive-go/bililive-go/src/pkg/streamprobe"
	"github.com/bililive-go/bililive-go/src/pkg/utils"
	"github.com/bililive-go/bililive-go/src/recorders/danmaku"
	"github.com/bililive-go/bililive-go/src/tools"
)

const (
	begin uint32 = iota
	pending
	running
	stopped
)

const soopRetryWarnInterval = time.Minute

// for test
var (
	// newParser 根据配置的下载器类型创建 parser，并实现回退逻辑：
	// bililive-recorder -> ffmpeg -> native
	newParser = func(u *url.URL, downloaderType configs.DownloaderType, cfg map[string]string, logger *livelogger.LiveLogger) (parser.Parser, error) {
		// 判断是否为 FLV 流
		isFLV := strings.Contains(u.Path, ".flv")

		// 根据下载器类型选择 parser，并实现回退逻辑
		parserName := resolveParserName(downloaderType, isFLV, logger)

		return parser.New(parserName, cfg, logger)
	}

	mkdir = func(path string) error {
		return os.MkdirAll(path, os.ModePerm)
	}

	removeEmptyFile = func(file string) {
		if stat, err := os.Stat(file); err == nil && stat.Size() == 0 {
			os.Remove(file)
		}
	}
)

// videoExtensions 用于匹配弹幕文件对应的视频文件
var videoExtensions = []string{".flv", ".mkv", ".ts", ".mp4"}

// cleanupOrphanedDanmakuFiles 清理没有对应视频文件的 ASS 弹幕文件。
// 视频流快速失败时（如 404），弹幕录制器可能已创建 .ass 文件但视频未生成，
// 遗留的孤立 .ass 文件会在前端显示为无效录制，需要清理。
func cleanupOrphanedDanmakuFiles(assFile string) {
	if assFile == "" {
		return
	}
	dir := filepath.Dir(assFile)
	base := strings.TrimSuffix(filepath.Base(assFile), ".ass")

	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		ext := strings.ToLower(filepath.Ext(name))
		if ext == ".ass" {
			assBase := strings.TrimSuffix(name, ".ass")
			if assBase != base && !strings.HasPrefix(assBase, base+"_PART") {
				continue
			}
			// 检查是否有同名视频文件
			hasVideo := false
			for _, vext := range videoExtensions {
				if _, err := os.Stat(filepath.Join(dir, assBase+vext)); err == nil {
					hasVideo = true
					break
				}
			}
			if !hasVideo {
				os.Remove(filepath.Join(dir, name))
			}
		}
	}
}

// findBililiveRecorderOutputFiles 查找录播姬生成的分段文件
// 录播姬的输出文件命名模式: {原文件名}_PART{3位序号}{扩展名}
// 例如: video.flv -> video_PART000.flv, video_PART001.flv, ...
// 注意：不使用 filepath.Glob，因为方括号 [] 在 glob 中是特殊字符
func findBililiveRecorderOutputFiles(expectedFileName string) []string {
	dir := filepath.Dir(expectedFileName)
	base := filepath.Base(expectedFileName)
	ext := filepath.Ext(base)
	nameWithoutExt := strings.TrimSuffix(base, ext)

	// 读取目录中的所有文件
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}

	// 文件名前缀: {nameWithoutExt}_PART
	prefix := nameWithoutExt + "_PART"

	// 过滤符合 {nameWithoutExt}_PARTXXX{ext} 格式的文件
	var validFiles []string
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		// 检查扩展名是否匹配
		if !strings.HasSuffix(name, ext) {
			continue
		}
		// 移除扩展名后检查前缀
		nameNoExt := strings.TrimSuffix(name, ext)
		if !strings.HasPrefix(nameNoExt, prefix) {
			continue
		}
		// 检查后缀是否为3位数字
		suffix := strings.TrimPrefix(nameNoExt, prefix)
		if len(suffix) == 3 {
			if _, err := strconv.Atoi(suffix); err == nil {
				validFiles = append(validFiles, filepath.Join(dir, name))
			}
		}
	}

	// 排序文件（按文件名字母顺序）
	if len(validFiles) > 1 {
		sort.Strings(validFiles)
	}

	return validFiles
}

// resolveParserName 根据下载器类型返回实际使用的 parser 名称
// 实现回退逻辑：bililive-recorder -> ffmpeg -> native
func resolveParserName(downloaderType configs.DownloaderType, isFLV bool, logger *livelogger.LiveLogger) string {
	switch downloaderType {
	case configs.DownloaderBililiveRecorder:
		// BililiveRecorder 只支持 FLV 流
		if isFLV && bililive_recorder.IsAvailable() {
			return bililive_recorder.Name
		}
		// 回退到 ffmpeg
		if logger != nil {
			if !isFLV {
				logger.Info("BililiveRecorder 不支持非 FLV 流，回退到 ffmpeg")
			} else {
				logger.Info("BililiveRecorder 工具不可用，回退到 ffmpeg")
			}
		}
		fallthrough

	case configs.DownloaderFFmpeg:
		// 检查 ffmpeg 是否可用（通过尝试获取路径）
		// 如果 ffmpeg 不可用，则回退到 native（仅限 FLV）
		if isFLV {
			// 对于 FLV 流，如果 ffmpeg 不可用，可以回退到 native
			return ffmpeg.Name
		}
		return ffmpeg.Name

	case configs.DownloaderNative:
		// Native parser 仅支持 FLV
		if isFLV {
			return flv.Name
		}
		// 非 FLV 流使用 ffmpeg
		if logger != nil {
			logger.Info("原生 FLV 解析器不支持非 FLV 流，使用 ffmpeg")
		}
		return ffmpeg.Name

	default:
		// 默认使用 ffmpeg
		return ffmpeg.Name
	}
}

func getDefaultFileNameTmpl() *template.Template {
	cfg := configs.GetCurrentConfig()
	return template.Must(template.New("filename").Funcs(utils.GetFuncMap(cfg)).
		Parse(`{{ .Live.GetPlatformCNName }}/{{ with .Live.GetOptions.NickName }}{{ . | filenameFilter }}{{ else }}{{ .HostName | filenameFilter }}{{ end }}/[{{ now | date "2006-01-02 15-04-05"}}][{{ .HostName | filenameFilter }}][{{ .RoomName | filenameFilter }}].flv`))
}

type Recorder interface {
	Start(ctx context.Context) error
	StartTime() time.Time
	GetStatus() (map[string]interface{}, error)
	Close()
	// GetParserPID 获取当前 parser 进程的 PID
	// 如果 parser 未启动或不支持 PID 获取，返回 0
	GetParserPID() int
	// IsRecording 返回当前是否正在实际录制（输出文件已有数据写入）
	// 与 HasRecorder 不同：HasRecorder 只要 recorder 存在就返回 true（包括重试等待中），
	// IsRecording 仅在输出文件实际写入数据后才返回 true（排除 ffmpeg 因 404 等原因秒退的情况）
	IsRecording() bool
	// RequestSegment 请求在下一个关键帧处分段
	// 仅在使用 FLV 代理时有效
	// 返回 true 表示请求已接受，false 表示不支持或请求被拒绝
	RequestSegment() bool
	// HasFlvProxy 检查当前是否使用 FLV 代理
	HasFlvProxy() bool
	// CloseForRestart 用于分段重启场景：关闭 recorder 但不推送摘要，
	// 等待 run() 完全退出后返回已累积的录制文件列表
	CloseForRestart() []notify.RecordingFileDetail
	// SetInitialRecordedFiles 设置初始录制文件列表（从上一个 recorder 继承）
	SetInitialRecordedFiles(files []notify.RecordingFileDetail)
}

// danmakuRecorder 弹幕录制器接口，支持不同平台的实现
type danmakuRecorder interface {
	Start(ctx context.Context) error
	Stop()
	OutputFile() string
	GetCount() int
	IsRunning() bool
	GetStatus() map[string]interface{}
	SetBroadcastCallback(cb danmaku.DanmakuBroadcastCallback)
}

// 编译期接口断言
var (
	_ danmakuRecorder = (*danmaku.DanmakuRecorder)(nil)
	_ danmakuRecorder = (*danmaku.DouyinDanmakuRecorder)(nil)
	_ danmakuRecorder = (*danmaku.DouyuDanmakuRecorder)(nil)
)

type recorder struct {
	Live       live.Live
	ed         events.Dispatcher
	cache      gcache.Cache
	startTime  time.Time
	parser     parser.Parser
	parserLock *sync.RWMutex
	danmakuRec danmakuRecorder

	stop  chan struct{}
	state uint32

	// startReady 是 Start 的发布屏障：只有 running 状态及 RecorderStart 已经
	// 完整发布，或 Close 抢先把 pending 取消且 Start 已确认不再发布时才关闭。
	// Close 在产生 RecorderStop/返回前等待它，防止 Stop→Start 反序和迟到事件。
	startReady     chan struct{}
	startReadyOnce sync.Once
	startEventSent atomic.Bool

	// 诊断关联 ID 只使用本次 run 的随机 HMAC 和随机序号，不把原始直播间 URL
	// 或输出路径写入调查包。
	roomScopeID string
	sessionID   string
	attemptSeq  atomic.Uint64

	// 当前录制文件信息
	currentFileLock sync.RWMutex
	currentFilePath string

	// 当前录制的流信息（来自平台 API）
	currentStreamInfo *live.AvailableStreamInfo

	// 当前录制使用的原始流 URL 和 Headers（供调试和前端展示）
	currentStreamURL     string
	currentStreamHeaders map[string]string

	// 实际流头部信息（来自 StreamProbe 探测）
	actualStreamInfo atomic.Pointer[streamprobe.StreamHeaderInfo]

	// 累积的录制文件信息，待录制结束后统一推送摘要
	// recordedFilesMu 保护 recordedFiles 的并发访问：
	// run() goroutine 中的 accumulateRecordedFiles 和 RestartRecorder 中的
	// SetInitialRecordedFiles 可能并发操作此 slice
	recordedFilesMu sync.Mutex
	recordedFiles   []notify.RecordingFileDetail

	// done 在 run() 退出时关闭，用于 CloseForRestart 等待 goroutine 完成
	done chan struct{}
	// runCancel 只取消本 recorder 的主任务。Close 必须在检查/停止 parser 前
	// 先调用它，覆盖“Close 看到 parser=nil，tryRecord 随后才安装 parser”的窗口。
	runCancelMu sync.Mutex
	runCancel   context.CancelFunc
	// auxWg 跟踪由 run 派生、但生命周期可能短暂超过一次 tryRecord 的探测和
	// 持久化任务。run 的 done 关闭后不会再 Add，因此 Close 可以安全 Wait。
	auxWg sync.WaitGroup
	// closeOnce 让每个 Close 调用都共享同一个同步关闭屏障。
	closeOnce sync.Once
	stopOnce  sync.Once
	doneOnce  sync.Once
	// suppressSummary 为 true 时，run() 退出不推送摘要（分段重启场景）
	suppressSummary bool

	retryLogMu              sync.Mutex
	lastRetryLogKey         string
	lastRetryLogAt          time.Time
	suppressedRetryLogCount int
}

func NewRecorder(ctx context.Context, live live.Live) (Recorder, error) {
	inst := instance.GetInstance(ctx)
	roomScopeID := diagnostics.ScopeID(live.GetRawUrl())
	result := &recorder{
		Live:        live,
		cache:       inst.Cache,
		startTime:   time.Now(),
		ed:          inst.EventDispatcher.(events.Dispatcher),
		state:       begin,
		stop:        make(chan struct{}),
		done:        make(chan struct{}),
		startReady:  make(chan struct{}),
		parserLock:  new(sync.RWMutex),
		roomScopeID: roomScopeID,
		sessionID:   diagnostics.NewID("recorder_session"),
	}
	diagnostics.Record(ctx, "recorder.session.created", diagnostics.Fields{
		"component":     "recorder",
		"lane":          "recorder",
		"room_scope_id": roomScopeID,
		"session_id":    result.sessionID,
		"status":        "created",
	})
	return result, nil
}

func (r *recorder) diagnosticFields() diagnostics.Fields {
	return diagnostics.Fields{
		"component":     "recorder",
		"lane":          "recorder",
		"room_scope_id": r.roomScopeID,
		"session_id":    r.sessionID,
	}
}

func diagnosticErrorType(err error) string {
	if err == nil {
		return ""
	}
	return fmt.Sprintf("%T", err)
}

func recorderStateName(state uint32) string {
	switch state {
	case begin:
		return "begin"
	case pending:
		return "pending"
	case running:
		return "running"
	case stopped:
		return "stopped"
	default:
		return "unknown"
	}
}

func (r *recorder) tryRecord(ctx context.Context) {
	attemptNumber := r.attemptSeq.Add(1)
	attemptID := diagnostics.NewID("stream_attempt")
	attemptFields := r.diagnosticFields()
	attemptFields["attempt_id"] = attemptID
	attemptFields["attempt_number"] = attemptNumber
	ctx = diagnostics.WithFields(ctx, attemptFields)
	ctx, endAttempt := diagnostics.NewTask(ctx, "recorder.stream_attempt", attemptFields)
	defer endAttempt(diagnostics.Fields{"status": "returned"})
	diagnostics.Record(ctx, "recorder.stream_attempt.start", diagnostics.Fields{
		"status": "started",
	})

	// 每次重试前重置探测状态，避免上次录制的旧数据残留
	// （例如上次探测成功但本次流分辨率已变化）
	r.actualStreamInfo.Store(nil)

	cfg := configs.GetCurrentConfig()

	// 获取层级配置
	platformKey := configs.GetPlatformKeyFromUrl(r.Live.GetRawUrl())
	room, roomErr := cfg.GetLiveRoomByUrl(r.Live.GetRawUrl())
	if roomErr != nil {
		// 如果找不到房间配置，使用空的房间配置
		room = &configs.LiveRoom{Url: r.Live.GetRawUrl()}
	}
	resolvedConfig := cfg.ResolveConfigForRoom(room, platformKey)

	var streamInfos []*live.StreamUrlInfo
	var err error
	resolveCtx, endResolve := diagnostics.StartSpan(ctx, "stream.resolve", diagnostics.Fields{
		"component": "stream",
		"lane":      "stream",
		"platform":  platformKey,
	})
	if streamInfos, err = r.Live.GetStreamInfos(); err == live.ErrNotImplemented {
		var urls []*url.URL
		// TODO: remove deprecated method GetStreamUrls
		//nolint:staticcheck
		if urls, err = r.Live.GetStreamUrls(); err == live.ErrNotImplemented {
			panic("GetStreamInfos and GetStreamUrls are not implemented for " + r.Live.GetPlatformCNName())
		} else if err == nil {
			streamInfos = utils.GenUrlInfos(urls, make(map[string]string))
		}
	}
	resolveStatus := "ok"
	if err != nil {
		resolveStatus = "error"
	} else if len(streamInfos) == 0 {
		resolveStatus = "empty"
	}
	endResolve(diagnostics.Fields{
		"status":          resolveStatus,
		"candidate_count": len(streamInfos),
		"error_type":      diagnosticErrorType(err),
	})
	if err != nil || len(streamInfos) == 0 {
		diagnostics.Record(resolveCtx, "recorder.stream_attempt.retry", diagnostics.Fields{
			"component":   "recorder",
			"lane":        "recorder",
			"severity":    "warn",
			"status":      resolveStatus,
			"retry_in_ms": 5000,
			"error_type":  diagnosticErrorType(err),
		})
		if err != nil && r.stopRetryForExplicitOffline(ctx, err) {
			return
		}
		r.logStreamURLRetry(err)
		// 使用可中断的等待，确保 Ctrl+C 能立即响应
		select {
		case <-ctx.Done():
		case <-r.stop:
		case <-time.After(5 * time.Second):
		}
		return
	}
	r.resetRetryLogState()

	obj, _ := r.cache.Get(r.Live)
	info := obj.(*live.Info)

	tmpl := getDefaultFileNameTmpl()
	// 使用层级配置的 OutputTmpl
	if resolvedConfig.OutputTmpl != "" {
		_tmpl, errTmpl := template.New("user_filename").Funcs(utils.GetFuncMap(cfg)).Parse(resolvedConfig.OutputTmpl)
		if errTmpl == nil {
			tmpl = _tmpl
		}
	}

	buf := new(bytes.Buffer)
	if err = tmpl.Execute(buf, info); err != nil {
		r.getLogger().WithError(err).Error("failed to render filename, recording aborted")
		return
	}
	// 使用层级配置的 OutPutPath
	fileName := filepath.Join(resolvedConfig.OutPutPath, buf.String())
	outputPath, _ := filepath.Split(fileName)

	// TODO 根据配置选择最佳流
	streamInfo := r.selectPreferredStream(streamInfos)
	r.saveCurrentStreamInfo(streamInfo)
	// 更新可用流信息到 info（用于API展示）
	r.updateAvailableStreams(ctx, info, streamInfos)

	url := streamInfo.Url

	// 保存原始流 URL 和 Headers（供前端调试展示）
	r.currentFileLock.Lock()
	r.currentStreamURL = url.String()
	r.currentStreamHeaders = streamInfo.HeadersForDownloader
	r.currentFileLock.Unlock()

	if strings.Contains(url.Path, "m3u8") {
		fileName = fileName[:len(fileName)-4] + ".ts"
	}

	if info.AudioOnly {
		fileName = fileName[:strings.LastIndex(fileName, ".")] + ".aac"
	}

	if err = mkdir(outputPath); err != nil {
		r.getLogger().WithError(err).Errorf("failed to create output path[%s]", outputPath)
		return
	}
	parserCfg := map[string]string{
		"timeout_in_us": strconv.Itoa(resolvedConfig.TimeoutInUs),
		"audio_only":    strconv.FormatBool(info.AudioOnly),
	}
	// 使用层级配置的下载器类型
	downloaderType := resolvedConfig.Feature.GetEffectiveDownloaderType()

	// 如果启用了 FLV 代理分段且使用 FFmpeg 下载器，传递配置
	if resolvedConfig.Feature.EnableFlvProxySegment && downloaderType == configs.DownloaderFFmpeg {
		parserCfg["use_flv_proxy"] = "true"
	}

	// 预测本次录制最终使用的 parser：除显式选择 ffmpeg 外，bililive-recorder（工具不可用
	// 或非 FLV 流）与 native（非 FLV 流）在回退后同样会使用 ffmpeg。传 nil logger 避免与
	// 后续 newParser 内部的回退日志重复。
	// 若最终会用 ffmpeg，则先按当前直播间的层级配置验证路径；房间/平台级
	// ffmpeg_path 可能在全局 FFmpeg 状态为 not_found 时仍然可用，不能用全局状态
	// 直接否决。只有当前直播间实际取不到 FFmpeg 且后台仍在 checking/downloading
	// 时才等待；终态后仍取不到则直接返回，不再连接上游 / 启动 StreamProbe，避免
	// FFmpeg 缺失或下载失败时每 5 秒重试都触碰直播源、触发平台限流。
	if resolveParserName(downloaderType, strings.Contains(url.Path, ".flv"), nil) == ffmpeg.Name {
		_, ffmpegPathErr := utils.GetFFmpegPathForLive(ctx, r.Live)
		ffmpegState := tools.GetFFmpegStatus().State
		diagnostics.Record(ctx, "dependency.ffmpeg.state", diagnostics.Fields{
			"component":     "dependency",
			"lane":          "stream",
			"room_scope_id": r.roomScopeID,
			"required":      true,
			"tool_state":    ffmpegState,
			"available":     ffmpegPathErr == nil,
			"error_type":    diagnosticErrorType(ffmpegPathErr),
		})
		if ffmpegPathErr != nil && resolvedConfig.FfmpegPath != "" {
			r.getLogger().WithError(ffmpegPathErr).Warn("配置的 FFmpeg 路径不可用，本次录制跳过")
			return
		}
		if ffmpegPathErr != nil && (ffmpegState == "checking" || ffmpegState == "downloading") {
			waitCtx, endWait := diagnostics.StartSpan(ctx, "dependency.ffmpeg.wait", diagnostics.Fields{
				"component":     "dependency",
				"lane":          "stream",
				"room_scope_id": r.roomScopeID,
				"tool_state":    ffmpegState,
				"reason":        "recording_prerequisite",
			})
			waitErr := tools.WaitFFmpegAsyncInitDone(waitCtx, r.stop)
			waitStatus := "ok"
			if waitErr != nil {
				waitStatus = "interrupted"
			}
			endWait(diagnostics.Fields{
				"status":     waitStatus,
				"error_type": diagnosticErrorType(waitErr),
			})
			if waitErr != nil {
				r.getLogger().WithError(waitErr).Warn("等待 FFmpeg 就绪被中断，放弃本次录制")
				return
			}
			_, ffmpegPathErr = utils.GetFFmpegPathForLive(waitCtx, r.Live)
		}
		if ffmpegPathErr != nil {
			r.getLogger().WithError(ffmpegPathErr).Warn("FFmpeg 不可用，本次录制跳过，等待下次重试")
			return
		}
	}

	// StreamProbe 探测：仅对 FLV 流使用代理探测
	// HLS 是分段 HTTP 请求协议，无法通过单一 HTTP 代理转发
	//
	// 保存原始流 URL：后续代理启动后 url 变量会被替换为 localhost 代理地址（路径固定为 /stream），
	// 但 newParser 内部通过 URL 路径判断是否为 FLV 流来选择下载器类型。
	// 如果用代理 URL 判断，所有 FLV 流都会被误判为"非 FLV"，导致 Native/录播姬下载器回退到 ffmpeg。
	originalURL := url
	isFLV := streamprobe.IsStreamFLV(url)
	if isFLV {
		probeTraceCtx, endProbe := diagnostics.StartSpan(ctx, "stream.probe", diagnostics.Fields{
			"component":   "stream",
			"lane":        "stream",
			"stream_kind": "flv",
		})
		// FLV 流：启动探测代理
		probeConfig := streamprobe.Config{
			UpstreamURL: url,
			Headers:     streamInfo.HeadersForDownloader,
			OnProbed: func(info *streamprobe.StreamHeaderInfo) {
				endProbe(diagnostics.Fields{
					"status":       "ok",
					"probe_status": info.ProbeStatus(),
				})
				r.actualStreamInfo.Store(info)
				r.getLogger().Infof("流探测完成: 编码=%s, 分辨率=%s, 帧率=%.1f, 状态=%s",
					info.VideoCodec, info.Resolution(), info.FrameRate, info.ProbeStatus())
			},
			OnProbeError: func(err error, msg string) {
				endProbe(diagnostics.Fields{
					"status":     "error",
					"error_type": diagnosticErrorType(err),
				})
				if err != nil {
					r.getLogger().Warnf("流探测警告: %s: %v", msg, err)
				} else {
					r.getLogger().Warnf("流探测警告: %s", msg)
				}
				// 探测出错时也设置状态，避免永远显示"探测中"
				r.actualStreamInfo.CompareAndSwap(nil, &streamprobe.StreamHeaderInfo{
					Unsupported:    true,
					UnsupportedMsg: fmt.Sprintf("流探测失败: %s", msg),
				})
			},
			Logger: r.getLogger(),
		}

		probe := streamprobe.New(probeConfig)
		if probeErr := probe.Start(probeTraceCtx); probeErr != nil {
			endProbe(diagnostics.Fields{
				"status":     "start_error",
				"error_type": diagnosticErrorType(probeErr),
			})
			// 探测代理启动失败不应影响录制，回退到直连上游
			r.getLogger().WithError(probeErr).Warn("流探测代理启动失败，将直接连接上游")
			r.actualStreamInfo.Store(&streamprobe.StreamHeaderInfo{
				Unsupported:    true,
				UnsupportedMsg: fmt.Sprintf("流探测代理启动失败: %v", probeErr),
			})
		} else {
			// 代理启动成功，用代理 URL 替换原始 URL
			defer func() {
				probe.Stop()
				endProbe(diagnostics.Fields{"status": "stopped_before_probe"})
			}()
			streamInfo = &live.StreamUrlInfo{
				Url:                  probe.LocalURL(),
				HeadersForDownloader: nil, // 本地代理不需要 headers
				Format:               streamInfo.Format,
				Quality:              streamInfo.Quality,
				Description:          streamInfo.Description,
				Codec:                streamInfo.Codec,
				Width:                streamInfo.Width,
				Height:               streamInfo.Height,
				Bitrate:              streamInfo.Bitrate,
				Vbitrate:             streamInfo.Vbitrate,
				FrameRate:            streamInfo.FrameRate,
			}
			url = probe.LocalURL()
		}
	} else if streamprobe.IsStreamHLS(url) {
		if r.Live.GetPlatformCNName() == "SOOP" {
			hlsFilterProxy, proxyErr := hlsproxy.New(url, streamInfo.HeadersForDownloader, true)
			if proxyErr != nil {
				r.getLogger().WithError(proxyErr).Warn("Soop HLS 过滤代理启动失败，将直接使用上游 m3u8")
			} else if proxyErr = hlsFilterProxy.Start(ctx); proxyErr != nil {
				r.getLogger().WithError(proxyErr).Warn("Soop HLS 过滤代理运行失败，将直接使用上游 m3u8")
			} else {
				defer hlsFilterProxy.Stop()
				streamInfo = &live.StreamUrlInfo{
					Url:                       hlsFilterProxy.LocalURL(),
					HeadersForDownloader:      nil,
					Format:                    streamInfo.Format,
					Quality:                   streamInfo.Quality,
					Description:               streamInfo.Description,
					Codec:                     streamInfo.Codec,
					Width:                     streamInfo.Width,
					Height:                    streamInfo.Height,
					Bitrate:                   streamInfo.Bitrate,
					Vbitrate:                  streamInfo.Vbitrate,
					FrameRate:                 streamInfo.FrameRate,
					Name:                      streamInfo.Name,
					AudioCodec:                streamInfo.AudioCodec,
					AttributesForStreamSelect: streamInfo.AttributesForStreamSelect,
				}
				url = hlsFilterProxy.LocalURL()
				r.getLogger().Info("Soop HLS 过滤代理已启用，将自动跳过 preloading 分片")
			}
		}

		// HLS 流：不使用代理，异步探测第一个 TS 分段的头部信息
		// 使用 tryRecord 的 ctx，当录制结束/重试时自动取消探测
		hlsTraceCtx, endHLSProbe := diagnostics.StartSpan(ctx, "stream.probe", diagnostics.Fields{
			"component":   "stream",
			"lane":        "stream",
			"stream_kind": "hls",
		})
		r.goTracked(hlsTraceCtx, func(probeCtx context.Context) {
			hlsInfo, probeErr := streamprobe.ProbeHLS(probeCtx, url, streamInfo.HeadersForDownloader, r.getLogger())
			if probeErr != nil {
				// context 取消不算真正的错误，不需要打印
				if probeCtx.Err() != nil {
					endHLSProbe(diagnostics.Fields{"status": "cancelled"})
					return
				}
				endHLSProbe(diagnostics.Fields{
					"status":     "error",
					"error_type": diagnosticErrorType(probeErr),
				})
				r.getLogger().Warnf("HLS 流探测失败: %v", probeErr)
				// 探测失败也设置一个状态，避免永远 pending
				r.actualStreamInfo.Store(&streamprobe.StreamHeaderInfo{
					Unsupported:    true,
					UnsupportedMsg: fmt.Sprintf("HLS 探测失败: %v", probeErr),
				})
				return
			}
			endHLSProbe(diagnostics.Fields{
				"status":       "ok",
				"probe_status": hlsInfo.ProbeStatus(),
			})
			r.actualStreamInfo.Store(hlsInfo)
			r.getLogger().Infof("HLS 流探测完成: 编码=%s, 分辨率=%s, 帧率=%.1f",
				hlsInfo.VideoCodec, hlsInfo.Resolution(), hlsInfo.FrameRate)
		})
	} else {
		// 其他格式：标记为不支持
		r.actualStreamInfo.Store(&streamprobe.StreamHeaderInfo{
			Unsupported:    true,
			UnsupportedMsg: "该流格式暂不支持头部数据探测",
		})
	}

	// 使用原始 URL 而非代理 URL 来判断下载器类型
	// 代理 URL 路径为 /stream，无法正确判断是否为 FLV 流
	parserName := resolveParserName(downloaderType, isFLV, nil)
	parserInitCtx, endParserInit := diagnostics.StartSpan(ctx, "parser.init", diagnostics.Fields{
		"component": "parser",
		"lane":      "stream",
		"parser":    parserName,
		"stream_kind": func() string {
			if isFLV {
				return "flv"
			}
			return "non_flv"
		}(),
	})
	p, err := newParser(originalURL, downloaderType, parserCfg, r.getLogger())
	if err != nil {
		endParserInit(diagnostics.Fields{
			"status":     "error",
			"error_type": diagnosticErrorType(err),
		})
		r.getLogger().WithError(err).Error("failed to init parse")
		return
	}
	endParserInit(diagnostics.Fields{"status": "ok"})
	if !r.setAndCloseParser(p) {
		diagnostics.Record(parserInitCtx, "parser.start.skipped", diagnostics.Fields{
			"component": "parser",
			"lane":      "stream",
			"parser":    parserName,
			"status":    "recorder_stopped",
		})
		return
	}
	r.startTime = time.Now()

	// 弹幕录制（支持哔哩哔哩、抖音、斗鱼平台）
	if resolvedConfig.DanmakuEnable {
		switch r.Live.GetPlatformCNName() {
		case "哔哩哔哩":
			r.startDanmakuRecorder(ctx, fileName, "哔哩哔哩", resolvedConfig,
				func(rid, cookies, assFile string, cfg configs.DanmakuConfig, logger *logrus.Entry) danmakuRecorder {
					return danmaku.NewDanmakuRecorder(extractRoomIDFromUrl(r.Live.GetRawUrl()), cookies, assFile, cfg, logger)
				})
		case "抖音":
			r.startDanmakuRecorder(ctx, fileName, "抖音", resolvedConfig,
				func(rid, cookies, assFile string, cfg configs.DanmakuConfig, logger *logrus.Entry) danmakuRecorder {
					return danmaku.NewDouyinDanmakuRecorder(rid, cookies, assFile, cfg, logger)
				})
		case "斗鱼":
			r.startDanmakuRecorder(ctx, fileName, "斗鱼", resolvedConfig,
				func(rid, cookies, assFile string, cfg configs.DanmakuConfig, logger *logrus.Entry) danmakuRecorder {
					return danmaku.NewDouyuDanmakuRecorder(rid, cookies, assFile, cfg, logger)
				})
		}
	} else {
		// 弹幕未启用，清理旧的录制器
		r.currentFileLock.Lock()
		old := r.danmakuRec
		r.danmakuRec = nil
		r.currentFileLock.Unlock()
		if old != nil {
			old.Stop()
		}
	}

	// 设置当前录制文件路径
	r.setCurrentFilePath(fileName)
	segmentID := diagnostics.ScopeID(fileName)
	diagnostics.Record(parserInitCtx, "segment.open", diagnostics.Fields{
		"component":     "segment",
		"lane":          "file",
		"room_scope_id": r.roomScopeID,
		"session_id":    r.sessionID,
		"attempt_id":    attemptID,
		"segment_id":    segmentID,
		"parser":        parserName,
		"status":        "observing",
		"file_type":     strings.TrimPrefix(strings.ToLower(filepath.Ext(fileName)), "."),
	})

	r.getLogger().Debugln("Start ParseLiveStream(" + url.String() + ", " + fileName + ")")
	parseCtx, endParserRun := diagnostics.StartSpan(ctx, "parser.run", diagnostics.Fields{
		"component":  "parser",
		"lane":       "stream",
		"parser":     parserName,
		"segment_id": segmentID,
	})
	parserRunEnded := false
	defer func() {
		if !parserRunEnded {
			endParserRun(diagnostics.Fields{"status": "interrupted"})
		}
	}()
	diagnostics.Record(parseCtx, "parser.start", diagnostics.Fields{
		"component":  "parser",
		"lane":       "stream",
		"parser":     parserName,
		"segment_id": segmentID,
		"status":     "started",
	})
	observerCtx, stopObserver := context.WithCancel(parseCtx)
	defer stopObserver()
	observerDone := make(chan struct{})
	bilisentry.GoWithContext(observerCtx, func(observerCtx context.Context) {
		defer close(observerDone)
		r.observeFirstNonzeroFile(observerCtx, fileName, segmentID, parserName, attemptID)
	})
	err = p.ParseLiveStream(parseCtx, streamInfo, r.Live, fileName)
	stopObserver()
	<-observerDone
	parserStatus := "ok"
	if err != nil {
		parserStatus = "error"
	}
	endParserRun(diagnostics.Fields{
		"status":     parserStatus,
		"error_type": diagnosticErrorType(err),
	})
	parserRunEnded = true

	// 清除当前录制文件路径
	r.setCurrentFilePath("")

	// 停止弹幕录制
	r.currentFileLock.RLock()
	dmRec := r.danmakuRec
	r.currentFileLock.RUnlock()
	dmFile := ""
	if dmRec != nil {
		dmFile = dmRec.OutputFile()
		dmRec.Stop()
	}

	if err != nil {
		r.getLogger().WithError(err).Error("failed to parse live stream")
		// 视频流快速失败时（如 404），清理没有对应视频文件的残留弹幕
		if elapsed := time.Since(r.startTime); elapsed < 5*time.Second {
			cleanupOrphanedDanmakuFiles(dmFile)
		}
		return
	}

	// 录制成功，累积弹幕文件
	if dmFile != "" {
		if fi, dmErr := os.Stat(dmFile); dmErr == nil && fi.Size() > 0 {
			r.accumulateRecordedFiles(dmFile)
		}
	}

	r.getLogger().Debugln("End ParseLiveStream(" + url.String() + ", " + fileName + ")")
	removeEmptyFile(fileName)

	// 使用层级配置的 OnRecordFinished
	cmdStr := strings.Trim(resolvedConfig.OnRecordFinished.CustomCommandline, "")
	if len(cmdStr) > 0 {
		// 累积录制文件信息（legacy 路径），待录制结束后统一推送摘要
		r.accumulateRecordedFiles(fileName)

		ffmpegPath := ""
		// legacy custom_commandline 只有在模板确实引用 .Ffmpeg 时才需要等待 / 查找
		// FFmpeg；纯上传、通知等无关命令不应被首次启动的 FFmpeg 下载阻塞。
		if strings.Contains(cmdStr, ".Ffmpeg") {
			// FFmpeg 可能仍在后台异步下载（如 native 下载器录制的短直播刚结束时），
			// 等待其就绪后再查找，避免后处理命令因 FFmpeg 未下载完成而失败。
			// tryRecord 的 ctx 继承自应用全局 Context，单个录制器被停止时不会取消，
			// 只有 r.stop 会关闭；故传入 r.stop 作为中断信号，避免下载卡住时协程无法优雅退出
			if waitErr := tools.WaitFFmpegAsyncInitDone(ctx, r.stop); waitErr != nil {
				r.getLogger().WithError(waitErr).Warn("等待 FFmpeg 就绪被中断，跳过后处理命令")
				return
			}
			var ffmpegErr error
			ffmpegPath, ffmpegErr = utils.GetFFmpegPathForLive(ctx, r.Live)
			if ffmpegErr != nil {
				r.getLogger().WithError(ffmpegErr).Error("failed to find ffmpeg")
				return
			}
		}
		customTmpl, errCmdTmpl := template.New("custom_commandline").Funcs(utils.GetFuncMap(cfg)).Parse(cmdStr)
		if errCmdTmpl != nil {
			r.getLogger().WithError(errCmdTmpl).Error("custom commandline parse failure")
			return
		}

		buf := new(bytes.Buffer)
		if execErr := customTmpl.Execute(buf, struct {
			*live.Info
			FileName string
			Ffmpeg   string
		}{
			Info:     info,
			FileName: fileName,
			Ffmpeg:   ffmpegPath,
		}); execErr != nil {
			r.getLogger().WithError(execErr).Errorln("failed to render custom commandline")
			return
		}
		bash := ""
		args := []string{}
		switch runtime.GOOS {
		case "linux":
			bash = "sh"
			args = []string{"-c"}
		case "windows":
			bash = "cmd"
			args = []string{"/C"}
		default:
			r.getLogger().Warnln("Unsupport system ", runtime.GOOS)
		}
		args = append(args, buf.String())
		r.getLogger().Debugf("start executing custom_commandline: %s", args[1])
		cmd := exec.Command(bash, args...)
		// 跟随全局 Debug 开关输出
		cmd.Stdout = utils.NewDebugControlledWriter(os.Stdout)
		cmd.Stderr = utils.NewDebugControlledWriter(os.Stderr)
		if err = cmd.Run(); err != nil {
			r.getLogger().WithError(err).Debugf("custom commandline execute failure (%s %s)\n", bash, strings.Join(args, " "))
		} else if resolvedConfig.OnRecordFinished.DeleteFlvAfterConvert {
			os.Remove(fileName)
		}
		r.getLogger().Debugf("end executing custom_commandline: %s", args[1])
	} else {
		// 使用新的 Pipeline 系统处理后处理任务
		inst := instance.GetInstance(ctx)

		// 确定实际输出的文件列表
		// 如果使用录播姬下载器，检查是否有分段文件
		var outputFiles []string
		if downloaderType == configs.DownloaderBililiveRecorder {
			partFiles := findBililiveRecorderOutputFiles(fileName)
			if len(partFiles) > 0 {
				outputFiles = partFiles
				r.getLogger().Infof("检测到录播姬分段文件: %d 个", len(partFiles))
				for i, f := range partFiles {
					r.getLogger().Debugf("  分段 %d: %s", i, f)
				}

				// 单文件重命名逻辑：
				// 1. 只有一个分段文件（_PART000）
				// 2. 未启用 FixFlvAtFirst（因为录播姬会在修复时自动分段，修复后的文件名已经是正确的）
				if len(partFiles) == 1 && !resolvedConfig.OnRecordFinished.FixFlvAtFirst {
					originalFileName := fileName // 原始期望的文件名，不带 _PART000
					partFileName := partFiles[0] // 录播姬实际输出的文件名，带 _PART000

					// 尝试重命名
					if err := os.Rename(partFileName, originalFileName); err != nil {
						r.getLogger().WithError(err).Warnf("无法将 %s 重命名为 %s，保留原文件名", partFileName, originalFileName)
					} else {
						r.getLogger().Infof("录播姬单文件重命名: %s -> %s", filepath.Base(partFileName), filepath.Base(originalFileName))
						outputFiles = []string{originalFileName}
					}
				}
			}
		}
		// 如果没有检测到分段文件，使用原始文件名
		if len(outputFiles) == 0 {
			// 检查原始文件是否存在
			if _, err := os.Stat(fileName); err == nil {
				outputFiles = []string{fileName}
			}
		}

		if len(outputFiles) == 0 {
			r.getLogger().Warn("没有找到任何输出文件，跳过后处理")
			return
		}

		// 累积录制文件信息，待录制结束后统一推送摘要
		r.accumulateRecordedFiles(outputFiles...)

		// 获取 PipelineManager
		pipelineManager := pipeline.GetManager(inst)
		if pipelineManager == nil {
			r.getLogger().Warn("pipeline manager not available, skipping post-processing")
			return
		}

		// 将旧配置转换为 Pipeline 配置
		pipelineConfig := pipeline.GetEffectivePipelineConfig(&resolvedConfig.OnRecordFinished)

		// 如果没有配置任何处理阶段，跳过
		if len(pipelineConfig.Stages) == 0 {
			r.getLogger().Debug("no pipeline stages configured, skipping post-processing")
			return
		}

		// 入队 Pipeline 任务
		if err := pipelineManager.EnqueueRecordingTask(info, pipelineConfig, outputFiles); err != nil {
			r.getLogger().WithError(err).Error("failed to enqueue pipeline task")
		} else {
			// 记录 Pipeline 阶段顺序
			var stageNames []string
			for _, stage := range pipelineConfig.Stages {
				stageNames = append(stageNames, stage.Name)
			}
			r.getLogger().Infof("pipeline task enqueued: %d files, stages: %v", len(outputFiles), stageNames)
		}
	}
}

// danmakuRecorderFactory 弹幕录制器工厂函数类型
type danmakuRecorderFactory func(roomID, cookies, outputFile string, cfg configs.DanmakuConfig, logger *logrus.Entry) danmakuRecorder

// startDanmakuRecorder 弹幕录制通用启动流程：解析房间ID、创建录制器、启动、替换旧录制器。
func (r *recorder) startDanmakuRecorder(ctx context.Context, fileName, platform string, resolvedConfig configs.ResolvedConfig, factory danmakuRecorderFactory) {
	assFile := fileName[:strings.LastIndex(fileName, ".")] + ".ass"
	cookies := extractCookiesString(r.Live)

	var roomID string
	switch platform {
	case "哔哩哔哩":
		if id := extractRoomIDFromUrl(r.Live.GetRawUrl()); id > 0 {
			roomID = strconv.Itoa(id)
		}
	case "抖音":
		roomID = extractDouyinRoomID(r.Live)
	case "斗鱼":
		roomID = extractDouyuRoomID(r.Live)
	}

	if roomID == "" {
		r.getLogger().Warn("弹幕录制已启用但无法解析房间ID: " + r.Live.GetRawUrl())
		return
	}

	r.getLogger().Infof("弹幕录制已启用，房间ID: %s, 输出: %s", roomID, assFile)
	rec := factory(roomID, cookies, assFile, resolvedConfig.Danmaku, r.getLogger().Entry)
	if dmErr := rec.Start(ctx); dmErr != nil {
		r.getLogger().WithError(dmErr).Warn("弹幕录制启动失败，继续录制视频")
		return
	}

	// 设置弹幕广播回调，将消息通过 SSE 推送到前端
	if broadcastDanmakuFunc != nil {
		liveId := r.Live.GetLiveId()
		rec.SetBroadcastCallback(func(msgType, username, content string, extra map[string]interface{}) {
			broadcastDanmakuFunc(liveId, msgType, username, content, extra)
		})
	}

	r.currentFileLock.Lock()
	old := r.danmakuRec
	r.danmakuRec = rec
	r.currentFileLock.Unlock()
	if old != nil {
		old.Stop()
	}
}

// stopRetryForExplicitOffline 在平台已明确给出"已下播"信号时补发一次 LiveEnd，
// 让 recorder manager 走正常回收流程，避免 recorder 永久停留在"录制准备中"。
func (r *recorder) stopRetryForExplicitOffline(ctx context.Context, err error) bool {
	if !errors.Is(err, live.ErrLiveOffline) {
		return false
	}
	r.getLogger().WithError(err).Info("stream source explicitly reported offline, dispatching LiveEnd")
	// 保留创建本 recorder 的 listener generation。否则旧 recorder 的延迟
	// LiveEnd 会退化成无 owner 的 legacy 事件，可能误删恢复监控后新建的 recorder。
	r.ed.DispatchEvent(events.NewEventWithContext(ctx, listeners.LiveEnd, r.Live))
	return true
}

func (r *recorder) selectPreferredStream(streamInfos []*live.StreamUrlInfo) (ret *live.StreamUrlInfo) {
	// 如果没有可用流，直接返回 nil
	if len(streamInfos) == 0 {
		return nil
	}

	streamPreference := configs.GetCurrentConfig().GetEffectiveConfigForRoom(r.Live.GetRawUrl()).StreamPreference

	// 如果未配置流偏好（Quality 和 Attributes 均为 nil），直接返回第一个流
	if streamPreference.Quality == nil && streamPreference.Attributes == nil {
		return streamInfos[0]
	}

	// 安全获取 Quality 和 Attributes，处理 nil 情况
	var quality string
	if streamPreference.Quality != nil {
		quality = *streamPreference.Quality
	}
	var attrs map[string]string
	if streamPreference.Attributes != nil {
		attrs = *streamPreference.Attributes
	}

	retMatchedCount := 0
	for _, info := range streamInfos {
		currMatchedCount := 0
		// 仅当配置了 Quality 时才匹配
		if quality != "" && info.Quality == quality {
			currMatchedCount += 100
		}
		// 仅当配置了 Attributes 时才匹配
		for k, v := range attrs {
			if info.AttributesForStreamSelect[k] == v {
				currMatchedCount += 1
			}
		}
		if currMatchedCount > retMatchedCount {
			ret = info
			retMatchedCount = currMatchedCount
		}
	}

	// 如果没有任何匹配的流，回退到第一个可用流
	if ret == nil {
		r.getLogger().Warnf("没有流匹配配置的偏好 (quality=%s, attrs=%v)，使用第一个可用流", quality, attrs)
		return streamInfos[0]
	}
	return
}

func (r *recorder) run(ctx context.Context) {
	defer r.sendAccumulatedSummary()
	runCtx := diagnostics.WithFields(ctx, r.diagnosticFields())
	exitReason := "unknown"
	diagnostics.Record(runCtx, "recorder.run.start", diagnostics.Fields{"status": "running"})
	defer func() {
		diagnostics.Record(runCtx, "recorder.run.end", diagnostics.Fields{
			"status":      "stopped",
			"exit_reason": exitReason,
		})
	}()

	const minRetryInterval = 5 * time.Second

	for {
		select {
		case <-r.stop:
			exitReason = "recorder_stop"
			return
		case <-ctx.Done():
			exitReason = "context_cancelled"
			return
		default:
			// 每次 tryRecord 使用独立的子 context
			// tryRecord 返回时 cancel 会停止所有异步操作（如 HLS 探测 goroutine）
			tryCtx, tryCancel := context.WithCancel(ctx)
			start := time.Now()
			r.tryRecord(tryCtx)
			tryCancel()

			// 确保两次 tryRecord 之间至少间隔 minRetryInterval
			// 防止快速失败（如 FFmpeg 秒退 404）导致紧密循环
			if elapsed := time.Since(start); elapsed < minRetryInterval {
				delay := minRetryInterval - elapsed
				select {
				case <-r.stop:
					exitReason = "recorder_stop"
					return
				case <-ctx.Done():
					exitReason = "context_cancelled"
					return
				case <-time.After(delay):
				}
			}
		}
	}
}

// accumulateRecordedFiles 累积录制文件信息，仅记录实际存在的文件
func (r *recorder) accumulateRecordedFiles(files ...string) {
	r.recordedFilesMu.Lock()
	defer r.recordedFilesMu.Unlock()
	for _, f := range files {
		if fi, err := os.Stat(f); err == nil {
			r.recordedFiles = append(r.recordedFiles, notify.RecordingFileDetail{
				Name: filepath.Base(f),
				Size: fi.Size(),
			})
		}
	}
}

// sendAccumulatedSummary 录制结束后统一推送录制文件摘要通知
// 在 run() 退出时通过 defer 调用，确保所有分段文件汇总为一条通知
func (r *recorder) sendAccumulatedSummary() {
	r.recordedFilesMu.Lock()
	defer r.recordedFilesMu.Unlock()
	if r.suppressSummary {
		r.getLogger().Infof("录制摘要推送已抑制（分段重启），累积 %d 个文件将传递给新 recorder", len(r.recordedFiles))
		return
	}
	if len(r.recordedFiles) == 0 {
		r.getLogger().Info("无录制文件，跳过摘要推送")
		return
	}
	obj, err := r.cache.Get(r.Live)
	if err != nil {
		r.getLogger().WithError(err).Error("获取直播信息失败，无法推送录制摘要")
		return
	}
	info := obj.(*live.Info)

	// 获取录制输出路径，用于查询剩余磁盘空间
	cfg := configs.GetCurrentConfig()
	outputPath := cfg.OutPutPath
	if room, roomErr := cfg.GetLiveRoomByUrl(r.Live.GetRawUrl()); roomErr == nil {
		platformKey := configs.GetPlatformKeyFromUrl(r.Live.GetRawUrl())
		resolved := cfg.ResolveConfigForRoom(room, platformKey)
		outputPath = resolved.OutPutPath
	}

	r.getLogger().Infof("推送录制摘要：%d 个文件", len(r.recordedFiles))
	notify.SendRecordingSummary(r.getLogger(), info.HostName, r.Live.GetPlatformCNName(), r.recordedFiles, outputPath)
}

func (r *recorder) logStreamURLRetry(err error) {
	if configs.IsDebug() || !strings.EqualFold(r.Live.GetPlatformCNName(), "SOOP") {
		r.warnStreamURLRetry(err, "failed to get stream url, will retry after 5s...", 0)
		return
	}

	key := ""
	if err != nil {
		key = err.Error()
	}

	now := time.Now()

	r.retryLogMu.Lock()
	if key != r.lastRetryLogKey || r.lastRetryLogAt.IsZero() {
		r.lastRetryLogKey = key
		r.lastRetryLogAt = now
		r.suppressedRetryLogCount = 0
		r.retryLogMu.Unlock()
		r.warnStreamURLRetry(err, "failed to get stream url, will retry after 5s...", 0)
		return
	}

	if now.Sub(r.lastRetryLogAt) >= soopRetryWarnInterval {
		suppressed := r.suppressedRetryLogCount
		r.lastRetryLogAt = now
		r.suppressedRetryLogCount = 0
		r.retryLogMu.Unlock()
		r.warnStreamURLRetry(err, "failed to get stream url, still retrying every 5s...", suppressed)
		return
	}

	r.suppressedRetryLogCount++
	r.retryLogMu.Unlock()
}

func (r *recorder) warnStreamURLRetry(err error, msg string, suppressed int) {
	logger := r.getLogger()
	if suppressed > 0 {
		entry := logger.WithField("suppressed", suppressed)
		if err != nil {
			entry = entry.WithError(err)
		}
		entry.Warn(msg)
		return
	}

	if err != nil {
		logger.WithError(err).Warn(msg)
		return
	}
	logger.Warn(msg)
}

func (r *recorder) resetRetryLogState() {
	r.retryLogMu.Lock()
	defer r.retryLogMu.Unlock()

	r.lastRetryLogKey = ""
	r.lastRetryLogAt = time.Time{}
	r.suppressedRetryLogCount = 0
}

func (r *recorder) getParser() parser.Parser {
	r.parserLock.RLock()
	defer r.parserLock.RUnlock()
	return r.parser
}

// setAndCloseParser 安装本次尝试的 parser，并与 Close 的 stopped 转换配合：
// 若 Close 已经取得 stopped，新的 parser 会被立即停止且绝不暴露给 run；
// 若本函数先安装，Close 随后的 getParser 一定能观察并停止它。
func (r *recorder) setAndCloseParser(p parser.Parser) bool {
	r.parserLock.Lock()
	if atomic.LoadUint32(&r.state) == stopped {
		r.parserLock.Unlock()
		if p != nil {
			if err := p.Stop(); err != nil {
				r.getLogger().WithError(err).Warn("failed to reject recorder after close")
			}
		}
		return false
	}
	old := r.parser
	r.parser = p
	r.parserLock.Unlock()

	if old != nil {
		if err := old.Stop(); err != nil {
			r.getLogger().WithError(err).Warn("failed to end recorder")
		}
	}
	return true
}

func (r *recorder) Start(ctx context.Context) error {
	fields := r.diagnosticFields()
	traceCtx := diagnostics.WithFields(ctx, fields)
	diagnostics.Record(traceCtx, "recorder.start.requested", diagnostics.Fields{
		"status":     "requested",
		"from_state": recorderStateName(atomic.LoadUint32(&r.state)),
	})
	if !atomic.CompareAndSwapUint32(&r.state, begin, pending) {
		diagnostics.Record(traceCtx, "recorder.start.ignored", diagnostics.Fields{
			"status":        "ignored",
			"current_state": recorderStateName(atomic.LoadUint32(&r.state)),
		})
		return nil
	}

	runCtx, runCancel := context.WithCancel(traceCtx)
	r.setRunCancel(runCancel)

	// 无论正常发布、被 Close 取消，还是 Start 中途异常退出，都必须释放 Close
	// 对 pending 阶段的等待。若主任务尚未成功交给运行时，还要补发 done，避免
	// Close 永久等待一个并不存在的 run。
	runLaunched := false
	var endTask func(diagnostics.Fields)
	defer func() {
		if !runLaunched {
			if endTask != nil {
				endTask(diagnostics.Fields{"status": "start_aborted"})
			}
			runCancel()
			r.stopOnce.Do(func() { close(r.stop) })
			r.signalDone()
		}
		r.signalStartReady()
	}()

	taskCtx, endTask := diagnostics.NewTask(runCtx, "recorder.session", fields)

	// running 是 RecorderStart 的发布权。Close 若先把 pending 改成 stopped，
	// 本次 Start 就安全取消且不产生 Start/Stop 半对事件；若此 CAS 成功，Close
	// 会等待 startReady，保证 Start 已完整提交后才能产生 RecorderStop。
	if !atomic.CompareAndSwapUint32(&r.state, pending, running) {
		diagnostics.Record(taskCtx, "recorder.start.cancelled", diagnostics.Fields{
			"status":        "cancelled",
			"current_state": recorderStateName(atomic.LoadUint32(&r.state)),
			"reason":        "close_won_pending",
		})
		return nil
	}
	diagnostics.Record(taskCtx, "recorder.state.transition", diagnostics.Fields{
		"status": "running",
		"from":   "pending",
		"to":     "running",
	})

	bilisentry.GoWithContext(taskCtx, func(ctx context.Context) {
		// endTask 必须先于 done 发布；Manager.Close 等待 done 后即可保证
		// recorder 主任务不会再写入业务轨迹。
		defer r.signalDone()
		defer runCancel()
		defer endTask(diagnostics.Fields{"status": "ended"})
		r.run(ctx)
	})
	runLaunched = true
	diagnostics.Record(taskCtx, "recorder.session.start", diagnostics.Fields{
		"status": "accepted",
	})

	r.getLogger().Info("Record Start ", r.Live.GetRawUrl())
	r.ed.DispatchEvent(events.NewEventWithContext(taskCtx, RecorderStart, r.Live))
	r.startEventSent.Store(true)
	return nil
}

func (r *recorder) StartTime() time.Time {
	return r.startTime
}

// IsRecording 返回当前是否正在实际录制
// 判断标准：输出文件已创建且有实际数据写入（size > 0）
// 仅有 parser（如 ffmpeg 进程）不代表真正在录制 —— ffmpeg 可能因为流 URL 404 等原因
// 启动后立即失败，没有写入任何视频数据
func (r *recorder) IsRecording() bool {
	filePath := r.getCurrentFilePath()
	if filePath == "" {
		return false
	}
	if fileInfo, err := os.Stat(filePath); err == nil {
		return fileInfo.Size() > 0
	}
	return false
}

func (r *recorder) Close() {
	r.closeOnce.Do(r.close)
}

func (r *recorder) close() {
	fields := r.diagnosticFields()
	traceCtx := diagnostics.WithFields(context.Background(), fields)

	initialState := atomic.LoadUint32(&r.state)
	diagnostics.Record(traceCtx, "recorder.close.requested", diagnostics.Fields{
		"status":     "requested",
		"from_state": recorderStateName(initialState),
	})

	started := false
	for {
		state := atomic.LoadUint32(&r.state)
		switch state {
		case begin:
			if !atomic.CompareAndSwapUint32(&r.state, begin, stopped) {
				continue
			}
			r.stopOnce.Do(func() { close(r.stop) })
			r.signalDone()
		case pending:
			if !atomic.CompareAndSwapUint32(&r.state, pending, stopped) {
				continue
			}
			started = true
			r.stopOnce.Do(func() { close(r.stop) })
		case running:
			if !atomic.CompareAndSwapUint32(&r.state, running, stopped) {
				continue
			}
			started = true
			r.stopOnce.Do(func() { close(r.stop) })
		case stopped:
			// closeOnce 保证只有本调用能在这里执行关闭主体；此分支仅兼容
			// 外部旧代码预先写入 stopped 的情况。
		default:
			diagnostics.Record(traceCtx, "recorder.close.ignored", diagnostics.Fields{
				"status":        "ignored",
				"current_state": recorderStateName(state),
			})
			return
		}
		break
	}

	// 先取消 recorder 私有 context，再检查 parser。即使 tryRecord 此刻尚未
	// 完成 parser 初始化，后续安装也会观察 stopped 并立即拒绝。
	r.cancelRun()

	if !started {
		diagnostics.Record(traceCtx, "recorder.close.ignored", diagnostics.Fields{
			"status":        "not_started",
			"current_state": recorderStateName(initialState),
		})
		return
	}

	if p := r.getParser(); p != nil {
		if err := p.Stop(); err != nil {
			r.getLogger().WithError(err).Warn("failed to end recorder")
		}
	}
	// 停止弹幕录制器
	r.currentFileLock.RLock()
	dmRec := r.danmakuRec
	r.currentFileLock.RUnlock()
	if dmRec != nil {
		dmRec.Stop()
	}

	// Start 可能刚把主任务交给运行时、但尚未发布 RecorderStart；必须等到它
	// 发布完成或确认取消，才能记录 session.end 和产生 RecorderStop。
	r.waitStartReady()

	// run 会先完成 parser/observer、摘要和 diagnostics task，再发布 done。
	// run 结束后不会再创建 aux 任务，因此随后 Wait 不存在 Add/Wait 竞态。
	<-r.done
	r.auxWg.Wait()

	r.getLogger().Info("Record End")
	diagnostics.Record(traceCtx, "recorder.session.end", diagnostics.Fields{
		"status": "stopped",
	})
	if r.startWasPublished() {
		r.ed.DispatchEvent(events.NewEventWithContext(traceCtx, RecorderStop, r.Live))
	} else {
		diagnostics.Record(traceCtx, "recorder.stop.event.skipped", diagnostics.Fields{
			"status": "not_published",
			"reason": "start_cancelled",
		})
	}
}

func (r *recorder) signalDone() {
	r.doneOnce.Do(func() { close(r.done) })
}

func (r *recorder) setRunCancel(cancel context.CancelFunc) {
	r.runCancelMu.Lock()
	r.runCancel = cancel
	alreadyStopped := atomic.LoadUint32(&r.state) == stopped
	r.runCancelMu.Unlock()

	// 覆盖 Close 已完成状态转换并在本函数加锁前读到 nil cancel 的交错。
	if alreadyStopped && cancel != nil {
		cancel()
	}
}

func (r *recorder) cancelRun() {
	r.runCancelMu.Lock()
	cancel := r.runCancel
	r.runCancelMu.Unlock()
	if cancel != nil {
		cancel()
	}
}

func (r *recorder) signalStartReady() {
	// 兼容少量直接构造 recorder 的旧测试/内部代码；NewRecorder 始终初始化。
	if r.startReady == nil {
		return
	}
	r.startReadyOnce.Do(func() { close(r.startReady) })
}

func (r *recorder) waitStartReady() {
	if r.startReady != nil {
		<-r.startReady
	}
}

func (r *recorder) startWasPublished() bool {
	// 未设置 startReady 只可能是直接构造的 legacy recorder。它若已经处于
	// running/pending，沿用旧语义产生 RecorderStop。
	return r.startReady == nil || r.startEventSent.Load()
}

func (r *recorder) goTracked(ctx context.Context, fn func(context.Context)) {
	r.auxWg.Add(1)
	bilisentry.GoWithContext(ctx, func(ctx context.Context) {
		defer r.auxWg.Done()
		fn(ctx)
	})
}

func (r *recorder) CloseForRestart() []notify.RecordingFileDetail {
	r.recordedFilesMu.Lock()
	r.suppressSummary = true
	r.recordedFilesMu.Unlock()
	r.Close()
	<-r.done // 等待 run() 完全退出，确保最后一个文件已累积
	r.recordedFilesMu.Lock()
	defer r.recordedFilesMu.Unlock()
	r.getLogger().Infof("分段重启：携带 %d 个累积文件传递给新 recorder", len(r.recordedFiles))
	return r.recordedFiles
}

func (r *recorder) SetInitialRecordedFiles(files []notify.RecordingFileDetail) {
	r.recordedFilesMu.Lock()
	// 分配新 slice 避免修改入参 files 的底层数组，防止调用方持有的切片被意外改变
	merged := make([]notify.RecordingFileDetail, 0, len(files)+len(r.recordedFiles))
	merged = append(merged, files...)
	merged = append(merged, r.recordedFiles...)
	r.recordedFiles = merged
	r.recordedFilesMu.Unlock()
	// 日志不依赖 r.recordedFiles，移到锁外减少持有时间
	r.getLogger().Infof("继承上一个 recorder 的 %d 个录制文件", len(files))
}

func (r *recorder) getLogger() *livelogger.LiveLogger {
	return r.Live.GetLogger()
}

// setCurrentFilePath 设置当前正在录制的文件路径
func (r *recorder) setCurrentFilePath(path string) {
	r.currentFileLock.Lock()
	defer r.currentFileLock.Unlock()
	r.currentFilePath = path
}

// observeFirstNonzeroFile 以有界轮询观察“输出文件首次变为非空”。
// 这是文件系统层面的近似观测，不宣称等于 parser 的精确首字节写入时刻；精度会
// 明确写入事件。原始文件路径只在进程内使用，绝不会进入 diagnostics attrs。
func (r *recorder) observeFirstNonzeroFile(
	ctx context.Context,
	expectedPath string,
	segmentID string,
	parserName string,
	attemptID string,
) {
	const pollInterval = 250 * time.Millisecond
	startedAt := time.Now()

	check := func() (int64, string, bool) {
		if info, err := os.Stat(expectedPath); err == nil && info.Mode().IsRegular() && info.Size() > 0 {
			return info.Size(), "expected", true
		}
		if parserName == bililive_recorder.Name {
			for _, partPath := range findBililiveRecorderOutputFiles(expectedPath) {
				if info, err := os.Stat(partPath); err == nil && info.Mode().IsRegular() && info.Size() > 0 {
					return info.Size(), "part", true
				}
			}
		}
		return 0, "", false
	}

	recordObserved := func(size int64, variant string) {
		diagnostics.Record(ctx, "segment.first_nonzero_observed", diagnostics.Fields{
			"component":              "segment",
			"lane":                   "file",
			"room_scope_id":          r.roomScopeID,
			"session_id":             r.sessionID,
			"attempt_id":             attemptID,
			"segment_id":             segmentID,
			"parser":                 parserName,
			"status":                 "observed",
			"observed_size_bytes":    size,
			"file_variant":           variant,
			"observation_latency_ms": float64(time.Since(startedAt)) / float64(time.Millisecond),
			"poll_interval_ms":       float64(pollInterval) / float64(time.Millisecond),
			"measurement_accuracy":   "filesystem_poll_upper_bound",
		})
	}

	if size, variant, ok := check(); ok {
		recordObserved(size, variant)
		return
	}
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			// Parser 可能恰好在两个 tick 之间完成，取消前做最后一次观察。
			if size, variant, ok := check(); ok {
				recordObserved(size, variant)
				return
			}
			diagnostics.Record(ctx, "segment.first_nonzero_observer.end", diagnostics.Fields{
				"component":        "segment",
				"lane":             "file",
				"room_scope_id":    r.roomScopeID,
				"session_id":       r.sessionID,
				"attempt_id":       attemptID,
				"segment_id":       segmentID,
				"parser":           parserName,
				"status":           "not_observed",
				"elapsed_ms":       float64(time.Since(startedAt)) / float64(time.Millisecond),
				"poll_interval_ms": float64(pollInterval) / float64(time.Millisecond),
			})
			return
		case <-ticker.C:
			if size, variant, ok := check(); ok {
				recordObserved(size, variant)
				return
			}
		}
	}
}

// getCurrentFilePath 获取当前正在录制的文件路径
func (r *recorder) getCurrentFilePath() string {
	r.currentFileLock.RLock()
	defer r.currentFileLock.RUnlock()
	return r.currentFilePath
}

func (r *recorder) GetStatus() (map[string]interface{}, error) {
	var status map[string]interface{}

	// 尝试从 parser 获取状态（FFmpeg 进度等）
	// 如果 parser 还没创建或不支持 StatusParser，使用空 map 继续
	// （流信息 stream_quality/stream_format 等仍然需要返回给前端）
	statusP, ok := r.getParser().(parser.StatusParser)
	if ok {
		var err error
		status, err = statusP.Status()
		if err != nil {
			status = nil
		}
	}
	if status == nil {
		status = make(map[string]interface{})
	}

	// 添加文件路径和文件大小信息
	filePath := r.getCurrentFilePath()
	if filePath != "" {
		status["file_path"] = filePath
		// 获取文件大小
		if fileInfo, err := os.Stat(filePath); err == nil {
			status["file_size"] = strconv.FormatInt(fileInfo.Size(), 10)
		}
	}

	// 添加当前录制的流信息
	r.currentFileLock.RLock()
	streamInfo := r.currentStreamInfo
	r.currentFileLock.RUnlock()
	if streamInfo != nil {
		status["stream_format"] = streamInfo.Format
		status["stream_quality"] = streamInfo.Quality
		status["stream_quality_name"] = streamInfo.QualityName
		if streamInfo.Description != "" && streamInfo.Description != streamInfo.Quality {
			status["stream_description"] = streamInfo.Description
		}
		if streamInfo.Width > 0 && streamInfo.Height > 0 {
			status["stream_resolution"] = fmt.Sprintf("%dx%d", streamInfo.Width, streamInfo.Height)
		}
		if streamInfo.Bitrate > 0 {
			status["stream_bitrate"] = fmt.Sprintf("%d", streamInfo.Bitrate)
		}
		if streamInfo.FrameRate > 0 {
			status["stream_fps"] = fmt.Sprintf("%.0f", streamInfo.FrameRate)
		}
		if streamInfo.AttributesForStreamSelect != nil {
			status["stream_attributes_for_stream_select"] = streamInfo.AttributesForStreamSelect
		}
		status["stream_codec"] = streamInfo.Codec
	}

	// 添加实际流头部信息（来自 StreamProbe 探测）
	if actualInfo := r.actualStreamInfo.Load(); actualInfo != nil {
		status["probe_status"] = actualInfo.ProbeStatus()
		if actualInfo.Resolution() != "" {
			status["actual_resolution"] = actualInfo.Resolution()
		}
		if actualInfo.VideoCodec != "" {
			status["actual_video_codec"] = actualInfo.VideoCodec
		}
		if actualInfo.AudioCodec != "" {
			status["actual_audio_codec"] = actualInfo.AudioCodec
		}
		if actualInfo.VideoBitrate > 0 {
			status["actual_video_bitrate"] = fmt.Sprintf("%d", actualInfo.VideoBitrate)
		}
		if actualInfo.FrameRate > 0 {
			status["actual_frame_rate"] = fmt.Sprintf("%.1f", actualInfo.FrameRate)
		}
		if actualInfo.Unsupported {
			status["probe_message"] = actualInfo.UnsupportedMsg
		}

		// 判断实际分辨率与平台声称的是否一致
		if actualInfo.Width > 0 && actualInfo.Height > 0 && streamInfo != nil {
			resolutionMatch := (streamInfo.Width == 0 && streamInfo.Height == 0) || // 平台未提供分辨率，视为一致
				(actualInfo.Width == streamInfo.Width && actualInfo.Height == streamInfo.Height)
			status["resolution_match"] = resolutionMatch
		}
	} else {
		status["probe_status"] = "pending"
	}

	// 添加原始流 URL 和 Headers（供前端调试展示）
	// 对敏感 Header（如 Cookie、Authorization）进行脱敏处理，
	// 避免在 WebUI 无鉴权或被反代到公网时泄露凭据
	r.currentFileLock.RLock()
	if r.currentStreamURL != "" {
		status["stream_url"] = r.currentStreamURL
	}
	if len(r.currentStreamHeaders) > 0 {
		status["stream_headers"] = sanitizeHeaders(r.currentStreamHeaders)
	}
	// 弹幕录制状态（与 currentFileLock 同步保护 danmakuRec 的并发访问）
	if r.danmakuRec != nil {
		for k, v := range r.danmakuRec.GetStatus() {
			status[k] = v
		}
	}
	r.currentFileLock.RUnlock()

	return status, nil
}

// GetParserPID 获取当前 parser 进程的 PID
func (r *recorder) GetParserPID() int {
	p := r.getParser()
	if p == nil {
		return 0
	}
	// 检查 parser 是否实现了 PIDProvider 接口
	if pidProvider, ok := p.(parser.PIDProvider); ok {
		return pidProvider.GetPID()
	}
	return 0
}

// RequestSegment 请求在下一个关键帧处分段
func (r *recorder) RequestSegment() bool {
	p := r.getParser()
	if p == nil {
		return false
	}
	// 检查 parser 是否实现了 SegmentRequester 接口
	if segmentRequester, ok := p.(parser.SegmentRequester); ok {
		return segmentRequester.RequestSegment()
	}
	return false
}

// HasFlvProxy 检查当前是否使用 FLV 代理
func (r *recorder) HasFlvProxy() bool {
	p := r.getParser()
	if p == nil {
		return false
	}
	// 检查 parser 是否实现了 SegmentRequester 接口
	if segmentRequester, ok := p.(parser.SegmentRequester); ok {
		return segmentRequester.HasFlvProxy()
	}
	return false
}

// saveCurrentStreamInfo 保存当前录制的流信息
func (r *recorder) saveCurrentStreamInfo(s *live.StreamUrlInfo) {
	if s == nil {
		return
	}

	// 格式
	format := strings.ToLower(s.Format)
	if format == "" && s.Url != nil {
		urlPath := s.Url.Path
		if strings.Contains(urlPath, ".flv") {
			format = "flv"
		} else if strings.Contains(urlPath, "m3u8") {
			format = "hls"
		}
	}

	// 编码
	codec := s.Codec
	if codec == "" {
		codec = "h264"
	}

	// 码率
	bitrate := s.Bitrate
	if bitrate == 0 && s.Vbitrate > 0 {
		bitrate = s.Vbitrate
	}

	streamInfo := &live.AvailableStreamInfo{
		Format:                    format,
		Quality:                   s.Quality,
		QualityName:               live.GetQualityName(s.Quality),
		Description:               s.Description,
		Width:                     s.Width,
		Height:                    s.Height,
		Bitrate:                   bitrate,
		FrameRate:                 s.FrameRate,
		Codec:                     codec,
		AudioCodec:                s.AudioCodec,
		AttributesForStreamSelect: s.AttributesForStreamSelect,
	}

	r.currentFileLock.Lock()
	r.currentStreamInfo = streamInfo
	r.currentFileLock.Unlock()
}

// updateAvailableStreams 更新可用流信息到 Info
func (r *recorder) updateAvailableStreams(ctx context.Context, info *live.Info, streamInfos []*live.StreamUrlInfo) {
	availableStreams := make([]*live.AvailableStreamInfo, 0, len(streamInfos))

	for _, s := range streamInfos {
		// 格式
		format := strings.ToLower(s.Format)
		if format == "" && s.Url != nil {
			urlPath := s.Url.Path
			if strings.Contains(urlPath, ".flv") {
				format = "flv"
			} else if strings.Contains(urlPath, "m3u8") {
				format = "hls"
			}
		}

		// 编码
		codec := s.Codec
		if codec == "" {
			codec = "h264"
		}

		// 码率
		bitrate := s.Bitrate
		if bitrate == 0 && s.Vbitrate > 0 {
			bitrate = s.Vbitrate
		}

		stream := &live.AvailableStreamInfo{
			Format:                    format,
			Quality:                   s.Quality,
			QualityName:               live.GetQualityName(s.Quality),
			Description:               s.Description,
			Width:                     s.Width,
			Height:                    s.Height,
			Bitrate:                   bitrate,
			FrameRate:                 s.FrameRate,
			Codec:                     codec,
			AudioCodec:                s.AudioCodec,
			AttributesForStreamSelect: s.AttributesForStreamSelect,
		}

		availableStreams = append(availableStreams, stream)
	}

	info.AvailableStreams = availableStreams
	info.AvailableStreamsUpdatedAt = time.Now().Unix()

	// 更新缓存，以便 API 可以获取到最新的可用流信息
	if r.cache != nil {
		r.cache.Set(r.Live, info)
	}

	// 保存到数据库（使用 goroutine 避免阻塞录制流程）
	r.goTracked(ctx, func(ctx context.Context) {
		r.saveAvailableStreamsToDatabase(ctx, availableStreams)
	})
}

// AvailableStreamData 可用流数据（用于保存到数据库的接口）
type AvailableStreamData struct {
	Format      string
	Quality     string
	QualityName string
	Description string
	Width       int
	Height      int
	Bitrate     int
	FrameRate   float64
	Codec       string
	AudioCodec  string
}

// AvailableStreamSaver 定义保存可用流的接口（避免循环导入）
type AvailableStreamSaver interface {
	SaveAvailableStreamsGeneric(ctx context.Context, liveID string, streams []AvailableStreamData) error
}

// saveAvailableStreamsToDatabase 保存可用流信息到数据库
func (r *recorder) saveAvailableStreamsToDatabase(ctx context.Context, streams []*live.AvailableStreamInfo) {
	inst := instance.GetInstance(ctx)
	if inst.LiveStateStore == nil {
		return
	}

	// 使用类型断言检查是否有 SaveAvailableStreamsGeneric 方法
	// 使用反射调用，避免循环导入
	storeVal := inst.LiveStateStore
	// 尝试获取 SaveAvailableStreams 方法
	type streamSaver interface {
		SaveAvailableStreamsAny(ctx context.Context, liveID string, streams interface{}) error
	}

	if saver, ok := storeVal.(streamSaver); ok {
		// 转换为通用数据类型
		data := make([]map[string]interface{}, 0, len(streams))
		for _, s := range streams {
			data = append(data, map[string]interface{}{
				"Format":                    s.Format,
				"Quality":                   s.Quality,
				"QualityName":               s.QualityName,
				"Description":               s.Description,
				"Width":                     s.Width,
				"Height":                    s.Height,
				"Bitrate":                   s.Bitrate,
				"FrameRate":                 s.FrameRate,
				"Codec":                     s.Codec,
				"AudioCodec":                s.AudioCodec,
				"AttributesForStreamSelect": s.AttributesForStreamSelect,
			})
		}

		// 使用 context.Background() 而非传入的 ctx，避免因录制 context 被取消
		// 导致数据库写入失败（context canceled）。数据库保存是独立的短暂操作，
		// 不应受录制生命周期影响。
		saveCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		if err := saver.SaveAvailableStreamsAny(saveCtx, string(r.Live.GetLiveId()), data); err != nil {
			r.getLogger().Warnf("保存可用流信息到数据库失败: %v", err)
		}
	}
}

// sensitiveHeaders 包含需要脱敏的 HTTP header 名称（小写）
var sensitiveHeaders = map[string]bool{
	"cookie":        true,
	"authorization": true,
	"set-cookie":    true,
	"x-api-key":     true,
	"x-auth-token":  true,
}

// sanitizeHeaders 对 HTTP Headers 进行脱敏处理
// 安全的 header（如 Referer、User-Agent）原样返回，
// 敏感的 header（如 Cookie、Authorization）只显示前后各几个字符
func sanitizeHeaders(headers map[string]string) map[string]string {
	result := make(map[string]string, len(headers))
	for k, v := range headers {
		if sensitiveHeaders[strings.ToLower(k)] {
			result[k] = maskValue(v)
		} else {
			result[k] = v
		}
	}
	return result
}

// maskValue 对敏感值进行脱敏：保留前 4 位和后 4 位，中间用 *** 替换
// 如果值太短（<= 12 字符），只显示前 4 位 + ***
func maskValue(v string) string {
	if len(v) <= 8 {
		return "***"
	}
	if len(v) <= 12 {
		return v[:4] + "***"
	}
	return v[:4] + "***" + v[len(v)-4:]
}

// extractRoomIDFromUrl extracts the numeric room ID from a Bilibili live URL path.
// Returns 0 if the URL doesn't contain a valid room ID.
func extractRoomIDFromUrl(rawUrl string) int {
	u, err := url.Parse(rawUrl)
	if err != nil {
		return 0
	}
	paths := strings.Split(u.Path, "/")
	if len(paths) < 2 {
		return 0
	}
	id, err := strconv.Atoi(paths[1])
	if err != nil {
		return 0
	}
	return id
}

// extractCookiesString extracts cookies from the Live's cookie jar as a semicolon-separated string.
func extractCookiesString(l live.Live) string {
	opts := l.GetOptions()
	if opts == nil || opts.Cookies == nil {
		return ""
	}
	u, err := url.Parse(l.GetRawUrl())
	if err != nil {
		return ""
	}
	cookies := opts.Cookies.Cookies(u)
	if len(cookies) == 0 {
		return ""
	}
	parts := make([]string, 0, len(cookies))
	for _, c := range cookies {
		parts = append(parts, c.Name+"="+c.Value)
	}
	return strings.Join(parts, "; ")
}

// extractDouyinRoomID 从抖音直播 URL 中提取房间号（字符串）。
// 抖音房间号是数字字符串，直接从 URL 路径中提取。
func extractDouyinRoomID(l live.Live) string {
	u, err := url.Parse(l.GetRawUrl())
	if err != nil {
		return ""
	}
	paths := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(paths) < 1 {
		return ""
	}
	roomID := paths[0]
	if roomID == "" {
		return ""
	}
	return roomID
}

// extractDouyuRoomID 从斗鱼直播 URL 中提取房间号（字符串）。
// 优先使用 Live 中已解析的数字 roomID（fetchRoomID 从页面解析），
// 避免别名 URL（如 /lotterytimer）传入非数字 ID 导致弹幕登录失败。
func extractDouyuRoomID(l live.Live) string {
	type roomIDProvider interface {
		GetRoomID() string
	}
	if provider, ok := l.(roomIDProvider); ok {
		if rid := provider.GetRoomID(); rid != "" {
			return rid
		}
	}
	u, err := url.Parse(l.GetRawUrl())
	if err != nil {
		return ""
	}
	return strings.Trim(u.Path, "/")
}
