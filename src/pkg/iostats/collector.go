package iostats

import (
	"context"
	"sync"
	"time"

	bilisentry "github.com/bililive-go/bililive-go/src/pkg/sentry"
	"github.com/bililive-go/bililive-go/src/pkg/utils"
	"github.com/sirupsen/logrus"
)

// RecorderStatusProvider 录制器状态提供者接口
// 由外部设置，用于获取录制器状态
type RecorderStatusProvider func() []RecorderStatus

// RecorderStatus 录制器状态
type RecorderStatus struct {
	LiveID    string
	Platform  string
	TotalSize int64
	FileSize  int64
}

// 全局录制器状态提供者
var (
	recorderStatusProvider     RecorderStatusProvider
	recorderStatusProviderLock sync.RWMutex
)

// SetRecorderStatusProvider 设置录制器状态提供者
func SetRecorderStatusProvider(provider RecorderStatusProvider) {
	recorderStatusProviderLock.Lock()
	defer recorderStatusProviderLock.Unlock()
	recorderStatusProvider = provider
}

// getRecorderStatuses 获取录制器状态
func getRecorderStatuses() []RecorderStatus {
	recorderStatusProviderLock.RLock()
	defer recorderStatusProviderLock.RUnlock()
	if recorderStatusProvider == nil {
		return nil
	}
	return recorderStatusProvider()
}

// Collector IO 统计收集器
type Collector struct {
	store    Store
	interval time.Duration
	stopCh   chan struct{}
	wg       sync.WaitGroup

	// 上一次采集时的网络字节数，用于计算速度
	lastNetworkBytes     map[string]int64
	lastNetworkBytesLock sync.RWMutex

	// 上一次采集时的录制字节数
	lastRecordBytes     map[string]int64
	lastRecordBytesLock sync.RWMutex

	// 上一次采集时间
	lastCollectTime time.Time

	// 磁盘 I/O 采集器
	diskIOCollector *DiskIOCollector
}

// NewCollector 创建收集器
func NewCollector(store Store, intervalSeconds int) *Collector {
	return &Collector{
		store:            store,
		interval:         time.Duration(intervalSeconds) * time.Second,
		stopCh:           make(chan struct{}),
		lastNetworkBytes: make(map[string]int64),
		lastRecordBytes:  make(map[string]int64),
		lastCollectTime:  time.Now(),
		diskIOCollector:  NewDiskIOCollector(),
	}
}

// Start 启动收集器
func (c *Collector) Start() {
	c.wg.Add(1)
	bilisentry.Go(c.run)
	logrus.Info("IO 统计收集器已启动")
}

// Stop 停止收集器
func (c *Collector) Stop() {
	close(c.stopCh)
	c.wg.Wait()
	logrus.Info("IO 统计收集器已停止")
}

// run 运行收集循环
func (c *Collector) run() {
	defer c.wg.Done()
	// 捕获子 goroutine 的 panic
	defer bilisentry.Recover()

	ticker := time.NewTicker(c.interval)
	defer ticker.Stop()

	for {
		select {
		case <-c.stopCh:
			return
		case <-ticker.C:
			c.collect()
		}
	}
}

// collect 执行一次数据采集
func (c *Collector) collect() {
	ctx := context.Background()
	now := time.Now()
	timestamp := now.UnixMilli()
	elapsed := now.Sub(c.lastCollectTime).Seconds()
	if elapsed <= 0 {
		elapsed = 1
	}
	c.lastCollectTime = now

	var stats []*IOStat

	// 1. 收集网络下载数据（来自 ConnCounterManager）
	networkStats := c.collectNetworkStats(timestamp, elapsed)
	stats = append(stats, networkStats...)

	// 2. 收集录制写入数据（来自活跃的 Recorder）
	recordStats := c.collectRecordStats(timestamp, elapsed)
	stats = append(stats, recordStats...)

	// 3. 保存统计数据
	if len(stats) > 0 {
		if err := c.store.SaveIOStats(ctx, stats); err != nil {
			logrus.WithError(err).Error("保存 IO 统计数据失败")
		}
	}

	// 4. 收集并保存系统磁盘 I/O 数据
	if c.diskIOCollector != nil {
		diskIOStats, err := c.diskIOCollector.Collect()
		if err != nil {
			logrus.WithError(err).Debug("采集磁盘 I/O 数据失败")
		} else if len(diskIOStats) > 0 {
			if err := c.store.SaveDiskIOStats(ctx, diskIOStats); err != nil {
				logrus.WithError(err).Error("保存磁盘 I/O 统计数据失败")
			}
		}
	}
}

// collectNetworkStats 收集网络统计数据
func (c *Collector) collectNetworkStats(timestamp int64, elapsed float64) []*IOStat {
	allStats := utils.ConnCounterManager.GetAllStats()

	var stats []*IOStat
	var totalBytes int64
	var totalSpeed int64

	c.lastNetworkBytesLock.Lock()
	defer c.lastNetworkBytesLock.Unlock()

	for _, connStat := range allStats {
		// 计算速度
		lastBytes, exists := c.lastNetworkBytes[connStat.Host]
		currentBytes := connStat.ReceivedBytes

		var speed int64
		if exists && currentBytes > lastBytes {
			speed = int64(float64(currentBytes-lastBytes) / elapsed)
		}

		c.lastNetworkBytes[connStat.Host] = currentBytes
		totalBytes += currentBytes
		totalSpeed += speed
	}

	// 记录全局网络统计
	if totalBytes > 0 || totalSpeed > 0 {
		stats = append(stats, &IOStat{
			Timestamp:  timestamp,
			StatType:   StatTypeNetworkDownload,
			LiveID:     "", // 全局
			Platform:   "",
			Speed:      totalSpeed,
			TotalBytes: totalBytes,
		})
	}

	return stats
}

// collectRecordStats 收集录制统计数据
func (c *Collector) collectRecordStats(timestamp int64, elapsed float64) []*IOStat {
	recorderStatuses := getRecorderStatuses()
	if len(recorderStatuses) == 0 {
		return nil
	}

	var stats []*IOStat
	var totalWriteSpeed int64

	c.lastRecordBytesLock.Lock()
	defer c.lastRecordBytesLock.Unlock()

	for _, rs := range recorderStatuses {
		// 使用 TotalSize 或 FileSize
		currentBytes := rs.TotalSize
		if currentBytes == 0 {
			currentBytes = rs.FileSize
		}

		key := rs.LiveID
		lastBytes, exists := c.lastRecordBytes[key]

		var speed int64
		if exists && currentBytes > lastBytes {
			speed = int64(float64(currentBytes-lastBytes) / elapsed)
		}

		c.lastRecordBytes[key] = currentBytes
		totalWriteSpeed += speed

		// 记录单个直播间的统计
		if speed > 0 {
			stats = append(stats, &IOStat{
				Timestamp:  timestamp,
				StatType:   StatTypeDiskRecordWrite,
				LiveID:     key,
				Platform:   rs.Platform,
				Speed:      speed,
				TotalBytes: currentBytes,
			})
		}
	}

	// 记录全局录制写入速度
	if totalWriteSpeed > 0 {
		stats = append(stats, &IOStat{
			Timestamp: timestamp,
			StatType:  StatTypeDiskRecordWrite,
			LiveID:    "", // 全局
			Platform:  "",
			Speed:     totalWriteSpeed,
		})
	}

	return stats
}
