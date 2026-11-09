package listeners

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"

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
	ed.EXPECT().DispatchEvent(events.NewEvent(LiveStart, live))
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
	ed.EXPECT().DispatchEvent(events.NewEvent(RoomNameChanged, live))
	l.refresh()

	// true -> false
	live.EXPECT().GetInfo().Return(&livepkg.Info{Status: false}, nil)
	live.EXPECT().GetRawUrl().Return("").AnyTimes() // 添加对GetRawUrl方法的期望调用
	live.EXPECT().GetPlatformCNName().Return("platform").AnyTimes()
	ed.EXPECT().DispatchEvent(events.NewEvent(LiveEnd, live))
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
	l := NewListener(ctx, live).(*listener)

	live.EXPECT().GetInfo().Return(nil, errors.New("this is error"))
	live.EXPECT().GetRawUrl().Return("")
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
	live.EXPECT().GetPlatformCNName().Return("platform").AnyTimes()
	live.EXPECT().GetRawUrl().Return("").AnyTimes() // 添加对GetRawUrl方法的期望调用
	ed.EXPECT().DispatchEvent(gomock.Any()).Times(2)
	l := NewListener(ctx, live)
	assert.NoError(t, l.Start())
	assert.NoError(t, l.Start())
	l.Close()
	l.Close()
}

func TestClosedListenerDoesNotPublishInfo(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	ed := evtmock.NewMockDispatcher(ctrl)
	ctx := context.WithValue(context.Background(), instance.Key, &instance.Instance{EventDispatcher: ed})
	live := livemock.NewMockLive(ctrl)
	l := NewListener(ctx, live).(*listener)
	atomic.StoreUint32(&l.state, running)
	ed.EXPECT().DispatchEvent(events.NewEvent(ListenStop, live))
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
	l := NewListener(ctx, live).(*listener)
	atomic.StoreUint32(&l.state, running)
	ed.EXPECT().DispatchEventSync(events.NewEvent(ListenStop, live))

	l.CloseSync()
	assert.True(t, l.isStopped())
}

func TestRoomNameSplitUsesRoomOverride(t *testing.T) {
	ctrl := gomock.NewController(t)
	cfg := configs.NewConfig()
	cfg.VideoSplitStrategies.OnRoomNameChanged = false
	cfg.LiveRooms = []configs.LiveRoom{
		{Url: "https://live.bilibili.com/1", OverridableConfig: configs.OverridableConfig{VideoSplitStrategies: &configs.VideoSplitStrategies{OnRoomNameChanged: true}}},
		{Url: "https://live.bilibili.com/2"},
	}
	previous := configs.GetCurrentConfig()
	configs.SetCurrentConfig(cfg)
	t.Cleanup(func() { configs.SetCurrentConfig(previous) })
	for i, room := range cfg.LiveRooms {
		ed := evtmock.NewMockDispatcher(ctrl)
		ctx := context.WithValue(context.Background(), instance.Key, &instance.Instance{EventDispatcher: ed})
		log.New(ctx)
		l := livemock.NewMockLive(ctrl)
		l.EXPECT().GetRawUrl().Return(room.Url).AnyTimes()
		l.EXPECT().GetLogger().Return(livelogger.New(1024, nil)).AnyTimes()
		l.EXPECT().GetPlatformCNName().Return("哔哩哔哩").AnyTimes()
		listener := NewListener(ctx, l).(*listener)
		listener.status = status{roomStatus: true, roomName: "旧标题"}
		if i == 0 {
			ed.EXPECT().DispatchEvent(events.NewEvent(RoomNameChanged, l))
		}
		listener.processInfo(&livepkg.Info{Status: true, RoomName: "新标题"})
	}
}
