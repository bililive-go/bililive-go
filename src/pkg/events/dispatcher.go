//go:generate go run go.uber.org/mock/mockgen -package mock -destination mock/mock.go github.com/bililive-go/bililive-go/src/pkg/events Dispatcher
package events

import (
	"container/list"
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/bililive-go/bililive-go/src/instance"
	"github.com/bililive-go/bililive-go/src/interfaces"
	"github.com/bililive-go/bililive-go/src/pkg/diagnostics"
	bilisentry "github.com/bililive-go/bililive-go/src/pkg/sentry"
)

func NewDispatcher(ctx context.Context) Dispatcher {
	ed := &dispatcher{
		saver: make(map[EventType]*list.List),
		ctx:   ctx,
	}
	ed.drainCond = sync.NewCond(&ed.drainMu)
	inst := instance.GetInstance(ctx)
	if inst != nil {
		inst.EventDispatcher = ed
	}
	return ed
}

type Dispatcher interface {
	interfaces.Module
	AddEventListener(eventType EventType, listener *EventListener)
	RemoveEventListener(eventType EventType, listener *EventListener)
	RemoveAllEventListener(eventType EventType)
	DispatchEvent(event *Event)
	// DispatchEventSync 在当前 goroutine 中按注册顺序执行事件处理器。
	// 仅应用于调用方必须等待状态交接完成后才能继续的场景；普通事件仍应使用 DispatchEvent。
	DispatchEventSync(event *Event)
}

type dispatcher struct {
	sync.RWMutex
	saver map[EventType]*list.List // map<EventType, List<*EventListener>>
	ctx   context.Context

	// drainMu 把“是否还接受派发”和 inFlight 计数放在同一个线性化点。
	// 不能直接用 WaitGroup：Close 的 Wait 与迟到/嵌套派发的 Add 并发会产生
	// 未定义语义，甚至让 Close 在新 handler 登记前提前返回。
	drainMu   sync.Mutex
	drainCond *sync.Cond
	closing   bool
	inFlight  uint64
}

type dispatchPermitContextKey struct{}

// dispatchPermit 只会被放入交给 handler 的 Event context。Close 开始后，
// 外部生产者会被拒绝，但已接收 handler 使用该 context 派生的同步或异步事件
// 仍可登记，保证一条已经进入 Dispatcher 的因果链能够完整 drain。
type dispatchPermit struct {
	owner  *dispatcher
	active atomic.Bool
}

type preparedDispatch struct {
	handlerEvent *Event
	listeners    []*EventListener
	taskCtx      context.Context
	endTask      func(diagnostics.Fields)
	baseFields   diagnostics.Fields
	enqueuedAt   time.Time
	permit       *dispatchPermit
}

func (e *dispatcher) Start(ctx context.Context) error {
	return nil
}

func (e *dispatcher) Close(ctx context.Context) {
	if e == nil {
		return
	}
	if ctx == nil {
		ctx = e.ctx
	}
	_, endClose := diagnostics.StartSpan(ctx, "event.dispatcher.close", diagnostics.Fields{
		"component": "events",
		"lane":      "events",
	})

	e.drainMu.Lock()
	e.closing = true
	inFlightAtClose := e.inFlight
	for e.inFlight > 0 {
		e.drainCond.Wait()
	}
	e.drainMu.Unlock()

	endClose(diagnostics.Fields{
		"status":             "ok",
		"in_flight_at_close": inFlightAtClose,
	})
}

func (e *dispatcher) AddEventListener(eventType EventType, listener *EventListener) {
	e.Lock()
	defer e.Unlock()
	listeners, ok := e.saver[eventType]
	if !ok || listener == nil {
		listeners = list.New()
		e.saver[eventType] = listeners
	}
	listeners.PushBack(listener)
}

