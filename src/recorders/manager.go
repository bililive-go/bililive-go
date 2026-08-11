package recorders

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/bililive-go/bililive-go/src/configs"
	"github.com/bililive-go/bililive-go/src/instance"
	"github.com/bililive-go/bililive-go/src/interfaces"
	"github.com/bililive-go/bililive-go/src/live"
	"github.com/bililive-go/bililive-go/src/pkg/diagnostics"
	"github.com/bililive-go/bililive-go/src/pkg/events"
	bilisentry "github.com/bililive-go/bililive-go/src/pkg/sentry"
	"github.com/bililive-go/bililive-go/src/types"
)

// BroadcastRecorderStatusFunc 是用于广播录制器状态的回调函数类型
type BroadcastRecorderStatusFunc func(liveId types.LiveID, status map[string]interface{})

// OnRecordingEndFunc 是录制结束时的回调函数类型
type OnRecordingEndFunc func(ctx context.Context)

// BroadcastDanmakuFunc 是用于广播弹幕消息的回调函数类型
type BroadcastDanmakuFunc func(liveId types.LiveID, msgType, username, content string, extra map[string]interface{})

var (
	// broadcastRecorderStatusFunc 全局广播函数，由 servers 包设置
	broadcastRecorderStatusFunc BroadcastRecorderStatusFunc
	// onRecordingEndFunc 录制结束时的回调函数，用于触发优雅更新检查
	onRecordingEndFunc OnRecordingEndFunc
	// broadcastDanmakuFunc 全局弹幕广播函数，由 servers 包设置
	broadcastDanmakuFunc BroadcastDanmakuFunc
)

func diagnosticManagerErrorType(err error) string {
	if err == nil {
		return ""
	}
	return fmt.Sprintf("%T", err)
}

// SetBroadcastRecorderStatusFunc 设置录制器状态广播函数
func SetBroadcastRecorderStatusFunc(fn BroadcastRecorderStatusFunc) {
	broadcastRecorderStatusFunc = fn
}

// SetOnRecordingEndFunc 设置录制结束回调函数
func SetOnRecordingEndFunc(fn OnRecordingEndFunc) {
	onRecordingEndFunc = fn
}

// SetBroadcastDanmakuFunc 设置弹幕广播函数
func SetBroadcastDanmakuFunc(fn BroadcastDanmakuFunc) {
	broadcastDanmakuFunc = fn
}

func NewManager(ctx context.Context) Manager {
	if ctx == nil {
		ctx = context.Background()
	}
	managerCtx, cancel := context.WithCancel(ctx)
	rm := &manager{
		savers:             make(map[types.LiveID]Recorder),
		listenerLifecycles: make(map[string]listenerLifecycle),
		recorderOwners:     make(map[types.LiveID]recorderOwner),
		statusStopCh:       make(chan struct{}),
		inst:               instance.GetInstance(ctx),
		ctx:                managerCtx,
		cancel:             cancel,
	}
	if rm.inst != nil {
		rm.inst.RecorderManager = rm
	}

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
	// GetActiveRecordingsCount 获取当前活跃的录制数量
	GetActiveRecordingsCount() int
}

// for test
var (
	newRecorder = NewRecorder
)

type manager struct {
	lock               sync.RWMutex
	savers             map[types.LiveID]Recorder
	listenerLifecycles map[string]listenerLifecycle
	recorderOwners     map[types.LiveID]recorderOwner
	statusTicker       *time.Ticker
	statusStopCh       chan struct{}
	statusWg           sync.WaitGroup // 用于等待广播 goroutine 退出
	// restartingCount 追踪正在执行 CloseForRestart 的旧 recorder 数量。
	// RestartRecorder 在释放锁后才执行 oldRecorder.CloseForRestart()，
	// 此期间 map 中只有新 recorder，但旧 recorder 仍在收尾运行。
	// 如果此时 LiveEnd 删掉新 recorder，GetActiveRecordingsCount() 仅看 map
	// 会误判为"无活跃录制"导致优雅更新被提前触发。
	// 通过 restartingCount 将收尾中的旧 recorder 也计入活跃数量。
	restartingCount atomic.Int32

	inst   *instance.Instance
	ctx    context.Context
	cancel context.CancelFunc

	lifecycleMu sync.Mutex
	operations  sync.WaitGroup
	background  sync.WaitGroup
	closing     bool
	started     bool
	accounted   bool
	closeOnce   sync.Once
}

