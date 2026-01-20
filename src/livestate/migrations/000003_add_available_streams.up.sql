-- 可用流信息表（存储最近获取的直播间可用流列表）
CREATE TABLE IF NOT EXISTS available_streams (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    live_id TEXT NOT NULL,                  -- 直播间ID
    stream_index INTEGER NOT NULL,          -- 流序号（优先级）
    quality TEXT NOT NULL,                  -- 清晰度标识（必填）: 1080p, 720p, 原画
    attributes TEXT DEFAULT '{}',           -- 流属性键值对（JSON格式）: {"format": "flv", "codec": "h264", "画质": "原画"}
    updated_at INTEGER DEFAULT 0,           -- 更新时间 (Unix timestamp)
    FOREIGN KEY (live_id) REFERENCES live_rooms(live_id) ON DELETE CASCADE
);

-- 索引
CREATE INDEX IF NOT EXISTS idx_available_streams_live_id ON available_streams(live_id);