func (e *dispatcher) RemoveEventListener(eventType EventType, listener *EventListener) {
	e.Lock()
	defer e.Unlock()
	listeners, ok := e.saver[eventType]
	if !ok || listeners == nil {
		return
	}
	for e := listeners.Front(); e != nil; e = e.Next() {
		if e.Value == listener {
			listeners.Remove(e)
		}
	}
	if listeners.Len() == 0 {
		delete(e.saver, eventType)
	}
}

func (e *dispatcher) RemoveAllEventListener(eventType EventType) {
	e.Lock()
	defer e.Unlock()
	e.saver = make(map[EventType]*list.List)
}

func (e *dispatcher) beginDispatch(event *Event) (*dispatchPermit, bool) {
	if event == nil {
		return nil, false
	}
	parentCtx := event.Context(e.ctx)
	parentPermit, _ := parentCtx.Value(dispatchPermitContextKey{}).(*dispatchPermit)

	e.drainMu.Lock()
	defer e.drainMu.Unlock()
	nested := parentPermit != nil &&
		parentPermit.owner == e &&
		parentPermit.active.Load()
	if e.closing && !nested {
		return nil, false
	}
	permit := &dispatchPermit{owner: e}
	permit.active.Store(true)
	e.inFlight++
	return permit, true
}

func (e *dispatcher) finishDispatch(permit *dispatchPermit) {
	e.drainMu.Lock()
	if permit != nil {
		permit.active.Store(false)
	}
	if e.inFlight > 0 {
		e.inFlight--
	}
	if e.closing && e.inFlight == 0 {
		e.drainCond.Broadcast()
	}
	e.drainMu.Unlock()
}

func (e *dispatcher) prepareDispatch(event *Event, mode string) *preparedDispatch {
	if event == nil {
		return nil
	}
	parentCtx := event.Context(e.ctx)
	dispatchID := diagnostics.NewID("dispatch")
	baseFields := diagnostics.Fields{
		"component":     "events",
		"lane":          "events",
		"dispatch_id":   dispatchID,
		"dispatch_mode": mode,
		"event_id":      event.ID,
		"event_type":    string(event.Type),
	}
	if causality, ok := event.Causality(); ok {
		// ScopeKey 是只供进程内状态机使用的房间键，禁止写入调查包。
		baseFields["listener_instance_id"] = causality.ProducerID
		baseFields["generation"] = causality.Generation
		baseFields["event_sequence"] = causality.Sequence
	}
	parentCtx = diagnostics.WithFields(parentCtx, baseFields)
	enqueuedAt := time.Now()

	permit, accepted := e.beginDispatch(event)
	if !accepted {
		diagnostics.Record(parentCtx, "event.dispatch", mergeFields(baseFields, diagnostics.Fields{
			"status":        "rejected_closing",
			"handler_count": 0,
		}))
		return nil
	}

	e.RLock()
	listeners, ok := e.saver[event.Type]
	if !ok || listeners == nil {
		e.RUnlock()
		diagnostics.Record(parentCtx, "event.dispatch", mergeFields(baseFields, diagnostics.Fields{
			"status":        "no_handlers",
			"handler_count": 0,
		}))
		e.finishDispatch(permit)
		return nil
	}
	hs := make([]*EventListener, 0, listeners.Len())
	for item := listeners.Front(); item != nil; item = item.Next() {
		hs = append(hs, item.Value.(*EventListener))
	}
	e.RUnlock()
	diagnostics.Record(parentCtx, "event.dispatch", mergeFields(baseFields, diagnostics.Fields{
		"status":        "enqueued",
		"handler_count": len(hs),
	}))

	taskCtx, endTask := diagnostics.NewTask(parentCtx, "event.dispatch", baseFields)
	handlerEvent := *event
	handlerEvent.traceCtx = context.WithValue(taskCtx, dispatchPermitContextKey{}, permit)
	return &preparedDispatch{
		handlerEvent: &handlerEvent,
		listeners:    hs,
		taskCtx:      taskCtx,
		endTask:      endTask,
		baseFields:   baseFields,
		enqueuedAt:   enqueuedAt,
		permit:       permit,
	}
}

