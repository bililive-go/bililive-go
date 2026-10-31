package sooplive

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/hr3lxphr6j/requests"
	"github.com/tidwall/gjson"
	"golang.org/x/sync/singleflight"

	"github.com/bililive-go/bililive-go/src/configs"
	"github.com/bililive-go/bililive-go/src/live"
	"github.com/bililive-go/bililive-go/src/live/internal"
	"github.com/bililive-go/bililive-go/src/pkg/utils"
)

const (
	domainPlaySoop     = "play.sooplive.com"
	cnName             = "SOOP"
	channelAPIURL      = "https://live.sooplive.com/afreeca/player_live_api.php"
	defaultOrigin      = "https://play.sooplive.com"
	channelResultOK    = 1
	channelResultLogin = -6
	channelResultEmpty = 0
	channelResultBlock = -2

	// 播放页与频道信息会被"监控判活"和紧随其后的"取流"连续读取，
	// 在这个窗口内复用成功结果即可省掉重复请求。窗口必须明显小于监控轮询间隔，
	// 否则下播感知会被缓存拖慢。
	pageMetaReuseTTL    = 15 * time.Second
	channelInfoReuseTTL = 15 * time.Second
)

var (
	reWindowBroadNo    = regexp.MustCompile(`window\.nBroadNo\s*=\s*(\d+|null);`)
	setCookiesFunc     = configs.SetCookies
	persistCookieGroup singleflight.Group
	streamInfoGroup    singleflight.Group
)

func init() {
	live.Register(domainPlaySoop, new(builder))
}

type builder struct{}

func (b *builder) Build(u *url.URL) (live.Live, error) {
	return &Live{
		BaseLive: internal.NewBaseLive(u),
	}, nil
}

type Live struct {
	internal.BaseLive
	runtimeCookie      string
	ignoreStoredCookie bool
	stateMu            sync.RWMutex
	optionsMu          sync.RWMutex
	// reuseMu 保护下面两个短期复用缓存，监控线程与录制线程会同时读写它们。
	// 只缓存播放页与频道信息：aid 这类播放凭证一旦跨尝试复用，
	// 平台拒绝旧凭证时本地无从得知，会把几秒的恢复拖成缓存窗口那么长的停录。
	reuseMu          sync.Mutex
	pageMetaCache    *pageMeta
	pageMetaCachedAt time.Time
	channelCache     *channelInfo
	channelCacheKey  string
	channelCachedAt  time.Time
}

func (l *Live) logRetryDetail(err error, format string, args ...interface{}) {
	if configs.IsDebug() {
		if err != nil {
			l.GetLogger().WithError(err).Warnf(format, args...)
			return
		}
		l.GetLogger().Warnf(format, args...)
		return
	}

	if err != nil {
		l.GetLogger().WithError(err).Debugf(format, args...)
		return
	}
	l.GetLogger().Debugf(format, args...)
}

// pageMeta 表示从播放页 HTML 中直接提取的最小元信息。
// 这一步不依赖 Soop API，主要用于：
// 1. 在接口异常时提供基础房间信息；
// 2. 区分页面是“明确离线（nBroadNo=null 或最终 URL 为 /null）”还是“字段缺失”；
// 3. 仅在页面字段缺失时，回退使用 URL 路径中的 broadNo 继续尝试后续 API。
//
// 字段语义：
// - PathBroadNo: 最终响应 URL 路径里携带的 broadNo（/channel/bno）。
// - PageBroadNo: 页面脚本里解析出的 broadNo，仅当页面给出数字时非空。
// - PageBroadNoFound: 页面是否出现过 window.nBroadNo 字段。
// - PageExplicitlyOffline: 页面是否明确给出 nBroadNo=null，或最终 URL 已落到 /null。
// - BroadNo: 当前后续 API 实际使用的最终 broadNo。
// - IsLiving: 当前是否存在“可继续请求后续 API 的在线候选”。
type pageMeta struct {
	Channel               string
	BroadNo               string
	PathBroadNo           string
	PageBroadNo           string
	PageBroadNoFound      bool
	PageExplicitlyOffline bool
	HostName              string
	RoomName              string
	IsLiving              bool
}

type channelInfo struct {
	Result      int
	BroadNo     string
	HostName    string
	RoomName    string
	RMD         string
	CDN         string
	NeedPwd     bool
	ViewPresets []viewPreset
}

type viewPreset struct {
	Label           string
	Name            string
	LabelResolution int
	BPS             int
}

// GetInfo 获取房间基础信息。
// 设计上分两层：
// 1. 先从 HTML 提取页面可见信息，保证接口波动时仍能返回基础数据；
// 2. 再尝试调用 Soop API 纠正主播名、标题和开播状态。
func (l *Live) GetInfo() (*live.Info, error) {
	l.GetLogger().Debugf("Soop GetInfo 开始: url=%s", l.GetRawUrl())
	options := l.GetOptions()
	meta, err := l.fetchPageMetaCached()
	if err != nil {
		l.GetLogger().WithError(err).Debug("Soop GetInfo 失败：页面元信息获取失败")
		return nil, err
	}

	info := &live.Info{
		Live:      l,
		HostName:  meta.HostName,
		RoomName:  meta.RoomName,
		Status:    false,
		AudioOnly: options.AudioOnly,
	}

	// 页面明确离线（nBroadNo=null 或 /null）或既没有页面 broadNo 也没有路径 broadNo 时，
	// 当前请求不会继续访问 Soop 播放信息接口，而是直接返回离线状态。
	if !meta.IsLiving {
		l.GetLogger().Debugf("Soop GetInfo 完成：页面判定离线 channel=%s pathBroadNo=%s pageBroadNo=%s pageBroadNoFound=%v explicitOffline=%v resolvedBroadNo=%s",
			meta.Channel, meta.PathBroadNo, meta.PageBroadNo, meta.PageBroadNoFound, meta.PageExplicitlyOffline, meta.BroadNo)
		return info, nil
	}

	// 这里不再吞掉错误，而是显式返回，便于前端区分：
	// - 登录失效
	// - Soop API 异常
	// - 地区 / 风控限制
	channelInfo, err := l.resolveChannelInfoCached(meta.Channel, meta.BroadNo)
	if err != nil {
		l.GetLogger().WithError(err).Debugf("Soop GetInfo 失败：解析频道信息失败 channel=%s broadNo=%s", meta.Channel, meta.BroadNo)
		return nil, err
	}
	if channelInfo.Result == channelResultOK {
		if channelInfo.HostName != "" {
			info.HostName = channelInfo.HostName
		}
		if channelInfo.RoomName != "" {
			info.RoomName = channelInfo.RoomName
		}
		info.Status = true
	}

	l.GetLogger().Debugf("Soop GetInfo 完成：host=%s room=%s living=%v result=%d", info.HostName, info.RoomName, info.Status, channelInfo.Result)
	return info, nil
}

