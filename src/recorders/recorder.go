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
	"github.com/bililive-go/bililive-go/src/pipeline"
	"github.com/bililive-go/bililive-go/src/pkg/events"
	"github.com/bililive-go/bililive-go/src/pkg/livelogger"
	"github.com/bililive-go/bililive-go/src/pkg/parser"
	"github.com/bililive-go/bililive-go/src/pkg/parser/bililive_recorder"
	"github.com/bililive-go/bililive-go/src/pkg/parser/ffmpeg"
	"github.com/bililive-go/bililive-go/src/pkg/parser/native/flv"
	bilisentry "github.com/bililive-go/bililive-go/src/pkg/sentry"
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
	// RequestSegment 请求在下一个关键帧处分段
	// 仅在使用 FLV 代理时有效
	// 返回 true 表示请求已接受，false 表示不支持或请求被拒绝
	RequestSegment() bool
	// HasFlvProxy 检查当前是否使用 FLV 代理
	HasFlvProxy() bool
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

	// 当前录制的流信息
	currentStreamInfo *live.AvailableStreamInfo
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
		parserLock: new(sync.RWMutex),
	}, nil
}

func (r *recorder) tryRecord(ctx context.Context) {
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
		time.Sleep(5 * time.Second)
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

	p, err := newParser(url, downloaderType, parserCfg, r.getLogger())
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
	for {
		select {
		case <-r.stop:
			return
		default:
			r.tryRecord(ctx)
		}
	}
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
	statusP, ok := r.getParser().(parser.StatusParser)
	if !ok {
		return nil, ErrParserNotSupportStatus
	}
	status, err := statusP.Status()
	if err != nil {
		return nil, err
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

		saveCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()

		if err := saver.SaveAvailableStreamsAny(saveCtx, string(r.Live.GetLiveId()), data); err != nil {
			r.getLogger().Warnf("保存可用流信息到数据库失败: %v", err)
		}
	}
}
