//go:generate go run go.uber.org/mock/mockgen -package mock -destination mock/mock.go github.com/bililive-go/bililive-go/src/live Live
package live

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/bililive-go/bililive-go/src/configs"
	"github.com/bililive-go/bililive-go/src/pkg/livelogger"
	"github.com/bililive-go/bililive-go/src/pkg/ratelimit"
	bilisentry "github.com/bililive-go/bililive-go/src/pkg/sentry"
	"github.com/bililive-go/bililive-go/src/types"
	"github.com/bluele/gcache"
)

// SchedulerRefreshCallback 调度器刷新完成的回调函数类型
type SchedulerRefreshCallback func(live Live, status SchedulerStatus)

// RequestStatusCallback 请求状态追踪的回调函数类型
type RequestStatusCallback func(liveID, platform string, success bool, errMsg string)

// PlatformReadinessChecker 检查某个平台依赖的外部工具是否已就绪。
// 返回 false 时调度器不会发起请求，reason 用于日志说明原因。
type PlatformReadinessChecker func(platformKey string) (ready bool, reason string)

// 全局调度器刷新回调（由外部包设置，避免循环依赖）
var schedulerRefreshCallback SchedulerRefreshCallback

// 全局请求状态追踪回调（由 iostats 包设置，避免循环依赖）
var requestStatusCallback RequestStatusCallback

// 全局平台工具就绪检查回调（由 tools 包设置，避免循环依赖）
var platformReadinessChecker PlatformReadinessChecker

// SetSchedulerRefreshCallback 设置调度器刷新完成的回调函数
func SetSchedulerRefreshCallback(callback SchedulerRefreshCallback) {
	schedulerRefreshCallback = callback
}

// SetRequestStatusCallback 设置请求状态追踪的回调函数
func SetRequestStatusCallback(callback RequestStatusCallback) {
	requestStatusCallback = callback
}

// SetPlatformReadinessChecker 设置平台依赖工具的就绪检查函数。
// 应在创建直播间之前调用；未设置时视为所有平台都已就绪。
func SetPlatformReadinessChecker(checker PlatformReadinessChecker) {
	platformReadinessChecker = checker
}

var (
	m                               = make(map[string]Builder)
	InitializingLiveBuilderInstance InitializingLiveBuilder
)

func Register(domain string, b Builder) {
	m[domain] = b
}

func getBuilder(domain string) (Builder, bool) {
	builder, ok := m[domain]
	return builder, ok
}

type Builder interface {
	Build(*url.URL) (Live, error)
}

type InitializingLiveBuilder interface {
	Build(Live, *url.URL) (Live, error)
}

// InitializingFinishedCallback 初始化完成时的回调函数类型
// 参数为：InitializingLive、原始Live、获取到的Info
type InitializingFinishedCallback func(initializingLive Live, originalLive Live, info *Info)

// InitializingLiveSetter 可设置初始化完成回调的接口
type InitializingLiveSetter interface {
	SetOnFinished(callback InitializingFinishedCallback)
	IsFinished() bool
}

// CachedInfoSetter 可设置缓存信息的接口（用于从数据库加载缓存的主播名和房间名）
type CachedInfoSetter interface {
	SetCachedInfo(hostName, roomName string)
	GetCachedInfo() (hostName, roomName string)
}

type InitializingFinishedParam struct {
	InitializingLive Live
	Live             Live
	Info             *Info
}

// GetLiveId 实现 liveIDProvider 接口，使 SSE 事件处理器能够正确广播此事件
func (p InitializingFinishedParam) GetLiveId() types.LiveID {
	if p.InitializingLive != nil {
		return p.InitializingLive.GetLiveId()
	}
	if p.Live != nil {
		return p.Live.GetLiveId()
	}
	return ""
}

type Options struct {
	Cookies   *cookiejar.Jar
	Quality   int
	AudioOnly bool
	NickName  string
}

func NewOptions(opts ...Option) (*Options, error) {
	cookieJar, err := cookiejar.New(&cookiejar.Options{})
	if err != nil {
		return nil, err
	}
	options := &Options{Cookies: cookieJar, Quality: 0}
	for _, opt := range opts {
		opt(options)
	}
	return options, nil
}

