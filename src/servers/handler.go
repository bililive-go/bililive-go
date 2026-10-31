package servers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"text/template"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/mux"
	"github.com/hr3lxphr6j/requests"
	"github.com/tidwall/gjson"
	"gopkg.in/yaml.v3"

	"github.com/bililive-go/bililive-go/src/configs"
	"github.com/bililive-go/bililive-go/src/consts"
	"github.com/bililive-go/bililive-go/src/instance"
	"github.com/bililive-go/bililive-go/src/listeners"
	"github.com/bililive-go/bililive-go/src/live"
	dy "github.com/bililive-go/bililive-go/src/live/douyu"
	soop "github.com/bililive-go/bililive-go/src/live/sooplive"
	"github.com/bililive-go/bililive-go/src/livestate"
	applog "github.com/bililive-go/bililive-go/src/log"
	"github.com/bililive-go/bililive-go/src/pkg/livelogger"
	"github.com/bililive-go/bililive-go/src/pkg/memstats"
	"github.com/bililive-go/bililive-go/src/pkg/metadata"
	"github.com/bililive-go/bililive-go/src/pkg/ratelimit"
	"github.com/bililive-go/bililive-go/src/pkg/utils"
	"github.com/bililive-go/bililive-go/src/recorders"
	dmdouyin "github.com/bililive-go/bililive-go/src/recorders/danmaku/douyin"
	"github.com/bililive-go/bililive-go/src/tools"
	"github.com/bililive-go/bililive-go/src/types"
)

// FIXME: remove this
func parseInfo(ctx context.Context, l live.Live) *live.Info {
	inst := instance.GetInstance(ctx)

	// 尝试从缓存获取信息
	obj, err := inst.Cache.Get(l)

	var info *live.Info
	if err != nil || obj == nil {
		// 缓存中没有信息，可能是 InitializingLive 还未初始化
		// 创建一个基础信息
		hostName := "初始化中..."
		roomName := l.GetRawUrl()

		// 尝试从 InitializingLive 获取数据库缓存的信息
		target := l
		if wrapped, ok := l.(*live.WrappedLive); ok {
			target = wrapped.Live
		}
		if setter, ok := target.(live.CachedInfoSetter); ok {
			h, r := setter.GetCachedInfo()
			if h != "" {
				hostName = h
			}
			if r != "" {
				roomName = r
			}
		}

		info = &live.Info{
			Live:         l,
			HostName:     hostName,
			RoomName:     roomName,
			Status:       false,
			Initializing: true,
		}
	} else {
		info = obj.(*live.Info)
	}

	info.Listening = inst.ListenerManager.(listeners.Manager).HasListener(ctx, l.GetLiveId())
	// 区分"有 recorder"和"真正在录制"
	// HasRecorder=true 但输出文件没有数据时，说明在重试（获取流 URL、连接失败等）
	// 前端应显示"录制准备中"而非"录制中"，避免用户误以为正在正常录制
	//
	// 注意：info 是从缓存获取的共享对象，必须先重置两个互斥字段，
	// 否则前一次调用的残留值会导致 recording=true + recording_preparing=true 同时返回
	info.Recording = false
	info.RecordingPreparing = false
	recorderMgr := inst.RecorderManager.(recorders.Manager)
	if recorderMgr.HasRecorder(ctx, l.GetLiveId()) {
		if recorder, err := recorderMgr.GetRecorder(ctx, l.GetLiveId()); err == nil && recorder.IsRecording() {
			info.Recording = true
		} else {
			// 有 recorder 但尚未真正开始录制（例如流 URL 404 导致不断重试）
			info.RecordingPreparing = true
		}
	}
	if info.HostName == "" {
		info.HostName = "获取失败"
	}
	if info.RoomName == "" {
		info.RoomName = l.GetRawUrl()
	}

	// 设置 NotifyOnly 状态
	info.NotifyOnly = false
	if cfg := configs.GetCurrentConfig(); cfg != nil {
		if room, err := cfg.GetLiveRoomByUrl(l.GetRawUrl()); err == nil {
			info.NotifyOnly = room.NotifyOnly
		}
	}

	return info
}

// extractStreamAttributeCombinations 从可用流中提取所有属性组合
// 返回所有流的 AttributesForStreamSelect，供前端动态生成选择器
func extractStreamAttributeCombinations(streams []*live.AvailableStreamInfo) []map[string]string {
	if len(streams) == 0 {
		return nil
	}

	combinations := make([]map[string]string, 0, len(streams))
	for _, stream := range streams {
		if len(stream.AttributesForStreamSelect) > 0 {
			combinations = append(combinations, stream.AttributesForStreamSelect)
		}
	}

	return combinations
}

func getAllLives(writer http.ResponseWriter, r *http.Request) {
	inst := instance.GetInstance(r.Context())
	lives := liveSlice(make([]*live.Info, 0, 4))
	inst.Lives.Range(func(_ types.LiveID, v live.Live) bool {
		lives = append(lives, parseInfo(r.Context(), v))
		return true
	})
	sort.Sort(lives)
	writeJSON(writer, lives)
}

func getLive(writer http.ResponseWriter, r *http.Request) {
	inst := instance.GetInstance(r.Context())
	vars := mux.Vars(r)
	liveObj, ok := inst.Lives.Get(types.LiveID(vars["id"]))
	if !ok {
		writeJsonWithStatusCode(writer, http.StatusNotFound, commonResp{
			ErrNo:  http.StatusNotFound,
			ErrMsg: fmt.Sprintf("live id: %s can not find", vars["id"]),
		})
		return
	}

	// 获取基本信息
	info := parseInfo(r.Context(), liveObj)

	// 获取全局配置
	cfg := configs.GetCurrentConfig()
	if cfg == nil {
		writeJSON(writer, info) // 如果配置为空，返回基本信息
		return
	}

	// 获取房间配置
	room, err := cfg.GetLiveRoomByUrl(liveObj.GetRawUrl())
	if err != nil {
		writeJSON(writer, info) // 如果找不到房间配置，返回基本信息
		return
	}

	// 获取平台key
	platformKey := configs.GetPlatformKeyFromUrl(liveObj.GetRawUrl())

	// 解析最终生效的配置
	resolvedConfig := cfg.ResolveConfigForRoom(room, platformKey)

	// 获取平台相关的连接统计
	// 从 URL 中提取主机名用于匹配连接统计
	rawURL := liveObj.GetRawUrl()
	parsedURL, _ := url.Parse(rawURL)
	var connStats []utils.ConnStats
	if parsedURL != nil {
		// 提取 API 主机名前缀（如 bilibili 对应 api.live.bilibili.com）
		host := parsedURL.Host
		// 移除端口号
		if colonIdx := strings.Index(host, ":"); colonIdx != -1 {
			host = host[:colonIdx]
		}
		// 提取主域名部分用于匹配
		parts := strings.Split(host, ".")
		if len(parts) >= 2 {
			domainPrefix := parts[len(parts)-2] // 如 "bilibili"
			connStats = utils.ConnCounterManager.GetStatsByHostPrefix(domainPrefix)
		}
	}

	// 获取平台等待状态信息
	platformRateLimiter := ratelimit.GetGlobalRateLimiter()
	platformRateLimitEnabled := platformRateLimiter.Enabled()
	waitInfo := platformRateLimiter.GetPlatformWaitInfo(platformKey)
	platformRateLimit := 0
	if platformRateLimitEnabled {
		platformRateLimit = cfg.GetPlatformMinAccessInterval(platformKey)
	}

	// 获取调度器状态信息
	var schedulerStatus *live.SchedulerStatus
	if provider, ok := liveObj.(live.SchedulerStatusProvider); ok {
		status := provider.GetSchedulerStatus()
		schedulerStatus = &status
	}

	// 获取录制状态和下载速度
	var recorderStatus map[string]interface{}
	if info.Recording || info.RecordingPreparing {
		// 正在录制或录制准备中，获取 recorder 状态（流信息等）
		if recorderMgr, ok := inst.RecorderManager.(recorders.Manager); ok {
			recorder, err := recorderMgr.GetRecorder(r.Context(), info.Live.GetLiveId())
			if err == nil {
				status, statusErr := recorder.GetStatus()
				if statusErr != nil {
					info.Live.GetLogger().Warnf("failed to get recorder status: %v", statusErr)
				}
				recorderStatus = status
			}
		}
	}

	// 获取录制开始时间
	var recordStartTime string
	if info.Recording || info.RecordingPreparing {
		if recorderMgr, ok := inst.RecorderManager.(recorders.Manager); ok {
			recorder, err := recorderMgr.GetRecorder(r.Context(), info.Live.GetLiveId())
			if err == nil {
				recordStartTime = recorder.StartTime().Format("2006-01-02 15:04:05")
			}
		}
	}

	// 获取开播时间
	var liveStartTime string
	lastStartTime := liveObj.GetLastStartTime()
	if !lastStartTime.IsZero() {
		liveStartTime = lastStartTime.Format("2006-01-02 15:04:05")
	}

	// 构造详细响应
	detailedInfo := map[string]interface{}{
		// 基本信息
		"host_name":           info.HostName,
		"room_name":           info.RoomName,
		"status":              info.Status,
		"listening":           info.Listening,
		"recording":           info.Recording,
		"recording_preparing": info.RecordingPreparing,
		"live_id":             info.Live.GetLiveId(),
		"raw_url":             info.Live.GetRawUrl(),
		"platform":            info.Live.GetPlatformCNName(),

		// 有效配置信息
		"platform_key":          platformKey,
		"effective_interval":    resolvedConfig.Interval,
		"effective_out_path":    resolvedConfig.OutPutPath,
		"effective_ffmpeg_path": resolvedConfig.FfmpegPath,
		"quality":               room.Quality,
		"audio_only":            room.AudioOnly,

		// 平台访问限制
		"platform_rate_limit":         platformRateLimit,
		"platform_rate_limit_enabled": platformRateLimitEnabled,

		// 平台等待状态
		"rate_limit_info": map[string]interface{}{
			"enabled":             platformRateLimitEnabled,
			"waited_seconds":      waitInfo.WaitedSeconds,
			"next_request_in_sec": waitInfo.NextRequestInSec,
			"min_interval_sec":    waitInfo.MinIntervalSec,
		},

		// 配置来源信息
		"config_sources": map[string]string{
			"interval":     getConfigSource(cfg, *room, platformKey, "interval"),
			"out_put_path": getConfigSource(cfg, *room, platformKey, "out_put_path"),
			"ffmpeg_path":  getConfigSource(cfg, *room, platformKey, "ffmpeg_path"),
		},

		// 运行时信息 - 连接统计
		"conn_stats": connStats,

		// 录制器状态（包括下载速度等）
		"recorder_status": recorderStatus,

		// 调度器状态（用于显示"距离下次刷新"）
		"scheduler_status": schedulerStatus,

		// 时间信息
		"live_start_time":  liveStartTime,   // 本次开播时间
		"last_record_time": recordStartTime, // 本次录制开始时间

		// 原始配置信息
		"room_config": room,
	}

	// 添加可用流信息（优先使用内存缓存，否则从数据库读取）
	if len(info.AvailableStreams) > 0 {
		detailedInfo["available_streams"] = info.AvailableStreams
		detailedInfo["available_streams_updated_at"] = info.AvailableStreamsUpdatedAt
		// 提取属性组合供前端流选择器使用
		detailedInfo["available_stream_attributes"] = extractStreamAttributeCombinations(info.AvailableStreams)
	} else {
		// 尝试从数据库读取
		if store, ok := inst.LiveStateStore.(livestate.Store); ok {
			streams, err := store.GetAvailableStreams(r.Context(), string(info.Live.GetLiveId()))
			if err == nil && len(streams) > 0 {
				// 转换为 API 格式
				apiStreams := make([]map[string]interface{}, 0, len(streams))
				for _, s := range streams {
					streamData := map[string]interface{}{
						"quality":                      s.Quality,
						"attributes_for_stream_select": s.Attributes,
					}
					apiStreams = append(apiStreams, streamData)
				}
				detailedInfo["available_streams"] = apiStreams
				if len(streams) > 0 && !streams[0].UpdatedAt.IsZero() {
					detailedInfo["available_streams_updated_at"] = streams[0].UpdatedAt.Unix()
				}

				// 提取属性组合供前端流选择器使用
				attributeCombinations := make([]map[string]string, 0, len(streams))
				for _, s := range streams {
					if len(s.Attributes) > 0 {
						attributeCombinations = append(attributeCombinations, s.Attributes)
					}
				}
				detailedInfo["available_stream_attributes"] = attributeCombinations
			}
		}
	}

	writeJSON(writer, detailedInfo)
}

// getConfigSource 获取配置项的来源级别
func getConfigSource(config *configs.Config, room configs.LiveRoom, platformKey, configKey string) string {
	// 检查房间级配置
	switch configKey {
	case "interval":
		if room.Interval != nil {
			return "room"
		}
	case "out_put_path":
		if room.OutPutPath != nil {
			return "room"
		}
	case "ffmpeg_path":
		if room.FfmpegPath != nil {
			return "room"
		}
	}

	// 检查平台级配置
	if platformConfig, exists := config.PlatformConfigs[platformKey]; exists {
		switch configKey {
		case "interval":
			if platformConfig.Interval != nil {
				return "platform"
			}
		case "out_put_path":
			if platformConfig.OutPutPath != nil {
				return "platform"
			}
		case "ffmpeg_path":
			if platformConfig.FfmpegPath != nil {
				return "platform"
			}
		}
	}

	// 默认为全局配置
	return "global"
}

func getLiveLogs(writer http.ResponseWriter, r *http.Request) {
	inst := instance.GetInstance(r.Context())
	vars := mux.Vars(r)
	linesStr := r.URL.Query().Get("lines")
	lines := 100 // 默认100行
	if linesStr != "" {
		if parsedLines, err := strconv.Atoi(linesStr); err == nil && parsedLines > 0 {
			lines = parsedLines
		}
	}

	liveID := types.LiveID(vars["id"])

	// 查找直播间
	liveInstance, ok := inst.Lives.Get(liveID)
	if !ok {
		writeJsonWithStatusCode(writer, http.StatusNotFound, commonResp{
			ErrNo:  http.StatusNotFound,
			ErrMsg: fmt.Sprintf("live id: %s can not find", vars["id"]),
		})
		return
	}

	// 从直播间的 Logger 获取日志（原始文本形式）
	logsText := liveInstance.GetLogger().GetLogs()

	// 按行分割日志
	var logLines []string
	if logsText != "" {
		// 分割成行，去掉末尾空行
		for _, line := range strings.Split(logsText, "\n") {
			if line != "" {
				logLines = append(logLines, line)
			}
		}
	}

	// 如果请求了行数限制，只返回最后 N 行
	if lines > 0 && len(logLines) > lines {
		logLines = logLines[len(logLines)-lines:]
	}

	logResponse := map[string]interface{}{
		"lines":     logLines,
		"total":     len(logLines),
		"max_lines": lines,
	}

	writeJSON(writer, logResponse)
}

func parseLiveAction(writer http.ResponseWriter, r *http.Request) {
	inst := instance.GetInstance(r.Context())
	vars := mux.Vars(r)
	resp := commonResp{}
	live, ok := inst.Lives.Get(types.LiveID(vars["id"]))
	if !ok {
		resp.ErrNo = http.StatusNotFound
		resp.ErrMsg = fmt.Sprintf("live id: %s can not find", vars["id"])
		writeJsonWithStatusCode(writer, http.StatusNotFound, resp)
		return
	}
	cfg := configs.GetCurrentConfig()
	_, err := cfg.GetLiveRoomByUrl(live.GetRawUrl())
	if err != nil {
		resp.ErrNo = http.StatusNotFound
		resp.ErrMsg = fmt.Sprintf("room : %s can not find", live.GetRawUrl())
		writeJsonWithStatusCode(writer, http.StatusNotFound, resp)
		return
	}
	switch vars["action"] {
	case "start":
		if err := startListening(inst.Ctx, live); err != nil {
			resp.ErrNo = http.StatusBadRequest
			resp.ErrMsg = err.Error()
			writeJsonWithStatusCode(writer, http.StatusBadRequest, resp)
			return
		}
		if _, err := configs.SetLiveRoomListening(live.GetRawUrl(), true); err != nil {
			live.GetLogger().Error("failed to set live room listening: " + err.Error())
		}
		// 广播监控开启事件
		GetSSEHub().BroadcastListChange(live.GetLiveId(), "listen_start", map[string]interface{}{
			"live_id": string(live.GetLiveId()),
		})
	case "stop":
		if err := stopListening(inst.Ctx, live.GetLiveId()); err != nil {
			resp.ErrNo = http.StatusBadRequest
			resp.ErrMsg = err.Error()
			writeJsonWithStatusCode(writer, http.StatusBadRequest, resp)
			return
		}
		if _, err := configs.SetLiveRoomListening(live.GetRawUrl(), false); err != nil {
			live.GetLogger().Error("failed to set live room listening: " + err.Error())
		}
		// 记录用户停止监控（结束当前会话）
		if manager, ok := inst.LiveStateManager.(*livestate.Manager); ok && manager != nil {
			manager.OnUserStopMonitoring(string(live.GetLiveId()))
		}
		// 广播监控停止事件
		GetSSEHub().BroadcastListChange(live.GetLiveId(), "listen_stop", map[string]interface{}{
			"live_id": string(live.GetLiveId()),
		})
	case "forceRefresh":
		// 强制刷新：忽略平台访问频率限制，立即获取最新信息
		platformKey := configs.GetPlatformKeyFromUrl(live.GetRawUrl())
		platformRateLimiter := ratelimit.GetGlobalRateLimiter()
		platformRateLimiter.ForceAccess(platformKey)

		// 手动调用 GetInfo 获取最新信息
		info, err := live.GetInfo()
		if err != nil {
			resp.ErrNo = http.StatusInternalServerError
			resp.ErrMsg = fmt.Sprintf("force refresh failed: %s", err.Error())
			writeJsonWithStatusCode(writer, http.StatusInternalServerError, resp)
			return
		}

		// 广播频率限制更新事件，通知前端更新倒计时
		waitInfo := platformRateLimiter.GetPlatformWaitInfo(platformKey)
		GetSSEHub().BroadcastRateLimitUpdate(live.GetLiveId(), map[string]interface{}{
			"enabled":             platformRateLimiter.Enabled(),
			"waited_seconds":      waitInfo.WaitedSeconds,
			"next_request_in_sec": waitInfo.NextRequestInSec,
			"min_interval_sec":    waitInfo.MinIntervalSec,
		})

		// 返回刷新后的信息
		writeJSON(writer, map[string]interface{}{
			"success":   true,
			"message":   "强制刷新成功",
			"host_name": info.HostName,
			"room_name": info.RoomName,
			"status":    info.Status,
		})
		return
	case "segment":
		// 请求在下一个关键帧处分段（仅在使用 FLV 代理时有效）
		recorderMgr, ok := inst.RecorderManager.(recorders.Manager)
		if !ok {
			resp.ErrNo = http.StatusInternalServerError
			resp.ErrMsg = "录制管理器不可用"
			writeJsonWithStatusCode(writer, http.StatusInternalServerError, resp)
			return
		}

		recorder, err := recorderMgr.GetRecorder(r.Context(), live.GetLiveId())
		if err != nil {
			resp.ErrNo = http.StatusBadRequest
			resp.ErrMsg = "直播间未在录制中"
			writeJsonWithStatusCode(writer, http.StatusBadRequest, resp)
			return
		}

		// 检查是否支持 FLV 代理分段
		if !recorder.HasFlvProxy() {
			resp.ErrNo = http.StatusBadRequest
			resp.ErrMsg = "当前录制未使用 FLV 代理，不支持手动分段（请在配置中启用 enable_flv_proxy_segment）"
			writeJsonWithStatusCode(writer, http.StatusBadRequest, resp)
			return
		}

		// 请求分段
		if recorder.RequestSegment() {
			writeJSON(writer, map[string]interface{}{
				"success": true,
				"message": "分段请求已接受，将在下一个关键帧处分段",
			})
		} else {
			resp.ErrNo = http.StatusTooManyRequests
			resp.ErrMsg = "分段请求被拒绝，可能距离上次分段时间过短（最小间隔 10 秒）"
			writeJsonWithStatusCode(writer, http.StatusTooManyRequests, resp)
		}
		return
	default:
		resp.ErrNo = http.StatusBadRequest
		resp.ErrMsg = fmt.Sprintf("invalid Action: %s", vars["action"])
		writeJsonWithStatusCode(writer, http.StatusBadRequest, resp)
		return
	}
	writeJSON(writer, parseInfo(r.Context(), live))
}

