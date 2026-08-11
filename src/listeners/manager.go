package listeners

import (
	"context"
	"sync"
	"time"

	"github.com/bililive-go/bililive-go/src/configs"
	"github.com/bililive-go/bililive-go/src/instance"
	"github.com/bililive-go/bililive-go/src/interfaces"
	"github.com/bililive-go/bililive-go/src/live"
	"github.com/bililive-go/bililive-go/src/pkg/diagnostics"
	"github.com/bililive-go/bililive-go/src/pkg/events"
	"github.com/bililive-go/bililive-go/src/types"
)

// for test
var newListener = NewListener

func NewManager(ctx context.Context) Manager {
	lm := &manager{
		savers: make(map[types.LiveID]Listener),
		inst:   instance.GetInstance(ctx),
	}
	if lm.inst != nil {
		lm.inst.ListenerManager = lm
	}
	return lm
}

type Manager interface {
	interfaces.Module
	AddListener(ctx context.Context, live live.Live) error
	RemoveListener(ctx context.Context, liveId types.LiveID) error
	GetListener(ctx context.Context, liveId types.LiveID) (Listener, error)
	HasListener(ctx context.Context, liveId types.LiveID) bool
}

type manager struct {
	lock   sync.RWMutex
	savers map[types.LiveID]Listener

	inst *instance.Instance

	lifecycleMu sync.Mutex
	operations  sync.WaitGroup
	closing     bool
	started     bool
	accounted   bool
	closeOnce   sync.Once
}

// beginOperation 在线性化的关闭闸门内登记一次会修改 manager 或产生业务
// 轨迹的操作。Close 先关闭闸门再 Wait，因此不存在 WaitGroup 的 Add/Wait 竞态。
func (m *manager) beginOperation() bool {
	m.lifecycleMu.Lock()
	defer m.lifecycleMu.Unlock()
	if m.closing {
		return false
	}
	m.operations.Add(1)
	return true
}

func (m *manager) endOperation() {
	m.operations.Done()
}

func (m *manager) registryListener(ctx context.Context, ed events.Dispatcher) {
	ed.AddEventListener(RoomInitializingFinished, events.NewNamedEventListener("listeners.manager.room_initializing_finished", func(event *events.Event) {
		// Dispatcher 是异步的。关闭开始前已经进入的 handler 会被 Close 等待；
		// 关闭后才到达的初始化完成事件必须完全静默，不能重新创建 listener。
		if !m.beginOperation() {
			return
		}
		defer m.endOperation()

		param := event.Object.(live.InitializingFinishedParam)
		initializingLive := param.InitializingLive
		originalLive := param.Live
		info := param.Info
		if info.CustomLiveId != "" {
			originalLive.SetLiveIdByString(info.CustomLiveId)
		}
		inst := instance.GetInstance(ctx)
		logger := originalLive.GetLogger()

		// 将原始 Live 包装为 WrappedLive（使用全局缓存）
		// 传入 ctx 以便调度器可以被统一取消
		oldLiveId := initializingLive.GetLiveId()
		wrappedLive := live.NewWrappedLive(ctx, originalLive, inst.Cache)

		// 原子地替换 Lives map 中的条目（删除旧 InitializingLive，添加新 wrappedLive）
		inst.Lives.ReplaceKey(oldLiveId, wrappedLive.GetLiveId(), wrappedLive)

		// 将已有的 info 注入新的 WrappedLive，避免 listener 交接后立即重复请求平台，
		// 同时让新调度器从本次成功请求开始计算下一轮间隔。
		info.Live = wrappedLive
		if err := wrappedLive.(*live.WrappedLive).SeedInfo(info); err != nil {
			logger.WithError(err).Warn("failed to cache info for new live")
		}

		cfg := configs.GetCurrentConfig()
		room, err := cfg.GetLiveRoomByUrl(wrappedLive.GetRawUrl())
		if err != nil {
			logger.WithFields(map[string]any{
				"room": wrappedLive.GetRawUrl(),
			}).Error(err)
			panic(err)
		}
		configs.SetLiveRoomId(wrappedLive.GetRawUrl(), wrappedLive.GetLiveId())
		if room.IsListening {
			if err := m.replaceListener(ctx, initializingLive, wrappedLive, info); err != nil {
				logger.WithFields(map[string]any{
					"url": wrappedLive.GetRawUrl(),
				}).Error(err)
			}
		}
	}))
}

