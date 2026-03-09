//go:generate go run go.uber.org/mock/mockgen -package recorders -destination mock_test.go github.com/bililive-go/bililive-go/src/recorders Recorder,Manager
package recorders

import (
	"bytes"
	"context"
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

	"github.com/bililive-go/bililive-go/src/configs"
	"github.com/bililive-go/bililive-go/src/instance"
	"github.com/bililive-go/bililive-go/src/live"
	"github.com/bililive-go/bililive-go/src/notify"
	"github.com/bililive-go/bililive-go/src/pipeline"
	"github.com/bililive-go/bililive-go/src/pkg/events"
	"github.com/bililive-go/bililive-go/src/pkg/livelogger"
	"github.com/bililive-go/bililive-go/src/pkg/parser"
	"github.com/bililive-go/bililive-go/src/pkg/parser/bililive_recorder"
	"github.com/bililive-go/bililive-go/src/pkg/parser/ffmpeg"
	"github.com/bililive-go/bililive-go/src/pkg/parser/native/flv"
	bilisentry "github.com/bililive-go/bililive-go/src/pkg/sentry"
	"github.com/bililive-go/bililive-go/src/pkg/streamprobe"
	"github.com/bililive-go/bililive-go/src/pkg/utils"
)

const (
	begin uint32 = iota
	pending
	running
	stopped
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
)

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

type recorder struct {
	Live       live.Live
	ed         events.Dispatcher
	cache      gcache.Cache
	startTime  time.Time
	parser     parser.Parser
	parserLock *sync.RWMutex

	stop  chan struct{}
	state uint32

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
	recordedFiles []notify.RecordingFileDetail

	// done 在 run() 退出时关闭，用于 CloseForRestart 等待 goroutine 完成
	done chan struct{}
	// suppressSummary 为 true 时，run() 退出不推送摘要（分段重启场景）
	suppressSummary bool
}

func NewRecorder(ctx context.Context, live live.Live) (Recorder, error) {
	inst := instance.GetInstance(ctx)

	return &recorder{
		Live:       live,
		cache:      inst.Cache,
		startTime:  time.Now(),
		ed:         inst.EventDispatcher.(events.Dispatcher),
		state:      begin,
		stop:       make(chan struct{}),
		done:       make(chan struct{}),
		parserLock: new(sync.RWMutex),
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
		r.getLogger().WithError(err).Warn("failed to get stream url, will retry after 5s...")
		// 使用可中断的等待，确保 Ctrl+C 能立即响应
		select {
		case <-ctx.Done():
		case <-r.stop:
		case <-time.After(5 * time.Second):
		}
		return
	}

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
		panic(fmt.Sprintf("failed to render filename, err: %v", err))
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

	// 设置当前录制文件路径
	r.setCurrentFilePath(fileName)

	r.getLogger().Debugln("Start ParseLiveStream(" + url.String() + ", " + fileName + ")")
	err = r.parser.ParseLiveStream(ctx, streamInfo, r.Live, fileName)

	// 清除当前录制文件路径
	r.setCurrentFilePath("")

	if err != nil {
		r.getLogger().WithError(err).Error("failed to parse live stream")
		return
	}
	r.getLogger().Debugln("End ParseLiveStream(" + url.String() + ", " + fileName + ")")
	removeEmptyFile(fileName)

	// 使用层级配置的 OnRecordFinished
	cmdStr := strings.Trim(resolvedConfig.OnRecordFinished.CustomCommandline, "")
	if len(cmdStr) > 0 {
		// 累积录制文件信息（legacy 路径），待录制结束后统一推送摘要
		r.accumulateRecordedFiles(fileName)

		ffmpegPath, ffmpegErr := utils.GetFFmpegPathForLive(ctx, r.Live)
		if ffmpegErr != nil {
			r.getLogger().WithError(ffmpegErr).Error("failed to find ffmpeg")
			return
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
			r.getLogger().Infof("pipeline task enqueued: %d files, %d stages", len(outputFiles), len(pipelineConfig.Stages))
		}
	}
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
	defer r.sendAccumulatedSummary()

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
	r.getLogger().Info("Record End")
	r.ed.DispatchEvent(events.NewEvent(RecorderStop, r.Live))
}

func (r *recorder) CloseForRestart() []notify.RecordingFileDetail {
	r.suppressSummary = true
	r.Close()
	<-r.done // 等待 run() 完全退出，确保最后一个文件已累积
	r.getLogger().Infof("分段重启：携带 %d 个累积文件传递给新 recorder", len(r.recordedFiles))
	return r.recordedFiles
}

func (r *recorder) SetInitialRecordedFiles(files []notify.RecordingFileDetail) {
	r.recordedFiles = append(files, r.recordedFiles...)
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