// switchStreamHandler 处理切换流设置的请求
func switchStreamHandler(writer http.ResponseWriter, r *http.Request) {
	inst := instance.GetInstance(r.Context())
	vars := mux.Vars(r)
	resp := commonResp{}

	live, ok := inst.Lives.Get(types.LiveID(vars["id"]))
	if !ok {
		resp.ErrNo = http.StatusNotFound
		resp.ErrMsg = fmt.Sprintf("直播间 ID: %s 未找到", vars["id"])
		writeJsonWithStatusCode(writer, http.StatusNotFound, resp)
		return
	}

	// 读取请求体获取目标流设置
	b, err := io.ReadAll(r.Body)
	if err != nil {
		resp.ErrNo = http.StatusBadRequest
		resp.ErrMsg = err.Error()
		writeJsonWithStatusCode(writer, http.StatusBadRequest, resp)
		return
	}

	var streamRequest configs.ResolvedStreamPreference
	if err := json.Unmarshal(b, &streamRequest); err != nil {
		resp.ErrNo = http.StatusBadRequest
		resp.ErrMsg = "无效的请求格式: " + err.Error()
		writeJsonWithStatusCode(writer, http.StatusBadRequest, resp)
		return
	}

	// 验证参数
	// if streamRequest.Quality == "" {
	// 	resp.ErrNo = http.StatusBadRequest
	// 	resp.ErrMsg = "必须指定 quality"
	// 	writeJsonWithStatusCode(writer, http.StatusBadRequest, resp)
	// 	return
	// }

	// 更新流配置
	_, err = configs.UpdateWithRetry(func(c *configs.Config) error {
		liveRoom, err1 := c.GetLiveRoomByUrl(live.GetRawUrl())
		if err1 != nil {
			return err1
		}

		if liveRoom.StreamPreference == nil {
			liveRoom.StreamPreference = &configs.StreamPreference{}
		}

		liveRoom.StreamPreference.Quality = &streamRequest.Quality
		liveRoom.StreamPreference.Attributes = &streamRequest.Attributes

		return nil
	}, 3, 10*time.Millisecond)

	if err != nil {
		resp.ErrNo = http.StatusInternalServerError
		resp.ErrMsg = "更新流配置失败: " + err.Error()
		writeJsonWithStatusCode(writer, http.StatusInternalServerError, resp)
		return
	}

	// 检查录制器是否存在，存在则重启
	recorderMgr, ok := inst.RecorderManager.(recorders.Manager)
	if !ok {
		resp.ErrNo = http.StatusInternalServerError
		resp.ErrMsg = "录制管理器不可用"
		writeJsonWithStatusCode(writer, http.StatusInternalServerError, resp)
		return
	}

	if recorderMgr.HasRecorder(inst.Ctx, live.GetLiveId()) {
		// 重启录制器以应用新的流设置
		if err := recorderMgr.RestartRecorder(inst.Ctx, live); err != nil {
			resp.ErrNo = http.StatusInternalServerError
			resp.ErrMsg = "重启录制器失败: " + err.Error()
			writeJsonWithStatusCode(writer, http.StatusInternalServerError, resp)
			return
		}
		live.GetLogger().Infof("流设置已更新并重启录制: quality=%s", streamRequest.Quality)

		writeJSON(writer, map[string]interface{}{
			"success": true,
			"message": "流设置已更新，录制已重新启动",
			"stream_config": map[string]interface{}{
				"quality":    streamRequest.Quality,
				"attributes": streamRequest.Attributes,
			},
		})
	} else {
		// 不在录制中，仅保存配置
		live.GetLogger().Infof("流设置已更新（未在录制中）: quality=%s", streamRequest.Quality)

		writeJSON(writer, map[string]interface{}{
			"success":      true,
			"message":      "流设置已更新（直播间未在录制中，将在下次录制时生效）",
			"is_recording": false,
			"stream_config": map[string]interface{}{
				"quality":    streamRequest.Quality,
				"attributes": streamRequest.Attributes,
			},
		})
	}
}

func startListening(ctx context.Context, live live.Live) error {
	inst := instance.GetInstance(ctx)
	return inst.ListenerManager.(listeners.Manager).AddListener(ctx, live)
}

func stopListening(ctx context.Context, liveId types.LiveID) error {
	inst := instance.GetInstance(ctx)
	return inst.ListenerManager.(listeners.Manager).RemoveListener(ctx, liveId)
}

/*
	Post data example

[

	{
		"url": "http://live.bilibili.com/1030",
		"listen": true
	},
	{
		"url": "https://live.bilibili.com/493",
		"listen": true
	}

]
*/
func addLives(writer http.ResponseWriter, r *http.Request) {
	inst := instance.GetInstance(r.Context())
	b, err := io.ReadAll(r.Body)
	if err != nil {
		writeJsonWithStatusCode(writer, http.StatusBadRequest, commonResp{
			ErrNo:  http.StatusBadRequest,
			ErrMsg: err.Error(),
		})
		return
	}
	info := liveSlice(make([]*live.Info, 0))
	errorMessages := make([]string, 0, 4)
	gjson.ParseBytes(b).ForEach(func(key, value gjson.Result) bool {
		isListen := value.Get("listen").Bool()
		notifyOnly := value.Get("notify_only").Bool()
		urlStr := strings.Trim(value.Get("url").String(), " ")
		if retInfo, err := addLiveImpl(inst.Ctx, urlStr, isListen, notifyOnly, true); err != nil {
			msg := urlStr + ": " + err.Error()
			applog.GetLogger().Error(msg)
			errorMessages = append(errorMessages, msg)
			return true
		} else {
			info = append(info, retInfo)
		}
		return true
	})
	sort.Sort(info)
	writeJSON(writer, info)
}

func addLiveImpl(ctx context.Context, urlStr string, isListen bool, notifyOnly bool, persist bool) (info *live.Info, err error) {
	if !strings.HasPrefix(urlStr, "http://") && !strings.HasPrefix(urlStr, "https://") {
		urlStr = "https://" + urlStr
	}
	// 统一别名域名（如手机分享出来的 m.douyu.com），使其与 cookies/平台配置的 host key 对齐
	urlStr = configs.NormalizeLiveRoomUrl(urlStr)
	u, err := url.Parse(urlStr)
	if err != nil {
		return nil, errors.New("can't parse url: " + urlStr)
	}
	// 抖音手机版分享短链入库前转为主播账号级稳定长链：短链 302 固化的房间号是分享那一刻的场次快照，
	// 主播重新开播后即失效；转成 live.douyin.com/<web_rid> 后视频/弹幕/cookie 全链路与手工长链完全一致。
	// 转换失败不阻断添加（保留原短链，弹幕侧还有运行时逐跳解析兜底）。
	if u.Host == "v.douyin.com" {
		if segs := strings.Split(strings.Trim(u.Path, "/"), "/"); len(segs) > 0 && segs[0] != "" {
			if longURL, rerr := dmdouyin.ResolveShareLongURL(ctx, segs[0]); rerr == nil {
				applog.GetLogger().Infof("抖音分享短链已转换为长链: %s -> %s", urlStr, longURL)
				// 当前配置里若已按原短链入库（配置编辑器粘贴、或存量短链房间重新添加），原位迁移该条目 URL，
				// 避免"短链条目 + 新增长链条目"双份入库导致重启后同主播重复录制
				shortURL := urlStr
				if _, gerr := configs.GetCurrentConfig().GetLiveRoomByUrl(shortURL); gerr == nil {
					migrate := func(c *configs.Config) error {
						if r, err := c.GetLiveRoomByUrl(shortURL); err == nil {
							r.Url = longURL
						}
						return nil
					}
					var merr error
					if persist {
						_, merr = configs.UpdateWithRetry(migrate, 3, 10*time.Millisecond)
					} else {
						_, merr = configs.UpdateWithRetryTransient(migrate, 3, 10*time.Millisecond)
					}
					if merr != nil {
						applog.GetLogger().Warnf("抖音短链房间迁移为长链失败: %v", merr)
					}
				}
				urlStr = longURL
				if u, err = url.Parse(urlStr); err != nil {
					return nil, errors.New("can't parse url: " + urlStr)
				}
			} else {
				applog.GetLogger().Warnf("抖音分享短链转长链失败，保留原链接: %v", rerr)
			}
		}
	}
	inst := instance.GetInstance(ctx)
	needAppend := false
	liveRoom, err := configs.GetCurrentConfig().GetLiveRoomByUrl(u.String())
	if err != nil {
		liveRoom = &configs.LiveRoom{
			Url:         u.String(),
			IsListening: isListen,
			NotifyOnly:  notifyOnly,
		}
		needAppend = true
	}
	newLive, err := live.New(ctx, liveRoom, inst.Cache)
	if err != nil {
		return nil, err
	}
	if !inst.Lives.SetIfAbsent(newLive.GetLiveId(), newLive) {
		return nil, errors.New("直播间已存在")
	}
	// 记录 LiveId 到全局配置（并发安全）
	configs.SetLiveRoomId(u.String(), newLive.GetLiveId())
	// 先将房间写入配置，再启动 Listener，确保 sendLiveNotification 能读到 NotifyOnly 等字段
	if needAppend {
		if liveRoom == nil {
			return nil, errors.New("liveRoom is nil, cannot append to LiveRooms")
		}
		if persist {
			if _, err := configs.AppendLiveRoom(*liveRoom); err != nil {
				return nil, err
			}
		} else {
			if _, err := configs.AppendLiveRoomTransient(*liveRoom); err != nil {
				return nil, err
			}
		}
	}
	if isListen {
		inst.ListenerManager.(listeners.Manager).AddListener(ctx, newLive)
	}
	info = parseInfo(ctx, newLive)
	// 广播直播间列表变更事件
	GetSSEHub().BroadcastListChange(newLive.GetLiveId(), "room_added", map[string]interface{}{
		"live_id":   string(newLive.GetLiveId()),
		"url":       urlStr,
		"listening": isListen,
	})
	return info, nil
}

func batchAddLives(writer http.ResponseWriter, r *http.Request) {
	inst := instance.GetInstance(r.Context())
	b, err := io.ReadAll(r.Body)
	if err != nil {
		writeJsonWithStatusCode(writer, http.StatusBadRequest, commonResp{
			ErrNo:  http.StatusBadRequest,
			ErrMsg: "failed to read request body",
		})
		return
	}

	var req batchAddRequest
	if err := json.Unmarshal(b, &req); err != nil {
		writeJsonWithStatusCode(writer, http.StatusBadRequest, commonResp{
			ErrNo:  http.StatusBadRequest,
			ErrMsg: "invalid request body",
		})
		return
	}

	// 过滤空 URL，只保留有效条目
	validURLs := make([]string, 0, len(req.URLs))
	for _, u := range req.URLs {
		if s := strings.TrimSpace(u); s != "" {
			validURLs = append(validURLs, s)
		}
	}

	if len(validURLs) == 0 {
		writeJsonWithStatusCode(writer, http.StatusBadRequest, commonResp{
			ErrNo:  http.StatusBadRequest,
			ErrMsg: "urls is empty",
		})
		return
	}

	const maxBatchSize = 50
	if len(validURLs) > maxBatchSize {
		writeJsonWithStatusCode(writer, http.StatusBadRequest, commonResp{
			ErrNo:  http.StatusBadRequest,
			ErrMsg: fmt.Sprintf("too many urls: %d (max %d)", len(validURLs), maxBatchSize),
		})
		return
	}

	// 使用客户端提供的 batch_id，或生成新的
	batchID := req.BatchID
	if batchID == "" {
		batchID = "batch_" + uuid.New().String()
	}

	// 立即返回 batch_id 和有效总数
	writeJSON(writer, batchAddResponse{
		BatchID: batchID,
		Total:   len(validURLs),
	})

	// 异步处理批量添加
	go func() {
		defer func() {
			if r := recover(); r != nil {
				applog.GetLogger().Errorf("batch add: panic recovered: %v", r)
				GetSSEHub().BroadcastBatchComplete(batchID, batchCompleteEvent{
					Total:        len(validURLs),
					SuccessCount: 0,
					FailCount:    len(validURLs),
					Timestamp:    time.Now(),
				})
			}
		}()
		hub := GetSSEHub()
		successCount := 0
		failCount := 0

		for i, urlStr := range validURLs {
			// 检查重复房间，避免调用 addLiveImpl 做无用的网络请求
			checkURL := urlStr
			if !strings.HasPrefix(checkURL, "http://") && !strings.HasPrefix(checkURL, "https://") {
				checkURL = "https://" + checkURL
			}
			checkURL = configs.NormalizeLiveRoomUrl(checkURL)
			if u, err := url.Parse(checkURL); err == nil {
				if _, err := configs.GetCurrentConfig().GetLiveRoomByUrl(u.String()); err == nil {
					event := batchProgressEvent{
						Index:   i,
						Total:   len(validURLs),
						URL:     urlStr,
						Success: false,
						Error:   "直播间已存在",
					}
					failCount++
					hub.BroadcastBatchProgress(batchID, event)
					continue
				}
			}

			retInfo, err := addLiveImpl(inst.Ctx, urlStr, req.Listen, req.NotifyOnly, false)
			event := batchProgressEvent{
				Index:   i,
				Total:   len(validURLs),
				URL:     urlStr,
				Success: err == nil,
			}
			if err != nil {
				event.Error = err.Error()
				failCount++
			} else {
				event.Info = retInfo
				successCount++
			}

			hub.BroadcastBatchProgress(batchID, event)
		}

		// 批量完成后统一持久化一次配置
		var persistErr string
		if successCount > 0 {
			if err := configs.Persist(); err != nil {
				persistErr = err.Error()
				applog.GetLogger().Errorf("batch add: failed to persist config: %v", err)
			}
		}

		hub.BroadcastBatchComplete(batchID, batchCompleteEvent{
			Total:        len(validURLs),
			SuccessCount: successCount,
			FailCount:    failCount,
			PersistError: persistErr,
			Timestamp:    time.Now(),
		})
	}()
}

func removeLive(writer http.ResponseWriter, r *http.Request) {
	inst := instance.GetInstance(r.Context())
	vars := mux.Vars(r)
	live, ok := inst.Lives.Get(types.LiveID(vars["id"]))
	if !ok {
		writeJsonWithStatusCode(writer, http.StatusNotFound, commonResp{
			ErrNo:  http.StatusNotFound,
			ErrMsg: fmt.Sprintf("live id: %s can not find", vars["id"]),
		})
		return
	}
	if err := removeLiveImpl(inst.Ctx, live); err != nil {
		writeJsonWithStatusCode(writer, http.StatusBadRequest, commonResp{
			ErrNo:  http.StatusBadRequest,
			ErrMsg: err.Error(),
		})
		return
	}
	writeJSON(writer, commonResp{
		Data: "OK",
	})
}

func removeLiveImpl(ctx context.Context, live live.Live) error {
	inst := instance.GetInstance(ctx)
	liveId := live.GetLiveId()
	lm := inst.ListenerManager.(listeners.Manager)
	if lm.HasListener(ctx, liveId) {
		if err := lm.RemoveListener(ctx, liveId); err != nil {
			return err
		}
	}
	inst.Lives.Delete(liveId)
	if _, err := configs.RemoveLiveRoomByUrl(live.GetRawUrl()); err != nil {
		return err
	}
	// 广播直播间列表变更事件
	GetSSEHub().BroadcastListChange(liveId, "room_removed", map[string]interface{}{
		"live_id": string(liveId),
	})
	return nil
}

func getConfig(writer http.ResponseWriter, r *http.Request) {
	writeJSON(writer, configs.GetCurrentConfig())
}

func putConfig(writer http.ResponseWriter, r *http.Request) {
	// 直接序列化当前快照即可；索引缓存不参与序列化，且共享快照不可变，
	// 不能在此调用 RefreshLiveRoomIndexCache 写它的 map。
	config := configs.GetCurrentConfig()
	if err := config.Marshal(); err != nil {
		writeJsonWithStatusCode(writer, http.StatusBadRequest, commonResp{
			ErrNo:  http.StatusBadRequest,
			ErrMsg: err.Error(),
		})
		return
	}
	writeJsonWithStatusCode(writer, http.StatusOK, commonResp{
		Data: "OK",
	})
}

func getRawConfig(writer http.ResponseWriter, r *http.Request) {
	// 掩码斗鱼长期凭证后再输出：/api/config 已经用 json:"-" 把 LTP0 挡在 JSON 之外，
	// 若这里原样吐出 YAML，"设置明文"页就等于把数月有效的票据连同一堆 cookie 一起交给浏览器
	// （默认 rpc.bind 是 :8080 且接口无鉴权）。用户显式重新扫码才会更换该票据。
	cfg := configs.CloneConfigShallow(configs.GetCurrentConfig())
	if cfg == nil {
		writeJsonWithStatusCode(writer, http.StatusInternalServerError, commonResp{
			ErrNo:  http.StatusInternalServerError,
			ErrMsg: "配置尚未加载",
		})
		return
	}
	if cfg.DouyuAuth.LTP0 != "" {
		cfg.DouyuAuth.LTP0 = douyuSecretMask
	}
	if cfg.DouyuAuth.DyDid != "" {
		cfg.DouyuAuth.DyDid = douyuSecretMask
	}
	// Soop 账号密码同理：明文页是整份 YAML 的展示与回写入口，原文输出等于把密码交给浏览器。
	// 用与调试转储同一个占位符，写回侧（applyRawConfigDoc）据此识别"这一项没被用户改过"。
	if cfg.SoopLiveAuth.Password != "" {
		cfg.SoopLiveAuth.Password = configs.LogSecretMask
	}
	b, err := yaml.Marshal(cfg)
	if err != nil {
		writeJsonWithStatusCode(writer, http.StatusInternalServerError, commonResp{
			ErrNo:  http.StatusBadRequest,
			ErrMsg: err.Error(),
		})
		return
	}
	writeJSON(writer, map[string]string{
		"config": string(b),
	})
}

// rawConfigTopKeys 取"设置明文"YAML 的顶层键集合，仅用于区分"这一节没提交"与"提交了但为空"。
// 解析失败返回 nil，此时按"未提交"处理（宁可沿用原凭证，也不静默清掉不可再生的长期票据）。
func rawConfigTopKeys(yamlText string) map[string]any {
	var top map[string]any
	if err := yaml.Unmarshal([]byte(yamlText), &top); err != nil {
		return nil
	}
	return top
}

