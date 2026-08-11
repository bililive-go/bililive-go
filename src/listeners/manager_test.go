package listeners

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	gomock "go.uber.org/mock/gomock"

	"github.com/bililive-go/bililive-go/src/configs"
	"github.com/bililive-go/bililive-go/src/instance"
	"github.com/bililive-go/bililive-go/src/live"
	livemock "github.com/bililive-go/bililive-go/src/live/mock"
	evtmock "github.com/bililive-go/bililive-go/src/pkg/events/mock"
	"github.com/bililive-go/bililive-go/src/types"
)

type blockingTestListener struct {
	closeEntered chan struct{}
	releaseClose chan struct{}
}

func (l *blockingTestListener) Start() error {
	return nil
}

func (l *blockingTestListener) StartWithInfo(*live.Info) error {
	return nil
}

func (l *blockingTestListener) Close() {
	close(l.closeEntered)
	<-l.releaseClose
}

func (l *blockingTestListener) CloseSync() {
	l.Close()
}

func TestManagerAddAndRemoveListener(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	ctx := context.WithValue(context.Background(), instance.Key, &instance.Instance{})
	m := NewManager(ctx)
	backup := newListener
	newListener = func(ctx context.Context, live live.Live) Listener {
		ln := NewMockListener(ctrl)
		ln.EXPECT().Start().Return(nil)
		ln.EXPECT().Close()
		return ln
	}
	defer func() { newListener = backup }()
	l := livemock.NewMockLive(ctrl)
	l.EXPECT().GetLiveId().Return(types.LiveID("test")).Times(3)
	assert.NoError(t, m.AddListener(context.Background(), l))
	assert.Equal(t, ErrListenerExist, m.AddListener(context.Background(), l))
	ln, err := m.GetListener(context.Background(), "test")
	assert.NoError(t, err)
	assert.NotNil(t, ln)
	assert.True(t, m.HasListener(context.Background(), "test"))
	assert.NoError(t, m.RemoveListener(context.Background(), "test"))
	assert.Equal(t, ErrListenerNotExist, m.RemoveListener(context.Background(), "test"))
	_, err = m.GetListener(context.Background(), "test")
	assert.Equal(t, ErrListenerNotExist, err)
	assert.False(t, m.HasListener(context.Background(), "test"))
}

func TestManagerStartAndClose(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	ed := evtmock.NewMockDispatcher(ctrl)
	ed.EXPECT().AddEventListener(RoomInitializingFinished, gomock.Any())
	configs.SetCurrentConfig(&configs.Config{
		RPC: configs.RPC{Enable: true},
	})
	ctx := context.WithValue(context.Background(), instance.Key, &instance.Instance{
		EventDispatcher: ed,
	})
	backup := newListener
	newListener = func(ctx context.Context, live live.Live) Listener {
		ln := NewMockListener(ctrl)
		ln.EXPECT().Start().Return(nil)
		ln.EXPECT().Close()
		return ln
	}
	defer func() { newListener = backup }()
	m := NewManager(ctx)
	assert.NoError(t, m.Start(ctx))
	for i := 0; i < 3; i++ {
		l := livemock.NewMockLive(ctrl)
		id := types.LiveID(fmt.Sprintf("test_%d", i))
		l.EXPECT().GetLiveId().Return(id).AnyTimes()
		assert.NoError(t, m.AddListener(ctx, l))
	}
	m.Close(ctx)
}

func TestReplaceListenerUsesSynchronousHandoverAndInitialInfo(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	oldListener := NewMockListener(ctrl)
	oldListener.EXPECT().CloseSync()
	newListenerMock := NewMockListener(ctrl)
	info := &live.Info{Status: true}
	newListenerMock.EXPECT().StartWithInfo(info).Return(nil)

	oldLive := livemock.NewMockLive(ctrl)
	oldLive.EXPECT().GetLiveId().Return(types.LiveID("old"))
	newLive := livemock.NewMockLive(ctrl)
	newLive.EXPECT().GetLiveId().Return(types.LiveID("new")).Times(2)

	m := &manager{savers: map[types.LiveID]Listener{"old": oldListener}}
	backup := newListener
	newListener = func(context.Context, live.Live) Listener { return newListenerMock }
	defer func() { newListener = backup }()

	assert.NoError(t, m.replaceListener(context.Background(), oldLive, newLive, info))
	assert.Same(t, newListenerMock, m.savers["new"])
	assert.NotContains(t, m.savers, types.LiveID("old"))
}

func TestManagerStartCloseWithRPCAndZeroListenersBalancesWaitGroup(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	ed := evtmock.NewMockDispatcher(ctrl)
	ed.EXPECT().AddEventListener(RoomInitializingFinished, gomock.Any())
	configs.SetCurrentConfig(&configs.Config{
		RPC: configs.RPC{Enable: true},
	})
	inst := &instance.Instance{EventDispatcher: ed}
	ctx := context.WithValue(context.Background(), instance.Key, inst)
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
		t.Fatal("0 listener 时 Listener Manager 的 WaitGroup Add/Done 未严格配对")
	}
}

func TestManagerCloseWaitsAndRejectsOperationsAfterClosing(t *testing.T) {
	inst := &instance.Instance{}
	ctx := context.WithValue(context.Background(), instance.Key, inst)
	manager := NewManager(ctx)
	assert.NoError(t, manager.Start(ctx))

	blocking := &blockingTestListener{
		closeEntered: make(chan struct{}),
		releaseClose: make(chan struct{}),
	}
	backup := newListener
	newListener = func(context.Context, live.Live) Listener {
		return blocking
	}
	defer func() { newListener = backup }()

	liveObject := &testLiveID{liveID: "close-wait"}
	assert.NoError(t, manager.AddListener(ctx, liveObject))

	closeReturned := make(chan struct{})
	go func() {
		manager.Close(ctx)
		close(closeReturned)
	}()
	select {
	case <-blocking.closeEntered:
	case <-time.After(time.Second):
		t.Fatal("Manager.Close 未调用 listener.Close")
	}
	select {
	case <-closeReturned:
		t.Fatal("listener 尚未退出时 Manager.Close 不应返回")
	case <-time.After(30 * time.Millisecond):
	}

	close(blocking.releaseClose)
	select {
	case <-closeReturned:
	case <-time.After(time.Second):
		t.Fatal("listener 退出后 Manager.Close 仍未返回")
	}

	assert.ErrorIs(t, manager.AddListener(ctx, liveObject), ErrManagerClosed)
	// Close 必须幂等，且 Start 中的全局 WaitGroup 计数已经对称归零。
	manager.Close(ctx)
	waited := make(chan struct{})
	go func() {
		inst.WaitGroup.Wait()
		close(waited)
	}()
	select {
	case <-waited:
	case <-time.After(time.Second):
		t.Fatal("Listener Manager 的 WaitGroup 未归零")
	}
}

// testLiveID 只为 manager 生命周期测试提供 AddListener 实际访问的方法。
// 其余 live.Live 方法由嵌入的 nil 接口兜底；diagnostics 未初始化时不会调用。
type testLiveID struct {
	live.Live
	liveID types.LiveID
}

func (l *testLiveID) GetLiveId() types.LiveID {
	return l.liveID
}
