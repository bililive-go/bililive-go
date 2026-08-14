package recorders

import (
	"context"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	gomock "go.uber.org/mock/gomock"

	"github.com/bililive-go/bililive-go/src/configs"
	"github.com/bililive-go/bililive-go/src/instance"
	"github.com/bililive-go/bililive-go/src/listeners"
	"github.com/bililive-go/bililive-go/src/live"
	livemock "github.com/bililive-go/bililive-go/src/live/mock"
	"github.com/bililive-go/bililive-go/src/pkg/events"
	"github.com/bililive-go/bililive-go/src/pkg/livelogger"
	"github.com/bililive-go/bililive-go/src/types"
)

type testListenerEventSource struct {
	closed bool
}

func (s *testListenerEventSource) Start() error {
	return nil
}

func (s *testListenerEventSource) StartWithInfo(*live.Info) error {
	return nil
}

func (s *testListenerEventSource) Close() {
	s.closed = true
}

func (s *testListenerEventSource) CloseSync() {
	s.closed = true
}

func (s *testListenerEventSource) IsClosed() bool {
	return s.closed
}

type testCurrentListenerManager struct {
	listener listeners.Listener
}

func (m *testCurrentListenerManager) Start(context.Context) error {
	return nil
}

func (m *testCurrentListenerManager) Close(context.Context) {}

func (m *testCurrentListenerManager) AddListener(context.Context, live.Live) error {
	return nil
}

func (m *testCurrentListenerManager) RemoveListener(context.Context, types.LiveID) error {
	return nil
}

func (m *testCurrentListenerManager) GetListener(context.Context, types.LiveID) (listeners.Listener, error) {
	if m.listener == nil {
		return nil, listeners.ErrListenerNotExist
	}
	return m.listener, nil
}

func (m *testCurrentListenerManager) HasListener(context.Context, types.LiveID) bool {
	return m.listener != nil
}

type recorderDoneObservedContext struct {
	context.Context
	once     sync.Once
	observed chan struct{}
}

func (c *recorderDoneObservedContext) Done() <-chan struct{} {
	c.once.Do(func() { close(c.observed) })
	return c.Context.Done()
}

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
			// 第二个 recorder 会被 RemoveRecorder 调用 CloseAndWait
			r.EXPECT().CloseAndWait()
		}
		return r, nil
	}
	defer func() { newRecorder = backup }()
	l := livemock.NewMockLive(ctrl)
	l.EXPECT().GetLiveId().Return(types.LiveID("test")).AnyTimes()
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

func TestManagerIgnoresStaleRecorderEvents(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	liveMock := livemock.NewMockLive(ctrl)
	liveMock.EXPECT().GetLiveId().Return(types.LiveID("test")).AnyTimes()

	m := &manager{savers: make(map[types.LiveID]Recorder)}
	backup := newRecorder
	newRecorder = func(context.Context, live.Live) (Recorder, error) {
		t.Fatal("关闭后的 listener 不应创建 recorder")
		return nil, nil
	}
	defer func() { newRecorder = backup }()

	closedSource := &testListenerEventSource{closed: true}
	staleLiveStart := events.NewEventWithSource(listeners.LiveStart, liveMock, closedSource)
	assert.NoError(t, m.addRecorder(context.Background(), liveMock, staleLiveStart))
	assert.Empty(t, m.savers)

	oldRecorder := NewMockRecorder(ctrl)
	currentRecorder := NewMockRecorder(ctrl)
	m.savers["test"] = currentRecorder
	staleLiveEnd := events.NewEventWithSource(listeners.LiveEnd, liveMock, oldRecorder)
	assert.NoError(t, m.removeRecorder(context.Background(), "test", recorderRemovalOptions{event: staleLiveEnd}))
	assert.Same(t, currentRecorder, m.savers[types.LiveID("test")])
}

