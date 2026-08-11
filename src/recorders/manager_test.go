package recorders

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

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

// TestListenerGenerationTombstoneDropsDelayedEvents 确定性复现：
//
//  1. generation 1 已经 Stop；
//  2. 它在停止后才送达一个旧 LiveStart；
//  3. 用户恢复监控，generation 2 正常 LiveStart 并创建 recorder；
//  4. generation 1 的 LiveEnd/ListenStop 又延迟到达。
//
// 旧实现只看 liveID，会在步骤 2 错误创建 recorder，并在步骤 4 删除 generation 2
// 的新 recorder。现在 Stop tombstone 会拒绝同 generation 的任何后续事件，owner
// 校验也会拒绝旧 generation 删除新 recorder。
func TestListenerGenerationTombstoneDropsDelayedEvents(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	configs.SetCurrentConfig(new(configs.Config))
	inst := &instance.Instance{}
	ctx := context.WithValue(context.Background(), instance.Key, inst)
	dispatcher := events.NewDispatcher(ctx)
	mgr := NewManager(ctx).(*manager)
	mgr.registryListener(ctx, dispatcher)

	liveObject := livemock.NewMockLive(ctrl)
	liveID := types.LiveID("generation-room")
	liveObject.EXPECT().GetLiveId().Return(liveID).AnyTimes()
	liveObject.EXPECT().GetRawUrl().Return("https://example.invalid/live/generation-room").AnyTimes()
	liveObject.EXPECT().GetLogger().Return(livelogger.New(0, nil)).AnyTimes()

	backup := newRecorder
	recorderCreateCount := 0
	newRecorder = func(context.Context, live.Live) (Recorder, error) {
		recorderCreateCount++
		recorder := NewMockRecorder(ctrl)
		recorder.EXPECT().Start(gomock.Any()).Return(nil)
		recorder.EXPECT().Close()
		return recorder, nil
	}
	defer func() { newRecorder = backup }()

	handled := make(chan string, 16)
	for _, eventType := range []events.EventType{
		listeners.ListenStart,
		listeners.LiveStart,
		listeners.LiveEnd,
		listeners.ListenStop,
	} {
		eventType := eventType
		dispatcher.AddEventListener(eventType, events.NewNamedEventListener(
			"test.after_recorder_manager."+string(eventType),
			func(event *events.Event) {
				handled <- event.ID
			},
		))
	}

	dispatchAndWait := func(event *events.Event) {
		t.Helper()
		dispatcher.DispatchEvent(event)
		select {
		case eventID := <-handled:
			assert.Equal(t, event.ID, eventID)
		case <-time.After(2 * time.Second):
			t.Fatalf("等待事件 %s 的 Recorder Manager handler 超时", event.Type)
		}
	}
	causalEvent := func(
		eventType events.EventType,
		producer string,
		generation uint64,
		sequence uint64,
	) *events.Event {
		return events.NewEventWithCausality(ctx, eventType, liveObject, events.Causality{
			ScopeKey:   "opaque-generation-room-key",
			ProducerID: producer,
			Generation: generation,
			Sequence:   sequence,
		})
	}

	// generation 1 先形成终止墓碑。即使后续 LiveStart 的 sequence 更大，
	// 也不能在 Stop 之后复活。
	dispatchAndWait(causalEvent(listeners.ListenStop, "listener-generation-1", 1, 2))
	dispatchAndWait(causalEvent(listeners.LiveStart, "listener-generation-1", 1, 3))
	assert.Equal(t, 0, recorderCreateCount)
	assert.False(t, mgr.HasRecorder(ctx, liveID))

	// 用户恢复监控后 generation 2 可以正常创建 recorder。
	dispatchAndWait(causalEvent(listeners.ListenStart, "listener-generation-2", 2, 1))
	dispatchAndWait(causalEvent(listeners.LiveStart, "listener-generation-2", 2, 2))
	assert.Equal(t, 1, recorderCreateCount)
	assert.True(t, mgr.HasRecorder(ctx, liveID))
	assert.Equal(t, uint64(2), mgr.recorderOwners[liveID].generation)

	// generation 1 的结束事件无权删除 generation 2 的 recorder。
	dispatchAndWait(causalEvent(listeners.LiveEnd, "listener-generation-1", 1, 4))
	dispatchAndWait(causalEvent(listeners.ListenStop, "listener-generation-1", 1, 5))
	dispatchAndWait(causalEvent(listeners.LiveStart, "listener-generation-1", 1, 6))
	assert.True(t, mgr.HasRecorder(ctx, liveID))
	assert.Equal(t, 1, recorderCreateCount)

	// 当前 generation 的 Stop 仍能正常回收自己的 recorder。
	dispatchAndWait(causalEvent(listeners.ListenStop, "listener-generation-2", 2, 3))
	assert.False(t, mgr.HasRecorder(ctx, liveID))
}

