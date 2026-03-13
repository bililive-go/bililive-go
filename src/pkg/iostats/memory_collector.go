package iostats

import (
	"context"
	"time"

	"github.com/bililive-go/bililive-go/src/pkg/memstats"
	bilisentry "github.com/bililive-go/bililive-go/src/pkg/sentry"
	"github.com/bililive-go/bililive-go/src/recorders"
	"github.com/bililive-go/bililive-go/src/tools"
	"github.com/sirupsen/logrus"
)

// MemoryCollector 内存统计收集器
type MemoryCollector struct {
	store    Store
	interval time.Duration
	stopCh   chan struct{}
}

// NewMemoryCollector 创建内存收集器
func NewMemoryCollector(store Store, intervalSeconds int) *MemoryCollector {
	return &MemoryCollector{
		store:    store,
		interval: time.Duration(intervalSeconds) * time.Second,
		stopCh:   make(chan struct{}),
	}
}

// Start 启动内存收集器
func (c *MemoryCollector) Start() {
	bilisentry.Go(c.run)
	logrus.Info("内存统计收集器已启动")
}

// Stop 停止内存收集器
func (c *MemoryCollector) Stop() {
	close(c.stopCh)
	logrus.Info("内存统计收集器已停止")
}

// run 运行收集循环
func (c *MemoryCollector) run() {
	// 捕获子 goroutine 的 panic
	defer bilisentry.Recover()

	ticker := time.NewTicker(c.interval)
	defer ticker.Stop()

	// 启动时先收集一次
	c.collect()

	for {
		select {
		case <-c.stopCh:
			return
		case <-ticker.C:
			c.collect()
		}
	}
}

// RecorderManagerProvider 录制器管理器提供者
type RecorderManagerProvider func() recorders.Manager

// LauncherPIDProvider 启动器 PID 提供者
type LauncherPIDProvider func() int

var (
	recorderManagerProvider RecorderManagerProvider
	launcherPIDProvider     LauncherPIDProvider
)

// SetRecorderManagerProvider 设置录制器管理器提供者
func SetRecorderManagerProvider(provider RecorderManagerProvider) {
	recorderManagerProvider = provider
}

// SetLauncherPIDProvider 设置启动器 PID 提供者
func SetLauncherPIDProvider(provider LauncherPIDProvider) {
	launcherPIDProvider = provider
}

// collect 执行一次内存数据采集
func (c *MemoryCollector) collect() {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	timestamp := time.Now().UnixMilli()
	var stats []*MemoryStat

	// 1. 收集主进程（Go 运行时）内存
	selfMem := memstats.GetSelfMemory()
	stats = append(stats, &MemoryStat{
		Timestamp:    timestamp,
		Category:     MemoryCategorySelf,
		RSS:          selfMem.RSS, // 使用 gopsutil 获取的真实物理内存 (RSS)
		VMS:          selfMem.VMS,
		Alloc:        selfMem.Alloc,
		Sys:          selfMem.Sys,
		NumGC:        selfMem.NumGC,
		NumGoroutine: selfMem.NumGoroutine,
	})

	totalRSS := selfMem.RSS

	// 2. 收集 tools 管理的子进程内存（按类别聚合）
	toolsProcesses := tools.GetAllProcessInfo()
	categoryRSS := make(map[string]uint64)
	categoryVMS := make(map[string]uint64)

	for _, proc := range toolsProcesses {
		if proc.PID <= 0 {
			continue
		}
		procMem, err := memstats.GetProcessMemory(proc.PID)
		if err != nil {
			continue
		}

		category := string(proc.Category)
		categoryRSS[category] += procMem.RSS
		categoryVMS[category] += procMem.VMS
		totalRSS += procMem.RSS
	}

	// 保存各类别的聚合内存
	for category, rss := range categoryRSS {
		stats = append(stats, &MemoryStat{
			Timestamp: timestamp,
			Category:  category,
			RSS:       rss,
			VMS:       categoryVMS[category],
		})
	}

	// 3. 收集 FFmpeg 子进程内存（来自 RecorderManager）
	if recorderManagerProvider != nil {
		if rm := recorderManagerProvider(); rm != nil {
			ffmpegPIDs := rm.GetAllParserPIDs()
			var ffmpegRSS, ffmpegVMS uint64
			for _, pid := range ffmpegPIDs {
				if pid <= 0 {
					continue
				}
				procMem, err := memstats.GetProcessMemory(pid)
				if err != nil {
					continue
				}
				ffmpegRSS += procMem.RSS
				ffmpegVMS += procMem.VMS
				totalRSS += procMem.RSS
			}

			if ffmpegRSS > 0 {
				stats = append(stats, &MemoryStat{
					Timestamp: timestamp,
					Category:  MemoryCategoryFFmpeg,
					RSS:       ffmpegRSS,
					VMS:       ffmpegVMS,
				})
			}
		}
	}

	// 4. 收集 launcher 进程内存（如果由启动器管理）
	if launcherPIDProvider != nil {
		if launcherPID := launcherPIDProvider(); launcherPID > 0 {
			if procMem, err := memstats.GetProcessMemory(launcherPID); err == nil {
				stats = append(stats, &MemoryStat{
					Timestamp: timestamp,
					Category:  MemoryCategoryLauncher,
					RSS:       procMem.RSS,
					VMS:       procMem.VMS,
				})
				totalRSS += procMem.RSS
			}
		}
	}

	// 5. 收集容器内存（仅 Linux 容器环境）
	if memstats.IsInContainer() {
		containerMem, err := memstats.GetContainerMemory()
		if err == nil && containerMem != nil {
			stats = append(stats, &MemoryStat{
				Timestamp: timestamp,
				Category:  MemoryCategoryContainer,
				RSS:       containerMem.Used,
			})
			// 在容器环境中，使用 cgroup 报告的内存作为“总内存”
			// cgroup memory.current 包含了容器内所有进程的实际内存使用
			// 比简单累加各进程 RSS 更准确（避免共享内存重复计算）
			if containerMem.Used > 0 {
				totalRSS = containerMem.Used
			}
		}
	}

	// 6. 记录总内存
	stats = append(stats, &MemoryStat{
		Timestamp: timestamp,
		Category:  MemoryCategoryTotal,
		RSS:       totalRSS,
	})

	// 保存统计数据
	if len(stats) > 0 {
		if err := c.store.SaveMemoryStats(ctx, stats); err != nil {
			logrus.WithError(err).Error("保存内存统计数据失败")
		}
	}
}
