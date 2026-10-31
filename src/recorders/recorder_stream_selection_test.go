package recorders

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bluele/gcache"
	"github.com/sirupsen/logrus"
	gomock "go.uber.org/mock/gomock"

	"github.com/bililive-go/bililive-go/src/configs"
	"github.com/bililive-go/bililive-go/src/instance"
	"github.com/bililive-go/bililive-go/src/live"
	livemock "github.com/bililive-go/bililive-go/src/live/mock"
	eventsmock "github.com/bililive-go/bililive-go/src/pkg/events/mock"
	"github.com/bililive-go/bililive-go/src/pkg/livelogger"
)

const testRoomURL = "https://example.com/room"

// deferredLive 模拟实现了 live.DeferredStreamResolver 的平台（当前只有 Soop）：
// 列候选只给元信息，被点名的那一路才解析出播放地址。
type deferredLive struct {
	*livemock.MockLive

	candidates   []*live.StreamUrlInfo
	resolveErr   func(candidate *live.StreamUrlInfo) error
	afterResolve func(candidate *live.StreamUrlInfo)
	resolveSeen  *[]string
	logger       *livelogger.LiveLogger
}

func (d *deferredLive) ListStreamCandidates() ([]*live.StreamUrlInfo, error) {
	return d.candidates, nil
}

func (d *deferredLive) ResolveStreamCandidate(candidate *live.StreamUrlInfo) error {
	if d.resolveSeen != nil {
		*d.resolveSeen = append(*d.resolveSeen, candidate.Quality)
	}
	if d.resolveErr != nil {
		if err := d.resolveErr(candidate); err != nil {
			return err
		}
	}
	candidate.Url = &url.URL{Scheme: "https", Host: "cdn.example.com", Path: "/" + candidate.Quality + ".m3u8"}
	if d.afterResolve != nil {
		d.afterResolve(candidate)
	}
	return nil
}

func qualityOf(name string) *string {
	value := name
	return &value
}

func testLogger() *livelogger.LiveLogger {
	return livelogger.New(0, logrus.Fields{"test": "stream_selection"})
}

// newDeferredLive 造一个只支持延迟解析的平台，并让它把每档的解析动作记进 seen。
func newDeferredLive(t *testing.T, candidates []*live.StreamUrlInfo, resolveErr func(*live.StreamUrlInfo) error) (*deferredLive, *[]string) {
	t.Helper()
	ctrl := gomock.NewController(t)
	base := livemock.NewMockLive(ctrl)
	logger := testLogger()
	base.EXPECT().GetLogger().Return(logger).AnyTimes()
	base.EXPECT().GetRawUrl().Return(testRoomURL).AnyTimes()
	base.EXPECT().GetPlatformCNName().Return("SOOP").AnyTimes()
	seen := make([]string, 0, len(candidates))
	return &deferredLive{
		MockLive:    base,
		candidates:  candidates,
		resolveErr:  resolveErr,
		resolveSeen: &seen,
		logger:      logger,
	}, &seen
}

func sampleCandidates() []*live.StreamUrlInfo {
	return []*live.StreamUrlInfo{
		{Quality: "HD", AttributesForStreamSelect: map[string]string{"quality_key": "HD"}},
		{Quality: "SD", AttributesForStreamSelect: map[string]string{"quality_key": "SD"}},
	}
}

