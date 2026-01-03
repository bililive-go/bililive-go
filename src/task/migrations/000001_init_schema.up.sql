-- 创建系统元数据表
CREATE TABLE IF NOT EXISTS system_meta (
    key TEXT PRIMARY KEY,
    value TEXT NOT NULL,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

-- 创建任务表
CREATE TABLE IF NOT EXISTS tasks (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    type TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'pending',
    priority INTEGER NOT NULL DEFAULT 0,
    input_file TEXT NOT NULL,
    output_file TEXT,
    temp_files TEXT,
    live_id TEXT,
    room_name TEXT,
    host_name TEXT,
    platform TEXT,
    pre_task_id INTEGER,
    post_task_id INTEGER,
    metadata TEXT,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    started_at DATETIME,
    completed_at DATETIME,
    error_message TEXT,
    progress INTEGER DEFAULT 0,
    can_requeue INTEGER DEFAULT 1,
    FOREIGN KEY (pre_task_id) REFERENCES tasks(id),
    FOREIGN KEY (post_task_id) REFERENCES tasks(id)
);

-- 创建索引
CREATE INDEX IF NOT EXISTS idx_tasks_status ON tasks(status);
CREATE INDEX IF NOT EXISTS idx_tasks_priority ON tasks(priority DESC);
CREATE INDEX IF NOT EXISTS idx_tasks_live_id ON tasks(live_id);
