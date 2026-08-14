package listeners

import (
	"context"
	"sync"

	"github.com/bililive-go/bililive-go/src/configs"
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
		savers:  make(map[types.LiveID]Listener),
		closing: make(map[types.LiveID]chan struct{}),
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
	lock    sync.RWMutex
	savers  map[types.LiveID]Listener
	closing map[types.LiveID]chan struct{}
}

func (m *manager) registryListener(ctx context.Context, ed events.Dispatcher) {
	ed.AddEventListener(RoomInitializingFinished, events.NewEventListener(func(event *events.Event) {
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
	inst := instance.GetInstance(ctx)
	if cfg := configs.GetCurrentConfig(); (cfg != nil && cfg.RPC.Enable) || inst.Lives.Len() > 0 {
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
	if err := ctx.Err(); err != nil {
		return err
	}
	liveID := live.GetLiveId()
	for {
		m.lock.Lock()
		if err := ctx.Err(); err != nil {
			m.lock.Unlock()
			return err
		}
		if done, ok := m.closing[liveID]; ok {
			m.lock.Unlock()
			select {
			case <-done:
				continue
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		if _, ok := m.savers[liveID]; ok {
			m.lock.Unlock()
			return ErrListenerExist
		}
		listener := newListener(ctx, live)
		m.savers[liveID] = listener
		err := listener.Start()
		m.lock.Unlock()
		return err
	}
}

func (m *manager) RemoveListener(ctx context.Context, liveId types.LiveID) error {
	m.lock.Lock()
	listener, ok := m.savers[liveId]
	if !ok {
		m.lock.Unlock()
		return ErrListenerNotExist
	}
	if m.closing == nil {
		m.closing = make(map[types.LiveID]chan struct{})
	}
	done := make(chan struct{})
	m.closing[liveId] = done
	delete(m.savers, liveId)
	m.lock.Unlock()

	// 同步清理在全局锁外执行，避免阻塞其他房间，也允许事件处理器回调 manager。
	// 同一房间的 AddListener 会等待 done，再发布新的 LiveStart。
	defer func() {
		m.lock.Lock()
		delete(m.closing, liveId)
		close(done)
		m.lock.Unlock()
	}()
	listener.CloseSync()
	return nil
}

func (m *manager) replaceListener(ctx context.Context, oldLive live.Live, newLive live.Live, info *live.Info) error {
	m.lock.Lock()
	defer m.lock.Unlock()
	oldLiveId := oldLive.GetLiveId()
	oldListener, ok := m.savers[oldLiveId]
	if !ok {
		return ErrListenerNotExist
	}
	// 必须等旧 ListenStop 的录制器清理完成后，才能让新 listener 发布 LiveStart。
	oldListener.CloseSync()
	newListener := newListener(ctx, newLive)
	if oldLiveId == newLive.GetLiveId() {
		m.savers[oldLiveId] = newListener
	} else {
		delete(m.savers, oldLiveId)
		m.savers[newLive.GetLiveId()] = newListener
	}
	return newListener.StartWithInfo(info)
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
