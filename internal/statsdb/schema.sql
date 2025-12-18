-- Daily statistics table
CREATE TABLE IF NOT EXISTS vendor_stats (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    vendor_id TEXT NOT NULL,
    vendor_name TEXT NOT NULL,
    endpoint_id TEXT NOT NULL,
    endpoint_name TEXT NOT NULL,
    path TEXT NOT NULL,
    date TEXT NOT NULL,
    interface_type TEXT NOT NULL,
    target_headers TEXT NOT NULL,
    duration_ms INTEGER DEFAULT 0,
    status_code INTEGER NOT NULL,
    status TEXT NOT NULL,
    input_tokens INTEGER DEFAULT 0,
    output_tokens INTEGER DEFAULT 0,
    cached_create INTEGER DEFAULT 0,
    cached_read INTEGER DEFAULT 0,
    reasoning INTEGER DEFAULT 0,
    create_time DATETIME DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_vendor_stats_date ON vendor_stats(date);
CREATE INDEX IF NOT EXISTS idx_vendor_stats_vendor ON vendor_stats(vendor_id, endpoint_id);

