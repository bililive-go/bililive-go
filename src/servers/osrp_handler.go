package servers

import (
	"context"
	"encoding/json"
	"net/http"
	"runtime"
	"strings"
	"time"

	"github.com/gorilla/mux"

	"github.com/bililive-go/bililive-go/src/configs"
	"github.com/bililive-go/bililive-go/src/consts"
	"github.com/bililive-go/bililive-go/src/instance"
	"github.com/bililive-go/bililive-go/src/live"
	"github.com/bililive-go/bililive-go/src/recorders"
	"github.com/bililive-go/bililive-go/src/types"
)

// OSRP 版本
const OSRPVersion = "0.1.0"

// OSRPResponse 标准响应格式
type OSRPResponse struct {
	Success bool        `json:"success"`
	Data    interface{} `json:"data,omitempty"`
	Error   *OSRPError  `json:"error,omitempty"`
}

// OSRPError 错误信息
type OSRPError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// osrpWriteJSON 写入 JSON 响应
func osrpWriteJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(data)
}

// osrpWriteSuccess 写入成功响应
func osrpWriteSuccess(w http.ResponseWriter, data interface{}) {
	osrpWriteJSON(w, http.StatusOK, OSRPResponse{
		Success: true,
		Data:    data,
	})
}

// osrpWriteError 写入错误响应
func osrpWriteError(w http.ResponseWriter, status int, code, message string) {
	osrpWriteJSON(w, status, OSRPResponse{
		Success: false,
		Error: &OSRPError{
			Code:    code,
			Message: message,
		},
	})
}

// ============================================
// 服务信息 API
// ============================================

// OSRPServiceInfo 服务信息
type OSRPServiceInfo struct {
	Name         string   `json:"name"`
	Version      string   `json:"version"`
	OSRPVersion  string   `json:"osrp_version"`
	Capabilities []string `json:"capabilities"`
	GoVersion    string   `json:"go_version"`
	OS           string   `json:"os"`
	Arch         string   `json:"arch"`
}

// osrpGetInfo GET /osrp/v1/info
func osrpGetInfo(w http.ResponseWriter, r *http.Request) {
	osrpWriteSuccess(w, OSRPServiceInfo{
		Name:        consts.AppInfo.AppName,
		Version:     consts.AppInfo.AppVersion,
		OSRPVersion: OSRPVersion,
		Capabilities: []string{
			"tasks",
			"streams",
			"resolve",
			"probe",
			"config",
			"sse",
		},
		GoVersion: runtime.Version(),
		OS:        runtime.GOOS,
		Arch:      runtime.GOARCH,
	})
}

// ============================================
// 能力声明 API
// ============================================

// OSRPCapabilities 能力声明
type OSRPCapabilities struct {
	StreamURLResolve  bool     `json:"stream_url_resolve"`
	StreamProbe       bool     `json:"stream_probe"`
	MultiQualityProbe bool     `json:"multi_quality_probe"`
	MP4Convert        bool     `json:"mp4_convert"`
	SegmentRecording  bool     `json:"segment_recording"`
	Webhook           bool     `json:"webhook"`
	SSE               bool     `json:"sse"`
	Platforms         []string `json:"platforms"`
}

// osrpGetCapabilities GET /osrp/v1/capabilities
func osrpGetCapabilities(w http.ResponseWriter, r *http.Request) {
	cfg := configs.GetCurrentConfig()

	mp4Convert := false
	if cfg != nil {
		mp4Convert = cfg.OnRecordFinished.ConvertToMp4
	}

	// 支持的平台列表
	platforms := []string{
		"bilibili", "douyin", "huya", "douyu", "kuaishou",
		"twitch", "youtube", "langlive", "xiaohongshu",
		"acfun", "yizhibo", "openrec", "missevan",
	}

	osrpWriteSuccess(w, OSRPCapabilities{
		StreamURLResolve:  true,
		StreamProbe:       true,
		MultiQualityProbe: true,
		MP4Convert:        mp4Convert,
		SegmentRecording:  true,
		Webhook:           true,
		SSE:               true,
		Platforms:         platforms,
	})
}

