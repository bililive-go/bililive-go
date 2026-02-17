package tools

import (
	"os"
	"sync"
	"time"

	blog "github.com/bililive-go/bililive-go/src/log"
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

// KillAllProcesses 终止所有已注册的子进程（包括其子进程树）并等待它们退出。
// 在进入 launcher 模式前调用，确保端口被释放以供新版本使用。
//
// 使用 killProcessTree 而非 p.Kill() 来杀掉整个进程树，
// 避免循环依赖：子进程的子进程持有管道 → cmd.Wait() 等管道关闭 →
// Job Object 等 cmd.Wait() 返回 → 子进程的子进程等 Job Object 关闭。
func KillAllProcesses() {
	logger := blog.GetLogger()
	allProcs := GetAllProcessInfo()
	for _, proc := range allProcs {
		if proc.PID > 0 {
			logger.Infof("正在终止子进程树 %s (PID: %d)", proc.Name, proc.PID)
			killStart := time.Now()
			if err := killProcessTree(proc.PID); err != nil {
				logger.Warnf("终止子进程树 %s (PID: %d) 失败: %v，尝试直接 Kill", proc.Name, proc.PID, err)
				// 回退到直接 Kill
				if p, err := os.FindProcess(proc.PID); err == nil {
					p.Kill()
				}
			}

			// 等待进程真正退出（最长 5 秒），确保管道句柄被释放
			p, err := os.FindProcess(proc.PID)
			if err == nil {
				done := make(chan struct{})
				go func() {
					p.Wait()
					close(done)
				}()
				select {
				case <-done:
					logger.Infof("子进程 %s (PID: %d) 已退出，耗时 %v", proc.Name, proc.PID, time.Since(killStart))
				case <-time.After(5 * time.Second):
					logger.Warnf("等待子进程 %s (PID: %d) 退出超时（5秒），继续", proc.Name, proc.PID)
				}
			}
			UnregisterProcess(proc.Name)
		}
	}
}
