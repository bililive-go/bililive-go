package livestate

import (
	"context"

	"github.com/bililive-go/bililive-go/src/listeners"
	"github.com/bililive-go/bililive-go/src/live"
	"github.com/bililive-go/bililive-go/src/pkg/events"
	"github.com/bililive-go/bililive-go/src/recorders"
	"github.com/bililive-go/bililive-go/src/types"
	"github.com/bluele/gcache"
	"github.com/sirupsen/logrus"
)

type listenerSourceLookup interface {
	GetListener(ctx context.Context, liveID types.LiveID) (listeners.Listener, error)
}

type recorderSourceLookup interface {
	GetRecorder(ctx context.Context, liveID types.LiveID) (recorders.Recorder, error)
}

// liveEndSourceReplaced 判断 LiveEnd 是否来自已被同类新实例替换的来源。
//
// 不能只检查来源是否已关闭：listener 在关闭前确认并派发的 LiveEnd 可能晚于
// CloseSync 执行，此时只要尚未出现替代实例，该事件仍应结束当前直播会话。
// recorder 也可能在 recorder manager 处理同一个 LiveEnd 时先被关闭，因此需要
// 比较当前实例身份，而不是把“已关闭”等同于“已失效”。
func liveEndSourceReplaced(
	ctx context.Context,
	event *events.Event,
	liveID types.LiveID,
	listenerManager listenerSourceLookup,
	recorderManager recorderSourceLookup,
) bool {
	if event == nil || event.Source == nil {
		return false
	}
	switch source := event.Source.(type) {
	case listeners.Listener:
		if listenerManager == nil {
			return false
		}
		current, err := listenerManager.GetListener(ctx, liveID)
		return err == nil && current != source
	case recorders.Recorder:
		if recorderManager == nil {
			return false
		}
		current, err := recorderManager.GetRecorder(ctx, liveID)
		return err == nil && current != source
	default:
		return false
	}
}

// RegisterEventListeners 注册事件监听器，用于在直播状态变化时更新数据库
// 参数：
//   - ctx: 应用级 context
//   - ed: 事件分发器
//   - manager: LiveStateManager 实例
//   - cache: 缓存实例，用于获取直播间信息
//   - listenerManager: listener 当前实例查询器
//   - recorderManager: recorder 当前实例查询器
func RegisterEventListeners(
	ctx context.Context,
	ed events.Dispatcher,
	manager *Manager,
	cache gcache.Cache,
	listenerManager listeners.Manager,
	recorderManager recorders.Manager,
) {
	if manager == nil {
		logrus.Debug("LiveStateManager 未初始化，跳过事件监听器注册")
		return
	}

	// 监听直播开始事件
	ed.AddEventListener(listeners.LiveStart, events.NewEventListener(func(event *events.Event) {
		if events.SourceClosed(event) {
			return
		}
		l, ok := event.Object.(live.Live)
		if !ok {
			return
		}

		// 获取直播间信息
		liveID := string(l.GetLiveId())
		url := l.GetRawUrl()
		platform := l.GetPlatformCNName()
		hostName := ""
		roomName := ""

		// 尝试从缓存获取更多信息
		if cache != nil {
			if info, err := cache.Get(l); err == nil {
				if liveInfo, ok := info.(*live.Info); ok {
					hostName = liveInfo.HostName
					roomName = liveInfo.RoomName
				}
			}
		}

		manager.OnLiveStart(liveID, url, platform, hostName, roomName)
	}))

	// 监听直播结束事件
	ed.AddEventListener(listeners.LiveEnd, events.NewEventListener(func(event *events.Event) {
		l, ok := event.Object.(live.Live)
		if !ok {
			return
		}

		liveID := l.GetLiveId()
		if liveEndSourceReplaced(ctx, event, liveID, listenerManager, recorderManager) {
			return
		}
		manager.OnLiveEnd(string(liveID))
	}))

	// 监听录制开始事件
	ed.AddEventListener(recorders.RecorderStart, events.NewEventListener(func(event *events.Event) {
		l, ok := event.Object.(live.Live)
		if !ok {
			return
		}

		liveID := string(l.GetLiveId())
		manager.OnRecordingStart(liveID)
	}))

	// 监听录制结束事件
	ed.AddEventListener(recorders.RecorderStop, events.NewEventListener(func(event *events.Event) {
		l, ok := event.Object.(live.Live)
		if !ok {
			return
		}

		liveID := string(l.GetLiveId())
		manager.OnRecordingStop(liveID)
	}))

	// 监听直播间初始化完成事件（用于保存初始信息）
	ed.AddEventListener(listeners.RoomInitializingFinished, events.NewEventListener(func(event *events.Event) {
		param, ok := event.Object.(live.InitializingFinishedParam)
		if !ok {
			return
		}

		liveID := string(param.GetLiveId())
		if liveID == "" {
			return
		}

		var l live.Live
		if param.InitializingLive != nil {
			l = param.InitializingLive
		} else if param.Live != nil {
			l = param.Live
		}

		if l == nil {
			return
		}

		url := l.GetRawUrl()
		platform := l.GetPlatformCNName()
		hostName := ""
		roomName := ""

		if param.Info != nil {
			hostName = param.Info.HostName
			roomName = param.Info.RoomName
		}

		// 更新直播间信息（会自动检测名称变更）
		manager.UpdateInfo(liveID, url, platform, hostName, roomName)
	}))

	logrus.Info("直播间状态持久化事件监听器已注册")
}
