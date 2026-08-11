package events

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestAddAndRemoveEventListener(t *testing.T) {
	d := NewDispatcher(context.Background()).(*dispatcher)
	l := NewEventListener(func(event *Event) {})
	d.AddEventListener("test", l)
	d.AddEventListener("test2", NewEventListener(func(event *Event) {}))
	ls, ok := d.saver["test"]
	assert.True(t, ok)
	assert.Equal(t, l, ls.Front().Value)
	d.RemoveEventListener("test", l)
	_, ok = d.saver["test"]
	assert.False(t, ok)
	d.RemoveAllEventListener("test2")
	assert.Empty(t, d.saver)
}

func TestDispatchEvent(t *testing.T) {
	received := make(chan int, 4)
	d := NewDispatcher(context.Background()).(*dispatcher)
	d.AddEventListener("test", NewEventListener(func(event *Event) {
		received <- 0
	}))
	d.AddEventListener("test", NewEventListener(func(event *Event) {
		received <- 1
	}))
	d.AddEventListener("test", NewEventListener(func(event *Event) {
		received <- 2
	}))
	d.AddEventListener("test", NewEventListener(func(event *Event) {
		received <- 3
	}))
	d.DispatchEvent(NewEvent("test", nil))

	actual := make([]int, 0, 4)
	timeout := time.NewTimer(time.Second)
	defer timeout.Stop()
	for len(actual) < 4 {
		select {
		case value := <-received:
			actual = append(actual, value)
		case <-timeout.C:
			t.Fatalf("等待事件处理器超时，已收到：%v", actual)
		}
	}
	assert.Equal(t, []int{0, 1, 2, 3}, actual)
}

func TestEventCarriesProducerContextAndID(t *testing.T) {
	type contextKey struct{}
	ctx := context.WithValue(context.Background(), contextKey{}, "producer")
	event := NewEventWithContext(ctx, "test", "payload")

	assert.NotEmpty(t, event.ID)
	assert.Equal(t, "producer", event.Context(context.Background()).Value(contextKey{}))
}

func TestCausalSourceProducesMonotonicEventSequence(t *testing.T) {
	ctx := WithCausalSource(context.Background(), "opaque-room-key", "listener-1", 7)

	first := NewEventWithContext(ctx, "first", nil)
	second := NewEventWithContext(first.Context(context.Background()), "second", nil)

	firstCausality, ok := first.Causality()
	assert.True(t, ok)
	assert.True(t, firstCausality.Valid())
	assert.Equal(t, "opaque-room-key", firstCausality.ScopeKey)
	assert.Equal(t, "listener-1", firstCausality.ProducerID)
	assert.Equal(t, uint64(7), firstCausality.Generation)
	assert.Equal(t, uint64(1), firstCausality.Sequence)

	secondCausality, ok := second.Causality()
	assert.True(t, ok)
	assert.Equal(t, uint64(2), secondCausality.Sequence)
}

func TestExplicitCausalityContinuesInDerivedEvents(t *testing.T) {
	parent := NewEventWithCausality(context.Background(), "parent", nil, Causality{
		ScopeKey:   "opaque-room-key",
		ProducerID: "listener-2",
		Generation: 3,
		Sequence:   41,
	})
	child := NewEventWithContext(parent.Context(context.Background()), "child", nil)

	childCausality, ok := child.Causality()
	assert.True(t, ok)
	assert.Equal(t, uint64(42), childCausality.Sequence)
	assert.Equal(t, uint64(3), childCausality.Generation)
	assert.Equal(t, "listener-2", childCausality.ProducerID)
}

func TestCausalSourceSequenceIsUniqueAcrossGoroutines(t *testing.T) {
	const eventCount = 64
	ctx := WithCausalSource(context.Background(), "opaque-room-key", "listener-concurrent", 9)
	sequences := make(chan uint64, eventCount)

	var waitGroup sync.WaitGroup
	waitGroup.Add(eventCount)
	for index := 0; index < eventCount; index++ {
		go func() {
			defer waitGroup.Done()
			event := NewEventWithContext(ctx, "concurrent", nil)
			causality, ok := event.Causality()
			assert.True(t, ok)
			sequences <- causality.Sequence
		}()
	}
	waitGroup.Wait()
	close(sequences)

	seen := make(map[uint64]struct{}, eventCount)
	for sequence := range sequences {
		seen[sequence] = struct{}{}
	}
	assert.Len(t, seen, eventCount)
	for sequence := uint64(1); sequence <= eventCount; sequence++ {
		_, ok := seen[sequence]
		assert.True(t, ok, "缺少事件生产序号 %d", sequence)
	}
}

func TestNamedEventListener(t *testing.T) {
	handler := func(event *Event) {}
	assert.Equal(t, "stable.handler", NewNamedEventListener("stable.handler", handler).Name)
	assert.NotEmpty(t, NewEventListener(handler).Name)
	assert.Equal(t, "nil_handler", NewEventListener(nil).Name)
}

func TestHandlerPanicAbandonsRemainingHandlers(t *testing.T) {
	firstCalled := make(chan struct{})
	unexpectedSecondCall := make(chan struct{}, 1)
	d := NewDispatcher(context.Background()).(*dispatcher)
	d.AddEventListener("panic", NewNamedEventListener("panic.first", func(event *Event) {
		close(firstCalled)
		panic("test panic")
	}))
	d.AddEventListener("panic", NewNamedEventListener("panic.second", func(event *Event) {
		unexpectedSecondCall <- struct{}{}
	}))

	d.DispatchEvent(NewEvent("panic", nil))
	select {
	case <-firstCalled:
	case <-time.After(time.Second):
		t.Fatal("第一个事件处理器没有执行")
	}

	select {
	case <-unexpectedSecondCall:
		t.Fatal("发生 panic 后仍执行了后续事件处理器")
	case <-time.After(100 * time.Millisecond):
	}
}

