package missevan

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"runtime"
	"testing"
	"time"

	"github.com/hr3lxphr6j/requests"
)

// TestResponseBodyLeak_Issue1078 验证 HTTP Response Body 泄漏导致的内存增长
//
// 背景：
//   - requests 库的 Response.Bytes()/Text()/JSON() 方法内部有 defer r.Body.Close()
//   - 但 missevan（以及其他所有平台）在 resp.StatusCode != 200 时直接 return，
//     不调用上述方法，导致 Body 永远不会被关闭
//   - 未关闭的 Body 会阻止 Go http.Transport 复用 TCP 连接
//   - 在"大量房间 × 持续返回错误"的场景下，连接不断泄漏，内存持续增长
//
// 本测试模拟场景：
//   - 用 httptest 启动一个服务器，对 /api/v2/live/* 始终返回 404
//   - 对比泄漏模式（不关闭 Body）和正常模式（关闭 Body）的内存差异
//   - 泄漏模式下 3000 次请求可导致 ~74MB Heap 增长和 ~9000 goroutine 泄漏
//
// 参考 Issue: https://github.com/bililive-go/bililive-go/issues/1078
func TestResponseBodyLeak_Issue1078(t *testing.T) {

	// 模拟远程 API 服务器：始终返回 404 + 一些响应体数据
	// 在真实场景中，这是猫耳/B站等平台的 API 返回非 200 的情况
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 返回 404 和一些响应体（模拟真实的错误响应）
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte(`{"code":-1,"msg":"room not exists","data":null}`))
	}))
	defer server.Close()

	session := requests.NewSession(server.Client())

	// == 阶段 1：泄漏模式（复现当前 bug） ==
	t.Run("LeakyPattern", func(t *testing.T) {
		runtime.GC()
		var m1 runtime.MemStats
		runtime.ReadMemStats(&m1)
		goroutinesBefore := runtime.NumGoroutine()

		const iterations = 3000

		for i := 0; i < iterations; i++ {
			// 完全复现 missevan.go:53-58 的代码模式
			resp, err := session.Get(server.URL + "/api/v2/live/12345")
			if err != nil {
				t.Fatalf("请求失败: %v", err)
			}
			if resp.StatusCode != http.StatusOK {
				// BUG：直接 return，不关闭 Body
				// 这正是 missevan.go:57-58 的代码
				_ = err // 模拟 return nil, live.ErrRoomNotExist
				continue
			}
			// 这行在 404 场景下永远不会执行
			resp.Bytes()
		}

		runtime.GC()
		// 等待 GC 和 finalizer 一小段时间
		time.Sleep(200 * time.Millisecond)
		runtime.GC()

		var m2 runtime.MemStats
		runtime.ReadMemStats(&m2)
		goroutinesAfter := runtime.NumGoroutine()

		// 计算内存增长
		heapGrowthBytes := int64(m2.HeapInuse) - int64(m1.HeapInuse)
		heapGrowthMB := float64(heapGrowthBytes) / 1024 / 1024
		goroutineGrowth := goroutinesAfter - goroutinesBefore

		t.Logf("泄漏模式(%d 次请求):", iterations)
		t.Logf("  Heap 增长: %.2f MB (%d bytes)", heapGrowthMB, heapGrowthBytes)
		t.Logf("  Goroutine 增长: %d (之前=%d, 之后=%d)",
			goroutineGrowth, goroutinesBefore, goroutinesAfter)
		t.Logf("  Sys 内存: %.2f MB",
			float64(m2.Sys)/1024/1024)

		// 断言：泄漏模式下应该能看到显著的内存增长
		// 3000 次请求 + 未关闭 Body → ~70+MB heap 增长, ~9000 goroutine 增长
		if heapGrowthMB < 10 {
			t.Errorf("预期泄漏模式下 Heap 增长 >10MB，实际 %.2f MB — 泄漏可能已被修复", heapGrowthMB)
		}
		if goroutineGrowth < 100 {
			t.Errorf("预期泄漏模式下 goroutine 增长 >100，实际 %d — 泄漏可能已被修复", goroutineGrowth)
		}
	})

	// == 阶段 2：修复后模式（关闭 Body） ==
	t.Run("FixedPattern", func(t *testing.T) {
		runtime.GC()
		var m1 runtime.MemStats
		runtime.ReadMemStats(&m1)
		goroutinesBefore := runtime.NumGoroutine()

		const iterations = 3000

		for i := 0; i < iterations; i++ {
			resp, err := session.Get(server.URL + "/api/v2/live/12345")
			if err != nil {
				t.Fatalf("请求失败: %v", err)
			}
			if resp.StatusCode != http.StatusOK {
				// FIX：关闭 Body 后再 return
				resp.Body.Close()
				continue
			}
			resp.Bytes()
		}

		runtime.GC()
		time.Sleep(200 * time.Millisecond)
		runtime.GC()

		var m2 runtime.MemStats
		runtime.ReadMemStats(&m2)
		goroutinesAfter := runtime.NumGoroutine()

		heapGrowthBytes := int64(m2.HeapInuse) - int64(m1.HeapInuse)
		heapGrowthMB := float64(heapGrowthBytes) / 1024 / 1024
		goroutineGrowth := goroutinesAfter - goroutinesBefore

		t.Logf("修复模式(%d 次请求):", iterations)
		t.Logf("  Heap 增长: %.2f MB (%d bytes)", heapGrowthMB, heapGrowthBytes)

		// 断言：修复模式下不应该有显著的内存/goroutine 增长
		if heapGrowthMB > 5 {
			t.Errorf("修复模式下 Heap 增长不应超过 5MB，实际 %.2f MB", heapGrowthMB)
		}
		if goroutineGrowth > 50 {
			t.Errorf("修复模式下 goroutine 增长不应超过 50，实际 %d", goroutineGrowth)
		}
		t.Logf("  Goroutine 增长: %d (之前=%d, 之后=%d)",
			goroutineGrowth, goroutinesBefore, goroutinesAfter)
		t.Logf("  Sys 内存: %.2f MB",
			float64(m2.Sys)/1024/1024)
	})
}

