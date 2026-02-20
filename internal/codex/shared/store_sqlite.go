package shared

import (
	"context"
	"database/sql"
	_ "embed"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

//go:embed codex_schema.sql
var codexSchemaSQL string

type SQLiteCodexAccountStore struct {
	db *sql.DB
}

func OpenCodexAccountStore(dbPath string) (*SQLiteCodexAccountStore, error) {
	if strings.TrimSpace(dbPath) == "" {
		return nil, errors.New("empty db path")
	}
	if dir := filepath.Dir(dbPath); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return nil, fmt.Errorf("create db dir: %w", err)
		}
	}

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	db.SetConnMaxLifetime(0)

	s := &SQLiteCodexAccountStore{db: db}
	if err := s.initSchema(context.Background()); err != nil {
		_ = db.Close()
		return nil, err
	}
	return s, nil
}

func (s *SQLiteCodexAccountStore) initSchema(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, codexSchemaSQL)
	if err != nil {
		return fmt.Errorf("apply codex schema: %w", err)
	}
	return nil
}

func (s *SQLiteCodexAccountStore) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

// --- Account CRUD ---

func (s *SQLiteCodexAccountStore) ListAccounts(ctx context.Context) ([]CodexAccount, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT account_id, refresh_token, access_token, id_token, email, plan_type,
		       password, mfa_code, expires_at, status, weight, proxy_url,
		       cooldown_until, cooldown_reason,
		       usage_primary_used_pct, usage_primary_reset_secs, usage_primary_window_mins,
		       usage_secondary_used_pct, usage_secondary_reset_secs, usage_secondary_window_mins,
		       usage_primary_over_secondary_pct, usage_updated_at,
		       created_at, updated_at
		FROM codex_accounts ORDER BY created_at`)
	if err != nil {
		return nil, fmt.Errorf("list accounts: %w", err)
	}
	defer rows.Close()

	var accounts []CodexAccount
	for rows.Next() {
		a, err := scanAccount(rows)
		if err != nil {
			return nil, err
		}
		accounts = append(accounts, *a)
	}
	return accounts, rows.Err()
}

func (s *SQLiteCodexAccountStore) ListAccountsPage(ctx context.Context, offset, limit int) ([]CodexAccount, error) {
	if offset < 0 {
		offset = 0
	}
	if limit <= 0 {
		limit = 20
	}

	rows, err := s.db.QueryContext(ctx, `
		SELECT account_id, refresh_token, access_token, id_token, email, plan_type,
		       password, mfa_code, expires_at, status, weight, proxy_url,
		       cooldown_until, cooldown_reason,
		       usage_primary_used_pct, usage_primary_reset_secs, usage_primary_window_mins,
		       usage_secondary_used_pct, usage_secondary_reset_secs, usage_secondary_window_mins,
		       usage_primary_over_secondary_pct, usage_updated_at,
		       created_at, updated_at
		FROM codex_accounts
		ORDER BY created_at, account_id
		LIMIT ? OFFSET ?`, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("list accounts page: %w", err)
	}
	defer rows.Close()

	var accounts []CodexAccount
	for rows.Next() {
		a, err := scanAccount(rows)
		if err != nil {
			return nil, err
		}
		accounts = append(accounts, *a)
	}
	return accounts, rows.Err()
}

func (s *SQLiteCodexAccountStore) CountAccounts(ctx context.Context) (int, error) {
	var total int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(1) FROM codex_accounts`).Scan(&total); err != nil {
		return 0, fmt.Errorf("count accounts: %w", err)
	}
	return total, nil
}

func (s *SQLiteCodexAccountStore) GetByID(ctx context.Context, accountID string) (*CodexAccount, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT account_id, refresh_token, access_token, id_token, email, plan_type,
		       password, mfa_code, expires_at, status, weight, proxy_url,
		       cooldown_until, cooldown_reason,
		       usage_primary_used_pct, usage_primary_reset_secs, usage_primary_window_mins,
		       usage_secondary_used_pct, usage_secondary_reset_secs, usage_secondary_window_mins,
		       usage_primary_over_secondary_pct, usage_updated_at,
		       created_at, updated_at
		FROM codex_accounts WHERE account_id = ?`, accountID)
	return scanAccountRow(row)
}

func (s *SQLiteCodexAccountStore) GetByRefreshToken(ctx context.Context, rt string) (*CodexAccount, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT account_id, refresh_token, access_token, id_token, email, plan_type,
		       password, mfa_code, expires_at, status, weight, proxy_url,
		       cooldown_until, cooldown_reason,
		       usage_primary_used_pct, usage_primary_reset_secs, usage_primary_window_mins,
		       usage_secondary_used_pct, usage_secondary_reset_secs, usage_secondary_window_mins,
		       usage_primary_over_secondary_pct, usage_updated_at,
		       created_at, updated_at
		FROM codex_accounts WHERE refresh_token = ?`, rt)
	return scanAccountRow(row)
}

