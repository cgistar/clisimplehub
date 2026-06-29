CREATE TABLE IF NOT EXISTS codex_accounts (
    id             TEXT PRIMARY KEY,
    account_id     TEXT NOT NULL DEFAULT '',
    refresh_token  TEXT NOT NULL DEFAULT '',
    access_token   TEXT NOT NULL DEFAULT '',
    id_token       TEXT NOT NULL DEFAULT '',
    email          TEXT NOT NULL DEFAULT '',
    plan_type      TEXT NOT NULL DEFAULT '',
    enabled        INTEGER NOT NULL DEFAULT 1,
    websockets     INTEGER NOT NULL DEFAULT 0,
    password       TEXT NOT NULL DEFAULT '',
    mfa_code       TEXT NOT NULL DEFAULT '',
    expires_at     DATETIME,
    status         TEXT NOT NULL DEFAULT 'valid',
    weight         INTEGER NOT NULL DEFAULT 1,
    proxy_url      TEXT NOT NULL DEFAULT '',
    cooldown_until DATETIME,
    cooldown_reason TEXT NOT NULL DEFAULT '',
    usage_primary_used_pct           REAL NOT NULL DEFAULT 0,
    usage_primary_reset_secs         INTEGER NOT NULL DEFAULT 0,
    usage_primary_window_mins        INTEGER NOT NULL DEFAULT 0,
    usage_secondary_used_pct         REAL NOT NULL DEFAULT 0,
    usage_secondary_reset_secs       INTEGER NOT NULL DEFAULT 0,
    usage_secondary_window_mins      INTEGER NOT NULL DEFAULT 0,
    usage_primary_over_secondary_pct REAL NOT NULL DEFAULT 0,
    usage_reset_credits_available_count INTEGER NOT NULL DEFAULT 0,
    usage_updated_at                 DATETIME,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS codex_account_stats (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    account_id      TEXT NOT NULL,
    account_email   TEXT NOT NULL DEFAULT '',
    model           TEXT NOT NULL DEFAULT '',
    date            TEXT NOT NULL,
    hour            INTEGER NOT NULL DEFAULT 0,
    input_tokens    INTEGER NOT NULL DEFAULT 0,
    output_tokens   INTEGER NOT NULL DEFAULT 0,
    total_tokens    INTEGER NOT NULL DEFAULT 0,
    cached_tokens   INTEGER NOT NULL DEFAULT 0,
    cache_read_tokens INTEGER NOT NULL DEFAULT 0,
    cache_creation_tokens INTEGER NOT NULL DEFAULT 0,
    reasoning_tokens INTEGER NOT NULL DEFAULT 0,
    status_code     INTEGER NOT NULL DEFAULT 0,
    status          TEXT NOT NULL DEFAULT 'unknown',
    error_type      TEXT NOT NULL DEFAULT '',
    duration_ms     INTEGER NOT NULL DEFAULT 0,
    primary_used_pct     REAL,
    secondary_used_pct   REAL,
    request_path    TEXT NOT NULL DEFAULT '',
    create_time     DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_codex_accounts_refresh_token ON codex_accounts(refresh_token);
CREATE INDEX IF NOT EXISTS idx_codex_accounts_account_id ON codex_accounts(account_id);
CREATE INDEX IF NOT EXISTS idx_codex_stats_account_date ON codex_account_stats(account_id, date);
CREATE INDEX IF NOT EXISTS idx_codex_stats_date ON codex_account_stats(date);
