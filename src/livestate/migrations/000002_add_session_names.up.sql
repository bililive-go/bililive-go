-- 在 live_sessions 表中添加主播名称和房间名称字段
ALTER TABLE live_sessions ADD COLUMN host_name TEXT DEFAULT '';
ALTER TABLE live_sessions ADD COLUMN room_name TEXT DEFAULT '';
