package iostats

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/bililive-go/bililive-go/src/configs"
	_ "modernc.org/sqlite"
)

var (
	// ErrStoreNotReady 存储未就绪错误
	ErrStoreNotReady = errors.New("iostats store not ready")
)

// Store IO 统计存储接口
type Store interface {
	// SaveIOStat 保存 IO 统计数据
	SaveIOStat(ctx context.Context, stat *IOStat) error
	// SaveIOStats 批量保存 IO 统计数据
	SaveIOStats(ctx context.Context, stats []*IOStat) error
	// QueryIOStats 查询 IO 统计数据
	QueryIOStats(ctx context.Context, query IOStatsQuery) ([]IOStat, error)

	// SaveRequestStatus 保存请求状态
	SaveRequestStatus(ctx context.Context, status *RequestStatus) error
	// QueryRequestStatus 查询请求状态
	QueryRequestStatus(ctx context.Context, query RequestStatusQuery) ([]RequestStatus, error)
	// QueryRequestStatusSegments 查询请求状态时间段（用于横条图）
	QueryRequestStatusSegments(ctx context.Context, query RequestStatusQuery) (*RequestStatusResponse, error)

	// SaveDiskIOStats 保存磁盘 I/O 统计数据
	SaveDiskIOStats(ctx context.Context, stats []*DiskIOStat) error
	// QueryDiskIOStats 查询磁盘 I/O 统计数据
	QueryDiskIOStats(ctx context.Context, query DiskIOQuery) ([]DiskIOStat, error)
	// GetDiskDevices 获取可用的磁盘设备列表
	GetDiskDevices(ctx context.Context) ([]string, error)

	// SaveMemoryStats 批量保存内存统计数据
	SaveMemoryStats(ctx context.Context, stats []*MemoryStat) error
	// QueryMemoryStats 查询内存统计数据
	QueryMemoryStats(ctx context.Context, query MemoryStatsQuery) (*MemoryStatsResponse, error)
	// GetMemoryCategories 获取可用的内存统计类别列表
	GetMemoryCategories(ctx context.Context) ([]string, error)

	// GetFilters 获取可用的筛选器选项
	GetFilters(ctx context.Context) (*FiltersResponse, error)

	// Cleanup 清理过期数据
	Cleanup(ctx context.Context, retentionDays int) error

	// Close 关闭存储
	Close() error
}

// SQLiteStore SQLite 存储实现
type SQLiteStore struct {
	db     *sql.DB
	dbPath string
	mu     sync.RWMutex
}

// NewSQLiteStore 创建 SQLite 存储
func NewSQLiteStore(dbPath string) (*SQLiteStore, error) {
	// 确保目录存在
	dir := filepath.Dir(dbPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create directory: %w", err)
	}

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}

	// 设置连接池参数
	db.SetMaxOpenConns(1) // SQLite 单写入
	db.SetMaxIdleConns(1)

	store := &SQLiteStore{
		db:     db,
		dbPath: dbPath,
	}

	// 初始化表结构
	if err := store.initTables(); err != nil {
		db.Close()
		return nil, fmt.Errorf("failed to init tables: %w", err)
	}

	return store, nil
}

