package livestate

import (
	"context"
	"sync"
	"time"

	bilisentry "github.com/bililive-go/bililive-go/src/pkg/sentry"
	"github.com/sirupsen/logrus"
)

const (
	// 心跳间隔：每5秒更新一次
	heartbeatInterval = 5 * time.Second
)

// Manager 直播间状态管理器
type Manager struct {
	store           Store
	heartbeatTicker *time.Ticker
	ctx             context.Context
	cancel          context.CancelFunc
	recordingRooms  map[string]bool // 当前正在录制的直播间
	mu              sync.RWMutex
}

// NewManager 创建状态管理器
func NewManager(dbPath string) (*Manager, error) {
	store, err := NewSQLiteStore(dbPath)
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithCancel(context.Background())
	return &Manager{
		store:          store,
		ctx:            ctx,
		cancel:         cancel,
		recordingRooms: make(map[string]bool),
	}, nil
}

// Start 启动管理器（包括心跳更新goroutine）
func (m *Manager) Start() error {
	// 先进行崩溃恢复
	if err := m.RecoverFromCrash(); err != nil {
		logrus.WithError(err).Warn("崩溃恢复处理失败")
	}

	// 启动心跳更新
	m.heartbeatTicker = time.NewTicker(heartbeatInterval)
	bilisentry.Go(m.heartbeatLoop)

	logrus.Info("直播间状态管理器已启动")
	return nil
}

// heartbeatLoop 心跳更新循环
func (m *Manager) heartbeatLoop() {
	for {
		select {
		case <-m.ctx.Done():
			return
		case <-m.heartbeatTicker.C:
			m.updateHeartbeats()
		}
	}
}

// updateHeartbeats 更新所有正在录制的直播间的心跳时间
func (m *Manager) updateHeartbeats() {
	m.mu.RLock()
	rooms := make([]string, 0, len(m.recordingRooms))
	for liveID := range m.recordingRooms {
		rooms = append(rooms, liveID)
	}
	m.mu.RUnlock()

	now := time.Now()
	for _, liveID := range rooms {
		if err := m.store.UpdateHeartbeat(m.ctx, liveID, now); err != nil {
			logrus.WithError(err).WithField("live_id", liveID).Debug("更新心跳时间戳失败")
		}
	}
}

// Close 关闭管理器
func (m *Manager) Close() error {
	m.cancel()
	if m.heartbeatTicker != nil {
		m.heartbeatTicker.Stop()
	}
	logrus.Info("直播间状态管理器已关闭")
	return m.store.Close()
}

// RecoverFromCrash 程序启动时调用，处理崩溃恢复
func (m *Manager) RecoverFromCrash() error {
	// 获取之前标记为正在录制的直播间
	rooms, err := m.store.GetRecordingLiveRooms(m.ctx)
	if err != nil {
		return err
	}

	if len(rooms) == 0 {
		return nil
	}

	logrus.WithField("count", len(rooms)).Info("发现之前未正常关闭的录制会话，正在进行恢复处理")

	for _, room := range rooms {
		// 使用心跳时间作为结束时间来关闭未完成的会话
		if err := m.store.EndSessionByHeartbeat(m.ctx, room.LiveID, EndReasonCrash); err != nil {
			logrus.WithError(err).WithField("live_id", room.LiveID).Warn("关闭崩溃会话失败")
		}

		// 更新关播时间
		endTime := room.LastHeartbeat
		if endTime.IsZero() {
			endTime = time.Now()
		}
		if err := m.store.UpdateLiveEndTime(m.ctx, room.LiveID, endTime); err != nil {
			logrus.WithError(err).WithField("live_id", room.LiveID).Warn("更新关播时间失败")
		}

		// 重置录制状态
		if err := m.store.SetRecordingStatus(m.ctx, room.LiveID, false); err != nil {
			logrus.WithError(err).WithField("live_id", room.LiveID).Warn("重置录制状态失败")
		}

		logrus.WithFields(logrus.Fields{
			"live_id":        room.LiveID,
			"host_name":      room.HostName,
			"last_heartbeat": room.LastHeartbeat,
		}).Info("已恢复崩溃的录制会话")
	}

	return nil
}

