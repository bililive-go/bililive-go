package pipeline

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/sirupsen/logrus"
	_ "modernc.org/sqlite" // Pure Go SQLite driver
)

// Store 任务存储接口
type Store interface {
	// CreateTask 创建任务
	CreateTask(ctx context.Context, task *PipelineTask) error
	// GetTask 获取任务
	GetTask(ctx context.Context, id int64) (*PipelineTask, error)
	// UpdateTask 更新任务
	UpdateTask(ctx context.Context, task *PipelineTask) error
	// DeleteTask 删除任务
	DeleteTask(ctx context.Context, id int64) error
	// ListTasks 列出任务
	ListTasks(ctx context.Context, filter TaskFilter) ([]*PipelineTask, error)
	// GetPendingTasks 获取待执行的任务
	GetPendingTasks(ctx context.Context, limit int) ([]*PipelineTask, error)
	// ResetRunningTasks 重置所有运行中的任务为待执行
	ResetRunningTasks(ctx context.Context) error
	// DeleteTasksByStatus 删除指定状态的所有任务
	DeleteTasksByStatus(ctx context.Context, status PipelineStatus) (int, error)
	// Close 关闭存储
	Close() error
}

// TaskFilter 任务过滤条件
type TaskFilter struct {
	Status   *PipelineStatus // 状态过滤
	LiveID   *string         // 直播间 ID 过滤
	Platform *string         // 平台过滤
	Limit    int             // 限制返回数量
	Offset   int             // 偏移量
}

// SQLiteStore SQLite 存储实现
type SQLiteStore struct {
	db *sql.DB
	mu sync.RWMutex
}

// NewSQLiteStore 创建 SQLite 存储
func NewSQLiteStore(dbPath string) (*SQLiteStore, error) {
	// 确保目录存在
	if err := os.MkdirAll(filepath.Dir(dbPath), 0755); err != nil {
		return nil, fmt.Errorf("failed to create database directory: %w", err)
	}

	db, err := sql.Open("sqlite", dbPath+"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}

	store := &SQLiteStore{db: db}

	// 初始化表结构
	if err := store.initSchema(); err != nil {
		db.Close()
		return nil, fmt.Errorf("failed to init schema: %w", err)
	}

	return store, nil
}

// initSchema 初始化数据库表结构
func (s *SQLiteStore) initSchema() error {
	schema := `
	CREATE TABLE IF NOT EXISTS pipeline_tasks (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		status TEXT NOT NULL DEFAULT 'pending',
		record_info_json TEXT,
		pipeline_config_json TEXT,
		initial_files_json TEXT,
		current_files_json TEXT,
		current_stage INTEGER DEFAULT 0,
		total_stages INTEGER DEFAULT 0,
		stage_results_json TEXT,
		progress INTEGER DEFAULT 0,
		created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
		started_at TIMESTAMP,
		completed_at TIMESTAMP,
		error_message TEXT,
		can_retry INTEGER DEFAULT 1
	);

	CREATE INDEX IF NOT EXISTS idx_pipeline_tasks_status ON pipeline_tasks(status);
	CREATE INDEX IF NOT EXISTS idx_pipeline_tasks_created_at ON pipeline_tasks(created_at);
	`

	_, err := s.db.Exec(schema)
	return err
}

// CreateTask 创建任务
func (s *SQLiteStore) CreateTask(ctx context.Context, task *PipelineTask) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	recordInfoJSON, _ := json.Marshal(task.RecordInfo)
	pipelineConfigJSON, _ := json.Marshal(task.PipelineConfig)
	initialFilesJSON, _ := json.Marshal(task.InitialFiles)
	currentFilesJSON, _ := json.Marshal(task.CurrentFiles)
	stageResultsJSON, _ := json.Marshal(task.StageResults)

	result, err := s.db.ExecContext(ctx, `
		INSERT INTO pipeline_tasks (
			status, record_info_json, pipeline_config_json,
			initial_files_json, current_files_json,
			current_stage, total_stages, stage_results_json,
			progress, created_at, can_retry
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`,
		task.Status,
		string(recordInfoJSON),
		string(pipelineConfigJSON),
		string(initialFilesJSON),
		string(currentFilesJSON),
		task.CurrentStage,
		task.TotalStages,
		string(stageResultsJSON),
		task.Progress,
		task.CreatedAt,
		boolToInt(task.CanRetry),
	)
	if err != nil {
		return err
	}

	id, err := result.LastInsertId()
	if err != nil {
		return err
	}
	task.ID = id

	return nil
}

