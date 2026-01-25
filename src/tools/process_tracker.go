package tools

import (
	"sync"
)

// ProcessCategory 进程类别，用于按程序名称聚合统计
type ProcessCategory string

const (
	// ProcessCategoryFFmpeg FFmpeg 进程（录制时的解码器）
	ProcessCategoryFFmpeg ProcessCategory = "ffmpeg"
	// ProcessCategoryBTools bililive-tools 进程
	ProcessCategoryBTools ProcessCategory = "bililive-tools"
	// ProcessCategoryKlive klive 进程
	ProcessCategoryKlive ProcessCategory = "klive"
	// ProcessCategoryRecorder BililiveRecorder 修复进程
	ProcessCategoryRecorder ProcessCategory = "bililive-recorder"
	// ProcessCategoryOther 其他进程
	ProcessCategoryOther ProcessCategory = "other"
)

// ProcessInfo 存储进程的基本信息
type ProcessInfo struct {
	PID      int             // 进程 ID
	Name     string          // 进程唯一标识名称
	Category ProcessCategory // 进程类别（用于聚合统计）
}

// processTracker 用于跟踪所有通过 tools 包启动的子进程
type processTracker struct {
	mu        sync.RWMutex
	processes map[string]ProcessInfo // key: 进程唯一标识名称
}

var tracker = &processTracker{
	processes: make(map[string]ProcessInfo),
}

// RegisterProcess 注册一个子进程
// name: 进程的唯一标识名称
// pid: 进程 ID
// category: 进程类别（用于聚合统计）
func RegisterProcess(name string, pid int, category ...ProcessCategory) {
	tracker.mu.Lock()
	defer tracker.mu.Unlock()

	cat := ProcessCategoryOther
	if len(category) > 0 {
		cat = category[0]
	}

	tracker.processes[name] = ProcessInfo{
		PID:      pid,
		Name:     name,
		Category: cat,
	}
}

// UnregisterProcess 取消注册一个子进程
func UnregisterProcess(name string) {
	tracker.mu.Lock()
	defer tracker.mu.Unlock()
	delete(tracker.processes, name)
}

// GetAllProcessPIDs 获取所有已注册子进程的 PID 列表
func GetAllProcessPIDs() []int {
	tracker.mu.RLock()
	defer tracker.mu.RUnlock()

	pids := make([]int, 0, len(tracker.processes))
	for _, info := range tracker.processes {
		if info.PID > 0 {
			pids = append(pids, info.PID)
		}
	}
	return pids
}

// GetAllProcessInfo 获取所有已注册子进程的详细信息
func GetAllProcessInfo() []ProcessInfo {
	tracker.mu.RLock()
	defer tracker.mu.RUnlock()

	infos := make([]ProcessInfo, 0, len(tracker.processes))
	for _, info := range tracker.processes {
		infos = append(infos, info)
	}
	return infos
}

// GetProcessPID 获取指定名称进程的 PID，如果未注册则返回 0
func GetProcessPID(name string) int {
	tracker.mu.RLock()
	defer tracker.mu.RUnlock()

	if info, ok := tracker.processes[name]; ok {
		return info.PID
	}
	return 0
}

// GetProcessesByCategory 获取指定类别的所有进程
func GetProcessesByCategory(category ProcessCategory) []ProcessInfo {
	tracker.mu.RLock()
	defer tracker.mu.RUnlock()

	var infos []ProcessInfo
	for _, info := range tracker.processes {
		if info.Category == category {
			infos = append(infos, info)
		}
	}
	return infos
}

// GetPIDsByCategory 获取指定类别的所有进程 PID
func GetPIDsByCategory(category ProcessCategory) []int {
	tracker.mu.RLock()
	defer tracker.mu.RUnlock()

	var pids []int
	for _, info := range tracker.processes {
		if info.Category == category && info.PID > 0 {
			pids = append(pids, info.PID)
		}
	}
	return pids
}
