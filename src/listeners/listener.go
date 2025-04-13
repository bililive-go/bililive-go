//go:generate mockgen -package listeners -destination mock_test.go github.com/hr3lxphr6j/bililive-go/src/listeners Listener,Manager
package listeners

import (
	"context"
	"sync/atomic"
	"time"

	"github.com/lthibault/jitterbug"

	"github.com/hr3lxphr6j/bililive-go/src/configs"
	"github.com/hr3lxphr6j/bililive-go/src/instance"
	"github.com/hr3lxphr6j/bililive-go/src/interfaces"
	"github.com/hr3lxphr6j/bililive-go/src/live"
	"github.com/hr3lxphr6j/bililive-go/src/live/system"
	"github.com/hr3lxphr6j/bililive-go/src/pkg/events"
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
		config: inst.Config,
		stop:   make(chan struct{}),
		ed:     inst.EventDispatcher.(events.Dispatcher),
		logger: inst.Logger,
		state:  begin,
	}
}

type listener struct {
	Live   live.Live
	status status

	config *configs.Config
	ed     events.Dispatcher
	logger *interfaces.Logger

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

func (l *listener) refresh() {
	info, err := l.Live.GetInfo()
	if err != nil {
		l.logger.
			WithError(err).
			WithField("url", l.Live.GetRawUrl()).
			Error("failed to load room info")
		return
	}

	room, err := l.config.GetLiveRoomByUrl(l.Live.GetRawUrl())
	if err != nil {
		l.logger.
			WithError(err).
			WithField("url", l.Live.GetRawUrl()).
			Error("failed to get room config")
		return
	}

	var (
		latestStatus = status{roomName: info.RoomName, roomStatus: info.Status}
		evtTyp       events.EventType
		logInfo      string
		fields       = map[string]interface{}{
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
	case statusToFalseEvt:
		evtTyp = LiveEnd
		logInfo = "Live end"
	case roomNameChangedEvt:
		// 优先使用直播间的配置，如果没有则使用全局配置
		if !room.VideoSplitStrategies.OnRoomNameChanged && !l.config.VideoSplitStrategies.OnRoomNameChanged {
			return
		}
		evtTyp = RoomNameChanged
		logInfo = "Room name was changed"
	}
	if isStatusChanged {
		l.ed.DispatchEvent(events.NewEvent(evtTyp, l.Live))
		l.logger.WithFields(fields).Info(logInfo)
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
	ticker := jitterbug.New(
		time.Duration(l.config.Interval)*time.Second,
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