// initTables 初始化表结构
func (s *SQLiteStore) initTables() error {
	// IO 统计表
	_, err := s.db.Exec(`
		CREATE TABLE IF NOT EXISTS io_stats (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			timestamp INTEGER NOT NULL,
			stat_type TEXT NOT NULL,
			live_id TEXT,
			platform TEXT,
			speed INTEGER NOT NULL DEFAULT 0,
			total_bytes INTEGER NOT NULL DEFAULT 0
		);
		CREATE INDEX IF NOT EXISTS idx_io_stats_timestamp ON io_stats(timestamp);
		CREATE INDEX IF NOT EXISTS idx_io_stats_type_time ON io_stats(stat_type, timestamp);
		CREATE INDEX IF NOT EXISTS idx_io_stats_live_time ON io_stats(live_id, timestamp);
	`)
	if err != nil {
		return fmt.Errorf("failed to create io_stats table: %w", err)
	}

	// 请求状态表
	_, err = s.db.Exec(`
		CREATE TABLE IF NOT EXISTS request_status (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			timestamp INTEGER NOT NULL,
			live_id TEXT NOT NULL,
			platform TEXT NOT NULL,
			success INTEGER NOT NULL,
			error_message TEXT
		);
		CREATE INDEX IF NOT EXISTS idx_request_status_timestamp ON request_status(timestamp);
		CREATE INDEX IF NOT EXISTS idx_request_status_live ON request_status(live_id, timestamp);
		CREATE INDEX IF NOT EXISTS idx_request_status_platform ON request_status(platform, timestamp);
	`)
	if err != nil {
		return fmt.Errorf("failed to create request_status table: %w", err)
	}

	// 磁盘 I/O 统计表
	_, err = s.db.Exec(`
		CREATE TABLE IF NOT EXISTS disk_io_stats (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			timestamp INTEGER NOT NULL,
			device_name TEXT NOT NULL,
			read_count INTEGER NOT NULL DEFAULT 0,
			write_count INTEGER NOT NULL DEFAULT 0,
			read_bytes INTEGER NOT NULL DEFAULT 0,
			write_bytes INTEGER NOT NULL DEFAULT 0,
			read_time_ms INTEGER NOT NULL DEFAULT 0,
			write_time_ms INTEGER NOT NULL DEFAULT 0,
			avg_read_latency REAL NOT NULL DEFAULT 0,
			avg_write_latency REAL NOT NULL DEFAULT 0,
			read_speed INTEGER NOT NULL DEFAULT 0,
			write_speed INTEGER NOT NULL DEFAULT 0
		);
		CREATE INDEX IF NOT EXISTS idx_disk_io_timestamp ON disk_io_stats(timestamp);
		CREATE INDEX IF NOT EXISTS idx_disk_io_device_time ON disk_io_stats(device_name, timestamp);
	`)
	if err != nil {
		return fmt.Errorf("failed to create disk_io_stats table: %w", err)
	}

	// 内存统计表
	_, err = s.db.Exec(`
		CREATE TABLE IF NOT EXISTS memory_stats (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			timestamp INTEGER NOT NULL,
			category TEXT NOT NULL,
			rss INTEGER NOT NULL DEFAULT 0,
			vms INTEGER NOT NULL DEFAULT 0,
			alloc INTEGER NOT NULL DEFAULT 0,
			sys INTEGER NOT NULL DEFAULT 0,
			num_gc INTEGER NOT NULL DEFAULT 0
		);
		CREATE INDEX IF NOT EXISTS idx_memory_stats_timestamp ON memory_stats(timestamp);
		CREATE INDEX IF NOT EXISTS idx_memory_stats_category_time ON memory_stats(category, timestamp);
	`)
	if err != nil {
		return fmt.Errorf("failed to create memory_stats table: %w", err)
	}

	return nil
}