func (m *manager) beginOperation() bool {
	m.lifecycleMu.Lock()
	defer m.lifecycleMu.Unlock()
	if m.closing {
		return false
	}
	m.operations.Add(1)
	return true
}

func (m *manager) endOperation() {
	m.operations.Done()
}

// runBackground 登记由一次已登记 operation 派生的异步收尾。Close 会先
// 封闭 operation 闸门并等待 operations，再等待 background，因此 Add 和
// Wait 不会并发发生。
func (m *manager) runBackground(ctx context.Context, fn func(context.Context)) {
	if ctx == nil {
		ctx = m.ctx
	}
	m.background.Add(1)
	bilisentry.GoWithContext(ctx, func(ctx context.Context) {
		defer m.background.Done()
		fn(ctx)
	})
}

func (m *manager) Start(ctx context.Context) error {
	m.lifecycleMu.Lock()
	defer m.lifecycleMu.Unlock()
	if m.closing {
		return ErrManagerClosed
	}
	if m.started {
		return nil
	}
	m.started = true
	inst := m.inst
	if inst == nil {
		inst = instance.GetInstance(ctx)
		m.inst = inst
	}
	if inst != nil {
		inst.WaitGroup.Add(1)
		m.accounted = true
	}

	if inst == nil || inst.EventDispatcher == nil {
		return nil
	}
	m.registryListener(ctx, inst.EventDispatcher.(events.Dispatcher))

	// 启动定期广播录制器状态的 goroutine
	m.startStatusBroadcaster(ctx)

	return nil
}

func (m *manager) Close(ctx context.Context) {
	m.closeOnce.Do(func() {
		_, endSpan := diagnostics.StartSpan(ctx, "recorder.manager.close", diagnostics.Fields{
			"component": "recorder_manager",
			"lane":      "recorder",
		})
		spanEnded := false
		defer func() {
			if !spanEnded {
				endSpan(diagnostics.Fields{"status": "interrupted"})
			}
		}()

		m.lifecycleMu.Lock()
		m.closing = true
		accounted := m.accounted
		inst := m.inst
		m.lifecycleMu.Unlock()
		m.cancel()

		// 停止状态广播器，并等待正在执行的广播释放读锁。
		if m.statusTicker != nil {
			m.statusTicker.Stop()
		}
		if m.statusStopCh != nil {
			close(m.statusStopCh)
			m.statusWg.Wait()
		}

		// Dispatcher 可能已经排队了 LiveStart。关闭闸门后，尚未进入的
		// handler 会静默丢弃；已经进入的 handler 必须完成后才能清空 map。
		m.operations.Wait()

		m.lock.Lock()
		recordersToClose := make([]Recorder, 0, len(m.savers))
		for id, recorder := range m.savers {
			recordersToClose = append(recordersToClose, recorder)
			delete(m.savers, id)
			delete(m.recorderOwners, id)
		}
		clear(m.listenerLifecycles)
		clear(m.recorderOwners)
		m.lock.Unlock()

		// Recorder.Close 是同步屏障；锁外等待 parser、探测、文件观察器和
		// summary 收尾，避免阻塞仍在结束的 handler 获取 manager 锁。
		for _, recorder := range recordersToClose {
			recorder.Close()
		}
		m.background.Wait()

		endSpan(diagnostics.Fields{"status": "ok"})
		spanEnded = true
		if accounted && inst != nil {
			inst.WaitGroup.Done()
		}
	})
}

func (m *manager) AddRecorder(ctx context.Context, live live.Live) error {
	if !m.beginOperation() {
		return ErrManagerClosed
	}
	defer m.endOperation()

	spanCtx, endSpan := diagnostics.StartSpan(ctx, "recorder.manager.add", diagnostics.Fields{
		"component": "recorder_manager",
		"lane":      "recorder",
	})
	spanEnded := false
	defer func() {
		if !spanEnded {
			endSpan(diagnostics.Fields{"status": "interrupted"})
		}
	}()
	lockStarted := time.Now()
	m.lock.Lock()
	lockWait := time.Since(lockStarted)
	result := func() error {
		defer m.lock.Unlock()
		return m.addRecorderLocked(spanCtx, live)
	}()
	status := "ok"
	if result != nil {
		status = "error"
	}
	endSpan(diagnostics.Fields{
		"status":       status,
		"error_type":   diagnosticManagerErrorType(result),
		"lock_wait_ms": float64(lockWait) / float64(time.Millisecond),
	})
	spanEnded = true
	return result
}

