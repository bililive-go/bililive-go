package recorders

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	gomock "go.uber.org/mock/gomock"

	"github.com/bililive-go/bililive-go/src/configs"
	"github.com/bililive-go/bililive-go/src/instance"
	"github.com/bililive-go/bililive-go/src/live"
	livemock "github.com/bililive-go/bililive-go/src/live/mock"
	"github.com/bililive-go/bililive-go/src/pkg/events"
	"github.com/bililive-go/bililive-go/src/pkg/livelogger"
	"github.com/bililive-go/bililive-go/src/types"
)

func TestManagerAddAndRemoveRecorder(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	configs.SetCurrentConfig(new(configs.Config))
	ctx := context.WithValue(context.Background(), instance.Key, &instance.Instance{})
	m := NewManager(ctx)
	backup := newRecorder
	callCount := 0
	newRecorder = func(ctx context.Context, live live.Live) (Recorder, error) {
		callCount++
		r := NewMockRecorder(ctrl)
		r.EXPECT().Start(ctx).Return(nil)
		if callCount == 1 {
			// 第一个 recorder 会被 RestartRecorder 调用 CloseForRestart
			r.EXPECT().CloseForRestart().Return(nil)
		} else {
			// 第二个 recorder 会被 RemoveRecorder 调用 Close
			r.EXPECT().Close()
		}
		return r, nil
	}
	defer func() { newRecorder = backup }()
	l := livemock.NewMockLive(ctrl)
	l.EXPECT().GetLiveId().Return(types.LiveID("test")).AnyTimes()
	l.EXPECT().GetRawUrl().Return("https://live.bilibili.com/1").AnyTimes()
	l.EXPECT().GetLogger().Return(livelogger.New(0, nil)).AnyTimes()
	assert.NoError(t, m.AddRecorder(context.Background(), l))
	assert.Equal(t, ErrRecorderExist, m.AddRecorder(context.Background(), l))
	ln, err := m.GetRecorder(context.Background(), "test")
	assert.NoError(t, err)
	assert.NotNil(t, ln)
	assert.True(t, m.HasRecorder(context.Background(), "test"))
	assert.NoError(t, m.RestartRecorder(context.Background(), l))
	assert.NoError(t, m.RemoveRecorder(context.Background(), "test"))
	assert.Equal(t, ErrRecorderNotExist, m.RemoveRecorder(context.Background(), "test"))
	_, err = m.GetRecorder(context.Background(), "test")
	assert.Equal(t, ErrRecorderNotExist, err)
	assert.False(t, m.HasRecorder(context.Background(), "test"))
}

// TestRecorderStopRequestedRemovesOnlyRecorder 验证 recorder 的内部停止请求会被
// manager 消费并回收 recorder，无需伪造具有全局直播状态语义的 LiveEnd。
func TestRecorderStopRequestedRemovesOnlyRecorder(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	ctx := context.Background()
	ed := events.NewDispatcher(ctx)
	l := livemock.NewMockLive(ctrl)
	l.EXPECT().GetLiveId().Return(types.LiveID("test")).AnyTimes()
	l.EXPECT().GetRawUrl().Return("https://live.bilibili.com/1").AnyTimes()

	r := NewMockRecorder(ctrl)
	r.EXPECT().Close()
	m := &manager{savers: map[types.LiveID]Recorder{"test": r}}
	m.registryListener(ctx, ed)

	ed.DispatchEventSync(events.NewEvent(RecorderStopRequested, l))

	assert.False(t, m.HasRecorder(ctx, "test"))
}

