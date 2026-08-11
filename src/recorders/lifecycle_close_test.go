package recorders

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	gomock "go.uber.org/mock/gomock"

	"github.com/bililive-go/bililive-go/src/live/mock"
	"github.com/bililive-go/bililive-go/src/pkg/events"
	eventmock "github.com/bililive-go/bililive-go/src/pkg/events/mock"
	"github.com/bililive-go/bililive-go/src/pkg/livelogger"
	parsermock "github.com/bililive-go/bililive-go/src/pkg/parser/mock"
)

// lifecycleOrderingDispatcher 在 RecorderStart 的“接受点”前暂停，用于把
// Start/Close 原先只能偶现的交错变成确定性场景。它只记录生产者提交事件的顺序，
// 不模拟真实 dispatcher 的异步 handler 调度。
type lifecycleOrderingDispatcher struct {
	startEntered chan struct{}
	releaseStart chan struct{}
	startOnce    sync.Once

	mu         sync.Mutex
	eventTypes []events.EventType
}

func newLifecycleOrderingDispatcher() *lifecycleOrderingDispatcher {
	return &lifecycleOrderingDispatcher{
		startEntered: make(chan struct{}),
		releaseStart: make(chan struct{}),
	}
}

func (d *lifecycleOrderingDispatcher) Start(context.Context) error { return nil }

func (d *lifecycleOrderingDispatcher) Close(context.Context) {}

func (d *lifecycleOrderingDispatcher) AddEventListener(events.EventType, *events.EventListener) {}

func (d *lifecycleOrderingDispatcher) RemoveEventListener(events.EventType, *events.EventListener) {
}

func (d *lifecycleOrderingDispatcher) RemoveAllEventListener(events.EventType) {}

func (d *lifecycleOrderingDispatcher) DispatchEvent(event *events.Event) {
	if event.Type == RecorderStart {
		d.startOnce.Do(func() { close(d.startEntered) })
		<-d.releaseStart
	}
	d.mu.Lock()
	d.eventTypes = append(d.eventTypes, event.Type)
	d.mu.Unlock()
}

func (d *lifecycleOrderingDispatcher) DispatchEventSync(event *events.Event) {
	d.DispatchEvent(event)
}

func (d *lifecycleOrderingDispatcher) snapshot() []events.EventType {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]events.EventType(nil), d.eventTypes...)
}

func TestRecorderCloseWaitsForRunAndAuxiliaryTasks(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	liveObject := mock.NewMockLive(ctrl)
	liveObject.EXPECT().GetLogger().Return(livelogger.New(0, nil)).AnyTimes()
	dispatcher := eventmock.NewMockDispatcher(ctrl)
	dispatcher.EXPECT().DispatchEvent(gomock.Any()).Do(func(event *events.Event) {
		assert.Equal(t, RecorderStop, event.Type)
	})

	current := &recorder{
		Live:        liveObject,
		ed:          dispatcher,
		parserLock:  new(sync.RWMutex),
		stop:        make(chan struct{}),
		done:        make(chan struct{}),
		state:       running,
		roomScopeID: "scope_test",
		sessionID:   "session_test",
	}

	runStopped := make(chan struct{})
	go func() {
		<-current.stop
		current.signalDone()
		close(runStopped)
	}()
	releaseAux := make(chan struct{})
	current.auxWg.Add(1)
	go func() {
		defer current.auxWg.Done()
		<-releaseAux
	}()

	closeReturned := make(chan struct{})
	go func() {
		current.Close()
		close(closeReturned)
	}()
	select {
	case <-runStopped:
	case <-time.After(time.Second):
		t.Fatal("Recorder.Close 未请求 run 停止")
	}
	select {
	case <-closeReturned:
		t.Fatal("派生任务尚未退出时 Recorder.Close 不应返回")
	case <-time.After(30 * time.Millisecond):
	}

	close(releaseAux)
	select {
	case <-closeReturned:
	case <-time.After(time.Second):
		t.Fatal("run 和派生任务退出后 Recorder.Close 仍未返回")
	}

	// sync.Once 使并发或重复 Close 共享同一个已完成的关闭屏障。
	current.Close()
	assert.Equal(t, uint32(stopped), atomic.LoadUint32(&current.state))
}

func TestRecorderCloseBeforeStartDoesNotWaitForever(t *testing.T) {
	current := &recorder{
		parserLock: new(sync.RWMutex),
		stop:       make(chan struct{}),
		done:       make(chan struct{}),
		state:      begin,
	}
	returned := make(chan struct{})
	go func() {
		current.Close()
		close(returned)
	}()
	select {
	case <-returned:
	case <-time.After(time.Second):
		t.Fatal("尚未 Start 的 recorder 关闭不应永久等待 done")
	}

	// Close 之后 Start 只能保持 stopped，不能重新启动 run。
	assert.NoError(t, current.Start(context.Background()))
	assert.Equal(t, uint32(stopped), atomic.LoadUint32(&current.state))
}

