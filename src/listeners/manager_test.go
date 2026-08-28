package listeners

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	gomock "go.uber.org/mock/gomock"

	"github.com/bililive-go/bililive-go/src/configs"
	"github.com/bililive-go/bililive-go/src/instance"
	"github.com/bililive-go/bililive-go/src/live"
	livemock "github.com/bililive-go/bililive-go/src/live/mock"
	"github.com/bililive-go/bililive-go/src/pkg/events"
	evtmock "github.com/bililive-go/bililive-go/src/pkg/events/mock"
	"github.com/bililive-go/bililive-go/src/pkg/livelogger"
	"github.com/bililive-go/bililive-go/src/types"
	"github.com/sirupsen/logrus"
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
	newLive.EXPECT().GetLiveId().Return(types.LiveID("new"))
	inst := &instance.Instance{}
	inst.Lives.Set("new", newLive)
	ctx := context.WithValue(context.Background(), instance.Key, inst)

	m := &manager{savers: map[types.LiveID]Listener{"old": oldListener}}
	backup := newListener
	newListener = func(context.Context, live.Live) Listener { return newListenerMock }
	defer func() { newListener = backup }()

	assert.NoError(t, m.replaceListener(ctx, oldLive, newLive, info))
	assert.Same(t, newListenerMock, m.savers["new"])
	assert.NotContains(t, m.savers, types.LiveID("old"))
}

func TestReplaceListenerRejectsExpiredInitializingResult(t *testing.T) {
	ctrl := gomock.NewController(t)
	newLive := livemock.NewMockLive(ctrl)
	newLive.EXPECT().GetLiveId().Return(types.LiveID("new"))
	ctx := context.WithValue(context.Background(), instance.Key, &instance.Instance{})
	m := &manager{savers: map[types.LiveID]Listener{"old": NewMockListener(ctrl)}}

	assert.ErrorIs(t, m.replaceListener(ctx, nil, newLive, &live.Info{}), ErrInitializingResultExpired)
}

func TestInitializingFinishedIgnoresRoomRemovedDuringInitialization(t *testing.T) {
	previousConfig := configs.GetCurrentConfig()
	configs.SetCurrentConfig(configs.NewConfig())
	t.Cleanup(func() { configs.SetCurrentConfig(previousConfig) })

	ctrl := gomock.NewController(t)
	oldLive := livemock.NewMockLive(ctrl)
	originalLive := livemock.NewMockLive(ctrl)
	oldID := types.LiveID("initializing-id")
	rawURL := "https://example.com/room"
	oldLive.EXPECT().GetLiveId().Return(oldID)
	oldLive.EXPECT().Close()
	originalLive.EXPECT().GetLogger().Return(livelogger.New(0, logrus.Fields{"test": t.Name()}))
	originalLive.EXPECT().GetRawUrl().Return(rawURL)

	inst := &instance.Instance{}
	inst.Lives.Set(oldID, oldLive)
	ctx := context.WithValue(context.Background(), instance.Key, inst)
	m := &manager{savers: make(map[types.LiveID]Listener)}

	var registered *events.EventListener
	ed := evtmock.NewMockDispatcher(ctrl)
	ed.EXPECT().AddEventListener(RoomInitializingFinished, gomock.Any()).Do(
		func(_ events.EventType, listener *events.EventListener) { registered = listener },
	)
	m.registryListener(ctx, ed)
	assert.NotNil(t, registered)

	assert.NotPanics(t, func() {
		registered.Handler(events.NewEvent(RoomInitializingFinished, live.InitializingFinishedParam{
			InitializingLive: oldLive,
			Live:             originalLive,
			Info:             &live.Info{},
		}))
	})
	assert.False(t, inst.Lives.Has(oldID), "迟到回调不应把已删除房间留在 LiveMap")
}