func putRawConfig(writer http.ResponseWriter, r *http.Request) {
	inst := instance.GetInstance(r.Context())
	b, err := io.ReadAll(r.Body)
	if err != nil {
		writeJsonWithStatusCode(writer, http.StatusBadRequest, commonResp{
			ErrNo:  http.StatusBadRequest,
			ErrMsg: err.Error(),
		})
		return
	}
	ctx := inst.Ctx
	var jsonBody map[string]any
	if err := json.Unmarshal(b, &jsonBody); err != nil {
		writeJsonWithStatusCode(writer, http.StatusBadRequest, commonResp{
			ErrNo:  http.StatusBadRequest,
			ErrMsg: "请求体不是合法 JSON: " + err.Error(),
		})
		return
	}
	rawYaml, ok := jsonBody["config"].(string)
	if !ok {
		writeJsonWithStatusCode(writer, http.StatusBadRequest, commonResp{
			ErrNo:  http.StatusBadRequest,
			ErrMsg: `缺少字符串字段 "config"`,
		})
		return
	}
	newConfig, err := configs.NewConfigWithBytes([]byte(rawYaml))
	if err != nil {
		writeJsonWithStatusCode(writer, http.StatusInternalServerError, commonResp{
			ErrNo:  http.StatusInternalServerError,
			ErrMsg: err.Error(),
		})
		return
	}
	// snapshot 是本次保存的基准（用户打开明文页时所见的配置），只用来判断"这一处用户改没改"。
	// 真正与提交结果比较的是 prevConfig（提交时刻的配置），见下面的三方合并说明。
	snapshot := configs.GetCurrentConfig()
	if snapshot == nil {
		writeJsonWithStatusCode(writer, http.StatusInternalServerError, commonResp{
			ErrNo:  http.StatusInternalServerError,
			ErrMsg: "配置尚未加载",
		})
		return
	}
	topKeys := rawConfigTopKeys(rawYaml)
	_, authSectionEdited := topKeys["douyu_auth"]
	_, soopAuthSectionEdited := topKeys["sooplive_auth"]
	_, cookiesSectionEdited := topKeys["cookies"]
	// 整体替换必须走带锁的提交路径：SetCurrentConfig 是裸写全局指针，绕开 updateMu 会把与本次
	// 编辑同期完成的其它更新（保存 cookie、斗鱼自动续期）从内存里覆盖掉；锁外再 Marshal 更是
	// 直接用这份基于旧快照的配置盖掉别人刚落盘的内容。
	//
	// 但只把提交挪进锁里还不够：UpdateWithRetry 每次重试都会把"最新配置的克隆"传进 mutator，
	// 若 mutator 依旧 *c = *基于入口快照算好的文档，重试就毫无意义（伪重试）——解析这几十毫秒里
	// 完成的并发写入会被静默覆盖回旧值，且随本次提交一起落盘，内存与磁盘同时倒带。
	// 实测：4000 条 cookies 的明文提交耗时约 60ms，窗口内的斗鱼续期结果 100% 被抹掉。
	// 因此所有继承/作废判定都必须以提交时刻的最新配置为基准在闭包内重算一遍（三方合并）。
	var prevConfig *configs.Config
	committed, err := configs.UpdateWithRetry(func(c *configs.Config) error {
		// c 是最新配置的私有克隆：先另存一份提交前状态，供运行态房间差异比对使用
		prevConfig = configs.CloneConfigShallow(c)
		prevConfig.RefreshLiveRoomIndexCache()
		applyRawConfigDoc(c, snapshot, newConfig, authSectionEdited, soopAuthSectionEdited, cookiesSectionEdited)
		return nil
	}, 3, 10*time.Millisecond)
	if err != nil {
		writeJsonWithStatusCode(writer, http.StatusInternalServerError, commonResp{
			ErrNo:  http.StatusInternalServerError,
			ErrMsg: err.Error(),
		})
		return
	}
	// 先落配置再驱动运行态差异：新增房间在初始化时会按全局配置取登录 cookie，
	// 若此时配置还是旧的，新加的房间就以匿名态建连。
	if err := applyLiveRoomsByConfig(ctx, prevConfig, committed); err != nil {
		writeJSON(writer, map[string]any{
			"error": err.Error(),
		})
		return
	}
	writeJSON(writer, commonResp{
		Data: "OK",
	})
}

// applyRawConfigDoc 把"设置明文"页提交的文档落到提交时刻的最新配置上（结果就地写回 latest）。
// snapshot 是用户打开该页时的配置，仅用于判断"这一处用户到底改没改"。
// 三方规则：文档相对快照未改动的内容一律取 latest（保住解析 YAML 这几十毫秒里完成的并发写入），
// 改动过的取文档值（以用户这次编辑为准）。latest 由 UpdateWithRetry 在锁内传入，是本函数的唯一写入目标。
func applyRawConfigDoc(latest, snapshot, doc *configs.Config, authSectionEdited, soopAuthSectionEdited, cookiesSectionEdited bool) {
	// 每次重试都从原始文档重新算：mutator 可能被调用多次，不能污染 doc
	merged := configs.CloneConfigShallow(doc)

	// 明文页提交的是"整份配置"，但"文档里根本没写这一节"与"写了但留空"含义不同：前者极可能是旧前端
	// 缓存或局部提交的客户端（它不认识斗鱼登录字段），后者才是用户显式清空。不区分的话，一次普通保存
	// 就把不可再生的长期凭证连同续期日程静默抹掉，表现为自动续期永久失效且没有任何提示（只能重新扫码）。
	// 实测：把 GET /raw-config 的响应体原样提交回去（YAML 里两节都缺）即可复现。
	//
	// 掩码是服务端自己写进文档的，不能算用户改动，所以判"改没改"要先把掩码换回快照里的原值再比。
	docAuth := doc.DouyuAuth
	if docAuth.LTP0 == douyuSecretMask {
		docAuth.LTP0 = snapshot.DouyuAuth.LTP0
	}
	if docAuth.DyDid == douyuSecretMask {
		docAuth.DyDid = snapshot.DouyuAuth.DyDid
	}
	if !authSectionEdited || docAuth == snapshot.DouyuAuth {
		// 整节没提交、或整节照抄没改（只想改别的东西时最常见）：续期日程、失效标记这些机器字段以最新值为准，
		// 否则一次普通保存就会把 keeper 刚写进去的 NextRefreshAt/NeedRescan/LastRenewSuccessAt 倒回入口快照。
		merged.DouyuAuth = latest.DouyuAuth
	} else {
		// 用户确实编辑了这一段：文档里的掩码仍要还原成最新票据，否则"打开明文页点一次保存"
		// 就会把掩码字符串本身当成票据存下来。真正要清除时留空 ltp0 即可。
		if merged.DouyuAuth.LTP0 == douyuSecretMask {
			merged.DouyuAuth.LTP0 = latest.DouyuAuth.LTP0
		}
		if merged.DouyuAuth.DyDid == douyuSecretMask {
			merged.DouyuAuth.DyDid = latest.DouyuAuth.DyDid
		}
	}

	// Soop 密码同理：明文页输出的是占位符，直接提交会把这串字符当成新密码存下来，
	// 之后自动登录拿它去换 cookie 必然失败，且面板只显示"已保存凭证"看不出异常。
	docSoop := doc.SoopLiveAuth
	if docSoop.Password == configs.LogSecretMask {
		docSoop.Password = snapshot.SoopLiveAuth.Password
	}
	if !soopAuthSectionEdited || docSoop == snapshot.SoopLiveAuth {
		// 整节没提交，或整节照抄没改：以提交时刻的最新值为准（这期间后台可能刚保存过新凭证）。
		merged.SoopLiveAuth = latest.SoopLiveAuth
	} else if merged.SoopLiveAuth.Password == configs.LogSecretMask {
		// 用户改了这一段里的别的东西（例如只改用户名）：密码位仍还原成最新原文。
		// 真要清除时把 password 留空提交即可。
		merged.SoopLiveAuth.Password = latest.SoopLiveAuth.Password
	}

	// cookies 同理做逐主机三方合并：用户在编辑器里没动过的那条，若已被后台续期改写，取最新值。
	// 整节缺失视为"这一节没提交"（局部客户端不认识它），不是"把所有登录 cookie 清空"。
	if !cookiesSectionEdited {
		merged.Cookies = cloneStrMap(latest.Cookies)
	} else {
		merged.Cookies = mergeCookies(snapshot.Cookies, doc.Cookies, latest.Cookies)
	}

	// OpenList 令牌由出封面阶段在每次轮换后自动回写，文档里带的还是打开页面那一刻的旧值：
	// 用户没改过这一项就取最新值，否则一次保存把新令牌倒回去，下次上传得重新登录。
	if doc.OpenList.Token == snapshot.OpenList.Token {
		merged.OpenList.Token = latest.OpenList.Token
	}

	// 继承原配置的文件路径
	merged.File = latest.File
	// 预先将旧配置中的 LiveId 迁移到新配置（相同 URL）
	oldMap := make(map[string]configs.LiveRoom, len(latest.LiveRooms))
	for _, room := range latest.LiveRooms {
		oldMap[room.Url] = room
	}
	for i := range merged.LiveRooms {
		if rOld, ok := oldMap[merged.LiveRooms[i].Url]; ok {
			merged.LiveRooms[i].LiveId = rOld.LiveId
		}
	}
	// 手工编辑"设置明文"里的斗鱼 cookie，只有换了登录账号才算换掉了一份登录态：按 acf_uid 判断。
	// 不能整串比较后一律清票据——补一个字段、删一个过期字段这类同账号微调非常常见，
	// 顺手清掉长期凭证的结果是自动续期静默失效，用户只看到"过几天又断流"却毫无线索。
	// 反之若真的换了账号却保留盘上的 LTP0，后台下次排期会用原账号换票并把它的 acf_* 合并回来，
	// 表现为"我手改的 cookie 过几天自己变了回去"（静默混号）。宁可放弃自动续期，也不能悄悄换人。
	// 比较的是三方合并后的最终登录态与当前盘上的登录态：同账号微调、或根本没提交这一节都不会触发。
	if oldUid, newUid := douyuCookieUid(latest.Cookies[dy.CookieHost]), douyuCookieUid(merged.Cookies[dy.CookieHost]); oldUid != newUid {
		// cookies 整节缺失时 newUid 必然为空，那不是"换了账号"而是"没提交这一节"，不能据此作废票据。
		if cookiesSectionEdited {
			if merged.DouyuAuth.LTP0 != "" {
				applog.GetLogger().Warnf("检测到斗鱼登录 Cookie 换了账号（acf_uid %s -> %s），已同时清除斗鱼长期凭证（需重新扫码才能启用自动续期）", oldUid, newUid)
			}
			merged.DouyuAuth = configs.DouyuAuth{}
		}
	}
	// 票据因"未提交该节"而被沿用、登录 cookie 却已不在（cookies 整节被局部提交带走）时，
	// 把排期改为"立即"：否则要空转到原排期（最长 3 天）才换回登录态，中间一直是匿名录制。
	if merged.DouyuAuth.LTP0 != "" && douyuCookieUid(merged.Cookies[dy.CookieHost]) == "" {
		merged.DouyuAuth.NextRefreshAt = 0
	}
	*latest = *merged
}

// mergeCookies 逐主机三方合并 cookies：snapshot 是用户打开页面所见，doc 是提交内容，latest 是当前最新值。
// 文档里没写这条主机时，区分"用户把它删了"（快照里有）与"提交期间别人才加上"（快照里没有），后者要保留。
func mergeCookies(snapshot, doc, latest map[string]string) map[string]string {
	merged := make(map[string]string, len(doc)+len(latest))
	for host, cookie := range doc {
		if old, ok := snapshot[host]; ok && old == cookie {
			// 用户没动这一条：以最新值为准（斗鱼自动续期可能已经把它换掉了）
			if cur, ok := latest[host]; ok {
				cookie = cur
			}
		}
		merged[host] = cookie
	}
	for host, cookie := range latest {
		if _, inDoc := doc[host]; inDoc {
			continue
		}
		if _, wasInSnapshot := snapshot[host]; !wasInSnapshot {
			merged[host] = cookie
		}
	}
	return merged
}

func cloneStrMap(src map[string]string) map[string]string {
	cp := make(map[string]string, len(src))
	for k, v := range src {
		cp[k] = v
	}
	return cp
}

func applyLiveRoomsByConfig(ctx context.Context, oldConfig *configs.Config, newConfig *configs.Config) error {
	inst := instance.GetInstance(ctx)
	newLiveRooms := newConfig.LiveRooms
	newUrlMap := make(map[string]*configs.LiveRoom)
	for index := range newLiveRooms {
		newRoom := &newLiveRooms[index]
		newUrlMap[newRoom.Url] = newRoom
		if room, err := oldConfig.GetLiveRoomByUrl(newRoom.Url); err != nil {
			// add live
			if _, err := addLiveImpl(ctx, newRoom.Url, newRoom.IsListening, newRoom.NotifyOnly, true); err != nil {
				return err
			}
		} else {
			live, ok := inst.Lives.Get(types.LiveID(room.LiveId))
			if !ok {
				return fmt.Errorf("live id: %s can not find", room.LiveId)
			}
			live.UpdateLiveOptionsbyConfig(ctx, newRoom)
			if room.IsListening != newRoom.IsListening {
				if newRoom.IsListening {
					// start listening
					if err := startListening(ctx, live); err != nil {
						return err
					}
				} else {
					// stop listening
					if err := stopListening(ctx, live.GetLiveId()); err != nil {
						return err
					}
				}
			}
		}
	}
	loopRooms := oldConfig.LiveRooms
	for _, room := range loopRooms {
		if _, ok := newUrlMap[room.Url]; !ok {
			// remove live
			live, ok := inst.Lives.Get(types.LiveID(room.LiveId))
			if !ok {
				return fmt.Errorf("live id: %s can not find", room.LiveId)
			}
			removeLiveImpl(ctx, live)
		}
	}
	return nil
}

func getInfo(writer http.ResponseWriter, r *http.Request) {
	writeJSON(writer, consts.GetAppInfo())
}

// EffectiveConfigResponse 用于返回配置及其实际生效值
type EffectiveConfigResponse struct {
	*configs.Config

	// 额外的实际生效值字段
	ActualOutPutPath         string `json:"actual_out_put_path"`
	ActualFfmpegPath         string `json:"actual_ffmpeg_path"`
	ActualLogFolder          string `json:"actual_log_folder"`
	ActualAppDataPath        string `json:"actual_app_data_path"`
	ActualReadOnlyToolFolder string `json:"actual_read_only_tool_folder"`
	ActualToolRootFolder     string `json:"actual_tool_root_folder"`
	DefaultOutPutTmpl        string `json:"default_out_put_tmpl"`
	TimeoutInSeconds         int    `json:"timeout_in_seconds"`
	LiveRoomsCount           int    `json:"live_rooms_count"`

	// 下载器可用性信息
	DownloaderAvailability tools.DownloaderAvailability `json:"downloader_availability"`
	// 可用的下载器类型列表
	AvailableDownloaders []string `json:"available_downloaders"`
}

// getEffectiveConfig 获取实际生效的配置值（用于GUI模式显示）
func getEffectiveConfig(writer http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	cfg := configs.GetCurrentConfig()
	if cfg == nil {
		writeJsonWithStatusCode(writer, http.StatusInternalServerError, commonResp{
			ErrNo:  http.StatusInternalServerError,
			ErrMsg: "配置未初始化",
		})
		return
	}

	// 获取实际的 ffmpeg 路径
	actualFfmpegPath, err := utils.GetFFmpegPath(ctx)
	if err == nil {
		actualFfmpegPath, _ = filepath.Abs(actualFfmpegPath)
	} else {
		actualFfmpegPath = "未找到"
	}

	// 获取输出路径的绝对路径
	actualOutPutPath, _ := filepath.Abs(cfg.OutPutPath)

	// 获取日志输出目录的绝对路径
	actualLogFolder := cfg.Log.OutPutFolder
	if actualLogFolder != "" {
		actualLogFolder, _ = filepath.Abs(actualLogFolder)
	}

	// 获取应用数据目录的绝对路径
	actualAppDataPath := cfg.AppDataPath
	if actualAppDataPath == "" {
		actualAppDataPath = filepath.Join(cfg.OutPutPath, ".appdata")
	}
	actualAppDataPath, _ = filepath.Abs(actualAppDataPath)

	// 获取只读工具目录的绝对路径
	actualReadOnlyToolFolder := cfg.ReadOnlyToolFolder
	if actualReadOnlyToolFolder != "" {
		actualReadOnlyToolFolder, _ = filepath.Abs(actualReadOnlyToolFolder)
	}

	// 获取可写工具目录的绝对路径
	actualToolRootFolder := cfg.ToolRootFolder
	if actualToolRootFolder != "" {
		actualToolRootFolder, _ = filepath.Abs(actualToolRootFolder)
	}

	// 默认输出模板
	defaultOutputTmpl := `{{ .Live.GetPlatformCNName }}/{{ with .Live.GetOptions.NickName }}{{ . | filenameFilter }}{{ else }}{{ .HostName | filenameFilter }}{{ end }}/[{{ now | date "2006-01-02 15-04-05"}}][{{ .HostName | filenameFilter }}][{{ .RoomName | filenameFilter }}].flv`

	// 获取下载器可用性信息
	downloaderAvail := tools.GetDownloaderAvailability()

	// 构建可用下载器列表
	availableDownloaders := []string{string(configs.DownloaderNative)} // native 始终可用
	if downloaderAvail.FFmpegAvailable {
		// 把 ffmpeg 放在第一位作为推荐选项
		availableDownloaders = append([]string{string(configs.DownloaderFFmpeg)}, availableDownloaders...)
	}
	if downloaderAvail.BililiveRecorderAvailable {
		availableDownloaders = append(availableDownloaders, string(configs.DownloaderBililiveRecorder))
	}

	// 构建响应
	response := &EffectiveConfigResponse{
		Config:                   cfg,
		ActualOutPutPath:         actualOutPutPath,
		ActualFfmpegPath:         actualFfmpegPath,
		ActualLogFolder:          actualLogFolder,
		ActualAppDataPath:        actualAppDataPath,
		ActualReadOnlyToolFolder: actualReadOnlyToolFolder,
		ActualToolRootFolder:     actualToolRootFolder,
		DefaultOutPutTmpl:        defaultOutputTmpl,
		TimeoutInSeconds:         cfg.TimeoutInUs / 1000000,
		LiveRoomsCount:           len(cfg.LiveRooms),
		DownloaderAvailability:   downloaderAvail,
		AvailableDownloaders:     availableDownloaders,
	}

	writeJSON(writer, response)
}

// getPlatformStats 获取平台相关的直播间统计
func getPlatformStats(writer http.ResponseWriter, r *http.Request) {
	inst := instance.GetInstance(r.Context())
	cfg := configs.GetCurrentConfig()
	if cfg == nil {
		writeJsonWithStatusCode(writer, http.StatusInternalServerError, commonResp{
			ErrNo:  http.StatusInternalServerError,
			ErrMsg: "配置未初始化",
		})
		return
	}
	platformRateLimitEnabled := ratelimit.GetGlobalRateLimiter().Enabled()

	// 统计每个平台的直播间（只统计正在监控的）
	platformRooms := make(map[string][]map[string]interface{})
	platformListeningCount := make(map[string]int) // 每个平台正在监控的直播间数量

	for _, room := range cfg.LiveRooms {
		platformKey := configs.GetPlatformKeyFromUrl(room.Url)
		if platformKey == "" {
			platformKey = "unknown"
		}

		// 计算 LiveId（从 URL 生成，与 live/internal/genLiveId 逻辑一致）
		var liveId string
		if room.LiveId != "" {
			liveId = string(room.LiveId)
		} else if parsedUrl, err := url.Parse(room.Url); err == nil {
			liveId = string(types.LiveID(utils.GetMd5String([]byte(parsedUrl.Host + parsedUrl.Path))))
		}

		roomInfo := map[string]interface{}{
			"url":          room.Url,
			"is_listening": room.IsListening,
			"quality":      room.Quality,
			"audio_only":   room.AudioOnly,
			"nick_name":    room.NickName,
			"live_id":      liveId,
			"room_config": map[string]interface{}{
				"danmaku_enable": room.DanmakuEnable,
				"danmaku":        room.Danmaku,
			},
		}

		// 获取直播间信息（不触发网络请求，使用 parseInfo 处理缓存和默认值）
		if liveInstance, ok := inst.Lives.Get(room.LiveId); ok {
			info := parseInfo(r.Context(), liveInstance)
			roomInfo["host_name"] = info.HostName
			roomInfo["room_name"] = info.RoomName
			roomInfo["status"] = info.Status
		}

		platformRooms[platformKey] = append(platformRooms[platformKey], roomInfo)
		if room.IsListening {
			platformListeningCount[platformKey]++
		}
	}

	// 所有已知平台列表
	allKnownPlatforms := []string{
		"bilibili", "douyin", "douyu", "huya", "kuaishou", "yy", "acfun",
		"lang", "missevan", "openrec", "weibolive", "xiaohongshu", "yizhibo",
		"hongdoufm", "zhanqi", "cc", "twitch", "qq", "huajiao", "sooplive",
	}

	// 构建平台统计响应
	stats := make([]map[string]interface{}, 0)

	// 首先添加有直播间的平台（按是否有配置和直播间数量排序）
	processedPlatforms := make(map[string]bool)

	// 1. 先添加配置中定义且有直播间的平台
	for platformKey, platformConfig := range cfg.PlatformConfigs {
		rooms := platformRooms[platformKey]
		if rooms == nil {
			rooms = []map[string]interface{}{}
		}
		listeningCount := platformListeningCount[platformKey]

		// 计算实际访问间隔
		interval := cfg.Interval
		if platformConfig.Interval != nil {
			interval = *platformConfig.Interval
		}
		actualAccessInterval := 0.0
		if listeningCount > 0 {
			actualAccessInterval = float64(interval) / float64(listeningCount)
		}

		// 检查是否低于最小访问间隔
		warningMessage := ""
		if platformRateLimitEnabled && listeningCount > 0 && platformConfig.MinAccessIntervalSec > 0 && actualAccessInterval < float64(platformConfig.MinAccessIntervalSec) {
			effectiveInterval := float64(platformConfig.MinAccessIntervalSec) * float64(listeningCount)
			warningMessage = fmt.Sprintf("当前设置下实际每个直播间的检测间隔约为 %.1f 秒（受最小访问间隔限制）", effectiveInterval)
		}

		stats = append(stats, map[string]interface{}{
			"platform_key":            platformKey,
			"platform_name":           platformConfig.Name,
			"room_count":              len(rooms),
			"listening_count":         listeningCount,
			"rooms":                   rooms,
			"has_config":              true,
			"has_rooms":               len(rooms) > 0,
			"min_access_interval_sec": platformConfig.MinAccessIntervalSec,
			"interval":                platformConfig.Interval,
			"effective_interval":      interval,
			"actual_access_interval":  actualAccessInterval,
			"warning_message":         warningMessage,
			"out_put_path":            platformConfig.OutPutPath,
			"ffmpeg_path":             platformConfig.FfmpegPath,
		})
		processedPlatforms[platformKey] = true
	}

	// 2. 添加有直播间但没有配置的平台
	for platformKey, rooms := range platformRooms {
		if processedPlatforms[platformKey] {
			continue
		}
		listeningCount := platformListeningCount[platformKey]

		// 使用全局间隔计算实际访问间隔
		actualAccessInterval := 0.0
		if listeningCount > 0 {
			actualAccessInterval = float64(cfg.Interval) / float64(listeningCount)
		}

		stats = append(stats, map[string]interface{}{
			"platform_key":           platformKey,
			"room_count":             len(rooms),
			"listening_count":        listeningCount,
			"rooms":                  rooms,
			"has_config":             false,
			"has_rooms":              true,
			"effective_interval":     cfg.Interval,
			"actual_access_interval": actualAccessInterval,
		})
		processedPlatforms[platformKey] = true
	}

	// 3. 添加没有直播间但有配置的平台
	for platformKey, platformConfig := range cfg.PlatformConfigs {
		if processedPlatforms[platformKey] {
			continue
		}
		stats = append(stats, map[string]interface{}{
			"platform_key":            platformKey,
			"platform_name":           platformConfig.Name,
			"room_count":              0,
			"listening_count":         0,
			"rooms":                   []map[string]interface{}{},
			"has_config":              true,
			"has_rooms":               false,
			"min_access_interval_sec": platformConfig.MinAccessIntervalSec,
			"interval":                platformConfig.Interval,
			"out_put_path":            platformConfig.OutPutPath,
			"ffmpeg_path":             platformConfig.FfmpegPath,
		})
		processedPlatforms[platformKey] = true
	}

	// 返回所有已知平台（用于添加新平台配置）
	availablePlatforms := make([]string, 0)
	for _, p := range allKnownPlatforms {
		if !processedPlatforms[p] {
			availablePlatforms = append(availablePlatforms, p)
		}
	}

	response := map[string]interface{}{
		"platforms":                   stats,
		"available_platforms":         availablePlatforms,
		"global_interval":             cfg.Interval,
		"platform_rate_limit_enabled": platformRateLimitEnabled,
	}

	writeJSON(writer, response)
}