// GetTask 获取任务
func (s *SQLiteStore) GetTask(ctx context.Context, id int64) (*PipelineTask, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	row := s.db.QueryRowContext(ctx, `
		SELECT id, status, record_info_json, pipeline_config_json,
			initial_files_json, current_files_json,
			current_stage, total_stages, stage_results_json,
			progress, created_at, started_at, completed_at,
			error_message, can_retry
		FROM pipeline_tasks WHERE id = ?
	`, id)

	return scanTask(row)
}

// UpdateTask 更新任务
func (s *SQLiteStore) UpdateTask(ctx context.Context, task *PipelineTask) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	currentFilesJSON, _ := json.Marshal(task.CurrentFiles)
	stageResultsJSON, _ := json.Marshal(task.StageResults)

	_, err := s.db.ExecContext(ctx, `
		UPDATE pipeline_tasks SET
			status = ?,
			current_files_json = ?,
			current_stage = ?,
			stage_results_json = ?,
			progress = ?,
			started_at = ?,
			completed_at = ?,
			error_message = ?,
			can_retry = ?
		WHERE id = ?
	`,
		task.Status,
		string(currentFilesJSON),
		task.CurrentStage,
		string(stageResultsJSON),
		task.Progress,
		task.StartedAt,
		task.CompletedAt,
		task.ErrorMessage,
		boolToInt(task.CanRetry),
		task.ID,
	)
	return err
}

// DeleteTask 删除任务
func (s *SQLiteStore) DeleteTask(ctx context.Context, id int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	_, err := s.db.ExecContext(ctx, "DELETE FROM pipeline_tasks WHERE id = ?", id)
	return err
}

// ListTasks 列出任务
func (s *SQLiteStore) ListTasks(ctx context.Context, filter TaskFilter) ([]*PipelineTask, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	query := `
		SELECT id, status, record_info_json, pipeline_config_json,
			initial_files_json, current_files_json,
			current_stage, total_stages, stage_results_json,
			progress, created_at, started_at, completed_at,
			error_message, can_retry
		FROM pipeline_tasks
	`

	var conditions []string
	var args []interface{}

	if filter.Status != nil {
		conditions = append(conditions, "status = ?")
		args = append(args, string(*filter.Status))
	}

	if len(conditions) > 0 {
		query += " WHERE " + strings.Join(conditions, " AND ")
	}

	query += " ORDER BY created_at DESC"

	if filter.Limit > 0 {
		query += fmt.Sprintf(" LIMIT %d", filter.Limit)
		if filter.Offset > 0 {
			query += fmt.Sprintf(" OFFSET %d", filter.Offset)
		}
	}

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var tasks []*PipelineTask
	for rows.Next() {
		task, err := scanTaskFromRows(rows)
		if err != nil {
			logrus.WithError(err).Warn("failed to scan pipeline task")
			continue
		}
		tasks = append(tasks, task)
	}

	return tasks, rows.Err()
}