// ============================================
// 任务管理 API
// ============================================

// OSRPTaskInfo 任务信息
type OSRPTaskInfo struct {
	ID             string     `json:"id"`
	Platform       string     `json:"platform"`
	StreamID       string     `json:"stream_id"`
	URL            string     `json:"url"`
	HostName       string     `json:"host_name"`
	RoomName       string     `json:"room_name"`
	Status         string     `json:"status"`
	IsLive         bool       `json:"is_live"`
	IsListening    bool       `json:"is_listening"`
	IsRecording    bool       `json:"is_recording"`
	RecordingSince *time.Time `json:"recording_since,omitempty"`
}

// convertLiveToOSRPTask 将 Live 转换为 OSRPTaskInfo
func convertLiveToOSRPTask(ctx context.Context, l live.Live) OSRPTaskInfo {
	inst := instance.GetInstance(ctx)

	// 从缓存获取信息
	info := parseInfo(ctx, l)

	isRecording := info.Recording
	var recordingSince *time.Time

	if isRecording {
		if recorderMgr, ok := inst.RecorderManager.(recorders.Manager); ok {
			if rec, err := recorderMgr.GetRecorder(ctx, l.GetLiveId()); err == nil && rec != nil {
				t := rec.StartTime()
				recordingSince = &t
			}
		}
	}

	status := "waiting"
	if isRecording {
		status = "recording"
	} else if info.Status {
		status = "live"
	}

	return OSRPTaskInfo{
		ID:             string(l.GetLiveId()),
		Platform:       l.GetPlatformCNName(),
		StreamID:       string(l.GetLiveId()),
		URL:            l.GetRawUrl(),
		HostName:       info.HostName,
		RoomName:       info.RoomName,
		Status:         status,
		IsLive:         info.Status,
		IsListening:    info.Listening,
		IsRecording:    isRecording,
		RecordingSince: recordingSince,
	}
}

// OSRPTaskListResponse 任务列表响应
type OSRPTaskListResponse struct {
	Tasks []OSRPTaskInfo `json:"tasks"`
	Total int            `json:"total"`
}

// osrpGetTasks GET /osrp/v1/tasks
func osrpGetTasks(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	inst := instance.GetInstance(ctx)

	tasks := make([]OSRPTaskInfo, 0, len(inst.Lives))
	for _, l := range inst.Lives {
		tasks = append(tasks, convertLiveToOSRPTask(ctx, l))
	}

	osrpWriteSuccess(w, OSRPTaskListResponse{
		Tasks: tasks,
		Total: len(tasks),
	})
}

// osrpGetTask GET /osrp/v1/tasks/{id}
func osrpGetTask(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	inst := instance.GetInstance(ctx)
	vars := mux.Vars(r)
	id := vars["id"]

	l, ok := inst.Lives[types.LiveID(id)]
	if !ok {
		osrpWriteError(w, http.StatusNotFound, "TASK_NOT_FOUND", "任务不存在")
		return
	}

	osrpWriteSuccess(w, convertLiveToOSRPTask(ctx, l))
}

// OSRPAddTaskRequest 添加任务请求
type OSRPAddTaskRequest struct {
	URL       string `json:"url"`
	AutoStart bool   `json:"auto_start"`
}

// osrpAddTask POST /osrp/v1/tasks
func osrpAddTask(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var req OSRPAddTaskRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		osrpWriteError(w, http.StatusBadRequest, "INVALID_REQUEST", "请求格式错误")
		return
	}

	if req.URL == "" {
		osrpWriteError(w, http.StatusBadRequest, "MISSING_URL", "缺少 URL 参数")
		return
	}

	// 添加直播间
	info, err := addLiveImpl(ctx, req.URL, req.AutoStart)
	if err != nil {
		osrpWriteError(w, http.StatusBadRequest, "ADD_FAILED", err.Error())
		return
	}

	// 获取刚添加的 live
	inst := instance.GetInstance(ctx)
	l, ok := inst.Lives[info.Live.GetLiveId()]
	if !ok {
		osrpWriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "添加成功但无法获取任务")
		return
	}

	osrpWriteJSON(w, http.StatusCreated, OSRPResponse{
		Success: true,
		Data:    convertLiveToOSRPTask(ctx, l),
	})
}