// previewOutputTmpl 预览输出模板生成的路径
func previewOutputTmpl(writer http.ResponseWriter, r *http.Request) {
	cfg := configs.GetCurrentConfig()
	if cfg == nil {
		writeJsonWithStatusCode(writer, http.StatusInternalServerError, commonResp{
			ErrNo:  http.StatusInternalServerError,
			ErrMsg: "配置未初始化",
		})
		return
	}

	b, err := io.ReadAll(r.Body)
	if err != nil {
		writeJsonWithStatusCode(writer, http.StatusBadRequest, commonResp{
			ErrNo:  http.StatusBadRequest,
			ErrMsg: err.Error(),
		})
		return
	}

	var req struct {
		Template   string `json:"template"`
		OutPutPath string `json:"out_put_path"`
	}
	if err := json.Unmarshal(b, &req); err != nil {
		writeJsonWithStatusCode(writer, http.StatusBadRequest, commonResp{
			ErrNo:  http.StatusBadRequest,
			ErrMsg: "无效的JSON格式: " + err.Error(),
		})
		return
	}

	// 使用默认模板如果未提供
	templateStr := req.Template
	if templateStr == "" {
		templateStr = `{{ .Live.GetPlatformCNName }}/{{ with .Live.GetOptions.NickName }}{{ . | filenameFilter }}{{ else }}{{ .HostName | filenameFilter }}{{ end }}/[{{ now | date "2006-01-02 15-04-05"}}][{{ .HostName | filenameFilter }}][{{ .RoomName | filenameFilter }}].flv`
	}

	outPutPath := req.OutPutPath
	if outPutPath == "" {
		outPutPath = cfg.OutPutPath
	}

	// 解析模板
	tmpl, err := template.New("preview").Funcs(utils.GetFuncMap(cfg)).Parse(templateStr)
	if err != nil {
		writeJSON(writer, map[string]interface{}{
			"success":    false,
			"error":      "模板语法错误: " + err.Error(),
			"error_type": "parse_error",
		})
		return
	}

	// 创建模拟数据
	mockInfo := &live.Info{
		HostName: "示例主播",
		RoomName: "示例直播间标题",
	}

	// 创建一个模拟的 Live 对象用于预览
	mockLive := &mockLiveForPreview{
		platformCNName: "示例平台",
		options: &live.Options{
			NickName: "示例昵称",
		},
	}
	mockInfo.Live = mockLive

	// 执行模板
	buf := new(bytes.Buffer)
	if err := tmpl.Execute(buf, mockInfo); err != nil {
		writeJSON(writer, map[string]interface{}{
			"success":    false,
			"error":      "模板执行错误: " + err.Error(),
			"error_type": "execute_error",
		})
		return
	}

	// 计算最终路径
	absOutPutPath, _ := filepath.Abs(outPutPath)
	previewPath := filepath.Join(absOutPutPath, buf.String())

	writeJSON(writer, map[string]interface{}{
		"success":       true,
		"preview_path":  previewPath,
		"relative_path": buf.String(),
		"base_path":     absOutPutPath,
	})
}

// mockLiveForPreview 用于模板预览的模拟 Live 对象
type mockLiveForPreview struct {
	platformCNName string
	options        *live.Options
}

func (m *mockLiveForPreview) GetPlatformCNName() string {
	return m.platformCNName
}

func (m *mockLiveForPreview) GetOptions() *live.Options {
	return m.options
}

// 实现 live.Live 接口的其他方法（返回空值）
func (m *mockLiveForPreview) SetLiveIdByString(string)     {}
func (m *mockLiveForPreview) GetLiveId() types.LiveID      { return "" }
func (m *mockLiveForPreview) GetRawUrl() string            { return "" }
func (m *mockLiveForPreview) GetInfo() (*live.Info, error) { return nil, nil }
func (m *mockLiveForPreview) GetInfoWithInterval(ctx context.Context) (*live.Info, error) {
	return nil, nil
}
func (m *mockLiveForPreview) GetStreamUrls() ([]*url.URL, error)             { return nil, nil }
func (m *mockLiveForPreview) Close()                                         {}
func (m *mockLiveForPreview) GetStreamInfos() ([]*live.StreamUrlInfo, error) { return nil, nil }
func (m *mockLiveForPreview) GetLastStartTime() time.Time                    { return time.Time{} }
func (m *mockLiveForPreview) SetLastStartTime(time.Time)                     {}
func (m *mockLiveForPreview) UpdateLiveOptionsbyConfig(ctx context.Context, room *configs.LiveRoom) error {
	return nil
}
func (m *mockLiveForPreview) GetLogger() *livelogger.LiveLogger { return nil }

// updateConfig 更新配置（支持部分更新）
func updateConfig(writer http.ResponseWriter, r *http.Request) {
	b, err := io.ReadAll(r.Body)
	if err != nil {
		writeJsonWithStatusCode(writer, http.StatusBadRequest, commonResp{
			ErrNo:  http.StatusBadRequest,
			ErrMsg: err.Error(),
		})
		return
	}

	var updates map[string]interface{}
	if err := json.Unmarshal(b, &updates); err != nil {
		writeJsonWithStatusCode(writer, http.StatusBadRequest, commonResp{
			ErrNo:  http.StatusBadRequest,
			ErrMsg: "无效的JSON格式: " + err.Error(),
		})
		return
	}

	_, err = configs.UpdateWithRetry(func(c *configs.Config) error {
		// 应用更新到配置
		if err := applyConfigUpdates(c, updates); err != nil {
			return err
		}
		// 校验配置
		return c.Verify()
	}, 3, 10*time.Millisecond)

	if err != nil {
		writeJsonWithStatusCode(writer, http.StatusInternalServerError, commonResp{
			ErrNo:  http.StatusInternalServerError,
			ErrMsg: "更新配置失败: " + err.Error(),
		})
		return
	}

	writeJSON(writer, commonResp{
		Data: "OK",
	})
}

// applyConfigUpdates 将更新应用到配置
func applyConfigUpdates(c *configs.Config, updates map[string]interface{}) error {
	// 处理 RPC 配置
	if rpc, ok := updates["rpc"].(map[string]interface{}); ok {
		if enable, ok := rpc["enable"].(bool); ok {
			c.RPC.Enable = enable
		}
		if bind, ok := rpc["bind"].(string); ok {
			c.RPC.Bind = bind
		}
	}

	// 处理基本配置
	if debug, ok := updates["debug"].(bool); ok {
		c.Debug = debug
	}
	if interval, ok := updates["interval"].(float64); ok {
		c.Interval = int(interval)
	}
	if outPutPath, ok := updates["out_put_path"].(string); ok {
		c.OutPutPath = outPutPath
	}
	if ffmpegPath, ok := updates["ffmpeg_path"].(string); ok {
		c.FfmpegPath = ffmpegPath
	}
	if outputTmpl, ok := updates["out_put_tmpl"].(string); ok {
		c.OutputTmpl = outputTmpl
	}
	if timeoutSec, ok := updates["timeout_in_seconds"].(float64); ok {
		c.TimeoutInUs = int(timeoutSec * 1000000)
	}
	if appDataPath, ok := updates["app_data_path"].(string); ok {
		c.AppDataPath = appDataPath
	}
	if readOnlyToolFolder, ok := updates["read_only_tool_folder"].(string); ok {
		c.ReadOnlyToolFolder = readOnlyToolFolder
	}
	if toolRootFolder, ok := updates["tool_root_folder"].(string); ok {
		c.ToolRootFolder = toolRootFolder
	}
	if danmakuEnable, ok := updates["danmaku_enable"].(bool); ok {
		c.DanmakuEnable = danmakuEnable
	}
	if danmaku, ok := updates["danmaku"].(map[string]interface{}); ok {
		if fontSize, ok := danmaku["font_size"].(float64); ok {
			c.Danmaku.FontSize = int(fontSize)
		}
		if fontName, ok := danmaku["font_name"].(string); ok {
			c.Danmaku.FontName = fontName
		}
		if displayMode, ok := danmaku["scroll_area"].(string); ok {
			c.Danmaku.ScrollArea = displayMode
		}
		if scrollTime, ok := danmaku["scroll_time"].(float64); ok {
			c.Danmaku.ScrollTime = int(scrollTime)
		}
		if resolution, ok := danmaku["resolution"].(string); ok {
			c.Danmaku.Resolution = resolution
		}
		if outline, ok := danmaku["outline"].(float64); ok {
			c.Danmaku.Outline = configs.IntPtr(int(outline))
		}
		if opacity, ok := danmaku["opacity"].(float64); ok {
			c.Danmaku.Opacity = configs.IntPtr(int(opacity))
		}
		if recordGift, ok := danmaku["record_gift"].(bool); ok {
			c.Danmaku.RecordGift = configs.BoolPtr(recordGift)
		} else if _, exists := danmaku["record_gift"]; exists && danmaku["record_gift"] == nil {
			c.Danmaku.RecordGift = nil
		}
		if recordDouyuGift, ok := danmaku["record_douyu_gift"].(bool); ok {
			c.Danmaku.RecordDouyuGift = configs.BoolPtr(recordDouyuGift)
		} else if _, exists := danmaku["record_douyu_gift"]; exists && danmaku["record_douyu_gift"] == nil {
			c.Danmaku.RecordDouyuGift = nil
		}
		if recordDouyinGift, ok := danmaku["record_douyin_gift"].(bool); ok {
			c.Danmaku.RecordDouyinGift = configs.BoolPtr(recordDouyinGift)
		} else if _, exists := danmaku["record_douyin_gift"]; exists && danmaku["record_douyin_gift"] == nil {
			c.Danmaku.RecordDouyinGift = nil
		}
		if recordGuard, ok := danmaku["record_guard"].(bool); ok {
			c.Danmaku.RecordGuard = configs.BoolPtr(recordGuard)
		} else if _, exists := danmaku["record_guard"]; exists && danmaku["record_guard"] == nil {
			c.Danmaku.RecordGuard = nil
		}
		if recordSuperChat, ok := danmaku["record_super_chat"].(bool); ok {
			c.Danmaku.RecordSuperChat = configs.BoolPtr(recordSuperChat)
		} else if _, exists := danmaku["record_super_chat"]; exists && danmaku["record_super_chat"] == nil {
			c.Danmaku.RecordSuperChat = nil
		}
		if showUsername, ok := danmaku["show_username"].(bool); ok {
			c.Danmaku.ShowUsername = configs.BoolPtr(showUsername)
		} else if _, exists := danmaku["show_username"]; exists && danmaku["show_username"] == nil {
			c.Danmaku.ShowUsername = nil
		}
		if guardPosition, ok := danmaku["guard_position"].(string); ok {
			c.Danmaku.GuardPosition = guardPosition
		}
		if scPosition, ok := danmaku["sc_position"].(string); ok {
			c.Danmaku.ScPosition = scPosition
		}
		if err := c.Danmaku.Validate(); err != nil {
			return fmt.Errorf("弹幕参数无效: %w", err)
		}
	}
	if soopAuth, ok := updates["sooplive_auth"].(map[string]interface{}); ok {
		if username, ok := soopAuth["username"].(string); ok {
			c.SoopLiveAuth.Username = username
		}
		if password, ok := soopAuth["password"].(string); ok {
			c.SoopLiveAuth.Password = password
		}
	}

	// 处理日志配置
	if log, ok := updates["log"].(map[string]interface{}); ok {
		if outPutFolder, ok := log["out_put_folder"].(string); ok {
			c.Log.OutPutFolder = outPutFolder
		}
		if saveLastLog, ok := log["save_last_log"].(bool); ok {
			c.Log.SaveLastLog = saveLastLog
		}
		if saveEveryLog, ok := log["save_every_log"].(bool); ok {
			c.Log.SaveEveryLog = saveEveryLog
		}
		if rotateDays, ok := log["rotate_days"].(float64); ok {
			c.Log.RotateDays = int(rotateDays)
		}
	}

	// 处理功能特性配置
	if feature, ok := updates["feature"].(map[string]interface{}); ok {
		if downloaderType, ok := feature["downloader_type"].(string); ok {
			c.Feature.DownloaderType = configs.ParseDownloaderType(downloaderType)
		}
		if useNativeFlvParser, ok := feature["use_native_flv_parser"].(bool); ok {
			c.Feature.UseNativeFlvParser = useNativeFlvParser
		}
		if removeSymbolOther, ok := feature["remove_symbol_other_character"].(bool); ok {
			c.Feature.RemoveSymbolOtherCharacter = removeSymbolOther
		}
	}

	// 处理视频分割策略
	if vss, ok := updates["video_split_strategies"].(map[string]interface{}); ok {
		if onRoomNameChanged, ok := vss["on_room_name_changed"].(bool); ok {
			c.VideoSplitStrategies.OnRoomNameChanged = onRoomNameChanged
		}
		if maxDuration, ok := vss["max_duration"].(float64); ok {
			c.VideoSplitStrategies.MaxDuration = time.Duration(maxDuration)
		}
		if maxFileSize, ok := vss["max_file_size"].(float64); ok {
			c.VideoSplitStrategies.MaxFileSize = configs.ByteSize(int64(maxFileSize))
		} else if maxFileSizeStr, ok := vss["max_file_size"].(string); ok {
			if parsed, err := configs.ParseByteSize(maxFileSizeStr); err == nil {
				c.VideoSplitStrategies.MaxFileSize = parsed
			}
		}
	}

	// 处理录制完成后动作
	if orf, ok := updates["on_record_finished"].(map[string]interface{}); ok {
		if convertToMp4, ok := orf["convert_to_mp4"].(bool); ok {
			c.OnRecordFinished.ConvertToMp4 = convertToMp4
		}
		if deleteFlv, ok := orf["delete_flv_after_convert"].(bool); ok {
			c.OnRecordFinished.DeleteFlvAfterConvert = deleteFlv
		}
		if customCmd, ok := orf["custom_commandline"].(string); ok {
			c.OnRecordFinished.CustomCommandline = customCmd
		}
		if fixFlv, ok := orf["fix_flv_at_first"].(bool); ok {
			c.OnRecordFinished.FixFlvAtFirst = fixFlv
		}
		if burnSubtitles, ok := orf["burn_subtitles"].(bool); ok {
			c.OnRecordFinished.BurnSubtitles = burnSubtitles
		}
		if codec, ok := orf["burn_subtitles_codec"].(string); ok {
			c.OnRecordFinished.BurnSubtitlesCodec = codec
		}
		if crf, ok := orf["burn_subtitles_crf"].(string); ok {
			c.OnRecordFinished.BurnSubtitlesCrf = crf
		}
		if preset, ok := orf["burn_subtitles_preset"].(string); ok {
			c.OnRecordFinished.BurnSubtitlesPreset = preset
		}
		if deleteAss, ok := orf["burn_delete_ass"].(bool); ok {
			c.OnRecordFinished.BurnDeleteAss = deleteAss
		}
		if deleteSource, ok := orf["burn_delete_source"].(bool); ok {
			c.OnRecordFinished.BurnDeleteSource = deleteSource
		}
		// 处理云上传配置
		if cloudUpload, ok := orf["cloud_upload"].(map[string]interface{}); ok {
			if enable, ok := cloudUpload["enable"].(bool); ok {
				c.OnRecordFinished.CloudUpload.Enable = enable
			}
			if storageName, ok := cloudUpload["storage_name"].(string); ok {
				c.OnRecordFinished.CloudUpload.StorageName = storageName
			}
			if uploadPathTmpl, ok := cloudUpload["upload_path_tmpl"].(string); ok {
				c.OnRecordFinished.CloudUpload.UploadPathTmpl = uploadPathTmpl
			}
			if deleteAfter, ok := cloudUpload["delete_after_upload"].(bool); ok {
				c.OnRecordFinished.CloudUpload.DeleteAfterUpload = deleteAfter
			}
			if deleteAllAfter, ok := cloudUpload["delete_all_after_upload"].(bool); ok {
				c.OnRecordFinished.CloudUpload.DeleteAllAfterUpload = deleteAllAfter
			}
			if uploadSubtitles, ok := cloudUpload["upload_subtitles"].(bool); ok {
				c.OnRecordFinished.CloudUpload.UploadSubtitles = uploadSubtitles
			}
		}
		// 处理上传时机
		if uploadTiming, ok := orf["upload_timing"].(string); ok {
			c.OnRecordFinished.UploadTiming = configs.UploadTiming(uploadTiming)
		}
	}

	// 处理通知配置
	if notify, ok := updates["notify"].(map[string]interface{}); ok {
		if sendRecordingSummary, ok := notify["send_recording_summary"].(bool); ok {
			c.Notify.SendRecordingSummary = sendRecordingSummary
		}
		if telegram, ok := notify["telegram"].(map[string]interface{}); ok {
			if enable, ok := telegram["enable"].(bool); ok {
				c.Notify.Telegram.Enable = enable
			}
			if withNotification, ok := telegram["withNotification"].(bool); ok {
				c.Notify.Telegram.WithNotification = withNotification
			}
			if botToken, ok := telegram["botToken"].(string); ok {
				c.Notify.Telegram.BotToken = botToken
			}
			if chatID, ok := telegram["chatID"].(string); ok {
				c.Notify.Telegram.ChatID = chatID
			}
		}
		if email, ok := notify["email"].(map[string]interface{}); ok {
			if enable, ok := email["enable"].(bool); ok {
				c.Notify.Email.Enable = enable
			}
			if smtpHost, ok := email["smtpHost"].(string); ok {
				c.Notify.Email.SMTPHost = smtpHost
			}
			if smtpPort, ok := email["smtpPort"].(float64); ok {
				c.Notify.Email.SMTPPort = int(smtpPort)
			}
			if senderEmail, ok := email["senderEmail"].(string); ok {
				c.Notify.Email.SenderEmail = senderEmail
			}
			if senderPassword, ok := email["senderPassword"].(string); ok {
				c.Notify.Email.SenderPassword = senderPassword
			}
			if recipientEmail, ok := email["recipientEmail"].(string); ok {
				c.Notify.Email.RecipientEmail = recipientEmail
			}
		}
		if barkCfg, ok := notify["bark"].(map[string]interface{}); ok {
			if enable, ok := barkCfg["enable"].(bool); ok {
				c.Notify.Bark.Enable = enable
			}
			if serverURL, ok := barkCfg["serverURL"].(string); ok {
				c.Notify.Bark.ServerURL = serverURL
			}
			if deviceKey, ok := barkCfg["deviceKey"].(string); ok {
				c.Notify.Bark.DeviceKey = deviceKey
			}
			if sound, ok := barkCfg["sound"].(string); ok {
				c.Notify.Bark.Sound = sound
			}
			if group, ok := barkCfg["group"].(string); ok {
				c.Notify.Bark.Group = group
			}
			if icon, ok := barkCfg["icon"].(string); ok {
				c.Notify.Bark.Icon = icon
			}
			if level, ok := barkCfg["level"].(string); ok {
				c.Notify.Bark.Level = level
			}
		}
		if wxpusherCfg, ok := notify["wxpusher"].(map[string]interface{}); ok {
			if enable, ok := wxpusherCfg["enable"].(bool); ok {
				c.Notify.WxPusher.Enable = enable
			}
			if appToken, ok := wxpusherCfg["appToken"].(string); ok {
				c.Notify.WxPusher.AppToken = appToken
			}
			if uids, ok := wxpusherCfg["uids"].([]interface{}); ok {
				uidList := make([]string, 0, len(uids))
				for _, uid := range uids {
					if uidStr, ok := uid.(string); ok && uidStr != "" {
						uidList = append(uidList, uidStr)
					}
				}
				c.Notify.WxPusher.UIDs = uidList
			}
		}
	}

	// 处理代理配置
	if proxy, ok := updates["proxy"].(map[string]interface{}); ok {
		if enable, ok := proxy["enable"].(bool); ok {
			c.Proxy.Enable = enable
		}
		if url, ok := proxy["url"].(string); ok {
			c.Proxy.URL = url
		}
	}

	// 处理全局流偏好配置
	if streamPref, ok := updates["stream_preference"].(map[string]interface{}); ok {
		// 处理 quality
		if quality, ok := streamPref["quality"].(string); ok {
			if quality == "" {
				c.StreamPreference.Quality = nil
			} else {
				c.StreamPreference.Quality = &quality
			}
		}

		// 处理 attributes
		if attrs, ok := streamPref["attributes"].(map[string]interface{}); ok {
			if len(attrs) == 0 {
				c.StreamPreference.Attributes = nil
			} else {
				attrMap := make(map[string]string)
				for k, v := range attrs {
					if strVal, ok := v.(string); ok {
						attrMap[k] = strVal
					}
				}
				if len(attrMap) > 0 {
					c.StreamPreference.Attributes = &attrMap
				} else {
					c.StreamPreference.Attributes = nil
				}
			}
		}
	}

	// 处理自动更新配置
	if update, ok := updates["update"].(map[string]interface{}); ok {
		if autoCheck, ok := update["auto_check"].(bool); ok {
			c.Update.AutoCheck = autoCheck
		}
		if checkIntervalHours, ok := update["check_interval_hours"].(float64); ok {
			c.Update.CheckIntervalHours = int(checkIntervalHours)
		}
		if autoDownload, ok := update["auto_download"].(bool); ok {
			c.Update.AutoDownload = autoDownload
		}
		if includePrerelease, ok := update["include_prerelease"].(bool); ok {
			c.Update.IncludePrerelease = includePrerelease
		}
	}

	// 处理 OpenList 配置
	if openlistCfg, ok := updates["openlist"].(map[string]interface{}); ok {
		if port, ok := openlistCfg["port"].(float64); ok {
			c.OpenList.Port = int(port)
		}
		if dataPath, ok := openlistCfg["data_path"].(string); ok {
			c.OpenList.DataPath = dataPath
		}
		if username, ok := openlistCfg["username"].(string); ok {
			c.OpenList.Username = username
		}
		if password, ok := openlistCfg["password"].(string); ok {
			c.OpenList.Password = password
		}
		if token, ok := openlistCfg["token"].(string); ok {
			c.OpenList.Token = token
		}
	}

	return nil
}