// GetStreamInfos 获取 Soop 所有可用 HLS 流。
// 1. 拿页面元信息和 bno；
// 2. 解析频道信息（含登录态预检）；
// 3. 针对每个清晰度申请 aid；
// 4. 通过调度接口获取 view_url；
// 5. 将 view_url 与 aid 组合成最终可录制的 m3u8 地址。
//
// 注意：
// - 页面明确离线（nBroadNo=null 或 /null）时，这里会直接返回“当前无可录制流”；
// - 页面缺失 nBroadNo 字段但路径里带有 broadNo 时，仍会回退使用路径 broadNo 继续请求 API；
// - 因此这里返回的“无法获取播放流”既可能表示离线，也可能表示登录态不足或页面/API 结构变化。
func (l *Live) GetStreamInfos() ([]*live.StreamUrlInfo, error) {
	// 录制重试、面板 /urls 与 /probe 可能在同一房间上几乎同时解析取流地址，
	// 合并为一次实际解析，避免成倍地敲播放页、播放信息和 aid/调度接口。
	result, err, _ := streamInfoGroup.Do(l.GetRawUrl(), func() (interface{}, error) {
		return l.resolveStreamInfos()
	})
	if err != nil {
		return nil, err
	}
	streams, _ := result.([]*live.StreamUrlInfo)
	return streams, nil
}

func (l *Live) resolveStreamInfos() ([]*live.StreamUrlInfo, error) {
	l.GetLogger().Debugf("Soop GetStreamInfos 开始: url=%s", l.GetRawUrl())
	meta, channelInfo, presets, err := l.resolveRecordingContext()
	if err != nil {
		return nil, err
	}

	streams := make([]*live.StreamUrlInfo, 0, len(presets))
	for _, preset := range presets {
		stream, err := l.resolvePresetStream(meta.Channel, channelInfo, preset)
		if err != nil {
			l.logRetryDetail(err, "解析 Soop 清晰度失败: quality=%s", preset.Name)
			continue
		}
		streams = append(streams, stream)
	}

	if len(streams) == 0 {
		if channelInfo.NeedPwd {
			return nil, fmt.Errorf("soop 房间开启了直播密码，当前版本尚未填写密码，无法获取播放流")
		}
		return nil, fmt.Errorf("soop 未返回任何可用流，可能是接口变更、登录态不足或调度节点异常")
	}

	l.GetLogger().Debugf("Soop GetStreamInfos 完成：streams=%d selected_default=%s", len(streams), streams[0].Quality)
	return streams, nil
}

// resolveRecordingContext 完成取流的前两步（播放页 + 播放信息）并校验是否可继续解析清晰度，
// 返回按画质优先级排好序、已剔除 auto 档的候选列表。
// 这两步的结果在 15 秒窗口内可复用，因此"先列候选、再只解析选中的一路"不会重复敲接口。
func (l *Live) resolveRecordingContext() (*pageMeta, *channelInfo, []viewPreset, error) {
	meta, err := l.fetchPageMetaCached()
	if err != nil {
		l.GetLogger().WithError(err).Debug("Soop GetStreamInfos 失败：页面元信息获取失败")
		return nil, nil, nil, err
	}
	if !meta.IsLiving {
		l.GetLogger().Debugf("Soop GetStreamInfos 结束：页面判定无有效 broadNo channel=%s pathBroadNo=%s pageBroadNo=%s pageBroadNoFound=%v explicitOffline=%v resolvedBroadNo=%s",
			meta.Channel, meta.PathBroadNo, meta.PageBroadNo, meta.PageBroadNoFound, meta.PageExplicitlyOffline, meta.BroadNo)
		if meta.PageExplicitlyOffline {
			return nil, nil, nil, fmt.Errorf("%w: Soop 页面已明确显示下播，当前无可录制流", live.ErrLiveOffline)
		}
		return nil, nil, nil, fmt.Errorf("未从 Soop 播放页解析到有效直播场次号，可能未开播、需要登录或页面结构已变更，暂时无法获取播放流")
	}

	channelInfo, err := l.resolveChannelInfoCached(meta.Channel, meta.BroadNo)
	if err != nil {
		l.GetLogger().WithError(err).Debugf("Soop GetStreamInfos 失败：解析频道信息失败 channel=%s broadNo=%s", meta.Channel, meta.BroadNo)
		return nil, nil, nil, err
	}
	if channelInfo.Result != channelResultOK {
		return nil, nil, nil, explainChannelResultError("Soop 播放信息接口返回异常", channelInfo.Result)
	}
	if channelInfo.BroadNo == "" || channelInfo.RMD == "" {
		return nil, nil, nil, fmt.Errorf("soop 播放信息不完整：缺少 broadcast id 或调度节点地址")
	}

	sortViewPresetsByPriority(channelInfo.ViewPresets)
	var presets []viewPreset
	for _, preset := range channelInfo.ViewPresets {
		// auto 档没有固定的 Name，无法据此申请 aid，只能跳过
		if strings.EqualFold(preset.Name, "auto") {
			continue
		}
		presets = append(presets, preset)
	}
	return meta, channelInfo, presets, nil
}

// resolvePresetStream 为一路清晰度申请 aid 并通过调度接口拿到播放地址。
func (l *Live) resolvePresetStream(channel string, channelInfo *channelInfo, preset viewPreset) (*live.StreamUrlInfo, error) {
	l.GetLogger().Debugf("Soop 开始处理清晰度: label=%s name=%s resolution=%d bps=%d", preset.Label, preset.Name, preset.LabelResolution, preset.BPS)

	aid, result, err := l.fetchAid(channel, channelInfo.BroadNo, preset.Name)
	if err != nil {
		return nil, fmt.Errorf("获取 Soop AID 失败(quality=%s): %w", preset.Name, err)
	}
	if result == channelResultLogin {
		l.logRetryDetail(nil, "Soop AID 申请提示需要登录，准备自动重登后重试: quality=%s", preset.Name)
		if err = l.tryAutoLogin(); err != nil {
			return nil, fmt.Errorf("自动重登失败，无法重试 Soop AID(quality=%s): %w", preset.Name, err)
		}
		aid, result, err = l.fetchAid(channel, channelInfo.BroadNo, preset.Name)
		if err != nil {
			return nil, fmt.Errorf("重登后再次获取 Soop AID 失败(quality=%s): %w", preset.Name, err)
		}
	}
	if result != channelResultOK || aid == "" {
		return nil, fmt.Errorf("申请 Soop AID 失败: quality=%s, reason=%s", preset.Name, explainChannelResult(result))
	}
	l.GetLogger().Debugf("Soop AID 申请成功: quality=%s aid_length=%d", preset.Name, len(aid))

	viewURL, err := l.fetchViewURL(channelInfo.RMD, channelInfo.CDN, channelInfo.BroadNo, preset.Name)
	if err != nil {
		return nil, fmt.Errorf("获取 Soop 播放地址失败(quality=%s): %w", preset.Name, err)
	}
	l.GetLogger().Debugf("Soop 调度成功: quality=%s view_url_host=%s", preset.Name, parseHostQuiet(viewURL))

	streamURL, err := appendQuery(viewURL, "aid", aid)
	if err != nil {
		return nil, fmt.Errorf("拼接 Soop 播放地址失败(quality=%s): %w", preset.Name, err)
	}
	u, err := url.Parse(streamURL)
	if err != nil {
		return nil, fmt.Errorf("解析 Soop 播放地址失败(quality=%s): %w", preset.Name, err)
	}

	stream := l.buildStreamInfo(preset)
	stream.Url = u
	l.GetLogger().Debugf("Soop 可用流已加入: quality=%s format=hls", stream.Quality)
	return stream, nil
}

