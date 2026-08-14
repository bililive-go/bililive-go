package events

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

type closeableEventSource struct {
	closed bool
}

func (s *closeableEventSource) IsClosed() bool {
	return s.closed
}

func TestSourceClosed(t *testing.T) {
	assert.False(t, SourceClosed(nil))
	assert.False(t, SourceClosed(NewEvent("test", nil)))
	assert.False(t, SourceClosed(NewEventWithSource("test", nil, &closeableEventSource{})))
	assert.True(t, SourceClosed(NewEventWithSource("test", nil, &closeableEventSource{closed: true})))
}

func TestAddAndRemoveEventListener(t *testing.T) {
	d := NewDispatcher(context.Background()).(*dispatcher)
	l := NewEventListener(func(event *Event) {})
	d.AddEventListener("test", l)
	d.AddEventListener("test2", NewEventListener(func(event *Event) {}))
	ls, ok := d.saver["test"]
	assert.True(t, ok)
	assert.Equal(t, l, ls.Front().Value)
	d.RemoveEventListener("test", l)
	_, ok = d.saver["test"]
	assert.False(t, ok)
	d.RemoveAllEventListener("test2")
	assert.Empty(t, d.saver)
}

func TestDispatchEvent(t *testing.T) {
	l := make([]int, 0)
	d := NewDispatcher(context.Background()).(*dispatcher)
	d.AddEventListener("test", NewEventListener(func(event *Event) {
		l = append(l, 0)
	}))
	d.AddEventListener("test", NewEventListener(func(event *Event) {
		l = append(l, 1)
	}))
	d.AddEventListener("test", NewEventListener(func(event *Event) {
		l = append(l, 2)
	}))
	d.AddEventListener("test", NewEventListener(func(event *Event) {
		l = append(l, 3)
	}))
	d.DispatchEvent(NewEvent("test", nil))
	time.Sleep(time.Second)
	assert.Equal(t, []int{0, 1, 2, 3}, l)
}

func TestDispatchEventSyncWaitsForHandlers(t *testing.T) {
	d := NewDispatcher(context.Background()).(*dispatcher)
	handlerStarted := make(chan struct{})
	releaseHandler := make(chan struct{})
	dispatchDone := make(chan struct{})

	d.AddEventListener("test", NewEventListener(func(event *Event) {
		close(handlerStarted)
		<-releaseHandler
	}))

	go func() {
		d.DispatchEventSync(NewEvent("test", nil))
		close(dispatchDone)
	}()

	select {
	case <-handlerStarted:
	case <-time.After(time.Second):
		t.Fatal("同步事件处理器未启动")
	}

	select {
	case <-dispatchDone:
		t.Fatal("同步派发在事件处理器完成前返回")
	default:
	}

	close(releaseHandler)
	select {
	case <-dispatchDone:
	case <-time.After(time.Second):
		t.Fatal("同步派发未在事件处理器完成后返回")
	}
}