// updatePlatformConfig 更新平台配置
func updatePlatformConfig(writer http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	platformKey := vars["platform"]

	b, err := io.ReadAll(r.Body)
	if err != nil {
		writeJsonWithStatusCode(writer, http.StatusBadRequest, commonResp{
			ErrNo:  http.StatusBadRequest,
			ErrMsg: err.Error(),
		})
		return
	}

	var updates map[string]interface{}
	if err := json.Unmarshal(b, &updates); err != nil {
		writeJsonWithStatusCode(writer, http.StatusBadRequest, commonResp{
			ErrNo:  http.StatusBadRequest,
			ErrMsg: "无效的JSON格式: " + err.Error(),
		})
		return
	}

	_, err = configs.UpdateWithRetry(func(c *configs.Config) error {
		if c.PlatformConfigs == nil {
			c.PlatformConfigs = make(map[string]configs.PlatformConfig)
		}

		pc := c.PlatformConfigs[platformKey]

		// 更新平台配置
		if name, ok := updates["name"].(string); ok {
			pc.Name = name
		}
		if minInterval, ok := updates["min_access_interval_sec"].(float64); ok {
			pc.MinAccessIntervalSec = int(minInterval)
		}
		// 使用助手函数更新可覆盖配置
		applyOverridableConfigUpdates(&pc.OverridableConfig, updates)

		// 验证弹幕配置有效性
		if pc.Danmaku != nil {
			if err := pc.Danmaku.ValidateWithPlatform(platformKey); err != nil {
				return fmt.Errorf("弹幕参数无效: %w", err)
			}
		}

		c.PlatformConfigs[platformKey] = pc
		return nil
	}, 3, 10*time.Millisecond)

	if err != nil {
		writeJsonWithStatusCode(writer, http.StatusInternalServerError, commonResp{
			ErrNo:  http.StatusInternalServerError,
			ErrMsg: "更新平台配置失败: " + err.Error(),
		})
		return
	}

	writeJSON(writer, commonResp{
		Data: "OK",
	})
}

// deletePlatformConfig 删除平台配置
func deletePlatformConfig(writer http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	platformKey := vars["platform"]

	_, err := configs.UpdateWithRetry(func(c *configs.Config) error {
		if c.PlatformConfigs != nil {
			delete(c.PlatformConfigs, platformKey)
		}
		return nil
	}, 3, 10*time.Millisecond)

	if err != nil {
		writeJsonWithStatusCode(writer, http.StatusInternalServerError, commonResp{
			ErrNo:  http.StatusInternalServerError,
			ErrMsg: "删除平台配置失败: " + err.Error(),
		})
		return
	}

	writeJSON(writer, commonResp{
		Data: "OK",
	})
}

// updateRoomConfigById 通过 ID 更新直播间配置
func updateRoomConfigById(writer http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	liveId := vars["id"]

	b, err := io.ReadAll(r.Body)
	if err != nil {
		writeJsonWithStatusCode(writer, http.StatusBadRequest, commonResp{
			ErrNo:  http.StatusBadRequest,
			ErrMsg: err.Error(),
		})
		return
	}

	var updates map[string]interface{}
	if err := json.Unmarshal(b, &updates); err != nil {
		writeJsonWithStatusCode(writer, http.StatusBadRequest, commonResp{
			ErrNo:  http.StatusBadRequest,
			ErrMsg: "无效的JSON格式: " + err.Error(),
		})
		return
	}

	// 记录更新前的 NotifyOnly 状态，用于判断是否需要自动开始录制
	var wasNotifyOnly bool
	inst := instance.GetInstance(r.Context())

	_, err = configs.UpdateWithRetry(func(c *configs.Config) error {
		// 查找直播间（先按 LiveId，回退按 URL 计算的 hash）
		roomIdx := -1
		for i, room := range c.LiveRooms {
			if string(room.LiveId) == liveId {
				roomIdx = i
				break
			}
		}
		// LiveId 可能未初始化（yaml:"-"），回退按 URL 计算 hash 匹配
		if roomIdx == -1 {
			for i, room := range c.LiveRooms {
				if parsedUrl, err := url.Parse(room.Url); err == nil {
					computedId := string(types.LiveID(utils.GetMd5String([]byte(parsedUrl.Host + parsedUrl.Path))))
					if computedId == liveId {
						roomIdx = i
						break
					}
				}
			}
		}

		if roomIdx == -1 {
			return fmt.Errorf("未找到直播间: %s", liveId)
		}

		room := &c.LiveRooms[roomIdx]

		// 保存更新前的 NotifyOnly 状态
		wasNotifyOnly = room.NotifyOnly

		// 更新直播间特有字段
		if url, ok := updates["url"].(string); ok {
			room.Url = url
		}
		if isListening, ok := updates["is_listening"].(bool); ok {
			room.IsListening = isListening
		}
		if quality, ok := updates["quality"].(float64); ok {
			room.Quality = int(quality)
		}
		if audioOnly, ok := updates["audio_only"].(bool); ok {
			room.AudioOnly = audioOnly
		}
		if notifyOnly, ok := updates["notify_only"].(bool); ok {
			room.NotifyOnly = notifyOnly
		}
		if nickName, ok := updates["nick_name"].(string); ok {
			room.NickName = nickName
		}

		// 更新可覆盖配置
		applyOverridableConfigUpdates(&room.OverridableConfig, updates)

		// 验证弹幕配置有效性（同时根据平台清理不适用的字段）
		if room.Danmaku != nil {
			platformKey := configs.GetPlatformKeyFromUrl(room.Url)
			if err := room.Danmaku.ValidateWithPlatform(platformKey); err != nil {
				return fmt.Errorf("弹幕参数无效: %w", err)
			}
		}

		return nil
	}, 3, 10*time.Millisecond)

	if err != nil {
		writeJsonWithStatusCode(writer, http.StatusInternalServerError, commonResp{
			ErrNo:  http.StatusInternalServerError,
			ErrMsg: "更新直播间配置失败: " + err.Error(),
		})
		return
	}

	// 如果从 NotifyOnly 切换为普通模式，检查是否正在直播并自动开始录制
	if notifyOnly, ok := updates["notify_only"].(bool); ok {
		if wasNotifyOnly && !notifyOnly {
			// 检查是否正在直播（通过缓存）
			liveObj, ok := inst.Lives.Get(types.LiveID(liveId))
			if ok {
				if obj, err := inst.Cache.Get(liveObj); err == nil && obj != nil {
					liveInfo := obj.(*live.Info)
					if liveInfo.Status {
						// 正在直播，检查是否已经在录制
						recorderMgr, ok := inst.RecorderManager.(recorders.Manager)
						if ok && !recorderMgr.HasRecorder(inst.Ctx, liveObj.GetLiveId()) {
							// 未在录制，自动开始录制
							if err := recorderMgr.AddRecorder(inst.Ctx, liveObj); err != nil {
								liveObj.GetLogger().Errorf("自动开始录制失败: %v", err)
							} else {
								liveObj.GetLogger().Info("从仅提醒模式切换为普通模式，自动开始录制")
								// 广播录制开始事件
								GetSSEHub().BroadcastListChange(liveObj.GetLiveId(), "record_start", map[string]interface{}{
									"live_id": string(liveObj.GetLiveId()),
								})
							}
						}
					}
				}
			}
		}
	}

	writeJSON(writer, commonResp{
		Data: "OK",
	})
}

// applyOverridableConfigUpdates 统一处理可覆盖配置的更新
func applyOverridableConfigUpdates(oc *configs.OverridableConfig, updates map[string]interface{}) {
	if interval, ok := updates["interval"].(float64); ok {
		val := int(interval)
		oc.Interval = &val
	}
	if outPutPath, ok := updates["out_put_path"].(string); ok {
		if outPutPath == "" {
			oc.OutPutPath = nil
		} else {
			oc.OutPutPath = &outPutPath
		}
	}
	if ffmpegPath, ok := updates["ffmpeg_path"].(string); ok {
		if ffmpegPath == "" {
			oc.FfmpegPath = nil
		} else {
			oc.FfmpegPath = &ffmpegPath
		}
	}
	if outPutTmpl, ok := updates["out_put_tmpl"].(string); ok {
		if outPutTmpl == "" {
			oc.OutputTmpl = nil
		} else {
			oc.OutputTmpl = &outPutTmpl
		}
	}
	if timeoutSec, ok := updates["timeout_in_seconds"].(float64); ok {
		val := int(timeoutSec * 1000000)
		oc.TimeoutInUs = &val
	}

	// 处理 feature 配置（包括 downloader_type）
	if feature, ok := updates["feature"].(map[string]interface{}); ok {
		if oc.Feature == nil {
			oc.Feature = &configs.Feature{}
		}
		if downloaderType, ok := feature["downloader_type"].(string); ok {
			oc.Feature.DownloaderType = configs.ParseDownloaderType(downloaderType)
		}
		if useNativeFlvParser, ok := feature["use_native_flv_parser"].(bool); ok {
			oc.Feature.UseNativeFlvParser = useNativeFlvParser
		}
		if enableFlvProxySegment, ok := feature["enable_flv_proxy_segment"].(bool); ok {
			oc.Feature.EnableFlvProxySegment = enableFlvProxySegment
		}
	}

	// 也支持直接在顶层设置 downloader_type（简化前端逻辑）
	if downloaderType, ok := updates["downloader_type"].(string); ok {
		if oc.Feature == nil {
			oc.Feature = &configs.Feature{}
		}
		oc.Feature.DownloaderType = configs.ParseDownloaderType(downloaderType)
	}

	// 处理 stream_preference 配置
	if streamPref, ok := updates["stream_preference"].(map[string]interface{}); ok {
		if oc.StreamPreference == nil {
			oc.StreamPreference = &configs.StreamPreference{}
		}

		// 处理 quality
		if quality, ok := streamPref["quality"].(string); ok {
			if quality == "" {
				oc.StreamPreference.Quality = nil
			} else {
				oc.StreamPreference.Quality = &quality
			}
		}

		// 处理 attributes
		if attrs, ok := streamPref["attributes"].(map[string]interface{}); ok {
			if len(attrs) == 0 {
				oc.StreamPreference.Attributes = nil
			} else {
				attrMap := make(map[string]string)
				for k, v := range attrs {
					if strVal, ok := v.(string); ok {
						attrMap[k] = strVal
					}
				}
				if len(attrMap) > 0 {
					oc.StreamPreference.Attributes = &attrMap
				} else {
					oc.StreamPreference.Attributes = nil
				}
			}
		}

		// 如果 quality 和 attributes 都为空，清空整个 StreamPreference
		if oc.StreamPreference.Quality == nil && oc.StreamPreference.Attributes == nil {
			oc.StreamPreference = nil
		}
	}

	// 处理 danmaku_enable 配置
	if danmakuEnable, ok := updates["danmaku_enable"].(bool); ok {
		oc.DanmakuEnable = &danmakuEnable
	} else if _, exists := updates["danmaku_enable"]; exists && updates["danmaku_enable"] == nil {
		// 显式 null → 清除覆盖，恢复继承
		oc.DanmakuEnable = nil
	}

	// 处理 danmaku 弹幕参数配置
	if danmaku, ok := updates["danmaku"].(map[string]interface{}); ok {
		if oc.Danmaku == nil {
			// 从全局默认值初始化，避免未设置的字段被覆盖为零值
			defaultCfg := configs.GetDefaultDanmakuConfig()
			oc.Danmaku = &defaultCfg
		}
		if fontSize, ok := danmaku["font_size"].(float64); ok {
			oc.Danmaku.FontSize = int(fontSize)
		}
		if fontName, ok := danmaku["font_name"].(string); ok {
			oc.Danmaku.FontName = fontName
		}
		if scrollArea, ok := danmaku["scroll_area"].(string); ok {
			oc.Danmaku.ScrollArea = scrollArea
		}
		if scrollTime, ok := danmaku["scroll_time"].(float64); ok {
			oc.Danmaku.ScrollTime = int(scrollTime)
		}
		if resolution, ok := danmaku["resolution"].(string); ok {
			oc.Danmaku.Resolution = resolution
		}
		if outline, ok := danmaku["outline"].(float64); ok {
			oc.Danmaku.Outline = configs.IntPtr(int(outline))
		}
		if opacity, ok := danmaku["opacity"].(float64); ok {
			oc.Danmaku.Opacity = configs.IntPtr(int(opacity))
		}
		if recordGift, ok := danmaku["record_gift"].(bool); ok {
			oc.Danmaku.RecordGift = configs.BoolPtr(recordGift)
		} else if _, exists := danmaku["record_gift"]; exists && danmaku["record_gift"] == nil {
			oc.Danmaku.RecordGift = nil
		}
		if recordDouyuGift, ok := danmaku["record_douyu_gift"].(bool); ok {
			oc.Danmaku.RecordDouyuGift = configs.BoolPtr(recordDouyuGift)
		} else if _, exists := danmaku["record_douyu_gift"]; exists && danmaku["record_douyu_gift"] == nil {
			oc.Danmaku.RecordDouyuGift = nil
		}
		if recordDouyinGift, ok := danmaku["record_douyin_gift"].(bool); ok {
			oc.Danmaku.RecordDouyinGift = configs.BoolPtr(recordDouyinGift)
		} else if _, exists := danmaku["record_douyin_gift"]; exists && danmaku["record_douyin_gift"] == nil {
			oc.Danmaku.RecordDouyinGift = nil
		}
		if recordGuard, ok := danmaku["record_guard"].(bool); ok {
			oc.Danmaku.RecordGuard = configs.BoolPtr(recordGuard)
		} else if _, exists := danmaku["record_guard"]; exists && danmaku["record_guard"] == nil {
			oc.Danmaku.RecordGuard = nil
		}
		if recordSuperChat, ok := danmaku["record_super_chat"].(bool); ok {
			oc.Danmaku.RecordSuperChat = configs.BoolPtr(recordSuperChat)
		} else if _, exists := danmaku["record_super_chat"]; exists && danmaku["record_super_chat"] == nil {
			oc.Danmaku.RecordSuperChat = nil
		}
		if showUsername, ok := danmaku["show_username"].(bool); ok {
			oc.Danmaku.ShowUsername = configs.BoolPtr(showUsername)
		} else if _, exists := danmaku["show_username"]; exists && danmaku["show_username"] == nil {
			oc.Danmaku.ShowUsername = nil
		}
		if guardPosition, ok := danmaku["guard_position"].(string); ok {
			oc.Danmaku.GuardPosition = guardPosition
		}
		if scPosition, ok := danmaku["sc_position"].(string); ok {
			oc.Danmaku.ScPosition = scPosition
		}
	} else if _, exists := updates["danmaku"]; exists && updates["danmaku"] == nil {
		// 显式 null → 清除覆盖，恢复继承
		oc.Danmaku = nil
	}
}