// osrpDeleteTask DELETE /osrp/v1/tasks/{id}
func osrpDeleteTask(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	inst := instance.GetInstance(ctx)
	vars := mux.Vars(r)
	id := vars["id"]

	l, ok := inst.Lives[types.LiveID(id)]
	if !ok {
		osrpWriteError(w, http.StatusNotFound, "TASK_NOT_FOUND", "任务不存在")
		return
	}

	if err := removeLiveImpl(ctx, l); err != nil {
		osrpWriteError(w, http.StatusInternalServerError, "DELETE_FAILED", err.Error())
		return
	}

	osrpWriteSuccess(w, map[string]bool{"deleted": true})
}

// OSRPTaskActionRequest 任务操作请求
type OSRPTaskActionRequest struct {
	Action string `json:"action"` // start, stop, cut
}

// osrpTaskAction POST /osrp/v1/tasks/{id}/actions
func osrpTaskAction(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	inst := instance.GetInstance(ctx)
	vars := mux.Vars(r)
	id := vars["id"]

	var req OSRPTaskActionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		osrpWriteError(w, http.StatusBadRequest, "INVALID_REQUEST", "请求格式错误")
		return
	}

	l, ok := inst.Lives[types.LiveID(id)]
	if !ok {
		osrpWriteError(w, http.StatusNotFound, "TASK_NOT_FOUND", "任务不存在")
		return
	}

	var err error
	switch req.Action {
	case "start":
		err = startListening(ctx, l)
	case "stop":
		err = stopListening(ctx, l.GetLiveId())
	case "cut":
		// cut 操作在当前版本不支持
		osrpWriteError(w, http.StatusNotImplemented, "NOT_IMPLEMENTED", "切片操作暂不支持")
		return
	default:
		osrpWriteError(w, http.StatusBadRequest, "INVALID_ACTION", "不支持的操作: "+req.Action)
		return
	}

	if err != nil {
		osrpWriteError(w, http.StatusInternalServerError, "ACTION_FAILED", err.Error())
		return
	}

	osrpWriteSuccess(w, map[string]string{
		"action": req.Action,
		"result": "ok",
	})
}

// ============================================
// 直播解析 API
// ============================================

// OSRPResolveRequest URL 解析请求
type OSRPResolveRequest struct {
	URL string `json:"url"`
}

// OSRPResolveResponse URL 解析响应
type OSRPResolveResponse struct {
	Platform     string `json:"platform"`
	StreamID     string `json:"stream_id"`
	CanonicalURL string `json:"canonical_url"`
}

// osrpResolve POST /osrp/v1/resolve
func osrpResolve(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var req OSRPResolveRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		osrpWriteError(w, http.StatusBadRequest, "INVALID_REQUEST", "请求格式错误")
		return
	}

	if req.URL == "" {
		osrpWriteError(w, http.StatusBadRequest, "MISSING_URL", "缺少 URL 参数")
		return
	}

	// 临时创建 LiveRoom 配置
	room := &configs.LiveRoom{Url: req.URL}
	l, err := live.New(ctx, room, nil)
	if err != nil {
		osrpWriteError(w, http.StatusBadRequest, "RESOLVE_FAILED", err.Error())
		return
	}
	defer l.Close()

	osrpWriteSuccess(w, OSRPResolveResponse{
		Platform:     l.GetPlatformCNName(),
		StreamID:     string(l.GetLiveId()),
		CanonicalURL: l.GetRawUrl(),
	})
}

// OSRPStreamStatusResponse 直播状态响应
type OSRPStreamStatusResponse struct {
	IsLive     bool      `json:"is_live"`
	Title      string    `json:"title"`
	AnchorName string    `json:"anchor_name"`
	CheckedAt  time.Time `json:"checked_at"`
}