func MustNewOptions(opts ...Option) *Options {
	options, err := NewOptions(opts...)
	if err != nil {
		panic(err)
	}
	return options
}

type Option func(*Options)

func WithKVStringCookies(u *url.URL, cookies string) Option {
	return func(opts *Options) {
		cookiesList := make([]*http.Cookie, 0)
		for _, pairStr := range strings.Split(cookies, ";") {
			pairs := strings.SplitN(pairStr, "=", 2)
			if len(pairs) != 2 {
				continue
			}
			cookiesList = append(cookiesList, &http.Cookie{
				Name:  strings.TrimSpace(pairs[0]),
				Value: strings.TrimSpace(pairs[1]),
			})
		}
		opts.Cookies.SetCookies(u, cookiesList)
	}
}

func WithQuality(quality int) Option {
	return func(opts *Options) {
		opts.Quality = quality
	}
}

func WithAudioOnly(audioOnly bool) Option {
	return func(opts *Options) {
		opts.AudioOnly = audioOnly
	}
}

func WithNickName(nickName string) Option {
	return func(opts *Options) {
		opts.NickName = nickName
	}
}

type StreamUrlInfo struct {
	Url         *url.URL
	Name        string
	Description string

	// 兼容旧字段
	Resolution int // 已废弃，使用Width/Height代替
	Vbitrate   int // 已废弃，使用Bitrate代替

	// 新增：完整的流信息
	Quality    string  `json:"quality"`     // 清晰度标识: "1080p", "720p", "原画"
	Format     string  `json:"format"`      // 流格式: "flv", "hls", "rtmp"
	Width      int     `json:"width"`       // 宽度: 1920, 1280
	Height     int     `json:"height"`      // 高度: 1080, 720
	Bitrate    int     `json:"bitrate"`     // 码率 (kbps)
	FrameRate  float64 `json:"frame_rate"`  // 帧率
	Codec      string  `json:"codec"`       // 视频编码: "h264", "h265"
	AudioCodec string  `json:"audio_codec"` // 音频编码: "aac"

	// 用于前端流选择的属性组合
	// 包含所有可用于选择此流的属性键值对（如 "画质": "原画", "format": "flv", "codec": "h264"）
	// 前端会根据这些属性动态生成下拉选择器
	AttributesForStreamSelect map[string]string `json:"attributes_for_stream_select,omitempty"`

	HeadersForDownloader map[string]string

	IsPlaceHolder bool `json:"is_placeholder"`
}

type Live interface {
	SetLiveIdByString(string)
	GetLiveId() types.LiveID
	GetRawUrl() string
	GetInfo() (*Info, error)
	// GetInfoWithInterval 是一个会阻塞的 GetInfo 方法
	// 它会先等待配置的访问间隔（同时尊重平台最小访问频率），然后再发送请求
	// ctx 可用于取消等待，返回 ctx.Err()
	// 这个方法适用于需要周期性获取信息的场景，如 listener 的 refresh 循环
	GetInfoWithInterval(ctx context.Context) (*Info, error)
	// Deprecated: GetStreamUrls is deprecated, using GetStreamInfos instead
	GetStreamUrls() ([]*url.URL, error)
	GetStreamInfos() ([]*StreamUrlInfo, error)
	GetPlatformCNName() string
	GetLastStartTime() time.Time
	SetLastStartTime(time.Time)
	UpdateLiveOptionsbyConfig(context.Context, *configs.LiveRoom) error
	GetOptions() *Options
	GetLogger() *livelogger.LiveLogger
	// Close 关闭 Live 对象，释放相关资源（如调度器 goroutine）
	Close()
}

// ErrPlatformToolsNotReady 表示直播间所属平台依赖的外部工具还没就绪，
// 此时不应该发起请求，也不应该按「获取失败」来对待（不是平台或网络的问题）。
var ErrPlatformToolsNotReady = errors.New("平台依赖的工具尚未就绪")

