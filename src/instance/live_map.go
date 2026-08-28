package instance

import (
	"sync"

	"github.com/bililive-go/bililive-go/src/live"
	"github.com/bililive-go/bililive-go/src/types"
)

// LiveMap 是一个并发安全的 Live 对象 map。
// 解决了 inst.Lives 在 HTTP handler goroutine 中迭代、
// 同时在事件处理器 goroutine 中写入导致的 concurrent map read/write panic。
//
// LiveMap 的零值可直接使用：sync.RWMutex 零值为未锁定状态，
// nil map 的读操作（len、for range、下标访问）在 Go 中是安全的。
// 写操作会在首次调用时懒初始化内部 map。
type LiveMap struct {
	mu sync.RWMutex
	m  map[types.LiveID]live.Live
}

// initLocked 在持有写锁的情况下懒初始化内部 map。
func (lm *LiveMap) initLocked() {
	if lm.m == nil {
		lm.m = make(map[types.LiveID]live.Live)
	}
}

// Get 根据 LiveID 获取 Live 对象。
func (lm *LiveMap) Get(id types.LiveID) (live.Live, bool) {
	lm.mu.RLock()
	defer lm.mu.RUnlock()
	v, ok := lm.m[id]
	return v, ok
}

// Set 设置一个 Live 对象。
func (lm *LiveMap) Set(id types.LiveID, l live.Live) {
	lm.mu.Lock()
	defer lm.mu.Unlock()
	lm.initLocked()
	lm.m[id] = l
}

// Delete 删除一个 Live 对象。
func (lm *LiveMap) Delete(id types.LiveID) {
	lm.mu.Lock()
	defer lm.mu.Unlock()
	delete(lm.m, id)
}

// Has 检查是否存在指定 LiveID。
func (lm *LiveMap) Has(id types.LiveID) bool {
	lm.mu.RLock()
	defer lm.mu.RUnlock()
	_, ok := lm.m[id]
	return ok
}

// Len 返回 map 中元素的数量。
func (lm *LiveMap) Len() int {
	lm.mu.RLock()
	defer lm.mu.RUnlock()
	return len(lm.m)
}

// Range 遍历 map 中的所有元素。
// 回调函数返回 false 时停止遍历。
//
// 警告：在回调函数执行期间持有读锁。回调中不得调用 Set、Delete、
// ReplaceKey 等写操作，否则会导致死锁（sync.RWMutex 不可重入）。
// 如果需要在遍历过程中修改 map，请使用 Snapshot() 获取快照后再操作。
func (lm *LiveMap) Range(fn func(id types.LiveID, l live.Live) bool) {
	lm.mu.RLock()
	defer lm.mu.RUnlock()
	for id, l := range lm.m {
		if !fn(id, l) {
			break
		}
	}
}

// Snapshot 返回 map 的一个浅拷贝快照。
// 适用于需要长时间处理的场景（避免长时间持有读锁），
// 或者需要在遍历过程中修改 LiveMap 的场景。
func (lm *LiveMap) Snapshot() map[types.LiveID]live.Live {
	lm.mu.RLock()
	defer lm.mu.RUnlock()
	snapshot := make(map[types.LiveID]live.Live, len(lm.m))
	for id, l := range lm.m {
		snapshot[id] = l
	}
	return snapshot
}

// SetIfAbsent 如果 key 不存在则设置，返回是否设置成功。
func (lm *LiveMap) SetIfAbsent(id types.LiveID, l live.Live) bool {
	lm.mu.Lock()
	defer lm.mu.Unlock()
	if _, ok := lm.m[id]; ok {
		return false
	}
	lm.initLocked()
	lm.m[id] = l
	return true
}

// ReplaceKeyIfCurrent 仅当 oldID 仍指向 expected 时，才原子地删除旧 key 并设置新 key。
// 用于 InitializingLive 完成初始化后替换 LiveID 的场景，避免已删除房间的迟到回调
// 把房间重新插回 map。若 newID 已被其他 Live 占用，也会拒绝覆盖。
func (lm *LiveMap) ReplaceKeyIfCurrent(oldID, newID types.LiveID, expected, replacement live.Live) bool {
	lm.mu.Lock()
	defer lm.mu.Unlock()
	current, ok := lm.m[oldID]
	if !ok || current != expected {
		return false
	}
	if newID != oldID {
		if _, exists := lm.m[newID]; exists {
			return false
		}
	}
	delete(lm.m, oldID)
	lm.m[newID] = replacement
	return true
}

// DeleteIfCurrent 仅当 id 仍指向 expected 时才删除，避免删除并发替换后的新对象。
func (lm *LiveMap) DeleteIfCurrent(id types.LiveID, expected live.Live) bool {
	lm.mu.Lock()
	defer lm.mu.Unlock()
	current, ok := lm.m[id]
	if !ok || current != expected {
		return false
	}
	delete(lm.m, id)
	return true
}

// DeleteByRawURL 原子删除所有匹配原始 URL 的 Live，并返回被删除对象供调用方在锁外关闭。
// 删除房间时 LiveID 可能正从初始化 ID 切换为平台 ID，因此不能只删除调用方最初看到的 key。
func (lm *LiveMap) DeleteByRawURL(rawURL string) []live.Live {
	lm.mu.Lock()
	defer lm.mu.Unlock()
	removed := make([]live.Live, 0, 1)
	for id, current := range lm.m {
		if current != nil && current.GetRawUrl() == rawURL {
			delete(lm.m, id)
			removed = append(removed, current)
		}
	}
	return removed
}
