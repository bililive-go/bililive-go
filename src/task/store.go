package task

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/golang-migrate/migrate/v4"
	"github.com/golang-migrate/migrate/v4/database/sqlite"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	_ "modernc.org/sqlite"

	"github.com/bililive-go/bililive-go/src/consts"
	"github.com/sirupsen/logrus"
)

var (
	ErrTaskNotFound        = errors.New("task not found")
	ErrDatabaseNotReady    = errors.New("database not ready")
	ErrVersionIncompatible = errors.New("database version incompatible with current app version")
)

// Store 任务存储接口
type Store interface {
	// CreateTask 创建任务
	CreateTask(ctx context.Context, task *Task) error
	// GetTask 获取任务
	GetTask(ctx context.Context, id int64) (*Task, error)
	// UpdateTask 更新任务
	UpdateTask(ctx context.Context, task *Task) error
	// DeleteTask 删除任务
	DeleteTask(ctx context.Context, id int64) error
	// ListTasks 列出任务
	ListTasks(ctx context.Context, filter TaskFilter) ([]*Task, error)
	// GetPendingTasks 获取待执行的任务（按优先级排序）
	GetPendingTasks(ctx context.Context, limit int) ([]*Task, error)
	// GetRunningTasks 获取正在执行的任务
	GetRunningTasks(ctx context.Context) ([]*Task, error)
	// ResetRunningTasks 重置所有运行中的任务为待执行状态（程序重启后调用）
	ResetRunningTasks(ctx context.Context) error
	// UpdateTaskPriority 更新任务优先级
	UpdateTaskPriority(ctx context.Context, id int64, priority int) error
	// Close 关闭存储
	Close() error
}

// TaskFilter 任务过滤器
type TaskFilter struct {
	Status *TaskStatus
	Type   *TaskType
	LiveID *string
	Limit  int
	Offset int
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
		return nil, fmt.Errorf("failed to create database directory: %w", err)
	}

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}

	store := &SQLiteStore{
		db:     db,
		dbPath: dbPath,
	}

	// 运行数据库迁移
	if err := store.runMigrations(); err != nil {
		db.Close()
		return nil, fmt.Errorf("failed to run migrations: %w", err)
	}

	// 更新版本信息
	if err := store.updateVersionInfo(); err != nil {
		db.Close()
		return nil, fmt.Errorf("failed to update version info: %w", err)
	}

	return store, nil
}

// runMigrations 运行数据库迁移
func (s *SQLiteStore) runMigrations() error {
	// 获取迁移文件系统
	migrationsFS, err := GetMigrationsFS()
	if err != nil {
		return fmt.Errorf("failed to get migrations fs: %w", err)
	}

	// 创建 iofs source
	sourceDriver, err := iofs.New(migrationsFS, ".")
	if err != nil {
		return fmt.Errorf("failed to create iofs source: %w", err)
	}

	// 创建 sqlite database driver
	dbDriver, err := sqlite.WithInstance(s.db, &sqlite.Config{})
	if err != nil {
		return fmt.Errorf("failed to create sqlite driver: %w", err)
	}

	// 创建 migrate 实例
	m, err := migrate.NewWithInstance("iofs", sourceDriver, "sqlite", dbDriver)
	if err != nil {
		return fmt.Errorf("failed to create migrate instance: %w", err)
	}

	// 获取当前版本（用于日志）
	currentVersion, dirty, _ := m.Version()

	// 运行迁移
	if err := m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return fmt.Errorf("migration failed: %w", err)
	}

	// 获取迁移后版本
	newVersion, _, _ := m.Version()

	if currentVersion != newVersion {
		logrus.WithFields(logrus.Fields{
			"from_version": currentVersion,
			"to_version":   newVersion,
			"was_dirty":    dirty,
			"embedded":     IsMigrationsEmbedded(),
		}).Info("database migration completed")
	} else {
		logrus.WithFields(logrus.Fields{
			"version":  newVersion,
			"embedded": IsMigrationsEmbedded(),
		}).Debug("database schema is up to date")
	}

	return nil
}

// updateVersionInfo 更新版本信息到 system_meta 表
func (s *SQLiteStore) updateVersionInfo() error {
	appVersion := consts.AppVersion

	// 检查是否有旧版本记录
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
		logrus.WithField("version", appVersion).Info("initialized task database version info")
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
		}).Info("updated task database version info")
	}

	return err
}

