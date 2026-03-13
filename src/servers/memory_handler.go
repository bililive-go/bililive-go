package servers

import (
	"encoding/json"
	"net/http"

	"github.com/bililive-go/bililive-go/src/pkg/memwatch"
)

// memoryWatcher 全局内存监控器实例
var memoryWatcher *memwatch.Watcher

// SetMemoryWatcher 设置全局内存监控器实例
func SetMemoryWatcher(w *memwatch.Watcher) {
	memoryWatcher = w
}

// getMemorySnapshots 获取内存快照列表
// GET /api/memory/snapshots
func getMemorySnapshots(w http.ResponseWriter, r *http.Request) {
	if memoryWatcher == nil {
		http.Error(w, "内存监控器未初始化", http.StatusServiceUnavailable)
		return
	}

	snapshots := memoryWatcher.GetSnapshots()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"snapshots": snapshots,
		"count":     len(snapshots),
	})
}
