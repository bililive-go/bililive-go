package livestate

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/bililive-go/bililive-go/src/instance"
	"github.com/bililive-go/bililive-go/src/listeners"
	"github.com/bililive-go/bililive-go/src/live"
	"github.com/bililive-go/bililive-go/src/pkg/events"
	"github.com/bililive-go/bililive-go/src/recorders"
	"github.com/bililive-go/bililive-go/src/types"
)

type liveEndListenerSource struct {
	closed bool
}

func (*liveEndListenerSource) Start() error {
	return nil
}

func (*liveEndListenerSource) StartWithInfo(*live.Info) error {
	return nil
}

func (s *liveEndListenerSource) Close() {
	s.closed = true
}

func (s *liveEndListenerSource) CloseSync() {
	s.closed = true
}

func (s *liveEndListenerSource) IsClosed() bool {
	return s.closed
}

type listenerSourceLookupStub struct {
	current listeners.Listener
	err     error
}

func (s listenerSourceLookupStub) GetListener(context.Context, types.LiveID) (listeners.Listener, error) {
	return s.current, s.err
}

type recorderSourceLookupStub struct {
	current recorders.Recorder
	err     error
}

func (s recorderSourceLookupStub) GetRecorder(context.Context, types.LiveID) (recorders.Recorder, error) {
	return s.current, s.err
}

func TestLiveEndSourceReplacedForListener(t *testing.T) {
	ctx := context.Background()
	oldListener := &liveEndListenerSource{closed: true}
	newListener := &liveEndListenerSource{}
	event := events.NewEventWithSource(listeners.LiveEnd, nil, oldListener)

	assert.False(t, liveEndSourceReplaced(
		ctx,
		event,
		"test",
		listenerSourceLookupStub{err: listeners.ErrListenerNotExist},
		nil,
	), "关闭前已派发的 LiveEnd 在没有替代 listener 时仍应保留")
	assert.False(t, liveEndSourceReplaced(
		ctx,
		event,
		"test",
		listenerSourceLookupStub{current: oldListener},
		nil,
	))
	assert.True(t, liveEndSourceReplaced(
		ctx,
		event,
		"test",
		listenerSourceLookupStub{current: newListener},
		nil,
	), "旧 listener 的 LiveEnd 不应结束新 listener 对应的直播会话")
}

func TestLiveEndSourceReplacedForRecorder(t *testing.T) {
	inst := &instance.Instance{}
	ctx := context.WithValue(context.Background(), instance.Key, inst)
	inst.EventDispatcher = events.NewDispatcher(ctx)
	oldRecorder, err := recorders.NewRecorder(ctx, nil)
	assert.NoError(t, err)
	newRecorder, err := recorders.NewRecorder(ctx, nil)
	assert.NoError(t, err)
	event := events.NewEventWithSource(listeners.LiveEnd, nil, oldRecorder)

	assert.False(t, liveEndSourceReplaced(
		ctx,
		event,
		"test",
		nil,
		recorderSourceLookupStub{err: recorders.ErrRecorderNotExist},
	), "没有替代 recorder 时应保留当前实例派发的 LiveEnd")
	assert.False(t, liveEndSourceReplaced(
		ctx,
		event,
		"test",
		nil,
		recorderSourceLookupStub{current: oldRecorder},
	))
	assert.True(t, liveEndSourceReplaced(
		ctx,
		event,
		"test",
		nil,
		recorderSourceLookupStub{current: newRecorder},
	), "旧 recorder 的 LiveEnd 不应结束新 recorder 对应的直播会话")
}