const (
	// defaultInterval 没有配置轮询间隔时使用的默认值
	defaultInterval = 30 * time.Second
	// maxFailureBackoff 连续失败时退避间隔的上限。
	// 取值需要兼顾两点：故障时不能把请求打成风暴，恢复后也不能太久才发现开播。
	maxFailureBackoff = 5 * time.Minute
	// toolReadinessPollInterval 依赖工具未就绪时重新检查的间隔。
	// 这里只是读一个内存中的状态，不产生任何网络请求，因此可以查得比较勤。
	toolReadinessPollInterval = 2 * time.Second
)

// infoResult 用于传递 GetInfo 的结果
type infoResult struct {
	info *Info
	err  error
}

// waiter 表示一个等待 GetInfo 结果的调用方
type waiter struct {
	ch  chan infoResult
	ctx context.Context
}

// SchedulerStatus 调度器状态信息
type SchedulerStatus struct {
	// HasWaiters 是否有等待的调用方（是否有定期刷新计划）
	HasWaiters bool `json:"has_waiters"`
	// WaiterCount 等待的调用方数量
	WaiterCount int `json:"waiter_count"`
	// LastRequestAt 上次发送请求的时间
	LastRequestAt time.Time `json:"last_request_at"`
	// NextRequestAt 预计下次发送请求的时间
	NextRequestAt time.Time `json:"next_request_at"`
	// IntervalSeconds 配置的访问间隔（秒）
	IntervalSeconds int `json:"interval_seconds"`
	// SecondsUntilNextRequest 距离下次请求的秒数（如果有计划的话）
	SecondsUntilNextRequest float64 `json:"seconds_until_next_request"`
	// SecondsSinceLastRequest 距离上次请求的秒数
	SecondsSinceLastRequest float64 `json:"seconds_since_last_request"`
	// SchedulerRunning 调度器是否在运行
	SchedulerRunning bool `json:"scheduler_running"`
}

// SchedulerStatusProvider 提供调度器状态的接口
type SchedulerStatusProvider interface {
	GetSchedulerStatus() SchedulerStatus
}

type WrappedLive struct {
	Live
	cache gcache.Cache
	// requestMu 串行化同一直播间的直接刷新与调度刷新，并保护缓存的读改写顺序。
	requestMu sync.Mutex

	// 请求调度相关字段
	mu                  sync.Mutex
	waiters             []waiter      // 等待下一次请求结果的调用方
	lastRequestAt       time.Time     // 上次发送请求的时间（不论成功还是失败）
	consecutiveFailures int           // 连续失败次数，用于计算退避间隔
	waitingForTool      bool          // 是否正在等待平台依赖的外部工具就绪
	schedulerOnce       sync.Once     // 确保调度器只启动一次
	schedulerStarted    bool          // 调度器是否已启动
	schedulerStop       chan struct{} // 停止调度器的信号
	schedulerCtx        context.Context
	schedulerCancel     context.CancelFunc
}

// NewWrappedLive 创建一个带有缓存功能的 Live 包装器
// 外部包可以使用此函数将原始 Live 对象包装为支持缓存的 WrappedLive
// ctx 用于控制调度器的生命周期，当 ctx 被取消时调度器会停止
func NewWrappedLive(ctx context.Context, live Live, cache gcache.Cache) Live {
	schedulerCtx, schedulerCancel := context.WithCancel(ctx)
	return &WrappedLive{
		Live:            live,
		cache:           cache,
		schedulerStop:   make(chan struct{}),
		schedulerCtx:    schedulerCtx,
		schedulerCancel: schedulerCancel,
	}
}

// Close 停止请求调度器，释放相关资源
func (w *WrappedLive) Close() {
	w.schedulerCancel()
	// 使用 select 避免重复 close panic
	select {
	case <-w.schedulerStop:
		// 已经关闭
	default:
		close(w.schedulerStop)
	}
}

// GetRoomID 返回底层 Live 已解析的数字房间号（如有）。
// 用于斗鱼等平台别名 URL 的弹幕录制。
func (w *WrappedLive) GetRoomID() string {
	type roomIDProvider interface {
		GetRoomID() string
	}
	if provider, ok := w.Live.(roomIDProvider); ok {
		return provider.GetRoomID()
	}
	return ""
}

