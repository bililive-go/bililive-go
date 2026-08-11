package events

import (
	"context"
	"reflect"
	"runtime"
	"sync/atomic"

	"github.com/bililive-go/bililive-go/src/pkg/diagnostics"
)

type EventType string

type EventHandler func(event *Event)

// Causality 描述事件所属的同一业务实体、生产者生命周期和生产顺序。
//
// ScopeKey 只用于进程内的正确性校验，不应写入日志或对外返回。ProducerID、
// Generation 和 Sequence 可以安全地用于诊断同一个 listener 生命周期中的乱序事件。
type Causality struct {
	ScopeKey   string
	ProducerID string
	Generation uint64
	Sequence   uint64
}

// Valid 返回该因果信息是否足以用于拒绝旧 generation 或乱序事件。
func (c Causality) Valid() bool {
	return c.ScopeKey != "" && c.ProducerID != "" && c.Generation > 0 && c.Sequence > 0
}

type causalSourceContextKey struct{}

type causalSource struct {
	scopeKey   string
	producerID string
	generation uint64
	sequence   atomic.Uint64
}

type Event struct {
	Type     EventType
	Object   any
	ID       string
	traceCtx context.Context

	causality    Causality
	hasCausality bool
}

func NewEvent(eventType EventType, object any) *Event {
	return NewEventWithContext(context.Background(), eventType, object)
}

// WithCausalSource 为后续通过该 context 创建的事件绑定一个有序生产者。
//
// 派生 context 会共享同一个原子序号，因此即使事件由多个 goroutine 创建，也能
// 得到唯一、单调递增的生产顺序。scopeKey 只能使用不含原文的进程内房间哈希键，不能传入
// 原始 URL、房间号或其他敏感信息。
func WithCausalSource(
	ctx context.Context,
	scopeKey string,
	producerID string,
	generation uint64,
) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	source := &causalSource{
		scopeKey:   scopeKey,
		producerID: producerID,
		generation: generation,
	}
	return context.WithValue(ctx, causalSourceContextKey{}, source)
}

// NewEventWithContext 创建一个携带业务因果上下文的事件。
//
// ctx 只用于传递 diagnostics 的 flow/task/span 等标识，不应使用短生命周期的
// HTTP request context 来控制事件处理器生命周期。
func NewEventWithContext(ctx context.Context, eventType EventType, object any) *Event {
	if ctx == nil {
		ctx = context.Background()
	}
	var (
		causality Causality
		hasCausal bool
	)
	if source, ok := ctx.Value(causalSourceContextKey{}).(*causalSource); ok && source != nil {
		causality = Causality{
			ScopeKey:   source.scopeKey,
			ProducerID: source.producerID,
			Generation: source.generation,
			Sequence:   source.sequence.Add(1),
		}
		hasCausal = true
	}
	return newEvent(ctx, eventType, object, causality, hasCausal)
}

// NewEventWithCausality 创建携带显式因果信息的事件。
//
// 该入口主要供桥接旧生产者和确定性测试使用；正常 listener 应通过
// WithCausalSource 让序号自动生成。返回事件的 Context 会以当前 Sequence 为
// 起点继续派生后续事件。
func NewEventWithCausality(
	ctx context.Context,
	eventType EventType,
	object any,
	causality Causality,
) *Event {
	if ctx == nil {
		ctx = context.Background()
	}
	source := &causalSource{
		scopeKey:   causality.ScopeKey,
		producerID: causality.ProducerID,
		generation: causality.Generation,
	}
	source.sequence.Store(causality.Sequence)
	ctx = context.WithValue(ctx, causalSourceContextKey{}, source)
	return newEvent(ctx, eventType, object, causality, true)
}

func newEvent(
	ctx context.Context,
	eventType EventType,
	object any,
	causality Causality,
	hasCausality bool,
) *Event {
	eventID := diagnostics.NewID("event")
	ctx = diagnostics.WithFields(ctx, diagnostics.Fields{
		"event_id": eventID,
	})
	return &Event{
		Type:         eventType,
		Object:       object,
		ID:           eventID,
		traceCtx:     ctx,
		causality:    causality,
		hasCausality: hasCausality,
	}
}

// Context 返回事件生产者的业务因果上下文；事件未携带上下文时返回 fallback。
func (e *Event) Context(fallback context.Context) context.Context {
	if e != nil && e.traceCtx != nil {
		return e.traceCtx
	}
	if fallback != nil {
		return fallback
	}
	return context.Background()
}

// Causality 返回事件在生产者生命周期中的不可变因果快照。
func (e *Event) Causality() (Causality, bool) {
	if e == nil || !e.hasCausality {
		return Causality{}, false
	}
	return e.causality, true
}

type EventListener struct {
	Handler EventHandler
	Name    string
}

func NewEventListener(handler EventHandler) *EventListener {
	return NewNamedEventListener(defaultHandlerName(handler), handler)
}

// NewNamedEventListener 创建带稳定诊断名称的事件处理器。
// 新代码应优先显式传入低基数名称；旧调用方仍可使用 NewEventListener。
func NewNamedEventListener(name string, handler EventHandler) *EventListener {
	if name == "" {
		name = defaultHandlerName(handler)
	}
	return &EventListener{
		Handler: handler,
		Name:    name,
	}
}

func defaultHandlerName(handler EventHandler) string {
	if handler == nil {
		return "nil_handler"
	}
	value := reflect.ValueOf(handler)
	if !value.IsValid() {
		return "unknown_handler"
	}
	if fn := runtime.FuncForPC(value.Pointer()); fn != nil {
		return fn.Name()
	}
	return "unknown_handler"
}
