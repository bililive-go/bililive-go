package recorders

import (
	"context"
	"sync"
	"time"

	"github.com/hr3lxphr6j/bililive-go/src/configs"
	"github.com/hr3lxphr6j/bililive-go/src/instance"
	"github.com/hr3lxphr6j/bililive-go/src/interfaces"
	"github.com/hr3lxphr6j/bililive-go/src/listeners"
	"github.com/hr3lxphr6j/bililive-go/src/live"
	"github.com/hr3lxphr6j/bililive-go/src/pkg/events"
	"github.com/hr3lxphr6j/bililive-go/src/types"
)

func NewManager(ctx context.Context) Manager {
	rm := &manager{
		savers: make(map[types.LiveID]Recorder),
		cfg:    instance.GetInstance(ctx).Config,
	}
	instance.GetInstance(ctx).RecorderManager = rm

	return rm
}

type Manager interface {
	interfaces.Module
	AddRecorder(ctx context.Context, live live.Live) error
	RemoveRecorder(ctx context.Context, liveId types.LiveID) error
	RestartRecorder(ctx context.Context, liveId live.Live) error
	GetRecorder(ctx context.Context, liveId types.LiveID) (Recorder, error)
	HasRecorder(ctx context.Context, liveId types.LiveID) bool
}

// for test
var (
	newRecorder = NewRecorder
)

type manager struct {
	lock   sync.RWMutex
	savers map[types.LiveID]Recorder
	cfg    *configs.Config
}

func (m *manager) registryListener(ctx context.Context, ed events.Dispatcher) {
	ed.AddEventListener(listeners.LiveStart, events.NewEventListener(func(event *events.Event) {
		live := event.Object.(live.Live)
		if err := m.AddRecorder(ctx, live); err != nil {
			instance.GetInstance(ctx).Logger.Errorf("failed to add recorder, err: %v", err)
		}
	}))

	ed.AddEventListener(listeners.RoomNameChanged, events.NewEventListener(func(event *events.Event) {
		live := event.Object.(live.Live)
		if !m.HasRecorder(ctx, live.GetLiveId()) {
			return
		}
		if err := m.RestartRecorder(ctx, live); err != nil {
			instance.GetInstance(ctx).Logger.Errorf("failed to cronRestart recorder, err: %v", err)
		}
	}))

	removeEvtListener := events.NewEventListener(func(event *events.Event) {
		live := event.Object.(live.Live)
		if !m.HasRecorder(ctx, live.GetLiveId()) {
			return
		}
		if err := m.RemoveRecorder(ctx, live.GetLiveId()); err != nil {
			instance.GetInstance(ctx).Logger.Errorf("failed to remove recorder, err: %v", err)
		}
	})
	ed.AddEventListener(listeners.LiveEnd, removeEvtListener)
	ed.AddEventListener(listeners.ListenStop, removeEvtListener)
}

func (m *manager) Start(ctx context.Context) error {
	inst := instance.GetInstance(ctx)
	if inst.Config.RPC.Enable || len(inst.Lives) > 0 {
		inst.WaitGroup.Add(1)
	}
	m.registryListener(ctx, inst.EventDispatcher.(events.Dispatcher))
	return nil
}

func (m *manager) Close(ctx context.Context) {
	m.lock.Lock()
	defer m.lock.Unlock()
	for id, recorder := range m.savers {
		recorder.Close()
		delete(m.savers, id)
	}
	inst := instance.GetInstance(ctx)
	inst.WaitGroup.Done()
}

func (m *manager) AddRecorder(ctx context.Context, live live.Live) error {
	m.lock.Lock()
	defer m.lock.Unlock()
	if _, ok := m.savers[live.GetLiveId()]; ok {
		return ErrRecorderExist
	}
	recorder, err := newRecorder(ctx, live)
	if err != nil {
		return err
	}
	m.savers[live.GetLiveId()] = recorder

	room, err := m.cfg.GetLiveRoomByUrl(live.GetRawUrl())
	if err != nil {
		instance.GetInstance(ctx).Logger.
			WithError(err).
			WithField("url", live.GetRawUrl()).
			Error("failed to get room config")
		return err
	}

	maxDur := room.VideoSplitStrategies.MaxDuration
	if maxDur == 0 {
		maxDur = m.cfg.VideoSplitStrategies.MaxDuration
	}

	if maxDur != 0 {
		go m.cronRestart(ctx, live)
	}
	return recorder.Start(ctx)
}

func (m *manager) cronRestart(ctx context.Context, live live.Live) {
	recorder, err := m.GetRecorder(ctx, live.GetLiveId())
	if err != nil {
		return
	}

	// 获取当前直播间的配置
	room, err := m.cfg.GetLiveRoomByUrl(live.GetRawUrl())
	if err != nil {
		instance.GetInstance(ctx).Logger.
			WithError(err).
			WithField("url", live.GetRawUrl()).
			Error("failed to get room config")
		return
	}

	// 优先使用直播间的配置，如果没有则使用全局配置
	maxDur := room.VideoSplitStrategies.MaxDuration
	if maxDur == 0 {
		maxDur = m.cfg.VideoSplitStrategies.MaxDuration
	}

	if time.Now().Sub(recorder.StartTime()) < maxDur {
		time.AfterFunc(time.Minute/4, func() {
			m.cronRestart(ctx, live)
		})
		return
	}
	if err := m.RestartRecorder(ctx, live); err != nil {
		return
	}
}

func (m *manager) RestartRecorder(ctx context.Context, live live.Live) error {
	if err := m.RemoveRecorder(ctx, live.GetLiveId()); err != nil {
		return err
	}
	if err := m.AddRecorder(ctx, live); err != nil {
		return err
	}
	return nil
}

func (m *manager) RemoveRecorder(ctx context.Context, liveId types.LiveID) error {
	m.lock.Lock()
	defer m.lock.Unlock()
	recorder, ok := m.savers[liveId]
	if !ok {
		return ErrRecorderNotExist
	}
	recorder.Close()
	delete(m.savers, liveId)
	return nil
}

func (m *manager) GetRecorder(ctx context.Context, liveId types.LiveID) (Recorder, error) {
	m.lock.RLock()
	defer m.lock.RUnlock()
	r, ok := m.savers[liveId]
	if !ok {
		return nil, ErrRecorderNotExist
	}
	return r, nil
}

func (m *manager) HasRecorder(ctx context.Context, liveId types.LiveID) bool {
	m.lock.RLock()
	defer m.lock.RUnlock()
	_, ok := m.savers[liveId]
	return ok
}