func TestUpdateAvailableStreamsMergesIntoLatestWrappedCache(t *testing.T) {
	ctrl := gomock.NewController(t)
	underlying := livemock.NewMockLive(ctrl)
	cache := gcache.New(2).LRU().Build()
	wrapped := live.NewWrappedLive(context.Background(), underlying, cache)
	t.Cleanup(wrapped.Close)

	current := &live.Info{Live: wrapped, AudioOnly: false, RoomName: "最新标题"}
	if err := cache.Set(wrapped, current); err != nil {
		t.Fatalf("写入直播信息缓存失败: %v", err)
	}
	r := &recorder{Live: wrapped, cache: cache}
	r.updateAvailableStreams(context.Background(), []*live.StreamUrlInfo{{
		Quality: "HD",
		Format:  "flv",
		Codec:   "h264",
	}})

	cached, err := cache.Get(wrapped)
	if err != nil {
		t.Fatalf("读取直播信息缓存失败: %v", err)
	}
	updated := cached.(*live.Info)
	if updated.AudioOnly {
		t.Fatal("合并可用流时不能把最新的 AudioOnly 覆盖成旧值")
	}
	if updated.RoomName != current.RoomName {
		t.Fatalf("合并可用流时覆盖了其他缓存字段: %q", updated.RoomName)
	}
	if len(updated.AvailableStreams) != 1 || updated.AvailableStreams[0].Quality != "HD" {
		t.Fatalf("可用流没有写入最新缓存: %+v", updated.AvailableStreams)
	}
	if updated.AvailableStreamsUpdatedAt == 0 {
		t.Fatal("可用流更新时间没有写入缓存")
	}
	if current.AvailableStreams != nil || current.AvailableStreamsUpdatedAt != 0 {
		t.Fatal("不能就地修改其他 goroutine 可能仍在读取的旧 Info")
	}
}

func TestGetStreamInfosForRecordingResolvesOnlyPreferredCandidate(t *testing.T) {
	restoreConfig := swapTestConfig(t)
	defer restoreConfig()

	l, seen := newDeferredLive(t, []*live.StreamUrlInfo{
		{Quality: "SD", AttributesForStreamSelect: map[string]string{"quality_key": "SD"}},
		{Quality: "HD", AttributesForStreamSelect: map[string]string{"quality_key": "HD"}},
		{Quality: "FHD", AttributesForStreamSelect: map[string]string{"quality_key": "FHD"}},
	}, nil)

	// 偏好 FHD 时平台顺序第一档是 SD，只有按偏好排过序才会先解析 FHD。
	streams, err := (&recorder{Live: l}).getStreamInfosForRecording(configs.StreamPreference{Quality: qualityOf("FHD")})
	if err != nil {
		t.Fatalf("取流失败: %v", err)
	}
	if len(*seen) != 1 || (*seen)[0] != "FHD" {
		t.Fatalf("应只解析被选中的那一路，实际解析顺序 %v", *seen)
	}
	if streams[0].Quality != "FHD" || streams[0].Url == nil {
		t.Fatalf("首位应为已解析的 FHD，实际 %s url=%v", streams[0].Quality, streams[0].Url)
	}
	// 其余档位只用于面板展示，刻意不带播放地址。
	for _, s := range streams[1:] {
		if s.Url != nil {
			t.Fatalf("未解析的候选 %s 不应带播放地址", s.Quality)
		}
	}
}

// TestPreferredStreamIgnoresPreferenceChangedMidAttempt 覆盖"解析期间用户改了画质偏好"这一时序：
// 一次解析要跑几百毫秒的平台请求，期间面板上的"切换清晰度"就会改写房间偏好。最终选择若再读一次
// 全局配置，就会选中根本没解析过的那一路，其 Url 为空，后面解引用直接把录制线程 panic 掉，
// 而且是在持有 currentFileLock 时崩的，面板读状态的请求会跟着一起挂住。
func TestPreferredStreamIgnoresPreferenceChangedMidAttempt(t *testing.T) {
	restoreConfig := swapTestConfig(t)
	defer restoreConfig()

	l, seen := newDeferredLive(t, sampleCandidates(), nil)
	r := &recorder{Live: l}
	pinned := configs.StreamPreference{Quality: qualityOf("HD")}

	streams, err := r.getStreamInfosForRecording(pinned)
	if err != nil {
		t.Fatalf("取流失败: %v", err)
	}
	if len(*seen) != 1 || (*seen)[0] != "HD" {
		t.Fatalf("用例前提不成立：本轮解析的不是 HD，实际 %v", *seen)
	}

	// 用户在这一轮解析期间把偏好改成了 SD：全局配置已经是新值，本轮仍必须用固定下来的 HD。
	setRoomPreference(configs.GetCurrentConfig(), testRoomURL, configs.StreamPreference{Quality: qualityOf("SD")})

	selected := r.selectPreferredStream(streams, pinned)
	if selected.Quality != "HD" || selected.Url == nil {
		t.Fatalf("本轮应选中已解析的 HD，实际 %s url=%v", selected.Quality, selected.Url)
	}
	// 未解析的 SD 仍留在列表里供面板展示档位，说明"重读全局偏好"确实会挑中空地址那一路。
	for _, s := range streams {
		if s.Quality == "SD" && s.Url != nil {
			t.Fatal("用例前提不成立：SD 这一路已被解析")
		}
	}
	if got := r.selectPreferredStream(streams, configs.GetCurrentConfig().GetEffectiveConfigForRoom(testRoomURL).StreamPreference); got.Url != nil {
		t.Fatal("用例前提不成立：按新全局偏好重选竟拿到了已解析的地址，本用例挡不住回归")
	}
}

