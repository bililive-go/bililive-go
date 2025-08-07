package events

type EventType string

type EventHandler func(event *Event)

type Event struct {
	Type   EventType
	Object any
}

func NewEvent(eventType EventType, object any) *Event {
	return &Event{eventType, object}
}

type EventListener struct {
	Handler EventHandler
}

func NewEventListener(handler EventHandler) *EventListener {
	return &EventListener{handler}
}
