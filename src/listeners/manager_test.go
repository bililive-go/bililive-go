package listeners

import (
	"context"
	"fmt"
	"sync"
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

const concurrentOperationTimeout = time.Second

type doneObservedContext struct {
	context.Context
	once     sync.Once
	observed chan struct{}
}

func (c *doneObservedContext) Done() <-chan struct{} {
	c.once.Do(func() { close(c.observed) })
	return c.Context.Done()
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
		ln.EXPECT().CloseSync()
		return ln
	}
	defer func() { newListener = backup }()
	l := livemock.NewMockLive(ctrl)
	l.EXPECT().GetLiveId().Return(types.LiveID("test")).Times(2)
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

func TestManagerAddListenerRejectsCanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	m := &manager{savers: make(map[types.LiveID]Listener)}
	liveMock := livemock.NewMockLive(gomock.NewController(t))
	backup := newListener
	newListener = func(context.Context, live.Live) Listener {
		t.Fatal("context 已取消时不应创建 listener")
		return nil
	}
	defer func() { newListener = backup }()

	assert.ErrorIs(t, m.AddListener(ctx, liveMock), context.Canceled)
	assert.Empty(t, m.savers)
}

func TestManagerAddListenerWaitsForSameLiveIDClosing(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	closeStarted := make(chan struct{})
	releaseClose := make(chan struct{})
	oldListener := NewMockListener(ctrl)
	oldListener.EXPECT().CloseSync().Do(func() {
		close(closeStarted)
		<-releaseClose
	})

	newListenerMock := NewMockListener(ctrl)
	newListenerMock.EXPECT().Start().Return(nil)

	liveMock := livemock.NewMockLive(ctrl)
	liveMock.EXPECT().GetLiveId().Return(types.LiveID("test")).Times(2)

	m := &manager{savers: map[types.LiveID]Listener{"test": oldListener}}
	backup := newListener
	newListenerCalls := 0
	newListener = func(context.Context, live.Live) Listener {
		newListenerCalls++
		return newListenerMock
	}
	defer func() { newListener = backup }()

	removeDone := make(chan error, 1)
	go func() {
		removeDone <- m.RemoveListener(context.Background(), "test")
	}()
	<-closeStarted

	baseCtx, cancel := context.WithCancel(context.Background())
	waitCtx := &doneObservedContext{Context: baseCtx, observed: make(chan struct{})}
	addDone := make(chan error, 1)
	go func() {
		addDone <- m.AddListener(waitCtx, liveMock)
	}()
	<-waitCtx.observed
	assert.Zero(t, newListenerCalls, "旧 listener 关闭完成前不应创建新 listener")
	cancel()
	assert.ErrorIs(t, <-addDone, context.Canceled)

	close(releaseClose)
	assert.NoError(t, <-removeDone)
	assert.NoError(t, m.AddListener(context.Background(), liveMock))
	assert.Equal(t, 1, newListenerCalls)
}

func TestManagerRemoveListenerDoesNotBlockOtherLiveID(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	closeStarted := make(chan struct{})
	releaseClose := make(chan struct{})
	oldListener := NewMockListener(ctrl)
	oldListener.EXPECT().CloseSync().Do(func() {
		close(closeStarted)
		<-releaseClose
	})

	otherListener := NewMockListener(ctrl)
	otherListener.EXPECT().Start().Return(nil)
	otherLive := livemock.NewMockLive(ctrl)
	otherLive.EXPECT().GetLiveId().Return(types.LiveID("other"))

	m := &manager{savers: map[types.LiveID]Listener{"test": oldListener}}
	backup := newListener
	newListener = func(context.Context, live.Live) Listener { return otherListener }
	defer func() { newListener = backup }()

	removeDone := make(chan error, 1)
	go func() {
		removeDone <- m.RemoveListener(context.Background(), "test")
	}()
	<-closeStarted

	addDone := make(chan error, 1)
	go func() {
		addDone <- m.AddListener(context.Background(), otherLive)
	}()

	addCompleted := false
	select {
	case err := <-addDone:
		addCompleted = true
		assert.NoError(t, err)
	case <-time.After(concurrentOperationTimeout):
		t.Error("关闭一个房间时阻塞了其他房间的 AddListener")
	}

	close(releaseClose)
	assert.NoError(t, <-removeDone)
	if !addCompleted {
		assert.NoError(t, <-addDone)
	}
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