// updateRoomConfig 更新直播间配置
func updateRoomConfig(writer http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	roomUrl := vars["url"]

	// URL 解码
	decodedUrl, err := url.QueryUnescape(roomUrl)
	if err != nil {
		writeJsonWithStatusCode(writer, http.StatusBadRequest, commonResp{
			ErrNo:  http.StatusBadRequest,
			ErrMsg: "无效的URL: " + err.Error(),
		})
		return
	}

	b, err := io.ReadAll(r.Body)
	if err != nil {
		writeJsonWithStatusCode(writer, http.StatusBadRequest, commonResp{
			ErrNo:  http.StatusBadRequest,
			ErrMsg: err.Error(),
		})
		return
	}

	var updates map[string]interface{}
	if err := json.Unmarshal(b, &updates); err != nil {
		writeJsonWithStatusCode(writer, http.StatusBadRequest, commonResp{
			ErrNo:  http.StatusBadRequest,
			ErrMsg: "无效的JSON格式: " + err.Error(),
		})
		return
	}

	_, err = configs.UpdateWithRetry(func(c *configs.Config) error {
		room, err := c.GetLiveRoomByUrl(decodedUrl)
		if err != nil {
			return errors.New("找不到直播间: " + decodedUrl)
		}

		// 更新直播间配置
		if quality, ok := updates["quality"].(float64); ok {
			room.Quality = int(quality)
		}
		if audioOnly, ok := updates["audio_only"].(bool); ok {
			room.AudioOnly = audioOnly
		}
		if notifyOnly, ok := updates["notify_only"].(bool); ok {
			room.NotifyOnly = notifyOnly
		}
		if nickName, ok := updates["nick_name"].(string); ok {
			room.NickName = nickName
		}

		// 处理可覆盖配置（弹幕、interval、outPutPath、ffmpegPath 等）
		applyOverridableConfigUpdates(&room.OverridableConfig, updates)

		// 验证弹幕配置（同时根据平台清理不适用的字段）
		if room.Danmaku != nil {
			platformKey := configs.GetPlatformKeyFromUrl(decodedUrl)
			if err := room.Danmaku.ValidateWithPlatform(platformKey); err != nil {
				return fmt.Errorf("弹幕配置无效: %w", err)
			}
		}

		return nil
	}, 3, 10*time.Millisecond)

	if err != nil {
		writeJsonWithStatusCode(writer, http.StatusInternalServerError, commonResp{
			ErrNo:  http.StatusInternalServerError,
			ErrMsg: "更新直播间配置失败: " + err.Error(),
		})
		return
	}

	writeJSON(writer, commonResp{
		Data: "OK",
	})
}

// getSafePath 验证并生成安全的绝对路径，防止路径遍历（Path Traversal）攻击。
// base: 授权访问的基础根目录。
// subPath: 待访问的相对子路径。
// 该函数会通过计算绝对路径并检查相对关系，确保最终路径不会逃逸出基础根目录。
func getSafePath(base, subPath string) (string, error) {
	absBase, err := filepath.Abs(base)
	if err != nil {
		return "", err
	}
	absTarget, err := filepath.Abs(filepath.Join(absBase, subPath))
	if err != nil {
		return "", err
	}

	rel, err := filepath.Rel(absBase, absTarget)
	if err != nil {
		return "", err
	}

	// 核心安全逻辑：如果计算出的相对路径以 ".." 开头，说明它逃逸到了 base 目录之外
	if strings.HasPrefix(rel, "..") || filepath.IsAbs(rel) {
		return "", errors.New("非法路径访问：超出授权范围")
	}

	return absTarget, nil
}

func getFileInfo(writer http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	path := vars["path"]

	cfg := configs.GetCurrentConfig()
	absPath, err := getSafePath(cfg.OutPutPath, path)
	if err != nil {
		writeJSON(writer, commonResp{
			ErrMsg: "无效或越权路径",
		})
		return
	}

	files, err := os.ReadDir(absPath)
	if err != nil {
		writeJSON(writer, commonResp{
			ErrMsg: "获取目录失败",
		})
		return
	}

	type jsonFile struct {
		IsFolder     bool   `json:"is_folder"`
		Name         string `json:"name"`
		LastModified int64  `json:"last_modified"`
		Size         int64  `json:"size"`
		SubtitleFile string `json:"subtitle_file,omitempty"`
		Uploaded     bool   `json:"uploaded,omitempty"`
	}

	// First pass: separate ASS files and build base-name -> ASS file map
	assFiles := make(map[string]string) // baseName (no ext) -> ass filename
	type fileEntry struct {
		dir  os.DirEntry
		info os.FileInfo
	}
	var validFiles []fileEntry
	for _, file := range files {
		info, err := file.Info()
		if err != nil {
			continue
		}
		name := file.Name()
		if !file.IsDir() && strings.HasSuffix(strings.ToLower(name), ".ass") {
			// Register ASS file by base name (without extension)
			baseName := name[:len(name)-4]
			assFiles[baseName] = name
		} else {
			validFiles = append(validFiles, fileEntry{dir: file, info: info})
		}
	}

	// Second pass: build response, attaching subtitle info to video files
	jsonFiles := make([]jsonFile, 0, len(validFiles))
	for _, fe := range validFiles {
		jf := jsonFile{
			IsFolder:     fe.dir.IsDir(),
			Name:         fe.dir.Name(),
			LastModified: fe.info.ModTime().Unix(),
		}
		if !fe.dir.IsDir() {
			jf.Size = fe.info.Size()
			// Check if this file has an associated ASS subtitle
			baseName := fe.dir.Name()
			if idx := strings.LastIndex(baseName, "."); idx > 0 {
				baseName = baseName[:idx]
			}
			if assName, ok := assFiles[baseName]; ok {
				jf.SubtitleFile = assName
			}
			// Check if this file has been uploaded to cloud
			relPath := fe.dir.Name()
			if path != "" {
				relPath = path + "/" + fe.dir.Name() // key 统一用正斜杠
			}
			if val, err := metadata.GetStore().Get(r.Context(), metadata.NamespaceUploaded, relPath); err == nil && val != "" {
				jf.Uploaded = true
			}
		}
		jsonFiles = append(jsonFiles, jf)
	}

	json := struct {
		Files []jsonFile `json:"files"`
		Path  string     `json:"path"`
	}{
		Files: jsonFiles,
		Path:  path,
	}

	writeJSON(writer, json)
}

// translateOSError 将系统错误转换为中文，兼容多平台。
func translateOSError(err error) string {
	if err == nil {
		return ""
	}

	// 1. 优先使用标准库提供的语义化判断（能应对各种操作系统语言）
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return "操作失败：文件或文件夹不存在"
	case errors.Is(err, fs.ErrExist):
		return "操作失败：目标文件名已存在"
	case errors.Is(err, fs.ErrPermission):
		return "操作被拒绝：权限不足或文件/文件夹正被占用"
	}

	// 2. 对于无法通过标准库语义识别、且具有平台特性的错误，保留并优化字符串匹配
	errStr := err.Error()
	loweredErr := strings.ToLower(errStr)
	switch {
	// 特别处理 Windows 常见的文件占用/共享冲突
	case strings.Contains(loweredErr, "being used by another process"),
		strings.Contains(loweredErr, "sharing violation"):
		return "操作失败：文件正被另一个程序占用"

	// 处理删除非空目录
	case strings.Contains(loweredErr, "is not empty"),
		strings.Contains(loweredErr, "directory not empty"):
		return "操作失败：目录不为空"

	default:
		// 最后的兜底，返回原始错误
		return errStr
	}
}

func renameFile(writer http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	path := vars["path"]

	var body struct {
		NewName string `json:"new_name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(writer, commonResp{ErrNo: 400, ErrMsg: "无效请求"})
		return
	}

	cfg := configs.GetCurrentConfig()
	oldAbsPath, err := getSafePath(cfg.OutPutPath, path)
	if err != nil {
		writeJSON(writer, commonResp{ErrNo: 400, ErrMsg: "无效或越权路径"})
		return
	}

	info, err := os.Stat(oldAbsPath)
	if err != nil {
		writeJSON(writer, commonResp{ErrNo: 404, ErrMsg: "文件不存在"})
		return
	}

	var newAbsPath string
	baseDir := filepath.Dir(oldAbsPath)
	if info.IsDir() {
		newAbsPath = filepath.Join(baseDir, body.NewName)
	} else {
		ext := filepath.Ext(oldAbsPath)
		newAbsPath = filepath.Join(baseDir, body.NewName+ext)
	}

	// 重点：必须再次校验新路径是否安全，防止 body.NewName 包含 ../ 等逃逸字符
	base, err := filepath.Abs(cfg.OutPutPath)
	if err != nil {
		writeJSON(writer, commonResp{ErrNo: 500, ErrMsg: "获取根目录绝对路径失败: " + err.Error()})
		return
	}
	rel, err := filepath.Rel(base, newAbsPath)
	if err != nil || strings.HasPrefix(rel, "..") || filepath.IsAbs(rel) {
		writeJSON(writer, commonResp{ErrNo: 400, ErrMsg: "非法的新文件名：禁止越界路径"})
		return
	}

	// 检查目标文件名是否已存在
	if _, err := os.Stat(newAbsPath); err == nil {
		writeJSON(writer, commonResp{ErrNo: 400, ErrMsg: "重命名失败：目标文件名已存在"})
		return
	}

	if err := os.Rename(oldAbsPath, newAbsPath); err != nil {
		writeJSON(writer, commonResp{ErrNo: 500, ErrMsg: "重命名失败: " + translateOSError(err)})
		return
	}

	// 同步重命名关联的 ASS 弹幕文件
	if !info.IsDir() {
		oldBase := strings.TrimSuffix(oldAbsPath, filepath.Ext(oldAbsPath))
		newBase := strings.TrimSuffix(newAbsPath, filepath.Ext(newAbsPath))
		oldAss := oldBase + ".ass"
		newAss := newBase + ".ass"
		if _, err := os.Stat(oldAss); err == nil {
			os.Rename(oldAss, newAss)
		}
	}

	// 迁移上传标记（key 统一用正斜杠）
	oldRel := path
	newRelPath, _ := filepath.Rel(base, newAbsPath)
	newRel := filepath.ToSlash(newRelPath)
	if info.IsDir() {
		// 目录重命名：迁移该目录下所有文件的上传标记
		allMarks, _ := metadata.GetStore().GetAll(r.Context(), metadata.NamespaceUploaded)
		oldPrefix := oldRel + "/"
		for key, val := range allMarks {
			if strings.HasPrefix(key, oldPrefix) {
				suffix := key[len(oldPrefix):]
				newKey := newRel + "/" + suffix
				metadata.GetStore().Set(r.Context(), metadata.NamespaceUploaded, newKey, val)
				metadata.GetStore().Delete(r.Context(), metadata.NamespaceUploaded, key)
			}
		}
	} else {
		// 文件重命名：迁移单个上传标记
		if val, err := metadata.GetStore().Get(r.Context(), metadata.NamespaceUploaded, oldRel); err == nil {
			metadata.GetStore().Set(r.Context(), metadata.NamespaceUploaded, newRel, val)
			metadata.GetStore().Delete(r.Context(), metadata.NamespaceUploaded, oldRel)
		}
	}

	writeJSON(writer, commonResp{Data: "OK"})
}

func deleteFile(writer http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	path := vars["path"]

	cfg := configs.GetCurrentConfig()
	base, err := filepath.Abs(cfg.OutPutPath)
	if err != nil {
		writeJSON(writer, commonResp{ErrNo: 500, ErrMsg: "获取根目录绝对路径失败: " + err.Error()})
		return
	}
	absPath, err := getSafePath(cfg.OutPutPath, path)
	if err != nil || absPath == base {
		writeJSON(writer, commonResp{ErrNo: 400, ErrMsg: "禁止删除根目录或无效/越权路径"})
		return
	}

	// 记录是否为目录（删除前检查）
	isDir := false
	if info, err := os.Stat(absPath); err == nil {
		isDir = info.IsDir()
	}

	// 删除关联的 ASS 弹幕文件
	if !isDir {
		assPath := strings.TrimSuffix(absPath, filepath.Ext(absPath)) + ".ass"
		if _, err := os.Stat(assPath); err == nil {
			os.Remove(assPath)
		}
	}

	if err := os.RemoveAll(absPath); err != nil {
		writeJSON(writer, commonResp{ErrNo: 500, ErrMsg: "删除失败: " + translateOSError(err)})
		return
	}

	// 清除上传标记
	if isDir {
		// 目录：清除该目录下所有文件的上传标记
		allMarks, _ := metadata.GetStore().GetAll(r.Context(), metadata.NamespaceUploaded)
		prefix := path + "/" // key 统一用正斜杠
		for key := range allMarks {
			if strings.HasPrefix(key, prefix) {
				metadata.GetStore().Delete(r.Context(), metadata.NamespaceUploaded, key)
			}
		}
	} else {
		metadata.GetStore().Delete(r.Context(), metadata.NamespaceUploaded, path)
	}

	writeJSON(writer, commonResp{Data: "OK"})
}

func batchRenameFiles(writer http.ResponseWriter, r *http.Request) {
	var body struct {
		Paths   []string `json:"paths"`
		Find    string   `json:"find"`
		Replace string   `json:"replace"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(writer, commonResp{ErrNo: 400, ErrMsg: "无效请求"})
		return
	}

	cfg := configs.GetCurrentConfig()
	base, err := filepath.Abs(cfg.OutPutPath)
	if err != nil {
		writeJSON(writer, commonResp{ErrNo: 500, ErrMsg: "获取根目录绝对路径失败: " + err.Error()})
		return
	}

	type Result struct {
		Path    string `json:"path"`
		Success bool   `json:"success"`
		Message string `json:"message"`
	}
	results := make([]Result, 0, len(body.Paths))

	for _, path := range body.Paths {
		oldAbsPath, err := getSafePath(cfg.OutPutPath, path)
		if err != nil {
			results = append(results, Result{Path: path, Success: false, Message: "无效或越权路径"})
			continue
		}

		info, err := os.Stat(oldAbsPath)
		if err != nil {
			results = append(results, Result{Path: path, Success: false, Message: "文件不存在"})
			continue
		}

		oldName := filepath.Base(oldAbsPath)
		var newName string
		if info.IsDir() {
			newName = strings.ReplaceAll(oldName, body.Find, body.Replace)
		} else {
			ext := filepath.Ext(oldName)
			nameWithoutExt := strings.TrimSuffix(oldName, ext)
			newNameWithoutExt := strings.ReplaceAll(nameWithoutExt, body.Find, body.Replace)
			newName = newNameWithoutExt + ext
		}

		if oldName == newName {
			results = append(results, Result{Path: path, Success: true, Message: "无需更改"})
			continue
		}

		newAbsPath := filepath.Join(filepath.Dir(oldAbsPath), newName)

		// 二次校验新路径
		rel, err := filepath.Rel(base, newAbsPath)
		if err != nil || strings.HasPrefix(rel, "..") || filepath.IsAbs(rel) {
			results = append(results, Result{Path: path, Success: false, Message: "目标名越界"})
			continue
		}

		// 检查目标文件名是否已存在
		if _, err := os.Stat(newAbsPath); err == nil {
			results = append(results, Result{Path: path, Success: false, Message: "目标已存在"})
			continue
		}

		if err := os.Rename(oldAbsPath, newAbsPath); err != nil {
			results = append(results, Result{Path: path, Success: false, Message: translateOSError(err)})
		} else {
			results = append(results, Result{Path: path, Success: true, Message: "成功"})
			// 同步重命名关联的 ASS 弹幕文件
			if !info.IsDir() {
				oldBase := strings.TrimSuffix(oldAbsPath, filepath.Ext(oldAbsPath))
				newBase := strings.TrimSuffix(newAbsPath, filepath.Ext(newAbsPath))
				oldAss := oldBase + ".ass"
				newAss := newBase + ".ass"
				if _, err := os.Stat(oldAss); err == nil {
					os.Rename(oldAss, newAss)
				}
			}

			// 迁移上传标记（key 统一用正斜杠）
			oldRel := path
			newRelPath, _ := filepath.Rel(base, newAbsPath)
			newRel := filepath.ToSlash(newRelPath)
			if info.IsDir() {
				allMarks, _ := metadata.GetStore().GetAll(r.Context(), metadata.NamespaceUploaded)
				oldPrefix := oldRel + "/"
				for key, val := range allMarks {
					if strings.HasPrefix(key, oldPrefix) {
						suffix := key[len(oldPrefix):]
						newKey := newRel + "/" + suffix
						metadata.GetStore().Set(r.Context(), metadata.NamespaceUploaded, newKey, val)
						metadata.GetStore().Delete(r.Context(), metadata.NamespaceUploaded, key)
					}
				}
			} else {
				if val, err := metadata.GetStore().Get(r.Context(), metadata.NamespaceUploaded, oldRel); err == nil {
					metadata.GetStore().Set(r.Context(), metadata.NamespaceUploaded, newRel, val)
					metadata.GetStore().Delete(r.Context(), metadata.NamespaceUploaded, oldRel)
				}
			}
		}
	}

	writeJSON(writer, commonResp{Data: results})
}

