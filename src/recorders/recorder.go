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

const (
	soopRetryWarnInterval       = time.Minute
	bililiveRecorderPart0Suffix = "_PART000"
)

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

	validateOutputFilePath = utils.ValidateOutputFilePath
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

func replaceOutputFileExtension(fileName, extension string) string {
	currentExtension := filepath.Ext(fileName)
	return strings.TrimSuffix(fileName, currentExtension) + extension
}

func bililiveRecorderPart0FileName(fileName string) string {
	extension := filepath.Ext(fileName)
	return strings.TrimSuffix(fileName, extension) + bililiveRecorderPart0Suffix + extension
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

// pipelineSharedState 封装可在分段重启时共享的 Pipeline 状态
// 分段重启时新旧 recorder 通过 redirect 链共享同一份状态，
// 确保旧任务的回调能正确递减新 recorder 的计数器
type pipelineSharedState struct {
	mu             sync.Mutex
	pendingCount   int                          // 尚未完成的 Pipeline 任务数
	enqueued       bool                         // 是否有 Pipeline 任务已入队
	summarySent    bool                         // 摘要是否已发送（幂等保护）
	runExited      bool                         // run() 是否已退出（defer 中置位，回调中检查）
	details        []notify.RecordingFileDetail // 收集的文件详情
	sourceNames    map[string]bool              // 被 Pipeline 消费的原始文件名（用于合并时排除已删除的源文件）
	onAllTasksDone func()                       // 所有任务完成时的回调（分段重启时设置）
	suppressSummary bool                        // 为 true 时，run() 退出不推送摘要（分段重启场景），用 mu 保护
	redirectedTo    atomic.Pointer[pipelineSharedState] // 分段重启时重定向到新 recorder 的共享状态
}

// resolveState 沿 redirect 链找到当前活跃的 pipelineSharedState。
// 使用无锁遍历 + 锁内验证的方式，避免与 TransferPipelineState 产生死锁。
// 返回时调用者持有 result.mu 锁。
func resolveState(state *pipelineSharedState) *pipelineSharedState {
	for {
		// 无锁遍历找到链尾候选（通过 atomic.Load 避免与写入端的数据竞争）
		final := state
		for next := final.redirectedTo.Load(); next != nil; next = final.redirectedTo.Load() {
			final = next
		}
		// 锁住候选状态，验证没有被并发重定向
		final.mu.Lock()
		if final.redirectedTo.Load() == nil {
			return final // 调用者持有 final.mu 锁
		}
		final.mu.Unlock()
	}
}

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

	// 当前录制文件信息
	currentFileLock sync.RWMutex
	currentFilePath string

	// closeForRestart 标记当前 recorder 正在分段重启，run() 退出时不发送摘要。
	// 在 CloseForRestart 中设置（早于 Close），run() 的 defer 中检查。
	closeForRestart atomic.Bool

	// pipelineState 封装 Pipeline 相关的共享状态
	// 使用指针，分段重启时新旧 recorder 共享同一份状态
	pipelineState *pipelineSharedState

	// transferWg 用于分段重启时的摘要屏障。
	// RestartRecorder 在创建新 recorder 后 Add(1)，
	// TransferPipelineState 完成后 Done()。
	// sendAccumulatedSummary 在发送前 Wait()，确保旧分段状态已转入。
	// 非分段重启场景为 nil，Wait 是 no-op。
	transferWg *sync.WaitGroup

	// notifyWg 跟踪异步通知 goroutine（go sendAccumulatedSummary）。
	// 由 RecorderManager 持有，关闭流程中等待所有通知发送完成。
	notifyWg *sync.WaitGroup

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
	// suppressSummary 已移入 pipelineSharedState，用 pipelineState.mu 保护

	retryLogMu              sync.Mutex
	lastRetryLogKey         string
	lastRetryLogAt          time.Time
	suppressedRetryLogCount int

	recordingErrorMu sync.RWMutex
	recordingError   string
}

