//go:generate go run go.uber.org/mock/mockgen -package listeners -destination mock_test.go github.com/bililive-go/bililive-go/src/listeners Listener,Manager
package listeners

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/bililive-go/bililive-go/src/configs"
	"github.com/bililive-go/bililive-go/src/consts"
	"github.com/bililive-go/bililive-go/src/instance"
	"github.com/bililive-go/bililive-go/src/live"
	applog "github.com/bililive-go/bililive-go/src/log"
	"github.com/bililive-go/bililive-go/src/notify"
	"github.com/bililive-go/bililive-go/src/pkg/diagnostics"
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
	roomScopeID, lifecycleScopeKey, listenerID, generation := listenerTraceIdentity(live)
	traceFields := listenerBaseFields(roomScopeID, listenerID, generation)
	// 监听器事件携带独立于 diagnostics 开关的房间 generation 与生产序号。
	// Recorder Manager 会据此拒绝 Stop 之后才到达的旧 LiveStart，以及旧
	// generation 对新 recorder 的误删。
	ctx = events.WithCausalSource(ctx, lifecycleScopeKey, listenerID, generation)
	ctx = diagnostics.WithFields(ctx, traceFields)
	// 创建一个可取消的 context，用于控制 run 循环中的等待
	runCtx, cancel := context.WithCancel(ctx)
	return &listener{
		Live:               live,
		status:             status{},
		stop:               make(chan struct{}),
		done:               make(chan struct{}),
		startReady:         make(chan struct{}),
		ed:                 inst.EventDispatcher.(events.Dispatcher),
		state:              begin,
		runCtx:             runCtx,
		runCancel:          cancel,
		traceCtx:           runCtx,
		traceFields:        traceFields,
		roomScopeID:        roomScopeID,
		listenerInstanceID: listenerID,
		generation:         generation,
	}
}

type listener struct {
	Live   live.Live
	status status
	ed     events.Dispatcher

	state      uint32
	stop       chan struct{}
	done       chan struct{}
	startReady chan struct{}
	runCtx     context.Context    // 用于控制 run 循环中的等待
	runCancel  context.CancelFunc // 取消 runCtx

	stopOnce           sync.Once
	doneOnce           sync.Once
	startReadyOnce     sync.Once
	startEventSent     atomic.Bool
	traceMu            sync.RWMutex
	traceCtx           context.Context
	traceFields        diagnostics.Fields
	traceTaskEnd       func(diagnostics.Fields)
	traceTaskEndOnce   sync.Once
	roomScopeID        string
	listenerInstanceID string
	generation         uint64
	pollSequence       atomic.Uint64
}

func (l *listener) Start() error {
	return l.start(nil)
}

// StartWithInfo 使用已经获取到的直播间信息启动监听器，避免初始化交接后立即重复请求平台。
func (l *listener) StartWithInfo(info *live.Info) error {
	return l.start(info)
}