func TestManagerRecorderClosingBlocksReAddAndListenStop(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	closeStarted := make(chan struct{})
	releaseClose := make(chan struct{})
	oldRecorder := NewMockRecorder(ctrl)
	oldRecorder.EXPECT().CloseAndWait().Do(func() {
		close(closeStarted)
		<-releaseClose
	})

	newRecorderMock := NewMockRecorder(ctrl)
	newRecorderMock.EXPECT().Start(gomock.Any()).Return(nil)
	liveMock := livemock.NewMockLive(ctrl)
	liveMock.EXPECT().GetLiveId().Return(types.LiveID("test")).AnyTimes()

	m := &manager{savers: map[types.LiveID]Recorder{"test": oldRecorder}}
	backup := newRecorder
	newRecorderCalls := 0
	newRecorder = func(context.Context, live.Live) (Recorder, error) {
		newRecorderCalls++
		return newRecorderMock, nil
	}
	defer func() { newRecorder = backup }()

	removeDone := make(chan error, 1)
	go func() {
		removeDone <- m.RemoveRecorder(context.Background(), "test")
	}()
	<-closeStarted

	baseCtx, cancel := context.WithCancel(context.Background())
	waitCtx := &recorderDoneObservedContext{Context: baseCtx, observed: make(chan struct{})}
	addDone := make(chan error, 1)
	go func() {
		addDone <- m.AddRecorder(waitCtx, liveMock)
	}()
	<-waitCtx.observed
	assert.Zero(t, newRecorderCalls, "旧 recorder 完全退出前不应创建新 recorder")
	assert.Equal(t, 2, m.GetActiveRecordingsCount(), "关闭屏障和等待重建的 recorder 都应阻止优雅更新")
	cancel()
	assert.ErrorIs(t, <-addDone, context.Canceled)

	stopBaseCtx, stopCancel := context.WithCancel(context.Background())
	defer stopCancel()
	stopCtx := &recorderDoneObservedContext{Context: stopBaseCtx, observed: make(chan struct{})}
	stopDone := make(chan error, 1)
	go func() {
		stopDone <- m.removeRecorder(stopCtx, "test", recorderRemovalOptions{
			event:               events.NewEventWithSource(listeners.ListenStop, liveMock, closedSourceForTest()),
			allowClosedListener: true,
			waitForClosing:      true,
		})
	}()
	<-stopCtx.observed

	close(releaseClose)
	assert.NoError(t, <-removeDone)
	assert.NoError(t, <-stopDone)
	assert.NoError(t, m.AddRecorder(context.Background(), liveMock))
	assert.Equal(t, 1, newRecorderCalls)
}

func TestManagerRejectsLateRoomNameChangedFromOldListener(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	liveMock := livemock.NewMockLive(ctrl)
	liveMock.EXPECT().GetLiveId().Return(types.LiveID("test")).AnyTimes()
	oldListener := &testListenerEventSource{}
	newListener := &testListenerEventSource{}
	currentRecorder := NewMockRecorder(ctrl)
	m := &manager{
		savers:  map[types.LiveID]Recorder{"test": currentRecorder},
		sources: map[types.LiveID]any{"test": newListener},
	}

	backup := newRecorder
	newRecorder = func(context.Context, live.Live) (Recorder, error) {
		t.Fatal("旧 listener 的迟到事件不应重启新 recorder")
		return nil, nil
	}
	defer func() { newRecorder = backup }()

	assert.NoError(t, m.restartRecorder(context.Background(), liveMock, oldListener))
	assert.Same(t, currentRecorder, m.savers[types.LiveID("test")])
}

func TestManagerDirectRecorderUsesCurrentListenerSource(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	configs.SetCurrentConfig(new(configs.Config))
	listenerSource := &testListenerEventSource{}
	inst := &instance.Instance{
		ListenerManager: &testCurrentListenerManager{listener: listenerSource},
	}
	ctx := context.WithValue(context.Background(), instance.Key, inst)
	m := NewManager(ctx).(*manager)

	liveMock := livemock.NewMockLive(ctrl)
	liveMock.EXPECT().GetLiveId().Return(types.LiveID("test")).AnyTimes()
	liveMock.EXPECT().GetLogger().Return(livelogger.New(0, nil)).AnyTimes()

	oldRecorder := NewMockRecorder(ctrl)
	oldRecorder.EXPECT().Start(ctx).Return(nil)
	oldRecorder.EXPECT().CloseForRestart().Return(nil)
	newRecorderMock := NewMockRecorder(ctrl)
	newRecorderMock.EXPECT().Start(ctx).Return(nil)

	backup := newRecorder
	callCount := 0
	newRecorder = func(context.Context, live.Live) (Recorder, error) {
		callCount++
		if callCount == 1 {
			return oldRecorder, nil
		}
		return newRecorderMock, nil
	}
	defer func() { newRecorder = backup }()

	assert.NoError(t, m.AddRecorder(ctx, liveMock))
	assert.Same(t, listenerSource, m.sources[types.LiveID("test")])

	// 当前 listener 的改名事件必须能够通过来源校验并重启直接录制器。
	assert.NoError(t, m.restartRecorder(ctx, liveMock, listenerSource))
	assert.Same(t, newRecorderMock, m.savers[types.LiveID("test")])
	assert.Same(t, listenerSource, m.sources[types.LiveID("test")])
}

func closedSourceForTest() *testListenerEventSource {
	return &testListenerEventSource{closed: true}
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
		r.EXPECT().CloseAndWait().AnyTimes()
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
