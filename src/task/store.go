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

	_ "modernc.org/sqlite"

	"github.com/bililive-go/bililive-go/src/consts"
	"github.com/bililive-go/bililive-go/src/pkg/migration"
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
	// DeleteTasksByStatus 删除指定状态的所有任务
	DeleteTasksByStatus(ctx context.Context, status TaskStatus) (int, error)
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
	// 使用新的迁移系统
	config := &migration.MigrationConfig{
		DBPath: s.dbPath,
		Schema: TaskDatabaseSchema,
		DB:     s.db,
	}

	migrator, err := migration.NewMigrator(config)
	if err != nil {
		return fmt.Errorf("failed to create migrator: %w", err)
	}

	// 先检查是否需要从上次失败的迁移中恢复
	recovered, err := migrator.CheckAndRecover()
	if err != nil {
		logrus.WithError(err).Warn("migration recovery check failed")
	}
	if recovered {
		logrus.Info("recovered from incomplete migration")
		// 恢复后需要重新打开数据库连接
		s.db.Close()
		db, err := sql.Open("sqlite", s.dbPath)
		if err != nil {
			return fmt.Errorf("failed to reopen database after recovery: %w", err)
		}
		s.db = db
		// 更新配置中的DB连接
		config.DB = s.db
		migrator, err = migration.NewMigrator(config)
		if err != nil {
			return fmt.Errorf("failed to recreate migrator after recovery: %w", err)
		}
	}

	// 执行迁移
	result, err := migrator.Run()
	if err != nil {
		return fmt.Errorf("migration failed: %w", err)
	}

	if result.BackupPath != "" {
		logrus.WithField("backup_path", result.BackupPath).Debug("database backup created")
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
	commandsJSON, _ := json.Marshal(task.Commands)
	canRequeue := 0
	if task.CanRequeue {
		canRequeue = 1
	}

	_, err := s.db.ExecContext(ctx, `
		UPDATE tasks SET 
			status = ?, priority = ?, output_file = ?, temp_files = ?, metadata = ?,
			started_at = ?, completed_at = ?, error_message = ?, progress = ?, can_requeue = ?,
			commands = ?, logs = ?
		WHERE id = ?
	`, task.Status, task.Priority, task.OutputFile, string(tempFilesJSON), string(metadataJSON),
		task.StartedAt, task.CompletedAt, task.ErrorMessage, task.Progress, canRequeue,
		string(commandsJSON), task.Logs, task.ID)
	return err
}

// DeleteTask 删除任务
func (s *SQLiteStore) DeleteTask(ctx context.Context, id int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	_, err := s.db.ExecContext(ctx, "DELETE FROM tasks WHERE id = ?", id)
	return err
}

// DeleteTasksByStatus 删除指定状态的所有任务
func (s *SQLiteStore) DeleteTasksByStatus(ctx context.Context, status TaskStatus) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	result, err := s.db.ExecContext(ctx, "DELETE FROM tasks WHERE status = ?", status)
	if err != nil {
		return 0, err
	}

	rowsAffected, _ := result.RowsAffected()
	return int(rowsAffected), nil
}

// ListTasks 列出任务
func (s *SQLiteStore) ListTasks(ctx context.Context, filter TaskFilter) ([]*Task, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	query := "SELECT id, type, status, priority, input_file, output_file, temp_files, live_id, room_name, host_name, platform, pre_task_id, post_task_id, metadata, created_at, started_at, completed_at, error_message, progress, can_requeue, commands, logs FROM tasks WHERE 1=1"
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

	query += " ORDER BY id DESC"

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
		var tempFilesJSON, outputFile, metadataJSON, errorMessage, commandsJSON, logs sql.NullString
		var preTaskID, postTaskID sql.NullInt64
		var startedAt, completedAt sql.NullTime
		var canRequeue sql.NullInt64

		err := rows.Scan(
			&task.ID, &task.Type, &task.Status, &task.Priority, &task.InputFile, &outputFile, &tempFilesJSON,
			&task.LiveID, &task.RoomName, &task.HostName, &task.Platform,
			&preTaskID, &postTaskID, &metadataJSON, &task.CreatedAt, &startedAt, &completedAt, &errorMessage, &task.Progress, &canRequeue,
			&commandsJSON, &logs,
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
		if errorMessage.Valid {
			task.ErrorMessage = errorMessage.String
		}
		if commandsJSON.Valid {
			json.Unmarshal([]byte(commandsJSON.String), &task.Commands)
		}
		if logs.Valid {
			task.Logs = logs.String
		}

		tasks = append(tasks, task)
	}
	return tasks, nil
}
