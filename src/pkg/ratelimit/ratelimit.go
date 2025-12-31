// Package ratelimit 为每个直播平台提供访问频率限制功能
package ratelimit

import (
	"sync"
	"time"
)

// PlatformRateLimiter 管理各个直播平台的访问频率限制
type PlatformRateLimiter struct {
	limiters map[string]*PlatformLimiter // 平台名称 -> 限制器
	mu       sync.RWMutex                 // 读写锁保护map
}

// PlatformLimiter 单个平台的频率限制器
type PlatformLimiter struct {
	minInterval  time.Duration  // 最小访问间隔
	lastAccess   time.Time      // 上次访问时间
	mu           sync.Mutex     // 保护访问时间的互斥锁
}

var globalRateLimiter = &PlatformRateLimiter{
	limiters: make(map[string]*PlatformLimiter),
}

// GetGlobalRateLimiter 获取全局速率限制器实例
func GetGlobalRateLimiter() *PlatformRateLimiter {
	return globalRateLimiter
}

// SetPlatformLimit 设置或更新指定平台的访问频率限制
func (prl *PlatformRateLimiter) SetPlatformLimit(platform string, intervalSec int) {
	if intervalSec <= 0 {
		// 如果间隔为0或负数，移除该平台的限制
		prl.mu.Lock()
		delete(prl.limiters, platform)
		prl.mu.Unlock()
		return
	}

	interval := time.Duration(intervalSec) * time.Second
	
	prl.mu.Lock()
	defer prl.mu.Unlock()
	
	if limiter, exists := prl.limiters[platform]; exists {
		// 更新现有限制器的间隔
		limiter.mu.Lock()
		limiter.minInterval = interval
		limiter.mu.Unlock()
	} else {
		// 创建新的限制器
		prl.limiters[platform] = &PlatformLimiter{
			minInterval: interval,
			lastAccess:  time.Time{}, // 零值时间，首次访问不会被限制
		}
	}
}

// WaitForPlatform 等待直到允许访问指定平台
// 如果平台没有设置限制，立即返回
func (prl *PlatformRateLimiter) WaitForPlatform(platform string) {
	prl.mu.RLock()
	limiter, exists := prl.limiters[platform]
	prl.mu.RUnlock()
	
	if !exists {
		// 平台没有设置限制，立即返回
		return
	}
	
	limiter.mu.Lock()
	defer limiter.mu.Unlock()
	
	now := time.Now()
	elapsed := now.Sub(limiter.lastAccess)
	
	if elapsed < limiter.minInterval {
		// 需要等待
		waitTime := limiter.minInterval - elapsed
		time.Sleep(waitTime)
		limiter.lastAccess = now.Add(waitTime)
	} else {
		// 已经等待足够长时间，直接更新访问时间
		limiter.lastAccess = now
	}
}

// GetPlatformNextAllowedTime 获取平台下次允许访问的时间
func (prl *PlatformRateLimiter) GetPlatformNextAllowedTime(platform string) time.Time {
	prl.mu.RLock()
	limiter, exists := prl.limiters[platform]
	prl.mu.RUnlock()
	
	if !exists {
		// 没有限制，立即可访问
		return time.Now()
	}
	
	limiter.mu.Lock()
	defer limiter.mu.Unlock()
	
	return limiter.lastAccess.Add(limiter.minInterval)
}

// RemovePlatformLimit 移除指定平台的访问限制
func (prl *PlatformRateLimiter) RemovePlatformLimit(platform string) {
	prl.mu.Lock()
	defer prl.mu.Unlock()
	
	delete(prl.limiters, platform)
}

// GetAllPlatformLimits 获取所有平台的当前限制设置
func (prl *PlatformRateLimiter) GetAllPlatformLimits() map[string]int {
	prl.mu.RLock()
	defer prl.mu.RUnlock()
	
	limits := make(map[string]int)
	for platform, limiter := range prl.limiters {
		limiter.mu.Lock()
		limits[platform] = int(limiter.minInterval.Seconds())
		limiter.mu.Unlock()
	}
	
	return limits
}