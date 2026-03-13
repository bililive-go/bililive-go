package memwatch

import "fmt"

// DetectAbnormalGrowth 使用滑动窗口对比检测内存异常增长
//
// 将快照分为 "前窗口" 和 "后窗口" 两段，对比两段的 GoAllocMB 均值。
// 如果后窗口均值 > 前窗口均值 × growthRatioThreshold 或绝对增长 > absThresholdMB
// 则判定为异常增长，返回 AlertInfo。
func DetectAbnormalGrowth(
	snapshots []MemorySnapshot,
	windowSize int,
	growthRatioThreshold float64,
	absThresholdMB float64,
) *AlertInfo {
	if len(snapshots) < windowSize*2 {
		return nil
	}

	// 前窗口：倒数第 2*windowSize 到倒数第 windowSize+1
	// 后窗口：最后 windowSize 个
	total := len(snapshots)
	prevWindow := snapshots[total-windowSize*2 : total-windowSize]
	currWindow := snapshots[total-windowSize:]

	prevAvg := averageAlloc(prevWindow)
	currAvg := averageAlloc(currWindow)

	if prevAvg <= 0 {
		return nil
	}

	growthRatio := currAvg / prevAvg
	absoluteGrowth := currAvg - prevAvg

	// 两个条件满足其一即告警
	if growthRatio >= growthRatioThreshold || absoluteGrowth >= absThresholdMB {
		return &AlertInfo{
			CurrentAllocMB: currAvg,
			GrowthRatio:    growthRatio,
			Message:        fmt.Sprintf("内存从 %.1f MB 增长到 %.1f MB（%.1f 倍），可能存在内存泄漏", prevAvg, currAvg, growthRatio),
		}
	}

	return nil
}

// averageAlloc 计算快照中 GoAllocMB 的均值
func averageAlloc(snapshots []MemorySnapshot) float64 {
	if len(snapshots) == 0 {
		return 0
	}
	var sum float64
	for _, s := range snapshots {
		sum += s.GoAllocMB
	}
	return sum / float64(len(snapshots))
}
