package livestate

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	_ "modernc.org/sqlite"

	"github.com/bililive-go/bililive-go/src/consts"
	"github.com/bililive-go/bililive-go/src/pkg/migration"
	"github.com/sirupsen/logrus"
)

var (
	// ErrLiveRoomNotFound 直播间不存在
	ErrLiveRoomNotFound = errors.New("live room not found")
	// ErrSessionNotFound 会话不存在
	ErrSessionNotFound = errors.New("session not found")
)

// Store 直播间状态存储接口
type Store interface {
	// 直播间基本信息
	UpsertLiveRoom(ctx context.Context, room *LiveRoom) error
	GetLiveRoom(ctx context.Context, liveID string) (*LiveRoom, error)
	GetAllLiveRooms(ctx context.Context) ([]*LiveRoom, error)
	GetRecordingLiveRooms(ctx context.Context) ([]*LiveRoom, error)
	UpdateHeartbeat(ctx context.Context, liveID string, timestamp time.Time) error
	SetRecordingStatus(ctx context.Context, liveID string, isRecording bool) error
	UpdateLiveInfo(ctx context.Context, liveID, hostName, roomName string) error
	UpdateLiveStartTime(ctx context.Context, liveID string, startTime time.Time) error
	UpdateLiveEndTime(ctx context.Context, liveID string, endTime time.Time) error

	// 直播会话
	StartSession(ctx context.Context, liveID, hostName, roomName string, startTime time.Time) (int64, error)
	EndSession(ctx context.Context, liveID string, endTime time.Time, reason string) error
	EndSessionByHeartbeat(ctx context.Context, liveID string, reason string) error
	GetOpenSessions(ctx context.Context) ([]*LiveSession, error)
	GetSessionsByLiveID(ctx context.Context, liveID string, limit int) ([]*LiveSession, error)

	// 名称变更历史
	RecordNameChange(ctx context.Context, liveID, nameType, oldValue, newValue string) error
	GetNameHistory(ctx context.Context, liveID string, limit int) ([]*NameChange, error)

	// 可用流信息
	SaveAvailableStreams(ctx context.Context, liveID string, streams []*AvailableStream) error
	GetAvailableStreams(ctx context.Context, liveID string) ([]*AvailableStream, error)
	// SaveAvailableStreamsAny 通用接口，避免循环导入（接收 []map[string]interface{} 类型）
	SaveAvailableStreamsAny(ctx context.Context, liveID string, streams interface{}) error

	// 生命周期
	Close() error
}

// SQLiteStore SQLite存储实现
type SQLiteStore struct {
	db     *sql.DB
	dbPath string
	mu     sync.RWMutex
}

// NewSQLiteStore 创建SQLite存储
func NewSQLiteStore(dbPath string) (*SQLiteStore, error) {
	// 确保目录存在
	if err := os.MkdirAll(filepath.Dir(dbPath), 0755); err != nil {
		return nil, fmt.Errorf("创建数据库目录失败: %w", err)
	}

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("打开数据库失败: %w", err)
	}

	store := &SQLiteStore{
		db:     db,
		dbPath: dbPath,
	}

	// 运行数据库迁移
	if err := store.runMigrations(); err != nil {
		db.Close()
		return nil, fmt.Errorf("运行数据库迁移失败: %w", err)
	}

	// 更新版本信息
	if err := store.updateVersionInfo(); err != nil {
		db.Close()
		return nil, fmt.Errorf("更新版本信息失败: %w", err)
	}

	return store, nil
}

