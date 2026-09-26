package recorders

import (
	"context"
	"errors"
	"net/url"
	"strings"
	"sync"
	"testing"

	"github.com/bluele/gcache"
	"github.com/sirupsen/logrus"
	gomock "go.uber.org/mock/gomock"

	"github.com/bililive-go/bililive-go/src/configs"
	"github.com/bililive-go/bililive-go/src/instance"
	"github.com/bililive-go/bililive-go/src/live"
	livemock "github.com/bililive-go/bililive-go/src/live/mock"
	"github.com/bililive-go/bililive-go/src/notify"
	"github.com/bililive-go/bililive-go/src/pipeline"
	"github.com/bililive-go/bililive-go/src/pkg/events"
	eventsmock "github.com/bililive-go/bililive-go/src/pkg/events/mock"
	"github.com/bililive-go/bililive-go/src/pkg/livelogger"
	"github.com/bililive-go/bililive-go/src/types"
)

func TestTryRecordStopsWithoutPanicWhenFilenameRenderFails(t *testing.T) {
	previousConfig := configs.GetCurrentConfig()
	cfg := configs.NewConfig()
	cfg.OutputTmpl = `{{ fail "render failed" }}`
	configs.SetCurrentConfig(cfg)
	t.Cleanup(func() {
		configs.SetCurrentConfig(previousConfig)
	})

	ctrl := gomock.NewController(t)
	l := livemock.NewMockLive(ctrl)
	logger := livelogger.New(0, logrus.Fields{"test": t.Name()})
	streamURL := &url.URL{Scheme: "https", Host: "example.com", Path: "/stream.flv"}

	l.EXPECT().GetRawUrl().Return("https://example.com/room").AnyTimes()
	l.EXPECT().GetStreamInfos().Return([]*live.StreamUrlInfo{{Url: streamURL}}, nil)
	l.EXPECT().GetLogger().Return(logger)

	// 渲染失败时 recorder 应请求 manager 仅回收自身，而不是错误派发 LiveEnd。
	ed := eventsmock.NewMockDispatcher(ctrl)
	ed.EXPECT().DispatchEvent(events.NewEvent(RecorderStopRequested, l))

	cache := gcache.New(1).LRU().Build()
	if err := cache.Set(l, &live.Info{Live: l}); err != nil {
		t.Fatalf("写入直播信息缓存失败: %v", err)
	}
	r := &recorder{Live: l, cache: cache, ed: ed}

	r.tryRecord(context.Background())

	logs := logger.GetLogs()
	if !strings.Contains(logs, "failed to render filename, stopping recorder") {
		t.Fatalf("未记录文件名渲染失败日志: %s", logs)
	}
	if !strings.Contains(logs, "render failed") {
		t.Fatalf("日志未保留原始模板错误: %s", logs)
	}
}

