package listeners

import (
	"context"
	"errors"
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
		inst := instance.GetInstance(ctx)
		logger := originalLive.GetLogger()
		rawURL := originalLive.GetRawUrl()
		oldLiveID := initializingLive.GetLiveId()

		// GetInfo 可能在用户删除房间后才返回。先确认配置仍存在，避免迟到的初始化
		// 回调重新插入已删除的房间，更不能把这种正常竞态升级成 panic。
		cfg := configs.GetCurrentConfig()
		if _, err := getConfiguredRoom(cfg, rawURL); err != nil {
			logger.Debug("直播间初始化完成时配置已不存在，忽略迟到结果")
			m.discardInitializingLive(ctx, inst, oldLiveID, initializingLive)
			return
		}

		if info.CustomLiveId != "" {
			originalLive.SetLiveIdByString(info.CustomLiveId)
		}

		// 将原始 Live 包装为 WrappedLive（使用全局缓存）
		// 传入 ctx 以便调度器可以被统一取消
		wrappedLive := live.NewWrappedLive(ctx, originalLive, inst.Cache)

		// 将已有的 info 注入新的 WrappedLive，避免 listener 交接后立即重复请求平台，
		// 同时让新调度器从本次成功请求开始计算下一轮间隔。
		info.Live = wrappedLive
		if err := wrappedLive.(*live.WrappedLive).SeedInfo(info); err != nil {
			logger.WithError(err).Warn("failed to cache info for new live")
		}

		// 只有 map 中仍是本次初始化对象时才允许交接。删除流程会先移除旧对象，
		// 因此迟到回调无法再把房间复活；同时避免覆盖已占用的新 LiveID。
		newLiveID := wrappedLive.GetLiveId()
		if !inst.Lives.ReplaceKeyIfCurrent(oldLiveID, newLiveID, initializingLive, wrappedLive) {
			logger.Debug("直播间初始化结果已过期，放弃状态交接")
			wrappedLive.Close()
			return
		}

		// 配置可能在第一次检查与 map 交接之间被删除，再检查一次并回滚新对象。
		cfg = configs.GetCurrentConfig()
		room, err := getConfiguredRoom(cfg, rawURL)
		if err != nil {
			logger.Debug("直播间在初始化交接期间被删除，回滚迟到结果")
			inst.Lives.DeleteIfCurrent(newLiveID, wrappedLive)
			wrappedLive.Close()
			initializingLive.Close()
			return
		}
		configs.SetLiveRoomId(rawURL, newLiveID)
		if room.IsListening {
			if err := m.replaceListener(ctx, initializingLive, wrappedLive, info); err != nil {
				logger.WithError(err).Error("直播间初始化完成，但 listener 交接失败")
				inst.Lives.DeleteIfCurrent(newLiveID, wrappedLive)
				wrappedLive.Close()
				m.discardInitializingLive(ctx, inst, oldLiveID, initializingLive)
				return
			}
		}
		initializingLive.Close()
	}))
}

func getConfiguredRoom(cfg *configs.Config, rawURL string) (*configs.LiveRoom, error) {
	if cfg == nil {
		return nil, errors.New("配置尚未初始化")
	}
	return cfg.GetLiveRoomByUrl(rawURL)
}

func (m *manager) discardInitializingLive(ctx context.Context, inst *instance.Instance, id types.LiveID, initializingLive live.Live) {
	if m.HasListener(ctx, id) {
		_ = m.RemoveListener(ctx, id)
	}
	inst.Lives.DeleteIfCurrent(id, initializingLive)
	initializingLive.Close()
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

func (m *manager) replaceListener(ctx context.Context, oldLive live.Live, newLive live.Live, info *live.Info) error {
	m.lock.Lock()
	defer m.lock.Unlock()
	newLiveID := newLive.GetLiveId()
	current, ok := instance.GetInstance(ctx).Lives.Get(newLiveID)
	if !ok || current != newLive {
		return ErrInitializingResultExpired
	}
	oldLiveId := oldLive.GetLiveId()
	oldListener, ok := m.savers[oldLiveId]
	if !ok {
		return ErrListenerNotExist
	}
	// 必须等旧 ListenStop 的录制器清理完成后，才能让新 listener 发布 LiveStart。
	oldListener.CloseSync()
	newListener := newListener(ctx, newLive)
	if oldLiveId == newLiveID {
		m.savers[oldLiveId] = newListener
	} else {
		delete(m.savers, oldLiveId)
		m.savers[newLiveID] = newListener
	}
	if err := newListener.StartWithInfo(info); err != nil {
		newListener.Close()
		delete(m.savers, newLiveID)
		return err
	}
	return nil
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