// runMigrations 运行数据库迁移
func (s *SQLiteStore) runMigrations() error {
	config := &migration.MigrationConfig{
		DBPath: s.dbPath,
		Schema: LiveStateDatabaseSchema,
		DB:     s.db,
	}

	migrator, err := migration.NewMigrator(config)
	if err != nil {
		return fmt.Errorf("创建迁移器失败: %w", err)
	}

	// 检查是否需要从上次失败的迁移中恢复
	recovered, err := migrator.CheckAndRecover()
	if err != nil {
		logrus.WithError(err).Warn("迁移恢复检查失败")
	}
	if recovered {
		logrus.Info("从未完成的迁移中恢复")
		s.db.Close()
		db, err := sql.Open("sqlite", s.dbPath)
		if err != nil {
			return fmt.Errorf("恢复后重新打开数据库失败: %w", err)
		}
		s.db = db
		config.DB = s.db
		migrator, err = migration.NewMigrator(config)
		if err != nil {
			return fmt.Errorf("恢复后重新创建迁移器失败: %w", err)
		}
	}

	// 执行迁移
	result, err := migrator.Run()
	if err != nil {
		return fmt.Errorf("迁移失败: %w", err)
	}

	if result.BackupPath != "" {
		logrus.WithField("backup_path", result.BackupPath).Debug("已创建数据库备份")
	}

	return nil
}

// updateVersionInfo 更新版本信息到 system_meta 表
func (s *SQLiteStore) updateVersionInfo() error {
	appVersion := consts.AppVersion

	var oldVersion string
	err := s.db.QueryRow("SELECT value FROM system_meta WHERE key = 'app_version'").Scan(&oldVersion)

	if err == sql.ErrNoRows {
		// 首次运行，插入版本信息
		_, err = s.db.Exec(`
			INSERT INTO system_meta (key, value) VALUES 
			('app_version', ?),
			('min_compatible_version', ?)
		`, appVersion, appVersion)
		if err != nil {
			return err
		}
		logrus.WithField("version", appVersion).Info("初始化直播间状态数据库版本信息")
		return nil
	}

	if err != nil {
		return err
	}

	// 更新版本信息
	_, err = s.db.Exec(`
		UPDATE system_meta SET value = ?, updated_at = CURRENT_TIMESTAMP 
		WHERE key = 'app_version'
	`, appVersion)

	if oldVersion != appVersion {
		logrus.WithFields(logrus.Fields{
			"old_version": oldVersion,
			"new_version": appVersion,
		}).Info("更新了直播间状态数据库版本信息")
	}

	return err
}

// UpsertLiveRoom 创建或更新直播间信息
func (s *SQLiteStore) UpsertLiveRoom(ctx context.Context, room *LiveRoom) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	lastStartTime := int64(0)
	lastEndTime := int64(0)
	lastHeartbeat := int64(0)
	isRecording := 0

	if !room.LastStartTime.IsZero() {
		lastStartTime = room.LastStartTime.Unix()
	}
	if !room.LastEndTime.IsZero() {
		lastEndTime = room.LastEndTime.Unix()
	}
	if !room.LastHeartbeat.IsZero() {
		lastHeartbeat = room.LastHeartbeat.Unix()
	}
	if room.IsRecording {
		isRecording = 1
	}

	_, err := s.db.ExecContext(ctx, `
		INSERT INTO live_rooms (live_id, url, platform, host_name, room_name, last_start_time, last_end_time, is_recording, last_heartbeat)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(live_id) DO UPDATE SET
			url = excluded.url,
			platform = excluded.platform,
			host_name = CASE WHEN excluded.host_name != '' THEN excluded.host_name ELSE live_rooms.host_name END,
			room_name = CASE WHEN excluded.room_name != '' THEN excluded.room_name ELSE live_rooms.room_name END,
			last_start_time = CASE WHEN excluded.last_start_time > 0 THEN excluded.last_start_time ELSE live_rooms.last_start_time END,
			last_end_time = CASE WHEN excluded.last_end_time > 0 THEN excluded.last_end_time ELSE live_rooms.last_end_time END,
			is_recording = excluded.is_recording,
			last_heartbeat = CASE WHEN excluded.last_heartbeat > 0 THEN excluded.last_heartbeat ELSE live_rooms.last_heartbeat END,
			updated_at = CURRENT_TIMESTAMP
	`, room.LiveID, room.URL, room.Platform, room.HostName, room.RoomName, lastStartTime, lastEndTime, isRecording, lastHeartbeat)

	return err
}

