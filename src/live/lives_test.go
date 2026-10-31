package live

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bluele/gcache"

	"github.com/bililive-go/bililive-go/src/configs"
	"github.com/bililive-go/bililive-go/src/types"
)

type blockingInfoLive struct {
	Live
	calls        atomic.Int32
	firstStarted chan struct{}
	releaseFirst chan struct{}
}

func (l *blockingInfoLive) GetRawUrl() string { return "" }

func (l *blockingInfoLive) GetInfo() (*Info, error) {
	if l.calls.Add(1) == 1 {
		close(l.firstStarted)
		<-l.releaseFirst
	}
	return &Info{}, nil
}

type errorInfoLive struct{ Live }

func (l *errorInfoLive) GetRawUrl() string         { return "" }
func (l *errorInfoLive) GetLiveId() types.LiveID   { return "error-test" }
func (l *errorInfoLive) GetPlatformCNName() string { return "测试平台" }
func (l *errorInfoLive) GetInfo() (*Info, error)   { return nil, errors.New("请求失败") }

type blockingOptionsLive struct {
	Live
	options     atomic.Pointer[Options]
	infoStarted chan struct{}
	releaseInfo chan struct{}
}

func (l *blockingOptionsLive) GetRawUrl() string { return "https://live.bilibili.com/123" }
func (l *blockingOptionsLive) GetInfo() (*Info, error) {
	options := l.options.Load()
	close(l.infoStarted)
	<-l.releaseInfo
	return &Info{Live: l, AudioOnly: options.AudioOnly}, nil
}
func (l *blockingOptionsLive) UpdateLiveOptionsbyConfig(_ context.Context, room *configs.LiveRoom) error {
	l.options.Store(&Options{AudioOnly: room.AudioOnly, Quality: room.Quality, NickName: room.NickName})
	return nil
}
func (l *blockingOptionsLive) GetOptions() *Options { return l.options.Load() }

// 未加载全局配置时 getConfiguredInterval 会直接返回默认间隔，
// 因此可以直接构造一个空的 WrappedLive 来验证退避曲线。
func TestNextRequestIntervalLocked(t *testing.T) {
	tests := []struct {
		name     string
		failures int
		want     time.Duration
	}{
		{name: "无失败时使用配置间隔", failures: 0, want: defaultInterval},
		{name: "一次失败翻倍", failures: 1, want: 2 * defaultInterval},
		{name: "两次失败四倍", failures: 2, want: 4 * defaultInterval},
		{name: "多次失败后封顶", failures: 100, want: maxFailureBackoff},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := &WrappedLive{consecutiveFailures: tt.failures}
			w.mu.Lock()
			got := w.nextRequestIntervalLocked()
			w.mu.Unlock()
			if got != tt.want {
				t.Errorf("consecutiveFailures=%d: got %v, want %v", tt.failures, got, tt.want)
			}
		})
	}
}

// 退避间隔必须单调不减，且永远不会超过上限，
// 否则失败时的请求频率反而会高于正常轮询频率。
func TestNextRequestIntervalNeverShrinksBelowConfigured(t *testing.T) {
	prev := time.Duration(0)
	for failures := 0; failures < 64; failures++ {
		w := &WrappedLive{consecutiveFailures: failures}
		w.mu.Lock()
		got := w.nextRequestIntervalLocked()
		w.mu.Unlock()
		if got < defaultInterval {
			t.Fatalf("consecutiveFailures=%d: 退避间隔 %v 小于配置间隔 %v", failures, got, defaultInterval)
		}
		if got > maxFailureBackoff {
			t.Fatalf("consecutiveFailures=%d: 退避间隔 %v 超过上限 %v", failures, got, maxFailureBackoff)
		}
		if got < prev {
			t.Fatalf("consecutiveFailures=%d: 退避间隔 %v 小于上一次的 %v", failures, got, prev)
		}
		prev = got
	}
}