func TestTryRecordStopsBeforeCreatingInvalidOutputPath(t *testing.T) {
	previousConfig := configs.GetCurrentConfig()
	previousValidator := validateOutputFilePath
	previousMkdir := mkdir
	cfg := configs.NewConfig()
	cfg.OutputTmpl = `recording.flv`
	cfg.Feature.DownloaderType = configs.DownloaderBililiveRecorder
	configs.SetCurrentConfig(cfg)

	mkdirCalled := false
	validatedPath := ""
	validateOutputFilePath = func(path string) error {
		validatedPath = path
		return errors.New("输出文件完整路径过长")
	}
	mkdir = func(string) error {
		mkdirCalled = true
		return nil
	}
	t.Cleanup(func() {
		configs.SetCurrentConfig(previousConfig)
		validateOutputFilePath = previousValidator
		mkdir = previousMkdir
	})

	ctrl := gomock.NewController(t)
	l := livemock.NewMockLive(ctrl)
	logger := livelogger.New(0, logrus.Fields{"test": t.Name()})
	streamURL := &url.URL{Scheme: "https", Host: "example.com", Path: "/stream.flv"}

	l.EXPECT().GetRawUrl().Return("https://example.com/room").AnyTimes()
	l.EXPECT().GetStreamInfos().Return([]*live.StreamUrlInfo{{Url: streamURL}}, nil).Times(2)
	l.EXPECT().GetLogger().Return(logger).AnyTimes()

	cache := gcache.New(1).LRU().Build()
	if err := cache.Set(l, &live.Info{Live: l, AudioOnly: true}); err != nil {
		t.Fatalf("写入直播信息缓存失败: %v", err)
	}
	r := &recorder{Live: l, cache: cache, parserLock: new(sync.RWMutex)}

	r.tryRecord(context.Background())
	r.tryRecord(context.Background())

	if mkdirCalled {
		t.Fatal("路径校验失败后不应创建输出目录")
	}
	if !strings.HasSuffix(validatedPath, "recording_PART000.aac") {
		t.Fatalf("应校验最终扩展名和 BililiveRecorder 分段后缀，实际路径为 %s", validatedPath)
	}
	logs := logger.GetLogs()
	if !strings.Contains(logs, "输出文件路径不兼容 Windows，已取消本次录制") {
		t.Fatalf("未记录路径校验失败日志: %s", logs)
	}
	if !strings.Contains(logs, "输出文件完整路径过长") {
		t.Fatalf("日志未保留路径校验错误: %s", logs)
	}
	if count := strings.Count(logs, "输出文件路径不兼容 Windows，已取消本次录制"); count != 1 {
		t.Fatalf("相同永久错误应只记录一次 Error，实际记录 %d 次: %s", count, logs)
	}
	status, err := r.GetStatus()
	if err != nil {
		t.Fatalf("获取录制状态失败: %v", err)
	}
	if status["recording_error"] != "输出文件完整路径过长" {
		t.Fatalf("前端状态未包含路径错误: %#v", status)
	}
}

func TestReplaceOutputFileExtensionUsesFinalExtension(t *testing.T) {
	tests := []struct {
		name      string
		fileName  string
		extension string
		want      string
	}{
		{name: "FLV 转 TS", fileName: "recording.flv", extension: ".ts", want: "recording.ts"},
		{name: "短扩展名转 AAC", fileName: "recording.x", extension: ".aac", want: "recording.aac"},
		{name: "无扩展名追加 AAC", fileName: "recording", extension: ".aac", want: "recording.aac"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := replaceOutputFileExtension(tt.fileName, tt.extension); got != tt.want {
				t.Fatalf("替换扩展名结果期望 %q，实际为 %q", tt.want, got)
			}
		})
	}
}

func TestBililiveRecorderPart0FileName(t *testing.T) {
	got := bililiveRecorderPart0FileName("recording.flv")
	if got != "recording_PART000.flv" {
		t.Fatalf("首个分段文件名期望 recording_PART000.flv，实际为 %s", got)
	}
}

func TestRecordingErrorState(t *testing.T) {
	r := &recorder{}
	err := errors.New("路径过长")
	if !r.setRecordingError(err) {
		t.Fatal("首次设置录制错误应报告状态变化")
	}
	if r.setRecordingError(err) {
		t.Fatal("重复设置相同录制错误不应报告状态变化")
	}
	if got := r.RecordingError(); got != err.Error() {
		t.Fatalf("录制错误期望 %q，实际为 %q", err, got)
	}
	r.clearRecordingError()
	if got := r.RecordingError(); got != "" {
		t.Fatalf("清除后录制错误应为空，实际为 %q", got)
	}
}

// newTestRecorder 创建用于测试的 recorder 实例（不启动 run goroutine）
func newTestRecorder(t *testing.T, liveId types.LiveID) *recorder {
	t.Helper()
	ctrl := gomock.NewController(t)
	l := livemock.NewMockLive(ctrl)
	l.EXPECT().GetLiveId().Return(liveId).AnyTimes()
	l.EXPECT().GetLogger().Return(livelogger.New(0, logrus.Fields{"test": t.Name()})).AnyTimes()

	// 创建带 instance 的 context（NewRecorder 需要 instance.Cache 和 EventDispatcher）
	inst := &instance.Instance{
		Cache: gcache.New(1).LRU().Build(),
	}
	ctx := context.WithValue(context.Background(), instance.Key, inst)
	// NewDispatcher 会自动设置 inst.EventDispatcher
	events.NewDispatcher(ctx)

	r, err := NewRecorder(ctx, l)
	if err != nil {
		t.Fatalf("创建 recorder 失败: %v", err)
	}
	return r.(*recorder)
}