// GetPendingTasks 获取待执行的任务
func (s *SQLiteStore) GetPendingTasks(ctx context.Context, limit int) ([]*PipelineTask, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	rows, err := s.db.QueryContext(ctx, `
		SELECT id, status, record_info_json, pipeline_config_json,
			initial_files_json, current_files_json,
			current_stage, total_stages, stage_results_json,
			progress, created_at, started_at, completed_at,
			error_message, can_retry
		FROM pipeline_tasks
		WHERE status = ?
		ORDER BY created_at ASC
		LIMIT ?
	`, PipelineStatusPending, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var tasks []*PipelineTask
	for rows.Next() {
		task, err := scanTaskFromRows(rows)
		if err != nil {
			logrus.WithError(err).Warn("failed to scan pending pipeline task")
			continue
		}
		tasks = append(tasks, task)
	}

	return tasks, rows.Err()
}

// ResetRunningTasks 重置所有运行中的任务为待执行
func (s *SQLiteStore) ResetRunningTasks(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	_, err := s.db.ExecContext(ctx, `
		UPDATE pipeline_tasks SET status = ?, started_at = NULL
		WHERE status = ?
	`, PipelineStatusPending, PipelineStatusRunning)
	return err
}

// DeleteTasksByStatus 删除指定状态的所有任务
func (s *SQLiteStore) DeleteTasksByStatus(ctx context.Context, status PipelineStatus) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	result, err := s.db.ExecContext(ctx, "DELETE FROM pipeline_tasks WHERE status = ?", status)
	if err != nil {
		return 0, err
	}

	affected, _ := result.RowsAffected()
	return int(affected), nil
}

// Close 关闭存储
func (s *SQLiteStore) Close() error {
	return s.db.Close()
}

// scanTask 从单行扫描任务
func scanTask(row *sql.Row) (*PipelineTask, error) {
	var task PipelineTask
	var recordInfoJSON, pipelineConfigJSON, initialFilesJSON, currentFilesJSON, stageResultsJSON string
	var status string
	var startedAt, completedAt sql.NullTime
	var errorMessage sql.NullString
	var canRetry int

	err := row.Scan(
		&task.ID,
		&status,
		&recordInfoJSON,
		&pipelineConfigJSON,
		&initialFilesJSON,
		&currentFilesJSON,
		&task.CurrentStage,
		&task.TotalStages,
		&stageResultsJSON,
		&task.Progress,
		&task.CreatedAt,
		&startedAt,
		&completedAt,
		&errorMessage,
		&canRetry,
	)
	if err != nil {
		return nil, err
	}

	task.Status = PipelineStatus(status)
	task.CanRetry = canRetry != 0

	if startedAt.Valid {
		task.StartedAt = &startedAt.Time
	}
	if completedAt.Valid {
		task.CompletedAt = &completedAt.Time
	}
	if errorMessage.Valid {
		task.ErrorMessage = errorMessage.String
	}

	// 解析 JSON 字段
	if recordInfoJSON != "" {
		json.Unmarshal([]byte(recordInfoJSON), &task.RecordInfo)
	}
	if pipelineConfigJSON != "" {
		json.Unmarshal([]byte(pipelineConfigJSON), &task.PipelineConfig)
	}
	if initialFilesJSON != "" {
		json.Unmarshal([]byte(initialFilesJSON), &task.InitialFiles)
	}
	if currentFilesJSON != "" {
		json.Unmarshal([]byte(currentFilesJSON), &task.CurrentFiles)
	}
	if stageResultsJSON != "" {
		json.Unmarshal([]byte(stageResultsJSON), &task.StageResults)
	}

	return &task, nil
}