// GetCachedInfo 只读取最近一次缓存，不触发平台请求。
func (w *WrappedLive) GetCachedInfo() (*Info, bool) {
	if w.cache == nil {
		return nil, false
	}
	cached, err := w.cache.Get(w)
	if err != nil {
		return nil, false
	}
	info, ok := cached.(*Info)
	return info, ok && info != nil
}

func (w *WrappedLive) GetInfo() (*Info, error) {
	return w.GetInfoWithContext(w.schedulerCtx)
}

// GetInfoWithContext 立即请求一次直播间信息，并把调用方的业务因果上下文继续
// 传给平台限流器。它不是 Live 接口的一部分，listener 会以可选能力使用它；
// 旧的 Live 实现无需改动。
func (w *WrappedLive) GetInfoWithContext(ctx context.Context) (*Info, error) {
	if ctx == nil {
		ctx = w.schedulerCtx
	}

	// forceRefresh 与调度器可能同时进入 GetInfo。若允许它们并发，较早开始但较晚写缓存的
	// 失败请求可能覆盖较新的成功结果；整个请求链串行后，缓存提交顺序与请求顺序一致。
	w.requestMu.Lock()
	defer w.requestMu.Unlock()

	// 依赖的外部工具还没就绪时不发请求（调度器之外还有 listener.refresh 等直接调用方）。
	// 正常情况下调度器会先拦住，这里主要覆盖直接调用以及「刚检查完就绪、随即工具挂掉」的竞态，
	// 因此同样要通知等待者，避免它们一直挂着。
	if ready, reason := w.platformToolsReadyWithReason(); !ready {
		err := fmt.Errorf("%w: %s", ErrPlatformToolsNotReady, reason)
		w.notifyWaiters(nil, err)
		return nil, err
	}

	// 在通用位置获取平台请求许可。许可同时约束请求开始间隔和同平台在途数量，
	// 防止启动或工具恢复时几百个房间一起进入平台实现。
	releasePlatform, ok := w.acquirePlatformRequest(ctx)
	if !ok {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		return nil, w.schedulerCtx.Err()
	}
	defer releasePlatform()

	i, err := w.Live.GetInfo()

	// 不论成功还是失败都要记录本次请求时间：
	// 如果失败时不更新 lastRequestAt，调度器会一直认为「早就该请求了」，
	// 从而退化成固定 3 秒一次的重试，反而把故障状态下的请求频率放大十倍，
	// 把本地工具和平台接口一起打爆，永远无法自愈。
	requestFailed := w.recordRequestResult(i, err)

	// 记录请求状态到 IO 统计（通过回调避免循环依赖）
	if requestStatusCallback != nil {
		liveID := string(w.GetLiveId())
		platform := w.GetPlatformCNName()
		if requestFailed {
			errMessage := ""
			if i != nil {
				errMessage = i.LastError
			}
			if err != nil {
				errMessage = err.Error()
			}
			requestStatusCallback(liveID, platform, false, errMessage)
		} else {
			requestStatusCallback(liveID, platform, true, "")
		}
	}

	// 不管成功还是失败，都通知所有等待的调用方
	w.notifyWaiters(i, err)

	if err != nil {
		// 将错误信息存到 LastError 而非 RoomName，避免错误文本出现在录制文件名中。
		// 注意这里不能就地修改缓存里的 *Info：它同时被 API/SSE 等读取方持有，
		// 就地写字段是无锁的数据竞争。改为写入一份副本再替换缓存。
		w.cacheLastError(err)
		// 失败同样要通知前端更新倒计时，否则退避期间界面会一直显示「即将刷新」
		w.dispatchSchedulerRefreshEvent()
		return nil, err
	}
	if w.cache != nil {
		// 初始化占位 Info 会通过 LastError 告诉调度器底层请求实际失败，既保留可展示的
		// 占位信息，也不能把这次请求误算成成功并清零退避。
		if !requestFailed {
			i.LastError = ""
		}
		w.cache.Set(w, i)
	}

	// 发送调度器刷新完成事件，通知前端更新倒计时
	w.dispatchSchedulerRefreshEvent()

	return i, nil
}

