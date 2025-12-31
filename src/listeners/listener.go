//go:generate go run go.uber.org/mock/mockgen -package listeners -destination mock_test.go github.com/bililive-go/bililive-go/src/listeners Listener,Manager
package listeners

import (
	"context"
	"sync/atomic"
	"time"

	"github.com/lthibault/jitterbug"

	"github.com/bililive-go/bililive-go/src/configs"
	"github.com/bililive-go/bililive-go/src/consts"
	"github.com/bililive-go/bililive-go/src/instance"
	"github.com/bililive-go/bililive-go/src/live"
	"github.com/bililive-go/bililive-go/src/live/system"
	applog "github.com/bililive-go/bililive-go/src/log"
	"github.com/bililive-go/bililive-go/src/notify"
	"github.com/bililive-go/bililive-go/src/pkg/events"
)

const (
	begin uint32 = iota
	pending
	running
	stopped
)

type Listener interface {
	Start() error
	Close()
}

func NewListener(ctx context.Context, live live.Live) Listener {
	inst := instance.GetInstance(ctx)
	return &listener{
		Live:   live,
		status: status{},
		stop:   make(chan struct{}),
		ed:     inst.EventDispatcher.(events.Dispatcher),
		state:  begin,
	}
}

type listener struct {
	Live   live.Live
	status status
	ed     events.Dispatcher

	state uint32
	stop  chan struct{}
}

func (l *listener) Start() error {
	if !atomic.CompareAndSwapUint32(&l.state, begin, pending) {
		return nil
	}
	defer atomic.CompareAndSwapUint32(&l.state, pending, running)

	l.ed.DispatchEvent(events.NewEvent(ListenStart, l.Live))
	l.refresh()
	go l.run()
	return nil
}

func (l *listener) Close() {
	if !atomic.CompareAndSwapUint32(&l.state, running, stopped) {
		return
	}
	l.ed.DispatchEvent(events.NewEvent(ListenStop, l.Live))
	close(l.stop)
}

// sendLiveNotification 发送直播状态变更通知
func (l *listener) sendLiveNotification(hostName, status string) {
	// 创建context用于日志记录
	ctx := context.Background()
	// 发送通知
	if err := notify.SendNotification(ctx, hostName, l.Live.GetPlatformCNName(), l.Live.GetRawUrl(), status); err != nil {
		applog.GetLogger().WithError(err).WithField("host", hostName).Error("failed to send notification")
	}
}

func (l *listener) refresh() {
	info, err := l.Live.GetInfo()
	if err != nil {
		applog.GetLogger().
			WithError(err).
			WithField("url", l.Live.GetRawUrl()).
			Error("failed to load room info")
		return
	}

	// 尝试从缓存中获取主播姓名，以防API调用失败
	hostName := info.HostName
	if hostName == "" {
		if wrappedLive, ok := l.Live.(*live.WrappedLive); ok {
			if cachedInfo, get_err := wrappedLive.GetInfo(); get_err == nil && cachedInfo != nil {
				hostName = cachedInfo.HostName
			}
		}
	}

	var (
		latestStatus = status{roomName: info.RoomName, roomStatus: info.Status}
		evtTyp       events.EventType
		logInfo      string
		fields       = map[string]any{
			"room": info.RoomName,
			"host": info.HostName,
		}
	)
	defer func() { l.status = latestStatus }()

	isStatusChanged := true
	switch l.status.Diff(latestStatus) {
	case 0:
		isStatusChanged = false
	case statusToTrueEvt:
		l.Live.SetLastStartTime(time.Now())
		evtTyp = LiveStart
		logInfo = "Live Start"
		// 发送开播提醒和录像通知
		l.sendLiveNotification(hostName, consts.LiveStatusStart)

	case statusToFalseEvt:
		evtTyp = LiveEnd
		logInfo = "Live end"
		// 发送结束直播提醒和录像通知
		l.sendLiveNotification(hostName, consts.LiveStatusStop)
	case roomNameChangedEvt:
		cfg := configs.GetCurrentConfig()
		if cfg == nil {
			// 如果配置为空，可能是系统正在初始化或关闭，这不一定是错误，但在这里返回是安全的
			// 为了防止 NPE，我们需要显式检查
			return
		}
		if !cfg.VideoSplitStrategies.OnRoomNameChanged {
			return
		}
		evtTyp = RoomNameChanged
		logInfo = "Room name was changed"
	}
	if isStatusChanged {
		l.ed.DispatchEvent(events.NewEvent(evtTyp, l.Live))
		applog.GetLogger().WithFields(fields).Info(logInfo)
	}

	if info.Initializing {
		initializingLive := l.Live.(*live.WrappedLive).Live.(*system.InitializingLive)
		info, err = initializingLive.OriginalLive.GetInfo()
		if err == nil {
			l.ed.DispatchEvent(events.NewEvent(RoomInitializingFinished, live.InitializingFinishedParam{
				InitializingLive: l.Live,
				Live:             initializingLive.OriginalLive,
				Info:             info,
			}))
		}
	}
}

func (l *listener) run() {
	interval := 30
	cfg := configs.GetCurrentConfig()
	if cfg != nil {
		if cfg.Interval > 0 {
			interval = cfg.Interval
		} else {
			applog.GetLogger().Warn("config interval is <= 0, using default 30s")
		}
	}
	ticker := jitterbug.New(
		time.Duration(interval)*time.Second,
		jitterbug.Norm{
			Stdev: time.Second * 3,
		},
	)
	defer ticker.Stop()

	for {
		select {
		case <-l.stop:
			return
		case <-ticker.C:
			l.refresh()
		}
	}
}