// buildStreamInfo 用清晰度元信息构造流条目；不带 Url 的条目只用于面板展示候选。
func (l *Live) buildStreamInfo(preset viewPreset) *live.StreamUrlInfo {
	qualityLabel := preset.Label
	if qualityLabel == "" {
		qualityLabel = preset.Name
	}
	return &live.StreamUrlInfo{
		Name:        qualityLabel,
		Description: qualityLabel,
		Quality:     qualityLabel,
		Format:      "hls",
		Height:      preset.LabelResolution,
		Bitrate:     preset.BPS,
		AttributesForStreamSelect: map[string]string{
			"format":      "hls",
			"quality_key": preset.Name,
		},
		HeadersForDownloader: l.getHeadersForDownloader(),
	}
}

// ListStreamCandidates 实现 live.DeferredStreamResolver：只走"播放页 + 播放信息"两步，
// 逐档不再各自申请 aid 与调度结果。录制每次只用一路，其余档位在这里以元信息形式给出，
// 面板的清晰度列表因此不受收窄影响。
func (l *Live) ListStreamCandidates() ([]*live.StreamUrlInfo, error) {
	_, _, presets, err := l.resolveRecordingContext()
	if err != nil {
		return nil, err
	}
	candidates := make([]*live.StreamUrlInfo, 0, len(presets))
	for _, preset := range presets {
		candidates = append(candidates, l.buildStreamInfo(preset))
	}
	if len(candidates) == 0 {
		return nil, fmt.Errorf("soop 未返回任何可用清晰度")
	}
	l.GetLogger().Debugf("Soop 清晰度候选已列出: count=%d first=%s", len(candidates), candidates[0].Quality)
	return candidates, nil
}

// ResolveStreamCandidate 实现 live.DeferredStreamResolver：只解析传入这一路的播放地址。
func (l *Live) ResolveStreamCandidate(candidate *live.StreamUrlInfo) error {
	if candidate == nil {
		return fmt.Errorf("soop 候选清晰度为空")
	}
	meta, channelInfo, presets, err := l.resolveRecordingContext()
	if err != nil {
		return err
	}
	qualityKey := candidate.AttributesForStreamSelect["quality_key"]
	for _, preset := range presets {
		if !sameSoopPreset(preset, qualityKey, candidate.Quality) {
			continue
		}
		stream, err := l.resolvePresetStream(meta.Channel, channelInfo, preset)
		if err != nil {
			return err
		}
		candidate.Url = stream.Url
		candidate.HeadersForDownloader = stream.HeadersForDownloader
		return nil
	}
	return fmt.Errorf("soop 候选清晰度 %s(quality_key=%s) 已不在当前直播的画质列表中", candidate.Quality, qualityKey)
}

// sameSoopPreset 按 quality_key 优先、清晰度标签兜底定位档位。
// quality_key 是平台给的内部名（如 HD、SD），同一场内稳定；面板上的旧条目若已换场，
// 两个条件都不匹配时会返回"不在列表中"错误，由调用方回退全量解析。
func sameSoopPreset(preset viewPreset, qualityKey, qualityLabel string) bool {
	if qualityKey != "" {
		return strings.EqualFold(preset.Name, qualityKey)
	}
	label := preset.Label
	if label == "" {
		label = preset.Name
	}
	return label == qualityLabel
}

func (l *Live) GetPlatformCNName() string {
	return cnName
}

func (l *Live) UpdateLiveOptionsbyConfig(ctx context.Context, room *configs.LiveRoom) error {
	l.optionsMu.Lock()
	defer l.optionsMu.Unlock()
	previousOptions := l.BaseLive.GetOptions()
	if err := l.BaseLive.UpdateLiveOptionsbyConfig(ctx, room); err != nil {
		return err
	}

	if !live.SameRecordingOptions(previousOptions, l.BaseLive.GetOptions()) || live.IsForcedAuthRefresh(ctx) {
		l.resetRuntimeState()
	}
	return nil
}

func (l *Live) GetOptions() *live.Options {
	l.optionsMu.RLock()
	defer l.optionsMu.RUnlock()
	return l.BaseLive.GetOptions()
}

