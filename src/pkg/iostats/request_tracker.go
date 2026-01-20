package iostats

import (
	"context"
	"sync"
	"time"

	"github.com/sirupsen/logrus"
)

// RequestTracker 请求状态追踪器
// 用于记录直播间请求的成功/失败状态
type RequestTracker struct {
	store Store
	mu    sync.Mutex
}

// NewRequestTracker 创建请求追踪器
func NewRequestTracker(store Store) *RequestTracker {
	return &RequestTracker{
		store: store,
	}
}

// RecordSuccess 记录成功的请求
func (t *RequestTracker) RecordSuccess(liveID, platform string) {
	t.record(liveID, platform, true, "")
}

// RecordFailure 记录失败的请求
func (t *RequestTracker) RecordFailure(liveID, platform string, errMsg string) {
	t.record(liveID, platform, false, errMsg)
}

// record 内部记录方法
func (t *RequestTracker) record(liveID, platform string, success bool, errMsg string) {
	t.mu.Lock()
	defer t.mu.Unlock()

	status := &RequestStatus{
		Timestamp:    time.Now().UnixMilli(),
		LiveID:       liveID,
		Platform:     platform,
		Success:      success,
		ErrorMessage: errMsg,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := t.store.SaveRequestStatus(ctx, status); err != nil {
		logrus.WithError(err).WithFields(logrus.Fields{
			"live_id":  liveID,
			"platform": platform,
			"success":  success,
		}).Error("保存请求状态失败")
	}
}

// 全局请求追踪器实例
var (
	globalTracker   *RequestTracker
	globalTrackerMu sync.RWMutex
)

// SetGlobalTracker 设置全局请求追踪器
func SetGlobalTracker(tracker *RequestTracker) {
	globalTrackerMu.Lock()
	defer globalTrackerMu.Unlock()
	globalTracker = tracker
}

// GetGlobalTracker 获取全局请求追踪器
func GetGlobalTracker() *RequestTracker {
	globalTrackerMu.RLock()
	defer globalTrackerMu.RUnlock()
	return globalTracker
}

// TrackRequestSuccess 便捷方法：记录成功的请求
func TrackRequestSuccess(liveID, platform string) {
	if tracker := GetGlobalTracker(); tracker != nil {
		tracker.RecordSuccess(liveID, platform)
	}
}

// TrackRequestFailure 便捷方法：记录失败的请求
func TrackRequestFailure(liveID, platform string, errMsg string) {
	if tracker := GetGlobalTracker(); tracker != nil {
		tracker.RecordFailure(liveID, platform, errMsg)
	}
}