// OnLiveStart 直播开始时调用
func (m *Manager) OnLiveStart(liveID, url, platform, hostName, roomName string) {
	now := time.Now()

	// 更新直播间信息
	room := &LiveRoom{
		LiveID:        liveID,
		URL:           url,
		Platform:      platform,
		HostName:      hostName,
		RoomName:      roomName,
		LastStartTime: now,
	}

	if err := m.store.UpsertLiveRoom(m.ctx, room); err != nil {
		logrus.WithError(err).WithField("live_id", liveID).Warn("保存直播间信息失败")
		return
	}

	// 开始新的会话（包含名称信息）
	if _, err := m.store.StartSession(m.ctx, liveID, hostName, roomName, now); err != nil {
		logrus.WithError(err).WithField("live_id", liveID).Warn("创建直播会话失败")
	}

	logrus.WithFields(logrus.Fields{
		"live_id":   liveID,
		"host_name": hostName,
		"room_name": roomName,
	}).Debug("记录直播开始")
}

// OnLiveEnd 直播结束时调用
func (m *Manager) OnLiveEnd(liveID string) {
	m.OnLiveEndWithReason(liveID, EndReasonNormal)
}

// OnLiveEndWithReason 直播结束时调用（指定结束原因）
func (m *Manager) OnLiveEndWithReason(liveID string, reason string) {
	now := time.Now()

	// 更新关播时间
	if err := m.store.UpdateLiveEndTime(m.ctx, liveID, now); err != nil {
		logrus.WithError(err).WithField("live_id", liveID).Warn("更新关播时间失败")
	}

	// 结束当前会话
	if err := m.store.EndSession(m.ctx, liveID, now, reason); err != nil {
		logrus.WithError(err).WithField("live_id", liveID).Warn("结束直播会话失败")
	}

	logrus.WithFields(logrus.Fields{
		"live_id": liveID,
		"reason":  reason,
	}).Debug("记录直播结束")
}

// OnUserStopMonitoring 用户停止监控时调用
func (m *Manager) OnUserStopMonitoring(liveID string) {
	m.OnLiveEndWithReason(liveID, EndReasonUserStop)
}

// OnRecordingStart 录制开始时调用
func (m *Manager) OnRecordingStart(liveID string) {
	m.mu.Lock()
	m.recordingRooms[liveID] = true
	m.mu.Unlock()

	// 设置录制状态并更新心跳
	now := time.Now()
	if err := m.store.SetRecordingStatus(m.ctx, liveID, true); err != nil {
		logrus.WithError(err).WithField("live_id", liveID).Warn("设置录制状态失败")
	}
	if err := m.store.UpdateHeartbeat(m.ctx, liveID, now); err != nil {
		logrus.WithError(err).WithField("live_id", liveID).Warn("更新心跳时间戳失败")
	}

	logrus.WithField("live_id", liveID).Debug("记录开始录制")
}

// OnRecordingStop 录制结束时调用
func (m *Manager) OnRecordingStop(liveID string) {
	m.mu.Lock()
	delete(m.recordingRooms, liveID)
	m.mu.Unlock()

	// 清除录制状态
	if err := m.store.SetRecordingStatus(m.ctx, liveID, false); err != nil {
		logrus.WithError(err).WithField("live_id", liveID).Warn("清除录制状态失败")
	}

	logrus.WithField("live_id", liveID).Debug("记录停止录制")
}

