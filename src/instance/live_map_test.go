package instance

import (
	"fmt"
	"sync"
	"testing"

	"github.com/bililive-go/bililive-go/src/live"
	livemock "github.com/bililive-go/bililive-go/src/live/mock"
	"github.com/bililive-go/bililive-go/src/types"
	gomock "go.uber.org/mock/gomock"
)

// TestLiveMap_ZeroValue 验证 LiveMap 的零值可以安全使用。
// 这保证了 Instance 通过 new() 创建后，无需显式初始化 Lives 字段，
// 启动早期 HTTP handler 和各类 manager 就可以安全调用方法。
func TestLiveMap_ZeroValue(t *testing.T) {
	var lm LiveMap // 零值，未经任何初始化

	t.Run("Len", func(t *testing.T) {
		if lm.Len() != 0 {
			t.Errorf("Len on zero LiveMap should return 0, got %d", lm.Len())
		}
	})

	t.Run("Has", func(t *testing.T) {
		if lm.Has("test") {
			t.Error("Has on zero LiveMap should return false")
		}
	})

	t.Run("Get", func(t *testing.T) {
		v, ok := lm.Get("test")
		if ok || v != nil {
			t.Errorf("Get on zero LiveMap should return (nil, false), got (%v, %v)", v, ok)
		}
	})

	t.Run("Range", func(t *testing.T) {
		called := false
		lm.Range(func(id types.LiveID, l live.Live) bool {
			called = true
			return true
		})
		if called {
			t.Error("Range on zero LiveMap should not call the callback")
		}
	})

	t.Run("Snapshot", func(t *testing.T) {
		snap := lm.Snapshot()
		if snap == nil {
			t.Error("Snapshot on zero LiveMap should return non-nil empty map")
		}
		if len(snap) != 0 {
			t.Errorf("Snapshot on zero LiveMap should return empty map, got %d entries", len(snap))
		}
	})

	t.Run("Delete", func(t *testing.T) {
		lm.Delete("test") // 不应 panic（delete on nil map 在 Go 中是安全的）
	})

	t.Run("SetIfAbsent", func(t *testing.T) {
		if lm.SetIfAbsent("test", nil) != true {
			t.Error("SetIfAbsent on zero LiveMap should succeed (lazy init)")
		}
		// 清理
		lm.Delete("test")
	})
}

// TestLiveMap_BasicOperations 验证基本的 CRUD 操作。
func TestLiveMap_BasicOperations(t *testing.T) {
	var lm LiveMap

	if lm.Len() != 0 {
		t.Fatalf("new LiveMap should be empty, got Len=%d", lm.Len())
	}

	// Set 触发懒初始化
	lm.Set("id1", nil)
	if lm.Len() != 1 {
		t.Fatalf("after Set, Len should be 1, got %d", lm.Len())
	}

	if !lm.Has("id1") {
		t.Error("Has should return true for existing key")
	}

	if lm.Has("id2") {
		t.Error("Has should return false for non-existent key")
	}

	// Delete
	lm.Delete("id1")
	if lm.Len() != 0 {
		t.Fatalf("after Delete, Len should be 0, got %d", lm.Len())
	}

	// SetIfAbsent
	if !lm.SetIfAbsent("id2", nil) {
		t.Error("SetIfAbsent should return true for new key")
	}
	if lm.SetIfAbsent("id2", nil) {
		t.Error("SetIfAbsent should return false for existing key")
	}

	// Snapshot
	snap := lm.Snapshot()
	if len(snap) != 1 {
		t.Errorf("Snapshot should contain 1 entry, got %d", len(snap))
	}
}

// TestLiveMap_ReplaceKeyIfCurrent 验证带对象身份检查的原子替换操作。
func TestLiveMap_ReplaceKeyIfCurrent(t *testing.T) {
	var lm LiveMap
	ctrl := gomock.NewController(t)
	current := livemock.NewMockLive(ctrl)
	unexpected := livemock.NewMockLive(ctrl)
	replacement := livemock.NewMockLive(ctrl)
	occupied := livemock.NewMockLive(ctrl)

	lm.Set("old", current)
	if !lm.Has("old") {
		t.Fatal("old key should exist")
	}

	if lm.ReplaceKeyIfCurrent("old", "new", unexpected, replacement) {
		t.Fatal("expected 不匹配时不应替换")
	}
	lm.Set("new", occupied)
	if lm.ReplaceKeyIfCurrent("old", "new", current, replacement) {
		t.Fatal("new key 已占用时不应替换")
	}
	lm.Delete("new")
	if !lm.ReplaceKeyIfCurrent("old", "new", current, replacement) {
		t.Fatal("expected 匹配且 new key 空闲时应替换")
	}

	if lm.Has("old") {
		t.Error("old key should be removed after ReplaceKeyIfCurrent")
	}
	got, ok := lm.Get("new")
	if !ok || got != replacement {
		t.Error("new key should point to replacement after ReplaceKeyIfCurrent")
	}
	if lm.Len() != 1 {
		t.Errorf("Len should be 1 after ReplaceKeyIfCurrent, got %d", lm.Len())
	}
}