func (e *dispatcher) runDispatch(dispatch *preparedDispatch) {
	queueDelay := time.Since(dispatch.enqueuedAt)
	runCtx, endRun := diagnostics.StartSpan(
		dispatch.taskCtx,
		"event.dispatch.run",
		mergeFields(dispatch.baseFields, diagnostics.Fields{
			"queue_delay_ms": durationMilliseconds(queueDelay),
			"handler_count":  len(dispatch.listeners),
		}),
	)
	completed := 0
	defer e.finishDispatch(dispatch.permit)
	defer func() {
		if recovered := recover(); recovered != nil {
			remaining := len(dispatch.listeners) - completed - 1
			if remaining < 0 {
				remaining = 0
			}
			panicFields := mergeFields(dispatch.baseFields, diagnostics.Fields{
				"status":               "panic",
				"completed_count":      completed,
				"abandoned_count":      remaining,
				"failed_handler_index": completed,
				"queue_delay_ms":       durationMilliseconds(queueDelay),
				"panic_type":           fmt.Sprintf("%T", recovered),
			})
			diagnostics.Record(runCtx, "event.dispatch.abandoned", panicFields)
			endRun(panicFields)
			dispatch.endTask(panicFields)
			// 保持既有语义：本次派发的剩余 handler 不再执行，panic 继续交给
			// bilisentry 的外层 Recover 处理。
			panic(recovered)
		}

		completeFields := mergeFields(dispatch.baseFields, diagnostics.Fields{
			"status":          "ok",
			"completed_count": completed,
			"handler_count":   len(dispatch.listeners),
			"queue_delay_ms":  durationMilliseconds(queueDelay),
		})
		diagnostics.Record(runCtx, "event.dispatch.complete", completeFields)
		endRun(completeFields)
		dispatch.endTask(completeFields)
	}()

	for index, handler := range dispatch.listeners {
		handlerName := "nil_handler"
		if handler != nil && handler.Name != "" {
			handlerName = handler.Name
		}
		handlerFields := mergeFields(dispatch.baseFields, diagnostics.Fields{
			"handler_index": index,
			"handler_name":  handlerName,
		})
		handlerCtx, endHandler := diagnostics.StartSpan(runCtx, "event.handler", handlerFields)
		func() {
			defer func() {
				if recovered := recover(); recovered != nil {
					panicFields := mergeFields(handlerFields, diagnostics.Fields{
						"status":     "panic",
						"panic_type": fmt.Sprintf("%T", recovered),
					})
					diagnostics.Record(handlerCtx, "event.handler.panic", panicFields)
					endHandler(panicFields)
					panic(recovered)
				}
				endHandler(diagnostics.Fields{"status": "ok"})
			}()
			handler.Handler(dispatch.handlerEvent)
		}()
		completed++
	}
}

func (e *dispatcher) DispatchEvent(event *Event) {
	dispatch := e.prepareDispatch(event, "async")
	if dispatch == nil {
		return
	}
	bilisentry.GoWithContext(dispatch.taskCtx, func(context.Context) {
		e.runDispatch(dispatch)
	})
}

func (e *dispatcher) DispatchEventSync(event *Event) {
	dispatch := e.prepareDispatch(event, "sync")
	if dispatch == nil {
		return
	}
	// 与异步派发保持相同的 panic 隔离语义，避免单个事件处理器打断调用方。
	func() {
		defer bilisentry.RecoverWithContext(dispatch.taskCtx)
		e.runDispatch(dispatch)
	}()
}

func mergeFields(base, extra diagnostics.Fields) diagnostics.Fields {
	result := make(diagnostics.Fields, len(base)+len(extra))
	for key, value := range base {
		result[key] = value
	}
	for key, value := range extra {
		result[key] = value
	}
	return result
}

func durationMilliseconds(duration time.Duration) float64 {
	return float64(duration) / float64(time.Millisecond)
}