// TestResponseBodyLeak_GoroutineAccumulation 测试 goroutine 累积
// 在真实场景中，未关闭的 response body 会阻止连接归还到连接池，
// 导致 transport 层为每个请求创建新连接，间接增加 goroutine 数量
func TestResponseBodyLeak_GoroutineAccumulation(t *testing.T) {
	// 使用独立的 HTTP server，确保不复用其他测试的连接
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		// 写入较大的响应体，让 Body 的影响更明显
		body := make([]byte, 4096)
		for i := range body {
			body[i] = 'x'
		}
		w.Write(body)
	}))
	defer server.Close()

	// 使用自定义 Transport 让效果更明显
	transport := &http.Transport{
		MaxIdleConns:        100,
		MaxIdleConnsPerHost: 100,
		DisableKeepAlives:   false, // 开启 keep-alive
	}
	client := &http.Client{Transport: transport}
	session := requests.NewSession(client)

	runtime.GC()
	goroutinesBefore := runtime.NumGoroutine()

	const iterations = 500

	for i := 0; i < iterations; i++ {
		resp, err := session.Get(server.URL + "/api/v2/live/room1")
		if err != nil {
			t.Fatalf("第 %d 次请求失败: %v", i, err)
		}
		if resp.StatusCode != http.StatusOK {
			// 不关闭 Body — 复现 bug
			continue
		}
		resp.Bytes()
	}

	// 等一小段时间让 goroutine 稳定
	time.Sleep(500 * time.Millisecond)
	goroutinesAfterLeak := runtime.NumGoroutine()

	t.Logf("泄漏模式: goroutine 增长 = %d (之前=%d, 之后=%d)",
		goroutinesAfterLeak-goroutinesBefore,
		goroutinesBefore, goroutinesAfterLeak)

	// 现在用关闭 Body 的方式做同样的事
	transport2 := &http.Transport{
		MaxIdleConns:        100,
		MaxIdleConnsPerHost: 100,
		DisableKeepAlives:   false,
	}
	client2 := &http.Client{Transport: transport2}
	session2 := requests.NewSession(client2)

	runtime.GC()
	goroutinesBefore2 := runtime.NumGoroutine()

	for i := 0; i < iterations; i++ {
		resp, err := session2.Get(server.URL + "/api/v2/live/room1")
		if err != nil {
			t.Fatalf("第 %d 次请求失败: %v", i, err)
		}
		if resp.StatusCode != http.StatusOK {
			// 正确关闭 Body
			resp.Body.Close()
			continue
		}
		resp.Bytes()
	}

	time.Sleep(500 * time.Millisecond)
	goroutinesAfterFixed := runtime.NumGoroutine()

	t.Logf("修复模式: goroutine 增长 = %d (之前=%d, 之后=%d)",
		goroutinesAfterFixed-goroutinesBefore2,
		goroutinesBefore2, goroutinesAfterFixed)

	leakGrowth := goroutinesAfterLeak - goroutinesBefore
	fixedGrowth := goroutinesAfterFixed - goroutinesBefore2

	t.Logf("对比: 泄漏=%d goroutines, 修复=%d goroutines, 差异=%d",
		leakGrowth, fixedGrowth, leakGrowth-fixedGrowth)

	// 泄漏模式应该显著多于修复模式
	// 注意：httptest.Server 使用 localhost 连接，影响可能没有远程连接那么大
	// 但差异应该仍然可观
	if leakGrowth > 10 && fixedGrowth < leakGrowth/2 {
		t.Logf("✅ 结果符合预期：泄漏模式 goroutine 增长 (%d) 明显多于修复模式 (%d)",
			leakGrowth, fixedGrowth)
	}
}