func (s *SQLiteCodexAccountStore) Insert(ctx context.Context, a *CodexAccount) error {
	if a == nil {
		return errors.New("nil account")
	}
	now := time.Now()
	if a.CreatedAt.IsZero() {
		a.CreatedAt = now
	}
	if a.UpdatedAt.IsZero() {
		a.UpdatedAt = now
	}

	var usagePrimPct, usageSecPct, usagePriOverSec float64
	var usagePrimReset, usagePrimWin, usageSecReset, usageSecWin int
	var usageUpdatedAt sql.NullTime
	if a.CodexUsage != nil {
		usagePrimPct = a.CodexUsage.PrimaryUsedPercent
		usagePrimReset = a.CodexUsage.PrimaryResetAfterSeconds
		usagePrimWin = a.CodexUsage.PrimaryWindowMinutes
		usageSecPct = a.CodexUsage.SecondaryUsedPercent
		usageSecReset = a.CodexUsage.SecondaryResetAfterSeconds
		usageSecWin = a.CodexUsage.SecondaryWindowMinutes
		usagePriOverSec = a.CodexUsage.PrimaryOverSecondaryPercent
		if !a.CodexUsage.UpdatedAt.IsZero() {
			usageUpdatedAt = sql.NullTime{Time: a.CodexUsage.UpdatedAt, Valid: true}
		}
	}

	_, err := s.db.ExecContext(ctx, `
		INSERT INTO codex_accounts (
			account_id, refresh_token, access_token, id_token, email, plan_type,
			password, mfa_code, expires_at, status, weight, proxy_url,
			cooldown_until, cooldown_reason,
			usage_primary_used_pct, usage_primary_reset_secs, usage_primary_window_mins,
			usage_secondary_used_pct, usage_secondary_reset_secs, usage_secondary_window_mins,
			usage_primary_over_secondary_pct, usage_updated_at,
			created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		a.AccountID, a.RefreshToken, a.AccessToken, a.IDToken, a.Email, a.PlanType,
		a.Password, a.MFACode,
		nullTime(a.ExpiresAt), string(a.Status), a.EffectiveWeight(), a.ProxyUrl,
		nullTime(a.CooldownUntil), a.CooldownReason,
		usagePrimPct, usagePrimReset, usagePrimWin,
		usageSecPct, usageSecReset, usageSecWin,
		usagePriOverSec, usageUpdatedAt,
		a.CreatedAt, a.UpdatedAt,
	)
	return err
}

func (s *SQLiteCodexAccountStore) Update(ctx context.Context, a *CodexAccount) error {
	if a == nil {
		return errors.New("nil account")
	}
	a.UpdatedAt = time.Now()

	var usagePrimPct, usageSecPct, usagePriOverSec float64
	var usagePrimReset, usagePrimWin, usageSecReset, usageSecWin int
	var usageUpdatedAt sql.NullTime
	if a.CodexUsage != nil {
		usagePrimPct = a.CodexUsage.PrimaryUsedPercent
		usagePrimReset = a.CodexUsage.PrimaryResetAfterSeconds
		usagePrimWin = a.CodexUsage.PrimaryWindowMinutes
		usageSecPct = a.CodexUsage.SecondaryUsedPercent
		usageSecReset = a.CodexUsage.SecondaryResetAfterSeconds
		usageSecWin = a.CodexUsage.SecondaryWindowMinutes
		usagePriOverSec = a.CodexUsage.PrimaryOverSecondaryPercent
		if !a.CodexUsage.UpdatedAt.IsZero() {
			usageUpdatedAt = sql.NullTime{Time: a.CodexUsage.UpdatedAt, Valid: true}
		}
	}

	_, err := s.db.ExecContext(ctx, `
		UPDATE codex_accounts SET
			refresh_token = ?, access_token = ?, id_token = ?, email = ?, plan_type = ?,
			password = ?, mfa_code = ?,
			expires_at = ?, status = ?, weight = ?, proxy_url = ?,
			cooldown_until = ?, cooldown_reason = ?,
			usage_primary_used_pct = ?, usage_primary_reset_secs = ?, usage_primary_window_mins = ?,
			usage_secondary_used_pct = ?, usage_secondary_reset_secs = ?, usage_secondary_window_mins = ?,
			usage_primary_over_secondary_pct = ?, usage_updated_at = ?,
			updated_at = ?
		WHERE account_id = ?`,
		a.RefreshToken, a.AccessToken, a.IDToken, a.Email, a.PlanType,
		a.Password, a.MFACode,
		nullTime(a.ExpiresAt), string(a.Status), a.EffectiveWeight(), a.ProxyUrl,
		nullTime(a.CooldownUntil), a.CooldownReason,
		usagePrimPct, usagePrimReset, usagePrimWin,
		usageSecPct, usageSecReset, usageSecWin,
		usagePriOverSec, usageUpdatedAt,
		a.UpdatedAt,
		a.AccountID,
	)
	return err
}

func (s *SQLiteCodexAccountStore) Delete(ctx context.Context, accountID string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, `DELETE FROM codex_account_stats WHERE account_id = ?`, accountID); err != nil {
		return fmt.Errorf("delete stats: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM codex_accounts WHERE account_id = ?`, accountID); err != nil {
		return fmt.Errorf("delete account: %w", err)
	}
	return tx.Commit()
}