func TestFailureBackoffDoesNotShortenLongConfiguredInterval(t *testing.T) {
	configured := 10 * time.Minute
	for _, failures := range []int{0, 1, 100} {
		if got := failureBackoffInterval(configured, failures); got < configured {
			t.Fatalf("consecutiveFailures=%d: 退避间隔 %v 小于配置间隔 %v", failures, got, configured)
		}
	}
}

func TestInitializingFailureCountsTowardBackoff(t *testing.T) {
	w := &WrappedLive{}
	placeholder := &Info{Initializing: true, LastError: "平台请求失败"}

	if failed := w.recordRequestResult(placeholder, nil); !failed {
		t.Fatal("初始化占位信息携带 LastError 时应计为失败")
	}
	w.mu.Lock()
	failures := w.consecutiveFailures
	w.mu.Unlock()
	if failures != 1 {
		t.Fatalf("连续失败次数 = %d，期望 1", failures)
	}

	if failed := w.recordRequestResult(&Info{}, nil); failed {
		t.Fatal("正常信息不应计为失败")
	}
	w.mu.Lock()
	failures = w.consecutiveFailures
	w.mu.Unlock()
	if failures != 0 {
		t.Fatalf("成功后连续失败次数 = %d，期望清零", failures)
	}
}

func TestWrappedLiveSerializesConcurrentGetInfo(t *testing.T) {
	underlying := &blockingInfoLive{
		firstStarted: make(chan struct{}),
		releaseFirst: make(chan struct{}),
	}
	w := NewWrappedLive(context.Background(), underlying, nil).(*WrappedLive)

	done := make(chan struct{}, 2)
	go func() {
		_, _ = w.GetInfo()
		done <- struct{}{}
	}()
	<-underlying.firstStarted
	go func() {
		_, _ = w.GetInfo()
		done <- struct{}{}
	}()

	time.Sleep(50 * time.Millisecond)
	if calls := underlying.calls.Load(); calls != 1 {
		t.Fatalf("首个请求完成前底层 GetInfo 被调用 %d 次，期望 1 次", calls)
	}

	close(underlying.releaseFirst)
	for range 2 {
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Fatal("并发 GetInfo 未完成")
		}
	}
}

func TestWrappedLiveSerializesOptionUpdateWithInfoCache(t *testing.T) {
	cache := gcache.New(10).Build()
	underlying := &blockingOptionsLive{
		infoStarted: make(chan struct{}),
		releaseInfo: make(chan struct{}),
	}
	underlying.options.Store(&Options{AudioOnly: false})
	w := NewWrappedLive(context.Background(), underlying, cache).(*WrappedLive)
	t.Cleanup(w.Close)

	infoDone := make(chan error, 1)
	go func() {
		_, err := w.GetInfo()
		infoDone <- err
	}()
	<-underlying.infoStarted

	updateDone := make(chan error, 1)
	go func() {
		updateDone <- w.UpdateLiveOptionsbyConfig(context.Background(), &configs.LiveRoom{
			Url:       underlying.GetRawUrl(),
			AudioOnly: true,
		})
	}()
	select {
	case err := <-updateDone:
		t.Fatalf("在途 GetInfo 完成前选项更新不应返回，err=%v", err)
	case <-time.After(50 * time.Millisecond):
	}

	close(underlying.releaseInfo)
	if err := <-infoDone; err != nil {
		t.Fatal(err)
	}
	if err := <-updateDone; err != nil {
		t.Fatal(err)
	}

	cached, err := cache.Get(w)
	if err != nil {
		t.Fatal(err)
	}
	if info := cached.(*Info); !info.AudioOnly {
		t.Fatal("在途 GetInfo 的旧音频标志覆盖了热更新")
	}
}