func TestResolveState_NoRedirect(t *testing.T) {
	state := &pipelineSharedState{
		sourceNames:  make(map[string]bool),
		pendingCount: 5,
	}

	resolved := resolveState(state)
	if resolved != state {
		t.Fatal("无 redirect 时 resolveState 应返回原始状态")
	}
	if resolved.pendingCount != 5 {
		t.Fatalf("pendingCount 期望 5，实际 %d", resolved.pendingCount)
	}
	resolved.mu.Unlock()
}

func TestResolveState_SingleRedirect(t *testing.T) {
	stateA := &pipelineSharedState{sourceNames: make(map[string]bool), pendingCount: 3}
	stateB := &pipelineSharedState{sourceNames: make(map[string]bool), pendingCount: 0}

	// 模拟 A→B 重定向
	stateA.redirectedTo.Store(stateB)

	resolved := resolveState(stateA)
	if resolved != stateB {
		t.Fatal("单跳 redirect 应解析到 stateB")
	}
	resolved.mu.Unlock()
}

func TestResolveState_MultiHopChain(t *testing.T) {
	stateA := &pipelineSharedState{sourceNames: make(map[string]bool), pendingCount: 3}
	stateB := &pipelineSharedState{sourceNames: make(map[string]bool), pendingCount: 2}
	stateC := &pipelineSharedState{sourceNames: make(map[string]bool), pendingCount: 1}

	// 模拟 A→B→C 链
	stateA.redirectedTo.Store(stateB)
	stateB.redirectedTo.Store(stateC)

	resolved := resolveState(stateA)
	if resolved != stateC {
		t.Fatal("多跳 redirect 链应解析到 stateC")
	}
	resolved.mu.Unlock()
}

func TestTransferPipelineState_ChainRedirect(t *testing.T) {
	// 模拟 A→B→C 连续分段重启
	recA := newTestRecorder(t, "test-live")
	recB := newTestRecorder(t, "test-live")
	recC := newTestRecorder(t, "test-live")

	// A 有 3 个待处理任务和一些文件详情
	recA.pipelineState.pendingCount = 3
	recA.pipelineState.enqueued = true
	recA.pipelineState.details = []notify.RecordingFileDetail{
		{Name: "a.flv", Size: 100},
	}
	recA.pipelineState.sourceNames["a_src.flv"] = true
	// A 模拟已抑制（CloseForRestart 设置）
	recA.pipelineState.suppressSummary = true

	// A → B 重启
	recB.TransferPipelineState(recA)

	// 验证 B 继承了 A 的状态
	if recB.pipelineState.pendingCount != 3 {
		t.Fatalf("B pendingCount 期望 3，实际 %d", recB.pipelineState.pendingCount)
	}
	if len(recB.pipelineState.details) != 1 {
		t.Fatalf("B details 期望 1，实际 %d", len(recB.pipelineState.details))
	}

	// B 有额外的 2 个任务
	recB.pipelineState.pendingCount += 2 // 模拟 B 自己又入队了 2 个任务

	// B → C 重启
	recC.TransferPipelineState(recB)

	// 验证 C 继承了所有状态
	if recC.pipelineState.pendingCount != 5 {
		t.Fatalf("C pendingCount 期望 5，实际 %d", recC.pipelineState.pendingCount)
	}

	// 关键验证：A 的 pipelineState 通过 redirect 链应解析到 C 的状态
	resolvedA := resolveState(recA.pipelineState)
	if resolvedA != recC.pipelineState {
		t.Fatal("A 的状态应通过 redirect 链解析到 C 的状态")
	}
	resolvedA.mu.Unlock()

	// B 的 pipelineState 应直接指向 C
	resolvedB := resolveState(recB.pipelineState)
	if resolvedB != recC.pipelineState {
		t.Fatal("B 的状态应解析到 C 的状态")
	}
	resolvedB.mu.Unlock()
}