func (m *manager) Start(ctx context.Context) error {
	m.lifecycleMu.Lock()
	defer m.lifecycleMu.Unlock()
	if m.closing {
		return ErrManagerClosed
	}
	if m.started {
		return nil
	}
	m.started = true
	inst := m.inst
	if inst == nil {
		inst = instance.GetInstance(ctx)
		m.inst = inst
	}
	if inst != nil {
		// Manager 在无 RPC、房间尚未初始化时也同样需要关闭。原来的条件式
		// Add 配合无条件 Done 会产生负 WaitGroup；统一登记才与 Close 对称。
		inst.WaitGroup.Add(1)
		m.accounted = true
	}

	if inst == nil || inst.EventDispatcher == nil {
		return nil
	}
	m.registryListener(ctx, inst.EventDispatcher.(events.Dispatcher))
	return nil
}

func (m *manager) Close(ctx context.Context) {
	m.closeOnce.Do(func() {
		m.lifecycleMu.Lock()
		m.closing = true
		accounted := m.accounted
		inst := m.inst
		m.lifecycleMu.Unlock()

		// 等待关闭前已经进入的 Add/Remove/初始化完成 handler。关闭闸门后
		// 新的异步事件无法登记，因此 Wait 返回后不会再出现新 listener。
		m.operations.Wait()

		m.lock.Lock()
		listenersToClose := make([]Listener, 0, len(m.savers))
		for id, listener := range m.savers {
			listenersToClose = append(listenersToClose, listener)
			delete(m.savers, id)
		}
		m.lock.Unlock()

		// 不持 manager 锁等待网络轮询退出，避免 Close 与回调形成锁循环。
		for _, listener := range listenersToClose {
			listener.Close()
		}
		if accounted && inst != nil {
			inst.WaitGroup.Done()
		}
	})
}

func (m *manager) AddListener(ctx context.Context, live live.Live) error {
	if !m.beginOperation() {
		return ErrManagerClosed
	}
	defer m.endOperation()

	traceCtx, baseFields := managerTraceContext(ctx, "add", live)
	traceCtx, endOperation := diagnostics.StartSpan(traceCtx, "listener.manager.add", baseFields)
	requestedAt := time.Now()
	m.lock.Lock()
	acquiredAt := time.Now()
	var result string
	endFields := diagnostics.Fields{}
	defer func() {
		if result == "" {
			// 所有正常返回路径都会填写 result；空值表示操作因 panic 中断。
			result = "aborted"
		}
		holdDuration := time.Since(acquiredAt)
		m.lock.Unlock()
		endOperation(mergeDiagnosticFields(endFields, diagnostics.Fields{
			"status":       managerOperationStatus(result),
			"result":       result,
			"lock_wait_ms": durationMilliseconds(acquiredAt.Sub(requestedAt)),
			"lock_hold_ms": durationMilliseconds(holdDuration),
		}))
	}()

	if _, ok := m.savers[live.GetLiveId()]; ok {
		result = "duplicate"
		return ErrListenerExist
	}
	listener := newListener(traceCtx, live)
	if traced, ok := listener.(interface{ diagnosticFields() diagnostics.Fields }); ok {
		endFields = traced.diagnosticFields()
	}
	m.savers[live.GetLiveId()] = listener
	err := listener.Start()
	if err != nil {
		delete(m.savers, live.GetLiveId())
		listener.Close()
		result = "start_error"
		return err
	}
	result = "started"
	return nil
}