func batchDeleteFiles(writer http.ResponseWriter, r *http.Request) {
	var body struct {
		Paths []string `json:"paths"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(writer, commonResp{ErrNo: 400, ErrMsg: "无效请求"})
		return
	}

	cfg := configs.GetCurrentConfig()
	base, err := filepath.Abs(cfg.OutPutPath)
	if err != nil {
		writeJSON(writer, commonResp{ErrNo: 500, ErrMsg: "获取根目录绝对路径失败: " + err.Error()})
		return
	}

	type Result struct {
		Path    string `json:"path"`
		Success bool   `json:"success"`
		Message string `json:"message"`
	}
	results := make([]Result, 0, len(body.Paths))

	for _, path := range body.Paths {
		absPath, err := getSafePath(cfg.OutPutPath, path)
		if err != nil || absPath == base {
			results = append(results, Result{Path: path, Success: false, Message: "禁止操作根目录或越权路径"})
			continue
		}

		// 记录是否为目录（删除前检查）
		isDir := false
		if info, err := os.Stat(absPath); err == nil {
			isDir = info.IsDir()
		}

		// 删除关联的 ASS 弹幕文件
		if !isDir {
			assPath := strings.TrimSuffix(absPath, filepath.Ext(absPath)) + ".ass"
			if _, err := os.Stat(assPath); err == nil {
				os.Remove(assPath)
			}
		}

		if err := os.RemoveAll(absPath); err != nil {
			results = append(results, Result{Path: path, Success: false, Message: translateOSError(err)})
		} else {
			// 清除上传标记
			if isDir {
				allMarks, _ := metadata.GetStore().GetAll(r.Context(), metadata.NamespaceUploaded)
				prefix := path + "/" // key 统一用正斜杠
				for key := range allMarks {
					if strings.HasPrefix(key, prefix) {
						metadata.GetStore().Delete(r.Context(), metadata.NamespaceUploaded, key)
					}
				}
			} else {
				metadata.GetStore().Delete(r.Context(), metadata.NamespaceUploaded, path)
			}
			results = append(results, Result{Path: path, Success: true, Message: "成功"})
		}
	}

	writeJSON(writer, commonResp{Data: results})
}

func getLiveHostCookie(writer http.ResponseWriter, r *http.Request) {
	inst := instance.GetInstance(r.Context())
	hostCookieMap := make(map[string]*live.InfoCookie)
	keys := make([]string, 0)
	// 配置在启动早期可能尚未就绪，取一次快照统一使用，避免逐房间重复解引用
	allCookies := map[string]string{}
	if cfg := configs.GetCurrentConfig(); cfg != nil {
		allCookies = cfg.Cookies
	}
	inst.Lives.Range(func(_ types.LiveID, v live.Live) bool {
		urltmp, _ := url.Parse(v.GetRawUrl())
		if _, ok := hostCookieMap[urltmp.Host]; ok {
			return true
		}
		host := urltmp.Host
		platformName := v.GetPlatformCNName()
		if cookie, ok := allCookies[host]; ok {
			tmp := &live.InfoCookie{Platform_cn_name: platformName, Host: host, Cookie: cookie}
			hostCookieMap[host] = tmp
		} else {
			tmp := &live.InfoCookie{Platform_cn_name: platformName, Host: host}
			hostCookieMap[host] = tmp
		}
		keys = append(keys, host)
		return true
	})
	// 斗鱼扫码入口兜底：列表行只由"运行中的房间 host"推导，用户删掉全部斗鱼房间后
	// 既看不到自动续期失败提醒、也无法重新扫码。只要后端仍持有斗鱼凭证就固定补一行。
	if cfg := configs.GetCurrentConfig(); cfg != nil {
		if _, ok := hostCookieMap[dy.CookieHost]; !ok && (cfg.DouyuAuth.LTP0 != "" || cfg.Cookies[dy.CookieHost] != "") {
			hostCookieMap[dy.CookieHost] = &live.InfoCookie{
				Platform_cn_name: "斗鱼",
				Host:             dy.CookieHost,
				Cookie:           cfg.Cookies[dy.CookieHost],
			}
			keys = append(keys, dy.CookieHost)
		}
	}
	sort.Strings(keys)
	result := make([]*live.InfoCookie, 0)
	for _, v := range keys {
		result = append(result, hostCookieMap[v])
	}
	writeJSON(writer, result)
}

// applyCookiesToLives 把 newCfg 中属于 hosts 的房间重新套用 cookie。
// 注意：newCfg 只用于"枚举哪些房间要刷新"，实际生效的 cookie 由
// UpdateLiveOptionsbyConfig 从全局当前配置现读；因此调用方必须在
// 配置更新（CAS 提交）成功之后调用，否则刷到的可能是并发写入的更新值。
func applyCookiesToLives(ctx context.Context, newCfg *configs.Config, hosts ...string) {
	inst := instance.GetInstance(ctx)
	hostSet := make(map[string]struct{}, len(hosts))
	for _, host := range hosts {
		hostSet[host] = struct{}{}
	}

	for _, room := range newCfg.LiveRooms {
		tmpurl, err := url.Parse(room.Url)
		if err != nil {
			continue
		}
		if _, ok := hostSet[tmpurl.Host]; !ok {
			continue
		}

		liveObj, _ := inst.Lives.Get(room.LiveId)
		if liveObj == nil {
			inst.Lives.Range(func(_ types.LiveID, l live.Live) bool {
				if l.GetRawUrl() == room.Url {
					liveObj = l
					return false
				}
				return true
			})
		}
		if liveObj == nil {
			applog.GetLogger().Warn("can't find live by id or url: " + string(room.LiveId) + " " + room.Url)
			continue
		}
		liveObj.UpdateLiveOptionsbyConfig(ctx, &room)
	}
}

func putLiveHostCookie(writer http.ResponseWriter, r *http.Request) {
	b, err := io.ReadAll(r.Body)
	if err != nil {
		writeJsonWithStatusCode(writer, http.StatusBadRequest, commonResp{
			ErrNo:  http.StatusBadRequest,
			ErrMsg: err.Error(),
		})
		return
	}
	ctx := r.Context()
	data := gjson.ParseBytes(b)

	// host 先归一为标准域名：房间 URL 与 Cookies 键都用标准 host，别名提交若不归一，
	// 斗鱼分支（清长期凭证）和热应用（按 host 匹配房间）都会静默不命中。
	host := configs.NormalizeCookieHost(data.Get("Host").Str)
	cookie := data.Get("Cookie").Str
	if cookie == "" {

	} else {
		reg, _ := regexp.Compile(".*=.*")
		if !reg.MatchString(cookie) {
			writeJsonWithStatusCode(writer, http.StatusBadRequest, commonResp{
				ErrNo:  http.StatusBadRequest,
				ErrMsg: "cookie格式错误",
			})
			return
		}
	}
	// 使用统一 Update 接口更新 Cookies
	// 斗鱼特殊处理：手动改写/清空 cookie 视为用户显式接管登录态，必须同时清除长期凭证 LTP0 与续期日程，
	// 否则后台 keeper 会在下次续期时用旧 LTP0 换票，把用户刚写入（或删除）的 cookie 覆盖回去（幽灵恢复/静默换账号）。
	var newCfg *configs.Config
	if host == dy.CookieHost {
		newCfg, err = configs.UpdateWithRetry(func(c *configs.Config) error {
			// 浏览器导出的 www.douyu.com cookie 常连带 passport 域的 LTP0（domain=.douyu.com）。
			// 长期凭证只应留在 DouyuAuth.LTP0 一处：混进 Cookies 后 /api/cookies 会把它明文回传浏览器，
			// 而且请求主站本来也不需要这张票。
			stored := configs.DropCookieFields(strings.TrimSpace(cookie), "LTP0")
			oldUid := douyuCookieUid(c.Cookies[host])
			if stored == "" {
				delete(c.Cookies, host)
			} else {
				if c.Cookies == nil {
					c.Cookies = make(map[string]string)
				}
				c.Cookies[host] = stored
			}
			// 只在"换账号"或"显式存一份未登录 cookie"时作废票据，而不是只要动过 cookie 就清：
			// 同账号补/删非登录字段很常见，顺手清掉票据会让自动续期从此静默失效，
			// 用户只看到"过几天又断流"。反之新的 cookie 里没有 acf_uid，说明用户在主动退出登录，
			// 留着票据就会被后台换票恢复成登录态。
			newUid := douyuCookieUid(stored)
			if newUid != oldUid || newUid == "" {
				if c.DouyuAuth.LTP0 != "" {
					applog.GetLogger().Warnf("斗鱼登录 Cookie 被手工替换为另一份登录态（acf_uid %s -> %s），已清除长期凭证（需重新扫码才能启用自动续期）", oldUid, newUid)
				}
				c.DouyuAuth = configs.DouyuAuth{}
			}
			return nil
		}, 3, 10*time.Millisecond)
	} else {
		newCfg, err = configs.SetCookie(host, cookie)
	}
	if err != nil {
		writeJsonWithStatusCode(writer, http.StatusInternalServerError, commonResp{
			ErrNo:  http.StatusInternalServerError,
			ErrMsg: err.Error(),
		})
		return
	}
	applyCookiesToLives(ctx, newCfg, host)
	// 不再额外 Marshal：上面的 UpdateWithRetry/SetCookie 已在 updateMu 锁内落过盘。
	// 在锁外重写同一个快照，会把期间后台续期刚写好的 DouyuAuth/新 cookie 盖回旧值（盘上比内存旧，重启才显现）。
	writeJSON(writer, commonResp{
		Data: "OK",
	})
}

// getSoopLiveAuthConfig 返回 Soop 凭证状态和当前已保存的账号字段（仅用户名与是否存在已保存凭证标记），供 WebUI 面板初始化使用。
// 注意：当前实现不会返回密码等敏感字段，只会返回 username 与 has_saved_credentials，避免将明文密码暴露给前端。
func getSoopLiveAuthConfig(writer http.ResponseWriter, _ *http.Request) {
	cfg := configs.GetCurrentConfig()
	if cfg == nil {
		writeJsonWithStatusCode(writer, http.StatusInternalServerError, commonResp{
			ErrNo:  http.StatusInternalServerError,
			ErrMsg: "配置未加载",
		})
		return
	}

	var verifyResult *soop.CookieVerifyResult
	verifyError := ""
	cookieStatus := "missing"
	storedCookie := ""
	if cfg.Cookies != nil {
		if cookie := strings.TrimSpace(cfg.Cookies["play.sooplive.com"]); cookie != "" {
			storedCookie = cookie
		}
	}
	if storedCookie != "" {
		if result, err := soop.VerifyCookieStringCached(storedCookie); err == nil {
			verifyResult = result
			if result != nil && result.IsLogin {
				cookieStatus = "valid"
			} else {
				cookieStatus = "invalid"
				verifyError = "Soop Cookie 校验未通过，当前登录态已失效或权限不足"
			}
		} else {
			cookieStatus = "error"
			verifyError = err.Error()
		}
	}
	applog.GetLogger().Debugf("Soop auth 状态查询: hasCookie=%v cookieStatus=%s hasSavedCredential=%v verifyError=%v",
		storedCookie != "", cookieStatus, cfg.SoopLiveAuth.Username != "" || cfg.SoopLiveAuth.Password != "", verifyError != "")

	writeJSON(writer, commonResp{
		Data: map[string]any{
			"username":              cfg.SoopLiveAuth.Username,
			"has_saved_credentials": cfg.SoopLiveAuth.Username != "" || cfg.SoopLiveAuth.Password != "",
			"cookie_status":         cookieStatus,
			// verify 为空通常表示：
			// 1. 当前没有保存 Cookie；
			// 2. Soop 校验接口请求失败；
			// 3. 平台暂时不可达。
			"verify":       verifyResult,
			"verify_error": verifyError,
		},
	})
}

// clearSoopLiveAuthConfig 清空 Soop 账号密码与持久化 Cookie。
// 这会让后续 Soop 请求退回到“无登录态”模式。
func clearSoopLiveAuthConfig(writer http.ResponseWriter, r *http.Request) {
	applog.GetLogger().Debug("Soop 清空账号密码与 Cookie 请求开始")
	newCfg, err := configs.UpdateWithRetry(func(c *configs.Config) error {
		c.SoopLiveAuth.Username = ""
		c.SoopLiveAuth.Password = ""
		if c.Cookies != nil {
			delete(c.Cookies, "play.sooplive.com")
		}
		return nil
	}, 3, 10*time.Millisecond)
	if err != nil {
		writeJsonWithStatusCode(writer, http.StatusInternalServerError, commonResp{
			ErrNo:  http.StatusInternalServerError,
			ErrMsg: "清空 Soop 凭证失败: " + err.Error(),
		})
		return
	}

	applyCookiesToLives(r.Context(), newCfg, "play.sooplive.com")
	applog.GetLogger().Debug("Soop 清空账号密码与 Cookie 请求完成")
	writeJSON(writer, commonResp{Data: "OK"})
}

// loginSoopLive 接收前端输入的账号密码，调用 Soop 登录接口换取 Cookie。
// 登录成功后：
// 1. 将 Cookie 写入配置文件；
// 2. 按当前前端选择决定是否保存明文账号密码；
// 3. 更新当前运行中 Soop 房间的请求选项。
func loginSoopLive(writer http.ResponseWriter, r *http.Request) {
	var req struct {
		Username        string `json:"username"`
		Password        string `json:"password"`
		SaveCredentials bool   `json:"save_credentials"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJsonWithStatusCode(writer, http.StatusBadRequest, commonResp{ErrNo: http.StatusBadRequest, ErrMsg: "请求体格式错误，无法解析 Soop 登录参数"})
		return
	}
	if strings.TrimSpace(req.Username) == "" || strings.TrimSpace(req.Password) == "" {
		writeJsonWithStatusCode(writer, http.StatusBadRequest, commonResp{ErrNo: http.StatusBadRequest, ErrMsg: "Soop 账号或密码为空，请完整填写后再登录"})
		return
	}
	applog.GetLogger().Debugf("Soop Web 登录开始: username=%s saveCredentials=%v", req.Username, req.SaveCredentials)

	result, err := soop.LoginAndGetCookie(req.Username, req.Password)
	if err != nil {
		applog.GetLogger().WithError(err).Debugf("Soop Web 登录失败: username=%s", req.Username)
		writeJsonWithStatusCode(writer, http.StatusUnauthorized, commonResp{
			ErrNo:  http.StatusUnauthorized,
			ErrMsg: "Soop 登录失败，后端未能完成“账号密码换 Cookie”流程；常见原因包括账号密码错误、平台风控、网络异常或接口已变更: " + err.Error(),
		})
		return
	}

	newCfg, err := configs.UpdateWithRetry(func(c *configs.Config) error {
		if c.Cookies == nil {
			c.Cookies = make(map[string]string)
		}
		c.Cookies["play.sooplive.com"] = result.Cookie
		if req.SaveCredentials {
			c.SoopLiveAuth.Username = req.Username
			c.SoopLiveAuth.Password = req.Password
		} else {
			c.SoopLiveAuth.Username = ""
			c.SoopLiveAuth.Password = ""
		}
		return nil
	}, 3, 10*time.Millisecond)
	if err != nil {
		writeJsonWithStatusCode(writer, http.StatusInternalServerError, commonResp{
			ErrNo:  http.StatusInternalServerError,
			ErrMsg: "Soop 登录成功，但写入配置失败: " + err.Error(),
		})
		return
	}

	applyCookiesToLives(r.Context(), newCfg, "play.sooplive.com")
	applog.GetLogger().Debugf("Soop Web 登录成功: username=%s cookieLength=%d loginID=%s", req.Username, len(result.Cookie), result.Verify.LoginID)

	writeJSON(writer, commonResp{
		Data: map[string]any{
			"cookie":   result.Cookie,
			"verify":   result.Verify,
			"username": result.Username,
		},
	})
}

// verifySoopLiveCookie 验证前端提供的 Soop Cookie 是否有效。
// 常见失败原因：
// - Cookie 已过期；
// - Cookie 不完整；
// - 账号已在平台侧退出登录；
// - Soop 校验接口当前不可用。
func verifySoopLiveCookie(writer http.ResponseWriter, r *http.Request) {
	var req struct {
		Cookie string `json:"cookie"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJsonWithStatusCode(writer, http.StatusBadRequest, commonResp{ErrNo: http.StatusBadRequest, ErrMsg: "请求体格式错误，无法解析 Soop Cookie"})
		return
	}
	if strings.TrimSpace(req.Cookie) == "" {
		writeJsonWithStatusCode(writer, http.StatusBadRequest, commonResp{ErrNo: http.StatusBadRequest, ErrMsg: "Soop Cookie 不能为空"})
		return
	}
	applog.GetLogger().Debugf("Soop Cookie 校验开始: cookieLength=%d", len(req.Cookie))

	result, err := soop.VerifyCookieStringCached(req.Cookie)
	if err != nil {
		applog.GetLogger().WithError(err).Debug("Soop Cookie 校验失败")
		writeJsonWithStatusCode(writer, http.StatusInternalServerError, commonResp{
			ErrNo:  http.StatusInternalServerError,
			ErrMsg: "验证 Soop Cookie 失败；这通常表示校验接口当前不可用、网络异常，或平台返回了当前版本无法识别的响应: " + err.Error(),
		})
		return
	}
	applog.GetLogger().Debugf("Soop Cookie 校验完成: isLogin=%v loginID=%s", result.IsLogin, result.LoginID)

	writeJSON(writer, commonResp{Data: result})
}

// getLiveSessionHistory 获取直播间的会话历史记录
func getLiveSessionHistory(writer http.ResponseWriter, r *http.Request) {
	inst := instance.GetInstance(r.Context())
	vars := mux.Vars(r)
	liveID := vars["id"]

	// 检查直播间是否存在
	if !inst.Lives.Has(types.LiveID(liveID)) {
		writeJsonWithStatusCode(writer, http.StatusNotFound, commonResp{
			ErrNo:  http.StatusNotFound,
			ErrMsg: fmt.Sprintf("live id: %s can not find", liveID),
		})
		return
	}

	// 获取 limit 参数
	limit := 20 // 默认 20 条
	if limitStr := r.URL.Query().Get("limit"); limitStr != "" {
		if parsedLimit, err := strconv.Atoi(limitStr); err == nil && parsedLimit > 0 {
			limit = parsedLimit
		}
	}

	// 获取 LiveStateManager
	manager, ok := inst.LiveStateManager.(*livestate.Manager)
	if !ok || manager == nil {
		writeJsonWithStatusCode(writer, http.StatusServiceUnavailable, commonResp{
			ErrNo:  http.StatusServiceUnavailable,
			ErrMsg: "状态持久化功能未启用",
		})
		return
	}

	// 获取会话历史
	sessions := manager.GetSessionHistory(liveID, limit)

	writeJSON(writer, map[string]interface{}{
		"live_id":  liveID,
		"sessions": sessions,
		"total":    len(sessions),
	})
}

// getLiveNameHistory 获取直播间的名称变更历史
func getLiveNameHistory(writer http.ResponseWriter, r *http.Request) {
	inst := instance.GetInstance(r.Context())
	vars := mux.Vars(r)
	liveID := vars["id"]

	// 检查直播间是否存在
	if !inst.Lives.Has(types.LiveID(liveID)) {
		writeJsonWithStatusCode(writer, http.StatusNotFound, commonResp{
			ErrNo:  http.StatusNotFound,
			ErrMsg: fmt.Sprintf("live id: %s can not find", liveID),
		})
		return
	}

	// 获取 limit 参数
	limit := 50 // 默认 50 条
	if limitStr := r.URL.Query().Get("limit"); limitStr != "" {
		if parsedLimit, err := strconv.Atoi(limitStr); err == nil && parsedLimit > 0 {
			limit = parsedLimit
		}
	}

	// 获取 LiveStateManager
	manager, ok := inst.LiveStateManager.(*livestate.Manager)
	if !ok || manager == nil {
		writeJsonWithStatusCode(writer, http.StatusServiceUnavailable, commonResp{
			ErrNo:  http.StatusServiceUnavailable,
			ErrMsg: "状态持久化功能未启用",
		})
		return
	}

	// 获取名称变更历史
	changes := manager.GetNameHistory(liveID, limit)

	writeJSON(writer, map[string]interface{}{
		"live_id":      liveID,
		"name_changes": changes,
		"total":        len(changes),
	})
}

// HistoryEvent 统一的历史事件格式
type HistoryEvent struct {
	ID        int64     `json:"id"`
	Type      string    `json:"type"`      // "session" 或 "name_change"
	Timestamp time.Time `json:"timestamp"` // 事件时间
	Data      any       `json:"data"`      // 事件详情
}

// getLiveHistory 获取直播间的历史事件（统一接口，支持分页和筛选）
func getLiveHistory(writer http.ResponseWriter, r *http.Request) {
	inst := instance.GetInstance(r.Context())
	vars := mux.Vars(r)
	liveID := vars["id"]

	// 检查直播间是否存在
	if !inst.Lives.Has(types.LiveID(liveID)) {
		writeJsonWithStatusCode(writer, http.StatusNotFound, commonResp{
			ErrNo:  http.StatusNotFound,
			ErrMsg: fmt.Sprintf("live id: %s can not find", liveID),
		})
		return
	}

	// 获取 LiveStateManager
	manager, ok := inst.LiveStateManager.(*livestate.Manager)
	if !ok || manager == nil {
		writeJsonWithStatusCode(writer, http.StatusServiceUnavailable, commonResp{
			ErrNo:  http.StatusServiceUnavailable,
			ErrMsg: "状态持久化功能未启用",
		})
		return
	}

	// 解析查询参数
	query := r.URL.Query()

	// 分页参数
	page := 1
	pageSize := 20
	if pageStr := query.Get("page"); pageStr != "" {
		if p, err := strconv.Atoi(pageStr); err == nil && p > 0 {
			page = p
		}
	}
	if pageSizeStr := query.Get("page_size"); pageSizeStr != "" {
		if ps, err := strconv.Atoi(pageSizeStr); err == nil && ps > 0 && ps <= 100 {
			pageSize = ps
		}
	}

	// 时间范围参数
	var startTime, endTime time.Time
	if startStr := query.Get("start_time"); startStr != "" {
		if t, err := time.Parse(time.RFC3339, startStr); err == nil {
			startTime = t
		} else if ts, err := strconv.ParseInt(startStr, 10, 64); err == nil {
			startTime = time.Unix(ts, 0)
		}
	}
	if endStr := query.Get("end_time"); endStr != "" {
		if t, err := time.Parse(time.RFC3339, endStr); err == nil {
			endTime = t
		} else if ts, err := strconv.ParseInt(endStr, 10, 64); err == nil {
			endTime = time.Unix(ts, 0)
		}
	}

	// 事件类型筛选
	eventTypes := query["type"] // 支持多选: ?type=session&type=name_change
	includeSession := len(eventTypes) == 0 || contains(eventTypes, "session")
	includeNameChange := len(eventTypes) == 0 || contains(eventTypes, "name_change")

	// 收集所有事件
	var events []HistoryEvent

	// 获取会话历史
	if includeSession {
		sessions := manager.GetSessionHistory(liveID, 1000) // 获取足够多的记录用于筛选
		for _, s := range sessions {
			eventTime := s.StartTime
			// 时间范围筛选
			if !startTime.IsZero() && eventTime.Before(startTime) {
				continue
			}
			if !endTime.IsZero() && eventTime.After(endTime) {
				continue
			}
			events = append(events, HistoryEvent{
				ID:        s.ID,
				Type:      "session",
				Timestamp: eventTime,
				Data:      s,
			})
		}
	}

	// 获取名称变更历史
	if includeNameChange {
		changes := manager.GetNameHistory(liveID, 1000)
		for _, c := range changes {
			// 时间范围筛选
			if !startTime.IsZero() && c.ChangedAt.Before(startTime) {
				continue
			}
			if !endTime.IsZero() && c.ChangedAt.After(endTime) {
				continue
			}
			events = append(events, HistoryEvent{
				ID:        c.ID,
				Type:      "name_change",
				Timestamp: c.ChangedAt,
				Data:      c,
			})
		}
	}

	// 按时间倒序排序
	sort.Slice(events, func(i, j int) bool {
		return events[i].Timestamp.After(events[j].Timestamp)
	})

	// 计算总数和分页
	total := len(events)
	totalPages := (total + pageSize - 1) / pageSize
	startIdx := (page - 1) * pageSize
	endIdx := startIdx + pageSize
	if startIdx >= total {
		events = []HistoryEvent{}
	} else {
		if endIdx > total {
			endIdx = total
		}
		events = events[startIdx:endIdx]
	}

	writeJSON(writer, map[string]interface{}{
		"live_id":     liveID,
		"events":      events,
		"total":       total,
		"page":        page,
		"page_size":   pageSize,
		"total_pages": totalPages,
	})
}

// contains 检查切片是否包含某个值
func contains(slice []string, val string) bool {
	for _, item := range slice {
		if item == val {
			return true
		}
	}
	return false
}

// ============================================================================
// 远程 WebUI 相关处理函数
// ============================================================================

// RemoteWebuiStatusResponse 远程 WebUI 状态响应
type RemoteWebuiStatusResponse struct {
	Available          bool                   `json:"available"`
	AppVersion         string                 `json:"app_version"`
	RemoteUIVersion    string                 `json:"remote_ui_version,omitempty"`
	LocalUIVersion     string                 `json:"local_ui_version,omitempty"`
	RemoteUIURL        string                 `json:"remote_ui_url,omitempty"`
	Error              string                 `json:"error,omitempty"`
	LastCheck          string                 `json:"last_check,omitempty"`
	RemoteWebuiBaseURL string                 `json:"remote_webui_base_url"`
	Status             map[string]interface{} `json:"status,omitempty"`
}

// getRemoteWebuiStatus 获取远程 WebUI 状态
func getRemoteWebuiStatus(writer http.ResponseWriter, r *http.Request) {
	response := RemoteWebuiStatusResponse{
		Available:          false,
		AppVersion:         consts.AppVersion,
		RemoteWebuiBaseURL: "https://bililive-go.com",
	}

	// 读取本地 UI 版本
	response.LocalUIVersion = getLocalUIVersion()

	// 尝试获取远程 WebUI 信息
	remoteInfo, err := fetchRemoteWebuiInfo(consts.AppVersion)
	if err != nil {
		response.Error = err.Error()
		writeJSON(writer, response)
		return
	}

	response.Available = true
	response.RemoteUIVersion = remoteInfo.UIVersion
	response.RemoteUIURL = remoteInfo.IndexURL
	response.LastCheck = time.Now().Format(time.RFC3339)

	writeJSON(writer, response)
}

// RemoteWebuiInfo 远程 WebUI 信息
type RemoteWebuiInfo struct {
	UIVersion string `json:"uiVersion"`
	IndexURL  string `json:"indexUrl"`
	WebuiPath string `json:"webuiPath"`
}

// fetchRemoteWebuiInfo 从远程获取 WebUI 信息
func fetchRemoteWebuiInfo(appVersion string) (*RemoteWebuiInfo, error) {
	apiURL := fmt.Sprintf("https://bililive-go.com/api/webui?appversion=%s", url.QueryEscape(appVersion))

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get(apiURL)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch remote webui info: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("remote API returned status %d: %s", resp.StatusCode, string(body))
	}

	var result struct {
		SelectedVersion struct {
			UIVersion string `json:"uiVersion"`
		} `json:"selectedVersion"`
		IndexURL  string `json:"indexUrl"`
		WebuiPath string `json:"webuiPath"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to decode remote webui info: %w", err)
	}

	return &RemoteWebuiInfo{
		UIVersion: result.SelectedVersion.UIVersion,
		IndexURL:  result.IndexURL,
		WebuiPath: result.WebuiPath,
	}, nil
}