// osrpGetStreamStatus GET /osrp/v1/streams/{platform}/{id}/status
func osrpGetStreamStatus(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	vars := mux.Vars(r)
	platform := vars["platform"]
	streamID := vars["id"]

	urlStr := osrpBuildURLFromPlatform(platform, streamID)
	if urlStr == "" {
		osrpWriteError(w, http.StatusBadRequest, "UNSUPPORTED_PLATFORM", "不支持的平台: "+platform)
		return
	}

	room := &configs.LiveRoom{Url: urlStr}
	l, err := live.New(ctx, room, nil)
	if err != nil {
		osrpWriteError(w, http.StatusBadRequest, "CREATE_LIVE_FAILED", err.Error())
		return
	}
	defer l.Close()

	info, err := l.GetInfo()
	if err != nil {
		osrpWriteError(w, http.StatusInternalServerError, "GET_INFO_FAILED", err.Error())
		return
	}

	osrpWriteSuccess(w, OSRPStreamStatusResponse{
		IsLive:     info.Status,
		Title:      info.RoomName,
		AnchorName: info.HostName,
		CheckedAt:  time.Now(),
	})
}

// OSRPStreamURLInfo 直播流信息
type OSRPStreamURLInfo struct {
	Quality   string `json:"quality"`
	QualityID int    `json:"quality_id"`
	Format    string `json:"format"`
	URL       string `json:"url"`
}

// OSRPStreamURLsResponse 直播流响应
type OSRPStreamURLsResponse struct {
	Streams []OSRPStreamURLInfo `json:"streams"`
}

// osrpGetStreamURLs GET /osrp/v1/streams/{platform}/{id}/urls
func osrpGetStreamURLs(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	vars := mux.Vars(r)
	platform := vars["platform"]
	streamID := vars["id"]

	urlStr := osrpBuildURLFromPlatform(platform, streamID)
	if urlStr == "" {
		osrpWriteError(w, http.StatusBadRequest, "UNSUPPORTED_PLATFORM", "不支持的平台: "+platform)
		return
	}

	room := &configs.LiveRoom{Url: urlStr}
	l, err := live.New(ctx, room, nil)
	if err != nil {
		osrpWriteError(w, http.StatusBadRequest, "CREATE_LIVE_FAILED", err.Error())
		return
	}
	defer l.Close()

	streamInfos, err := l.GetStreamInfos()
	if err != nil {
		osrpWriteError(w, http.StatusInternalServerError, "GET_STREAMS_FAILED", err.Error())
		return
	}

	streams := make([]OSRPStreamURLInfo, 0, len(streamInfos))
	for _, s := range streamInfos {
		format := "flv"
		streamURL := ""
		if s.Url != nil {
			streamURL = s.Url.String()
			if strings.Contains(streamURL, ".m3u8") {
				format = "hls"
			}
		}

		streams = append(streams, OSRPStreamURLInfo{
			Quality:   s.Name,
			QualityID: s.Resolution,
			Format:    format,
			URL:       streamURL,
		})
	}

	osrpWriteSuccess(w, OSRPStreamURLsResponse{
		Streams: streams,
	})
}

// OSRPProbeRequest 探测请求
type OSRPProbeRequest struct {
	URL            string `json:"url"`
	IncludeStreams bool   `json:"include_streams"`
}

// OSRPProbeResponse 探测响应
type OSRPProbeResponse struct {
	Platform   string              `json:"platform"`
	StreamID   string              `json:"stream_id"`
	IsLive     bool                `json:"is_live"`
	Title      string              `json:"title"`
	AnchorName string              `json:"anchor_name"`
	Streams    []OSRPStreamURLInfo `json:"streams,omitempty"`
}

