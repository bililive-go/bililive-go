//go:generate go run go.uber.org/mock/mockgen -package listeners -destination mock_test.go github.com/bililive-go/bililive-go/src/listeners Listener,Manager
package listeners

import (
	"context"
	"errors"
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
	StartWithInfo(info *live.Info) error
	Close()
	CloseSync()
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
	return l.start(nil)
}

// StartWithInfo 使用已经获取到的直播间信息启动监听器，避免初始化交接后立即重复请求平台。
func (l *listener) StartWithInfo(info *live.Info) error {
	return l.start(info)
}

func (l *listener) start(initialInfo *live.Info) error {
	if !atomic.CompareAndSwapUint32(&l.state, begin, pending) {
		return nil
	}
	defer atomic.CompareAndSwapUint32(&l.state, pending, running)

	l.ed.DispatchEvent(events.NewEventWithSource(ListenStart, l.Live, l))

	// 首次信息获取放到后台执行。调用方 manager.AddListener 全程持有管理器的全局锁，
	// 而 refresh 是一次真实的网络请求，还要排队等待平台访问频率限制；
	// 几百个直播间串行下来会把锁占用好几分钟，期间任何增删直播间的操作都会被卡住。
	bilisentry.Go(func() {
		if !l.isStopped() {
			if initialInfo != nil {
				l.processInfo(initialInfo)
			} else {
				l.refresh()
			}
		}
		l.run()
	})
	return nil
}

// isStopped 返回 listener 是否已经被关闭
func (l *listener) isStopped() bool {
	select {
	case <-l.stop:
		return true
	default:
		return false
	}
}

// IsClosed 供事件处理器判断事件是否来自已经关闭的 listener 实例。
func (l *listener) IsClosed() bool {
	return l.isStopped()
}

func (l *listener) Close() {
	l.close(false)
}

// CloseSync 同步完成 ListenStop 的所有处理器，用于调用方必须等待下游状态清理
// 完成后才能继续的 listener 交接路径。
func (l *listener) CloseSync() {
	l.close(true)
}

func (l *listener) close(syncEvent bool) {
	if !atomic.CompareAndSwapUint32(&l.state, running, stopped) {
		return
	}
	l.runCancel() // 先取消等待并标记停止，禁止并发请求结果继续发布事件
	close(l.stop)
	event := events.NewEventWithSource(ListenStop, l.Live, l)
	if syncEvent {
		l.ed.DispatchEventSync(event)
	} else {
		l.ed.DispatchEvent(event)
	}
}

// sendLiveNotification 发送直播状态变更通知
func (l *listener) sendLiveNotification(hostName, status string) {
	// 检查是否为仅提醒模式
	notifyOnly := false
	if cfg := configs.GetCurrentConfig(); cfg != nil {
		if room, err := cfg.GetLiveRoomByUrl(l.Live.GetRawUrl()); err == nil {
			notifyOnly = room.NotifyOnly
		}
	}

	// 发送通知
	if err := notify.SendNotification(l.Live.GetLogger(), hostName, l.Live.GetPlatformCNName(), l.Live.GetRawUrl(), status, notifyOnly); err != nil {
		l.Live.GetLogger().WithError(err).WithField("host", hostName).Error("failed to send notification")
	}
}

// refresh 用于启动时的第一次信息获取（不等待间隔）
func (l *listener) refresh() {
	info, err := l.Live.GetInfo()
	if err != nil {
		l.logGetInfoError(err)
		return
	}
	l.processInfo(info)
}

// logGetInfoError 记录获取直播间信息失败的日志。
// 依赖工具尚未就绪不是异常状况（工具还在下载/启动中，稍后调度器会自动恢复），
// 几百个直播间同时报错只会淹没日志，因此降级为 debug。
func (l *listener) logGetInfoError(err error) {
	entry := l.Live.GetLogger().
		WithError(err).
		WithField("url", l.Live.GetRawUrl())
	if errors.Is(err, live.ErrPlatformToolsNotReady) {
		entry.Debug("skip loading room info")
		return
	}
	entry.Error("failed to load room info")
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
				l.logGetInfoError(err)
				continue
			}
			l.processInfo(info)
		}
	}
}

// processInfo 处理获取到的直播间信息，检测状态变化并触发事件
func (l *listener) processInfo(info *live.Info) {
	// 初始化完成回调可能在 GetInfo 返回前同步替换并关闭当前 listener。
	// 已关闭的旧 listener 不得再发布 LiveStart/LiveEnd，否则会与新 listener 的事件竞态。
	if l.isStopped() {
		return
	}

	// 尝试从缓存中获取主播姓名，以防API调用失败
	hostName := info.HostName
	if hostName == "" {
		if wrappedLive, ok := l.Live.(*live.WrappedLive); ok {
			if cachedInfo, found := wrappedLive.GetCachedInfo(); found {
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
		l.ed.DispatchEvent(events.NewEventWithSource(evtTyp, l.Live, l))
		applog.GetLogger().WithFields(fields).Info(logInfo)
	}
}