// TestTryRecordKeepsResolvedCandidateThroughoutAttempt 把上一条覆盖到真实的 tryRecord 里：
// 解析要跑几百毫秒的平台请求，用户在面板上点"切换清晰度"就落在这个往返中间，
// 而本轮后续的选路必须仍然按尝试开始时那份偏好走。偏好若在读一次之后再读一次，
// 选中的就是没解析过的档位，其播放地址为空，本轮直接作废（修复前这里连错误都拿不到，
// 是解引用空指针把录制线程崩掉）。
// 输出路径故意指向一个普通文件，让 mkdir 失败，从而在选路之后立刻收住这一轮。
func TestTryRecordKeepsResolvedCandidateThroughoutAttempt(t *testing.T) {
	restoreConfig := swapTestConfig(t)
	defer restoreConfig()

	blocker := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(blocker, []byte("x"), 0o600); err != nil {
		t.Fatalf("准备输出路径占位文件失败: %v", err)
	}
	cfg := configs.GetCurrentConfig()
	cfg.OutputTmpl = "stream.ts"
	cfg.OutPutPath = blocker
	setRoomPreference(cfg, testRoomURL, configs.StreamPreference{Quality: qualityOf("HD")})

	l, seen := newDeferredLive(t, sampleCandidates(), nil)
	l.afterResolve = func(*live.StreamUrlInfo) {
		// 模拟解析期间用户把偏好改成了 SD：SD 这一路本轮没有被解析过，没有播放地址。
		setRoomPreference(cfg, testRoomURL, configs.StreamPreference{Quality: qualityOf("SD")})
	}

	inst := &instance.Instance{Cache: gcache.New(1).LRU().Build()}
	ctx := context.WithValue(context.Background(), instance.Key, inst)
	ctrl := gomock.NewController(t)
	ed := eventsmock.NewMockDispatcher(ctrl)
	if err := inst.Cache.Set(l, &live.Info{Live: l}); err != nil {
		t.Fatalf("写入直播信息缓存失败: %v", err)
	}
	r := &recorder{Live: l, cache: inst.Cache, ed: ed}

	r.tryRecord(ctx)

	if len(*seen) != 1 || (*seen)[0] != "HD" {
		t.Fatalf("本轮应只解析偏好指定的 HD，实际解析顺序 %v", *seen)
	}
	logs := l.logger.GetLogs()
	if strings.Contains(logs, "尚未解析出播放地址") {
		t.Fatalf("选中了未解析的候选，本轮被空地址挡下：\n%s", logs)
	}
	if !strings.Contains(logs, "failed to create output path") {
		t.Fatalf("用例前提不成立：本轮没有走到选路之后，日志为:\n%s", logs)
	}
	r.currentFileLock.RLock()
	streamURL := r.currentStreamURL
	r.currentFileLock.RUnlock()
	if !strings.Contains(streamURL, "/HD.m3u8") {
		t.Fatalf("本轮应录制已解析的 HD，实际 %q", streamURL)
	}
}