func (m *manager) RemoveListener(ctx context.Context, liveId types.LiveID) error {
	if !m.beginOperation() {
		return ErrManagerClosed
	}
	defer m.endOperation()

	traceCtx := ctx
	if traceCtx == nil {
		traceCtx = context.Background()
	}
	baseFields := diagnostics.Fields{
		"component": "listener_manager",
		"lane":      "listener",
		"operation": "remove",
	}
	traceCtx = diagnostics.WithFields(traceCtx, baseFields)
	_, endOperation := diagnostics.StartSpan(traceCtx, "listener.manager.remove", baseFields)
	requestedAt := time.Now()
	m.lock.Lock()
	acquiredAt := time.Now()
	var result string
	endFields := diagnostics.Fields{}
	defer func() {
		if result == "" {
			result = "aborted"
		}
		holdDuration := time.Since(acquiredAt)
		m.lock.Unlock()
		endOperation(mergeDiagnosticFields(endFields, diagnostics.Fields{
			"status":       managerOperationStatus(result),
			"result":       result,
			"lock_wait_ms": durationMilliseconds(acquiredAt.Sub(requestedAt)),
			"lock_hold_ms": durationMilliseconds(holdDuration),
		}))
	}()
	listener, ok := m.savers[liveId]
	if !ok {
		result = "not_found"
		return ErrListenerNotExist
	}
	if traced, ok := listener.(interface{ diagnosticFields() diagnostics.Fields }); ok {
		endFields = traced.diagnosticFields()
	}
	listener.Close()
	delete(m.savers, liveId)
	result = "removed"
	return nil
}

func (m *manager) replaceListener(ctx context.Context, oldLive live.Live, newLive live.Live, info *live.Info) error {
	traceCtx, baseFields := managerTraceContext(ctx, "replace", newLive)
	traceCtx, endOperation := diagnostics.StartSpan(traceCtx, "listener.manager.replace", baseFields)
	requestedAt := time.Now()
	m.lock.Lock()
	acquiredAt := time.Now()
	var result string
	endFields := diagnostics.Fields{}
	defer func() {
		if result == "" {
			result = "aborted"
		}
		holdDuration := time.Since(acquiredAt)
		m.lock.Unlock()
		endOperation(mergeDiagnosticFields(endFields, diagnostics.Fields{
			"status":       managerOperationStatus(result),
			"result":       result,
			"lock_wait_ms": durationMilliseconds(acquiredAt.Sub(requestedAt)),
			"lock_hold_ms": durationMilliseconds(holdDuration),
		}))
	}()
	oldLiveId := oldLive.GetLiveId()
	oldListener, ok := m.savers[oldLiveId]
	if !ok {
		result = "old_not_found"
		return ErrListenerNotExist
	}
	// 必须等旧 ListenStop 的录制器清理完成后，才能让新 listener 发布 LiveStart。
	oldListener.CloseSync()
	replacement := newListener(traceCtx, newLive)
	if traced, ok := replacement.(interface{ diagnosticFields() diagnostics.Fields }); ok {
		endFields = traced.diagnosticFields()
	}
	if oldLiveId == newLive.GetLiveId() {
		m.savers[oldLiveId] = replacement
	} else {
		delete(m.savers, oldLiveId)
		m.savers[newLive.GetLiveId()] = replacement
	}
	err := replacement.StartWithInfo(info)
	if err != nil {
		result = "start_error"
		return err
	}
	result = "replaced"
	return nil
}

func (m *manager) GetListener(ctx context.Context, liveId types.LiveID) (Listener, error) {
	m.lock.RLock()
	defer m.lock.RUnlock()
	listener, ok := m.savers[liveId]
	if !ok {
		return nil, ErrListenerNotExist
	}
	return listener, nil
}

func (m *manager) HasListener(ctx context.Context, liveId types.LiveID) bool {
	m.lock.RLock()
	defer m.lock.RUnlock()
	_, ok := m.savers[liveId]
	return ok
}