// recordRequestResult 更新调度器的请求时间和连续失败计数。
// InitializingLive 为了向前端保留占位信息会返回 nil error，因此还要识别其 LastError。
func (w *WrappedLive) recordRequestResult(info *Info, err error) bool {
	requestFailed := err != nil || (info != nil && info.Initializing && info.LastError != "")
	w.mu.Lock()
	w.lastRequestAt = time.Now()
	if requestFailed {
		w.consecutiveFailures++
	} else {
		w.consecutiveFailures = 0
	}
	w.mu.Unlock()
	return requestFailed
}

// SeedInfo 把已经完成的初始化请求结果注入新的 WrappedLive。
// 监听器交接时复用这份结果，可以避免新 listener 立刻重复请求一次，并让调度器从本次
// 请求时间开始计算下一轮间隔。
func (w *WrappedLive) SeedInfo(info *Info) error {
	if info == nil {
		return errors.New("不能注入空的直播间信息")
	}

	w.requestMu.Lock()
	defer w.requestMu.Unlock()

	w.mu.Lock()
	w.lastRequestAt = time.Now()
	w.consecutiveFailures = 0
	w.mu.Unlock()

	info.LastError = ""
	if w.cache == nil {
		return nil
	}
	return w.cache.Set(w, info)
}

// cacheLastError 把最近一次的错误信息记录到缓存的直播间信息上。
// 通过「读出来 → 复制 → 改副本 → 写回」的方式更新，避免修改其他 goroutine
// 正在读取的 *Info（gcache 只保证自身的并发安全，不保证值对象的）。
func (w *WrappedLive) cacheLastError(err error) {
	if w.cache == nil {
		return
	}
	cached, cacheErr := w.cache.Get(w)
	if cacheErr != nil {
		return
	}
	info, ok := cached.(*Info)
	if !ok || info == nil {
		return
	}
	updated := *info
	updated.LastError = err.Error()
	w.cache.Set(w, &updated)
}

// dispatchSchedulerRefreshEvent 发送调度器刷新完成事件
func (w *WrappedLive) dispatchSchedulerRefreshEvent() {
	if schedulerRefreshCallback != nil {
		schedulerRefreshCallback(w, w.GetSchedulerStatus())
	}
}

// notifyWaiters 通知所有等待的调用方
func (w *WrappedLive) notifyWaiters(info *Info, err error) {
	w.mu.Lock()
	waiters := w.waiters
	w.waiters = nil
	w.mu.Unlock()

	result := infoResult{info: info, err: err}
	for _, waiter := range waiters {
		select {
		case waiter.ch <- result:
		case <-waiter.ctx.Done():
			// 调用方已经取消，忽略
		default:
			// channel 已满或已关闭，忽略
		}
	}
}

// GetInfoWithInterval 是一个会阻塞的 GetInfo 方法
// 调用方会等待直到下一次 GetInfo 请求完成，然后获得该请求的结果
// 多个调用方会共享同一次请求的结果
// ctx 可用于取消等待
func (w *WrappedLive) GetInfoWithInterval(ctx context.Context) (*Info, error) {
	// 确保调度器已启动
	w.startScheduler()

	// 创建等待通道
	ch := make(chan infoResult, 1)

	w.mu.Lock()
	w.waiters = append(w.waiters, waiter{ch: ch, ctx: ctx})
	w.mu.Unlock()

	// 等待结果或取消
	select {
	case <-ctx.Done():
		// 从等待列表中移除（避免内存泄漏）
		w.removeWaiter(ch)
		return nil, ctx.Err()
	case result := <-ch:
		return result.info, result.err
	}
}

// removeWaiter 从等待列表中移除指定的等待者
func (w *WrappedLive) removeWaiter(ch chan infoResult) {
	w.mu.Lock()
	defer w.mu.Unlock()
	for i, waiter := range w.waiters {
		if waiter.ch == ch {
			w.waiters = append(w.waiters[:i], w.waiters[i+1:]...)
			return
		}
	}
}

