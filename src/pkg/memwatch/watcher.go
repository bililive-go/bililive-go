// Package memwatch 提供内存使用监控和异常检测功能
// 定期采样内存状态，检测异常增长趋势，通过回调通知上层
package memwatch

import (
	"sync"
	"time"

	"github.com/bililive-go/bililive-go/src/pkg/memstats"
	bilisentry "github.com/bililive-go/bililive-go/src/pkg/sentry"
	"github.com/bililive-go/bililive-go/src/pkg/utils"
	"github.com/sirupsen/logrus"
)

const (
	// 默认采样间隔 5 分钟
	defaultSampleInterval = 5 * time.Minute
	// 默认窗口大小：12 个采样点 = 1 小时（@ 5 分钟间隔）
	defaultWindowSize = 12
	// 默认增长率阈值：1.5 倍认为异常
	defaultGrowthRatioThreshold = 1.5
	// 默认绝对增长阈值：100 MB
	defaultAbsoluteGrowthThresholdMB = 100.0
	// 默认告警冷却时间 6 小时
	defaultAlertCooldown = 6 * time.Hour
	// 快照最大保留数量
	maxSnapshotCount = 288 // 24 小时 @ 5 分钟间隔
)

// MemorySnapshot 内存快照
type MemorySnapshot struct {
	Timestamp       int64   `json:"ts"`
	GoAllocMB       float64 `json:"go_alloc_mb"`
	GoSysMB         float64 `json:"go_sys_mb"`
	NumGC           uint32  `json:"num_gc"`
	NumGoroutine    int     `json:"num_goroutine"`
	ConnCounterSize int     `json:"conn_counter_size"`
	ContainerMB     float64 `json:"container_mb,omitempty"`
}

// AlertInfo 内存警告信息
type AlertInfo struct {
	CurrentAllocMB float64 `json:"current_alloc_mb"`
	GrowthRatio    float64 `json:"growth_ratio"`
	Message        string  `json:"message"`
}

// AlertCallback 告警回调函数类型
type AlertCallback func(alert AlertInfo)

// Config 监控器配置
type Config struct {
	SampleInterval            time.Duration
	WindowSize                int
	GrowthRatioThreshold      float64
	AbsoluteGrowthThresholdMB float64
	AlertCooldown             time.Duration
}

// DefaultConfig 返回默认配置
func DefaultConfig() Config {
	return Config{
		SampleInterval:            defaultSampleInterval,
		WindowSize:                defaultWindowSize,
		GrowthRatioThreshold:      defaultGrowthRatioThreshold,
		AbsoluteGrowthThresholdMB: defaultAbsoluteGrowthThresholdMB,
		AlertCooldown:             defaultAlertCooldown,
	}
}

// Watcher 内存监控器
type Watcher struct {
	config Config
	stopCh chan struct{}
	wg     sync.WaitGroup

	// 快照存储（环形缓冲）
	snapshotsMu sync.RWMutex
	snapshots   []MemorySnapshot

	// 告警状态
	alertCallback AlertCallback
	lastAlertTime time.Time
}

// New 创建内存监控器
func New(config Config) *Watcher {
	return &Watcher{
		config:    config,
		stopCh:    make(chan struct{}),
		snapshots: make([]MemorySnapshot, 0, defaultWindowSize*2),
	}
}

// SetAlertCallback 设置告警回调
func (w *Watcher) SetAlertCallback(cb AlertCallback) {
	w.alertCallback = cb
}

// Start 启动监控
func (w *Watcher) Start() {
	w.wg.Add(1)
	bilisentry.Go(w.run)
	logrus.Info("内存监控器已启动")
}

// Stop 停止监控
func (w *Watcher) Stop() {
	close(w.stopCh)
	w.wg.Wait()
	logrus.Info("内存监控器已停止")
}

// GetSnapshots 获取所有快照（线程安全）
func (w *Watcher) GetSnapshots() []MemorySnapshot {
	w.snapshotsMu.RLock()
	defer w.snapshotsMu.RUnlock()
	result := make([]MemorySnapshot, len(w.snapshots))
	copy(result, w.snapshots)
	return result
}

// run 运行采样循环
func (w *Watcher) run() {
	defer w.wg.Done()
	defer bilisentry.Recover()

	ticker := time.NewTicker(w.config.SampleInterval)
	defer ticker.Stop()

	// 启动时立即采一次
	w.sample()

	for {
		select {
		case <-w.stopCh:
			return
		case <-ticker.C:
			w.sample()
		}
	}
}

// sample 执行一次内存采样并检查趋势
func (w *Watcher) sample() {
	snapshot := w.collectSnapshot()

	w.snapshotsMu.Lock()
	w.snapshots = append(w.snapshots, snapshot)
	// 保持快照数量在限制内
	if len(w.snapshots) > maxSnapshotCount {
		w.snapshots = w.snapshots[len(w.snapshots)-maxSnapshotCount:]
	}
	snapshotsCopy := make([]MemorySnapshot, len(w.snapshots))
	copy(snapshotsCopy, w.snapshots)
	w.snapshotsMu.Unlock()

	// 检查内存趋势
	w.checkTrend(snapshotsCopy)
}

// collectSnapshot 收集当前内存快照
func (w *Watcher) collectSnapshot() MemorySnapshot {
	selfMem := memstats.GetSelfMemory()

	snapshot := MemorySnapshot{
		Timestamp:       time.Now().UnixMilli(),
		GoAllocMB:       float64(selfMem.Alloc) / (1024 * 1024),
		GoSysMB:         float64(selfMem.Sys) / (1024 * 1024),
		NumGC:           selfMem.NumGC,
		NumGoroutine:    selfMem.NumGoroutine,
		ConnCounterSize: utils.ConnCounterManager.GetMapSize(),
	}

	// 如果在容器中，尝试获取容器内存
	if memstats.IsInContainer() {
		if containerMem, err := memstats.GetContainerMemory(); err == nil && containerMem != nil {
			snapshot.ContainerMB = float64(containerMem.Used) / (1024 * 1024)
		}
	}

	return snapshot
}

// checkTrend 检查内存趋势是否异常
func (w *Watcher) checkTrend(snapshots []MemorySnapshot) {
	if w.alertCallback == nil {
		return
	}

	windowSize := w.config.WindowSize
	if len(snapshots) < windowSize*2 {
		// 数据点不够两个窗口大小，暂不检测
		return
	}

	// 检查冷却期
	if !w.lastAlertTime.IsZero() && time.Since(w.lastAlertTime) < w.config.AlertCooldown {
		return
	}

	alert := DetectAbnormalGrowth(snapshots, windowSize, w.config.GrowthRatioThreshold, w.config.AbsoluteGrowthThresholdMB)
	if alert != nil {
		w.lastAlertTime = time.Now()
		logrus.WithFields(logrus.Fields{
			"current_alloc_mb": alert.CurrentAllocMB,
			"growth_ratio":     alert.GrowthRatio,
		}).Warn("检测到内存异常增长")
		w.alertCallback(*alert)
	}
}
