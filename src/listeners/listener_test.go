package listeners

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bluele/gcache"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"

	"github.com/bililive-go/bililive-go/src/configs"
	"github.com/bililive-go/bililive-go/src/instance"
	livepkg "github.com/bililive-go/bililive-go/src/live"
	livemock "github.com/bililive-go/bililive-go/src/live/mock"
	"github.com/bililive-go/bililive-go/src/log"
	"github.com/bililive-go/bililive-go/src/pkg/events"
	evtmock "github.com/bililive-go/bililive-go/src/pkg/events/mock"
	"github.com/bililive-go/bililive-go/src/pkg/livelogger"
	gomock "go.uber.org/mock/gomock"
)

func TestRefresh(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	ed := evtmock.NewMockDispatcher(ctrl)
	cfg := configs.NewConfig()
	cfg.VideoSplitStrategies = configs.VideoSplitStrategies{
		OnRoomNameChanged: false,
	}
	configs.SetCurrentConfig(cfg)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ctx = context.WithValue(ctx, instance.Key, &instance.Instance{
		EventDispatcher: ed,
	})
	log.New(ctx)
	live := livemock.NewMockLive(ctrl)
	// 创建一个测试用的 LiveLogger
	testLogger := livelogger.New(1024, logrus.Fields{"test": "listener"})
	live.EXPECT().GetLogger().Return(testLogger).AnyTimes()
	live.EXPECT().GetRawUrl().Return("").AnyTimes()
	l := NewListener(ctx, live).(*listener)

	// false -> false
	live.EXPECT().GetInfo().Return(&livepkg.Info{Status: false}, nil)
	live.EXPECT().GetRawUrl().Return("").AnyTimes()                 // 添加对GetRawUrl方法的期望调用
	live.EXPECT().GetPlatformCNName().Return("platform").AnyTimes() // 添加对GetPlatformCNName方法的期望调用
	l.refresh()
	assert.False(t, l.status.roomStatus)

	// false -> true
	live.EXPECT().GetInfo().Return(&livepkg.Info{Status: true}, nil)
	live.EXPECT().SetLastStartTime(gomock.Any())
	live.EXPECT().GetPlatformCNName().Return("platform").AnyTimes()
	ed.EXPECT().DispatchEvent(gomock.Any()).Do(func(event *events.Event) {
		assert.Equal(t, LiveStart, event.Type)
		assert.Equal(t, live, event.Object)
		assert.NotEmpty(t, event.ID)
	})
	l.refresh()
	assert.True(t, l.status.roomStatus)

	// true -> true, roomName change
	live.EXPECT().GetInfo().Return(&livepkg.Info{Status: true, RoomName: "a"}, nil)
	live.EXPECT().GetRawUrl().Return("").AnyTimes()                 // 添加对GetRawUrl方法的期望调用
	live.EXPECT().GetPlatformCNName().Return("platform").AnyTimes() // 添加对GetPlatformCNName方法的期望调用
	l.refresh()

	// true -> true, roomName change
	cfg.VideoSplitStrategies.OnRoomNameChanged = true
	live.EXPECT().GetInfo().Return(&livepkg.Info{Status: true, RoomName: "b"}, nil)
	live.EXPECT().GetRawUrl().Return("").AnyTimes()                 // 添加对GetRawUrl方法的期望调用
	live.EXPECT().GetPlatformCNName().Return("platform").AnyTimes() // 添加对GetPlatformCNName方法的期望调用
	ed.EXPECT().DispatchEvent(gomock.Any()).Do(func(event *events.Event) {
		assert.Equal(t, RoomNameChanged, event.Type)
		assert.Equal(t, live, event.Object)
	})
	l.refresh()

	// true -> false
	live.EXPECT().GetInfo().Return(&livepkg.Info{Status: false}, nil)
	live.EXPECT().GetRawUrl().Return("").AnyTimes() // 添加对GetRawUrl方法的期望调用
	live.EXPECT().GetPlatformCNName().Return("platform").AnyTimes()
	ed.EXPECT().DispatchEvent(gomock.Any()).Do(func(event *events.Event) {
		assert.Equal(t, LiveEnd, event.Type)
		assert.Equal(t, live, event.Object)
	})
	l.refresh()
	assert.False(t, l.status.roomStatus)
}

