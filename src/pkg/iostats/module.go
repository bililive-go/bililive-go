package iostats

import (
	"context"
	"sync"
	"time"

	"github.com/bililive-go/bililive-go/src/interfaces"
	bilisentry "github.com/bililive-go/bililive-go/src/pkg/sentry"
	"github.com/sirupsen/logrus"
)

// Module IO 统计模块
type Module struct {
	store           *SQLiteStore
	collector       *Collector
	memoryCollector *MemoryCollector
	tracker         *RequestTracker
	config          Config
	stopCh          chan struct{}
	cleanupWg       sync.WaitGroup
}

// NewModule 创建 IO 统计模块
func NewModule(ctx context.Context, config Config) (interfaces.Module, error) {
	if !config.Enabled {
		logrus.Info("IO 统计模块已禁用")
		return &disabledModule{}, nil
	}

	// 创建存储
	store, err := NewSQLiteStore(GetDefaultDBPath())
	if err != nil {
		return nil, err
	}

	// 创建收集器（不再需要 instance）
	collector := NewCollector(store, config.CollectInterval)

	// 创建内存收集器
	memoryCollector := NewMemoryCollector(store, config.CollectInterval)

	// 创建请求追踪器
	tracker := NewRequestTracker(store)
	SetGlobalTracker(tracker)

	return &Module{
		store:           store,
		collector:       collector,
		memoryCollector: memoryCollector,
		tracker:         tracker,
		config:          config,
		stopCh:          make(chan struct{}),
	}, nil
}

// Start 启动模块
func (m *Module) Start(ctx context.Context) error {
	// 启动收集器
	m.collector.Start()

	// 启动内存收集器
	m.memoryCollector.Start()

	// 启动定期清理任务
	m.cleanupWg.Add(1)
	bilisentry.Go(m.runCleanup)

	logrus.Info("IO 统计模块已启动")
	return nil
}

// Close 关闭模块
func (m *Module) Close(ctx context.Context) {
	close(m.stopCh)

	// 停止收集器
	m.collector.Stop()

	// 停止内存收集器
	m.memoryCollector.Stop()

	// 等待清理任务结束
	m.cleanupWg.Wait()

	// 关闭存储
	if err := m.store.Close(); err != nil {
		logrus.WithError(err).Error("关闭 IO 统计存储失败")
	}

	// 清除全局追踪器
	SetGlobalTracker(nil)

	logrus.Info("IO 统计模块已关闭")
}

// GetStore 获取存储实例
func (m *Module) GetStore() Store {
	return m.store
}

// GetTracker 获取请求追踪器
func (m *Module) GetTracker() *RequestTracker {
	return m.tracker
}

// runCleanup 运行定期清理任务
func (m *Module) runCleanup() {
	defer m.cleanupWg.Done()

	// 每天凌晨 3 点执行清理
	ticker := time.NewTicker(1 * time.Hour)
	defer ticker.Stop()

	// 启动时先执行一次清理
	m.doCleanup()

	for {
		select {
		case <-m.stopCh:
			return
		case <-ticker.C:
			now := time.Now()
			// 只在凌晨 3 点到 4 点之间执行
			if now.Hour() == 3 {
				m.doCleanup()
			}
		}
	}
}

// doCleanup 执行清理
func (m *Module) doCleanup() {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	if err := m.store.Cleanup(ctx, m.config.RetentionDays); err != nil {
		logrus.WithError(err).Error("清理过期 IO 统计数据失败")
	} else {
		logrus.WithField("retention_days", m.config.RetentionDays).Info("已清理过期 IO 统计数据")
	}
}

// disabledModule 禁用状态的模块
type disabledModule struct{}

func (d *disabledModule) Start(ctx context.Context) error {
	return nil
}

func (d *disabledModule) Close(ctx context.Context) {
}

// RecordTaskIOStats 记录任务 IO 统计（用于 FLV 修复和 MP4 转换）
func RecordTaskIOStats(statType StatType, liveID, platform string, speed, totalBytes int64) {
	tracker := GetGlobalTracker()
	if tracker == nil || tracker.store == nil {
		return
	}

	stat := &IOStat{
		Timestamp:  time.Now().UnixMilli(),
		StatType:   statType,
		LiveID:     liveID,
		Platform:   platform,
		Speed:      speed,
		TotalBytes: totalBytes,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := tracker.store.SaveIOStat(ctx, stat); err != nil {
		logrus.WithError(err).WithFields(logrus.Fields{
			"stat_type": statType,
			"live_id":   liveID,
		}).Error("保存任务 IO 统计失败")
	}
}
