//go:generate go run go.uber.org/mock/mockgen -package listeners -destination mock_test.go github.com/bililive-go/bililive-go/src/listeners Listener,Manager
package listeners

import (
	"context"
	"sync/atomic"
	"time"

	"github.com/bililive-go/bililive-go/src/configs"
	"github.com/bililive-go/bililive-go/src/consts"
	"github.com/bililive-go/bililive-go/src/instance"
	"github.com/bililive-go/bililive-go/src/live"
	applog "github.com/bililive-go/bililive-go/src/log"
	"github.com/bililive-go/bililive-go/src/notify"
	"github.com/bililive-go/bililive-go/src/pkg/events"
	bilisentry "github.com/bililive-go/bililive-go/src/pkg/sentry"
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
	// 创建一个可取消的 context，用于控制 run 循环中的等待
	runCtx, cancel := context.WithCancel(ctx)
	return &listener{
		Live:      live,
		status:    status{},
		stop:      make(chan struct{}),
		ed:        inst.EventDispatcher.(events.Dispatcher),
		state:     begin,
		runCtx:    runCtx,
		runCancel: cancel,
	}
}

type listener struct {
	Live   live.Live
	status status
	ed     events.Dispatcher

	state     uint32
	stop      chan struct{}
	runCtx    context.Context    // 用于控制 run 循环中的等待
	runCancel context.CancelFunc // 取消 runCtx
}

func (l *listener) Start() error {
	if !atomic.CompareAndSwapUint32(&l.state, begin, pending) {
		return nil
	}
	defer atomic.CompareAndSwapUint32(&l.state, pending, running)

	l.ed.DispatchEvent(events.NewEvent(ListenStart, l.Live))
	l.refresh()
	bilisentry.Go(func() { l.run() })
	return nil
}

func (l *listener) Close() {
	if !atomic.CompareAndSwapUint32(&l.state, running, stopped) {
		return
	}
	l.ed.DispatchEvent(events.NewEvent(ListenStop, l.Live))
	l.runCancel() // 取消 run 循环中的等待
	close(l.stop)
}

// sendLiveNotification 发送直播状态变更通知
func (l *listener) sendLiveNotification(hostName, status string) {
	// 发送通知
	if err := notify.SendNotification(l.Live.GetLogger(), hostName, l.Live.GetPlatformCNName(), l.Live.GetRawUrl(), status); err != nil {
		l.Live.GetLogger().WithError(err).WithField("host", hostName).Error("failed to send notification")
	}
}

// refresh 用于启动时的第一次信息获取（不等待间隔）
func (l *listener) refresh() {
	info, err := l.Live.GetInfo()
	if err != nil {
		l.Live.GetLogger().
			WithError(err).
			WithField("url", l.Live.GetRawUrl()).
			Error("failed to load room info")
		return
	}
	l.processInfo(info)
}

func (l *listener) run() {
	// 使用 GetInfoWithInterval 来处理等待和请求
	// 它会自动获取配置的间隔时间，并在尊重平台速率限制的前提下等待后发送请求
	for {
		select {
		case <-l.stop:
			return
		default:
			// 使用 GetInfoWithInterval，它会等待配置的间隔时间后再发送请求
			info, err := l.Live.GetInfoWithInterval(l.runCtx)
			if err != nil {
				// 如果是 context 取消导致的错误，说明 listener 正在关闭
				if l.runCtx.Err() != nil {
					return
				}
				l.Live.GetLogger().
					WithError(err).
					WithField("url", l.Live.GetRawUrl()).
					Error("failed to load room info")
				continue
			}
			l.processInfo(info)
		}
	}
}

// processInfo 处理获取到的直播间信息，检测状态变化并触发事件
func (l *listener) processInfo(info *live.Info) {
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
}