// startScheduler 启动请求调度器
func (w *WrappedLive) startScheduler() {
	w.schedulerOnce.Do(func() {
		w.mu.Lock()
		w.schedulerStarted = true
		w.mu.Unlock()
		bilisentry.Go(w.runScheduler)
	})
}

// GetSchedulerStatus 获取调度器状态信息
func (w *WrappedLive) GetSchedulerStatus() SchedulerStatus {
	w.mu.Lock()
	defer w.mu.Unlock()

	now := time.Now()
	interval := w.getConfiguredInterval()
	intervalDuration := w.nextRequestIntervalLocked()

	status := SchedulerStatus{
		HasWaiters:       len(w.waiters) > 0,
		WaiterCount:      len(w.waiters),
		LastRequestAt:    w.lastRequestAt,
		IntervalSeconds:  interval,
		SchedulerRunning: w.schedulerStarted,
	}

	// 计算距离上次请求的秒数
	if !w.lastRequestAt.IsZero() {
		status.SecondsSinceLastRequest = now.Sub(w.lastRequestAt).Seconds()
	}

	// 只有在有等待者且调度器运行时才计算下次请求时间
	if status.HasWaiters && status.SchedulerRunning {
		nextRequestAt := w.lastRequestAt.Add(intervalDuration)
		status.NextRequestAt = nextRequestAt
		if nextRequestAt.After(now) {
			status.SecondsUntilNextRequest = nextRequestAt.Sub(now).Seconds()
		} else {
			// 已经过了预计时间，正在等待平台限制或准备发送请求
			status.SecondsUntilNextRequest = 0
		}
	} else {
		// 没有计划的刷新
		status.SecondsUntilNextRequest = -1 // -1 表示没有计划
	}

	return status
}

// runScheduler 运行请求调度循环
func (w *WrappedLive) runScheduler() {
	for {
		// 先检查是否有等待的调用方，如果没有就等待一小段时间再检查
		w.mu.Lock()
		hasWaiters := len(w.waiters) > 0
		w.mu.Unlock()

		if !hasWaiters {
			// 没有等待者，休眠一小段时间再检查
			select {
			case <-w.schedulerStop:
				return
			case <-w.schedulerCtx.Done():
				return
			case <-time.After(100 * time.Millisecond):
				continue
			}
		}

		// 平台依赖的外部工具（如抖音需要的 bililive-tools）还没就绪时不发起请求。
		// 此时请求必然失败，只会把还在启动中的本地服务打爆，并让直播间被误判为未开播；
		// 等待期间调用方继续阻塞在 GetInfoWithInterval 上，也就不会触发录制。
		if !w.platformToolsReady() {
			select {
			case <-w.schedulerStop:
				return
			case <-w.schedulerCtx.Done():
				return
			case <-time.After(toolReadinessPollInterval):
				continue
			}
		}

		// 有等待者，计算需要等待的时间（连续失败时会自动退避）
		w.mu.Lock()
		nextRequestAt := w.lastRequestAt.Add(w.nextRequestIntervalLocked())
		w.mu.Unlock()

		now := time.Now()
		var waitDuration time.Duration
		if nextRequestAt.After(now) {
			waitDuration = nextRequestAt.Sub(now)
		} else {
			// 已经过了下一次请求时间，添加一点随机抖动避免同时请求
			waitDuration = time.Duration(randomJitter()+3000) * time.Millisecond
			if waitDuration < 0 {
				waitDuration = 0
			}
		}

		// 等待直到下一次请求时间
		if waitDuration > 0 {
			timer := time.NewTimer(waitDuration)
			select {
			case <-w.schedulerStop:
				timer.Stop()
				return
			case <-w.schedulerCtx.Done():
				timer.Stop()
				return
			case <-timer.C:
			}
		}

		// 再次检查是否还有等待者（可能在等待期间被取消了）
		w.mu.Lock()
		hasWaiters = len(w.waiters) > 0
		w.mu.Unlock()

		if hasWaiters {
			// 同一个 WrappedLive 通常只有一个 listener 等待者。保留首个仍有效
			// 等待者的 diagnostics context，使共享平台限流的等待能归因到具体
			// room_scope_id / generation / flow_id；没有有效等待者时退回调度器
			// 生命周期 context。
			requestCtx := w.schedulerCtx
			w.mu.Lock()
			for _, pending := range w.waiters {
				if pending.ctx != nil && pending.ctx.Err() == nil {
					requestCtx = pending.ctx
					break
				}
			}
			w.mu.Unlock()
			// 发送请求（GetInfo 会通知所有等待者）
			_, _ = w.GetInfoWithContext(requestCtx)
		}
	}
}

