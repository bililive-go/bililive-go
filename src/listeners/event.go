package listeners

import (
	"github.com/bililive-go/bililive-go/src/pkg/events"
)

const (
	ListenStart              events.EventType = "ListenStart"
	ListenStop               events.EventType = "ListenStop"
	LiveStart                events.EventType = "LiveStart"
	LiveEnd                  events.EventType = "LiveEnd"
	RoomNameChanged          events.EventType = "RoomNameChanged"
	RoomInitializingFinished events.EventType = "RoomInitializingFinished"
)
