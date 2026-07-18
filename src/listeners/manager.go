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

		// 直接将已有的 info 存入缓存，避免再次调用 GetInfo() 触发 API 请求
		// info.Live 需要更新为新的 wrappedLive，确保后续 JSON 序列化正确
		info.Live = wrappedLive
		if err := inst.Cache.Set(wrappedLive, info); err != nil {
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
			if err := m.replaceListener(ctx, initializingLive, wrappedLive); err != nil {
				logger.WithFields(map[string]any{
					"url": wrappedLive.GetRawUrl(),
				}).Error(err)
			}
		}
	}))
}

func (m *manager) Start(ctx context.Context) error {
	inst := instance.GetInstance(ctx)
	// manager 在直播间初始化前启动，不能用此时仍为空的 Lives 判断是否需要阻塞主进程。
	// 配置校验已经保证 RPC 关闭时至少存在一个直播间，因此 manager 启动后始终参与生命周期等待。
	inst.WaitGroup.Add(1)
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
