CREATE TABLE IF NOT EXISTS io_stats (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    timestamp INTEGER NOT NULL,
    stat_type TEXT NOT NULL,
    live_id TEXT,
    platform TEXT,
    speed INTEGER NOT NULL DEFAULT 0,
    total_bytes INTEGER NOT NULL DEFAULT 0
);

CREATE INDEX IF NOT EXISTS idx_io_stats_timestamp ON io_stats(timestamp);
CREATE INDEX IF NOT EXISTS idx_io_stats_type_time ON io_stats(stat_type, timestamp);
CREATE INDEX IF NOT EXISTS idx_io_stats_live_time ON io_stats(live_id, timestamp);

CREATE TABLE IF NOT EXISTS request_status (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    timestamp INTEGER NOT NULL,
    live_id TEXT NOT NULL,
    platform TEXT NOT NULL,
    success INTEGER NOT NULL,
    error_message TEXT
);

CREATE INDEX IF NOT EXISTS idx_request_status_timestamp ON request_status(timestamp);
CREATE INDEX IF NOT EXISTS idx_request_status_live ON request_status(live_id, timestamp);
CREATE INDEX IF NOT EXISTS idx_request_status_platform ON request_status(platform, timestamp);

CREATE TABLE IF NOT EXISTS disk_io_stats (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    timestamp INTEGER NOT NULL,
    device_name TEXT NOT NULL,
    read_count INTEGER NOT NULL DEFAULT 0,
    write_count INTEGER NOT NULL DEFAULT 0,
    read_bytes INTEGER NOT NULL DEFAULT 0,
    write_bytes INTEGER NOT NULL DEFAULT 0,
    read_time_ms INTEGER NOT NULL DEFAULT 0,
    write_time_ms INTEGER NOT NULL DEFAULT 0,
    avg_read_latency REAL NOT NULL DEFAULT 0,
    avg_write_latency REAL NOT NULL DEFAULT 0,
    read_speed INTEGER NOT NULL DEFAULT 0,
    write_speed INTEGER NOT NULL DEFAULT 0
);

CREATE INDEX IF NOT EXISTS idx_disk_io_timestamp ON disk_io_stats(timestamp);
CREATE INDEX IF NOT EXISTS idx_disk_io_device_time ON disk_io_stats(device_name, timestamp);

CREATE TABLE IF NOT EXISTS memory_stats (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    timestamp INTEGER NOT NULL,
    category TEXT NOT NULL,
    rss INTEGER NOT NULL DEFAULT 0,
    vms INTEGER NOT NULL DEFAULT 0,
    alloc INTEGER NOT NULL DEFAULT 0,
    sys INTEGER NOT NULL DEFAULT 0,
    num_gc INTEGER NOT NULL DEFAULT 0
);

CREATE INDEX IF NOT EXISTS idx_memory_stats_timestamp ON memory_stats(timestamp);
CREATE INDEX IF NOT EXISTS idx_memory_stats_category_time ON memory_stats(category, timestamp);