func TestOnPipelineTaskComplete_AfterChainRedirect(t *testing.T) {
	// 模拟 A→B→C 后，A 的回调操作 C 的状态
	recA := newTestRecorder(t, "test-live")
	recB := newTestRecorder(t, "test-live")
	recC := newTestRecorder(t, "test-live")

	// A 有 1 个待处理任务
	recA.pipelineState.pendingCount = 1
	recA.pipelineState.enqueued = true
	recA.pipelineState.suppressSummary = true
	recA.pipelineState.runExited = true

	// A → B → C
	recB.TransferPipelineState(recA)
	recC.TransferPipelineState(recB)

	// 模拟 C 的 run() 已退出（C 是当前活跃 recorder，runExited 是 per-recorder 的）
	recC.pipelineState.mu.Lock()
	recC.pipelineState.runExited = true
	recC.pipelineState.mu.Unlock()

	// C 的 onAllTasksDone 回调应存在（TransferPipelineState 始终设置）
	onAllDoneCalled := false
	recC.pipelineState.onAllTasksDone = func() {
		recC.pipelineState.mu.Lock()
		shouldSend := recC.pipelineState.runExited
		recC.pipelineState.mu.Unlock()
		if shouldSend {
			onAllDoneCalled = true
		}
	}

	// 模拟 A 的 Pipeline 任务完成
	task := &pipeline.PipelineTask{
		Status:       pipeline.PipelineStatusCompleted,
		CurrentFiles: []pipeline.FileInfo{},
		InitialFiles: []pipeline.FileInfo{
			{Path: "/data/a.flv", Size: 1024, Type: pipeline.FileTypeVideo},
		},
		LastStageFiles: []pipeline.FileInfo{
			{Path: "/data/a.flv", Size: 1024, Type: pipeline.FileTypeVideo, Metadata: map[string]any{"uploaded": true}},
		},
	}

	recA.onPipelineTaskComplete(task)

	// 验证：C 的 pendingCount 应为 0（A 的回调正确递减了 C 的计数）
	if recC.pipelineState.pendingCount != 0 {
		t.Fatalf("C pendingCount 期望 0，实际 %d（A 的回调未正确操作 C 的状态）", recC.pipelineState.pendingCount)
	}

	// 验证：A 的回调收集的 details 应在 C 的状态中
	if len(recC.pipelineState.details) == 0 {
		t.Fatal("C 的 details 不应为空（A 的回调应将文件详情写入 C 的状态）")
	}

	// 验证：onAllTasksDone 应被触发（C 已退出）
	if !onAllDoneCalled {
		t.Fatal("onAllTasksDone 回调应被触发（C.runExited=true）")
	}
}

func TestExtractUploadedDetails_ExcludesCover(t *testing.T) {
	files := []pipeline.FileInfo{
		{Path: "/data/video.flv", Size: 1024, Type: pipeline.FileTypeVideo, Metadata: map[string]any{"uploaded": true}},
		{Path: "/data/cover.jpg", Size: 512, Type: pipeline.FileTypeCover, Metadata: map[string]any{"uploaded": true}},
		{Path: "/data/other.txt", Size: 256, Type: pipeline.FileTypeOther, Metadata: map[string]any{"uploaded": true}},
	}

	details := extractUploadedDetails(files)

	if len(details) != 2 {
		t.Fatalf("期望 2 个文件（排除封面），实际 %d", len(details))
	}

	names := map[string]bool{}
	for _, d := range details {
		names[d.Name] = true
	}
	if names["cover.jpg"] {
		t.Fatal("封面文件不应出现在上传详情中")
	}
	if !names["video.flv"] || !names["other.txt"] {
		t.Fatal("视频和其他类型文件应出现在上传详情中")
	}
}

