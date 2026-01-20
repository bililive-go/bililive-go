package recorders

import (
	"context"
	"sync"
	"time"

	"github.com/bililive-go/bililive-go/src/configs"
	"github.com/bililive-go/bililive-go/src/instance"
	"github.com/bililive-go/bililive-go/src/interfaces"
	"github.com/bililive-go/bililive-go/src/listeners"
	"github.com/bililive-go/bililive-go/src/live"
	"github.com/bililive-go/bililive-go/src/pkg/events"
	bilisentry "github.com/bililive-go/bililive-go/src/pkg/sentry"
	"github.com/bililive-go/bililive-go/src/types"
)

// BroadcastRecorderStatusFunc 是用于广播录制器状态的回调函数类型
type BroadcastRecorderStatusFunc func(liveId types.LiveID, status map[string]interface{})

// OnRecordingEndFunc 是录制结束时的回调函数类型
type OnRecordingEndFunc func(ctx context.Context)

var (
	// broadcastRecorderStatusFunc 全局广播函数，由 servers 包设置
	broadcastRecorderStatusFunc BroadcastRecorderStatusFunc
	// onRecordingEndFunc 录制结束时的回调函数，用于触发优雅更新检查
	onRecordingEndFunc OnRecordingEndFunc
)

// SetBroadcastRecorderStatusFunc 设置录制器状态广播函数
func SetBroadcastRecorderStatusFunc(fn BroadcastRecorderStatusFunc) {
	broadcastRecorderStatusFunc = fn
}

// SetOnRecordingEndFunc 设置录制结束回调函数
func SetOnRecordingEndFunc(fn OnRecordingEndFunc) {
	onRecordingEndFunc = fn
}

func NewManager(ctx context.Context) Manager {
	rm := &manager{
		savers:       make(map[types.LiveID]Recorder),
		statusStopCh: make(chan struct{}),
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
	// GetAllParserPIDs 获取所有活动录制器的 parser PID 列表
	GetAllParserPIDs() []int
	// GetRecorderStatus 获取指定直播间录制器的状态
	// 实现 iostats.RecorderStatusProvider 接口
	GetRecorderStatus(ctx context.Context, liveId types.LiveID) (map[string]interface{}, error)
}

// for test
var (
	newRecorder = NewRecorder
)

type manager struct {
	lock         sync.RWMutex
	savers       map[types.LiveID]Recorder
	statusTicker *time.Ticker
	statusStopCh chan struct{}
	statusWg     sync.WaitGroup // 用于等待广播 goroutine 退出
}

func (m *manager) registryListener(ctx context.Context, ed events.Dispatcher) {
	ed.AddEventListener(listeners.LiveStart, events.NewEventListener(func(event *events.Event) {
		live := event.Object.(live.Live)
		if err := m.AddRecorder(ctx, live); err != nil {
			live.GetLogger().Errorf("failed to add recorder, err: %v", err)
		}
	}))

	ed.AddEventListener(listeners.RoomNameChanged, events.NewEventListener(func(event *events.Event) {
		live := event.Object.(live.Live)
		if !m.HasRecorder(ctx, live.GetLiveId()) {
			return
		}
		if err := m.RestartRecorder(ctx, live); err != nil {
			live.GetLogger().Errorf("failed to cronRestart recorder, err: %v", err)
		}
	}))

	removeEvtListener := events.NewEventListener(func(event *events.Event) {
		live := event.Object.(live.Live)
		if !m.HasRecorder(ctx, live.GetLiveId()) {
			return
		}
		if err := m.RemoveRecorder(ctx, live.GetLiveId()); err != nil {
			live.GetLogger().Errorf("failed to remove recorder, err: %v", err)
		}
	})
	ed.AddEventListener(listeners.LiveEnd, removeEvtListener)
	ed.AddEventListener(listeners.ListenStop, removeEvtListener)
}

func (m *manager) Start(ctx context.Context) error {
	inst := instance.GetInstance(ctx)
	if cfg := configs.GetCurrentConfig(); (cfg != nil && cfg.RPC.Enable) || len(inst.Lives) > 0 {
		inst.WaitGroup.Add(1)
	}
	m.registryListener(ctx, inst.EventDispatcher.(events.Dispatcher))

	// 启动定期广播录制器状态的 goroutine
	m.startStatusBroadcaster(ctx)

	return nil
}

func (m *manager) Close(ctx context.Context) {
	// 停止状态广播器
	if m.statusTicker != nil {
		m.statusTicker.Stop()
	}
	if m.statusStopCh != nil {
		close(m.statusStopCh)
		// 等待广播 goroutine 退出
		m.statusWg.Wait()
	}

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

	cfg := configs.GetCurrentConfig()
	if cfg != nil {
		if maxDur := cfg.VideoSplitStrategies.MaxDuration; maxDur != 0 {
			bilisentry.GoWithContext(ctx, func(ctx context.Context) { m.cronRestart(ctx, live) })
		}
	}
	return recorder.Start(ctx)
}

func (m *manager) cronRestart(ctx context.Context, live live.Live) {
	recorder, err := m.GetRecorder(ctx, live.GetLiveId())
	if err != nil {
		return
	}
	cfg := configs.GetCurrentConfig()
	if cfg == nil {
		return
	}
	if time.Since(recorder.StartTime()) < cfg.VideoSplitStrategies.MaxDuration {
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

	// 录制结束后，检查是否有等待中的优雅更新
	if onRecordingEndFunc != nil {
		bilisentry.GoWithContext(ctx, func(ctx context.Context) { onRecordingEndFunc(ctx) })
	}

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

// startStatusBroadcaster 启动定期广播录制器状态的 goroutine
func (m *manager) startStatusBroadcaster(ctx context.Context) {
	// 每5秒广播一次录制器状态
	m.statusTicker = time.NewTicker(5 * time.Second)

	m.statusWg.Add(1)
	bilisentry.Go(func() {
		defer m.statusWg.Done()
		// 使用回调函数避免循环依赖
		// 回调在 server 初始化时由 SetBroadcastRecorderStatusFunc 设置
		for {
			select {
			case <-m.statusStopCh:
				return
			case <-m.statusTicker.C:
				m.broadcastAllRecorderStatus(ctx)
			}
		}
	})
}

// broadcastAllRecorderStatus 广播所有录制器的状态
func (m *manager) broadcastAllRecorderStatus(ctx context.Context) {
	m.lock.RLock()
	defer m.lock.RUnlock()

	// 如果没有设置广播函数，直接返回
	if broadcastRecorderStatusFunc == nil {
		return
	}

	// 遍历所有录制器并广播状态
	for liveId, recorder := range m.savers {
		status, err := recorder.GetStatus()
		if err == nil && status != nil {
			broadcastRecorderStatusFunc(liveId, status)
		}
	}
}

// GetAllParserPIDs 获取所有活动录制器的 parser PID 列表
func (m *manager) GetAllParserPIDs() []int {
	m.lock.RLock()
	defer m.lock.RUnlock()

	pids := make([]int, 0, len(m.savers))
	for _, recorder := range m.savers {
		if pid := recorder.GetParserPID(); pid > 0 {
			pids = append(pids, pid)
		}
	}
	return pids
}

// GetRecorderStatus 获取指定直播间录制器的状态
// 实现 iostats.RecorderStatusProvider 接口
func (m *manager) GetRecorderStatus(ctx context.Context, liveId types.LiveID) (map[string]interface{}, error) {
	recorder, err := m.GetRecorder(ctx, liveId)
	if err != nil {
		return nil, err
	}
	return recorder.GetStatus()
}