func (l *listener) start(initialInfo *live.Info) error {
	traceCtx := l.currentTraceContext()
	currentState := atomic.LoadUint32(&l.state)
	diagnostics.Record(traceCtx, "listener.start.requested", mergeDiagnosticFields(l.traceFields, diagnostics.Fields{
		"state": listenerStateName(currentState),
	}))
	if !atomic.CompareAndSwapUint32(&l.state, begin, pending) {
		diagnostics.Record(traceCtx, "listener.start.ignored", mergeDiagnosticFields(l.traceFields, diagnostics.Fields{
			"status": "ignored",
			"state":  listenerStateName(atomic.LoadUint32(&l.state)),
		}))
		return nil
	}
	diagnostics.Record(traceCtx, "listener.start.accepted", mergeDiagnosticFields(l.traceFields, diagnostics.Fields{
		"status": "accepted",
	}))
	diagnostics.Record(traceCtx, "listener.state.transition", mergeDiagnosticFields(l.traceFields, diagnostics.Fields{
		"kind": "state.transition",
		"from": "begin",
		"to":   "pending",
	}))

	taskCtx, endTask := diagnostics.NewTask(l.runCtx, "listener", l.traceFields)
	l.traceMu.Lock()
	l.traceCtx = taskCtx
	l.traceTaskEnd = endTask
	l.traceMu.Unlock()

	runLaunched := false
	defer func() {
		if runLaunched {
			if atomic.CompareAndSwapUint32(&l.state, pending, running) {
				diagnostics.Record(taskCtx, "listener.state.transition", mergeDiagnosticFields(l.traceFields, diagnostics.Fields{
					"kind": "state.transition",
					"from": "pending",
					"to":   "running",
				}))
			}
			return
		}

		// Start 在 run goroutine 真正交给运行时之前异常退出时，也必须释放
		// Close 对 pending 阶段的等待，不能永久悬挂。
		l.signalStartReady()
		if atomic.CompareAndSwapUint32(&l.state, pending, stopped) {
			diagnostics.Record(taskCtx, "listener.state.transition", mergeDiagnosticFields(l.traceFields, diagnostics.Fields{
				"kind":   "state.transition",
				"from":   "pending",
				"to":     "stopped",
				"reason": "start_aborted",
			}))
		}
		l.requestStop()
		l.finishTraceTask("aborted")
		l.signalDone()
	}()

	monitorFields := mergeDiagnosticFields(l.traceFields, diagnostics.Fields{
		"reason": "listener_start",
	})
	if intervalMS, ok := configuredPollIntervalMilliseconds(l.Live); ok {
		monitorFields["configured_interval_ms"] = intervalMS
	}
	diagnostics.Record(taskCtx, "monitor.started", monitorFields)

	listenStartEvent := events.NewEventWithContext(taskCtx, ListenStart, l.Live)
	diagnostics.Record(taskCtx, "listener.event.produced", mergeDiagnosticFields(l.traceFields, diagnostics.Fields{
		"event_id":   listenStartEvent.ID,
		"event_type": string(ListenStart),
	}))
	l.ed.DispatchEvent(listenStartEvent)
	l.startEventSent.Store(true)
	l.signalStartReady()

	// 首次信息获取必须与定时轮询处于同一个可等待的后台生命周期中。
	// manager.AddListener 会持有全局锁；若在这里同步访问平台，批量启动几百个
	// 直播间时会串行占锁数分钟。StartWithInfo 则直接消费初始化阶段已经取得的
	// info，既不重复请求平台，也保留同一条 listener task/因果链。
	diagnostics.Record(taskCtx, "listener.run.spawned", l.traceFields)
	bilisentry.GoWithContext(taskCtx, func(context.Context) { l.run(initialInfo) })
	runLaunched = true
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

func (l *listener) Close() {
	l.close(false)
}

// CloseSync 同步完成 ListenStop 的所有处理器，仅用于初始化 listener 的交接路径。
// 普通 Close 同样会等待 listener 自身 goroutine 退出；CloseSync 额外保证
// ListenStop 的所有处理器在返回前已经按注册顺序处理完毕。
func (l *listener) CloseSync() {
	l.close(true)
}

func (l *listener) close(syncEvent bool) {
	traceCtx := l.currentTraceContext()
	currentState := atomic.LoadUint32(&l.state)
	diagnostics.Record(traceCtx, "listener.close.requested", mergeDiagnosticFields(l.traceFields, diagnostics.Fields{
		"state": listenerStateName(currentState),
	}))

	for {
		state := atomic.LoadUint32(&l.state)
		switch state {
		case stopped:
			diagnostics.Record(l.currentTraceContext(), "listener.close.ignored", mergeDiagnosticFields(l.traceFields, diagnostics.Fields{
				"status": "ignored",
				"state":  listenerStateName(state),
			}))
			<-l.done
			return
		case begin:
			if !atomic.CompareAndSwapUint32(&l.state, begin, stopped) {
				continue
			}
			l.recordCloseAccepted("begin")
			l.requestStop()
			l.signalStartReady()
			l.signalDone()
			l.recordCloseComplete()
			return
		case pending, running:
			if !atomic.CompareAndSwapUint32(&l.state, state, stopped) {
				continue
			}
			l.recordCloseAccepted(listenerStateName(state))
			// 先取消首次 refresh/定时轮询，再等待 Start 完成 ListenStart 的
			// 派发。这样 pending 阶段关闭既不会死锁，也不会产生
			// ListenStop 先于 ListenStart 的反序事件。
			l.requestStop()
			<-l.startReady
			if l.startEventSent.Load() {
				l.dispatchStopEvent(syncEvent)
			}
			// Close 是 listener 生命周期的同步屏障。只有 run 的最终 span 和
			// task 都已经结束后才允许 Manager.Close 返回。
			<-l.done
			l.recordCloseComplete()
			return
		default:
			return
		}
	}
}

func (l *listener) recordCloseAccepted(from string) {
	diagnostics.Record(l.currentTraceContext(), "listener.close.accepted", mergeDiagnosticFields(l.traceFields, diagnostics.Fields{
		"status": "accepted",
	}))
	diagnostics.Record(l.currentTraceContext(), "listener.state.transition", mergeDiagnosticFields(l.traceFields, diagnostics.Fields{
		"kind": "state.transition",
		"from": from,
		"to":   "stopped",
	}))
}

func (l *listener) dispatchStopEvent(syncEvent bool) {
	traceCtx := l.currentTraceContext()
	listenStopEvent := events.NewEventWithContext(traceCtx, ListenStop, l.Live)
	diagnostics.Record(traceCtx, "listener.event.produced", mergeDiagnosticFields(l.traceFields, diagnostics.Fields{
		"event_id":   listenStopEvent.ID,
		"event_type": string(ListenStop),
	}))
	if syncEvent {
		l.ed.DispatchEventSync(listenStopEvent)
		return
	}
	l.ed.DispatchEvent(listenStopEvent)
}

func (l *listener) requestStop() {
	diagnostics.Record(l.currentTraceContext(), "listener.cancel.requested", mergeDiagnosticFields(l.traceFields, diagnostics.Fields{
		"cancel_reason": "listener_close",
	}))
	l.runCancel()
	l.stopOnce.Do(func() {
		close(l.stop)
	})
}

func (l *listener) recordCloseComplete() {
	diagnostics.Record(l.currentTraceContext(), "listener.close.complete", mergeDiagnosticFields(l.traceFields, diagnostics.Fields{
		"status": "stopped",
	}))
}

func (l *listener) currentTraceContext() context.Context {
	l.traceMu.RLock()
	defer l.traceMu.RUnlock()
	return l.traceCtx
}

func (l *listener) signalStartReady() {
	l.startReadyOnce.Do(func() {
		close(l.startReady)
	})
}

func (l *listener) signalDone() {
	l.doneOnce.Do(func() {
		close(l.done)
	})
}

func (l *listener) finishTraceTask(status string) {
	l.traceTaskEndOnce.Do(func() {
		l.traceMu.RLock()
		endTask := l.traceTaskEnd
		l.traceMu.RUnlock()
		if endTask != nil {
			endTask(diagnostics.Fields{"status": status})
		}
	})
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
	l.refreshWithContext(l.currentTraceContext(), "initial")
}

func (l *listener) refreshWithContext(ctx context.Context, source string) {
	_ = l.pollWithContext(ctx, source, func(fetchCtx context.Context) (*live.Info, error) {
		if contextual, ok := l.Live.(interface {
			GetInfoWithContext(context.Context) (*live.Info, error)
		}); ok {
			return contextual.GetInfoWithContext(fetchCtx)
		}
		return l.Live.GetInfo()
	})
}

type infoFetcher func(context.Context) (*live.Info, error)

// pollWithContext 为一次完整的直播状态检测建立里程碑。listener.poll.end
// 必须先于由该检测产生的事件派发写出，Viewer 才能可靠拆分“检测耗时”和
// “事件交接耗时”。
func (l *listener) pollWithContext(ctx context.Context, source string, fetch infoFetcher) error {
	pollNo := l.pollSequence.Add(1)
	pollFields := mergeDiagnosticFields(l.traceFields, diagnostics.Fields{
		"poll_no": pollNo,
		"reason":  pollReason(source),
		"source":  source,
	})
	if source == "scheduled" {
		diagnostics.Record(ctx, "listener.poll.scheduled", pollFields)
	}

	pollCtx, endPoll := diagnostics.StartSpan(ctx, "listener.poll", pollFields)
	refreshCtx, endRefresh := diagnostics.StartSpan(pollCtx, "live.refresh", pollFields)
	info, err := fetch(refreshCtx)
	if err != nil {
		status := "error"
		if refreshCtx.Err() != nil {
			status = "cancelled"
		}
		resultFields := diagnostics.Fields{
			"status":     status,
			"error_type": fmt.Sprintf("%T", err),
		}
		endRefresh(resultFields)
		endPoll(resultFields)
		l.logGetInfoError(err)
		return err
	}

	resultFields := diagnostics.Fields{
		"status":       "ok",
		"live":         info.Status,
		"initializing": info.Initializing,
	}
	endRefresh(resultFields)
	endPoll(resultFields)

	// 不再沿用已经结束的 poll span，只保留 task/flow 与 poll_no，避免生成
	// “父 span 已结束后才出现子事件”的误导时间线。
	processCtx := diagnostics.WithFields(ctx, diagnostics.Fields{
		"poll_no":     pollNo,
		"poll_source": source,
	})
	l.processInfoWithContext(processCtx, info)
	return nil
}

func pollReason(source string) string {
	switch source {
	case "initial":
		return "initial_refresh"
	case "scheduled":
		return "scheduled_interval"
	default:
		return source
	}
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

func (l *listener) run(initialInfo *live.Info) {
	runCtx, endRun := diagnostics.StartSpan(l.currentTraceContext(), "listener.run", l.traceFields)
	exitStatus := "unknown"
	// 后注册的诊断收尾 defer 先执行，done 因而是“所有 listener 轨迹均已
	// 写完”的可靠屏障，而不只是轮询循环已经 return。
	defer l.signalDone()
	defer func() {
		endRun(diagnostics.Fields{"status": exitStatus})
		l.finishTraceTask(exitStatus)
	}()

	if !l.isStopped() {
		if initialInfo != nil {
			pollNo := l.pollSequence.Add(1)
			seedFields := mergeDiagnosticFields(l.traceFields, diagnostics.Fields{
				"poll_no": pollNo,
				"reason":  "initial_seed",
				"source":  "initial_seeded",
				"status":  "ok",
			})
			diagnostics.Record(runCtx, "listener.poll.seeded", seedFields)
			processCtx := diagnostics.WithFields(runCtx, diagnostics.Fields{
				"poll_no":     pollNo,
				"poll_source": "initial_seeded",
			})
			l.processInfoWithContext(processCtx, initialInfo)
		} else {
			l.refreshWithContext(runCtx, "initial")
		}
	}

	// 使用 GetInfoWithInterval 来处理等待和请求
	// 它会自动获取配置的间隔时间，并在尊重平台速率限制的前提下等待后发送请求
	for {
		select {
		case <-l.stop:
			exitStatus = "stopped"
			return
		default:
			// 使用 GetInfoWithInterval，它会等待配置的间隔时间后再发送请求
			err := l.pollWithContext(runCtx, "scheduled", l.Live.GetInfoWithInterval)
			if err != nil {
				// 如果是 context 取消导致的错误，说明 listener 正在关闭
				if runCtx.Err() != nil {
					exitStatus = "cancelled"
					return
				}
				continue
			}
		}
	}
}

// processInfo 处理获取到的直播间信息，检测状态变化并触发事件
func (l *listener) processInfo(info *live.Info) {
	l.processInfoWithContext(l.currentTraceContext(), info)
}

func (l *listener) processInfoWithContext(ctx context.Context, info *live.Info) {
	// 初始化完成回调可能在 GetInfo 返回前同步替换并关闭当前 listener。
	// 已关闭的旧 listener 不得再发布 LiveStart/LiveEnd，否则会与新 listener 的事件竞态。
	if l.isStopped() {
		return
	}

	// 尝试从缓存中获取主播姓名，以防API调用失败
	hostName := info.HostName
	if hostName == "" {
		if wrappedLive, ok := l.Live.(*live.WrappedLive); ok {
			_, endFallback := diagnostics.StartSpan(ctx, "live.refresh", mergeDiagnosticFields(l.traceFields, diagnostics.Fields{
				"source": "host_name_fallback",
			}))
			cachedInfo, found := wrappedLive.GetCachedInfo()
			if found && cachedInfo != nil {
				hostName = cachedInfo.HostName
			}
			status := "miss"
			if found {
				status = "hit"
			}
			endFallback(diagnostics.Fields{"status": status})
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
		diagnostics.Record(ctx, "live.state.transition", mergeDiagnosticFields(l.traceFields, diagnostics.Fields{
			"kind": "state.transition",
			"from": "offline",
			"to":   "live",
		}))
		l.Live.SetLastStartTime(time.Now())
		evtTyp = LiveStart
		logInfo = "Live Start"
		// 发送开播提醒和录像通知
		_, endNotify := diagnostics.StartSpan(ctx, "listener.notification", mergeDiagnosticFields(l.traceFields, diagnostics.Fields{
			"transition": "live_start",
		}))
		l.sendLiveNotification(hostName, consts.LiveStatusStart)
		endNotify(diagnostics.Fields{"status": "completed"})

	case statusToFalseEvt:
		diagnostics.Record(ctx, "live.state.transition", mergeDiagnosticFields(l.traceFields, diagnostics.Fields{
			"kind": "state.transition",
			"from": "live",
			"to":   "offline",
		}))
		evtTyp = LiveEnd
		logInfo = "Live end"
		// 发送结束直播提醒和录像通知
		_, endNotify := diagnostics.StartSpan(ctx, "listener.notification", mergeDiagnosticFields(l.traceFields, diagnostics.Fields{
			"transition": "live_end",
		}))
		l.sendLiveNotification(hostName, consts.LiveStatusStop)
		endNotify(diagnostics.Fields{"status": "completed"})
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
		diagnostics.Record(ctx, "live.room_name.changed", l.traceFields)
	}
	if isStatusChanged {
		producedEvent := events.NewEventWithContext(ctx, evtTyp, l.Live)
		diagnostics.Record(ctx, "listener.event.produced", mergeDiagnosticFields(l.traceFields, diagnostics.Fields{
			"event_id":   producedEvent.ID,
			"event_type": string(evtTyp),
		}))
		l.ed.DispatchEvent(producedEvent)
		applog.GetLogger().WithFields(fields).Info(logInfo)
	}
}

func (l *listener) diagnosticFields() diagnostics.Fields {
	return diagnostics.Fields{
		"room_scope_id":        l.roomScopeID,
		"listener_instance_id": l.listenerInstanceID,
		"generation":           l.generation,
	}
}