func TestRefreshWithError(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	ed := evtmock.NewMockDispatcher(ctrl)
	cache := gcache.New(4).LRU().Build()
	configs.SetCurrentConfig(configs.NewConfig())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ctx = context.WithValue(ctx, instance.Key, &instance.Instance{
		EventDispatcher: ed,
		Cache:           cache,
	})
	log.New(ctx)
	live := livemock.NewMockLive(ctrl)
	// 创建一个测试用的 LiveLogger
	testLogger := livelogger.New(1024, logrus.Fields{"test": "listener"})
	live.EXPECT().GetLogger().Return(testLogger).AnyTimes()
	live.EXPECT().GetRawUrl().Return("").AnyTimes()
	l := NewListener(ctx, live).(*listener)

	live.EXPECT().GetInfo().Return(nil, errors.New("this is error"))
	live.EXPECT().GetRawUrl().Return("").AnyTimes()
	live.EXPECT().GetPlatformCNName().Return("platform").AnyTimes() // 添加对GetPlatformCNName方法的期望调用
	l.refresh()
	assert.False(t, l.status.roomStatus)
}

func TestListenerStartAndClose(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	ed := evtmock.NewMockDispatcher(ctrl)
	cache := gcache.New(4).LRU().Build()
	config := configs.NewConfig()
	config.Interval = 5
	configs.SetCurrentConfig(config)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ctx = context.WithValue(ctx, instance.Key, &instance.Instance{
		EventDispatcher: ed,
		Cache:           cache,
	})
	log.New(ctx)
	live := livemock.NewMockLive(ctrl)
	// 创建一个测试用的 LiveLogger
	testLogger := livelogger.New(1024, logrus.Fields{"test": "listener"})
	live.EXPECT().GetLogger().Return(testLogger).AnyTimes()
	live.EXPECT().GetInfo().Return(&livepkg.Info{Status: false}, nil).AnyTimes()
	live.EXPECT().GetInfoWithInterval(gomock.Any()).Return(nil, context.Canceled).AnyTimes()
	live.EXPECT().GetPlatformCNName().Return("platform").AnyTimes()
	live.EXPECT().GetRawUrl().Return("").AnyTimes() // 添加对GetRawUrl方法的期望调用
	var dispatched []*events.Event
	ed.EXPECT().DispatchEvent(gomock.Any()).Do(func(event *events.Event) {
		dispatched = append(dispatched, event)
	}).Times(2)
	l := NewListener(ctx, live)
	assert.NoError(t, l.Start())
	assert.NoError(t, l.Start())
	l.Close()
	l.Close()

	if assert.Len(t, dispatched, 2) {
		assert.Equal(t, ListenStart, dispatched[0].Type)
		assert.Equal(t, ListenStop, dispatched[1].Type)
		startCausality, startOK := dispatched[0].Causality()
		stopCausality, stopOK := dispatched[1].Causality()
		assert.True(t, startOK)
		assert.True(t, stopOK)
		assert.True(t, startCausality.Valid())
		assert.Equal(t, startCausality.ScopeKey, stopCausality.ScopeKey)
		assert.Equal(t, startCausality.ProducerID, stopCausality.ProducerID)
		assert.Equal(t, startCausality.Generation, stopCausality.Generation)
		assert.Greater(t, stopCausality.Sequence, startCausality.Sequence)
	}
}

func TestListenerCloseWaitsForPollingGoroutineAndTraceTask(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	ed := evtmock.NewMockDispatcher(ctrl)
	configs.SetCurrentConfig(configs.NewConfig())
	inst := &instance.Instance{
		EventDispatcher: ed,
		Cache:           gcache.New(4).LRU().Build(),
	}
	ctx := context.WithValue(context.Background(), instance.Key, inst)

	liveObject := livemock.NewMockLive(ctrl)
	logger := livelogger.New(16, logrus.Fields{"test": "listener-close-wait"})
	liveObject.EXPECT().GetLogger().Return(logger).AnyTimes()
	liveObject.EXPECT().GetRawUrl().Return("https://example.invalid/close-wait").AnyTimes()
	liveObject.EXPECT().GetPlatformCNName().Return("platform").AnyTimes()
	liveObject.EXPECT().GetInfo().Return(&livepkg.Info{Status: false}, nil)

	pollEntered := make(chan struct{})
	releasePoll := make(chan struct{})
	liveObject.EXPECT().
		GetInfoWithInterval(gomock.Any()).
		DoAndReturn(func(fetchCtx context.Context) (*livepkg.Info, error) {
			close(pollEntered)
			<-releasePoll
			return nil, fetchCtx.Err()
		})
	ed.EXPECT().DispatchEvent(gomock.Any()).Times(2)

	current := NewListener(ctx, liveObject)
	assert.NoError(t, current.Start())
	select {
	case <-pollEntered:
	case <-time.After(time.Second):
		t.Fatal("listener 轮询 goroutine 未启动")
	}

	closeReturned := make(chan struct{})
	go func() {
		current.Close()
		close(closeReturned)
	}()
	select {
	case <-closeReturned:
		t.Fatal("底层轮询尚未退出时 Listener.Close 不应返回")
	case <-time.After(30 * time.Millisecond):
	}

	close(releasePoll)
	select {
	case <-closeReturned:
	case <-time.After(time.Second):
		t.Fatal("轮询退出后 Listener.Close 仍未返回")
	}
}