func TestSeedInfoUsesCurrentAudioOption(t *testing.T) {
	cache := gcache.New(10).Build()
	underlying := &blockingOptionsLive{}
	underlying.options.Store(&Options{AudioOnly: true})
	w := NewWrappedLive(context.Background(), underlying, cache).(*WrappedLive)
	t.Cleanup(w.Close)

	// 模拟新包装器已经发布、但初始化 Info 尚未注入时保存了房间设置。
	if err := w.UpdateLiveOptionsbyConfig(context.Background(), &configs.LiveRoom{
		Url:       underlying.GetRawUrl(),
		AudioOnly: false,
	}); err != nil {
		t.Fatal(err)
	}
	stale := &Info{Live: underlying, AudioOnly: true, LastError: "初始化旧结果"}
	if err := w.SeedInfo(stale); err != nil {
		t.Fatal(err)
	}

	cached, err := cache.Get(w)
	if err != nil {
		t.Fatal(err)
	}
	seeded := cached.(*Info)
	if seeded.AudioOnly {
		t.Fatal("SeedInfo 用初始化旧结果覆盖了当前音频选项")
	}
	if seeded.Live != w || seeded.LastError != "" {
		t.Fatalf("注入的缓存状态不正确: Live=%T LastError=%q", seeded.Live, seeded.LastError)
	}
	if !stale.AudioOnly || stale.LastError != "初始化旧结果" {
		t.Fatal("SeedInfo 不应就地修改初始化流程仍可能持有的旧 Info")
	}
}

func TestAvailableStreamsCacheUpdateDoesNotWaitForInfoRequest(t *testing.T) {
	cache := gcache.New(10).Build()
	underlying := &blockingOptionsLive{
		infoStarted: make(chan struct{}),
		releaseInfo: make(chan struct{}),
	}
	underlying.options.Store(&Options{AudioOnly: false})
	w := NewWrappedLive(context.Background(), underlying, cache).(*WrappedLive)
	t.Cleanup(w.Close)
	if err := cache.Set(w, &Info{Live: w, RoomName: "缓存标题"}); err != nil {
		t.Fatal(err)
	}

	released := false
	defer func() {
		if !released {
			close(underlying.releaseInfo)
		}
	}()
	infoDone := make(chan error, 1)
	go func() {
		_, err := w.GetInfo()
		infoDone <- err
	}()
	<-underlying.infoStarted

	streamsDone := make(chan error, 1)
	go func() {
		streamsDone <- w.UpdateCachedAvailableStreams([]*AvailableStreamInfo{{Quality: "HD"}}, 123)
	}()
	select {
	case err := <-streamsDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("展示用流列表缓存被在途平台请求阻塞")
	}

	cached, err := cache.Get(w)
	if err != nil {
		t.Fatal(err)
	}
	info := cached.(*Info)
	if len(info.AvailableStreams) != 1 || info.AvailableStreams[0].Quality != "HD" || info.AvailableStreamsUpdatedAt != 123 {
		t.Fatalf("可用流没有合并进最新缓存: %+v", info)
	}

	close(underlying.releaseInfo)
	released = true
	if err := <-infoDone; err != nil {
		t.Fatal(err)
	}
	cached, err = cache.Get(w)
	if err != nil {
		t.Fatal(err)
	}
	info = cached.(*Info)
	if len(info.AvailableStreams) != 1 || info.AvailableStreams[0].Quality != "HD" || info.AvailableStreamsUpdatedAt != 123 {
		t.Fatalf("平台请求提交覆盖了录制器刚更新的可用流: %+v", info)
	}
}

func TestRequestStatusCallbackHandlesNilInfoOnFailure(t *testing.T) {
	callbackCalled := false
	SetRequestStatusCallback(func(_, _ string, success bool, errMsg string) {
		callbackCalled = true
		if success || errMsg != "请求失败" {
			t.Fatalf("错误的请求状态回调：success=%v, err=%q", success, errMsg)
		}
	})
	t.Cleanup(func() { SetRequestStatusCallback(nil) })

	w := NewWrappedLive(context.Background(), &errorInfoLive{}, nil).(*WrappedLive)
	if _, err := w.GetInfo(); err == nil {
		t.Fatal("底层请求失败时应返回错误")
	}
	if !callbackCalled {
		t.Fatal("请求失败时未调用状态回调")
	}
}