// scanTaskFromRows 从多行结果扫描任务
func scanTaskFromRows(rows *sql.Rows) (*PipelineTask, error) {
	var task PipelineTask
	var recordInfoJSON, pipelineConfigJSON, initialFilesJSON, currentFilesJSON, stageResultsJSON string
	var status string
	var startedAt, completedAt sql.NullTime
	var errorMessage sql.NullString
	var canRetry int

	err := rows.Scan(
		&task.ID,
		&status,
		&recordInfoJSON,
		&pipelineConfigJSON,
		&initialFilesJSON,
		&currentFilesJSON,
		&task.CurrentStage,
		&task.TotalStages,
		&stageResultsJSON,
		&task.Progress,
		&task.CreatedAt,
		&startedAt,
		&completedAt,
		&errorMessage,
		&canRetry,
	)
	if err != nil {
		return nil, err
	}

	task.Status = PipelineStatus(status)
	task.CanRetry = canRetry != 0

	if startedAt.Valid {
		task.StartedAt = &startedAt.Time
	}
	if completedAt.Valid {
		task.CompletedAt = &completedAt.Time
	}
	if errorMessage.Valid {
		task.ErrorMessage = errorMessage.String
	}

	// 解析 JSON 字段
	if recordInfoJSON != "" {
		json.Unmarshal([]byte(recordInfoJSON), &task.RecordInfo)
	}
	if pipelineConfigJSON != "" {
		json.Unmarshal([]byte(pipelineConfigJSON), &task.PipelineConfig)
	}
	if initialFilesJSON != "" {
		json.Unmarshal([]byte(initialFilesJSON), &task.InitialFiles)
	}
	if currentFilesJSON != "" {
		json.Unmarshal([]byte(currentFilesJSON), &task.CurrentFiles)
	}
	if stageResultsJSON != "" {
		json.Unmarshal([]byte(stageResultsJSON), &task.StageResults)
	}

	return &task, nil
}

// boolToInt 布尔值转整数
func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// MemoryStore 内存存储实现（用于测试）
type MemoryStore struct {
	tasks  map[int64]*PipelineTask
	nextID int64
	mu     sync.RWMutex
}

// NewMemoryStore 创建内存存储
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		tasks:  make(map[int64]*PipelineTask),
		nextID: 1,
	}
}

func (s *MemoryStore) CreateTask(ctx context.Context, task *PipelineTask) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	task.ID = s.nextID
	s.nextID++
	task.CreatedAt = time.Now()

	// 深拷贝
	taskCopy := *task
	s.tasks[task.ID] = &taskCopy

	return nil
}

func (s *MemoryStore) GetTask(ctx context.Context, id int64) (*PipelineTask, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	task, ok := s.tasks[id]
	if !ok {
		return nil, fmt.Errorf("task not found: %d", id)
	}

	taskCopy := *task
	return &taskCopy, nil
}

func (s *MemoryStore) UpdateTask(ctx context.Context, task *PipelineTask) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.tasks[task.ID]; !ok {
		return fmt.Errorf("task not found: %d", task.ID)
	}

	taskCopy := *task
	s.tasks[task.ID] = &taskCopy
	return nil
}

func (s *MemoryStore) DeleteTask(ctx context.Context, id int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	delete(s.tasks, id)
	return nil
}

func (s *MemoryStore) ListTasks(ctx context.Context, filter TaskFilter) ([]*PipelineTask, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var tasks []*PipelineTask
	for _, task := range s.tasks {
		if filter.Status != nil && task.Status != *filter.Status {
			continue
		}
		taskCopy := *task
		tasks = append(tasks, &taskCopy)
	}

	return tasks, nil
}

func (s *MemoryStore) GetPendingTasks(ctx context.Context, limit int) ([]*PipelineTask, error) {
	status := PipelineStatusPending
	return s.ListTasks(ctx, TaskFilter{Status: &status, Limit: limit})
}

func (s *MemoryStore) ResetRunningTasks(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	for _, task := range s.tasks {
		if task.Status == PipelineStatusRunning {
			task.Status = PipelineStatusPending
			task.StartedAt = nil
		}
	}
	return nil
}

func (s *MemoryStore) DeleteTasksByStatus(ctx context.Context, status PipelineStatus) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	count := 0
	for id, task := range s.tasks {
		if task.Status == status {
			delete(s.tasks, id)
			count++
		}
	}
	return count, nil
}

func (s *MemoryStore) Close() error {
	return nil
}
