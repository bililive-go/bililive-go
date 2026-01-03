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
	"github.com/bililive-go/bililive-go/src/pkg/events"
	"github.com/bililive-go/bililive-go/src/pkg/livelogger"
	"github.com/bililive-go/bililive-go/src/pkg/parser"
	"github.com/bililive-go/bililive-go/src/pkg/parser/ffmpeg"
	"github.com/bililive-go/bililive-go/src/pkg/parser/native/flv"
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
	newParser = func(u *url.URL, useNativeFlvParser bool, cfg map[string]string, logger *livelogger.LiveLogger) (parser.Parser, error) {
		parserName := ffmpeg.Name
		if strings.Contains(u.Path, ".flv") && useNativeFlvParser {
			parserName = flv.Name
		}
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
	}
	p, err := newParser(url, resolvedConfig.Feature.UseNativeFlvParser, parserCfg, r.getLogger())
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
		// 使用任务队列处理后处理任务
		inst := instance.GetInstance(ctx)
		enqueuer := inst.TaskEnqueuer
		if enqueuer == nil {
			r.getLogger().Warn("task queue not available, skipping post-processing")
			return
		}

		if resolvedConfig.OnRecordFinished.FixFlvAtFirst {
			if err := enqueuer.EnqueueFixFlvTask(fileName); err != nil {
				r.getLogger().WithError(err).Error("failed to enqueue fix flv task")
			} else {
				r.getLogger().Infof("fix flv task enqueued: %s", fileName)
			}
		}

		if resolvedConfig.OnRecordFinished.ConvertToMp4 {
			if err := enqueuer.EnqueueConvertMp4Task(fileName, resolvedConfig.OnRecordFinished.DeleteFlvAfterConvert); err != nil {
				r.getLogger().WithError(err).Error("failed to enqueue convert mp4 task")
			} else {
				r.getLogger().Infof("convert mp4 task enqueued: %s", fileName)
			}
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
	go r.run(ctx)
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