// TestRestartRecorderRaceWithLiveEnd 验证 RestartRecorder 和 LiveEnd（RemoveRecorder）
// 并发执行时不会产生僵尸录制器。
//
// 问题场景：cronRestart 调用 RestartRecorder 的同时，listener 检测到直播结束触发 LiveEnd。
// 旧实现中 RestartRecorder 分别调用 RemoveRecorder 和 AddRecorder（各自独立获取锁），
// 导致 LiveEnd 的 HasRecorder 可能在两次操作的间隙返回 false，从而错过移除新录制器，
// 产生僵尸录制器不断发送请求。
//
// 修复后 RestartRecorder 在整个 map 替换操作期间持有锁，LiveEnd 无法看到中间状态。
//
// 测试策略：利用已有的 newRecorder 函数变量注入同步逻辑。RestartRecorder 在锁内
// 先从 map 中移除旧 recorder，然后调用 addRecorderLocked，后者调用 newRecorder
// 创建新录制器——此时仍持有写锁且新录制器尚未放入 map。
// 测试在 newRecorder 中通知 LiveEnd goroutine，由于写锁未释放，LiveEnd 的
// HasRecorder（需要读锁）会阻塞直到 restart 完成，确定性地验证中间状态不可见。
func TestRestartRecorderRaceWithLiveEnd(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	configs.SetCurrentConfig(new(configs.Config))
	ctx := context.WithValue(context.Background(), instance.Key, &instance.Instance{})
	mgr := NewManager(ctx)

	// restartPhase 标记当前 newRecorder 是初始 AddRecorder 调用还是 RestartRecorder 中的调用
	restartPhase := false
	// afterRemoveCh：在 restart 的 add 阶段（remove 已完成）通知 LiveEnd goroutine
	afterRemoveCh := make(chan struct{}, 1)

	backup := newRecorder
	newRecorder = func(ctx context.Context, l live.Live) (Recorder, error) {
		r := NewMockRecorder(ctrl)
		r.EXPECT().Start(gomock.Any()).Return(nil).AnyTimes()
		r.EXPECT().Close().AnyTimes()
		r.EXPECT().CloseForRestart().Return(nil).AnyTimes()

		if restartPhase {
			// RestartRecorder 的 add 阶段：此时旧 recorder 已从 map 移除，
			// 但 addRecorderLocked 还没有把新录制器放入 map。写锁仍被持有。
			// 通知 LiveEnd goroutine 可以尝试检查了。
			afterRemoveCh <- struct{}{}
		}

		return r, nil
	}
	defer func() { newRecorder = backup }()

	l := livemock.NewMockLive(ctrl)
	l.EXPECT().GetLiveId().Return(types.LiveID("test")).AnyTimes()
	l.EXPECT().GetRawUrl().Return("https://live.bilibili.com/1").AnyTimes()
	l.EXPECT().GetLogger().Return(livelogger.New(0, nil)).AnyTimes()

	// 先正常添加一个录制器
	assert.NoError(t, mgr.AddRecorder(ctx, l))

	// 标记后续 newRecorder 调用为 restart 阶段
	restartPhase = true

	var wg sync.WaitGroup
	wg.Add(2)

	// 模拟 LiveEnd 事件处理器：等待 restart 的 remove 完成后执行检查
	var hasRecorderResult bool
	go func() {
		defer wg.Done()
		// 等待 RestartRecorder 完成 remove、进入 add 阶段
		<-afterRemoveCh
		// 此时 RestartRecorder 仍持有写锁（正在 addRecorderLocked 内部）
		// 修复后：HasRecorder 需要读锁，会阻塞直到 RestartRecorder 释放写锁
		// 释放时 add 已完成，HasRecorder 看到的是 restart 后的新录制器，返回 true
		// 旧实现：remove 和 add 分别获取锁，此时 HasRecorder 看到中间状态，返回 false
		hasRecorderResult = mgr.HasRecorder(ctx, "test")
		if hasRecorderResult {
			mgr.RemoveRecorder(ctx, "test")
		}
	}()

	// 模拟 cronRestart 触发的 RestartRecorder
	go func() {
		defer wg.Done()
		mgr.RestartRecorder(ctx, l)
	}()

	wg.Wait()

	// 验证：由于修复后 RestartRecorder 持有锁贯穿 remove+add，
	// LiveEnd 的 HasRecorder 在获得锁时看到的是 restart 后的新录制器（返回 true），
	// 然后 RemoveRecorder 正常移除。因此最终不应残留僵尸录制器。
	assert.False(t, mgr.HasRecorder(ctx, "test"),
		"发现僵尸录制器 - RestartRecorder 竞态条件未修复")

	// 验证 HasRecorder 在锁释放后应返回 true（说明它等到了 restart 完成，而不是看到中间状态 false）
	assert.True(t, hasRecorderResult,
		"HasRecorder 应在 RestartRecorder 完成后返回 true，说明锁正确阻止了中间状态暴露")
}

func TestCronRestartUsesRoomVideoSplitStrategies(t *testing.T) {
	ctrl := gomock.NewController(t)
	cfg := configs.NewConfig()
	cfg.VideoSplitStrategies.MaxDuration = time.Hour
	cfg.LiveRooms = []configs.LiveRoom{
		{Url: "https://live.bilibili.com/1", OverridableConfig: configs.OverridableConfig{VideoSplitStrategies: &configs.VideoSplitStrategies{MaxDuration: time.Minute}}},
		{Url: "https://live.bilibili.com/2", OverridableConfig: configs.OverridableConfig{VideoSplitStrategies: &configs.VideoSplitStrategies{}}},
	}
	previous := configs.GetCurrentConfig()
	configs.SetCurrentConfig(cfg)
	t.Cleanup(func() { configs.SetCurrentConfig(previous) })
	backup := newRecorder
	t.Cleanup(func() { newRecorder = backup })
	restarted := 0
	newRecorder = func(context.Context, live.Live) (Recorder, error) {
		restarted++
		return nil, errors.New("测试中终止重启")
	}
	m := &manager{savers: make(map[types.LiveID]Recorder)}
	for i, room := range cfg.LiveRooms {
		l := livemock.NewMockLive(ctrl)
		id := types.LiveID(room.Url)
		l.EXPECT().GetRawUrl().Return(room.Url).AnyTimes()
		l.EXPECT().GetLiveId().Return(id).AnyTimes()
		r := NewMockRecorder(ctrl)
		if i == 0 {
			r.EXPECT().StartTime().Return(time.Now().Add(-2 * time.Minute))
		}
		m.savers[id] = r
		m.cronRestart(context.Background(), l, r)
	}
	assert.Equal(t, 1, restarted)
}

