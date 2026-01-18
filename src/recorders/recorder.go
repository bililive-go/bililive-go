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
	GetStatus() (map[string]string, error)
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
	streamInfo := streamInfos[0]
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
	r.getLogger().Info("Record Start")
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

func (r *recorder) GetStatus() (map[string]string, error) {
	statusP, ok := r.getParser().(parser.StatusParser)
	if !ok {
		return nil, ErrParserNotSupportStatus
	}
	status, err := statusP.Status()
	if err != nil {
		return nil, err
	}
	if status == nil {
		status = make(map[string]string)
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

// renderUploadPath 渲染上传路径模板
// 支持的变量: {{ .Platform }}, {{ .HostName }}, {{ .RoomName }}, {{ .Ext }}, {{ now | date "2006-01-02" }}
func (r *recorder) renderUploadPath(tmplStr string, info *live.Info, localFile string, cfg *configs.Config) string {
	if tmplStr == "" {
		return ""
	}

	// 获取文件扩展名
	ext := filepath.Ext(localFile)
	if len(ext) > 0 && ext[0] == '.' {
		ext = ext[1:] // 移除前导点
	}

	// 创建模板数据
	data := struct {
		Platform string
		HostName string
		RoomName string
		Ext      string
		FileName string // 原始文件名（不含路径）
	}{
		Platform: info.Live.GetPlatformCNName(),
		HostName: info.HostName,
		RoomName: info.RoomName,
		Ext:      ext,
		FileName: filepath.Base(localFile),
	}

	// 解析模板
	tmpl, err := template.New("upload_path").Funcs(utils.GetFuncMap(cfg)).Parse(tmplStr)
	if err != nil {
		r.getLogger().WithError(err).Error("failed to parse upload path template")
		return ""
	}

	// 执行模板
	buf := new(bytes.Buffer)
	if err := tmpl.Execute(buf, data); err != nil {
		r.getLogger().WithError(err).Error("failed to render upload path template")
		return ""
	}

	return buf.String()
}
