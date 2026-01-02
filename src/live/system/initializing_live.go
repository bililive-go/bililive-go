package system

import (
	"net/url"
	"sync"

	"github.com/bililive-go/bililive-go/src/live"
	"github.com/bililive-go/bililive-go/src/live/internal"
)

func init() {
	live.InitializingLiveBuilderInstance = new(builder)
}

type builder struct{}

func (b *builder) Build(originalLive live.Live, url *url.URL) (live.Live, error) {
	return &InitializingLive{
		BaseLive:     internal.NewBaseLive(url),
		OriginalLive: originalLive,
	}, nil
}

type InitializingLive struct {
	internal.BaseLive
	OriginalLive live.Live

	// 初始化完成回调（使用 live 包定义的类型）
	onFinished live.InitializingFinishedCallback
	// 用于标记初始化是否已完成（防止重复触发回调）
	finished bool
	// 用于保护并发访问
	mu sync.Mutex
}

// SetOnFinished 设置初始化完成时的回调函数
// 当 GetInfo() 成功获取到真实信息时会调用此回调
func (l *InitializingLive) SetOnFinished(callback live.InitializingFinishedCallback) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.onFinished = callback
}

// IsFinished 返回初始化是否已完成
func (l *InitializingLive) IsFinished() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.finished
}

// GetInfo 尝试获取真实的直播间信息
// 如果成功获取，将触发初始化完成回调并返回真实信息
// 如果失败，返回初始化状态信息（允许后续重试）
func (l *InitializingLive) GetInfo() (info *live.Info, err error) {
	l.mu.Lock()
	// 如果已经完成初始化，直接调用原始Live的GetInfo
	if l.finished {
		l.mu.Unlock()
		return l.OriginalLive.GetInfo()
	}
	l.mu.Unlock()

	// 尝试获取真实信息
	realInfo, err := l.OriginalLive.GetInfo()
	if err != nil {
		// 获取失败，返回初始化状态信息（允许后续重试）
		return &live.Info{
			Live:         l,
			HostName:     "",
			RoomName:     l.GetRawUrl(),
			Status:       false,
			Initializing: true,
		}, nil // 返回 nil 错误，让上层知道这是一个有效的（初始化中的）状态
	}

	// 获取成功，标记为已完成并触发回调
	l.mu.Lock()
	if l.finished {
		// 另一个 goroutine 已经完成了初始化
		l.mu.Unlock()
		return realInfo, nil
	}
	l.finished = true
	callback := l.onFinished
	l.mu.Unlock()

	// 触发回调（在锁外执行，避免死锁）
	if callback != nil {
		callback(l, l.OriginalLive, realInfo)
	}

	return realInfo, nil
}

func (l *InitializingLive) GetStreamUrls() (us []*url.URL, err error) {
	us = make([]*url.URL, 0)
	err = nil
	return
}

func (l *InitializingLive) GetPlatformCNName() string {
	return ""
}
