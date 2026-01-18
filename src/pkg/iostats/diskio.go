// Package iostats 磁盘 I/O 采集器
package iostats

import (
	"sync"
	"time"

	"github.com/shirou/gopsutil/v3/disk"
)

// DiskIOCollector 系统级磁盘 I/O 采集器
type DiskIOCollector struct {
	lastCounters    map[string]disk.IOCountersStat
	lastCollectTime time.Time
	mu              sync.RWMutex
}

// NewDiskIOCollector 创建磁盘 I/O 采集器
func NewDiskIOCollector() *DiskIOCollector {
	return &DiskIOCollector{
		lastCounters:    make(map[string]disk.IOCountersStat),
		lastCollectTime: time.Now(),
	}
}

// Collect 采集一次磁盘 I/O 数据
// 返回采样周期内各磁盘的 I/O 统计
func (c *DiskIOCollector) Collect() ([]*DiskIOStat, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	now := time.Now()
	timestamp := now.UnixMilli()
	elapsed := now.Sub(c.lastCollectTime).Seconds()
	if elapsed <= 0 {
		elapsed = 1
	}

	// 获取当前磁盘 I/O 计数器
	counters, err := disk.IOCounters()
	if err != nil {
		return nil, err
	}

	var stats []*DiskIOStat

	for deviceName, current := range counters {
		// 获取上次采集的数据
		last, exists := c.lastCounters[deviceName]
		if !exists {
			// 第一次采集，只保存数据不计算
			c.lastCounters[deviceName] = current
			continue
		}

		// 计算采样周期内的差值
		deltaReadCount := current.ReadCount - last.ReadCount
		deltaWriteCount := current.WriteCount - last.WriteCount
		deltaReadBytes := current.ReadBytes - last.ReadBytes
		deltaWriteBytes := current.WriteBytes - last.WriteBytes
		deltaReadTime := current.ReadTime - last.ReadTime    // 毫秒
		deltaWriteTime := current.WriteTime - last.WriteTime // 毫秒

		// 如果没有任何 I/O 活动，跳过
		if deltaReadCount == 0 && deltaWriteCount == 0 {
			c.lastCounters[deviceName] = current
			continue
		}

		stat := &DiskIOStat{
			Timestamp:   timestamp,
			DeviceName:  deviceName,
			ReadCount:   deltaReadCount,
			WriteCount:  deltaWriteCount,
			ReadBytes:   deltaReadBytes,
			WriteBytes:  deltaWriteBytes,
			ReadTimeMs:  deltaReadTime,
			WriteTimeMs: deltaWriteTime,
		}

		// 计算平均延迟（毫秒/次）
		if deltaReadCount > 0 {
			stat.AvgReadLatency = float64(deltaReadTime) / float64(deltaReadCount)
		}
		if deltaWriteCount > 0 {
			stat.AvgWriteLatency = float64(deltaWriteTime) / float64(deltaWriteCount)
		}

		// 计算速度（bytes/s）
		stat.ReadSpeed = int64(float64(deltaReadBytes) / elapsed)
		stat.WriteSpeed = int64(float64(deltaWriteBytes) / elapsed)

		stats = append(stats, stat)

		// 更新缓存
		c.lastCounters[deviceName] = current
	}

	c.lastCollectTime = now
	return stats, nil
}

// GetDevices 获取所有可用的磁盘设备名
func (c *DiskIOCollector) GetDevices() []string {
	counters, err := disk.IOCounters()
	if err != nil {
		return nil
	}

	devices := make([]string, 0, len(counters))
	for name := range counters {
		devices = append(devices, name)
	}
	return devices
}