// nextRequestIntervalLocked 计算下一次请求前应该等待的间隔。
// 正常情况下就是配置的轮询间隔；连续失败时按 2 的幂次退避，最长为
// max(maxFailureBackoff, 配置间隔)，一次成功即恢复到配置间隔。
// 调用方必须持有 w.mu。
func (w *WrappedLive) nextRequestIntervalLocked() time.Duration {
	interval := time.Duration(w.getConfiguredInterval()) * time.Second
	if interval <= 0 {
		interval = defaultInterval
	}
	return failureBackoffInterval(interval, w.consecutiveFailures)
}

func failureBackoffInterval(interval time.Duration, consecutiveFailures int) time.Duration {
	maxBackoff := maxFailureBackoff
	if interval > maxBackoff {
		maxBackoff = interval
	}

	backoff := interval
	for i := 0; i < consecutiveFailures && backoff < maxBackoff; i++ {
		if backoff > maxBackoff/2 {
			backoff = maxBackoff
		} else {
			backoff *= 2
		}
	}
	return backoff
}

// getConfiguredInterval 获取此直播间配置的访问间隔（秒）
func (w *WrappedLive) getConfiguredInterval() int {
	cfg := configs.GetCurrentConfig()
	if cfg == nil {
		return int(defaultInterval.Seconds())
	}

	room, err := cfg.GetLiveRoomByUrl(w.GetRawUrl())
	if err != nil {
		if cfg.Interval > 0 {
			return cfg.Interval
		}
		return int(defaultInterval.Seconds())
	}

	platformKey := configs.GetPlatformKeyFromUrl(w.GetRawUrl())
	resolvedConfig := cfg.ResolveConfigForRoom(room, platformKey)
	return resolvedConfig.Interval
}

// randomJitter 生成 -3000 到 +3000 毫秒的随机抖动
func randomJitter() int64 {
	// 使用简单的方法生成随机数，避免导入额外的包
	return (time.Now().UnixNano() % 6001) - 3000
}

// platformToolsReady 判断本直播间所属平台依赖的外部工具是否已就绪。
func (w *WrappedLive) platformToolsReady() bool {
	ready, _ := w.platformToolsReadyWithReason()
	return ready
}

// platformToolsReadyWithReason 判断依赖的外部工具是否已就绪，并返回未就绪的原因。
// 状态发生变化时输出一条日志，方便用户理解「为什么这个直播间还没开始轮询」。
func (w *WrappedLive) platformToolsReadyWithReason() (bool, string) {
	checker := platformReadinessChecker
	if checker == nil {
		return true, ""
	}

	platformKey := configs.GetPlatformKeyFromUrl(w.GetRawUrl())
	if platformKey == "" {
		return true, ""
	}

	ready, reason := checker(platformKey)

	w.mu.Lock()
	wasWaiting := w.waitingForTool
	w.waitingForTool = !ready
	w.mu.Unlock()

	switch {
	case !ready && !wasWaiting:
		w.GetLogger().Debugf("依赖的工具尚未就绪，暂不请求直播间信息：%s", reason)
	case ready && wasWaiting:
		w.GetLogger().Debug("依赖的工具已就绪，恢复请求直播间信息")
	}

	return ready, reason
}

