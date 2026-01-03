-- 删除索引
DROP INDEX IF EXISTS idx_tasks_live_id;
DROP INDEX IF EXISTS idx_tasks_priority;
DROP INDEX IF EXISTS idx_tasks_status;

-- 删除任务表
DROP TABLE IF EXISTS tasks;

-- 删除系统元数据表
DROP TABLE IF EXISTS system_meta;
