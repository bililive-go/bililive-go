package sooplive

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/hr3lxphr6j/requests"
	"github.com/tidwall/gjson"
	"golang.org/x/sync/singleflight"

	"github.com/bililive-go/bililive-go/src/configs"
	"github.com/bililive-go/bililive-go/src/live"
	"github.com/bililive-go/bililive-go/src/live/internal"
	"github.com/bililive-go/bililive-go/src/pkg/utils"
)

const (
	domainPlaySoop     = "play.sooplive.co.kr"
	cnName             = "SOOP"
	channelAPIURL      = "https://live.sooplive.co.kr/afreeca/player_live_api.php"
	defaultOrigin      = "https://play.sooplive.co.kr"
	channelResultOK    = 1
	channelResultLogin = -6
	channelResultEmpty = 0
	channelResultBlock = -2
)

var (
	reWindowBroadNo    = regexp.MustCompile(`window\.nBroadNo\s*=\s*(\d+|null);`)
	setCookiesFunc     = configs.SetCookies
	persistCookieGroup singleflight.Group
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
}

// pageMeta 表示从播放页 HTML 中直接提取的最小元信息。
// 这一步不依赖 Soop API，主要用于：
// 1. 在接口异常时提供基础房间信息；
// 2. 从页面脚本中兜底提取 nBroadNo；
// 3. 在未开播场景下快速返回。
type pageMeta struct {
	Channel  string
	BroadNo  string
	HostName string
	RoomName string
	IsLiving bool
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
	meta, err := l.fetchPageMeta()
	if err != nil {
		l.GetLogger().WithError(err).Debug("Soop GetInfo 失败：页面元信息获取失败")
		return nil, err
	}

	info := &live.Info{
		Live:      l,
		HostName:  meta.HostName,
		RoomName:  meta.RoomName,
		Status:    meta.IsLiving,
		AudioOnly: l.Options.AudioOnly,
	}

	// 页面上不存在 nBroadNo 时，通常表示未开播或页面结构异常。
	// 此时直接返回页面元信息，不再继续请求播放接口。
	if !meta.IsLiving {
		l.GetLogger().Debugf("Soop GetInfo 完成：页面判断当前未开播 channel=%s broadNo=%s", meta.Channel, meta.BroadNo)
		return info, nil
	}

	// 这里不再吞掉错误，而是显式返回，便于前端区分：
	// - 登录失效
	// - Soop API 异常
	// - 地区 / 风控限制
	channelInfo, err := l.resolveChannelInfo(meta.Channel, meta.BroadNo)
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
// 链路与 streamlink/soop.py 基本一致：
// 1. 拿页面元信息和 bno；
// 2. 解析频道信息（含登录态预检）；
// 3. 针对每个清晰度申请 aid；
// 4. 通过调度接口获取 view_url；
// 5. 将 view_url 与 aid 组合成最终可录制的 m3u8 地址。
func (l *Live) GetStreamInfos() ([]*live.StreamUrlInfo, error) {
	l.GetLogger().Debugf("Soop GetStreamInfos 开始: url=%s", l.GetRawUrl())
	meta, err := l.fetchPageMeta()
	if err != nil {
		l.GetLogger().WithError(err).Debug("Soop GetStreamInfos 失败：页面元信息获取失败")
		return nil, err
	}
	if !meta.IsLiving {
		return nil, fmt.Errorf("Soop 房间当前未开播，无法获取播放流")
	}

	channelInfo, err := l.resolveChannelInfo(meta.Channel, meta.BroadNo)
	if err != nil {
		l.GetLogger().WithError(err).Debugf("Soop GetStreamInfos 失败：解析频道信息失败 channel=%s broadNo=%s", meta.Channel, meta.BroadNo)
		return nil, err
	}
	if channelInfo.Result != channelResultOK {
		return nil, explainChannelResultError("Soop 播放信息接口返回异常", channelInfo.Result)
	}

	if channelInfo.BroadNo == "" || channelInfo.RMD == "" {
		return nil, fmt.Errorf("Soop 播放信息不完整：缺少 broadcast id 或调度节点地址")
	}

	headers := l.getHeadersForDownloader()
	sortViewPresetsByPriority(channelInfo.ViewPresets)
	streams := make([]*live.StreamUrlInfo, 0, len(channelInfo.ViewPresets))
	for _, preset := range channelInfo.ViewPresets {
		if strings.EqualFold(preset.Name, "auto") {
			continue
		}
		l.GetLogger().Debugf("Soop 开始处理清晰度: label=%s name=%s resolution=%d bps=%d", preset.Label, preset.Name, preset.LabelResolution, preset.BPS)

		aid, result, err := l.fetchAid(meta.Channel, channelInfo.BroadNo, preset.Name)
		if err != nil {
			l.GetLogger().WithError(err).Warnf("获取 Soop AID 失败: quality=%s", preset.Name)
			continue
		}
		if result == channelResultLogin {
			l.GetLogger().Warnf("Soop AID 申请提示需要登录，准备自动重登后重试: quality=%s", preset.Name)
			if err = l.tryAutoLogin(); err != nil {
				l.GetLogger().WithError(err).Warnf("Soop 自动重登失败，无法重试 AID: quality=%s", preset.Name)
				continue
			}
			aid, result, err = l.fetchAid(meta.Channel, channelInfo.BroadNo, preset.Name)
			if err != nil {
				l.GetLogger().WithError(err).Warnf("Soop 重登后再次获取 AID 失败: quality=%s", preset.Name)
				continue
			}
		}
		if result != channelResultOK || aid == "" {
			l.GetLogger().Warnf("Soop AID 申请失败: quality=%s, reason=%s, aid_empty=%v", preset.Name, explainChannelResult(result), aid == "")
			continue
		}
		l.GetLogger().Debugf("Soop AID 申请成功: quality=%s aid_length=%d", preset.Name, len(aid))

		viewURL, err := l.fetchViewURL(channelInfo.RMD, channelInfo.CDN, channelInfo.BroadNo, preset.Name)
		if err != nil {
			l.GetLogger().WithError(err).Warnf("获取 Soop 播放地址失败: quality=%s", preset.Name)
			continue
		}
		l.GetLogger().Debugf("Soop 调度成功: quality=%s view_url_host=%s", preset.Name, parseHostQuiet(viewURL))

		streamURL, err := appendQuery(viewURL, "aid", aid)
		if err != nil {
			l.GetLogger().WithError(err).Warnf("拼接 Soop 播放地址失败: quality=%s", preset.Name)
			continue
		}

		u, err := url.Parse(streamURL)
		if err != nil {
			l.GetLogger().WithError(err).Warnf("解析 Soop 播放地址失败: quality=%s", preset.Name)
			continue
		}

		qualityLabel := preset.Label
		if qualityLabel == "" {
			qualityLabel = preset.Name
		}

		streams = append(streams, &live.StreamUrlInfo{
			Url:         u,
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
			HeadersForDownloader: headers,
		})
		l.GetLogger().Debugf("Soop 可用流已加入: quality=%s format=hls", qualityLabel)
	}

	if len(streams) == 0 {
		if channelInfo.NeedPwd {
			return nil, fmt.Errorf("Soop 房间开启了直播密码，当前版本尚未填写密码，无法获取播放流")
		}
		return nil, fmt.Errorf("Soop 未返回任何可用流，可能是接口变更、登录态不足或调度节点异常")
	}

	l.GetLogger().Debugf("Soop GetStreamInfos 完成：streams=%d selected_default=%s", len(streams), streams[0].Quality)
	return streams, nil
}

func (l *Live) GetPlatformCNName() string {
	return cnName
}

func (l *Live) UpdateLiveOptionsbyConfig(ctx context.Context, room *configs.LiveRoom) error {
	if err := l.BaseLive.UpdateLiveOptionsbyConfig(ctx, room); err != nil {
		return err
	}

	l.resetRuntimeState()
	return nil
}

// fetchPageMeta 从播放页 HTML 中提取频道名、bno、主播名、标题等基础信息。
// 这是所有后续 API 请求的前置步骤，因为 Soop 的很多接口都依赖 channel + bno。
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
		return nil, fmt.Errorf("Soop 播放页返回异常状态码: %d", resp.StatusCode)
	}

	body, err := resp.Text()
	if err != nil {
		return nil, fmt.Errorf("读取 Soop 播放页响应失败: %w", err)
	}

	channel, pathBroadNo, err := parseChannelAndBroadNoFromURL(l.Url)
	if err != nil {
		return nil, fmt.Errorf("解析 Soop 房间 URL 失败: %w", err)
	}
	pageBroadNo, pageBroadNoFound := parseBroadNoFromPage(body)
	broadNo := pathBroadNo
	if pageBroadNoFound {
		broadNo = pageBroadNo
	}

	hostName := parseWindowString(body, "szBjNick")
	roomName := parseWindowString(body, "szBroadTitle")

	meta := &pageMeta{
		Channel:  channel,
		BroadNo:  broadNo,
		HostName: hostName,
		RoomName: roomName,
		IsLiving: broadNo != "",
	}
	l.GetLogger().Debugf("Soop 页面元信息: channel=%s pathBroadNo=%s pageBroadNo=%s pageBroadNoFound=%v host=%s room=%s living=%v",
		channel, pathBroadNo, pageBroadNo, pageBroadNoFound, hostName, roomName, meta.IsLiving)
	return meta, nil
}

