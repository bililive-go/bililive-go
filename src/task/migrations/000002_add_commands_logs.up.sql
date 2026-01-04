-- 在 tasks 表中添加命令和日志字段
ALTER TABLE tasks ADD COLUMN commands TEXT DEFAULT '[]';
ALTER TABLE tasks ADD COLUMN logs TEXT DEFAULT '';