// fetchPageMeta 从播放页 HTML 中提取频道名、bno、主播名、标题等基础信息。
// 这是当前后续 API 请求的前置步骤，因为 Soop 的很多接口都依赖 channel + bno。
//
// 解析规则：
// 1. 页面给出有效 nBroadNo 时，优先使用页面值；
// 2. 页面明确给出 nBroadNo=null，或最终 URL 已变为 /null 时，视为页面确认离线，不再回退到路径 broadNo；
// 3. 只有页面字段缺失时，才允许回退到 URL 路径中的 broadNo。
func (l *Live) fetchPageMeta() (*pageMeta, error) {
	l.GetLogger().Debugf("Soop 请求播放页: url=%s", l.GetRawUrl())
	resp, err := l.RequestSession.Get(
		l.Url.String(),
		requests.Headers(l.getHeadersForRequest()),
		requests.Cookies(l.getCookieMap()),
	)
	if err != nil {
		return nil, fmt.Errorf("请求 Soop 播放页失败: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		// 复用的访客标识若被服务端拒绝，丢掉它让下一次退化为全新匿名访问（即持久化之前的行为），
		// 避免一个失效的 _au 长期挡住该 host 上所有房间的请求。
		soopVisitorCookieJar.clear(l.Url.Host)
		return nil, fmt.Errorf("soop 播放页返回异常状态码: %d", resp.StatusCode)
	}
	soopVisitorCookieJar.remember(l.Url.Host, resp.Cookies())

	body, err := resp.Text()
	if err != nil {
		return nil, fmt.Errorf("读取 Soop 播放页响应失败: %w", err)
	}

	channel, pathBroadNo, pathExplicitlyOffline, err := parseChannelAndBroadNoStateFromURL(l.Url)
	if err != nil {
		return nil, fmt.Errorf("解析 Soop 房间 URL 失败: %w", err)
	}
	if finalURL := getFinalResponseURL(resp); finalURL != nil {
		if finalChannel, finalPathBroadNo, finalPathExplicitlyOffline, finalErr := parseChannelAndBroadNoStateFromURL(finalURL); finalErr == nil && finalChannel == channel {
			pathBroadNo = finalPathBroadNo
			pathExplicitlyOffline = finalPathExplicitlyOffline
		}
	}
	pageBroadNo, pageBroadNoFound := parseBroadNoFromPage(body)
	broadNo, isLiving, pageExplicitlyOffline := resolvePageBroadNo(pathBroadNo, pageBroadNo, pageBroadNoFound, pathExplicitlyOffline)

	hostName := parseWindowString(body, "szBjNick")
	roomName := parseWindowString(body, "szBroadTitle")

	meta := &pageMeta{
		Channel:               channel,
		BroadNo:               broadNo,
		PathBroadNo:           pathBroadNo,
		PageBroadNo:           pageBroadNo,
		PageBroadNoFound:      pageBroadNoFound,
		PageExplicitlyOffline: pageExplicitlyOffline,
		HostName:              hostName,
		RoomName:              roomName,
		IsLiving:              isLiving,
	}
	l.GetLogger().Debugf("Soop 页面元信息: channel=%s pathBroadNo=%s pageBroadNo=%s pageBroadNoFound=%v explicitOffline=%v resolvedBroadNo=%s host=%s room=%s living=%v",
		channel, pathBroadNo, pageBroadNo, pageBroadNoFound, pageExplicitlyOffline, broadNo, hostName, roomName, meta.IsLiving)
	return meta, nil
}

// fetchPageMetaCached 在 pageMetaReuseTTL 窗口内复用"已成功且页面判定开播"的元信息。
// 同一场直播里监控判活和紧随其后的取流会连续读播放页，复用能省掉其中一次整页下载
// （播放页实测单页 170KB 以上，且平台没有给出任何可缓存的响应头）。
// 明确离线、字段缺失和请求失败一律不缓存，保证下播与异常能被立刻感知。
func (l *Live) fetchPageMetaCached() (*pageMeta, error) {
	l.reuseMu.Lock()
	cached := l.pageMetaCache
	fresh := cached != nil && time.Since(l.pageMetaCachedAt) <= pageMetaReuseTTL
	l.reuseMu.Unlock()
	if fresh {
		l.GetLogger().Debugf("Soop 复用播放页元信息: channel=%s broadNo=%s", cached.Channel, cached.BroadNo)
		return cached, nil
	}

	meta, err := l.fetchPageMeta()
	if err != nil {
		return nil, err
	}
	if meta.IsLiving {
		l.reuseMu.Lock()
		l.pageMetaCache = meta
		l.pageMetaCachedAt = time.Now()
		l.reuseMu.Unlock()
	}
	return meta, nil
}

// resolveChannelInfoCached 在 channelInfoReuseTTL 窗口内复用 result 正常的播放信息。
// 需要登录（-6）、已下线（0）等地区/风控异常从不缓存，仍按原逻辑重新解析。
func (l *Live) resolveChannelInfoCached(channel, broadNo string) (*channelInfo, error) {
	key := channelInfoKey(channel, broadNo)
	l.reuseMu.Lock()
	cached := l.channelCache
	fresh := cached != nil && l.channelCacheKey == key && time.Since(l.channelCachedAt) <= channelInfoReuseTTL
	l.reuseMu.Unlock()
	if fresh {
		l.GetLogger().Debugf("Soop 复用播放信息: channel=%s broadNo=%s presets=%d", channel, broadNo, len(cached.ViewPresets))
		// 调用方会按画质优先级就地排序，返回副本避免污染缓存
		return copyChannelInfo(cached), nil
	}

	info, err := l.resolveChannelInfo(channel, broadNo)
	if err != nil {
		return nil, err
	}
	l.reuseMu.Lock()
	if info.Result == channelResultOK {
		l.channelCache = info
		l.channelCacheKey = key
		l.channelCachedAt = time.Now()
	} else {
		// 状态已经转为需要登录或下线，旧的正常结果不能再复用
		l.channelCache = nil
		l.channelCacheKey = ""
	}
	l.reuseMu.Unlock()
	// 缓存里保存原始对象，返回副本给调用方，避免就地排序污染缓存
	return copyChannelInfo(info), nil
}

func channelInfoKey(channel, broadNo string) string {
	return channel + "|" + broadNo
}

func copyChannelInfo(info *channelInfo) *channelInfo {
	copied := *info
	copied.ViewPresets = append([]viewPreset(nil), info.ViewPresets...)
	return &copied
}

// clearReuseCache 丢弃全部短期复用结果，供登录态变化和房间配置更新时调用。
func (l *Live) clearReuseCache() {
	l.reuseMu.Lock()
	defer l.reuseMu.Unlock()
	l.pageMetaCache = nil
	l.pageMetaCachedAt = time.Time{}
	l.channelCache = nil
	l.channelCacheKey = ""
}

// fetchChannelInfo 调用 player_live_api.php(type=live) 获取房间主信息。
// 返回值同时为 GetInfo 和 GetStreamInfos 服务。
// 当前版本未实现密码房输入，因此这里固定以空密码请求。
func (l *Live) fetchChannelInfo(channel, broadNo string) (*channelInfo, error) {
	if channel == "" || broadNo == "" {
		return nil, fmt.Errorf("soop 房间标识不完整：channel=%q broadNo=%q", channel, broadNo)
	}

	l.GetLogger().Debugf("Soop 请求播放信息接口: type=live channel=%s broadNo=%s cookie_count=%d",
		channel, broadNo, len(l.getCookieMap()))
	resp, err := l.RequestSession.Post(
		channelAPIURL,
		requests.Form(map[string]string{
			"from_api":    "0",
			"mode":        "landing",
			"player_type": "html5",
			"stream_type": "common",
			"type":        "live",
			"bid":         channel,
			"bno":         broadNo,
			"pwd":         "",
		}),
		requests.Headers(l.getHeadersForRequest()),
		requests.Cookies(l.getCookieMap()),
	)
	if err != nil {
		return nil, fmt.Errorf("请求 Soop 播放信息接口失败: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("soop 播放信息接口返回异常状态码: %d", resp.StatusCode)
	}

	body, err := resp.Bytes()
	if err != nil {
		return nil, fmt.Errorf("读取 Soop 播放信息响应失败: %w", err)
	}

	result := int(gjson.GetBytes(body, "CHANNEL.RESULT").Int())
	info := &channelInfo{
		Result:   result,
		BroadNo:  gjson.GetBytes(body, "CHANNEL.BNO").String(),
		HostName: gjson.GetBytes(body, "CHANNEL.BJNICK").String(),
		RoomName: gjson.GetBytes(body, "CHANNEL.TITLE").String(),
		RMD:      gjson.GetBytes(body, "CHANNEL.RMD").String(),
		CDN:      gjson.GetBytes(body, "CHANNEL.CDN").String(),
		NeedPwd:  strings.EqualFold(gjson.GetBytes(body, "CHANNEL.BPWD").String(), "Y"),
	}

	viewPresetResult := gjson.GetBytes(body, "CHANNEL.VIEWPRESET")
	if viewPresetResult.Exists() {
		for _, item := range viewPresetResult.Array() {
			labelResolution, _ := strconv.Atoi(item.Get("label_resolution").String())
			info.ViewPresets = append(info.ViewPresets, viewPreset{
				Label:           item.Get("label").String(),
				Name:            item.Get("name").String(),
				LabelResolution: labelResolution,
				BPS:             int(item.Get("bps").Int()),
			})
		}
	}

	l.GetLogger().Debugf("Soop 播放信息接口完成: result=%d host=%s room=%s broadNo=%s rmd=%s cdn=%s presets=%d needPwd=%v",
		info.Result, info.HostName, info.RoomName, info.BroadNo, info.RMD, info.CDN, len(info.ViewPresets), info.NeedPwd)
	return info, nil
}

// fetchAid 调用 player_live_api.php(type=aid) 为指定清晰度申请播放凭证。
// 当前版本未实现密码房输入，因此这里固定以空密码请求。
func (l *Live) fetchAid(channel, broadNo, quality string) (string, int, error) {
	l.GetLogger().Debugf("Soop 请求 AID: channel=%s broadNo=%s quality=%s cookie_count=%d",
		channel, broadNo, quality, len(l.getCookieMap()))
	resp, err := l.RequestSession.Post(
		channelAPIURL,
		requests.Form(map[string]string{
			"from_api":    "0",
			"mode":        "landing",
			"player_type": "html5",
			"stream_type": "common",
			"type":        "aid",
			"bid":         channel,
			"bno":         broadNo,
			"pwd":         "",
			"quality":     quality,
		}),
		requests.Headers(l.getHeadersForRequest()),
		requests.Cookies(l.getCookieMap()),
	)
	if err != nil {
		return "", 0, fmt.Errorf("请求 Soop AID 接口失败: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return "", 0, fmt.Errorf("soop AID 接口返回异常状态码: %d", resp.StatusCode)
	}

	body, err := resp.Bytes()
	if err != nil {
		return "", 0, fmt.Errorf("读取 Soop AID 响应失败: %w", err)
	}
	aid := gjson.GetBytes(body, "CHANNEL.AID").String()
	result := int(gjson.GetBytes(body, "CHANNEL.RESULT").Int())
	l.GetLogger().Debugf("Soop AID 响应: quality=%s result=%d aid_length=%d", quality, result, len(aid))
	return aid, result, nil
}

// fetchViewURL 根据 RMD 调度节点、CDN 类型和 quality 获取最终 m3u8 地址。
func (l *Live) fetchViewURL(rmd, cdn, broadNo, quality string) (string, error) {
	if rmd == "" {
		return "", fmt.Errorf("soop 调度节点为空，无法获取播放地址")
	}

	l.GetLogger().Debugf("Soop 请求调度接口: rmd=%s cdn=%s broadNo=%s quality=%s return_type=%s",
		rmd, cdn, broadNo, quality, mapCDNType(cdn))
	resp, err := l.RequestSession.Get(
		strings.TrimRight(rmd, "/")+"/broad_stream_assign.html",
		requests.Query("return_type", mapCDNType(cdn)),
		requests.Query("broad_key", buildBroadKey(broadNo, quality)),
		requests.Headers(l.getHeadersForRequest()),
		requests.Cookies(l.getCookieMap()),
	)
	if err != nil {
		return "", fmt.Errorf("请求 Soop 调度接口失败: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("soop 调度接口返回异常状态码: %d", resp.StatusCode)
	}

	body, err := resp.Bytes()
	if err != nil {
		return "", fmt.Errorf("读取 Soop 调度接口响应失败: %w", err)
	}

	viewURL := gjson.GetBytes(body, "view_url").String()
	if viewURL == "" {
		return "", fmt.Errorf("soop 调度接口未返回 view_url，当前 CDN=%q quality=%q", cdn, quality)
	}
	l.GetLogger().Debugf("Soop 调度接口响应成功: quality=%s stream_status=%s view_url_host=%s",
		quality, gjson.GetBytes(body, "stream_status").String(), parseHostQuiet(viewURL))
	return viewURL, nil
}

// getHeadersForDownloader 返回下载 m3u8/分片时需要携带的固定请求头。
// Soop 对 Referer/Origin 较敏感，缺少这些头部时容易出现 403 或空播放列表。
func (l *Live) getHeadersForDownloader() map[string]string {
	return map[string]string{
		"User-Agent":      "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/133.0.0.0 Safari/537.36",
		"Referer":         l.Url.String(),
		"Origin":          defaultOrigin,
		"Accept":          "application/json, text/plain, */*",
		"Accept-Language": "ko-KR,ko;q=0.9,en-US;q=0.8,en;q=0.7",
	}
}

func (l *Live) getHeadersForRequest() map[string]any {
	headers := l.getHeadersForDownloader()
	result := make(map[string]any, len(headers))
	for key, value := range headers {
		result[key] = value
	}
	return result
}

// soopVisitorCookieNames 是允许在进程内粘滞复用的访客 cookie 白名单。
// 实测匿名请求播放页时，服务端首轮下发全新的 _au（长度 32），之后每一轮都再发一个新的；
// 把上一轮的 _au 回传，服务端就不再下发 _au（连同 _au3rd 一起停发），说明这个访客标识是可复用的。
// 复用它就不必每次轮询都向平台申报一个"全新访客"，减少大批量房间下的异常特征。
// 注意生效范围：配置里带了 play 域的登录 cookie 时，服务端压根不下发 _au，这里存不下来也是空转，
// 只有匿名录制形态（未登录或该 host 没有 cookie）才会真正复用。
// 白名单外的名字一律不进存储：SESSION 等登录凭证必须继续走显式配置和登录流程，
// 不能让一次普通页面响应把它们变成进程内的隐式状态。
var soopVisitorCookieNames = []string{"_au"}

// visitorCookieStore 按 host 保存进程内的访客 cookie，不落盘、不进配置、重启即失效。
type visitorCookieStore struct {
	mu     sync.RWMutex
	byHost map[string]map[string]string
}

func (s *visitorCookieStore) remember(host string, cookies []*http.Cookie) {
	if host == "" {
		return
	}
	kept := make(map[string]string, len(soopVisitorCookieNames))
	for _, cookie := range cookies {
		if cookie == nil || !isSoopVisitorCookie(cookie.Name) {
			continue
		}
		// 空值是服务端"清除该 cookie"的写法，留下只会一直发一个无效访客标识
		if cookie.Value == "" {
			continue
		}
		kept[cookie.Name] = cookie.Value
	}
	if len(kept) == 0 {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.byHost == nil {
		s.byHost = make(map[string]map[string]string)
	}
	current := s.byHost[host]
	if current == nil {
		current = make(map[string]string, len(kept))
		s.byHost[host] = current
	}
	for name, value := range kept {
		current[name] = value
	}
}

func (s *visitorCookieStore) get(host string) map[string]string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	stored := s.byHost[host]
	if len(stored) == 0 {
		return nil
	}
	// 返回副本，调用方合并 cookie 时不会改到共享状态
	result := make(map[string]string, len(stored))
	for name, value := range stored {
		result[name] = value
	}
	return result
}

func (s *visitorCookieStore) clear(host string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.byHost, host)
}

func isSoopVisitorCookie(name string) bool {
	for _, allowed := range soopVisitorCookieNames {
		if name == allowed {
			return true
		}
	}
	return false
}

var soopVisitorCookieJar visitorCookieStore

// getCookieMap 将运行时 CookieJar 和配置文件中保存的 Cookie 合并成请求使用的 map。
// 这样可以同时兼容：
// 1. 本次运行中通过登录接口注入的 Cookie；
// 2. 历史配置文件里持久化的 Cookie。
// 3. 本次运行中从平台响应里收到的访客标识（_au），仅在用户没有显式给出同名 cookie 时补上。
func (l *Live) getCookieMap() map[string]string {
	runtimeCookie, ignoreStoredCookie := l.getRuntimeState()
	if ignoreStoredCookie && strings.TrimSpace(runtimeCookie) == "" {
		return map[string]string{}
	}

	options := l.GetOptions()
	cookies := options.Cookies.Cookies(l.Url)
	cookieMap := make(map[string]string, len(cookies))
	for _, item := range cookies {
		cookieMap[item.Name] = item.Value
	}

	if cfg := configs.GetCurrentConfig(); cfg != nil && cfg.Cookies != nil {
		for _, rawCookie := range []string{
			cfg.Cookies[l.Url.Host],
			cfg.Cookies[domainPlaySoop],
		} {
			if rawCookie == "" {
				continue
			}
			for name, value := range parseCookieString(rawCookie) {
				cookieMap[name] = value
			}
		}
	}
	if runtimeCookie != "" {
		for name, value := range parseCookieString(runtimeCookie) {
			cookieMap[name] = value
		}
	}
	// 访客标识优先级最低：用户或登录流程给出的同名 cookie 永远赢过进程内缓存。
	for name, value := range soopVisitorCookieJar.get(l.Url.Host) {
		if existing, exists := cookieMap[name]; !exists || existing == "" {
			cookieMap[name] = value
		}
	}
	return cookieMap
}

// resolveChannelInfo 是 Soop 获取频道信息的统一入口。
// 它在真正请求播放信息前，会先做一次 Cookie 预检和必要的自动重登。
// 这里的“播放信息前”指的是 Soop API 请求前，不包括更早一步的页面元信息抓取。
func (l *Live) resolveChannelInfo(channel, broadNo string) (*channelInfo, error) {
	l.GetLogger().Debugf("Soop 开始解析频道信息: channel=%s broadNo=%s", channel, broadNo)
	if err := l.tryVerifyAndReloginIfNeeded(); err != nil {
		l.logRetryDetail(err, "Soop 登录态预检失败")
	}

	info, err := l.fetchChannelInfo(channel, broadNo)
	if err != nil {
		return nil, err
	}
	if info.Result == channelResultLogin {
		l.GetLogger().Debugf("Soop 播放信息提示需要登录，准备自动重登: channel=%s broadNo=%s", channel, broadNo)
		if err := l.tryAutoLogin(); err != nil {
			return nil, fmt.Errorf("soop 需要登录态，但自动重登失败: %w", err)
		}
		info, err = l.fetchChannelInfo(channel, broadNo)
		if err != nil {
			return nil, err
		}
	}
	l.GetLogger().Debugf("Soop 频道信息解析完成: channel=%s broadNo=%s result=%d", channel, broadNo, info.Result)
	return info, nil
}

// tryVerifyAndReloginIfNeeded 在真正访问 Soop 播放接口前预检查登录态。
// 如果已有 Cookie 失效且配置中存在账号密码，则自动重新登录，失败时把错误上抛。
// 这里不会回溯重抓播放页，因此只影响后续 API 调用，不改变当前页面解析步骤的行为。
func (l *Live) tryVerifyAndReloginIfNeeded() error {
	cookie := l.getPrimaryCookieString()
	cfg := configs.GetCurrentConfig()
	hasCredential := cfg != nil && strings.TrimSpace(cfg.SoopLiveAuth.Username) != "" && strings.TrimSpace(cfg.SoopLiveAuth.Password) != ""
	runtimeCookie, ignoreStoredCookie := l.getRuntimeState()
	l.GetLogger().Debugf("Soop 登录态预检开始: hasCookie=%v hasCredential=%v ignoreStoredCookie=%v runtimeCookie=%v",
		cookie != "", hasCredential, ignoreStoredCookie, strings.TrimSpace(runtimeCookie) != "")

	if cookie == "" {
		if hasCredential {
			l.GetLogger().Debug("Soop 未找到可用 Cookie，但存在账号密码，尝试自动登录")
			return l.tryAutoLogin()
		}
		l.setIgnoreStoredCookie(false)
		l.GetLogger().Debug("Soop 未找到 Cookie，且未配置账号密码，将以匿名方式访问")
		return nil
	}

	verifyResult, err := verifyCookieWithCache(cookie)
	if err == nil && verifyResult != nil && verifyResult.IsLogin {
		l.setIgnoreStoredCookie(false)
		l.GetLogger().Debugf("Soop 登录态预检通过: loginID=%s", verifyResult.LoginID)
		return nil
	}
	if err != nil {
		l.setIgnoreStoredCookie(false)
		l.GetLogger().WithError(err).Warn("Soop Cookie 校验接口异常，继续使用当前 Cookie 访问")
		return nil
	}
	if !hasCredential {
		// 对普通房间来说，失效 Cookie 不应阻塞匿名访问。
		// 只有明确校验结果为未登录时，后续请求才暂时忽略配置中的 Cookie，按未登录模式访问。
		l.setIgnoreStoredCookie(true)
		l.GetLogger().Warn("Soop 已保存 Cookie 已失效，将降级为匿名访问")
		l.GetLogger().Debug("Soop 普通房间将忽略已失效 Cookie，继续尝试匿名访问")
		return nil
	}
	// 配了账号密码就说明这个房间需要登录态（19+ 房间匿名必定取不到流），
	// 因此这里绝不降级匿名：把失效 Cookie 留着、把错误上抛，让外层轮询退避生效并在面板上显示失败原因。
	l.setIgnoreStoredCookie(false)
	l.GetLogger().Debug("Soop Cookie 无效，但已配置账号密码，准备自动登录")
	return l.tryAutoLogin()
}

// tryAutoLogin 使用配置文件中的 Soop 账号密码重新换取 Cookie。
// 登录成功后会：
// 1. 回写配置中的 Soop Cookie；
// 2. 更新当前 Live 的 CookieJar；
// 3. 让后续同一轮请求立即生效。
func (l *Live) tryAutoLogin() error {
	cfg := configs.GetCurrentConfig()
	if cfg == nil {
		return fmt.Errorf("当前配置未加载，无法执行 Soop 自动登录")
	}
	username := strings.TrimSpace(cfg.SoopLiveAuth.Username)
	password := strings.TrimSpace(cfg.SoopLiveAuth.Password)
	if username == "" || password == "" {
		return fmt.Errorf("未配置 Soop 账号密码，无法执行自动登录")
	}
	// 同一账号的自动登录按 autoLoginCooldown 节流，失败与"登录成功但仍判未登录"都算一次尝试。
	l.GetLogger().Debugf("Soop 自动登录开始: username=%s", username)

	result, err := autoLoginWithCooldown(username, password)
	if err != nil {
		// 被冷却挡住说明这一路根本没发登录请求，而同账号的别的房间可能刚刚已经换到新 Cookie
		// 并写进了配置。先验一次配置里那份，可用就直接采用；不能因为"这个账号十分钟内登录过了"
		// 就让这个房间继续举着自家的旧运行态干等下一个登录窗口。
		if errors.Is(err, errAutoLoginCoolingDown) && l.adoptPersistedCookie() {
			return nil
		}
		l.GetLogger().WithError(err).Debug("Soop 自动登录失败")
		return err
	}
	if result == nil {
		return fmt.Errorf("soop 自动登录未返回结果")
	}
	if result.Cookie == "" {
		return fmt.Errorf("soop 自动登录成功，但未获得可用 Cookie")
	}
	if !result.Verify.IsLogin {
		return fmt.Errorf("soop 自动登录成功，但登录态校验未通过")
	}

	if err := persistSoopCookieWithSingleflight(result.Cookie); err != nil {
		l.GetLogger().WithError(err).Warn("更新 Soop Cookie 到配置失败")
	}
	l.applySessionCookie(result.Cookie)
	l.GetLogger().Debugf("Soop 自动登录成功: loginID=%s cookie_length=%d", result.Verify.LoginID, len(result.Cookie))

	return nil
}

// applySessionCookie 把一份已经可用的登录 Cookie 装到当前实例：运行态、短期复用缓存与 Options。
func (l *Live) applySessionCookie(cookie string) {
	l.setRuntimeState(cookie, false)
	// 换了登录身份，旧会话抓到的播放页与播放信息一律不再复用
	l.clearReuseCache()

	options := l.GetOptions()
	for _, targetURL := range []*url.URL{playSoopURL, l.Url} {
		if targetURL == nil {
			continue
		}
		live.WithKVStringCookies(targetURL, cookie)(options)
	}
}

// adoptPersistedCookie 尝试采用配置里当前保存的 Cookie，成功返回 true。
// 只在自动登录被冷却挡住时调用：本实例的运行态刚被验过一次不通过，
// 配置里那份与它不同才值得再验一次，相同就没必要重复请求校验接口。
func (l *Live) adoptPersistedCookie() bool {
	cfg := configs.GetCurrentConfig()
	if cfg == nil || cfg.Cookies == nil {
		return false
	}
	candidate := strings.TrimSpace(cfg.Cookies[l.Url.Host])
	if candidate == "" {
		candidate = strings.TrimSpace(cfg.Cookies[domainPlaySoop])
	}
	if candidate == "" || candidate == strings.TrimSpace(l.getRuntimeCookie()) {
		return false
	}
	result, err := verifyCookieWithCache(candidate)
	if err != nil || result == nil || !result.IsLogin {
		return false
	}
	l.applySessionCookie(candidate)
	l.GetLogger().Infof("冷却期内直接采用配置中已刷新的 Soop Cookie: loginID=%s", result.LoginID)
	return true
}

func persistSoopCookieWithSingleflight(cookie string) error {
	cookie = strings.TrimSpace(cookie)
	if cookie == "" {
		return nil
	}

	_, err, _ := persistCookieGroup.Do(cookie, func() (any, error) {
		_, err := setCookiesFunc(map[string]string{
			domainPlaySoop: cookie,
		})
		return nil, err
	})
	return err
}

// getPrimaryCookieString 返回当前最优先使用的 Soop Cookie 字符串。
// 优先级：
// 1. 当前房间 host 对应的 Cookie；
// 2. play.sooplive.com；
// 3. 无。
func (l *Live) getPrimaryCookieString() string {
	if cookie := strings.TrimSpace(l.getRuntimeCookie()); cookie != "" {
		return cookie
	}

	cfg := configs.GetCurrentConfig()
	options := l.GetOptions()
	if cfg == nil || cfg.Cookies == nil {
		return buildCookieStringFromCookies(options.Cookies.Cookies(l.Url))
	}
	if cookie := strings.TrimSpace(cfg.Cookies[l.Url.Host]); cookie != "" {
		return cookie
	}
	if cookie := strings.TrimSpace(cfg.Cookies[domainPlaySoop]); cookie != "" {
		return cookie
	}
	return buildCookieStringFromCookies(options.Cookies.Cookies(l.Url))
}

func (l *Live) getRuntimeState() (string, bool) {
	l.stateMu.RLock()
	defer l.stateMu.RUnlock()
	return l.runtimeCookie, l.ignoreStoredCookie
}

func (l *Live) getRuntimeCookie() string {
	l.stateMu.RLock()
	defer l.stateMu.RUnlock()
	return l.runtimeCookie
}

func (l *Live) setRuntimeState(cookie string, ignoreStoredCookie bool) {
	l.stateMu.Lock()
	defer l.stateMu.Unlock()
	l.runtimeCookie = cookie
	l.ignoreStoredCookie = ignoreStoredCookie
}

func (l *Live) setIgnoreStoredCookie(ignoreStoredCookie bool) {
	l.stateMu.Lock()
	defer l.stateMu.Unlock()
	l.ignoreStoredCookie = ignoreStoredCookie
}

func (l *Live) resetRuntimeState() {
	l.setRuntimeState("", false)
	// 登录态或房间配置已经变化，之前复用的播放页与播放信息都可能不再成立
	l.clearReuseCache()
}

func buildCookieStringFromCookies(cookies []*http.Cookie) string {
	if len(cookies) == 0 {
		return ""
	}

	parts := make([]string, 0, len(cookies))
	for _, cookie := range cookies {
		if cookie == nil || strings.TrimSpace(cookie.Name) == "" {
			continue
		}
		parts = append(parts, fmt.Sprintf("%s=%s", cookie.Name, cookie.Value))
	}
	return strings.Join(parts, "; ")
}

// parseChannelAndBroadNoFromURL 从 Soop URL 中提取频道名与可选的 broadNo。
// 支持：
// - /channel
// - /channel/123456789
func parseChannelAndBroadNoFromURL(u *url.URL) (string, string, error) {
	channel, broadNo, _, err := parseChannelAndBroadNoStateFromURL(u)
	return channel, broadNo, err
}

// parseChannelAndBroadNoStateFromURL 额外识别 Soop 的 /channel/null 下播路径。
func parseChannelAndBroadNoStateFromURL(u *url.URL) (string, string, bool, error) {
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(parts) == 0 || parts[0] == "" {
		return "", "", false, live.ErrRoomUrlIncorrect
	}

	channel := parts[0]
	broadNo := ""
	if len(parts) > 1 {
		if strings.EqualFold(parts[1], "null") {
			return channel, "", true, nil
		}
		if _, err := strconv.ParseInt(parts[1], 10, 64); err == nil {
			broadNo = parts[1]
		}
	}

	return channel, broadNo, false, nil
}

// parseBroadNoFromPage 从页面脚本中的 window.nBroadNo 提取直播场次号。
// 第二个返回值表示“该字段是否存在”，用于区分：
// - 页面里就是 null（通常是未开播）
// - 页面结构变化导致根本找不到字段
func parseBroadNoFromPage(body string) (string, bool) {
	match := reWindowBroadNo.FindStringSubmatch(body)
	if len(match) < 2 {
		return "", false
	}
	if strings.EqualFold(match[1], "null") {
		return "", true
	}
	return match[1], true
}

// resolvePageBroadNo 统一处理页面 broadNo 与路径 broadNo 的优先级。
// 返回值含义：
// 1. resolvedBroadNo: 当前后续 API 应使用的 broadNo；
// 2. isLiving: 当前是否仍存在“可继续请求后续 API 的在线候选”；
// 3. explicitlyOffline: 页面是否明确给出 nBroadNo=null，或最终 URL 已落到 /null。
func resolvePageBroadNo(pathBroadNo, pageBroadNo string, pageBroadNoFound, pathExplicitlyOffline bool) (string, bool, bool) {
	switch {
	case pageBroadNoFound && pageBroadNo != "":
		return pageBroadNo, true, false
	case pageBroadNoFound:
		return "", false, true
	case pathExplicitlyOffline:
		return "", false, true
	case pathBroadNo != "":
		return pathBroadNo, true, false
	default:
		return "", false, false
	}
}

func getFinalResponseURL(resp *requests.Response) *url.URL {
	if resp == nil || resp.Request == nil || resp.Request.URL == nil {
		return nil
	}
	return resp.Request.URL
}

// parseWindowString 从页面脚本中的 window.xxx = '...'/ "..." 提取字符串变量。
func parseWindowString(body, varName string) string {
	doubleQuotePattern := fmt.Sprintf(`window\.%s\s*=\s*"((?:\\.|[^"\\])*)"`, regexp.QuoteMeta(varName))
	if matched := utils.Match1(doubleQuotePattern, body); matched != "" {
		return decodeEscapedString(matched)
	}

	singleQuotePattern := fmt.Sprintf(`window\.%s\s*=\s*'((?:\\.|[^'\\])*)'`, regexp.QuoteMeta(varName))
	if matched := utils.Match1(singleQuotePattern, body); matched != "" {
		return decodeEscapedString(matched)
	}

	return ""
}

// decodeEscapedString 将 JS 字符串字面量中的转义序列还原为正常文本。
func decodeEscapedString(raw string) string {
	raw = strings.ReplaceAll(raw, `\'`, `'`)
	quoted := `"` + strings.ReplaceAll(raw, `"`, `\"`) + `"`
	if decoded, err := strconv.Unquote(quoted); err == nil {
		return utils.ParseString(decoded, utils.ParseUnicode, utils.UnescapeHTMLEntity)
	}
	return utils.ParseString(raw, utils.ParseUnicode, utils.UnescapeHTMLEntity)
}

// mapCDNType 兼容 Soop 调度接口要求的 return_type 参数命名。
func mapCDNType(cdn string) string {
	switch {
	case strings.Contains(cdn, "gs_cdn"):
		return "gs_cdn_pc_web"
	case strings.Contains(cdn, "lg_cdn"):
		return "lg_cdn_pc_web"
	default:
		return cdn
	}
}

// buildBroadKey 按 Soop 当前约定拼接 broad_key。
func buildBroadKey(broadNo, quality string) string {
	return fmt.Sprintf("%s-common-%s-hls", broadNo, quality)
}

// appendQuery 向播放地址中追加 aid 等查询参数。
func appendQuery(rawURL, key, value string) (string, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return "", err
	}
	query := u.Query()
	query.Set(key, value)
	u.RawQuery = query.Encode()
	return u.String(), nil
}

// parseCookieString 将 "a=b; c=d" 形式的 Cookie 字符串拆成 map。
func parseCookieString(cookie string) map[string]string {
	result := make(map[string]string)
	for _, part := range strings.Split(cookie, ";") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		pairs := strings.SplitN(part, "=", 2)
		if len(pairs) != 2 {
			continue
		}
		result[strings.TrimSpace(pairs[0])] = strings.TrimSpace(pairs[1])
	}
	return result
}

func parseHostQuiet(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return ""
	}
	return u.Host
}