// fetchChannelInfo 调用 player_live_api.php(type=live) 获取房间主信息。
// 返回值同时为 GetInfo 和 GetStreamInfos 服务。
// 当前版本未实现密码房输入，因此这里固定以空密码请求。
func (l *Live) fetchChannelInfo(channel, broadNo string) (*channelInfo, error) {
	if channel == "" || broadNo == "" {
		return nil, fmt.Errorf("Soop 房间标识不完整：channel=%q broadNo=%q", channel, broadNo)
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
		return nil, fmt.Errorf("Soop 播放信息接口返回异常状态码: %d", resp.StatusCode)
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
		return "", 0, fmt.Errorf("Soop AID 接口返回异常状态码: %d", resp.StatusCode)
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
		return "", fmt.Errorf("Soop 调度节点为空，无法获取播放地址")
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
		return "", fmt.Errorf("Soop 调度接口返回异常状态码: %d", resp.StatusCode)
	}

	body, err := resp.Bytes()
	if err != nil {
		return "", fmt.Errorf("读取 Soop 调度接口响应失败: %w", err)
	}

	viewURL := gjson.GetBytes(body, "view_url").String()
	if viewURL == "" {
		return "", fmt.Errorf("Soop 调度接口未返回 view_url，当前 CDN=%q quality=%q", cdn, quality)
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

// getCookieMap 将运行时 CookieJar 和配置文件中保存的 Cookie 合并成请求使用的 map。
// 这样可以同时兼容：
// 1. 本次运行中通过登录接口注入的 Cookie；
// 2. 历史配置文件里持久化的 Cookie。
func (l *Live) getCookieMap() map[string]string {
	runtimeCookie, ignoreStoredCookie := l.getRuntimeState()
	if ignoreStoredCookie && strings.TrimSpace(runtimeCookie) == "" {
		return map[string]string{}
	}

	cookies := l.Options.Cookies.Cookies(l.Url)
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
	return cookieMap
}

// resolveChannelInfo 是 Soop 获取频道信息的统一入口。
// 它在真正请求播放信息前，会先做一次 Cookie 预检和必要的自动重登。
func (l *Live) resolveChannelInfo(channel, broadNo string) (*channelInfo, error) {
	l.GetLogger().Debugf("Soop 开始解析频道信息: channel=%s broadNo=%s", channel, broadNo)
	if err := l.tryVerifyAndReloginIfNeeded(); err != nil {
		l.GetLogger().WithError(err).Warn("Soop 登录态预检失败")
	}

	info, err := l.fetchChannelInfo(channel, broadNo)
	if err != nil {
		return nil, err
	}
	if info.Result == channelResultLogin {
		l.GetLogger().Debugf("Soop 播放信息提示需要登录，准备自动重登: channel=%s broadNo=%s", channel, broadNo)
		if err := l.tryAutoLogin(); err != nil {
			return nil, fmt.Errorf("Soop 需要登录态，但自动重登失败: %w", err)
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
// 如果已有 Cookie 失效且配置中存在账号密码，则自动重新登录。
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
	l.GetLogger().Debugf("Soop 自动登录开始: username=%s", username)

	result, err := loginAndGetCookieWithSingleflight(username, password)
	if err != nil {
		l.GetLogger().WithError(err).Debug("Soop 自动登录失败")
		return err
	}
	if result == nil {
		return fmt.Errorf("Soop 自动登录未返回结果")
	}
	if result.Cookie == "" {
		return fmt.Errorf("Soop 自动登录成功，但未获得可用 Cookie")
	}
	if !result.Verify.IsLogin {
		return fmt.Errorf("Soop 自动登录成功，但登录态校验未通过")
	}

	if err := persistSoopCookieWithSingleflight(result.Cookie); err != nil {
		l.GetLogger().WithError(err).Warn("更新 Soop Cookie 到配置失败")
	}
	l.setRuntimeState(result.Cookie, false)
	l.GetLogger().Debugf("Soop 自动登录成功: loginID=%s cookie_length=%d", result.Verify.LoginID, len(result.Cookie))

	for _, targetURL := range []*url.URL{playSoopURL, l.Url} {
		if targetURL == nil {
			continue
		}
		live.WithKVStringCookies(targetURL, result.Cookie)(l.Options)
	}

	return nil
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
// 2. play.sooplive.co.kr；
// 3. 无。
func (l *Live) getPrimaryCookieString() string {
	if cookie := strings.TrimSpace(l.getRuntimeCookie()); cookie != "" {
		return cookie
	}

	cfg := configs.GetCurrentConfig()
	if cfg == nil || cfg.Cookies == nil {
		return buildCookieStringFromCookies(l.Options.Cookies.Cookies(l.Url))
	}
	if cookie := strings.TrimSpace(cfg.Cookies[l.Url.Host]); cookie != "" {
		return cookie
	}
	if cookie := strings.TrimSpace(cfg.Cookies[domainPlaySoop]); cookie != "" {
		return cookie
	}
	return buildCookieStringFromCookies(l.Options.Cookies.Cookies(l.Url))
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
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(parts) == 0 || parts[0] == "" {
		return "", "", live.ErrRoomUrlIncorrect
	}

	channel := parts[0]
	broadNo := ""
	if len(parts) > 1 {
		if _, err := strconv.ParseInt(parts[1], 10, 64); err == nil {
			broadNo = parts[1]
		}
	}

	return channel, broadNo, nil
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
