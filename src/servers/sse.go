package servers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/bililive-go/bililive-go/src/instance"
	"github.com/bililive-go/bililive-go/src/live"
	"github.com/bililive-go/bililive-go/src/pipeline"
	"github.com/bililive-go/bililive-go/src/pkg/events"
	"github.com/bililive-go/bililive-go/src/types"
)

// SSEEventType SSE 事件类型
type SSEEventType string

const (
	// SSEEventLiveUpdate 直播间状态更新
	SSEEventLiveUpdate SSEEventType = "live_update"
	// SSEEventLog 日志更新
	SSEEventLog SSEEventType = "log"
	// SSEEventConnStats 连接统计更新
	SSEEventConnStats SSEEventType = "conn_stats"
	// SSEEventRecorderStatus 录制器状态更新（包含下载速度等）
	SSEEventRecorderStatus SSEEventType = "recorder_status"
	// SSEEventListChange 直播间列表变更（增删、监控开关等）
	SSEEventListChange SSEEventType = "list_change"
	// SSEEventRateLimitUpdate 频率限制信息更新
	SSEEventRateLimitUpdate SSEEventType = "rate_limit_update"
	// SSEEventPipelineTaskUpdate Pipeline 任务更新
	SSEEventPipelineTaskUpdate SSEEventType = "pipeline_task_update"
)

// SSEMessage SSE 消息结构
type SSEMessage struct {
	Type   SSEEventType `json:"type"`
	RoomID string       `json:"room_id"`
	Data   interface{}  `json:"data"`
}

// SSEHub 管理所有 SSE 连接
type SSEHub struct {
	mu      sync.RWMutex
	clients map[chan SSEMessage]struct{}
	closeCh chan struct{} // 关闭信号 channel
	closed  bool
}

var (
	sseHub     *SSEHub
	sseHubOnce sync.Once
)

// GetSSEHub 获取全局 SSE Hub 单例
func GetSSEHub() *SSEHub {
	sseHubOnce.Do(func() {
		sseHub = &SSEHub{
			clients: make(map[chan SSEMessage]struct{}),
			closeCh: make(chan struct{}),
		}
	})
	return sseHub
}

// AddClient 添加一个 SSE 客户端
func (h *SSEHub) AddClient(ch chan SSEMessage) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.clients[ch] = struct{}{}
}

// RemoveClient 移除一个 SSE 客户端
func (h *SSEHub) RemoveClient(ch chan SSEMessage) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if _, ok := h.clients[ch]; ok {
		delete(h.clients, ch)
		close(ch)
	}
}

// Broadcast 向所有客户端广播消息
func (h *SSEHub) Broadcast(msg SSEMessage) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	for ch := range h.clients {
		select {
		case ch <- msg:
		default:
			// 如果 channel 满了，跳过这条消息（避免阻塞）
		}
	}
}

// BroadcastLiveUpdate 广播直播间状态更新
func (h *SSEHub) BroadcastLiveUpdate(roomID types.LiveID, data interface{}) {
	h.Broadcast(SSEMessage{
		Type:   SSEEventLiveUpdate,
		RoomID: string(roomID),
		Data:   data,
	})
}

// BroadcastLog 广播日志更新
func (h *SSEHub) BroadcastLog(roomID types.LiveID, logLine string) {
	h.Broadcast(SSEMessage{
		Type:   SSEEventLog,
		RoomID: string(roomID),
		Data:   logLine,
	})
}

// BroadcastConnStats 广播连接统计更新
func (h *SSEHub) BroadcastConnStats(roomID types.LiveID, stats interface{}) {
	h.Broadcast(SSEMessage{
		Type:   SSEEventConnStats,
		RoomID: string(roomID),
		Data:   stats,
	})
}

// BroadcastRecorderStatus 广播录制器状态更新
func (h *SSEHub) BroadcastRecorderStatus(roomID types.LiveID, status interface{}) {
	h.Broadcast(SSEMessage{
		Type:   SSEEventRecorderStatus,
		RoomID: string(roomID),
		Data:   status,
	})
}

// BroadcastListChange 广播直播间列表变更
func (h *SSEHub) BroadcastListChange(roomID types.LiveID, changeType string, data interface{}) {
	h.Broadcast(SSEMessage{
		Type:   SSEEventListChange,
		RoomID: string(roomID),
		Data: map[string]interface{}{
			"change_type": changeType,
			"data":        data,
		},
	})
}

// BroadcastRateLimitUpdate 广播频率限制信息更新
func (h *SSEHub) BroadcastRateLimitUpdate(roomID types.LiveID, data interface{}) {
	h.Broadcast(SSEMessage{
		Type:   SSEEventRateLimitUpdate,
		RoomID: string(roomID),
		Data:   data,
	})
}

// BroadcastPipelineTaskUpdate 广播 Pipeline 任务更新
func (h *SSEHub) BroadcastPipelineTaskUpdate(task *pipeline.PipelineTask) {
	h.Broadcast(SSEMessage{
		Type:   SSEEventPipelineTaskUpdate,
		RoomID: string(task.RecordInfo.LiveID),
		Data:   task,
	})
}

