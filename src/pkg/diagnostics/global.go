package diagnostics

import (
	"context"
	"crypto/rand"
	"sync"
)

var (
	defaultMu      sync.RWMutex
	defaultManager *Manager
	fallbackKey    [32]byte
	fallbackOnce   sync.Once
)

// Init 创建新 run 并将其设为全局默认 Manager。前一个 Manager 已关闭后可再次 Init。
func Init(cfg Config) (*Manager, error) {
	defaultMu.Lock()
	defer defaultMu.Unlock()
	if defaultManager != nil && !defaultManager.closed.Load() {
		return nil, ErrAlreadyInitialized
	}
	manager, err := newManager(cfg)
	if err != nil {
		return nil, err
	}
	defaultManager = manager
	return manager, nil
}

// Default 返回全局 Manager；尚未 Init 时返回 nil。关闭后仍保留 Manager 以便管理旧 run。
func Default() *Manager {
	defaultMu.RLock()
	defer defaultMu.RUnlock()
	return defaultManager
}

func cryptoRead(target []byte) (int, error) {
	return rand.Read(target)
}

func globalScopeKey() []byte {
	if manager := Default(); manager != nil {
		return manager.scopeKey
	}
	fallbackOnce.Do(func() {
		_, _ = rand.Read(fallbackKey[:])
	})
	return fallbackKey[:]
}

// RunID 返回全局当前 run ID，未初始化时返回空串。
func RunID() string {
	if manager := Default(); manager != nil {
		return manager.RunID()
	}
	return ""
}

func ListRuns() ([]RunInfo, error) {
	if manager := Default(); manager != nil {
		return manager.ListRuns()
	}
	return nil, ErrNotInitialized
}

func StartupStatus() StartupReport {
	if manager := Default(); manager != nil {
		return manager.StartupStatus()
	}
	return StartupReport{}
}

func Acknowledge(runID string) error {
	if manager := Default(); manager != nil {
		return manager.Acknowledge(runID)
	}
	return ErrNotInitialized
}

func BuildViewerBundle(runID string) (Artifact, error) {
	if manager := Default(); manager != nil {
		return manager.BuildViewerBundle(runID)
	}
	return Artifact{}, ErrNotInitialized
}

func BuildArchive(runID string) (Artifact, error) {
	if manager := Default(); manager != nil {
		return manager.BuildArchive(runID)
	}
	return Artifact{}, ErrNotInitialized
}

func LatestFlightPath(runID string) (Artifact, error) {
	if manager := Default(); manager != nil {
		return manager.LatestFlightPath(runID)
	}
	return Artifact{}, ErrNotInitialized
}

func SnapshotCurrent() (Snapshot, error) {
	if manager := Default(); manager != nil {
		return manager.SnapshotCurrent()
	}
	return Snapshot{}, ErrNotInitialized
}

// Close 正常关闭全局 Manager 并写 clean marker。
func Close() error {
	if manager := Default(); manager != nil {
		return manager.Close()
	}
	return ErrNotInitialized
}

// Abort 关闭全局 Manager，但保留为未 clean 的异常 run。
func Abort() error {
	if manager := Default(); manager != nil {
		return manager.Abort()
	}
	return ErrNotInitialized
}

// RecordPanic 为全局 Manager 写 panic marker。未初始化时安全返回 ErrNotInitialized。
func RecordPanic(ctx context.Context, recovered any) error {
	if manager := Default(); manager != nil {
		return manager.RecordPanic(ctx, recovered)
	}
	return ErrNotInitialized
}

// RecoverPanic 用作 defer helper：持久化当前 panic 后，以原值重新 panic。
// 可选 context 用于继承 task/span 关联。
func RecoverPanic(contexts ...context.Context) {
	recovered := recover()
	if recovered == nil {
		return
	}
	ctx := context.Background()
	if len(contexts) > 0 && contexts[0] != nil {
		ctx = contexts[0]
	}
	func() {
		defer func() {
			_ = recover()
		}()
		_ = RecordPanic(ctx, recovered)
	}()
	panic(recovered)
}
