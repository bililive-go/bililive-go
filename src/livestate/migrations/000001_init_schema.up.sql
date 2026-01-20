-- 直播间基本信息表
CREATE TABLE IF NOT EXISTS live_rooms (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    live_id TEXT UNIQUE NOT NULL,           -- 直播间ID (types.LiveID)
    url TEXT NOT NULL,                      -- 直播间URL
    platform TEXT NOT NULL,                 -- 平台标识
    host_name TEXT DEFAULT '',              -- 主播名称
    room_name TEXT DEFAULT '',              -- 直播间名称
    last_start_time INTEGER DEFAULT 0,      -- 上次开播时间 (Unix timestamp)
    last_end_time INTEGER DEFAULT 0,        -- 上次关播时间 (Unix timestamp)
    is_recording INTEGER DEFAULT 0,         -- 上次关闭时是否正在录制 (0/1)
    last_heartbeat INTEGER DEFAULT 0,       -- 录制心跳时间戳 (Unix timestamp)
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

-- 开播/下播历史记录表
CREATE TABLE IF NOT EXISTS live_sessions (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    live_id TEXT NOT NULL,
    start_time INTEGER NOT NULL,            -- 开播时间 (Unix timestamp)
    end_time INTEGER DEFAULT 0,             -- 下播时间 (Unix timestamp)，0 表示仍在直播或崩溃未记录
    end_reason TEXT DEFAULT 'normal',       -- 结束原因: normal, crash, unknown
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY (live_id) REFERENCES live_rooms(live_id) ON DELETE CASCADE
);

-- 名称变更历史表
CREATE TABLE IF NOT EXISTS name_history (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    live_id TEXT NOT NULL,
    name_type TEXT NOT NULL,                -- 类型: host_name, room_name
    old_value TEXT DEFAULT '',
    new_value TEXT NOT NULL,
    changed_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY (live_id) REFERENCES live_rooms(live_id) ON DELETE CASCADE
);

-- 系统元数据表
CREATE TABLE IF NOT EXISTS system_meta (
    key TEXT PRIMARY KEY,
    value TEXT NOT NULL,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

-- 索引
CREATE INDEX IF NOT EXISTS idx_live_sessions_live_id ON live_sessions(live_id);
CREATE INDEX IF NOT EXISTS idx_live_sessions_start_time ON live_sessions(start_time);
CREATE INDEX IF NOT EXISTS idx_name_history_live_id ON name_history(live_id);
CREATE INDEX IF NOT EXISTS idx_live_rooms_is_recording ON live_rooms(is_recording);