func TestDispatchEventSyncWaitsForHandlers(t *testing.T) {
	d := NewDispatcher(context.Background()).(*dispatcher)
	handlerStarted := make(chan struct{})
	releaseHandler := make(chan struct{})
	dispatchDone := make(chan struct{})

	d.AddEventListener("test", NewEventListener(func(event *Event) {
		close(handlerStarted)
		<-releaseHandler
	}))

	go func() {
		d.DispatchEventSync(NewEvent("test", nil))
		close(dispatchDone)
	}()

	select {
	case <-handlerStarted:
	case <-time.After(time.Second):
		t.Fatal("同步事件处理器未启动")
	}

	select {
	case <-dispatchDone:
		t.Fatal("同步派发在事件处理器完成前返回")
	default:
	}

	close(releaseHandler)
	select {
	case <-dispatchDone:
	case <-time.After(time.Second):
		t.Fatal("同步派发未在事件处理器完成后返回")
	}
}

func waitForDispatcherClosing(t *testing.T, dispatcher *dispatcher) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		dispatcher.drainMu.Lock()
		closing := dispatcher.closing
		dispatcher.drainMu.Unlock()
		if closing {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("Dispatcher Close 没有关闭派发闸门")
}

func TestCloseDrainsAcceptedAsyncDispatchAndRejectsExternalDispatch(t *testing.T) {
	d := NewDispatcher(context.Background()).(*dispatcher)
	handlerStarted := make(chan struct{})
	releaseHandler := make(chan struct{})
	closeDone := make(chan struct{})
	var calls atomic.Int32

	d.AddEventListener("test", NewNamedEventListener("test.blocking", func(event *Event) {
		if calls.Add(1) == 1 {
			close(handlerStarted)
			<-releaseHandler
		}
	}))
	d.DispatchEvent(NewEvent("test", nil))

	select {
	case <-handlerStarted:
	case <-time.After(time.Second):
		t.Fatal("已接收的异步 handler 未启动")
	}

	go func() {
		d.Close(context.Background())
		close(closeDone)
	}()
	waitForDispatcherClosing(t, d)

	// Close 线性化之后的外部派发必须被拒绝，不能延长或逃逸 drain。
	d.DispatchEvent(NewEvent("test", nil))
	select {
	case <-closeDone:
		t.Fatal("Close 在已接收 handler 完成前返回")
	default:
	}

	close(releaseHandler)
	select {
	case <-closeDone:
	case <-time.After(time.Second):
		t.Fatal("Close 未在异步 handler 完成后返回")
	}
	time.Sleep(20 * time.Millisecond)
	assert.Equal(t, int32(1), calls.Load())
}

func TestCloseDrainsNestedSynchronousDispatchFromAcceptedHandler(t *testing.T) {
	d := NewDispatcher(context.Background()).(*dispatcher)
	outerStarted := make(chan struct{})
	allowNested := make(chan struct{})
	innerStarted := make(chan struct{})
	releaseInner := make(chan struct{})
	closeDone := make(chan struct{})

	d.AddEventListener("outer", NewNamedEventListener("test.outer", func(event *Event) {
		close(outerStarted)
		<-allowNested
		// 使用 handler 收到的 context 派生事件，明确表示它属于已经接收的
		// 因果链；即使 Close 已经关闭外部门闸，也必须安全地同步执行。
		d.DispatchEventSync(NewEventWithContext(event.Context(context.Background()), "inner", nil))
	}))
	d.AddEventListener("inner", NewNamedEventListener("test.inner", func(event *Event) {
		close(innerStarted)
		<-releaseInner
	}))
	d.DispatchEvent(NewEvent("outer", nil))

	select {
	case <-outerStarted:
	case <-time.After(time.Second):
		t.Fatal("外层 handler 未启动")
	}
	go func() {
		d.Close(context.Background())
		close(closeDone)
	}()
	waitForDispatcherClosing(t, d)
	close(allowNested)

	select {
	case <-innerStarted:
	case <-time.After(time.Second):
		t.Fatal("Close 期间已接收 handler 的嵌套同步事件被错误拒绝")
	}
	select {
	case <-closeDone:
		t.Fatal("Close 在嵌套同步 handler 完成前返回")
	default:
	}

	close(releaseInner)
	select {
	case <-closeDone:
	case <-time.After(time.Second):
		t.Fatal("Close 未能 drain 嵌套同步派发")
	}
}

func TestCloseWaitsForConcurrentSynchronousDispatch(t *testing.T) {
	d := NewDispatcher(context.Background()).(*dispatcher)
	handlerStarted := make(chan struct{})
	releaseHandler := make(chan struct{})
	syncDone := make(chan struct{})
	closeDone := make(chan struct{})

	d.AddEventListener("sync", NewNamedEventListener("test.sync", func(event *Event) {
		close(handlerStarted)
		<-releaseHandler
	}))
	go func() {
		d.DispatchEventSync(NewEvent("sync", nil))
		close(syncDone)
	}()
	select {
	case <-handlerStarted:
	case <-time.After(time.Second):
		t.Fatal("同步 handler 未启动")
	}

	go func() {
		d.Close(context.Background())
		close(closeDone)
	}()
	waitForDispatcherClosing(t, d)
	select {
	case <-closeDone:
		t.Fatal("Close 在同步 handler 完成前返回")
	default:
	}

	close(releaseHandler)
	select {
	case <-syncDone:
	case <-time.After(time.Second):
		t.Fatal("同步派发未完成")
	}
	select {
	case <-closeDone:
	case <-time.After(time.Second):
		t.Fatal("Close 未等待同步派发")
	}
}