// --- Hot-path partial updates ---

func (s *SQLiteCodexAccountStore) UpdateTokens(ctx context.Context, accountID, accessToken, idToken, refreshToken string, expiresAt time.Time) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE codex_accounts SET
			access_token = ?, id_token = ?, refresh_token = ?, expires_at = ?, updated_at = ?
		WHERE account_id = ?`,
		accessToken, idToken, refreshToken, nullTime(expiresAt), time.Now(), accountID)
	return err
}

func (s *SQLiteCodexAccountStore) UpdateStatus(ctx context.Context, accountID string, status CodexAccountStatus) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE codex_accounts SET status = ?, updated_at = ? WHERE account_id = ?`,
		string(status), time.Now(), accountID)
	return err
}

func (s *SQLiteCodexAccountStore) UpdateCooldown(ctx context.Context, accountID string, until time.Time, reason string) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE codex_accounts SET cooldown_until = ?, cooldown_reason = ?, updated_at = ? WHERE account_id = ?`,
		nullTime(until), reason, time.Now(), accountID)
	return err
}

func (s *SQLiteCodexAccountStore) UpdateUsageSnapshot(ctx context.Context, accountID string, snapshot *CodexUsageSnapshot) error {
	if snapshot == nil {
		return nil
	}
	_, err := s.db.ExecContext(ctx, `
		UPDATE codex_accounts SET
			usage_primary_used_pct = ?, usage_primary_reset_secs = ?, usage_primary_window_mins = ?,
			usage_secondary_used_pct = ?, usage_secondary_reset_secs = ?, usage_secondary_window_mins = ?,
			usage_primary_over_secondary_pct = ?, usage_updated_at = ?,
			updated_at = ?
		WHERE account_id = ?`,
		snapshot.PrimaryUsedPercent, snapshot.PrimaryResetAfterSeconds, snapshot.PrimaryWindowMinutes,
		snapshot.SecondaryUsedPercent, snapshot.SecondaryResetAfterSeconds, snapshot.SecondaryWindowMinutes,
		snapshot.PrimaryOverSecondaryPercent, nullTime(snapshot.UpdatedAt),
		time.Now(), accountID)
	return err
}

// --- Stats ---

func (s *SQLiteCodexAccountStore) InsertStat(ctx context.Context, stat *CodexAccountStat) error {
	if stat == nil {
		return nil
	}
	now := time.Now()
	if stat.Date == "" {
		stat.Date = now.Format("2006-01-02")
	}
	if stat.Hour < 0 {
		stat.Hour = now.Hour()
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO codex_account_stats (
			account_id, account_email, model, date, hour,
			input_tokens, output_tokens, total_tokens,
			status_code, status, error_type, duration_ms,
			primary_used_pct, secondary_used_pct, request_path
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		stat.AccountID, stat.AccountEmail, stat.Model, stat.Date, stat.Hour,
		stat.InputTokens, stat.OutputTokens, stat.TotalTokens,
		stat.StatusCode, stat.Status, stat.ErrorType, stat.DurationMs,
		stat.PrimaryUsedPct, stat.SecondaryUsedPct, stat.RequestPath,
	)
	return err
}

func (s *SQLiteCodexAccountStore) GetStatsSummary(ctx context.Context, accountID string, timeRange string) (*CodexAccountStatsSummary, error) {
	dateCond := codexStatsDateCondition(timeRange)
	row := s.db.QueryRowContext(ctx, fmt.Sprintf(`
		SELECT
			account_id,
			COALESCE(MAX(account_email), '') as account_email,
			COUNT(*) as request_count,
			COALESCE(SUM(input_tokens), 0) as input_tokens,
			COALESCE(SUM(output_tokens), 0) as output_tokens,
			COALESCE(SUM(total_tokens), 0) as total_tokens,
			SUM(CASE WHEN status_code >= 400 OR status = 'error' THEN 1 ELSE 0 END) as error_count,
			COALESCE(AVG(duration_ms), 0) as avg_duration_ms
		FROM codex_account_stats
		WHERE account_id = ? AND %s
		GROUP BY account_id`, dateCond), accountID)

	var sum CodexAccountStatsSummary
	err := row.Scan(&sum.AccountID, &sum.AccountEmail, &sum.RequestCount,
		&sum.InputTokens, &sum.OutputTokens, &sum.TotalTokens,
		&sum.ErrorCount, &sum.AvgDurationMs)
	if err == sql.ErrNoRows {
		return &CodexAccountStatsSummary{AccountID: accountID}, nil
	}
	if err != nil {
		return nil, err
	}
	return &sum, nil
}

func (s *SQLiteCodexAccountStore) GetStatsSummaryMap(ctx context.Context, accountIDs []string, timeRange string) (map[string]CodexAccountStatsSummary, error) {
	result := make(map[string]CodexAccountStatsSummary, len(accountIDs))
	if len(accountIDs) == 0 {
		return result, nil
	}

	uniqueIDs := make([]string, 0, len(accountIDs))
	seen := make(map[string]struct{}, len(accountIDs))
	for _, id := range accountIDs {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		uniqueIDs = append(uniqueIDs, id)
	}
	if len(uniqueIDs) == 0 {
		return result, nil
	}

	dateCond := codexStatsDateCondition(timeRange)
	placeholders := strings.TrimRight(strings.Repeat("?,", len(uniqueIDs)), ",")
	query := fmt.Sprintf(`
		SELECT
			account_id,
			COALESCE(MAX(account_email), '') as account_email,
			COUNT(*) as request_count,
			COALESCE(SUM(input_tokens), 0) as input_tokens,
			COALESCE(SUM(output_tokens), 0) as output_tokens,
			COALESCE(SUM(total_tokens), 0) as total_tokens,
			SUM(CASE WHEN status_code >= 400 OR status = 'error' THEN 1 ELSE 0 END) as error_count,
			COALESCE(AVG(duration_ms), 0) as avg_duration_ms
		FROM codex_account_stats
		WHERE account_id IN (%s) AND %s
		GROUP BY account_id`, placeholders, dateCond)

	args := make([]any, 0, len(uniqueIDs))
	for _, id := range uniqueIDs {
		args = append(args, id)
	}

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	for rows.Next() {
		var sum CodexAccountStatsSummary
		if err := rows.Scan(&sum.AccountID, &sum.AccountEmail, &sum.RequestCount,
			&sum.InputTokens, &sum.OutputTokens, &sum.TotalTokens,
			&sum.ErrorCount, &sum.AvgDurationMs); err != nil {
			return nil, err
		}
		result[sum.AccountID] = sum
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	for _, id := range uniqueIDs {
		if _, ok := result[id]; !ok {
			result[id] = CodexAccountStatsSummary{AccountID: id}
		}
	}
	return result, nil
}

func (s *SQLiteCodexAccountStore) GetAllStatsSummary(ctx context.Context, timeRange string) ([]CodexAccountStatsSummary, error) {
	dateCond := codexStatsDateCondition(timeRange)
	rows, err := s.db.QueryContext(ctx, fmt.Sprintf(`
		SELECT
			account_id,
			COALESCE(MAX(account_email), '') as account_email,
			COUNT(*) as request_count,
			COALESCE(SUM(input_tokens), 0) as input_tokens,
			COALESCE(SUM(output_tokens), 0) as output_tokens,
			COALESCE(SUM(total_tokens), 0) as total_tokens,
			SUM(CASE WHEN status_code >= 400 OR status = 'error' THEN 1 ELSE 0 END) as error_count,
			COALESCE(AVG(duration_ms), 0) as avg_duration_ms
		FROM codex_account_stats
		WHERE %s
		GROUP BY account_id
		ORDER BY request_count DESC`, dateCond))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []CodexAccountStatsSummary
	for rows.Next() {
		var sum CodexAccountStatsSummary
		if err := rows.Scan(&sum.AccountID, &sum.AccountEmail, &sum.RequestCount,
			&sum.InputTokens, &sum.OutputTokens, &sum.TotalTokens,
			&sum.ErrorCount, &sum.AvgDurationMs); err != nil {
			return nil, err
		}
		result = append(result, sum)
	}
	return result, rows.Err()
}

func (s *SQLiteCodexAccountStore) DeleteStats(ctx context.Context, accountID string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM codex_account_stats WHERE account_id = ?`, accountID)
	return err
}