// BackupDatabase 备份数据库文件（可供外部调用）
func (s *SQLiteStore) BackupDatabase() (string, error) {
	timestamp := time.Now().Format("20060102_150405")
	backupPath := s.dbPath + ".backup_" + timestamp

	src, err := os.Open(s.dbPath)
	if err != nil {
		return "", err
	}
	defer src.Close()

	dst, err := os.Create(backupPath)
	if err != nil {
		return "", err
	}
	defer dst.Close()

	if _, err := io.Copy(dst, src); err != nil {
		os.Remove(backupPath)
		return "", err
	}

	logrus.WithField("backup_path", backupPath).Info("database backed up")
	return backupPath, nil
}

// CreateTask 创建任务
func (s *SQLiteStore) CreateTask(ctx context.Context, task *Task) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	tempFilesJSON, _ := json.Marshal(task.TempFiles)
	metadataJSON, _ := json.Marshal(task.Metadata)

	result, err := s.db.ExecContext(ctx, `
		INSERT INTO tasks (type, status, priority, input_file, output_file, temp_files, live_id, room_name, host_name, platform, pre_task_id, post_task_id, metadata, can_requeue)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, task.Type, task.Status, task.Priority, task.InputFile, task.OutputFile, string(tempFilesJSON),
		task.LiveID, task.RoomName, task.HostName, task.Platform, task.PreTaskID, task.PostTaskID, string(metadataJSON), task.CanRequeue)
	if err != nil {
		return err
	}

	id, err := result.LastInsertId()
	if err != nil {
		return err
	}
	task.ID = id
	task.CreatedAt = time.Now()
	return nil
}

// GetTask 获取任务
func (s *SQLiteStore) GetTask(ctx context.Context, id int64) (*Task, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	task := &Task{}
	var tempFilesJSON, outputFile, metadataJSON sql.NullString
	var preTaskID, postTaskID sql.NullInt64
	var startedAt, completedAt sql.NullTime
	var canRequeue sql.NullInt64

	err := s.db.QueryRowContext(ctx, `
		SELECT id, type, status, priority, input_file, output_file, temp_files, live_id, room_name, host_name, platform, 
			   pre_task_id, post_task_id, metadata, created_at, started_at, completed_at, error_message, progress, can_requeue
		FROM tasks WHERE id = ?
	`, id).Scan(
		&task.ID, &task.Type, &task.Status, &task.Priority, &task.InputFile, &outputFile, &tempFilesJSON,
		&task.LiveID, &task.RoomName, &task.HostName, &task.Platform,
		&preTaskID, &postTaskID, &metadataJSON, &task.CreatedAt, &startedAt, &completedAt, &task.ErrorMessage, &task.Progress, &canRequeue,
	)
	if err == sql.ErrNoRows {
		return nil, ErrTaskNotFound
	}
	if err != nil {
		return nil, err
	}

	if outputFile.Valid {
		task.OutputFile = outputFile.String
	}
	if tempFilesJSON.Valid {
		json.Unmarshal([]byte(tempFilesJSON.String), &task.TempFiles)
	}
	if metadataJSON.Valid {
		json.Unmarshal([]byte(metadataJSON.String), &task.Metadata)
	}
	if preTaskID.Valid {
		task.PreTaskID = &preTaskID.Int64
	}
	if postTaskID.Valid {
		task.PostTaskID = &postTaskID.Int64
	}
	if startedAt.Valid {
		task.StartedAt = &startedAt.Time
	}
	if completedAt.Valid {
		task.CompletedAt = &completedAt.Time
	}
	if canRequeue.Valid {
		task.CanRequeue = canRequeue.Int64 == 1
	}

	return task, nil
}

// UpdateTask 更新任务
func (s *SQLiteStore) UpdateTask(ctx context.Context, task *Task) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	tempFilesJSON, _ := json.Marshal(task.TempFiles)
	metadataJSON, _ := json.Marshal(task.Metadata)
	canRequeue := 0
	if task.CanRequeue {
		canRequeue = 1
	}

	_, err := s.db.ExecContext(ctx, `
		UPDATE tasks SET 
			status = ?, priority = ?, output_file = ?, temp_files = ?, metadata = ?,
			started_at = ?, completed_at = ?, error_message = ?, progress = ?, can_requeue = ?
		WHERE id = ?
	`, task.Status, task.Priority, task.OutputFile, string(tempFilesJSON), string(metadataJSON),
		task.StartedAt, task.CompletedAt, task.ErrorMessage, task.Progress, canRequeue, task.ID)
	return err
}

// DeleteTask 删除任务
func (s *SQLiteStore) DeleteTask(ctx context.Context, id int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	_, err := s.db.ExecContext(ctx, "DELETE FROM tasks WHERE id = ?", id)
	return err
}

// ListTasks 列出任务
func (s *SQLiteStore) ListTasks(ctx context.Context, filter TaskFilter) ([]*Task, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	query := "SELECT id, type, status, priority, input_file, output_file, temp_files, live_id, room_name, host_name, platform, pre_task_id, post_task_id, metadata, created_at, started_at, completed_at, error_message, progress, can_requeue FROM tasks WHERE 1=1"
	args := []interface{}{}

	if filter.Status != nil {
		query += " AND status = ?"
		args = append(args, *filter.Status)
	}
	if filter.Type != nil {
		query += " AND type = ?"
		args = append(args, *filter.Type)
	}
	if filter.LiveID != nil {
		query += " AND live_id = ?"
		args = append(args, *filter.LiveID)
	}

	query += " ORDER BY priority DESC, created_at ASC"

	if filter.Limit > 0 {
		query += " LIMIT ?"
		args = append(args, filter.Limit)
	}
	if filter.Offset > 0 {
		query += " OFFSET ?"
		args = append(args, filter.Offset)
	}

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	return s.scanTasks(rows)
}

// GetPendingTasks 获取待执行的任务
func (s *SQLiteStore) GetPendingTasks(ctx context.Context, limit int) ([]*Task, error) {
	status := TaskStatusPending
	return s.ListTasks(ctx, TaskFilter{Status: &status, Limit: limit})
}

// GetRunningTasks 获取正在执行的任务
func (s *SQLiteStore) GetRunningTasks(ctx context.Context) ([]*Task, error) {
	status := TaskStatusRunning
	return s.ListTasks(ctx, TaskFilter{Status: &status})
}

// ResetRunningTasks 重置所有运行中的任务为待执行状态
func (s *SQLiteStore) ResetRunningTasks(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	_, err := s.db.ExecContext(ctx, "UPDATE tasks SET status = ?, started_at = NULL, progress = 0 WHERE status = ?",
		TaskStatusPending, TaskStatusRunning)
	return err
}

// UpdateTaskPriority 更新任务优先级
func (s *SQLiteStore) UpdateTaskPriority(ctx context.Context, id int64, priority int) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	_, err := s.db.ExecContext(ctx, "UPDATE tasks SET priority = ? WHERE id = ?", priority, id)
	return err
}

// Close 关闭存储
func (s *SQLiteStore) Close() error {
	return s.db.Close()
}

// scanTasks 从rows扫描任务列表
func (s *SQLiteStore) scanTasks(rows *sql.Rows) ([]*Task, error) {
	var tasks []*Task
	for rows.Next() {
		task := &Task{}
		var tempFilesJSON, outputFile, metadataJSON sql.NullString
		var preTaskID, postTaskID sql.NullInt64
		var startedAt, completedAt sql.NullTime
		var canRequeue sql.NullInt64

		err := rows.Scan(
			&task.ID, &task.Type, &task.Status, &task.Priority, &task.InputFile, &outputFile, &tempFilesJSON,
			&task.LiveID, &task.RoomName, &task.HostName, &task.Platform,
			&preTaskID, &postTaskID, &metadataJSON, &task.CreatedAt, &startedAt, &completedAt, &task.ErrorMessage, &task.Progress, &canRequeue,
		)
		if err != nil {
			return nil, err
		}

		if outputFile.Valid {
			task.OutputFile = outputFile.String
		}
		if tempFilesJSON.Valid {
			json.Unmarshal([]byte(tempFilesJSON.String), &task.TempFiles)
		}
		if metadataJSON.Valid {
			json.Unmarshal([]byte(metadataJSON.String), &task.Metadata)
		}
		if preTaskID.Valid {
			task.PreTaskID = &preTaskID.Int64
		}
		if postTaskID.Valid {
			task.PostTaskID = &postTaskID.Int64
		}
		if startedAt.Valid {
			task.StartedAt = &startedAt.Time
		}
		if completedAt.Valid {
			task.CompletedAt = &completedAt.Time
		}
		if canRequeue.Valid {
			task.CanRequeue = canRequeue.Int64 == 1
		}

		tasks = append(tasks, task)
	}
	return tasks, nil
}