func TestAddRecorderSchedulesRoomVideoSplit(t *testing.T) {
	ctrl := gomock.NewController(t)
	cfg := configs.NewConfig()
	cfg.VideoSplitStrategies.MaxDuration = 0
	cfg.LiveRooms = []configs.LiveRoom{{
		Url:               "https://live.bilibili.com/1",
		OverridableConfig: configs.OverridableConfig{VideoSplitStrategies: &configs.VideoSplitStrategies{MaxDuration: time.Minute}},
	}}
	previous := configs.GetCurrentConfig()
	configs.SetCurrentConfig(cfg)
	t.Cleanup(func() { configs.SetCurrentConfig(previous) })
	l := livemock.NewMockLive(ctrl)
	l.EXPECT().GetRawUrl().Return(cfg.LiveRooms[0].Url).AnyTimes()
	l.EXPECT().GetLiveId().Return(types.LiveID("test")).AnyTimes()
	r := NewMockRecorder(ctrl)
	r.EXPECT().Start(gomock.Any()).Return(nil)
	r.EXPECT().StartTime().Return(time.Now().Add(-2 * time.Minute))
	backup := newRecorder
	t.Cleanup(func() { newRecorder = backup })
	restarted := make(chan struct{})
	calls := 0
	newRecorder = func(context.Context, live.Live) (Recorder, error) {
		calls++
		if calls == 1 {
			return r, nil
		}
		close(restarted)
		return nil, errors.New("测试中终止重启")
	}
	m := &manager{savers: make(map[types.LiveID]Recorder)}
	require.NoError(t, m.AddRecorder(context.Background(), l))
	select {
	case <-restarted:
		// 等待重启释放锁，避免测试清理与调度协程并发。
		_, err := m.GetRecorder(context.Background(), "test")
		require.NoError(t, err)
	case <-time.After(3 * time.Second):
		t.Fatal("全局关闭分片时，房间时长覆盖未启动调度")
	}
}

func TestCronRestartStopsForReplacedRecorder(t *testing.T) {
	ctrl := gomock.NewController(t)
	l := livemock.NewMockLive(ctrl)
	l.EXPECT().GetLiveId().Return(types.LiveID("test"))
	old := NewMockRecorder(ctrl)
	current := NewMockRecorder(ctrl)
	m := &manager{savers: map[types.LiveID]Recorder{"test": current}}
	// 旧任务不得读取新录制器的开始时间，也不得再次调度。
	m.cronRestart(context.Background(), l, old)
}

func TestCronRestartRechecksRecorderUnderRestartLock(t *testing.T) {
	ctrl := gomock.NewController(t)
	previous := configs.GetCurrentConfig()
	cfg := configs.NewConfig()
	cfg.VideoSplitStrategies.MaxDuration = time.Minute
	configs.SetCurrentConfig(cfg)
	t.Cleanup(func() { configs.SetCurrentConfig(previous) })
	l := livemock.NewMockLive(ctrl)
	l.EXPECT().GetLiveId().Return(types.LiveID("test")).AnyTimes()
	l.EXPECT().GetRawUrl().Return("https://live.bilibili.com/1")
	old := NewMockRecorder(ctrl)
	current := NewMockRecorder(ctrl)
	m := &manager{savers: map[types.LiveID]Recorder{"test": old}}
	old.EXPECT().StartTime().DoAndReturn(func() time.Time {
		// 精确模拟标题分片在定时任务已取得旧实例、但尚未拿到重启锁时完成替换。
		m.lock.Lock()
		m.savers["test"] = current
		m.lock.Unlock()
		return time.Now().Add(-2 * time.Minute)
	})
	backup := newRecorder
	t.Cleanup(func() { newRecorder = backup })
	newRecorder = func(context.Context, live.Live) (Recorder, error) {
		t.Fatal("旧定时任务不应重启刚替换的新实例")
		return nil, nil
	}
	m.cronRestart(context.Background(), l, old)
	rec, err := m.GetRecorder(context.Background(), "test")
	require.NoError(t, err)
	require.Same(t, current, rec)
}

func TestCronRestartStopsOnCanceledContext(t *testing.T) {
	ctrl := gomock.NewController(t)
	l := livemock.NewMockLive(ctrl)
	r := NewMockRecorder(ctrl)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	m := &manager{}
	m.cronRestart(ctx, l, r)
	require.ErrorIs(t, m.restartRecorder(ctx, l, r), context.Canceled)
}
