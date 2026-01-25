package ratelimit

import (
	"testing"
	"time"
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
