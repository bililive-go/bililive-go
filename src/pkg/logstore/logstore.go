package logstore

import (
	"container/ring"
	"sync"
	"time"

	"github.com/bililive-go/bililive-go/src/types"
)

// LogEntry 表示单个日志条目
type LogEntry struct {
	Timestamp time.Time `json:"timestamp"`
	Level     string    `json:"level"`
	Message   string    `json:"message"`
	LiveID    types.LiveID `json:"live_id"`
}

// LogStore 为每个直播间存储日志
type LogStore struct {
	mu    sync.RWMutex
	logs  map[types.LiveID]*ring.Ring // 每个直播间的环形缓冲区
	size  int                         // 每个直播间保存的最大日志条数
}

// New 创建新的日志存储
func New(maxLines int) *LogStore {
	return &LogStore{
		logs: make(map[types.LiveID]*ring.Ring),
		size: maxLines,
	}
}

// AddLog 添加日志条目到指定直播间
func (ls *LogStore) AddLog(liveID types.LiveID, level, message string) {
	ls.mu.Lock()
	defer ls.mu.Unlock()

	entry := &LogEntry{
		Timestamp: time.Now(),
		Level:     level,
		Message:   message,
		LiveID:    liveID,
	}

	// 获取或创建该直播间的环形缓冲区
	r, exists := ls.logs[liveID]
	if !exists {
		r = ring.New(ls.size)
		ls.logs[liveID] = r
	}

	r.Value = entry
	ls.logs[liveID] = r.Next()
}

// GetLogs 获取指定直播间的日志
func (ls *LogStore) GetLogs(liveID types.LiveID, maxLines int) []LogEntry {
	ls.mu.RLock()
	defer ls.mu.RUnlock()

	r, exists := ls.logs[liveID]
	if !exists {
		return []LogEntry{}
	}

	var logs []LogEntry
	count := 0
	
	// 从当前位置向前遍历环形缓冲区
	r.Do(func(val interface{}) {
		if val != nil && count < maxLines {
			if entry, ok := val.(*LogEntry); ok {
				logs = append(logs, *entry)
				count++
			}
		}
	})

	// 按时间倒序排列（最新的在前面）
	for i := 0; i < len(logs)/2; i++ {
		logs[i], logs[len(logs)-1-i] = logs[len(logs)-1-i], logs[i]
	}

	return logs
}

// GetAllLogs 获取所有直播间的日志
func (ls *LogStore) GetAllLogs() map[types.LiveID][]LogEntry {
	ls.mu.RLock()
	defer ls.mu.RUnlock()

	result := make(map[types.LiveID][]LogEntry)
	for liveID := range ls.logs {
		result[liveID] = ls.GetLogs(liveID, ls.size)
	}
	return result
}

// ClearLogs 清除指定直播间的日志
func (ls *LogStore) ClearLogs(liveID types.LiveID) {
	ls.mu.Lock()
	defer ls.mu.Unlock()
	delete(ls.logs, liveID)
}

// SetMaxLines 设置最大日志行数
func (ls *LogStore) SetMaxLines(maxLines int) {
	ls.mu.Lock()
	defer ls.mu.Unlock()
	ls.size = maxLines
	// 注意：已存在的环形缓冲区不会自动调整大小
}