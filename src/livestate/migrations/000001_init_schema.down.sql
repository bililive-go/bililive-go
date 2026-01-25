-- 回滚：删除所有表
DROP INDEX IF EXISTS idx_live_rooms_is_recording;
DROP INDEX IF EXISTS idx_name_history_live_id;
DROP INDEX IF EXISTS idx_live_sessions_start_time;
DROP INDEX IF EXISTS idx_live_sessions_live_id;

DROP TABLE IF EXISTS name_history;
DROP TABLE IF EXISTS live_sessions;
DROP TABLE IF EXISTS system_meta;
DROP TABLE IF EXISTS live_rooms;