func TestRecorderConcurrentStartClosePublishesOrderedEvents(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	liveObject := mock.NewMockLive(ctrl)
	liveObject.EXPECT().GetLogger().Return(livelogger.New(0, nil)).AnyTimes()
	liveObject.EXPECT().GetRawUrl().Return("https://example.invalid/room").AnyTimes()

	dispatcher := newLifecycleOrderingDispatcher()
	current := &recorder{
		Live:        liveObject,
		ed:          dispatcher,
		parserLock:  new(sync.RWMutex),
		stop:        make(chan struct{}),
		done:        make(chan struct{}),
		startReady:  make(chan struct{}),
		state:       begin,
		roomScopeID: "scope_ordering",
		sessionID:   "session_ordering",
	}

	// 本测试只验证生命周期发布，不进入真实录制逻辑。预先关闭 stop，使 run
	// 一启动就退出；同时消费 stopOnce，Close 不会重复关闭 channel。
	current.stopOnce.Do(func() { close(current.stop) })

	startReturned := make(chan struct{})
	go func() {
		assert.NoError(t, current.Start(context.Background()))
		close(startReturned)
	}()

	select {
	case <-dispatcher.startEntered:
	case <-time.After(time.Second):
		t.Fatal("Start 未到达 RecorderStart 发布点")
	}

	closeInvoked := make(chan struct{})
	closeReturned := make(chan struct{})
	go func() {
		close(closeInvoked)
		current.Close()
		close(closeReturned)
	}()
	<-closeInvoked

	// 等到 Close 已经取得 stopped 转换，证明它确实与仍被 dispatcher 阻塞的
	// Start 发生交错。此时发布屏障必须让 Close 等待，不能先发 Stop 或返回。
	deadline := time.Now().Add(time.Second)
	for atomic.LoadUint32(&current.state) != stopped && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	assert.Equal(t, uint32(stopped), atomic.LoadUint32(&current.state))
	assert.Empty(t, dispatcher.snapshot())
	select {
	case <-closeReturned:
		t.Fatal("RecorderStart 尚未提交时 Close 不应返回")
	default:
	}
	close(dispatcher.releaseStart)

	select {
	case <-startReturned:
	case <-time.After(time.Second):
		t.Fatal("解除发布点后 Start 未返回")
	}
	select {
	case <-closeReturned:
	case <-time.After(time.Second):
		t.Fatal("Start 完成后 Close 未返回")
	}

	assert.Equal(t,
		[]events.EventType{RecorderStart, RecorderStop},
		dispatcher.snapshot(),
	)
	assert.Equal(t, uint32(stopped), atomic.LoadUint32(&current.state))

	// Close 的同步屏障返回后，不得再有迟到的 Start；重复 Start 也不能复活。
	eventCountAtClose := len(dispatcher.snapshot())
	assert.NoError(t, current.Start(context.Background()))
	assert.Len(t, dispatcher.snapshot(), eventCountAtClose)
}

func TestRecorderCloseCancelsRunContextBeforeWaiting(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	liveObject := mock.NewMockLive(ctrl)
	liveObject.EXPECT().GetLogger().Return(livelogger.New(0, nil)).AnyTimes()
	dispatcher := eventmock.NewMockDispatcher(ctrl)
	dispatcher.EXPECT().DispatchEvent(gomock.Any()).Do(func(event *events.Event) {
		assert.Equal(t, RecorderStop, event.Type)
	})

	current := &recorder{
		Live:       liveObject,
		ed:         dispatcher,
		parserLock: new(sync.RWMutex),
		stop:       make(chan struct{}),
		done:       make(chan struct{}),
		state:      running,
	}
	runCtx, runCancel := context.WithCancel(context.Background())
	current.setRunCancel(runCancel)

	runCancelled := make(chan struct{})
	go func() {
		<-runCtx.Done()
		close(runCancelled)
		current.signalDone()
	}()

	closeReturned := make(chan struct{})
	go func() {
		current.Close()
		close(closeReturned)
	}()

	select {
	case <-runCancelled:
	case <-time.After(time.Second):
		t.Fatal("Close 等待 done 前未取消 recorder 主任务 context")
	}
	select {
	case <-closeReturned:
	case <-time.After(time.Second):
		t.Fatal("主任务响应 context 取消后 Close 未返回")
	}
}

func TestRecorderCloseRejectsParserInstalledAfterStopped(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	liveObject := mock.NewMockLive(ctrl)
	liveObject.EXPECT().GetLogger().Return(livelogger.New(0, nil)).AnyTimes()
	dispatcher := eventmock.NewMockDispatcher(ctrl)
	dispatcher.EXPECT().DispatchEvent(gomock.Any()).Do(func(event *events.Event) {
		assert.Equal(t, RecorderStop, event.Type)
	})

	current := &recorder{
		Live:       liveObject,
		ed:         dispatcher,
		parserLock: new(sync.RWMutex),
		stop:       make(chan struct{}),
		done:       make(chan struct{}),
		state:      running,
	}

	closeReturned := make(chan struct{})
	go func() {
		current.Close()
		close(closeReturned)
	}()

	// 等到 Close 已线性化 stopped，但故意不发布 done，让它保持在同步关闭中。
	deadline := time.Now().Add(time.Second)
	for atomic.LoadUint32(&current.state) != stopped && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if atomic.LoadUint32(&current.state) != stopped {
		t.Fatal("Close 未取得 stopped 状态")
	}

	lateParser := parsermock.NewMockParser(ctrl)
	lateParser.EXPECT().Stop().Return(nil)
	assert.False(t, current.setAndCloseParser(lateParser))
	assert.Nil(t, current.getParser())

	current.signalDone()
	select {
	case <-closeReturned:
	case <-time.After(time.Second):
		t.Fatal("拒绝迟到 parser 且主任务结束后 Close 未返回")
	}
}