// SaveIOStat 保存单条 IO 统计数据
func (s *SQLiteStore) SaveIOStat(ctx context.Context, stat *IOStat) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	_, err := s.db.ExecContext(ctx,
		`INSERT INTO io_stats (timestamp, stat_type, live_id, platform, speed, total_bytes)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		stat.Timestamp, stat.StatType, stat.LiveID, stat.Platform, stat.Speed, stat.TotalBytes,
	)
	return err
}

// SaveIOStats 批量保存 IO 统计数据
func (s *SQLiteStore) SaveIOStats(ctx context.Context, stats []*IOStat) error {
	if len(stats) == 0 {
		return nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	stmt, err := tx.PrepareContext(ctx,
		`INSERT INTO io_stats (timestamp, stat_type, live_id, platform, speed, total_bytes)
		 VALUES (?, ?, ?, ?, ?, ?)`,
	)
	if err != nil {
		return err
	}
	defer stmt.Close()

	for _, stat := range stats {
		_, err = stmt.ExecContext(ctx, stat.Timestamp, stat.StatType, stat.LiveID, stat.Platform, stat.Speed, stat.TotalBytes)
		if err != nil {
			return err
		}
	}

	return tx.Commit()
}

// QueryIOStats 查询 IO 统计数据
func (s *SQLiteStore) QueryIOStats(ctx context.Context, query IOStatsQuery) ([]IOStat, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	// 构建查询
	sqlQuery := `SELECT id, timestamp, stat_type, live_id, platform, speed, total_bytes 
				 FROM io_stats WHERE timestamp >= ? AND timestamp <= ?`
	args := []interface{}{query.StartTime, query.EndTime}

	if len(query.StatTypes) > 0 {
		sqlQuery += " AND stat_type IN ("
		for i, st := range query.StatTypes {
			if i > 0 {
				sqlQuery += ","
			}
			sqlQuery += "?"
			args = append(args, st)
		}
		sqlQuery += ")"
	}

	if query.LiveID != "" {
		sqlQuery += " AND live_id = ?"
		args = append(args, query.LiveID)
	}

	if query.Platform != "" {
		sqlQuery += " AND platform = ?"
		args = append(args, query.Platform)
	}

	sqlQuery += " ORDER BY timestamp ASC"

	rows, err := s.db.QueryContext(ctx, sqlQuery, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var stats []IOStat
	for rows.Next() {
		var stat IOStat
		var liveID, platform sql.NullString
		if err := rows.Scan(&stat.ID, &stat.Timestamp, &stat.StatType, &liveID, &platform, &stat.Speed, &stat.TotalBytes); err != nil {
			return nil, err
		}
		stat.LiveID = liveID.String
		stat.Platform = platform.String
		stats = append(stats, stat)
	}

	// 如果需要聚合
	if query.Aggregation != "" && query.Aggregation != "none" {
		stats = s.aggregateStats(stats, query.Aggregation)
	}

	return stats, rows.Err()
}

// aggregateStats 聚合统计数据
func (s *SQLiteStore) aggregateStats(stats []IOStat, aggregation string) []IOStat {
	if len(stats) == 0 {
		return stats
	}

	var interval int64
	switch aggregation {
	case "minute":
		interval = 60 * 1000 // 1 分钟（毫秒）
	case "hour":
		interval = 3600 * 1000 // 1 小时（毫秒）
	default:
		return stats
	}

	// 按 stat_type + live_id + platform + 时间段分组
	type groupKey struct {
		statType StatType
		liveID   string
		platform string
		bucket   int64
	}

	groups := make(map[groupKey]*struct {
		count      int
		speedSum   int64
		totalBytes int64
	})

	for _, stat := range stats {
		bucket := stat.Timestamp / interval
		key := groupKey{
			statType: stat.StatType,
			liveID:   stat.LiveID,
			platform: stat.Platform,
			bucket:   bucket,
		}

		if groups[key] == nil {
			groups[key] = &struct {
				count      int
				speedSum   int64
				totalBytes int64
			}{}
		}
		groups[key].count++
		groups[key].speedSum += stat.Speed
		groups[key].totalBytes = stat.TotalBytes // 使用最后一个值
	}

	// 转换回切片
	result := make([]IOStat, 0, len(groups))
	for key, group := range groups {
		result = append(result, IOStat{
			Timestamp:  key.bucket * interval,
			StatType:   key.statType,
			LiveID:     key.liveID,
			Platform:   key.platform,
			Speed:      group.speedSum / int64(group.count), // 平均速度
			TotalBytes: group.totalBytes,
		})
	}

	return result
}

// SaveRequestStatus 保存请求状态
func (s *SQLiteStore) SaveRequestStatus(ctx context.Context, status *RequestStatus) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	successInt := 0
	if status.Success {
		successInt = 1
	}

	_, err := s.db.ExecContext(ctx,
		`INSERT INTO request_status (timestamp, live_id, platform, success, error_message)
		 VALUES (?, ?, ?, ?, ?)`,
		status.Timestamp, status.LiveID, status.Platform, successInt, status.ErrorMessage,
	)
	return err
}

// QueryRequestStatus 查询请求状态
func (s *SQLiteStore) QueryRequestStatus(ctx context.Context, query RequestStatusQuery) ([]RequestStatus, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	sqlQuery := `SELECT id, timestamp, live_id, platform, success, error_message 
				 FROM request_status WHERE timestamp >= ? AND timestamp <= ?`
	args := []interface{}{query.StartTime, query.EndTime}

	switch query.ViewMode {
	case ViewModeByLive:
		if query.LiveID != "" {
			sqlQuery += " AND live_id = ?"
			args = append(args, query.LiveID)
		}
	case ViewModeByPlatform:
		if query.Platform != "" {
			sqlQuery += " AND platform = ?"
			args = append(args, query.Platform)
		}
	}

	sqlQuery += " ORDER BY timestamp ASC"

	rows, err := s.db.QueryContext(ctx, sqlQuery, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var statuses []RequestStatus
	for rows.Next() {
		var status RequestStatus
		var successInt int
		var errorMsg sql.NullString
		if err := rows.Scan(&status.ID, &status.Timestamp, &status.LiveID, &status.Platform, &successInt, &errorMsg); err != nil {
			return nil, err
		}
		status.Success = successInt == 1
		status.ErrorMessage = errorMsg.String
		statuses = append(statuses, status)
	}

	return statuses, rows.Err()
}

// QueryRequestStatusSegments 查询请求状态时间段
func (s *SQLiteStore) QueryRequestStatusSegments(ctx context.Context, query RequestStatusQuery) (*RequestStatusResponse, error) {
	statuses, err := s.QueryRequestStatus(ctx, query)
	if err != nil {
		return nil, err
	}

	response := &RequestStatusResponse{
		Segments:        make([]RequestStatusSegment, 0),
		GroupedSegments: make(map[string][]RequestStatusSegment),
	}

	if len(statuses) == 0 {
		return response, nil
	}

	// 根据查看模式处理
	switch query.ViewMode {
	case ViewModeGlobal:
		response.Segments = s.buildSegments(statuses)
	case ViewModeByLive:
		// 按直播间分组
		grouped := make(map[string][]RequestStatus)
		for _, status := range statuses {
			grouped[status.LiveID] = append(grouped[status.LiveID], status)
		}
		for liveID, group := range grouped {
			response.GroupedSegments[liveID] = s.buildSegments(group)
		}
	case ViewModeByPlatform:
		// 按平台分组
		grouped := make(map[string][]RequestStatus)
		for _, status := range statuses {
			grouped[status.Platform] = append(grouped[status.Platform], status)
		}
		for platform, group := range grouped {
			response.GroupedSegments[platform] = s.buildSegments(group)
		}
	}

	return response, nil
}

// buildSegments 构建状态时间段
func (s *SQLiteStore) buildSegments(statuses []RequestStatus) []RequestStatusSegment {
	if len(statuses) == 0 {
		return nil
	}

	segments := make([]RequestStatusSegment, 0)
	var currentSegment *RequestStatusSegment

	for _, status := range statuses {
		if currentSegment == nil {
			currentSegment = &RequestStatusSegment{
				StartTime: status.Timestamp,
				EndTime:   status.Timestamp,
				Success:   status.Success,
				Count:     1,
			}
			continue
		}

		// 如果状态相同且时间间隔不超过 2 分钟，合并到当前段
		if status.Success == currentSegment.Success && status.Timestamp-currentSegment.EndTime < 2*60*1000 {
			currentSegment.EndTime = status.Timestamp
			currentSegment.Count++
		} else {
			// 开始新的时间段
			segments = append(segments, *currentSegment)
			currentSegment = &RequestStatusSegment{
				StartTime: status.Timestamp,
				EndTime:   status.Timestamp,
				Success:   status.Success,
				Count:     1,
			}
		}
	}

	// 添加最后一个段
	if currentSegment != nil {
		segments = append(segments, *currentSegment)
	}

	return segments
}

// GetFilters 获取可用的筛选器选项
func (s *SQLiteStore) GetFilters(ctx context.Context) (*FiltersResponse, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	response := &FiltersResponse{
		LiveIDs:   make([]string, 0),
		Platforms: make([]string, 0),
	}

	// 获取直播间 ID 列表
	rows, err := s.db.QueryContext(ctx, `SELECT DISTINCT live_id FROM request_status WHERE live_id IS NOT NULL AND live_id != '' ORDER BY live_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	for rows.Next() {
		var liveID string
		if err := rows.Scan(&liveID); err != nil {
			return nil, err
		}
		response.LiveIDs = append(response.LiveIDs, liveID)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// 获取平台列表
	rows2, err := s.db.QueryContext(ctx, `SELECT DISTINCT platform FROM request_status WHERE platform IS NOT NULL AND platform != '' ORDER BY platform`)
	if err != nil {
		return nil, err
	}
	defer rows2.Close()

	for rows2.Next() {
		var platform string
		if err := rows2.Scan(&platform); err != nil {
			return nil, err
		}
		response.Platforms = append(response.Platforms, platform)
	}

	return response, rows2.Err()
}

// Cleanup 清理过期数据
func (s *SQLiteStore) Cleanup(ctx context.Context, retentionDays int) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	cutoff := time.Now().AddDate(0, 0, -retentionDays).UnixMilli()

	// 清理 IO 统计数据
	_, err := s.db.ExecContext(ctx, `DELETE FROM io_stats WHERE timestamp < ?`, cutoff)
	if err != nil {
		return fmt.Errorf("failed to cleanup io_stats: %w", err)
	}

	// 清理请求状态数据
	_, err = s.db.ExecContext(ctx, `DELETE FROM request_status WHERE timestamp < ?`, cutoff)
	if err != nil {
		return fmt.Errorf("failed to cleanup request_status: %w", err)
	}

	// 清理磁盘 I/O 统计数据
	_, err = s.db.ExecContext(ctx, `DELETE FROM disk_io_stats WHERE timestamp < ?`, cutoff)
	if err != nil {
		return fmt.Errorf("failed to cleanup disk_io_stats: %w", err)
	}

	// 清理内存统计数据
	_, err = s.db.ExecContext(ctx, `DELETE FROM memory_stats WHERE timestamp < ?`, cutoff)
	if err != nil {
		return fmt.Errorf("failed to cleanup memory_stats: %w", err)
	}

	return nil
}

