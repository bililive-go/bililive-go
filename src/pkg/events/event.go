package events

type EventType string

type EventHandler func(event *Event)

type Event struct {
	Type   EventType
	Object any
	// Source 标识事件的发布实例，供处理器拒绝已失效实例的迟到事件。
	Source any
}

func NewEvent(eventType EventType, object any) *Event {
	return &Event{Type: eventType, Object: object}
}

func NewEventWithSource(eventType EventType, object, source any) *Event {
	return &Event{Type: eventType, Object: object, Source: source}
}

// SourceClosed 用于判断事件是否来自已经关闭的发布实例。
// 事件处理器可用它过滤关闭后才执行的异步事件，避免旧实例影响新实例的状态。
func SourceClosed(event *Event) bool {
	if event == nil {
		return false
	}
	source, ok := event.Source.(interface{ IsClosed() bool })
	return ok && source.IsClosed()
}

type EventListener struct {
	Handler EventHandler
}

func NewEventListener(handler EventHandler) *EventListener {
	return &EventListener{handler}
}