// addRecorderLocked 是 AddRecorder 的内部实现，调用者必须已持有 m.lock
func (m *manager) addRecorderLocked(ctx context.Context, live live.Live) error {
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
			m.runBackground(m.ctx, func(ctx context.Context) { m.cronRestart(ctx, live) })
		}
	}
	if err := recorder.Start(ctx); err != nil {
		// Start 失败时从 map 删除，并登记可等待的异步 Close。Close 会先等待
		// 当前 operation 结束，再等待该收尾，不会在 clean marker 后遗留 goroutine。
		delete(m.savers, live.GetLiveId())
		m.runBackground(m.ctx, func(context.Context) { recorder.Close() })
		return err
	}
	return nil
}

func (m *manager) cronRestart(ctx context.Context, live live.Live) {
	ticker := time.NewTicker(time.Minute / 4)
	defer ticker.Stop()
	for {
		recorder, err := m.GetRecorder(ctx, live.GetLiveId())
		if err != nil {
			return
		}
		cfg := configs.GetCurrentConfig()
		if cfg == nil || cfg.VideoSplitStrategies.MaxDuration == 0 {
			return
		}
		if time.Since(recorder.StartTime()) >= cfg.VideoSplitStrategies.MaxDuration {
			_ = m.RestartRecorder(ctx, live)
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (m *manager) RestartRecorder(ctx context.Context, live live.Live) error {
	if !m.beginOperation() {
		return ErrManagerClosed
	}
	defer m.endOperation()
	return m.restartRecorder(ctx, live, nil)
}

// restartRecorder 使用可选的 listener owner 令牌保护事件触发的重启。
// owner 在获得 manager 锁后再次校验，防止旧 RoomNameChanged 在新 generation
// 已接管 recorder 后错误重启新实例。
func (m *manager) restartRecorder(
	ctx context.Context,
	live live.Live,
	expectedOwner *recorderOwner,
) error {
	spanCtx, endSpan := diagnostics.StartSpan(ctx, "recorder.manager.restart", diagnostics.Fields{
		"component": "recorder_manager",
		"lane":      "recorder",
	})
	spanEnded := false
	defer func() {
		if !spanEnded {
			endSpan(diagnostics.Fields{"status": "interrupted"})
		}
	}()
	// 1. 在锁内完成 map 操作：取出旧 recorder，创建并放入新 recorder
	// 这样外部观察者（如 LiveEnd 事件处理器）始终能看到录制器存在，不会出现中间状态
	lockStarted := time.Now()
	m.lock.Lock()
	lockWait := time.Since(lockStarted)
	liveID := live.GetLiveId()
	oldRecorder, ok := m.savers[liveID]
	if !ok {
		m.lock.Unlock()
		endSpan(diagnostics.Fields{
			"status":       "not_found",
			"lock_wait_ms": float64(lockWait) / float64(time.Millisecond),
		})
		spanEnded = true
		return ErrRecorderNotExist
	}
	if expectedOwner != nil {
		currentOwner, ownerOK := m.recorderOwners[liveID]
		if !ownerOK || currentOwner != *expectedOwner {
			m.lock.Unlock()
			endSpan(diagnostics.Fields{
				"status":       "stale_owner",
				"lock_wait_ms": float64(lockWait) / float64(time.Millisecond),
			})
			spanEnded = true
			return errRecorderOwnerChanged
		}
	}
	// 从 map 中移除旧 recorder 并立即添加新 recorder，保持锁贯穿整个替换操作
	delete(m.savers, liveID)
	if err := m.addRecorderLocked(spanCtx, live); err != nil {
		// 添加新 recorder 失败，恢复旧 recorder 避免僵尸状态
		m.savers[liveID] = oldRecorder
		m.lock.Unlock()
		endSpan(diagnostics.Fields{
			"status":       "error",
			"error_type":   diagnosticManagerErrorType(err),
			"lock_wait_ms": float64(lockWait) / float64(time.Millisecond),
		})
		spanEnded = true
		return err
	}
	newRec := m.savers[liveID]
	// restartingCount 必须在释放锁之前递增，否则 Unlock 到 Add(1) 之间
	// LiveEnd 可能移除新 recorder 并看到 restartingCount==0，
	// 导致 GetActiveRecordingsCount() 误判为"无活跃录制"触发优雅更新
	m.restartingCount.Add(1)
	m.lock.Unlock()

	// 2. 锁外执行耗时操作：关闭旧 recorder 并获取累积文件
	// restartingCount 保证 CloseForRestart 期间旧 recorder 仍被计入活跃数量
	defer func() {
		m.restartingCount.Add(-1)
		// 收尾完成后检查是否有等待中的优雅更新：
		// 如果 LiveEnd 在 CloseForRestart 期间移除了新 recorder，
		// 那次 CheckGracefulUpdate 会因 restartingCount>0 而跳过，
		// 此处递减后需要再触发一次检查，避免优雅更新永久卡住。
		if onRecordingEndFunc != nil {
			m.runBackground(m.ctx, func(ctx context.Context) { onRecordingEndFunc(ctx) })
		}
	}()
	oldFiles := oldRecorder.CloseForRestart()
	live.GetLogger().Infof("分段重启录制，携带 %d 个历史文件", len(oldFiles))

	// 3. 将旧文件传递给新 recorder（在锁下确认 recorder 仍存在且为预期实例）
	if len(oldFiles) > 0 {
		m.lock.RLock()
		currentRec, stillExists := m.savers[liveID]
		m.lock.RUnlock()
		if stillExists && currentRec == newRec {
			newRec.SetInitialRecordedFiles(oldFiles)
		} else {
			live.GetLogger().Warnf("分段重启时新 recorder 已被移除，跳过 %d 个历史文件传递", len(oldFiles))
		}
	}

	endSpan(diagnostics.Fields{
		"status":               "ok",
		"lock_wait_ms":         float64(lockWait) / float64(time.Millisecond),
		"inherited_file_count": len(oldFiles),
	})
	spanEnded = true
	return nil
}

func (m *manager) RemoveRecorder(ctx context.Context, liveId types.LiveID) error {
	if !m.beginOperation() {
		return ErrManagerClosed
	}
	defer m.endOperation()

	spanCtx, endSpan := diagnostics.StartSpan(ctx, "recorder.manager.remove", diagnostics.Fields{
		"component":     "recorder_manager",
		"lane":          "recorder",
		"live_id_scope": diagnostics.ScopeID(string(liveId)),
	})
	spanEnded := false
	defer func() {
		if !spanEnded {
			endSpan(diagnostics.Fields{"status": "interrupted"})
		}
	}()
	lockStarted := time.Now()
	m.lock.Lock()
	lockWait := time.Since(lockStarted)
	result := func() error {
		defer m.lock.Unlock()
		return m.removeRecorderLocked(spanCtx, liveId)
	}()
	status := "ok"
	if result != nil {
		status = "error"
	}
	endSpan(diagnostics.Fields{
		"status":       status,
		"error_type":   diagnosticManagerErrorType(result),
		"lock_wait_ms": float64(lockWait) / float64(time.Millisecond),
	})
	spanEnded = true
	return result
}

// removeRecorderLocked 是 RemoveRecorder 的内部实现，调用者必须已持有 m.lock
func (m *manager) removeRecorderLocked(ctx context.Context, liveId types.LiveID) error {
	recorder, ok := m.savers[liveId]
	if !ok {
		return ErrRecorderNotExist
	}
	recorder.Close()
	delete(m.savers, liveId)
	delete(m.recorderOwners, liveId)

	// 录制结束后，检查是否有等待中的优雅更新
	if onRecordingEndFunc != nil {
		m.runBackground(m.ctx, func(ctx context.Context) { onRecordingEndFunc(ctx) })
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

// GetActiveRecordingsCount 获取当前活跃的录制数量
// 包含 map 中的录制器和正在执行 CloseForRestart 收尾的旧录制器
func (m *manager) GetActiveRecordingsCount() int {
	m.lock.RLock()
	defer m.lock.RUnlock()
	return len(m.savers) + int(m.restartingCount.Load())
}