// ClientCount 获取当前连接的客户端数量
func (h *SSEHub) ClientCount() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.clients)
}

// Close 关闭所有 SSE 连接
// 这会触发所有 sseHandler 退出
func (h *SSEHub) Close() {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return
	}
	h.closed = true
	close(h.closeCh)
	// 关闭所有客户端 channel
	for ch := range h.clients {
		close(ch)
		delete(h.clients, ch)
	}
}

// Done 返回关闭信号 channel
func (h *SSEHub) Done() <-chan struct{} {
	return h.closeCh
}

// sseHandler 处理 SSE 连接请求
func sseHandler(w http.ResponseWriter, r *http.Request) {
	// 设置 SSE 必需的响应头
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	// 获取 Flusher 接口
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "SSE not supported", http.StatusInternalServerError)
		return
	}

	// 创建客户端 channel（缓冲 100 条消息）
	clientCh := make(chan SSEMessage, 100)
	hub := GetSSEHub()
	hub.AddClient(clientCh)

	// 发送初始连接成功消息
	fmt.Fprintf(w, "event: connected\ndata: {\"message\":\"SSE connected\",\"clients\":%d}\n\n", hub.ClientCount())
	flusher.Flush()

	// 启动心跳 goroutine
	heartbeatTicker := time.NewTicker(30 * time.Second)
	defer heartbeatTicker.Stop()

	// 监听客户端断开和服务器关闭
	ctx := r.Context()

	for {
		select {
		case <-ctx.Done():
			// 客户端断开连接
			hub.RemoveClient(clientCh)
			return

		case <-hub.Done():
			// 服务器关闭，强制退出
			return

		case <-heartbeatTicker.C:
			// 发送心跳消息
			fmt.Fprintf(w, ":heartbeat\n\n")
			flusher.Flush()

		case msg, ok := <-clientCh:
			if !ok {
				// channel 被关闭（服务器关闭时）
				return
			}
			// 序列化消息
			data, err := json.Marshal(msg)
			if err != nil {
				continue
			}
			// 发送 SSE 消息
			fmt.Fprintf(w, "event: %s\ndata: %s\n\n", msg.Type, data)
			flusher.Flush()
		}
	}
}

// SSEEventListener 创建 SSE 事件监听器
// 监听系统事件并通过 SSE 广播
type SSEEventListener struct {
	hub *SSEHub
}

// NewSSEEventListener 创建新的 SSE 事件监听器
func NewSSEEventListener() *SSEEventListener {
	return &SSEEventListener{
		hub: GetSSEHub(),
	}
}

// HandleEvent 处理事件
func (l *SSEEventListener) HandleEvent(event *events.Event) {
	if event == nil || event.Object == nil {
		return
	}

	// 从事件对象中获取 LiveID
	type liveIDProvider interface {
		GetLiveId() types.LiveID
	}

	provider, ok := event.Object.(liveIDProvider)
	if !ok {
		return
	}

	roomID := provider.GetLiveId()

	// 根据事件类型广播不同的消息
	l.hub.BroadcastLiveUpdate(roomID, map[string]interface{}{
		"event_type": string(event.Type),
		"timestamp":  time.Now().Unix(),
	})
}

// RegisterSSEEventListeners 注册 SSE 事件监听器到 EventDispatcher
func RegisterSSEEventListeners(inst *instance.Instance) {
	if inst == nil || inst.EventDispatcher == nil {
		return
	}

	listener := NewSSEEventListener()
	handler := events.NewEventListener(listener.HandleEvent)

	// 监听所有相关事件
	dispatcher := inst.EventDispatcher.(events.Dispatcher)

	// 导入事件类型常量
	// 这些事件在 listeners/event.go 和 recorders/event.go 中定义
	eventTypes := []events.EventType{
		"ListenStart",
		"ListenStop",
		"LiveStart",
		"LiveEnd",
		"RoomNameChanged",
		"RoomInitializingFinished",
		"RecorderStart", // 录制开始
		"RecorderStop",  // 录制结束
	}

	for _, eventType := range eventTypes {
		dispatcher.AddEventListener(eventType, handler)
	}

	// 注册调度器刷新完成的回调（使用回调方式避免循环依赖）
	live.SetSchedulerRefreshCallback(func(liveObj live.Live, status live.SchedulerStatus) {
		hub := GetSSEHub()
		hub.BroadcastRateLimitUpdate(liveObj.GetLiveId(), map[string]interface{}{
			"event_type":       string(live.SchedulerRefreshCompleted),
			"scheduler_status": status,
			"timestamp":        time.Now().Unix(),
		})
	})

	// 注册 Pipeline 任务更新事件监听器
	pipelineHandler := events.NewEventListener(func(event *events.Event) {
		if event == nil || event.Object == nil {
			return
		}
		if task, ok := event.Object.(*pipeline.PipelineTask); ok {
			hub := GetSSEHub()
			hub.BroadcastPipelineTaskUpdate(task)
		}
	})
	dispatcher.AddEventListener(pipeline.PipelineTaskUpdateEvent, pipelineHandler)
}