// GetLiveRoom 获取直播间信息
func (s *SQLiteStore) GetLiveRoom(ctx context.Context, liveID string) (*LiveRoom, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	room := &LiveRoom{}
	var lastStartTime, lastEndTime, lastHeartbeat int64
	var isRecording int

	err := s.db.QueryRowContext(ctx, `
		SELECT id, live_id, url, platform, host_name, room_name, last_start_time, last_end_time, is_recording, last_heartbeat, created_at, updated_at
		FROM live_rooms WHERE live_id = ?
	`, liveID).Scan(
		&room.ID, &room.LiveID, &room.URL, &room.Platform, &room.HostName, &room.RoomName,
		&lastStartTime, &lastEndTime, &isRecording, &lastHeartbeat, &room.CreatedAt, &room.UpdatedAt,
	)
	if err == sql.ErrNoRows {
		return nil, ErrLiveRoomNotFound
	}
	if err != nil {
		return nil, err
	}

	if lastStartTime > 0 {
		room.LastStartTime = time.Unix(lastStartTime, 0)
	}
	if lastEndTime > 0 {
		room.LastEndTime = time.Unix(lastEndTime, 0)
	}
	if lastHeartbeat > 0 {
		room.LastHeartbeat = time.Unix(lastHeartbeat, 0)
	}
	room.IsRecording = isRecording == 1

	return room, nil
}

// GetAllLiveRooms 获取所有直播间信息
func (s *SQLiteStore) GetAllLiveRooms(ctx context.Context) ([]*LiveRoom, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	rows, err := s.db.QueryContext(ctx, `
		SELECT id, live_id, url, platform, host_name, room_name, last_start_time, last_end_time, is_recording, last_heartbeat, created_at, updated_at
		FROM live_rooms ORDER BY updated_at DESC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	return s.scanLiveRooms(rows)
}

// GetRecordingLiveRooms 获取之前正在录制的直播间
func (s *SQLiteStore) GetRecordingLiveRooms(ctx context.Context) ([]*LiveRoom, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	rows, err := s.db.QueryContext(ctx, `
		SELECT id, live_id, url, platform, host_name, room_name, last_start_time, last_end_time, is_recording, last_heartbeat, created_at, updated_at
		FROM live_rooms WHERE is_recording = 1 ORDER BY last_heartbeat DESC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	return s.scanLiveRooms(rows)
}

// scanLiveRooms 从 rows 扫描直播间列表
func (s *SQLiteStore) scanLiveRooms(rows *sql.Rows) ([]*LiveRoom, error) {
	var rooms []*LiveRoom
	for rows.Next() {
		room := &LiveRoom{}
		var lastStartTime, lastEndTime, lastHeartbeat int64
		var isRecording int

		err := rows.Scan(
			&room.ID, &room.LiveID, &room.URL, &room.Platform, &room.HostName, &room.RoomName,
			&lastStartTime, &lastEndTime, &isRecording, &lastHeartbeat, &room.CreatedAt, &room.UpdatedAt,
		)
		if err != nil {
			return nil, err
		}

		if lastStartTime > 0 {
			room.LastStartTime = time.Unix(lastStartTime, 0)
		}
		if lastEndTime > 0 {
			room.LastEndTime = time.Unix(lastEndTime, 0)
		}
		if lastHeartbeat > 0 {
			room.LastHeartbeat = time.Unix(lastHeartbeat, 0)
		}
		room.IsRecording = isRecording == 1

		rooms = append(rooms, room)
	}
	return rooms, nil
}

// UpdateHeartbeat 更新录制心跳时间戳
func (s *SQLiteStore) UpdateHeartbeat(ctx context.Context, liveID string, timestamp time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	_, err := s.db.ExecContext(ctx, `
		UPDATE live_rooms SET last_heartbeat = ?, updated_at = CURRENT_TIMESTAMP WHERE live_id = ?
	`, timestamp.Unix(), liveID)
	return err
}

// SetRecordingStatus 设置录制状态
func (s *SQLiteStore) SetRecordingStatus(ctx context.Context, liveID string, isRecording bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	val := 0
	if isRecording {
		val = 1
	}
	_, err := s.db.ExecContext(ctx, `
		UPDATE live_rooms SET is_recording = ?, updated_at = CURRENT_TIMESTAMP WHERE live_id = ?
	`, val, liveID)
	return err
}

// UpdateLiveInfo 更新直播间信息（主播名、房间名）
func (s *SQLiteStore) UpdateLiveInfo(ctx context.Context, liveID, hostName, roomName string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	_, err := s.db.ExecContext(ctx, `
		UPDATE live_rooms SET 
			host_name = CASE WHEN ? != '' THEN ? ELSE host_name END,
			room_name = CASE WHEN ? != '' THEN ? ELSE room_name END,
			updated_at = CURRENT_TIMESTAMP 
		WHERE live_id = ?
	`, hostName, hostName, roomName, roomName, liveID)
	return err
}

// UpdateLiveStartTime 更新开播时间
func (s *SQLiteStore) UpdateLiveStartTime(ctx context.Context, liveID string, startTime time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	_, err := s.db.ExecContext(ctx, `
		UPDATE live_rooms SET last_start_time = ?, updated_at = CURRENT_TIMESTAMP WHERE live_id = ?
	`, startTime.Unix(), liveID)
	return err
}

// UpdateLiveEndTime 更新关播时间
func (s *SQLiteStore) UpdateLiveEndTime(ctx context.Context, liveID string, endTime time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	_, err := s.db.ExecContext(ctx, `
		UPDATE live_rooms SET last_end_time = ?, updated_at = CURRENT_TIMESTAMP WHERE live_id = ?
	`, endTime.Unix(), liveID)
	return err
}

// StartSession 开始一个新的直播会话
func (s *SQLiteStore) StartSession(ctx context.Context, liveID, hostName, roomName string, startTime time.Time) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	result, err := s.db.ExecContext(ctx, `
		INSERT INTO live_sessions (live_id, host_name, room_name, start_time) VALUES (?, ?, ?, ?)
	`, liveID, hostName, roomName, startTime.Unix())
	if err != nil {
		return 0, err
	}
	return result.LastInsertId()
}