// TestTryRecordStopsWhenSelectedStreamHasNoURL 选中的那一路没有播放地址时，本轮必须带着原因退出。
// 这里曾在持有 currentFileLock 的情况下解引用空 URL：panic 被 goroutine 边界 recover 之后
// 写锁永远不会再释放，面板查直播间状态的读请求会全部挂住，表现成整个房间无响应而不是报错。
func TestTryRecordStopsWhenSelectedStreamHasNoURL(t *testing.T) {
	restoreConfig := swapTestConfig(t)
	defer restoreConfig()

	ctrl := gomock.NewController(t)
	base := livemock.NewMockLive(ctrl)
	logger := testLogger()
	base.EXPECT().GetLogger().Return(logger).AnyTimes()
	base.EXPECT().GetRawUrl().Return(testRoomURL).AnyTimes()
	base.EXPECT().GetPlatformCNName().Return("SOOP").AnyTimes()
	// 缓存按 key 反射取哈希，会顺带读到 Options。
	base.EXPECT().GetOptions().Return(live.MustNewOptions()).AnyTimes()
	// 只有元信息、没有播放地址的一路（延迟解析的列表就是这个形状）
	base.EXPECT().GetStreamInfos().Return([]*live.StreamUrlInfo{{Quality: "HD"}}, nil)

	inst := &instance.Instance{Cache: gcache.New(1).LRU().Build()}
	ctx := context.WithValue(context.Background(), instance.Key, inst)
	if err := inst.Cache.Set(base, &live.Info{Live: base}); err != nil {
		t.Fatalf("写入直播信息缓存失败: %v", err)
	}
	r := &recorder{Live: base, cache: inst.Cache, ed: eventsmock.NewMockDispatcher(ctrl)}

	r.tryRecord(ctx)

	if !strings.Contains(logger.GetLogs(), "清晰度 HD 尚未解析出播放地址") {
		t.Fatalf("未记录空播放地址的失败原因：\n%s", logger.GetLogs())
	}
	// 抢得到写锁才说明状态接口没有被挂住的读锁拖死。
	if !r.currentFileLock.TryLock() {
		t.Fatal("currentFileLock 被泄漏的写锁占住")
	}
	r.currentFileLock.Unlock()
}

// TestGetStreamInfosForRecordingDoesNotReResolveAllCandidates 全部候选都失败时不能顺手再全量解析一遍：
// 那等于在同一次尝试里把每一档的凭证与调度请求重新敲一次，而"全部失败"恰恰是最需要少发请求的状态。
func TestGetStreamInfosForRecordingDoesNotReResolveAllCandidates(t *testing.T) {
	restoreConfig := swapTestConfig(t)
	defer restoreConfig()

	l, seen := newDeferredLive(t, sampleCandidates(), func(*live.StreamUrlInfo) error {
		return fmt.Errorf("拒绝发放播放凭证")
	})
	// 不给 MockLive 设 GetStreamInfos 期望：一旦被调用 gomock 直接判失败，等于断言没有回退全量解析。

	_, err := (&recorder{Live: l}).getStreamInfosForRecording(configs.StreamPreference{})
	if err == nil {
		t.Fatal("全部候选解析失败时应返回错误")
	}
	if want := "清晰度 HD: 拒绝发放播放凭证"; err.Error() != want {
		t.Fatalf("错误应带上第一档的失败原因，实际 %q", err.Error())
	}
	if len(*seen) != 2 {
		t.Fatalf("应逐档各试一次后放弃，实际 %v", *seen)
	}
}