// getLocalUIVersion 获取本地 UI 版本
func getLocalUIVersion() string {
	// 尝试从嵌入的 version.json 读取
	// 如果读取失败，返回 "unknown"
	return "1.0.0" // TODO: 从嵌入的资源中读取
}

// checkRemoteWebuiUpdate 检查远程 WebUI 是否有更新
func checkRemoteWebuiUpdate(writer http.ResponseWriter, r *http.Request) {
	localVersion := getLocalUIVersion()

	remoteInfo, err := fetchRemoteWebuiInfo(consts.AppVersion)
	if err != nil {
		writeJSON(writer, map[string]interface{}{
			"has_update":    false,
			"error":         err.Error(),
			"local_version": localVersion,
			"app_version":   consts.AppVersion,
		})
		return
	}

	hasUpdate := remoteInfo.UIVersion != localVersion && remoteInfo.UIVersion != ""

	writeJSON(writer, map[string]interface{}{
		"has_update":     hasUpdate,
		"local_version":  localVersion,
		"remote_version": remoteInfo.UIVersion,
		"remote_url":     remoteInfo.IndexURL,
		"app_version":    consts.AppVersion,
	})
}

// getMemoryStats 获取内存统计信息
func getMemoryStats(writer http.ResponseWriter, r *http.Request) {
	inst := instance.GetInstance(r.Context())

	// 获取所有 parser 的 PID
	var pids []int
	if rm, ok := inst.RecorderManager.(recorders.Manager); ok {
		pids = rm.GetAllParserPIDs()
	}

	// 添加所有通过 tools 包启动的子进程 PID（如 bililive-tools、klive 等）
	toolsPIDs := tools.GetAllProcessPIDs()
	pids = append(pids, toolsPIDs...)

	stats, err := memstats.GetMemoryStats(pids)
	if err != nil {
		writeJsonWithStatusCode(writer, http.StatusInternalServerError, commonResp{
			ErrNo:  http.StatusInternalServerError,
			ErrMsg: fmt.Sprintf("获取内存统计失败: %v", err),
		})
		return
	}

	writeJSON(writer, stats)
}

// getBilibiliQRCode 获取哔哩哔哩登录二维码
func getBilibiliQRCode(writer http.ResponseWriter, r *http.Request) {
	resp, err := requests.Get("https://passport.bilibili.com/x/passport-login/web/qrcode/generate")
	if err != nil {
		writeJsonWithStatusCode(writer, http.StatusInternalServerError, commonResp{
			ErrNo:  http.StatusInternalServerError,
			ErrMsg: "获取二维码失败: " + err.Error(),
		})
		return
	}
	body, err := resp.Bytes()
	if err != nil {
		writeJsonWithStatusCode(writer, http.StatusInternalServerError, commonResp{
			ErrNo:  http.StatusInternalServerError,
			ErrMsg: "读取响应体失败: " + err.Error(),
		})
		return
	}
	writer.Header().Set("Content-Type", "application/json")
	writer.Write(body)
}

// pollBilibiliQRCode 轮询哔哩哔哩登录状态
func pollBilibiliQRCode(writer http.ResponseWriter, r *http.Request) {
	key := r.URL.Query().Get("key")
	if key == "" {
		writeJsonWithStatusCode(writer, http.StatusBadRequest, commonResp{ErrNo: http.StatusBadRequest, ErrMsg: "缺少 key 参数"})
		return
	}

	resp, err := requests.Get("https://passport.bilibili.com/x/passport-login/web/qrcode/poll", requests.Query("qrcode_key", key))
	if err != nil {
		writeJsonWithStatusCode(writer, http.StatusInternalServerError, commonResp{
			ErrNo:  http.StatusInternalServerError,
			ErrMsg: "轮询登录状态失败: " + err.Error(),
		})
		return
	}

	body, err := resp.Bytes()
	if err != nil {
		writeJsonWithStatusCode(writer, http.StatusInternalServerError, commonResp{
			ErrNo:  http.StatusInternalServerError,
			ErrMsg: "读取响应体失败: " + err.Error(),
		})
		return
	}

	// 登录成功时 B 站把凭证放在响应头 Set-Cookie 里下发，body 的 data.url 只有一个跳转地址，
	// 前端光解析 data.url 拼不出 SESSDATA，所以这里把登录字段补进 data.cookies 再回传。
	body = attachBilibiliLoginCookies(body, resp.Cookies())

	writer.Header().Set("Content-Type", "application/json")
	writer.Write(body)
}

// biliLoginCookieNames 是录制与弹幕真正需要的 B 站 Cookie 字段。
// buvid、_uuid 这类埋点字段不回传，避免被写进配置文件。
var biliLoginCookieNames = map[string]bool{
	"SESSDATA":          true,
	"bili_jct":          true,
	"DedeUserID":        true,
	"DedeUserID__ckMd5": true,
	"sid":               true,
}

// attachBilibiliLoginCookies 在轮询返回登录成功时，把登录字段汇总到 data.cookies，
// 其余上游字段原样保留；非成功状态或解析失败都返回原始 body。
func attachBilibiliLoginCookies(body []byte, cookies []*http.Cookie) []byte {
	var result map[string]interface{}
	decoder := json.NewDecoder(bytes.NewReader(body))
	// 必须用 UseNumber：默认会把所有数字解析成 float64，重新 Marshal 时
	// 超过 2^53 的整数（B 站以后可能回 mid/room_id 这类大整数）会被静默截断成近似值。
	decoder.UseNumber()
	if err := decoder.Decode(&result); err != nil {
		applog.GetLogger().Error("轮询登录状态解析 JSON 失败: " + err.Error())
		return body
	}
	data, ok := result["data"].(map[string]interface{})
	if !ok || !isJSONZero(result["code"]) || !isJSONZero(data["code"]) {
		return body
	}

	login := make(map[string]string)
	// 上游也可能把字段挂在 data.url 的 query 上（原实现只从 URL 里取 sid），这里一并收进来，
	// 同名时 Set-Cookie 的值优先。
	if rawURL, _ := data["url"].(string); rawURL != "" {
		if u, err := url.Parse(rawURL); err == nil {
			for name, values := range u.Query() {
				if biliLoginCookieNames[name] && len(values) > 0 {
					login[name] = values[0]
				}
			}
		}
	}
	for _, c := range cookies {
		if biliLoginCookieNames[c.Name] && c.Value != "" {
			login[c.Name] = c.Value
		}
	}
	if len(login) == 0 {
		return body
	}

	data["cookies"] = login
	newBody, err := json.Marshal(result)
	if err != nil {
		applog.GetLogger().Error("序列化增强后的登录结果失败: " + err.Error())
		return body
	}
	return newBody
}

// isJSONZero 判断 json 数值字段是否为 0（配合 UseNumber，数值是 json.Number）。
func isJSONZero(v interface{}) bool {
	switch number := v.(type) {
	case json.Number:
		return number.String() == "0"
	case float64:
		return number == 0
	default:
		return false
	}
}

// verifyBilibiliCookie 验证哔哩哔哩 Cookie 有效性
func verifyBilibiliCookie(writer http.ResponseWriter, r *http.Request) {
	var req struct {
		Cookie string `json:"cookie"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJsonWithStatusCode(writer, http.StatusBadRequest, commonResp{ErrNo: http.StatusBadRequest, ErrMsg: "无效的请求体"})
		return
	}

	resp, err := requests.Get("https://api.bilibili.com/x/web-interface/nav", requests.Header("Cookie", req.Cookie))
	if err != nil {
		writeJsonWithStatusCode(writer, http.StatusInternalServerError, commonResp{
			ErrNo:  http.StatusInternalServerError,
			ErrMsg: "验证 Cookie 失败: " + err.Error(),
		})
		return
	}
	body, err := resp.Bytes()
	if err != nil {
		writeJsonWithStatusCode(writer, http.StatusInternalServerError, commonResp{
			ErrNo:  http.StatusInternalServerError,
			ErrMsg: "读取响应体失败: " + err.Error(),
		})
		return
	}
	writer.Header().Set("Content-Type", "application/json")
	writer.Write(body)
}

// getDouyuQRCode 获取斗鱼扫码登录二维码（后端代理 passport.douyu.com，
// 规避跨域与 WAF 对非浏览器来源请求的拦截）
func getDouyuQRCode(writer http.ResponseWriter, r *http.Request) {
	session, err := dy.GenerateLoginQRCode(r.Context())
	if err != nil {
		applog.GetLogger().WithError(err).Warn("获取斗鱼登录二维码失败")
		writeJsonWithStatusCode(writer, http.StatusInternalServerError, commonResp{
			ErrNo:  http.StatusInternalServerError,
			ErrMsg: "获取斗鱼登录二维码失败: " + err.Error(),
		})
		return
	}
	writeJSON(writer, commonResp{Data: session})
}

// pollDouyuQRCode 轮询斗鱼扫码登录状态；用户在手机端确认成功后，
// 后端自动用回调 URL 换取登录 cookie 并写入 configs.Cookies["www.douyu.com"]，
// 同步应用到运行中的斗鱼房间（录制流请求将携带该 cookie，消除约 5 分钟的匿名断流）。
func pollDouyuQRCode(writer http.ResponseWriter, r *http.Request) {
	code := r.URL.Query().Get("code")
	result, err := dy.PollLoginQRCode(r.Context(), code)
	if err != nil {
		applog.GetLogger().WithError(err).Warn("轮询斗鱼登录状态失败")
		writeJsonWithStatusCode(writer, http.StatusInternalServerError, commonResp{
			ErrNo:  http.StatusInternalServerError,
			ErrMsg: "轮询斗鱼登录状态失败: " + err.Error(),
		})
		return
	}

	if result.State == dy.QRStateSuccess {
		newCfg, err := configs.UpdateWithRetry(func(c *configs.Config) error {
			applyDouyuScanResult(c, result, time.Now())
			return nil
		}, 3, 10*time.Millisecond)
		if err != nil {
			// 手机端已经确认登录、票据已消耗，回 HTTP 500 会让前端只看到"查询连续失败"，
			// 用户以为是网络问题而反复重扫。用独立状态把"保存失败"这个真相送到界面上。
			applog.GetLogger().WithError(err).Error("斗鱼扫码登录成功但写入配置失败")
			result.State = dy.QRStateSaveFailed
			result.Msg = err.Error()
			writeJSON(writer, commonResp{Data: result})
			return
		}
		// 扫码已经拿到全新登录态，为"上一轮写盘失败"记的内存退避到此失去意义：
		// 不退避就会让 keeper 在几小时后拿刚扫到的新鲜 cookie 再打一次 safeAuth。
		clearRenewScheduleOverride()
		// 同理复位进程内的提醒去重标记：用户按提示重扫成功后，若还是这张票据又出了同样的问题，
		// 必须还能再提醒一次，否则"我扫过了却又断了"将彻底无声。
		noteRenewHealthy()
		applyCookiesToLives(r.Context(), newCfg, dy.CookieHost)
		applog.GetLogger().Infof("斗鱼扫码登录成功: uid=%s nickname=%s cookieLength=%d gotLTP0=%t", result.UserID, result.Nickname, len(result.Cookie), result.LTP0 != "")
	}

	// 不回传原始 cookie 内容，前端需要时可经 /api/cookies 查看；
	// LTP0/dy_did 是 passport 域长期凭证，绝不能下发给前端，仅留在后端配置中用于续期。
	result.Cookie = ""
	result.LTP0 = ""
	result.DyDid = ""
	writeJSON(writer, commonResp{Data: result})
}

// douyuAuthStatus 斗鱼登录态自动续期的前端可见状态。
// 只暴露状态与时间点，绝不回传 LTP0/dy_did/cookie 原文。
type douyuAuthStatus struct {
	LoggedIn      bool  `json:"logged_in"`       // 登录态是否可用（有 acf_uid，且续期链路没有长期停摆）
	HasLTP0       bool  `json:"has_ltp0"`        // 是否已绑定扫码引导的长期凭证
	NeedRescan    bool  `json:"need_rescan"`     // 自动续期是否已判定凭证失效、需重新扫码
	NextRefreshAt int64 `json:"next_refresh_at"` // 下次自动续期的 Unix 秒；0 表示尚未排期
}

// getDouyuAuthStatus 供 Web 界面展示斗鱼 cookie 自动续期健康状况（续期失败提醒等）
func getDouyuAuthStatus(writer http.ResponseWriter, r *http.Request) {
	cfg := configs.GetCurrentConfig()
	if cfg == nil {
		writeJsonWithStatusCode(writer, http.StatusInternalServerError, commonResp{
			ErrNo:  http.StatusInternalServerError,
			ErrMsg: "配置尚未加载",
		})
		return
	}
	auth := cfg.DouyuAuth
	loggedIn := douyuCookieUid(cfg.Cookies[dy.CookieHost]) != ""
	// cookie 里有 acf_uid 只代表"曾经登录过"，不代表现在还在有效期内（主站 cookie 约 6 天）。
	// 判据与录制侧共用 dy.LoginCookieKnownExpired：界面说"未登录"和录制端"不再发这份 cookie"
	// 必须同口径，两处各留一份阈值迟早会漂移（表现为界面绿着却一直在匿名录制，或反过来
	// 界面报未登录而实际还能用）。没有 LTP0（手填 cookie、历史配置）时没有任何过期证据，不据此判未登录。
	if loggedIn && dy.LoginCookieKnownExpired(auth.LTP0, auth.LastRenewSuccessAt, time.Now()) {
		loggedIn = false
	}
	writeJSON(writer, commonResp{Data: douyuAuthStatus{
		LoggedIn:      loggedIn,
		HasLTP0:       auth.LTP0 != "",
		NeedRescan:    auth.NeedRescan,
		NextRefreshAt: effectiveNextRefreshAt(cfg), // 内存退避也要计入，否则界面显示的续期时间比实际早
	}})
}

// startRecordDirect 直接启动录制（绕过 Listener，适用于 NotifyOnly 房间）
func startRecordDirect(writer http.ResponseWriter, r *http.Request) {
	inst := instance.GetInstance(r.Context())
	vars := mux.Vars(r)
	resp := commonResp{}

	liveObj, ok := inst.Lives.Get(types.LiveID(vars["id"]))
	if !ok {
		resp.ErrNo = http.StatusNotFound
		resp.ErrMsg = fmt.Sprintf("live id: %s can not find", vars["id"])
		writeJsonWithStatusCode(writer, http.StatusNotFound, resp)
		return
	}

	recorderMgr, ok := inst.RecorderManager.(recorders.Manager)
	if !ok {
		resp.ErrNo = http.StatusInternalServerError
		resp.ErrMsg = "录制管理器不可用"
		writeJsonWithStatusCode(writer, http.StatusInternalServerError, resp)
		return
	}

	// 检查是否已在录制
	if recorderMgr.HasRecorder(inst.Ctx, liveObj.GetLiveId()) {
		resp.ErrNo = http.StatusConflict
		resp.ErrMsg = "该直播间已在录制中"
		writeJsonWithStatusCode(writer, http.StatusConflict, resp)
		return
	}

	if err := recorderMgr.AddRecorder(inst.Ctx, liveObj); err != nil {
		resp.ErrNo = http.StatusInternalServerError
		resp.ErrMsg = fmt.Sprintf("启动录制失败: %s", err.Error())
		writeJsonWithStatusCode(writer, http.StatusInternalServerError, resp)
		return
	}

	// 广播录制开始事件
	GetSSEHub().BroadcastListChange(liveObj.GetLiveId(), "record_start", map[string]interface{}{
		"live_id": string(liveObj.GetLiveId()),
	})

	writeJSON(writer, parseInfo(r.Context(), liveObj))
}

// stopRecordDirect 直接停止录制
func stopRecordDirect(writer http.ResponseWriter, r *http.Request) {
	inst := instance.GetInstance(r.Context())
	vars := mux.Vars(r)
	resp := commonResp{}

	liveObj, ok := inst.Lives.Get(types.LiveID(vars["id"]))
	if !ok {
		resp.ErrNo = http.StatusNotFound
		resp.ErrMsg = fmt.Sprintf("live id: %s can not find", vars["id"])
		writeJsonWithStatusCode(writer, http.StatusNotFound, resp)
		return
	}

	recorderMgr, ok := inst.RecorderManager.(recorders.Manager)
	if !ok {
		resp.ErrNo = http.StatusInternalServerError
		resp.ErrMsg = "录制管理器不可用"
		writeJsonWithStatusCode(writer, http.StatusInternalServerError, resp)
		return
	}

	if !recorderMgr.HasRecorder(inst.Ctx, liveObj.GetLiveId()) {
		resp.ErrNo = http.StatusNotFound
		resp.ErrMsg = "该直播间未在录制中"
		writeJsonWithStatusCode(writer, http.StatusNotFound, resp)
		return
	}

	if err := recorderMgr.RemoveRecorder(inst.Ctx, liveObj.GetLiveId()); err != nil {
		resp.ErrNo = http.StatusInternalServerError
		resp.ErrMsg = fmt.Sprintf("停止录制失败: %s", err.Error())
		writeJsonWithStatusCode(writer, http.StatusInternalServerError, resp)
		return
	}

	// 广播录制停止事件
	GetSSEHub().BroadcastListChange(liveObj.GetLiveId(), "record_stop", map[string]interface{}{
		"live_id": string(liveObj.GetLiveId()),
	})

	writeJSON(writer, parseInfo(r.Context(), liveObj))
}
