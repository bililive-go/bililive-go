package system

import (
	"context"
	"net/url"
	"sync"

	"github.com/bililive-go/bililive-go/src/configs"
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
	// 是否已经记录过初始化失败日志（后续重试降级为 debug，避免刷屏）
	failureLogged bool
	// 用于保护并发访问
	mu sync.Mutex

	// 缓存的信息（从数据库加载，用于在初始化前显示）
	cachedHostName string
	cachedRoomName string
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

// SetCachedInfo 设置缓存的直播间信息（从数据库加载）
// 这些信息会在初始化完成前用于显示
func (l *InitializingLive) SetCachedInfo(hostName, roomName string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.cachedHostName = hostName
	l.cachedRoomName = roomName
}

// GetCachedInfo 获取缓存的直播间信息
func (l *InitializingLive) GetCachedInfo() (hostName, roomName string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.cachedHostName, l.cachedRoomName
}

// GetInfo 尝试获取真实的直播间信息
// 如果成功获取，将触发初始化完成回调并返回真实信息
// 如果失败，返回初始化状态信息（使用缓存信息，允许后续重试）
func (l *InitializingLive) GetInfo() (info *live.Info, err error) {
	l.mu.Lock()
	// 如果已经完成初始化，直接调用原始Live的GetInfo
	if l.finished {
		l.mu.Unlock()
		return l.OriginalLive.GetInfo()
	}
	// 获取缓存信息用于后续使用
	cachedHost := l.cachedHostName
	cachedRoom := l.cachedRoomName
	l.mu.Unlock()

	// 尝试获取真实信息
	realInfo, err := l.OriginalLive.GetInfo()
	if err != nil {
		// 这里向上返回 nil error（初始化中是一个有效状态），
		// 但错误本身不能完全吞掉：否则「直播间一直卡在初始化中」在日志里
		// 没有任何线索，排查时完全看不出根因。
		// 首次失败记 warn，后续重试降为 debug，避免大量直播间同时刷屏。
		l.mu.Lock()
		firstFailure := !l.failureLogged
		l.failureLogged = true
		l.mu.Unlock()
		if firstFailure {
			l.GetLogger().WithError(err).Warn("初始化直播间信息失败，将继续重试")
		} else {
			l.GetLogger().WithError(err).Debug("初始化直播间信息失败，将继续重试")
		}

		// 获取失败，返回初始化状态信息（使用缓存信息，允许后续重试）
		roomName := l.GetRawUrl()
		if cachedRoom != "" {
			roomName = cachedRoom
		}
		return &live.Info{
			Live:         l,
			HostName:     cachedHost,
			RoomName:     roomName,
			Status:       false,
			Initializing: true,
			// 上层仍收到可展示的初始化占位信息和 nil error，但通过 LastError 可以把
			// 真实的平台失败计入调度退避，避免故障期间始终按基础间隔重试。
			LastError: err.Error(),
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

// UpdateLiveOptionsbyConfig 同时更新包装对象与原始 Live 的房间选项。
// BaseLive 是值嵌入，若只调用继承来的实现，选项（登录 cookie、只录音频、清晰度）只落在包装对象上；
// 而初始化完成后会用 OriginalLive 顶掉本包装对象，期间热应用的 cookie 就跟着包装对象一起丢了——
// 表现为"重新扫码后老房间仍按扫码前的登录态取流"，且没有任何提示。
func (l *InitializingLive) UpdateLiveOptionsbyConfig(ctx context.Context, room *configs.LiveRoom) error {
	if err := l.BaseLive.UpdateLiveOptionsbyConfig(ctx, room); err != nil {
		return err
	}
	return l.OriginalLive.UpdateLiveOptionsbyConfig(ctx, room)
}

func (l *InitializingLive) GetOptions() *live.Options {
	return l.OriginalLive.GetOptions()
}

// GetRoomID 委托给原始 Live：房间号由平台实现自己解析，包装对象没有。
// 少了这层转发，WrappedLive 的类型断言只能走到本对象就返回空，斗鱼别名 URL
// （如 /lotterytimer）的弹幕录制会退回拿 URL 路径当房间号，弹幕登录直接失败。
func (l *InitializingLive) GetRoomID() string {
	type roomIDProvider interface {
		GetRoomID() string
	}
	if provider, ok := l.OriginalLive.(roomIDProvider); ok {
		return provider.GetRoomID()
	}
	return ""
}

func (l *InitializingLive) GetStreamUrls() (us []*url.URL, err error) {
	// 委托给原始 Live 而不是返回空列表
	return l.OriginalLive.GetStreamUrls() //nolint:staticcheck // deprecated 方法的委托实现
}

func (l *InitializingLive) GetStreamInfos() ([]*live.StreamUrlInfo, error) {
	// 委托给原始 Live
	return l.OriginalLive.GetStreamInfos()
}

// ListStreamCandidates 委托给原始 Live；平台没有实现时返回 ErrNotImplemented，
// 由调用方回退全量解析，不能谎报"候选为空"。
func (l *InitializingLive) ListStreamCandidates() ([]*live.StreamUrlInfo, error) {
	if resolver, ok := l.OriginalLive.(live.DeferredStreamResolver); ok {
		return resolver.ListStreamCandidates()
	}
	return nil, live.ErrNotImplemented
}

func (l *InitializingLive) ResolveStreamCandidate(candidate *live.StreamUrlInfo) error {
	if resolver, ok := l.OriginalLive.(live.DeferredStreamResolver); ok {
		return resolver.ResolveStreamCandidate(candidate)
	}
	return live.ErrNotImplemented
}

func (l *InitializingLive) GetPlatformCNName() string {
	return l.OriginalLive.GetPlatformCNName()
}