func TestManagerCloseRejectsListenerEventsThatEnterAfterClosing(t *testing.T) {
	configs.SetCurrentConfig(new(configs.Config))
	inst := &instance.Instance{}
	ctx := context.WithValue(context.Background(), instance.Key, inst)
	dispatcher := events.NewDispatcher(ctx)
	manager := NewManager(ctx).(*manager)
	manager.registryListener(ctx, dispatcher)

	backup := newRecorder
	var created atomic.Int32
	newRecorder = func(context.Context, live.Live) (Recorder, error) {
		created.Add(1)
		return nil, nil
	}
	defer func() { newRecorder = backup }()

	manager.Close(ctx)

	handled := make(chan struct{})
	dispatcher.AddEventListener(listeners.LiveStart, events.NewNamedEventListener(
		"test.after_closed_recorder_manager",
		func(*events.Event) { close(handled) },
	))
	event := events.NewEventWithCausality(ctx, listeners.LiveStart, struct{}{}, events.Causality{
		ScopeKey:   "closed-room",
		ProducerID: "closed-listener",
		Generation: 1,
		Sequence:   1,
	})
	dispatcher.DispatchEvent(event)
	select {
	case <-handled:
	case <-time.After(time.Second):
		t.Fatal("等待迟到事件派发完成超时")
	}

	assert.Zero(t, created.Load(), "关闭后到达的 LiveStart 不得创建 recorder")
	assert.ErrorIs(t, manager.AddRecorder(ctx, nil), ErrManagerClosed)
}

func TestManagerCloseWaitsForInFlightListenerHandler(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	configs.SetCurrentConfig(new(configs.Config))
	inst := &instance.Instance{}
	ctx := context.WithValue(context.Background(), instance.Key, inst)
	dispatcher := events.NewDispatcher(ctx)
	manager := NewManager(ctx).(*manager)
	manager.registryListener(ctx, dispatcher)

	liveObject := livemock.NewMockLive(ctrl)
	liveID := types.LiveID("in-flight-close")
	liveObject.EXPECT().GetLiveId().Return(liveID).AnyTimes()
	liveObject.EXPECT().GetRawUrl().Return("https://example.invalid/in-flight-close").AnyTimes()
	liveObject.EXPECT().GetLogger().Return(livelogger.New(0, nil)).AnyTimes()

	handlerEntered := make(chan struct{})
	releaseHandler := make(chan struct{})
	recorder := NewMockRecorder(ctrl)
	recorder.EXPECT().Start(gomock.Any()).Return(nil)
	recorder.EXPECT().Close()
	backup := newRecorder
	newRecorder = func(context.Context, live.Live) (Recorder, error) {
		close(handlerEntered)
		<-releaseHandler
		return recorder, nil
	}
	defer func() { newRecorder = backup }()

	dispatcher.DispatchEvent(events.NewEventWithCausality(
		ctx,
		listeners.LiveStart,
		liveObject,
		events.Causality{
			ScopeKey:   "in-flight-close-room",
			ProducerID: "listener-in-flight",
			Generation: 1,
			Sequence:   1,
		},
	))
	select {
	case <-handlerEntered:
	case <-time.After(time.Second):
		t.Fatal("Recorder Manager handler 未进入")
	}

	closeReturned := make(chan struct{})
	go func() {
		manager.Close(ctx)
		close(closeReturned)
	}()
	select {
	case <-closeReturned:
		t.Fatal("正在处理 LiveStart 时 Manager.Close 不应返回")
	case <-time.After(30 * time.Millisecond):
	}

	close(releaseHandler)
	select {
	case <-closeReturned:
	case <-time.After(time.Second):
		t.Fatal("handler 和 recorder 关闭后 Manager.Close 仍未返回")
	}
	assert.False(t, manager.HasRecorder(ctx, liveID))
}

func TestManagerStartCloseWithRPCAndZeroRecordersBalancesWaitGroup(t *testing.T) {
	configs.SetCurrentConfig(&configs.Config{
		RPC: configs.RPC{Enable: true},
	})
	inst := &instance.Instance{}
	ctx := context.WithValue(context.Background(), instance.Key, inst)
	inst.EventDispatcher = events.NewDispatcher(ctx)
	manager := NewManager(ctx)

	assert.NoError(t, manager.Start(ctx))
	assert.NoError(t, manager.Start(ctx))
	manager.Close(ctx)
	manager.Close(ctx)

	waited := make(chan struct{})
	go func() {
		inst.WaitGroup.Wait()
		close(waited)
	}()
	select {
	case <-waited:
	case <-time.After(time.Second):
		t.Fatal("0 recorder 时 Recorder Manager 的 WaitGroup Add/Done 未严格配对")
	}
}
