package listeners

import (
	"context"
	"errors"
	"testing"

	"github.com/bluele/gcache"
	"github.com/stretchr/testify/assert"

	"github.com/bililive-go/bililive-go/src/configs"
	"github.com/bililive-go/bililive-go/src/instance"
	livepkg "github.com/bililive-go/bililive-go/src/live"
	livemock "github.com/bililive-go/bililive-go/src/live/mock"
	"github.com/bililive-go/bililive-go/src/log"
	"github.com/bililive-go/bililive-go/src/pkg/events"
	evtmock "github.com/bililive-go/bililive-go/src/pkg/events/mock"
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
	ctx := context.WithValue(context.Background(), instance.Key, &instance.Instance{
		EventDispatcher: ed,
		Config:          cfg,
	})
	log.New(ctx)
	live := livemock.NewMockLive(ctrl)
	l := NewListener(ctx, live).(*listener)

	// false -> false
	live.EXPECT().GetInfo().Return(&livepkg.Info{Status: false}, nil)
	l.refresh()
	assert.False(t, l.status.roomStatus)

	// false -> true
	live.EXPECT().GetInfo().Return(&livepkg.Info{Status: true}, nil)
	live.EXPECT().SetLastStartTime(gomock.Any())
	ed.EXPECT().DispatchEvent(events.NewEvent(LiveStart, live))
	l.refresh()
	assert.True(t, l.status.roomStatus)

	// true -> true, roomName change
	live.EXPECT().GetInfo().Return(&livepkg.Info{Status: true, RoomName: "a"}, nil)
	l.refresh()

	// true -> true, roomName change
	cfg.VideoSplitStrategies.OnRoomNameChanged = true
	live.EXPECT().GetInfo().Return(&livepkg.Info{Status: true, RoomName: "b"}, nil)
	ed.EXPECT().DispatchEvent(events.NewEvent(RoomNameChanged, live))
	l.refresh()

	// true -> false
	live.EXPECT().GetInfo().Return(&livepkg.Info{Status: false}, nil)
	ed.EXPECT().DispatchEvent(events.NewEvent(LiveEnd, live))
	l.refresh()
	assert.False(t, l.status.roomStatus)
}

func TestRefreshWithError(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	ed := evtmock.NewMockDispatcher(ctrl)
	cache := gcache.New(4).LRU().Build()
	ctx := context.WithValue(context.Background(), instance.Key, &instance.Instance{
		EventDispatcher: ed,
		Cache:           cache,
		Config:          configs.NewConfig(),
	})
	log.New(ctx)
	live := livemock.NewMockLive(ctrl)
	l := NewListener(ctx, live).(*listener)

	live.EXPECT().GetInfo().Return(nil, errors.New("this is error"))
	live.EXPECT().GetRawUrl().Return("")
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
	ctx := context.WithValue(context.Background(), instance.Key, &instance.Instance{
		EventDispatcher: ed,
		Cache:           cache,
		Config:          config,
	})
	log.New(ctx)
	live := livemock.NewMockLive(ctrl)
	live.EXPECT().GetInfo().Return(&livepkg.Info{Status: false}, nil)
	ed.EXPECT().DispatchEvent(gomock.Any()).Times(2)
	l := NewListener(ctx, live)
	assert.NoError(t, l.Start())
	assert.NoError(t, l.Start())
	l.Close()
	l.Close()
}