// osrpProbe POST /osrp/v1/probe
func osrpProbe(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var req OSRPProbeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		osrpWriteError(w, http.StatusBadRequest, "INVALID_REQUEST", "请求格式错误")
		return
	}

	if req.URL == "" {
		osrpWriteError(w, http.StatusBadRequest, "MISSING_URL", "缺少 URL 参数")
		return
	}

	room := &configs.LiveRoom{Url: req.URL}
	l, err := live.New(ctx, room, nil)
	if err != nil {
		osrpWriteError(w, http.StatusBadRequest, "RESOLVE_FAILED", err.Error())
		return
	}
	defer l.Close()

	info, err := l.GetInfo()
	if err != nil {
		osrpWriteError(w, http.StatusInternalServerError, "GET_INFO_FAILED", err.Error())
		return
	}

	resp := OSRPProbeResponse{
		Platform:   l.GetPlatformCNName(),
		StreamID:   string(l.GetLiveId()),
		IsLive:     info.Status,
		Title:      info.RoomName,
		AnchorName: info.HostName,
	}

	// 如果请求了流信息且正在直播
	if req.IncludeStreams && info.Status {
		streamInfos, err := l.GetStreamInfos()
		if err == nil {
			streams := make([]OSRPStreamURLInfo, 0, len(streamInfos))
			for _, s := range streamInfos {
				format := "flv"
				streamURL := ""
				if s.Url != nil {
					streamURL = s.Url.String()
					if strings.Contains(streamURL, ".m3u8") {
						format = "hls"
					}
				}
				streams = append(streams, OSRPStreamURLInfo{
					Quality:   s.Name,
					QualityID: s.Resolution,
					Format:    format,
					URL:       streamURL,
				})
			}
			resp.Streams = streams
		}
	}

	osrpWriteSuccess(w, resp)
}

// osrpBuildURLFromPlatform 根据平台和 ID 构造 URL
func osrpBuildURLFromPlatform(platform, streamID string) string {
	switch strings.ToLower(platform) {
	case "bilibili", "哔哩哔哩":
		return "https://live.bilibili.com/" + streamID
	case "douyin", "抖音":
		return "https://live.douyin.com/" + streamID
	case "huya", "虎牙":
		return "https://www.huya.com/" + streamID
	case "douyu", "斗鱼":
		return "https://www.douyu.com/" + streamID
	case "kuaishou", "快手":
		return "https://live.kuaishou.com/u/" + streamID
	case "twitch":
		return "https://www.twitch.tv/" + streamID
	case "youtube":
		return "https://www.youtube.com/watch?v=" + streamID
	case "langlive", "浪live":
		return "https://www.lang.live/room/" + streamID
	default:
		return ""
	}
}

// RegisterOSRPRoutes 注册 OSRP 路由
func RegisterOSRPRoutes(router *mux.Router, inst *instance.Instance) {
	osrp := router.PathPrefix("/osrp/v1").Subrouter()
	osrp.Use(mux.CORSMethodMiddleware(osrp))

	// 服务信息
	osrp.HandleFunc("/info", osrpGetInfo).Methods("GET", "OPTIONS")
	osrp.HandleFunc("/capabilities", osrpGetCapabilities).Methods("GET", "OPTIONS")

	// 任务管理
	osrp.HandleFunc("/tasks", osrpGetTasks).Methods("GET", "OPTIONS")
	osrp.HandleFunc("/tasks", osrpAddTask).Methods("POST", "OPTIONS")
	osrp.HandleFunc("/tasks/{id}", osrpGetTask).Methods("GET", "OPTIONS")
	osrp.HandleFunc("/tasks/{id}", osrpDeleteTask).Methods("DELETE", "OPTIONS")
	osrp.HandleFunc("/tasks/{id}/actions", osrpTaskAction).Methods("POST", "OPTIONS")

	// 解析 API
	osrp.HandleFunc("/resolve", osrpResolve).Methods("POST", "OPTIONS")
	osrp.HandleFunc("/streams/{platform}/{id}/status", osrpGetStreamStatus).Methods("GET", "OPTIONS")
	osrp.HandleFunc("/streams/{platform}/{id}/urls", osrpGetStreamURLs).Methods("GET", "OPTIONS")
	osrp.HandleFunc("/probe", osrpProbe).Methods("POST", "OPTIONS")
}