func NewRecorder(ctx context.Context, live live.Live) (Recorder, error) {
	inst := instance.GetInstance(ctx)

	return &recorder{
		Live:            live,
		cache:           inst.Cache,
		startTime:       time.Now(),
		ed:              inst.EventDispatcher.(events.Dispatcher),
		state:           begin,
		stop:            make(chan struct{}),
		done:            make(chan struct{}),
		parserLock:      new(sync.RWMutex),
		pipelineState: &pipelineSharedState{
			sourceNames: make(map[string]bool),
		},
	}, nil
}

func (r *recorder) tryRecord(ctx context.Context) {
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
	if err != nil || len(streamInfos) == 0 {
		if err != nil && r.stopRetryForExplicitOffline(err) {
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

	// TODO 根据配置选择最佳流
	streamInfo := r.selectPreferredStream(streamInfos)
	url := streamInfo.Url

	if strings.Contains(url.Path, "m3u8") {
		fileName = replaceOutputFileExtension(fileName, ".ts")
	}

	if info.AudioOnly {
		fileName = replaceOutputFileExtension(fileName, ".aac")
	}

	// 使用层级配置的下载器类型
	downloaderType := resolvedConfig.Feature.GetEffectiveDownloaderType()
	effectiveParserName := resolveParserName(downloaderType, strings.Contains(url.Path, ".flv"), nil)
	pathToValidate := fileName
	if downloaderType == configs.DownloaderBililiveRecorder {
		// BililiveRecorder 首个实际落盘文件会追加 _PART000，必须校验真实候选路径。
		pathToValidate = bililiveRecorderPart0FileName(fileName)
	}
	if err = validateOutputFilePath(pathToValidate); err != nil {
		if r.setRecordingError(err) {
			r.getLogger().WithError(err).Error("输出文件路径不兼容 Windows，已取消本次录制")
		} else {
			r.getLogger().WithError(err).Debug("输出文件路径仍不兼容 Windows，继续跳过本次录制")
		}
		return
	}
	r.clearRecordingError()
	r.saveCurrentStreamInfo(streamInfo)
	// 更新可用流信息到 info（用于API展示）
	r.updateAvailableStreams(ctx, info, streamInfos)

	// 保存原始流 URL 和 Headers（供前端调试展示）
	r.currentFileLock.Lock()
	r.currentStreamURL = url.String()
	r.currentStreamHeaders = streamInfo.HeadersForDownloader
	r.currentFileLock.Unlock()

	outputPath, _ := filepath.Split(fileName)
	if err = mkdir(outputPath); err != nil {
		r.getLogger().WithError(err).Errorf("failed to create output path[%s]", outputPath)
		return
	}
	parserCfg := map[string]string{
		"timeout_in_us": strconv.Itoa(resolvedConfig.TimeoutInUs),
		"audio_only":    strconv.FormatBool(info.AudioOnly),
	}

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
	if effectiveParserName == ffmpeg.Name {
		_, ffmpegPathErr := utils.GetFFmpegPathForLive(ctx, r.Live)
		ffmpegState := tools.GetFFmpegStatus().State
		if ffmpegPathErr != nil && resolvedConfig.FfmpegPath != "" {
			r.getLogger().WithError(ffmpegPathErr).Warn("配置的 FFmpeg 路径不可用，本次录制跳过")
			return
		}
		if ffmpegPathErr != nil && (ffmpegState == "checking" || ffmpegState == "downloading") {
			if waitErr := tools.WaitFFmpegAsyncInitDone(ctx, r.stop); waitErr != nil {
				r.getLogger().WithError(waitErr).Warn("等待 FFmpeg 就绪被中断，放弃本次录制")
				return
			}
			_, ffmpegPathErr = utils.GetFFmpegPathForLive(ctx, r.Live)
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
		// FLV 流：启动探测代理
		probeConfig := streamprobe.Config{
			UpstreamURL: url,
			Headers:     streamInfo.HeadersForDownloader,
			OnProbed: func(info *streamprobe.StreamHeaderInfo) {
				r.actualStreamInfo.Store(info)
				r.getLogger().Infof("流探测完成: 编码=%s, 分辨率=%s, 帧率=%.1f, 状态=%s",
					info.VideoCodec, info.Resolution(), info.FrameRate, info.ProbeStatus())
			},
			OnProbeError: func(err error, msg string) {
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
		if probeErr := probe.Start(ctx); probeErr != nil {
			// 探测代理启动失败不应影响录制，回退到直连上游
			r.getLogger().WithError(probeErr).Warn("流探测代理启动失败，将直接连接上游")
			r.actualStreamInfo.Store(&streamprobe.StreamHeaderInfo{
				Unsupported:    true,
				UnsupportedMsg: fmt.Sprintf("流探测代理启动失败: %v", probeErr),
			})
		} else {
			// 代理启动成功，用代理 URL 替换原始 URL
			defer probe.Stop()
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
		go func(probeCtx context.Context) {
			hlsInfo, probeErr := streamprobe.ProbeHLS(probeCtx, url, streamInfo.HeadersForDownloader, r.getLogger())
			if probeErr != nil {
				// context 取消不算真正的错误，不需要打印
				if probeCtx.Err() != nil {
					return
				}
				r.getLogger().Warnf("HLS 流探测失败: %v", probeErr)
				// 探测失败也设置一个状态，避免永远 pending
				r.actualStreamInfo.Store(&streamprobe.StreamHeaderInfo{
					Unsupported:    true,
					UnsupportedMsg: fmt.Sprintf("HLS 探测失败: %v", probeErr),
				})
				return
			}
			r.actualStreamInfo.Store(hlsInfo)
			r.getLogger().Infof("HLS 流探测完成: 编码=%s, 分辨率=%s, 帧率=%.1f",
				hlsInfo.VideoCodec, hlsInfo.Resolution(), hlsInfo.FrameRate)
		}(ctx)
	} else {
		// 其他格式：标记为不支持
		r.actualStreamInfo.Store(&streamprobe.StreamHeaderInfo{
			Unsupported:    true,
			UnsupportedMsg: "该流格式暂不支持头部数据探测",
		})
	}

	// 使用原始 URL 而非代理 URL 来判断下载器类型
	// 代理 URL 路径为 /stream，无法正确判断是否为 FLV 流
	p, err := newParser(originalURL, downloaderType, parserCfg, r.getLogger())
	if err != nil {
		r.getLogger().WithError(err).Error("failed to init parse")
		return
	}
	r.setAndCloseParser(p)
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

	r.getLogger().Debugln("Start ParseLiveStream(" + url.String() + ", " + fileName + ")")
	err = r.parser.ParseLiveStream(ctx, streamInfo, r.Live, fileName)

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

		// 先递增 pending 计数（在入队前，避免任务被异步调度完成后回调递减到 -1）
		r.pipelineState.mu.Lock()
		r.pipelineState.pendingCount++
		r.pipelineState.mu.Unlock()

		// 入队 Pipeline 任务
		if err := pipelineManager.EnqueueRecordingTask(info, pipelineConfig, outputFiles, r.onPipelineTaskComplete); err != nil {
			r.getLogger().WithError(err).Error("failed to enqueue pipeline task")
			// 入队失败，回滚计数
			r.pipelineState.mu.Lock()
			r.pipelineState.pendingCount--
			r.pipelineState.mu.Unlock()
		} else {
			r.pipelineState.enqueued = true
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
func (r *recorder) stopRetryForExplicitOffline(err error) bool {
	if !errors.Is(err, live.ErrLiveOffline) {
		return false
	}
	r.getLogger().WithError(err).Info("stream source explicitly reported offline, dispatching LiveEnd")
	r.ed.DispatchEvent(events.NewEvent(listeners.LiveEnd, r.Live))
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
	defer close(r.done)
	defer func() {
		// 标记 run() 已退出，使回调在 remaining==0 时直接发送摘要，
		// 而不是等待 r.done 通道关闭（避免 defer 执行顺序导致的漏发窗口）
		r.pipelineState.mu.Lock()
		r.pipelineState.runExited = true
		r.pipelineState.mu.Unlock()
		// 分段重启时跳过摘要：CloseForRestart 已设置此标记，
		// TransferPipelineState 完成后由回调发送含完整 Pipeline 详情的摘要
		if !r.closeForRestart.Load() {
			r.sendAccumulatedSummary()
		}
	}()

	const minRetryInterval = 5 * time.Second

	for {
		select {
		case <-r.stop:
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
					return
				case <-ctx.Done():
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
	// 分段重启屏障：等待 TransferPipelineState 完成，
	// 确保旧分段的 pipelineDetails/pendingCount 已转入再发送摘要
	if r.transferWg != nil {
		r.transferWg.Wait()
	}
	r.pipelineState.mu.Lock()
	pendingCount := r.pipelineState.pendingCount
	// 幂等保护：防止 defer 和回调并发双发
	if r.pipelineState.summarySent {
		r.pipelineState.mu.Unlock()
		return
	}
	// Pipeline 任务尚未全部完成，等待回调触发最终摘要
	if r.pipelineState.enqueued && pendingCount > 0 {
		r.pipelineState.mu.Unlock()
		r.getLogger().Infof("Pipeline 任务尚未全部完成（剩余 %d 个），延迟摘要推送", pendingCount)
		return
	}
	// 标记摘要已发送（在实际发送前，防止并发双发）
	r.pipelineState.summarySent = true
	suppressSummary := r.pipelineState.suppressSummary
	pipelineDetails := r.pipelineState.details
	pipelineSourceNames := r.pipelineState.sourceNames
	r.pipelineState.mu.Unlock()

	r.recordedFilesMu.Lock()
	defer r.recordedFilesMu.Unlock()
	if suppressSummary {
		r.getLogger().Infof("录制摘要推送已抑制（分段重启），累积 %d 个文件将传递给新 recorder", len(r.recordedFiles))
		return
	}
	if len(r.recordedFiles) == 0 && len(pipelineDetails) == 0 {
		r.getLogger().Info("无录制文件，跳过摘要推送")
		return
	}

	// 获取录制输出路径，用于查询剩余磁盘空间
	cfg := configs.GetCurrentConfig()
	outputPath := cfg.OutPutPath
	if room, roomErr := cfg.GetLiveRoomByUrl(r.Live.GetRawUrl()); roomErr == nil {
		platformKey := configs.GetPlatformKeyFromUrl(r.Live.GetRawUrl())
		resolved := cfg.ResolveConfigForRoom(room, platformKey)
		outputPath = resolved.OutPutPath
	}

	// 使用 Pipeline 收集的最终文件详情（如有），否则用录制累积的原始文件
	if len(pipelineDetails) > 0 {
		// 合并未被 Pipeline 覆盖的录制文件（如某分段因 stages==0 未入队）
		merged := append([]notify.RecordingFileDetail(nil), pipelineDetails...)
		if len(r.recordedFiles) > 0 {
			inPipeline := map[string]bool{}
			for _, d := range pipelineDetails {
				inPipeline[d.Name] = true
			}
			// 排除已被 Pipeline 转换并删除的源文件（如 convert_mp4 delete_source 场景）
			for name := range pipelineSourceNames {
				inPipeline[name] = true
			}
			for _, f := range r.recordedFiles {
				if !inPipeline[f.Name] {
					merged = append(merged, f)
				}
			}
			// 清理无关联视频的 .ass 文件（DAA/executor 可能已删除视频但 .ass 从 recordedFiles 加入）
			merged = filterOrphanedAssFromDetails(merged)
		}
		r.getLogger().Infof("推送 Pipeline 录制摘要：%d 个文件", len(merged))
		hostName := ""
		platform := r.Live.GetPlatformCNName()
		if obj, cacheErr := r.cache.Get(r.Live); cacheErr == nil {
			if info, ok := obj.(*live.Info); ok {
				hostName = info.HostName
			}
		}
		notify.SendPipelineRecordingSummary(
			r.getLogger(),
			hostName,
			platform,
			r.recordedFiles,     // originalFiles（allUploaded 时显示用）
			merged,              // finalFiles（含未入队分段的文件）
			outputPath,
		)
	} else {
		// Pipeline 未产出有效文件详情，回退到录制累积
		if len(r.recordedFiles) == 0 {
			return
		}
		obj, err := r.cache.Get(r.Live)
		if err != nil {
			r.getLogger().WithError(err).Error("获取直播信息失败，无法推送录制摘要")
			return
		}
		info := obj.(*live.Info)
		r.getLogger().Infof("推送录制摘要：%d 个文件", len(r.recordedFiles))
		notify.SendRecordingSummary(r.getLogger(), info.HostName, r.Live.GetPlatformCNName(), r.recordedFiles, outputPath)
	}
}

// onPipelineTaskComplete Pipeline 任务完成回调
// 由 Pipeline Manager 在 executeTask 结束时调用
func (r *recorder) onPipelineTaskComplete(task *pipeline.PipelineTask) {
	r.getLogger().Infof("Pipeline 回调触发：状态=%s, CurrentFiles=%d, LastStageFiles=%d, InitialFiles=%d",
		task.Status, len(task.CurrentFiles), len(task.LastStageFiles), len(task.InitialFiles))
	// 沿 redirect 链找到当前活跃的共享状态
	// 支持连续重启 A→B→C 场景：A 的回调通过 resolveState 找到 C 的状态
	ps := resolveState(r.pipelineState)
	// ps.mu 已被 resolveState 锁住

	ps.pendingCount--

	// 收集任务的最终文件详情
	switch task.Status {
	case pipeline.PipelineStatusCompleted:
		// 始终从 LastStageFiles 提取已上传的文件（含 Metadata["uploaded"] 标记）
		uploadedDetails := extractUploadedDetails(task.LastStageFiles)
		if len(uploadedDetails) > 0 {
			ps.details = append(ps.details, uploadedDetails...)
		}
		// 合并本地保留的文件（CurrentFiles 中未被上传标记覆盖的文件）
		uploadedNames := map[string]bool{}
		for _, d := range uploadedDetails {
			uploadedNames[d.Name] = true
		}
		for _, d := range pipeline.FileInfoToDetails(task.CurrentFiles) {
			if !uploadedNames[d.Name] {
				ps.details = append(ps.details, d)
			}
		}
		// 回退：无上传标记且无保留文件时用 InitialFiles
		if len(uploadedDetails) == 0 && len(task.CurrentFiles) == 0 {
			for _, d := range pipeline.FileInfoToDetails(task.InitialFiles) {
				d.Uploaded = true
				ps.details = append(ps.details, d)
			}
		}
	case pipeline.PipelineStatusFailed, pipeline.PipelineStatusCancelled:
		// 失败/取消时优先用 CurrentFiles（最后成功阶段的输出），回退到 InitialFiles
		if len(task.CurrentFiles) > 0 {
			ps.details = append(ps.details, pipeline.FileInfoToDetails(task.CurrentFiles)...)
		} else {
			ps.details = append(ps.details, pipeline.FileInfoToDetails(task.InitialFiles)...)
		}
	}

	// 收集被 Pipeline 转换的原始文件名（SourcePath），用于摘要合并时排除已删除的源文件
	collectSourceNames(ps.sourceNames, task.CurrentFiles, task.LastStageFiles)

	remaining := ps.pendingCount
	runExited := ps.runExited
	onAllDone := ps.onAllTasksDone

	ps.mu.Unlock()

	r.getLogger().Infof("Pipeline 任务完成（状态: %s），剩余 %d 个任务", task.Status, remaining)

	// 所有任务完成且录制已结束，发送统一摘要
	// 使用 goroutine 避免通知发送（Telegram/Email 等）阻塞 Pipeline 任务槽位
	if remaining == 0 {
		if onAllDone != nil {
			// 分段重启场景：通过共享回调触发摘要（回调内部检查 runExited）
			onAllDone()
		} else if runExited {
			// 非分段重启场景：run() 已退出，异步发送摘要
			if r.notifyWg != nil {
				r.notifyWg.Add(1)
			}
			go func() {
				defer func() {
					if r.notifyWg != nil {
						r.notifyWg.Done()
					}
				}()
				r.sendAccumulatedSummary()
			}()
		}
		// run() 仍在运行且无回调：等待 run() 退出时由 defer 中的 sendAccumulatedSummary 发送
	}
}

// extractUploadedDetails 从最后阶段输出文件中提取有上传标记的文件详情
// CloudUploadStage 会为成功上传的文件设置 Metadata["uploaded"]=true
// 排除封面文件（封面不在录制摘要中展示，与 FileInfoToDetails 保持一致）
func extractUploadedDetails(files []pipeline.FileInfo) []notify.RecordingFileDetail {
	var details []notify.RecordingFileDetail
	for _, f := range files {
		if f.Type == pipeline.FileTypeCover {
			continue
		}
		uploaded := false
		if f.Metadata != nil {
			if u, ok := f.Metadata["uploaded"].(bool); ok && u {
				uploaded = true
			}
		}
		if uploaded && f.Size > 0 {
			details = append(details, notify.RecordingFileDetail{
				Name:     filepath.Base(f.Path),
				Size:     f.Size,
				Uploaded: true,
			})
		}
	}
	logrus.WithFields(logrus.Fields{
		"input_count":    len(files),
		"uploaded_count": len(details),
	}).Debug("extractUploadedDetails")
	return details
}

// collectSourceNames 从 Pipeline 输出文件中收集被转换的原始文件名
// 当 convert_mp4/burn_subtitles 等阶段转换文件时，输出文件的 SourcePath 指向原始文件
// 这些原始文件可能已被 Pipeline 删除，摘要合并时需要排除，避免将已删除的源文件重新纳入
func collectSourceNames(sourceNames map[string]bool, fileSets ...[]pipeline.FileInfo) {
	for _, files := range fileSets {
		for _, f := range files {
			if f.SourcePath != "" {
				sourceNames[filepath.Base(f.SourcePath)] = true
			}
		}
	}
}

// filterOrphanedAssFromDetails 从文件详情列表中移除无关联视频的 .ass 文件
// 当 DAA/executor 删除了视频但 .ass 从 recordedFiles 加入 merged 时，
// 需要清理这些孤立的 .ass，避免通知误显为本地保留
func filterOrphanedAssFromDetails(details []notify.RecordingFileDetail) []notify.RecordingFileDetail {
	videoExts := map[string]bool{".flv": true, ".mp4": true, ".mkv": true, ".ts": true}
	videoBases := map[string]bool{}
	for _, d := range details {
		ext := strings.ToLower(filepath.Ext(d.Name))
		if videoExts[ext] {
			base := strings.TrimSuffix(d.Name, ext)
			videoBases[base] = true
		}
	}
	var result []notify.RecordingFileDetail
	for _, d := range details {
		ext := strings.ToLower(filepath.Ext(d.Name))
		if ext == ".ass" {
			base := strings.TrimSuffix(d.Name, ".ass")
			if !videoBases[base] {
				continue // 无关联视频，跳过
			}
		}
		result = append(result, d)
	}
	return result
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

func (r *recorder) setAndCloseParser(p parser.Parser) {
	r.parserLock.Lock()
	defer r.parserLock.Unlock()
	if r.parser != nil {
		if err := r.parser.Stop(); err != nil {
			r.getLogger().WithError(err).Warn("failed to end recorder")
		}
	}
	r.parser = p
}

func (r *recorder) Start(ctx context.Context) error {
	if !atomic.CompareAndSwapUint32(&r.state, begin, pending) {
		return nil
	}
	bilisentry.GoWithContext(ctx, func(ctx context.Context) { r.run(ctx) })
	r.getLogger().Info("Record Start ", r.Live.GetRawUrl())
	r.ed.DispatchEvent(events.NewEvent(RecorderStart, r.Live))
	atomic.CompareAndSwapUint32(&r.state, pending, running)
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
	if !atomic.CompareAndSwapUint32(&r.state, running, stopped) {
		return
	}
	close(r.stop)
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
	r.getLogger().Info("Record End")
	r.ed.DispatchEvent(events.NewEvent(RecorderStop, r.Live))
}

func (r *recorder) CloseForRestart() []notify.RecordingFileDetail {
	// 先设置重启标记，使 run() 的 defer 跳过 sendAccumulatedSummary，
	// 避免在 TransferPipelineState 之前发送不含 Pipeline 详情的摘要
	r.closeForRestart.Store(true)
	r.pipelineState.mu.Lock()
	r.pipelineState.suppressSummary = true
	r.pipelineState.mu.Unlock()
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

// TransferPipelineState 从旧 recorder 转移 Pipeline 状态
// 分段重启时由 RestartRecorder 调用，确保旧分段的 Pipeline 结果不丢失
// 使用 redirect 链替代指针覆盖，支持连续重启 A→B→C 场景
func (r *recorder) TransferPipelineState(old *recorder) {
	r.pipelineState.mu.Lock()
	defer r.pipelineState.mu.Unlock()

	// 沿 old 的 redirect 链找到最终活跃状态（可能经过多次重启 A→B→C→...）
	oldState := resolveState(old.pipelineState)
	// 此时 oldState.mu 已被 resolveState 锁住

	// 合并 Pipeline 状态
	r.pipelineState.enqueued = r.pipelineState.enqueued || oldState.enqueued
	r.pipelineState.pendingCount += oldState.pendingCount
	// 注意：suppressSummary 不合并。该标志表示"我是被重启的 recorder，不发摘要"，
	// 新 recorder 从未调用 CloseForRestart，应保持自己的 suppressSummary=false。
	// 回调通过 onAllTasksDone（含 runExited 检查）触发，不依赖 suppressSummary 传播。

	// 合并文件详情（旧 recorder 的详情追加到新 recorder）
	r.pipelineState.details = append(r.pipelineState.details, oldState.details...)

	// 合并已被 Pipeline 消费的原始文件名
	for name := range oldState.sourceNames {
		r.pipelineState.sourceNames[name] = true
	}

	// 设置回调：旧任务全部完成时，触发摘要发送
	// 回调与 suppressSummary 解耦：suppressSummary 是 recorder 级别的抑制标志，
	// 回调是 pipeline 级别的完成通知。多跳 A→B→C 场景下，A 的回调通过 redirect
	// 链解析到 C 的状态，如果只在 suppressSummary=true 时设置回调，B→C 转移时
	// B.suppressSummary=false 导致 C 没有回调，A 的摘要永远发不出。
	// runExited 检查确保只在新 recorder 已退出时发送，避免录制中提前发出摘要。
	r.pipelineState.onAllTasksDone = func() {
		r.pipelineState.mu.Lock()
		shouldSend := r.pipelineState.runExited
		r.pipelineState.mu.Unlock()
		if shouldSend {
			if r.notifyWg != nil {
				r.notifyWg.Add(1)
			}
			go func() {
				defer func() {
					if r.notifyWg != nil {
						r.notifyWg.Done()
					}
				}()
				r.sendAccumulatedSummary()
			}()
		}
		// run() 未退出时不发送：新 recorder 的 defer 会在退出时检查并发送
	}

	// 关键：设置 redirect 链，使旧 recorder 及更早的 recorder 的回调
	// 能通过 resolveState 找到当前活跃的共享状态
	oldState.redirectedTo.Store(r.pipelineState)
	oldState.mu.Unlock()

	r.getLogger().Infof("继承 Pipeline 状态：pending=%d, details=%d 个文件",
		r.pipelineState.pendingCount, len(r.pipelineState.details))
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

// getCurrentFilePath 获取当前正在录制的文件路径
func (r *recorder) getCurrentFilePath() string {
	r.currentFileLock.RLock()
	defer r.currentFileLock.RUnlock()
	return r.currentFilePath
}

// RecordingError 返回当前阻止录制启动的永久性错误，供 API 向前端展示。
func (r *recorder) RecordingError() string {
	r.recordingErrorMu.RLock()
	defer r.recordingErrorMu.RUnlock()
	return r.recordingError
}

// setRecordingError 保存错误，并返回错误内容是否发生变化。
func (r *recorder) setRecordingError(err error) bool {
	message := ""
	if err != nil {
		message = err.Error()
	}

	r.recordingErrorMu.Lock()
	defer r.recordingErrorMu.Unlock()
	if r.recordingError == message {
		return false
	}
	r.recordingError = message
	return true
}

func (r *recorder) clearRecordingError() {
	r.setRecordingError(nil)
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
	if recordingError := r.RecordingError(); recordingError != "" {
		status["recording_error"] = recordingError
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
	bilisentry.GoWithContext(ctx, func(ctx context.Context) {
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
