package ratelimit

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestPlatformRateLimiter(t *testing.T) {
	limiter := GetGlobalRateLimiter()

	// 设置测试平台限制：2秒间隔
	limiter.SetPlatformLimit("test_platform", 2)

	// 第一次访问应该立即通过
	start := time.Now()
	limiter.WaitForPlatform("test_platform")
	elapsed1 := time.Since(start)

	if elapsed1 > 100*time.Millisecond {
		t.Errorf("First access should be immediate, took %v", elapsed1)
	}

	// 第二次访问应该等待约2秒
	start = time.Now()
	limiter.WaitForPlatform("test_platform")
	elapsed2 := time.Since(start)

	if elapsed2 < 1900*time.Millisecond || elapsed2 > 2100*time.Millisecond {
		t.Errorf("Second access should wait ~2s, took %v", elapsed2)
	}

	// 测试没有限制的平台应该立即通过
	start = time.Now()
	limiter.WaitForPlatform("unlimited_platform")
	elapsed3 := time.Since(start)

	if elapsed3 > 100*time.Millisecond {
		t.Errorf("Unlimited platform access should be immediate, took %v", elapsed3)
	}

	// 清理
	limiter.RemovePlatformLimit("test_platform")
}

func TestPlatformRateLimiterUpdate(t *testing.T) {
	limiter := GetGlobalRateLimiter()

	// 设置初始限制
	limiter.SetPlatformLimit("update_test", 3)

	// 更新限制
	limiter.SetPlatformLimit("update_test", 1)

	// 第一次访问应该立即通过
	limiter.WaitForPlatform("update_test")

	// 验证新的限制生效
	start := time.Now()
	limiter.WaitForPlatform("update_test")
	elapsed := time.Since(start)

	if elapsed < 900*time.Millisecond || elapsed > 1100*time.Millisecond {
		t.Errorf("Updated limit should wait ~1s, took %v", elapsed)
	}

	// 清理
	limiter.RemovePlatformLimit("update_test")
}

func TestConfigSyncRateLimits(t *testing.T) {
	// 这个测试需要配置系统的支持，暂时跳过具体实现
	t.Skip("Config sync test requires full config system")
}

func TestAcquirePlatformSerializesInFlightRequests(t *testing.T) {
	limiter := &PlatformRateLimiter{
		limiters: map[string]*PlatformLimiter{
			"test": {
				inFlight: make(chan struct{}, 1),
			},
		},
	}

	releaseFirst, ok := limiter.AcquirePlatformWithContext(context.Background(), "test")
	if !ok {
		t.Fatal("首次请求未获取到平台许可")
	}

	secondAcquired := make(chan func(), 1)
	go func() {
		release, acquired := limiter.AcquirePlatformWithContext(context.Background(), "test")
		if acquired {
			secondAcquired <- release
		}
	}()

	select {
	case release := <-secondAcquired:
		release()
		t.Fatal("首个请求未释放时，第二个同平台请求不应进入")
	case <-time.After(50 * time.Millisecond):
	}

	releaseFirst()
	select {
	case release := <-secondAcquired:
		release()
	case <-time.After(time.Second):
		t.Fatal("首个请求释放后，第二个请求未获取平台许可")
	}
}

func TestAcquirePlatformCancellationWhileWaitingForInFlight(t *testing.T) {
	limiter := &PlatformRateLimiter{
		limiters: map[string]*PlatformLimiter{
			"test": {
				inFlight: make(chan struct{}, 1),
			},
		},
	}
	releaseFirst, ok := limiter.AcquirePlatformWithContext(context.Background(), "test")
	if !ok {
		t.Fatal("首次请求未获取到平台许可")
	}
	defer releaseFirst()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	releaseSecond, acquired := limiter.AcquirePlatformWithContext(ctx, "test")
	if acquired || releaseSecond != nil {
		t.Fatal("等待在途槽位超时后不应获得平台许可")
	}

	// 取消的等待者不能消费或释放首个请求持有的槽位。
	if occupied := len(limiter.limiters["test"].inFlight); occupied != 1 {
		t.Fatalf("首个请求尚未 release，在途槽位占用数 = %d，期望 1", occupied)
	}
}

func TestEnsurePlatformLimitDoesNotOverrideExplicitLimit(t *testing.T) {
	limiter := &PlatformRateLimiter{limiters: make(map[string]*PlatformLimiter)}
	limiter.SetPlatformLimit("test", 5)
	limiter.EnsurePlatformLimit("test", 1)

	if got := limiter.GetAllPlatformLimits()["test"]; got != 5 {
		t.Fatalf("兜底限制覆盖了显式配置：得到 %d 秒，期望 5 秒", got)
	}
}

func TestPlatformRateLimiterDiagnosticCountersOnGrant(t *testing.T) {
	state := &PlatformLimiter{minInterval: 15 * time.Millisecond}
	limiter := &PlatformRateLimiter{
		limiters: map[string]*PlatformLimiter{"diagnostic_grant": state},
	}

	assert.True(t, limiter.WaitForPlatformWithContext(context.Background(), "diagnostic_grant"))
	startedAt := time.Now()
	assert.True(t, limiter.WaitForPlatformWithContext(context.Background(), "diagnostic_grant"))

	assert.GreaterOrEqual(t, time.Since(startedAt), 10*time.Millisecond)
	assert.Equal(t, uint64(2), state.grantSeq.Load())
	assert.GreaterOrEqual(t, state.rechecks.Load(), uint64(1))
	assert.Equal(t, int64(0), state.waiters.Load())
	assert.GreaterOrEqual(t, state.peakWaiters.Load(), int64(1))
}

func TestPlatformRateLimiterDiagnosticCountersOnCancellation(t *testing.T) {
	state := &PlatformLimiter{
		minInterval: time.Hour,
		lastAccess:  time.Now(),
	}
	limiter := &PlatformRateLimiter{
		limiters: map[string]*PlatformLimiter{"diagnostic_cancel": state},
	}

	const waiterCount = 3
	cancels := make([]context.CancelFunc, 0, waiterCount)
	results := make(chan bool, waiterCount)
	for range waiterCount {
		ctx, cancel := context.WithCancel(context.Background())
		cancels = append(cancels, cancel)
		go func() {
			results <- limiter.WaitForPlatformWithContext(ctx, "diagnostic_cancel")
		}()
	}

	assert.Eventually(t, func() bool {
		return state.waiters.Load() == waiterCount
	}, time.Second, time.Millisecond)
	for _, cancel := range cancels {
		cancel()
	}
	for range waiterCount {
		select {
		case granted := <-results:
			assert.False(t, granted)
		case <-time.After(time.Second):
			t.Fatal("等待限流取消结果超时")
		}
	}

	assert.Eventually(t, func() bool {
		return state.waiters.Load() == 0
	}, time.Second, time.Millisecond)
	assert.GreaterOrEqual(t, state.peakWaiters.Load(), int64(waiterCount))
	assert.Equal(t, uint64(0), state.grantSeq.Load())
}