func TestListenerCloseWhileInitialRefreshPendingDoesNotDeadlock(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	ed := evtmock.NewMockDispatcher(ctrl)
	configs.SetCurrentConfig(configs.NewConfig())
	inst := &instance.Instance{
		EventDispatcher: ed,
		Cache:           gcache.New(4).LRU().Build(),
	}
	ctx := context.WithValue(context.Background(), instance.Key, inst)

	liveObject := livemock.NewMockLive(ctrl)
	logger := livelogger.New(16, logrus.Fields{"test": "listener-pending-close"})
	liveObject.EXPECT().GetLogger().Return(logger).AnyTimes()
	liveObject.EXPECT().GetRawUrl().Return("https://example.invalid/pending-close").AnyTimes()
	liveObject.EXPECT().GetPlatformCNName().Return("platform").AnyTimes()

	initialRefreshEntered := make(chan struct{})
	releaseInitialRefresh := make(chan struct{})
	liveObject.EXPECT().GetInfo().DoAndReturn(func() (*livepkg.Info, error) {
		close(initialRefreshEntered)
		<-releaseInitialRefresh
		return &livepkg.Info{Status: false}, nil
	})

	var dispatched []events.EventType
	var dispatchedMu sync.Mutex
	ed.EXPECT().DispatchEvent(gomock.Any()).Do(func(event *events.Event) {
		dispatchedMu.Lock()
		dispatched = append(dispatched, event.Type)
		dispatchedMu.Unlock()
	}).Times(2)

	current := NewListener(ctx, liveObject)
	startReturned := make(chan struct{})
	go func() {
		assert.NoError(t, current.Start())
		close(startReturned)
	}()
	select {
	case <-initialRefreshEntered:
	case <-time.After(time.Second):
		t.Fatal("listener 未进入首次 refresh")
	}

	closeReturned := make(chan struct{})
	go func() {
		current.Close()
		close(closeReturned)
	}()
	select {
	case <-closeReturned:
		t.Fatal("首次 refresh 尚未退出时 Listener.Close 不应提前返回")
	case <-time.After(30 * time.Millisecond):
	}

	close(releaseInitialRefresh)
	select {
	case <-startReturned:
	case <-time.After(time.Second):
		t.Fatal("释放首次 refresh 后 Listener.Start 仍未返回")
	}
	select {
	case <-closeReturned:
	case <-time.After(time.Second):
		t.Fatal("pending listener 的 run 退出后 Listener.Close 仍未返回")
	}

	dispatchedMu.Lock()
	defer dispatchedMu.Unlock()
	assert.Equal(t, []events.EventType{ListenStart, ListenStop}, dispatched)
}

func TestClosedListenerDoesNotPublishInfo(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	ed := evtmock.NewMockDispatcher(ctrl)
	ctx := context.WithValue(context.Background(), instance.Key, &instance.Instance{EventDispatcher: ed})
	live := livemock.NewMockLive(ctrl)
	live.EXPECT().GetRawUrl().Return("https://example.invalid/closed-listener").AnyTimes()
	l := NewListener(ctx, live).(*listener)
	atomic.StoreUint32(&l.state, running)
	l.startEventSent.Store(true)
	l.signalStartReady()
	go func() {
		<-l.stop
		l.signalDone()
	}()
	ed.EXPECT().DispatchEvent(gomock.Any()).Do(func(event *events.Event) {
		assert.Equal(t, ListenStop, event.Type)
		assert.Same(t, live, event.Object)
	})
	l.Close()

	// 模拟网络请求在 listener 关闭后才返回开播信息；不得再发布 LiveStart。
	l.processInfo(&livepkg.Info{Status: true})
	assert.False(t, l.status.roomStatus)
}

func TestListenerCloseSyncWaitsForStopHandlers(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	ed := evtmock.NewMockDispatcher(ctrl)
	ctx := context.WithValue(context.Background(), instance.Key, &instance.Instance{EventDispatcher: ed})
	live := livemock.NewMockLive(ctrl)
	live.EXPECT().GetRawUrl().Return("https://example.invalid/close-sync").AnyTimes()
	l := NewListener(ctx, live).(*listener)
	atomic.StoreUint32(&l.state, running)
	l.startEventSent.Store(true)
	l.signalStartReady()
	go func() {
		<-l.stop
		l.signalDone()
	}()
	ed.EXPECT().DispatchEventSync(gomock.Any()).Do(func(event *events.Event) {
		assert.Equal(t, ListenStop, event.Type)
		assert.Same(t, live, event.Object)
	})

	l.CloseSync()
	assert.True(t, l.isStopped())
}
