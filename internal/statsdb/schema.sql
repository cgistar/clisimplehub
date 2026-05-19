-- Daily statistics table
CREATE TABLE IF NOT EXISTS usage_stats (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    endpoint_id TEXT NOT NULL,
    endpoint_name TEXT NOT NULL,
    provider_name TEXT,
    path TEXT NOT NULL,
    date TEXT NOT NULL,
    interface_type TEXT NOT NULL,
    target_headers TEXT NOT NULL,
    request_body TEXT,
    response_body TEXT,
    duration_ms INTEGER DEFAULT 0,
    status_code INTEGER NOT NULL,
    status TEXT NOT NULL,
    input_tokens INTEGER DEFAULT 0,
    output_tokens INTEGER DEFAULT 0,
    total_tokens INTEGER DEFAULT 0,
    cached_create INTEGER DEFAULT 0,
    cached_read INTEGER DEFAULT 0,
    reasoning INTEGER DEFAULT 0,
    create_time DATETIME DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_usage_stats_date ON usage_stats(date);
CREATE INDEX IF NOT EXISTS idx_usage_stats_provider ON usage_stats(provider_name, endpoint_id);