// TestTryRecordStopsWhenCandidateResolvesToOffline 列完候选之后房间才显示下播时，
// 解析这一档拿到的 ErrLiveOffline 必须一路传到 stopRetryForExplicitOffline：
// 既不能接着敲剩下档位的凭证请求，也不能把这次失败计进重试退避让面板一直转圈。
func TestTryRecordStopsWhenCandidateResolvesToOffline(t *testing.T) {
	restoreConfig := swapTestConfig(t)
	defer restoreConfig()

	candidates := []*live.StreamUrlInfo{
		{Quality: "HD", AttributesForStreamSelect: map[string]string{"quality_key": "HD"}},
		{Quality: "SD", AttributesForStreamSelect: map[string]string{"quality_key": "SD"}},
		{Quality: "FHD", AttributesForStreamSelect: map[string]string{"quality_key": "FHD"}},
	}
	l, seen := newDeferredLive(t, candidates, func(candidate *live.StreamUrlInfo) error {
		if candidate.Quality == "HD" {
			return fmt.Errorf("拒绝发放播放凭证")
		}
		return fmt.Errorf("%w: Soop 页面已明确显示下播", live.ErrLiveOffline)
	})
	ctrl := gomock.NewController(t)
	// 不给 GetStreamInfos 设期望：判定下播后不应再有任何取流请求。

	inst := &instance.Instance{Cache: gcache.New(1).LRU().Build()}
	ctx := context.WithValue(context.Background(), instance.Key, inst)
	ed := eventsmock.NewMockDispatcher(ctrl)
	ed.EXPECT().DispatchEvent(gomock.Any())
	r := &recorder{Live: l, cache: inst.Cache, ed: ed}

	r.tryRecord(ctx)

	if len(*seen) != 2 || (*seen)[1] != "SD" {
		t.Fatalf("确认下播后不应再解析 FHD，实际解析顺序 %v", *seen)
	}
	if strings.Contains(l.logger.GetLogs(), "will retry in") {
		t.Fatalf("下播不该进重试退避，日志为:\n%s", l.logger.GetLogs())
	}
}

func TestTransferPipelineStateKeepsHeldStreamFact(t *testing.T) {
	ctrl := gomock.NewController(t)
	base := livemock.NewMockLive(ctrl)
	base.EXPECT().GetLogger().Return(testLogger()).AnyTimes()
	old := &recorder{Live: base, pipelineState: &pipelineSharedState{sourceNames: make(map[string]bool)}}
	fresh := &recorder{Live: base, pipelineState: &pipelineSharedState{sourceNames: make(map[string]bool)}}

	// 新 recorder 在 RestartRecorder 里先被 Start，转移到它手上时它可能已经录上过；
	// 旧分段"从未录上过"不能把这个事实抹掉，否则重试间隔会退回更长的封顶档位。
	fresh.everHeldStream.Store(true)
	fresh.consecutiveFastFailures = 2
	fresh.TransferPipelineState(old)
	if !fresh.everHeldStream.Load() {
		t.Fatal("新实例已观测到的成功不应被旧实例覆盖")
	}
	// 观测到成功与没观测到成功，同样的失败次数落在不同档位上，这才是用户可见的差别。
	if got := fresh.pendingRetryInterval(); got != 15*time.Second {
		t.Fatalf("已录过上的分段应保持 15s 封顶档，实际 %s", got)
	}
	notHeld := &recorder{Live: base, pipelineState: &pipelineSharedState{sourceNames: make(map[string]bool)}}
	notHeld.consecutiveFastFailures = 2
	notHeld.TransferPipelineState(old)
	if got := notHeld.pendingRetryInterval(); got != 20*time.Second {
		t.Fatalf("未继承成功事实时应落在升档序列的 20s，实际 %s", got)
	}

	old.everHeldStream.Store(true)
	inherited := &recorder{Live: base, pipelineState: &pipelineSharedState{sourceNames: make(map[string]bool)}}
	inherited.TransferPipelineState(old)
	if !inherited.everHeldStream.Load() {
		t.Fatal("旧分段录上过，新分段应沿用该事实")
	}
}

// swapTestConfig 装一份只含测试房间的空配置，返回还原函数。
func swapTestConfig(t *testing.T) func() {
	t.Helper()
	previous := configs.GetCurrentConfig()
	cfg := configs.NewConfig()
	configs.SetCurrentConfig(cfg)
	return func() { configs.SetCurrentConfig(previous) }
}

// setRoomPreference 把房间级画质偏好写进配置，模拟用户在面板上点"切换清晰度"。
func setRoomPreference(cfg *configs.Config, roomURL string, preference configs.StreamPreference) {
	value := preference
	for i := range cfg.LiveRooms {
		if cfg.LiveRooms[i].Url == roomURL {
			cfg.LiveRooms[i].StreamPreference = &value
			return
		}
	}
	cfg.LiveRooms = append(cfg.LiveRooms, configs.LiveRoom{
		Url: roomURL,
		OverridableConfig: configs.OverridableConfig{
			StreamPreference: &value,
		},
	})
}