// --- Helpers ---

func nullTime(t time.Time) sql.NullTime {
	if t.IsZero() {
		return sql.NullTime{}
	}
	return sql.NullTime{Time: t, Valid: true}
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanFromScanner(s rowScanner) (*CodexAccount, error) {
	var a CodexAccount
	var expiresAt, cooldownUntil, usageUpdatedAt sql.NullTime
	var status string
	var usagePrimPct, usageSecPct, usagePriOverSec float64
	var usagePrimReset, usagePrimWin, usageSecReset, usageSecWin int

	err := s.Scan(
		&a.AccountID, &a.RefreshToken, &a.AccessToken, &a.IDToken, &a.Email, &a.PlanType,
		&a.Password, &a.MFACode,
		&expiresAt, &status, &a.Weight, &a.ProxyUrl,
		&cooldownUntil, &a.CooldownReason,
		&usagePrimPct, &usagePrimReset, &usagePrimWin,
		&usageSecPct, &usageSecReset, &usageSecWin,
		&usagePriOverSec, &usageUpdatedAt,
		&a.CreatedAt, &a.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}

	a.Status = CodexAccountStatus(status)
	if expiresAt.Valid {
		a.ExpiresAt = expiresAt.Time
	}
	if cooldownUntil.Valid {
		a.CooldownUntil = cooldownUntil.Time
	}
	if usageUpdatedAt.Valid || usagePrimPct > 0 || usageSecPct > 0 {
		a.CodexUsage = &CodexUsageSnapshot{
			PrimaryUsedPercent:          usagePrimPct,
			PrimaryResetAfterSeconds:    usagePrimReset,
			PrimaryWindowMinutes:        usagePrimWin,
			SecondaryUsedPercent:        usageSecPct,
			SecondaryResetAfterSeconds:  usageSecReset,
			SecondaryWindowMinutes:      usageSecWin,
			PrimaryOverSecondaryPercent: usagePriOverSec,
		}
		if usageUpdatedAt.Valid {
			a.CodexUsage.UpdatedAt = usageUpdatedAt.Time
		}
	}
	return &a, nil
}

func scanAccount(rows *sql.Rows) (*CodexAccount, error) {
	return scanFromScanner(rows)
}

func scanAccountRow(row *sql.Row) (*CodexAccount, error) {
	a, err := scanFromScanner(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return a, err
}

func codexStatsDateCondition(timeRange string) string {
	now := time.Now()
	switch timeRange {
	case "today":
		return fmt.Sprintf("date = '%s'", now.Format("2006-01-02"))
	case "yesterday":
		return fmt.Sprintf("date = '%s'", now.AddDate(0, 0, -1).Format("2006-01-02"))
	case "week":
		return fmt.Sprintf("date >= '%s'", now.AddDate(0, 0, -int(now.Weekday())).Format("2006-01-02"))
	case "month":
		return fmt.Sprintf("date >= '%s'", time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, now.Location()).Format("2006-01-02"))
	default:
		return "1=1"
	}
}