// acquirePlatformRequest 在通用位置获取平台请求许可。
// 调用方 context 同时控制排队取消并携带 room/generation/flow，使等待
// in-flight 槽位和访问间隔都出现在正确的 diagnostics 业务轨迹上。
func (w *WrappedLive) acquirePlatformRequest(ctx context.Context) (release func(), ok bool) {
	if ctx == nil {
		ctx = w.schedulerCtx
	}
	platformKey := configs.GetPlatformKeyFromUrl(w.GetRawUrl())
	if platformKey != "" {
		minInterval := 1
		if cfg := configs.GetCurrentConfig(); cfg != nil {
			minInterval = cfg.GetPlatformMinAccessInterval(platformKey)
		}
		rateLimiter := ratelimit.GetGlobalRateLimiter()
		// 添加新平台的第一个房间时，Live 会先于配置持久化创建；在请求入口兜底补齐
		// 默认限制，避免该时间窗内首次请求直接放行。
		rateLimiter.EnsurePlatformLimit(platformKey, minInterval)
		return rateLimiter.AcquirePlatformWithContext(ctx, platformKey)
	}
	return func() {}, true
}

func New(ctx context.Context, room *configs.LiveRoom, cache gcache.Cache) (live Live, err error) {
	url, err := url.Parse(room.Url)
	if err != nil {
		return nil, err
	}
	builder, ok := getBuilder(url.Host)
	if !ok {
		return nil, errors.New("not support this url")
	}
	live, err = builder.Build(url)
	if err != nil {
		return
	}
	live.UpdateLiveOptionsbyConfig(ctx, room)
	live = NewWrappedLive(ctx, live, cache)
	for i := 0; i < 3; i++ {
		var info *Info
		if info, err = live.GetInfo(); err == nil {
			if info.CustomLiveId != "" {
				live.SetLiveIdByString(info.CustomLiveId)
			}
			return
		}
		time.Sleep(1 * time.Second)
	}

	// when room initializaion is failed
	live, err = InitializingLiveBuilderInstance.Build(live, url)
	if err != nil {
		return nil, err
	}
	live.UpdateLiveOptionsbyConfig(ctx, room)
	live = NewWrappedLive(ctx, live, cache)
	live.GetInfo() // dummy call to initialize cache inside wrappedLive
	return
}

// NewInitializing 创建一个初始化状态的 Live 对象
// 与 New 不同，它不会立即调用 GetInfo()，而是创建一个 InitializingLive
// 允许快速创建所有直播间，后续由 Listener 在后台异步获取信息
// onFinished 回调会在 GetInfo() 成功获取真实信息时被调用
// 注意：回调中的 initializingLive 参数是外层的 WrappedLive，而不是内部的 InitializingLive
func NewInitializing(ctx context.Context, room *configs.LiveRoom, cache gcache.Cache, onFinished InitializingFinishedCallback) (Live, error) {
	u, err := url.Parse(room.Url)
	if err != nil {
		return nil, err
	}
	builder, ok := getBuilder(u.Host)
	if !ok {
		return nil, errors.New("not support this url")
	}
	originalLive, err := builder.Build(u)
	if err != nil {
		return nil, err
	}
	originalLive.UpdateLiveOptionsbyConfig(ctx, room)

	// 直接创建 InitializingLive，不调用 GetInfo
	initLive, err := InitializingLiveBuilderInstance.Build(originalLive, u)
	if err != nil {
		return nil, err
	}
	initLive.UpdateLiveOptionsbyConfig(ctx, room)

	// 先创建 WrappedLive，传入 ctx 以便调度器可以被取消
	wrappedLive := NewWrappedLive(ctx, initLive, cache)

	// 设置初始化完成回调
	// 包装用户的回调，将 initializingLive 参数替换为外层的 WrappedLive
	if setter, ok := initLive.(InitializingLiveSetter); ok && onFinished != nil {
		setter.SetOnFinished(func(_ Live, originalLive Live, info *Info) {
			// 传递 wrappedLive 作为 initializingLive 参数
			// 这样事件处理器可以正确地从 inst.Lives 和 m.savers 中找到对应的条目
			onFinished(wrappedLive, originalLive, info)
		})
	}

	// 设置一个初始 LiveId（基于 URL）
	// 真正的 LiveId 会在 GetInfo 成功后更新
	return wrappedLive, nil
}