// EndSession 结束当前打开的直播会话
func (s *SQLiteStore) EndSession(ctx context.Context, liveID string, endTime time.Time, reason string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	_, err := s.db.ExecContext(ctx, `
		UPDATE live_sessions SET end_time = ?, end_reason = ? 
		WHERE live_id = ? AND end_time = 0
	`, endTime.Unix(), reason, liveID)
	return err
}

// EndSessionByHeartbeat 使用心跳时间作为结束时间来结束会话（用于崩溃恢复）
func (s *SQLiteStore) EndSessionByHeartbeat(ctx context.Context, liveID string, reason string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// 获取直播间的最后心跳时间
	var lastHeartbeat int64
	err := s.db.QueryRowContext(ctx, `
		SELECT last_heartbeat FROM live_rooms WHERE live_id = ?
	`, liveID).Scan(&lastHeartbeat)
	if err != nil {
		return err
	}

	// 如果心跳时间为0，使用当前时间
	if lastHeartbeat == 0 {
		lastHeartbeat = time.Now().Unix()
	}

	_, err = s.db.ExecContext(ctx, `
		UPDATE live_sessions SET end_time = ?, end_reason = ? 
		WHERE live_id = ? AND end_time = 0
	`, lastHeartbeat, reason, liveID)
	return err
}