// Close 关闭存储
func (s *SQLiteStore) Close() error {
	return s.db.Close()
}

// GetDefaultDBPath 获取默认数据库路径
func GetDefaultDBPath() string {
	cfg := configs.GetCurrentConfig()
	if cfg != nil && cfg.AppDataPath != "" {
		return filepath.Join(cfg.AppDataPath, "db", "iostats.db")
	}
	// 默认使用当前目录
	return filepath.Join(".", ".appdata", "db", "iostats.db")
}

// SaveDiskIOStats 批量保存磁盘 I/O 统计数据
func (s *SQLiteStore) SaveDiskIOStats(ctx context.Context, stats []*DiskIOStat) error {
	if len(stats) == 0 {
		return nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	stmt, err := tx.PrepareContext(ctx,
		`INSERT INTO disk_io_stats (timestamp, device_name, read_count, write_count, read_bytes, write_bytes, 
		 read_time_ms, write_time_ms, avg_read_latency, avg_write_latency, read_speed, write_speed)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
	)
	if err != nil {
		return err
	}
	defer stmt.Close()

	for _, stat := range stats {
		_, err = stmt.ExecContext(ctx,
			stat.Timestamp, stat.DeviceName, stat.ReadCount, stat.WriteCount,
			stat.ReadBytes, stat.WriteBytes, stat.ReadTimeMs, stat.WriteTimeMs,
			stat.AvgReadLatency, stat.AvgWriteLatency, stat.ReadSpeed, stat.WriteSpeed,
		)
		if err != nil {
			return err
		}
	}

	return tx.Commit()
}

// QueryDiskIOStats 查询磁盘 I/O 统计数据
func (s *SQLiteStore) QueryDiskIOStats(ctx context.Context, query DiskIOQuery) ([]DiskIOStat, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	sqlQuery := `SELECT id, timestamp, device_name, read_count, write_count, read_bytes, write_bytes,
				 read_time_ms, write_time_ms, avg_read_latency, avg_write_latency, read_speed, write_speed
				 FROM disk_io_stats WHERE timestamp >= ? AND timestamp <= ?`
	args := []interface{}{query.StartTime, query.EndTime}

	if query.DeviceName != "" {
		sqlQuery += " AND device_name = ?"
		args = append(args, query.DeviceName)
	}

	sqlQuery += " ORDER BY timestamp ASC"

	rows, err := s.db.QueryContext(ctx, sqlQuery, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var stats []DiskIOStat
	for rows.Next() {
		var stat DiskIOStat
		if err := rows.Scan(
			&stat.ID, &stat.Timestamp, &stat.DeviceName,
			&stat.ReadCount, &stat.WriteCount, &stat.ReadBytes, &stat.WriteBytes,
			&stat.ReadTimeMs, &stat.WriteTimeMs, &stat.AvgReadLatency, &stat.AvgWriteLatency,
			&stat.ReadSpeed, &stat.WriteSpeed,
		); err != nil {
			return nil, err
		}
		stats = append(stats, stat)
	}

	return stats, rows.Err()
}

// GetDiskDevices 获取可用的磁盘设备列表
func (s *SQLiteStore) GetDiskDevices(ctx context.Context) ([]string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	rows, err := s.db.QueryContext(ctx,
		`SELECT DISTINCT device_name FROM disk_io_stats WHERE device_name IS NOT NULL AND device_name != '' ORDER BY device_name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var devices []string
	for rows.Next() {
		var device string
		if err := rows.Scan(&device); err != nil {
			return nil, err
		}
		devices = append(devices, device)
	}

	return devices, rows.Err()
}

// SaveMemoryStats 批量保存内存统计数据
func (s *SQLiteStore) SaveMemoryStats(ctx context.Context, stats []*MemoryStat) error {
	if len(stats) == 0 {
		return nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	stmt, err := tx.PrepareContext(ctx,
		`INSERT INTO memory_stats (timestamp, category, rss, vms, alloc, sys, num_gc)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
	)
	if err != nil {
		return err
	}
	defer stmt.Close()

	for _, stat := range stats {
		_, err = stmt.ExecContext(ctx,
			stat.Timestamp, stat.Category, stat.RSS, stat.VMS, stat.Alloc, stat.Sys, stat.NumGC,
		)
		if err != nil {
			return err
		}
	}

	return tx.Commit()
}

// QueryMemoryStats 查询内存统计数据
func (s *SQLiteStore) QueryMemoryStats(ctx context.Context, query MemoryStatsQuery) (*MemoryStatsResponse, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	sqlQuery := `SELECT id, timestamp, category, rss, vms, alloc, sys, num_gc
				 FROM memory_stats WHERE timestamp >= ? AND timestamp <= ?`
	args := []interface{}{query.StartTime, query.EndTime}

	if len(query.Categories) > 0 {
		sqlQuery += " AND category IN ("
		for i, cat := range query.Categories {
			if i > 0 {
				sqlQuery += ","
			}
			sqlQuery += "?"
			args = append(args, cat)
		}
		sqlQuery += ")"
	}

	sqlQuery += " ORDER BY timestamp ASC"

	rows, err := s.db.QueryContext(ctx, sqlQuery, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var stats []MemoryStat
	for rows.Next() {
		var stat MemoryStat
		if err := rows.Scan(
			&stat.ID, &stat.Timestamp, &stat.Category,
			&stat.RSS, &stat.VMS, &stat.Alloc, &stat.Sys, &stat.NumGC,
		); err != nil {
			return nil, err
		}
		stats = append(stats, stat)
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	// 如果需要聚合
	if query.Aggregation != "" && query.Aggregation != "none" {
		stats = s.aggregateMemoryStats(stats, query.Aggregation)
	}

	// 按类别分组
	response := &MemoryStatsResponse{
		Stats:        stats,
		GroupedStats: make(map[string][]MemoryStat),
	}

	for _, stat := range stats {
		response.GroupedStats[stat.Category] = append(response.GroupedStats[stat.Category], stat)
	}

	return response, nil
}

// aggregateMemoryStats 聚合内存统计数据
func (s *SQLiteStore) aggregateMemoryStats(stats []MemoryStat, aggregation string) []MemoryStat {
	if len(stats) == 0 {
		return stats
	}

	var interval int64
	switch aggregation {
	case "minute":
		interval = 60 * 1000 // 1 分钟（毫秒）
	case "hour":
		interval = 3600 * 1000 // 1 小时（毫秒）
	default:
		return stats
	}

	// 按 category + 时间段分组
	type groupKey struct {
		category string
		bucket   int64
	}

	groups := make(map[groupKey]*struct {
		count int
		rss   uint64
		vms   uint64
		alloc uint64
		sys   uint64
		numGC uint32
	})

	for _, stat := range stats {
		bucket := stat.Timestamp / interval
		key := groupKey{
			category: stat.Category,
			bucket:   bucket,
		}

		if groups[key] == nil {
			groups[key] = &struct {
				count int
				rss   uint64
				vms   uint64
				alloc uint64
				sys   uint64
				numGC uint32
			}{}
		}
		groups[key].count++
		groups[key].rss += stat.RSS
		groups[key].vms += stat.VMS
		groups[key].alloc += stat.Alloc
		groups[key].sys += stat.Sys
		if stat.NumGC > groups[key].numGC {
			groups[key].numGC = stat.NumGC // 使用最大值
		}
	}

	// 转换回切片
	result := make([]MemoryStat, 0, len(groups))
	for key, group := range groups {
		result = append(result, MemoryStat{
			Timestamp: key.bucket * interval,
			Category:  key.category,
			RSS:       group.rss / uint64(group.count), // 平均值
			VMS:       group.vms / uint64(group.count),
			Alloc:     group.alloc / uint64(group.count),
			Sys:       group.sys / uint64(group.count),
			NumGC:     group.numGC,
		})
	}

	return result
}

// GetMemoryCategories 获取可用的内存统计类别列表
func (s *SQLiteStore) GetMemoryCategories(ctx context.Context) ([]string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	rows, err := s.db.QueryContext(ctx,
		`SELECT DISTINCT category FROM memory_stats WHERE category IS NOT NULL AND category != '' ORDER BY category`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var categories []string
	for rows.Next() {
		var category string
		if err := rows.Scan(&category); err != nil {
			return nil, err
		}
		categories = append(categories, category)
	}

	return categories, rows.Err()
}
