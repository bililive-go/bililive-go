package listeners

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"sync"
	"sync/atomic"
	"time"

	"github.com/bililive-go/bililive-go/src/configs"
	"github.com/bililive-go/bililive-go/src/live"
	"github.com/bililive-go/bililive-go/src/pkg/diagnostics"
)

var listenerGenerationCounters sync.Map // map[lifecycle_scope_key]*atomic.Uint64

func nextListenerGeneration(lifecycleScopeKey string) uint64 {
	counterValue, _ := listenerGenerationCounters.LoadOrStore(lifecycleScopeKey, &atomic.Uint64{})
	return counterValue.(*atomic.Uint64).Add(1)
}

func listenerTraceIdentity(liveObj live.Live) (string, string, string, uint64) {
	// generation 也是并发正确性的一部分，不能依赖 diagnostics 是否启用。
	// 原始 URL 只用于生成会话 ScopeID 和进程内哈希键，不会写入事件或业务轨迹。
	rawURL := liveObj.GetRawUrl()
	roomScopeID := diagnostics.ScopeID(rawURL)
	lifecycleScopeKey := listenerLifecycleScopeKey(rawURL)
	listenerID := diagnostics.NewID("listener")
	generation := nextListenerGeneration(lifecycleScopeKey)
	return roomScopeID, lifecycleScopeKey, listenerID, generation
}

func listenerLifecycleScopeKey(rawURL string) string {
	sum := sha256.Sum256([]byte("bililive-go/listener-room/v1\x00" + rawURL))
	return "listener_room_" + hex.EncodeToString(sum[:])
}

func listenerBaseFields(roomScopeID, listenerID string, generation uint64) diagnostics.Fields {
	return diagnostics.Fields{
		"component":            "listener",
		"lane":                 "listener",
		"room_scope_id":        roomScopeID,
		"listener_instance_id": listenerID,
		"generation":           generation,
	}
}

func managerTraceContext(ctx context.Context, operation string, liveObj live.Live) (context.Context, diagnostics.Fields) {
	if ctx == nil {
		ctx = context.Background()
	}
	fields := diagnostics.Fields{
		"component": "listener_manager",
		"lane":      "listener",
		"operation": operation,
	}
	if diagnostics.Default() != nil {
		// 原始 URL 只在此处用于生成会话内 HMAC。
		roomScopeID := diagnostics.ScopeID(liveObj.GetRawUrl())
		fields["room_scope_id"] = roomScopeID
	}
	return diagnostics.WithFields(ctx, fields), fields
}

func listenerStateName(value uint32) string {
	switch value {
	case begin:
		return "begin"
	case pending:
		return "pending"
	case running:
		return "running"
	case stopped:
		return "stopped"
	default:
		return "unknown"
	}
}

func durationMilliseconds(duration time.Duration) float64 {
	return float64(duration) / float64(time.Millisecond)
}

func managerOperationStatus(result string) string {
	switch result {
	case "started", "removed", "replaced":
		return "ok"
	default:
		return "error"
	}
}

func configuredPollIntervalMilliseconds(liveObj live.Live) (float64, bool) {
	// diagnostics 关闭时不为观测数据额外调用 Live/配置解析逻辑。
	if diagnostics.Default() == nil {
		return 0, false
	}
	if provider, ok := liveObj.(live.SchedulerStatusProvider); ok {
		if seconds := provider.GetSchedulerStatus().IntervalSeconds; seconds > 0 {
			return float64(time.Duration(seconds) * time.Second / time.Millisecond), true
		}
	}
	if cfg := configs.GetCurrentConfig(); cfg != nil && cfg.Interval > 0 {
		return float64(time.Duration(cfg.Interval) * time.Second / time.Millisecond), true
	}
	return 0, false
}

func mergeDiagnosticFields(base, extra diagnostics.Fields) diagnostics.Fields {
	result := make(diagnostics.Fields, len(base)+len(extra))
	for key, value := range base {
		result[key] = value
	}
	for key, value := range extra {
		result[key] = value
	}
	return result
}