// UpdateInfo 更新直播间信息（检测名称变更）
func (m *Manager) UpdateInfo(liveID, url, platform, hostName, roomName string) {
	// 先获取现有信息
	existing, err := m.store.GetLiveRoom(m.ctx, liveID)
	if err == ErrLiveRoomNotFound {
		// 直播间不存在，直接创建
		room := &LiveRoom{
			LiveID:   liveID,
			URL:      url,
			Platform: platform,
			HostName: hostName,
			RoomName: roomName,
		}
		if err := m.store.UpsertLiveRoom(m.ctx, room); err != nil {
			logrus.WithError(err).WithField("live_id", liveID).Warn("创建直播间信息失败")
		}
		return
	}
	if err != nil {
		logrus.WithError(err).WithField("live_id", liveID).Debug("获取直播间信息失败")
		return
	}

	// 检测主播名变更
	if hostName != "" && existing.HostName != "" && existing.HostName != hostName {
		if err := m.store.RecordNameChange(m.ctx, liveID, NameTypeHost, existing.HostName, hostName); err != nil {
			logrus.WithError(err).WithField("live_id", liveID).Warn("记录主播名变更失败")
		} else {
			logrus.WithFields(logrus.Fields{
				"live_id":      liveID,
				"old_hostname": existing.HostName,
				"new_hostname": hostName,
			}).Info("检测到主播名变更")
		}
	}

	// 检测房间名变更
	if roomName != "" && existing.RoomName != "" && existing.RoomName != roomName {
		if err := m.store.RecordNameChange(m.ctx, liveID, NameTypeRoom, existing.RoomName, roomName); err != nil {
			logrus.WithError(err).WithField("live_id", liveID).Warn("记录房间名变更失败")
		} else {
			logrus.WithFields(logrus.Fields{
				"live_id":       liveID,
				"old_room_name": existing.RoomName,
				"new_room_name": roomName,
			}).Debug("检测到房间名变更")
		}
	}

	// 更新直播间信息
	if err := m.store.UpdateLiveInfo(m.ctx, liveID, hostName, roomName); err != nil {
		logrus.WithError(err).WithField("live_id", liveID).Warn("更新直播间信息失败")
	}
}

// GetCachedInfo 获取缓存的直播间信息（用于启动时快速恢复）
func (m *Manager) GetCachedInfo(liveID string) *LiveRoom {
	room, err := m.store.GetLiveRoom(m.ctx, liveID)
	if err != nil {
		return nil
	}
	return room
}

// GetAllCachedRooms 获取所有缓存的直播间信息
func (m *Manager) GetAllCachedRooms() []*LiveRoom {
	rooms, err := m.store.GetAllLiveRooms(m.ctx)
	if err != nil {
		logrus.WithError(err).Warn("获取所有缓存直播间失败")
		return nil
	}
	return rooms
}

// GetPreviouslyRecordingRooms 获取之前正在录制的直播间（用于优先恢复）
func (m *Manager) GetPreviouslyRecordingRooms() []*LiveRoom {
	rooms, err := m.store.GetRecordingLiveRooms(m.ctx)
	if err != nil {
		logrus.WithError(err).Warn("获取之前录制中的直播间失败")
		return nil
	}
	return rooms
}

// GetSessionHistory 获取直播间的会话历史
func (m *Manager) GetSessionHistory(liveID string, limit int) []*LiveSession {
	sessions, err := m.store.GetSessionsByLiveID(m.ctx, liveID, limit)
	if err != nil {
		logrus.WithError(err).WithField("live_id", liveID).Warn("获取会话历史失败")
		return nil
	}
	return sessions
}

// GetNameHistory 获取直播间的名称变更历史
func (m *Manager) GetNameHistory(liveID string, limit int) []*NameChange {
	changes, err := m.store.GetNameHistory(m.ctx, liveID, limit)
	if err != nil {
		logrus.WithError(err).WithField("live_id", liveID).Warn("获取名称变更历史失败")
		return nil
	}
	return changes
}

// GetStore 获取底层存储（用于测试或高级操作）
func (m *Manager) GetStore() Store {
	return m.store
}