// TestResponseBodyLeak_SimulateMultiRoom 模拟多房间并发轮询场景
// 这是最接近用户真实使用场景的测试
func TestResponseBodyLeak_SimulateMultiRoom(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte(`{"code":-1,"msg":"room not exists"}`))
	}))
	defer server.Close()

	const (
		numRooms     = 50                    // 模拟 50 个直播间
		pollInterval = 10 * time.Millisecond // 加速轮询（真实场景 20 秒）
		testDuration = 3 * time.Second       // 测试持续 3 秒
	)

	t.Run("LeakyMultiRoom", func(t *testing.T) {
		runMultiRoomTest(t, server.URL, numRooms, pollInterval, testDuration, false)
	})

	// 等待上一个子测试的残留 goroutine 清理
	time.Sleep(1 * time.Second)
	runtime.GC()

	t.Run("FixedMultiRoom", func(t *testing.T) {
		runMultiRoomTest(t, server.URL, numRooms, pollInterval, testDuration, true)
	})
}

func runMultiRoomTest(t *testing.T, serverURL string, numRooms int, pollInterval, duration time.Duration, closeBody bool) {
	t.Helper()

	runtime.GC()
	time.Sleep(100 * time.Millisecond)
	runtime.GC()

	var m1 runtime.MemStats
	runtime.ReadMemStats(&m1)
	goroutinesBefore := runtime.NumGoroutine()

	// 为每个房间创建独立的 session（模拟真实场景）
	sessions := make([]*requests.Session, numRooms)
	for i := 0; i < numRooms; i++ {
		sessions[i] = requests.NewSession(&http.Client{
			Transport: &http.Transport{
				MaxIdleConns:        10,
				MaxIdleConnsPerHost: 10,
			},
		})
	}

	done := make(chan struct{})
	requestCount := make([]int, numRooms)

	// 每个房间一个 goroutine 进行轮询（模拟 listener.run()）
	for i := 0; i < numRooms; i++ {
		go func(roomIdx int) {
			ticker := time.NewTicker(pollInterval)
			defer ticker.Stop()

			roomID := fmt.Sprintf("room_%d", roomIdx)

			for {
				select {
				case <-done:
					return
				case <-ticker.C:
					resp, err := sessions[roomIdx].Get(serverURL + "/api/v2/live/" + roomID)
					if err != nil {
						continue
					}
					requestCount[roomIdx]++

					if resp.StatusCode != http.StatusOK {
						if closeBody {
							// 修复：关闭 Body
							resp.Body.Close()
						}
						// 不关闭 Body — 复现 bug
						continue
					}
					resp.Bytes()
				}
			}
		}(i)
	}

	// 等待测试时长
	time.Sleep(duration)
	close(done)
	time.Sleep(200 * time.Millisecond) // 等待 goroutine 退出

	runtime.GC()
	time.Sleep(200 * time.Millisecond)
	runtime.GC()

	var m2 runtime.MemStats
	runtime.ReadMemStats(&m2)
	goroutinesAfter := runtime.NumGoroutine()

	// 统计总请求数
	totalRequests := 0
	for _, count := range requestCount {
		totalRequests += count
	}

	heapGrowthBytes := int64(m2.HeapInuse) - int64(m1.HeapInuse)
	heapGrowthMB := float64(heapGrowthBytes) / 1024 / 1024
	goroutineGrowth := goroutinesAfter - goroutinesBefore

	mode := "泄漏模式"
	if closeBody {
		mode = "修复模式"
	}

	t.Logf("%s (%d 房间, %d 总请求):", mode, numRooms, totalRequests)
	t.Logf("  Heap 增长: %.2f MB", heapGrowthMB)
	t.Logf("  Goroutine 增长: %d (之前=%d, 之后=%d)",
		goroutineGrowth, goroutinesBefore, goroutinesAfter)
	t.Logf("  HeapInuse: %.2f MB -> %.2f MB",
		float64(m1.HeapInuse)/1024/1024, float64(m2.HeapInuse)/1024/1024)
	t.Logf("  HeapObjects: %d -> %d (增长: %d)",
		m1.HeapObjects, m2.HeapObjects, m2.HeapObjects-m1.HeapObjects)
	t.Logf("  总 GC 暂停: %v", time.Duration(m2.PauseTotalNs))
}
