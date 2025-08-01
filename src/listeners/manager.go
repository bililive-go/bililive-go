package listeners

import (
	"context"
	"sync"

	"github.com/bililive-go/bililive-go/src/instance"
	"github.com/bililive-go/bililive-go/src/interfaces"
	"github.com/bililive-go/bililive-go/src/live"
	"github.com/bililive-go/bililive-go/src/pkg/events"
	"github.com/bililive-go/bililive-go/src/types"
)

// for test
var newListener = NewListener

func NewManager(ctx context.Context) Manager {
	lm := &manager{
		savers: make(map[types.LiveID]Listener),
	}
	instance.GetInstance(ctx).ListenerManager = lm
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
}

func (m *manager) registryListener(ctx context.Context, ed events.Dispatcher) {
	ed.AddEventListener(RoomInitializingFinished, events.NewEventListener(func(event *events.Event) {
		param := event.Object.(live.InitializingFinishedParam)
		initializingLive := param.InitializingLive
		live := param.Live
		info := param.Info
		if info.CustomLiveId != "" {
			live.SetLiveIdByString(info.CustomLiveId)
		}
		inst := instance.GetInstance(ctx)
		logger := inst.Logger
		inst.Lives[live.GetLiveId()] = live

		room, err := inst.Config.GetLiveRoomByUrl(live.GetRawUrl())
		if err != nil {
			logger.WithFields(map[string]any{
				"room": live.GetRawUrl(),
			}).Error(err)
			panic(err)
		}
		room.LiveId = live.GetLiveId()
		if room.IsListening {
			if err := m.replaceListener(ctx, initializingLive, live); err != nil {
				logger.WithFields(map[string]any{
					"url": live.GetRawUrl(),
				}).Error(err)
			}
		}
	}))
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
	for id, listener := range m.savers {
		listener.Close()
		delete(m.savers, id)
	}
	inst := instance.GetInstance(ctx)
	inst.WaitGroup.Done()
}

func (m *manager) AddListener(ctx context.Context, live live.Live) error {
	m.lock.Lock()
	defer m.lock.Unlock()

	if _, ok := m.savers[live.GetLiveId()]; ok {
		return ErrListenerExist
	}
	listener := newListener(ctx, live)
	m.savers[live.GetLiveId()] = listener
	return listener.Start()
}

func (m *manager) RemoveListener(ctx context.Context, liveId types.LiveID) error {
	m.lock.Lock()
	defer m.lock.Unlock()
	listener, ok := m.savers[liveId]
	if !ok {
		return ErrListenerNotExist
	}
	listener.Close()
	delete(m.savers, liveId)
	return nil
}

func (m *manager) replaceListener(ctx context.Context, oldLive live.Live, newLive live.Live) error {
	m.lock.Lock()
	defer m.lock.Unlock()
	oldLiveId := oldLive.GetLiveId()
	oldListener, ok := m.savers[oldLiveId]
	if !ok {
		return ErrListenerNotExist
	}
	oldListener.Close()
	newListener := newListener(ctx, newLive)
	if oldLiveId == newLive.GetLiveId() {
		m.savers[oldLiveId] = newListener
	} else {
		delete(m.savers, oldLiveId)
		m.savers[newLive.GetLiveId()] = newListener
	}
	return newListener.Start()
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