// GetOpenSessions 获取所有未结束的会话
func (s *SQLiteStore) GetOpenSessions(ctx context.Context) ([]*LiveSession, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	rows, err := s.db.QueryContext(ctx, `
		SELECT id, live_id, start_time, end_time, end_reason, created_at
		FROM live_sessions WHERE end_time = 0
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	return s.scanSessions(rows)
}

// GetSessionsByLiveID 获取指定直播间的会话历史
func (s *SQLiteStore) GetSessionsByLiveID(ctx context.Context, liveID string, limit int) ([]*LiveSession, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	query := `
		SELECT id, live_id, host_name, room_name, start_time, end_time, end_reason, created_at
		FROM live_sessions WHERE live_id = ? ORDER BY start_time DESC
	`
	if limit > 0 {
		query += fmt.Sprintf(" LIMIT %d", limit)
	}

	rows, err := s.db.QueryContext(ctx, query, liveID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	return s.scanSessions(rows)
}

// scanSessions 从 rows 扫描会话列表
func (s *SQLiteStore) scanSessions(rows *sql.Rows) ([]*LiveSession, error) {
	var sessions []*LiveSession
	for rows.Next() {
		session := &LiveSession{}
		var startTime, endTime int64
		var createdAtStr string
		var hostName, roomName sql.NullString

		err := rows.Scan(&session.ID, &session.LiveID, &hostName, &roomName, &startTime, &endTime, &session.EndReason, &createdAtStr)
		if err != nil {
			return nil, err
		}

		session.HostName = hostName.String
		session.RoomName = roomName.String

		if startTime > 0 {
			session.StartTime = time.Unix(startTime, 0)
		}
		if endTime > 0 {
			session.EndTime = time.Unix(endTime, 0)
		}
		// 解析 SQLite DATETIME 格式
		if createdAtStr != "" {
			if t, err := time.Parse("2006-01-02 15:04:05", createdAtStr); err == nil {
				session.CreatedAt = t
			}
		}

		sessions = append(sessions, session)
	}
	return sessions, nil
}

// RecordNameChange 记录名称变更
func (s *SQLiteStore) RecordNameChange(ctx context.Context, liveID, nameType, oldValue, newValue string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	_, err := s.db.ExecContext(ctx, `
		INSERT INTO name_history (live_id, name_type, old_value, new_value) VALUES (?, ?, ?, ?)
	`, liveID, nameType, oldValue, newValue)
	return err
}

// GetNameHistory 获取名称变更历史
func (s *SQLiteStore) GetNameHistory(ctx context.Context, liveID string, limit int) ([]*NameChange, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	query := `
		SELECT id, live_id, name_type, old_value, new_value, changed_at
		FROM name_history WHERE live_id = ? ORDER BY changed_at DESC
	`
	if limit > 0 {
		query += fmt.Sprintf(" LIMIT %d", limit)
	}

	rows, err := s.db.QueryContext(ctx, query, liveID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var changes []*NameChange
	for rows.Next() {
		change := &NameChange{}
		err := rows.Scan(&change.ID, &change.LiveID, &change.NameType, &change.OldValue, &change.NewValue, &change.ChangedAt)
		if err != nil {
			return nil, err
		}
		changes = append(changes, change)
	}
	return changes, nil
}

// SaveAvailableStreams 保存可用流信息（先删除旧数据再插入新数据）
func (s *SQLiteStore) SaveAvailableStreams(ctx context.Context, liveID string, streams []*AvailableStream) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// 开始事务
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	// 删除该直播间旧的可用流数据
	_, err = tx.ExecContext(ctx, `DELETE FROM available_streams WHERE live_id = ?`, liveID)
	if err != nil {
		return err
	}

	// 插入新的可用流数据
	stmt, err := tx.PrepareContext(ctx, `
		INSERT INTO available_streams 
		(live_id, stream_index, quality, attributes, updated_at)
		VALUES (?, ?, ?, ?, ?)
	`)
	if err != nil {
		return err
	}
	defer stmt.Close()

	now := time.Now().Unix()
	for i, stream := range streams {
		// 序列化 attributes 为 JSON
		attributesJSON := "{}"
		if len(stream.Attributes) > 0 {
			data, err := json.Marshal(stream.Attributes)
			if err != nil {
				return fmt.Errorf("序列化流属性失败: %w", err)
			}
			attributesJSON = string(data)
		}

		logrus.WithFields(logrus.Fields{
			"live_id":        liveID,
			"stream_index":   i,
			"quality":        stream.Quality,
			"attr_count":     len(stream.Attributes),
			"attributesJSON": attributesJSON,
		}).Debug("SaveAvailableStreams: 准备保存流信息")

		_, err = stmt.ExecContext(ctx, liveID, i, stream.Quality, attributesJSON, now)
		if err != nil {
			return err
		}
	}

	return tx.Commit()
}

// GetAvailableStreams 获取可用流信息
func (s *SQLiteStore) GetAvailableStreams(ctx context.Context, liveID string) ([]*AvailableStream, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	rows, err := s.db.QueryContext(ctx, `
		SELECT id, live_id, stream_index, quality, attributes, updated_at
		FROM available_streams WHERE live_id = ? ORDER BY stream_index ASC
	`, liveID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var streams []*AvailableStream
	for rows.Next() {
		stream := &AvailableStream{}
		var updatedAt int64
		var attributesJSON string
		err := rows.Scan(
			&stream.ID, &stream.LiveID, &stream.StreamIndex, &stream.Quality, &attributesJSON, &updatedAt,
		)
		if err != nil {
			return nil, err
		}

		// 反序列化 attributes
		stream.Attributes = make(map[string]string)
		if attributesJSON != "" && attributesJSON != "{}" {
			logrus.WithFields(logrus.Fields{
				"live_id":        liveID,
				"stream_index":   stream.StreamIndex,
				"attributesJSON": attributesJSON,
			}).Debug("GetAvailableStreams: 准备反序列化 attributes")

			if err := json.Unmarshal([]byte(attributesJSON), &stream.Attributes); err != nil {
				logrus.WithError(err).Warn("反序列化流属性失败")
			} else {
				logrus.WithFields(logrus.Fields{
					"live_id":      liveID,
					"stream_index": stream.StreamIndex,
					"attr_count":   len(stream.Attributes),
					"attributes":   stream.Attributes,
				}).Debug("GetAvailableStreams: attributes 反序列化成功")
			}
		} else {
			logrus.WithFields(logrus.Fields{
				"live_id":      liveID,
				"stream_index": stream.StreamIndex,
				"json_value":   attributesJSON,
			}).Debug("GetAvailableStreams: attributes JSON 为空或 {}")
		}

		if updatedAt > 0 {
			stream.UpdatedAt = time.Unix(updatedAt, 0)
		}
		streams = append(streams, stream)
	}
	return streams, nil
}

// SaveAvailableStreamsAny 通用接口实现，将 []map[string]interface{} 转换后保存
func (s *SQLiteStore) SaveAvailableStreamsAny(ctx context.Context, liveID string, streams interface{}) error {
	// 类型断言转换
	streamMaps, ok := streams.([]map[string]interface{})
	if !ok {
		return fmt.Errorf("invalid streams type: expected []map[string]interface{}")
	}

	logrus.WithFields(logrus.Fields{
		"live_id":      liveID,
		"stream_count": len(streamMaps),
	}).Debug("SaveAvailableStreamsAny: 开始保存流信息")

	// 转换为 AvailableStream 类型
	availableStreams := make([]*AvailableStream, 0, len(streamMaps))
	for _, m := range streamMaps {
		quality := getStringFromMap(m, "Quality")
		attributes := make(map[string]string)

		// 提取 AttributesForStreamSelect
		if attrMap, ok := m["AttributesForStreamSelect"].(map[string]string); ok {
			attributes = attrMap
		} else if attrMapInterface, ok := m["AttributesForStreamSelect"].(map[string]interface{}); ok {
			// 处理 map[string]interface{} 类型
			for k, v := range attrMapInterface {
				if strVal, ok := v.(string); ok {
					attributes[k] = strVal
				}
			}
		}

		stream := &AvailableStream{
			Quality:    quality,
			Attributes: attributes,
		}
		availableStreams = append(availableStreams, stream)
	}

	return s.SaveAvailableStreams(ctx, liveID, availableStreams)
}

// 辅助函数：从 map 中获取 string 值
func getStringFromMap(m map[string]interface{}, key string) string {
	if v, ok := m[key]; ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

// Close 关闭存储
func (s *SQLiteStore) Close() error {
	return s.db.Close()
}
