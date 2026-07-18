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
		RPC:       configs.RPC{Enable: false},
		LiveRooms: []configs.LiveRoom{{Url: "https://live.bilibili.com/1", IsListening: true}},
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
	waitDone := make(chan struct{})
	go func() {
		ctx.Value(instance.Key).(*instance.Instance).WaitGroup.Wait()
		close(waitDone)
	}()
	select {
	case <-waitDone:
		t.Fatal("RPC 关闭且 Lives 尚未初始化时，manager 未阻塞主进程")
	case <-time.After(50 * time.Millisecond):
	}
	for i := 0; i < 3; i++ {
		l := livemock.NewMockLive(ctrl)
		id := types.LiveID(fmt.Sprintf("test_%d", i))
		l.EXPECT().GetLiveId().Return(id).AnyTimes()
		assert.NoError(t, m.AddListener(ctx, l))
	}
	m.Close(ctx)
	select {
	case <-waitDone:
	case <-time.After(time.Second):
		t.Fatal("manager 关闭后仍未解除主进程等待")
	}
}