// sortViewPresetsByPriority 将 Soop 返回的清晰度列表按“最高画质优先”排序。
// Soop 接口返回的 VIEWPRESET 通常是从低到高排列，而 bililive-go 在未配置偏好时会默认取第一个流。
// 因此这里需要显式倒序，以保证默认录制最高画质。
func sortViewPresetsByPriority(presets []viewPreset) {
	sort.SliceStable(presets, func(i, j int) bool {
		if presets[i].LabelResolution != presets[j].LabelResolution {
			return presets[i].LabelResolution > presets[j].LabelResolution
		}
		if presets[i].BPS != presets[j].BPS {
			return presets[i].BPS > presets[j].BPS
		}
		return presets[i].Label > presets[j].Label
	})
}

// explainChannelResult 将 Soop 的业务码翻译成更可读的中文说明。
// 注意：Soop 对外没有稳定公开的错误码文档，这里的解释基于当前已知行为和实测现象。
func explainChannelResult(result int) string {
	switch result {
	case channelResultOK:
		return "成功"
	case channelResultLogin:
		return "需要登录"
	case channelResultEmpty:
		return "未返回有效直播信息（通常表示当前无可用播放信息）"
	case channelResultBlock:
		return "房间不可用或访问受限（通常是地区限制、风控或无效场次）"
	default:
		return fmt.Sprintf("未知业务码 %d", result)
	}
}

func explainChannelResultError(prefix string, result int) error {
	return fmt.Errorf("%s：%s", prefix, explainChannelResult(result))
}