func TestLiveMap_DeleteByRawURL(t *testing.T) {
	var lm LiveMap
	ctrl := gomock.NewController(t)
	first := livemock.NewMockLive(ctrl)
	second := livemock.NewMockLive(ctrl)
	other := livemock.NewMockLive(ctrl)
	first.EXPECT().GetRawUrl().Return("https://example.com/room")
	second.EXPECT().GetRawUrl().Return("https://example.com/room")
	other.EXPECT().GetRawUrl().Return("https://example.com/other")
	lm.Set("initializing", first)
	lm.Set("platform-id", second)
	lm.Set("other", other)

	removed := lm.DeleteByRawURL("https://example.com/room")
	if len(removed) != 2 {
		t.Fatalf("应删除同 URL 的两个对象，实际删除 %d 个", len(removed))
	}
	if lm.Has("initializing") || lm.Has("platform-id") || !lm.Has("other") {
		t.Fatal("DeleteByRawURL 删除了错误的 map 条目")
	}
}

// TestLiveMap_Concurrent 验证 LiveMap 在多 goroutine 并发访问下的线程安全性。
// 需要配合 `go test -race` 运行以触发 Go 的 race detector。
// 这是解决 "concurrent map iteration and map write" panic 的核心测试。
func TestLiveMap_Concurrent(t *testing.T) {
	var lm LiveMap
	const goroutines = 10
	const iterations = 100

	var wg sync.WaitGroup
	wg.Add(goroutines * 5) // 5 种操作

	// 并发 Set
	for g := 0; g < goroutines; g++ {
		go func(g int) {
			defer wg.Done()
			for i := 0; i < iterations; i++ {
				id := types.LiveID(fmt.Sprintf("live-%d-%d", g, i))
				lm.Set(id, nil)
			}
		}(g)
	}

	// 并发 Get
	for g := 0; g < goroutines; g++ {
		go func(g int) {
			defer wg.Done()
			for i := 0; i < iterations; i++ {
				id := types.LiveID(fmt.Sprintf("live-%d-%d", g, i))
				lm.Get(id)
			}
		}(g)
	}

	// 并发 Delete
	for g := 0; g < goroutines; g++ {
		go func(g int) {
			defer wg.Done()
			for i := 0; i < iterations; i++ {
				id := types.LiveID(fmt.Sprintf("live-%d-%d", g, i))
				lm.Delete(id)
			}
		}(g)
	}

	// 并发 Range（读操作与写操作同时进行）
	for g := 0; g < goroutines; g++ {
		go func() {
			defer wg.Done()
			for i := 0; i < iterations; i++ {
				lm.Range(func(id types.LiveID, l live.Live) bool {
					return true
				})
			}
		}()
	}

	// 并发 Snapshot
	for g := 0; g < goroutines; g++ {
		go func() {
			defer wg.Done()
			for i := 0; i < iterations; i++ {
				_ = lm.Snapshot()
			}
		}()
	}

	wg.Wait()
	// 如果没有 panic 或 race condition，测试通过
}

// TestLiveMap_ConcurrentReplaceKey 验证 ReplaceKeyIfCurrent 在并发场景下的原子性。
func TestLiveMap_ConcurrentReplaceKey(t *testing.T) {
	var lm LiveMap
	const goroutines = 10
	const iterations = 50

	// 预置数据
	for i := 0; i < goroutines*iterations; i++ {
		lm.Set(types.LiveID(fmt.Sprintf("old-%d", i)), nil)
	}

	var wg sync.WaitGroup
	wg.Add(goroutines * 2) // ReplaceKey + 并发读

	// 并发 ReplaceKeyIfCurrent
	for g := 0; g < goroutines; g++ {
		go func(g int) {
			defer wg.Done()
			for i := 0; i < iterations; i++ {
				idx := g*iterations + i
				oldID := types.LiveID(fmt.Sprintf("old-%d", idx))
				newID := types.LiveID(fmt.Sprintf("new-%d", idx))
				lm.ReplaceKeyIfCurrent(oldID, newID, nil, nil)
			}
		}(g)
	}

	// 并发 Snapshot（与 ReplaceKey 同时）
	for g := 0; g < goroutines; g++ {
		go func() {
			defer wg.Done()
			for i := 0; i < iterations; i++ {
				snap := lm.Snapshot()
				_ = snap // 只验证不 panic
			}
		}()
	}

	wg.Wait()
}