func TestSuppressSummary_InSharedState(t *testing.T) {
	// 验证 suppressSummary 在 pipelineSharedState 中，通过 mu 保护
	rec := newTestRecorder(t, "test-live")

	// 初始值应为 false
	rec.pipelineState.mu.Lock()
	if rec.pipelineState.suppressSummary {
		t.Fatal("suppressSummary 初始值应为 false")
	}
	rec.pipelineState.mu.Unlock()

	// CloseForRestart 设置 suppressSummary
	rec.pipelineState.mu.Lock()
	rec.pipelineState.suppressSummary = true
	rec.pipelineState.mu.Unlock()

	// 验证设置成功
	rec.pipelineState.mu.Lock()
	if !rec.pipelineState.suppressSummary {
		t.Fatal("suppressSummary 应为 true")
	}
	rec.pipelineState.mu.Unlock()
}

func TestTransferPipelineState_SetRedirect(t *testing.T) {
	// 验证 TransferPipelineState 设置了 redirect 链
	recA := newTestRecorder(t, "test-live")
	recB := newTestRecorder(t, "test-live")

	recA.pipelineState.pendingCount = 1
	recA.pipelineState.suppressSummary = true

	recB.TransferPipelineState(recA)

	// A 的状态应被重定向到 B
	if recA.pipelineState.redirectedTo.Load() != recB.pipelineState {
		t.Fatal("A 的 redirect 应指向 B 的状态")
	}

	// resolveState 应从 A 找到 B
	resolved := resolveState(recA.pipelineState)
	if resolved != recB.pipelineState {
		t.Fatal("resolveState(A) 应返回 B 的状态")
	}
	resolved.mu.Unlock()
}

func TestOnAllTasksDone_NotSentWhileRecording(t *testing.T) {
	// 验证 Bot 指出的场景：旧任务完成时新 recorder 仍在录制，摘要不应提前发送
	recA := newTestRecorder(t, "test-live")
	recB := newTestRecorder(t, "test-live")

	recA.pipelineState.pendingCount = 1
	recA.pipelineState.enqueued = true
	recA.pipelineState.suppressSummary = true
	recA.pipelineState.runExited = true

	// A → B
	recB.TransferPipelineState(recA)

	// B 仍在录制（runExited=false）
	// 记录 B 的 onAllTasksDone 是否被调用
	callbackTriggered := false
	originalCallback := recB.pipelineState.onAllTasksDone
	recB.pipelineState.onAllTasksDone = func() {
		callbackTriggered = true
		originalCallback()
	}

	// 模拟 A 的任务完成
	task := &pipeline.PipelineTask{
		Status:       pipeline.PipelineStatusCompleted,
		CurrentFiles: []pipeline.FileInfo{},
		InitialFiles: []pipeline.FileInfo{
			{Path: "/data/a.flv", Size: 1024, Type: pipeline.FileTypeVideo},
		},
		LastStageFiles: []pipeline.FileInfo{
			{Path: "/data/a.flv", Size: 1024, Type: pipeline.FileTypeVideo},
		},
	}
	recA.onPipelineTaskComplete(task)

	// 回调应被触发（remaining=0, suppressSummary=true）
	if !callbackTriggered {
		t.Fatal("onAllTasksDone 应被触发")
	}

	// 但摘要不应被发送（B 仍在录制，runExited=false）
	// 验证方式：summarySent 应仍为 false
	recB.pipelineState.mu.Lock()
	summarySent := recB.pipelineState.summarySent
	recB.pipelineState.mu.Unlock()
	if summarySent {
		t.Fatal("摘要不应在新 recorder 仍在录制时发送（summarySent 应为 false）")
	}
}

func TestFinalRecorder_SummaryNotSuppressed(t *testing.T) {
	// 验证最终 recorder 的摘要不被 suppressSummary 抑制
	// （suppressSummary 不应从旧 recorder 传播到新 recorder）
	recA := newTestRecorder(t, "test-live")
	recB := newTestRecorder(t, "test-live")

	recA.pipelineState.pendingCount = 1
	recA.pipelineState.enqueued = true
	recA.pipelineState.suppressSummary = true

	// A → B
	recB.TransferPipelineState(recA)

	// B 的 suppressSummary 应为 false（不从 A 继承）
	recB.pipelineState.mu.Lock()
	bSuppress := recB.pipelineState.suppressSummary
	recB.pipelineState.mu.Unlock()
	if bSuppress {
		t.Fatal("最终 recorder 的 suppressSummary 应为 false（不应从旧 recorder 继承）")
	}
}
