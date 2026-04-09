package shared

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"
)

func TestSQLiteCodexAccountStoreListAccountsReadsPersistedTimes(t *testing.T) {
	t.Parallel()

	store, err := OpenCodexAccountStore(filepath.Join(t.TempDir(), "codex.db"))
	if err != nil {
		t.Fatalf("OpenCodexAccountStore: %v", err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Fatalf("store.Close: %v", err)
		}
	})

	now := time.Date(2026, 4, 9, 10, 11, 12, 0, time.UTC)
	account := CodexAccount{
		AccountID:    "acct-1",
		RefreshToken: "rt-1",
		AccessToken:  "at-1",
		IDToken:      "id-1",
		Email:        "test@example.com",
		PlanType:     "pro",
		Password:     "secret",
		MFACode:      "123456",
		ExpiresAt:    now.Add(2 * time.Hour),
		Status:       CodexStatusValid,
		Weight:       2,
		ProxyUrl:     "http://proxy",
		CooldownUntil: now.Add(30 * time.Minute),
		CooldownReason: "busy",
		CodexUsage: &CodexUsageSnapshot{
			PrimaryUsedPercent:          25,
			PrimaryResetAfterSeconds:    3600,
			PrimaryWindowMinutes:        60,
			SecondaryUsedPercent:        10,
			SecondaryResetAfterSeconds:  7200,
			SecondaryWindowMinutes:      120,
			PrimaryOverSecondaryPercent: 15,
			UpdatedAt:                   now,
		},
		CreatedAt: now,
		UpdatedAt: now,
	}

	if err := store.ReplaceAllAccounts(context.Background(), []CodexAccount{account}); err != nil {
		t.Fatalf("ReplaceAllAccounts: %v", err)
	}

	accounts, err := store.ListAccounts(context.Background())
	if err != nil {
		t.Fatalf("ListAccounts: %v", err)
	}
	if len(accounts) != 1 {
		t.Fatalf("len(accounts) = %d, want 1", len(accounts))
	}
	if !accounts[0].ExpiresAt.Equal(account.ExpiresAt) {
		t.Fatalf("ExpiresAt = %v, want %v", accounts[0].ExpiresAt, account.ExpiresAt)
	}
	if !accounts[0].CooldownUntil.Equal(account.CooldownUntil) {
		t.Fatalf("CooldownUntil = %v, want %v", accounts[0].CooldownUntil, account.CooldownUntil)
	}
	if !accounts[0].CreatedAt.Equal(account.CreatedAt) {
		t.Fatalf("CreatedAt = %v, want %v", accounts[0].CreatedAt, account.CreatedAt)
	}
	if !accounts[0].UpdatedAt.Equal(account.UpdatedAt) {
		t.Fatalf("UpdatedAt = %v, want %v", accounts[0].UpdatedAt, account.UpdatedAt)
	}
	if accounts[0].CodexUsage == nil {
		t.Fatal("CodexUsage = nil, want snapshot")
	}
	if !accounts[0].CodexUsage.UpdatedAt.Equal(account.CodexUsage.UpdatedAt) {
		t.Fatalf("CodexUsage.UpdatedAt = %v, want %v", accounts[0].CodexUsage.UpdatedAt, account.CodexUsage.UpdatedAt)
	}
}

func TestSQLiteCodexAccountStoreListAccountsReadsStringTimes(t *testing.T) {
	t.Parallel()

	store, err := OpenCodexAccountStore(filepath.Join(t.TempDir(), "codex.db"))
	if err != nil {
		t.Fatalf("OpenCodexAccountStore: %v", err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Fatalf("store.Close: %v", err)
		}
	})

	const (
		expiresAtRaw     = "2026-04-10 20:01:45 +0800 CST"
		cooldownUntilRaw = "2026-04-09 21:01:45 +0800 CST"
		usageUpdatedRaw  = "2026-04-09 08:11:12.123456 +0800 CST"
		createdAtRaw     = "2026-03-31 20:01:45.896771 +0800 CST"
		updatedAtRaw     = "2026-04-09 10:42:22.95915 +0800 CST m=+23849.601805584"
	)

	_, err = store.queue.ExecWrite(context.Background(), `
		INSERT INTO codex_accounts (
			account_id, refresh_token, access_token, id_token, email, plan_type,
			password, mfa_code, expires_at, status, weight, proxy_url,
			cooldown_until, cooldown_reason,
			usage_primary_used_pct, usage_primary_reset_secs, usage_primary_window_mins,
			usage_secondary_used_pct, usage_secondary_reset_secs, usage_secondary_window_mins,
			usage_primary_over_secondary_pct, usage_updated_at,
			created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		"acct-2", "rt-2", "at-2", "id-2", "demo@example.com", "pro",
		"secret", "654321", expiresAtRaw, string(CodexStatusValid), 1, "http://proxy",
		cooldownUntilRaw, "busy",
		25.0, 3600, 60,
		10.0, 7200, 120,
		15.0, usageUpdatedRaw,
		createdAtRaw, updatedAtRaw,
	)
	if err != nil {
		t.Fatalf("ExecWrite(insert string times): %v", err)
	}

	accounts, err := store.ListAccounts(context.Background())
	if err != nil {
		t.Fatalf("ListAccounts: %v", err)
	}
	if len(accounts) != 1 {
		t.Fatalf("len(accounts) = %d, want 1", len(accounts))
	}
	expiresAtWant := time.Date(2026, 4, 10, 20, 1, 45, 0, time.FixedZone("CST", 8*60*60))
	cooldownUntilWant := time.Date(2026, 4, 9, 21, 1, 45, 0, time.FixedZone("CST", 8*60*60))
	usageUpdatedWant := time.Date(2026, 4, 9, 8, 11, 12, 123456000, time.FixedZone("CST", 8*60*60))
	createdAtWant := time.Date(2026, 3, 31, 20, 1, 45, 896771000, time.FixedZone("CST", 8*60*60))
	updatedAtWant := time.Date(2026, 4, 9, 10, 42, 22, 959150000, time.FixedZone("CST", 8*60*60))

	if !accounts[0].ExpiresAt.Equal(expiresAtWant) {
		t.Fatalf("ExpiresAt = %v, want %v", accounts[0].ExpiresAt, expiresAtWant)
	}
	if !accounts[0].CooldownUntil.Equal(cooldownUntilWant) {
		t.Fatalf("CooldownUntil = %v, want %v", accounts[0].CooldownUntil, cooldownUntilWant)
	}
	if !accounts[0].CreatedAt.Equal(createdAtWant) {
		t.Fatalf("CreatedAt = %v, want %v", accounts[0].CreatedAt, createdAtWant)
	}
	if !accounts[0].UpdatedAt.Equal(updatedAtWant) {
		t.Fatalf("UpdatedAt = %v, want %v", accounts[0].UpdatedAt, updatedAtWant)
	}
	if accounts[0].CodexUsage == nil {
		t.Fatal("CodexUsage = nil, want snapshot")
	}
	if !accounts[0].CodexUsage.UpdatedAt.Equal(usageUpdatedWant) {
		t.Fatalf("CodexUsage.UpdatedAt = %v, want %v", accounts[0].CodexUsage.UpdatedAt, usageUpdatedWant)
	}
}

func TestSQLiteCodexAccountStoreReplaceAllAccountsPersistsRFC3339Times(t *testing.T) {
	t.Parallel()

	store, err := OpenCodexAccountStore(filepath.Join(t.TempDir(), "codex.db"))
	if err != nil {
		t.Fatalf("OpenCodexAccountStore: %v", err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Fatalf("store.Close: %v", err)
		}
	})

	zone := time.FixedZone("CST", 8*60*60)
	account := CodexAccount{
		AccountID:      "acct-3",
		RefreshToken:   "rt-3",
		ExpiresAt:      time.Date(2026, 4, 10, 20, 1, 45, 123456000, zone),
		CooldownUntil:  time.Date(2026, 4, 9, 21, 1, 45, 0, zone),
		CreatedAt:      time.Date(2026, 3, 31, 20, 1, 45, 896771000, zone),
		UpdatedAt:      time.Date(2026, 4, 9, 10, 42, 22, 959150000, zone),
		Status:         CodexStatusValid,
		Weight:         1,
		ProxyUrl:       "http://proxy",
		CooldownReason: "busy",
		CodexUsage: &CodexUsageSnapshot{
			UpdatedAt: time.Date(2026, 4, 9, 10, 42, 22, 959120000, zone),
		},
	}

	if err := store.ReplaceAllAccounts(context.Background(), []CodexAccount{account}); err != nil {
		t.Fatalf("ReplaceAllAccounts: %v", err)
	}

	var expiresAt, cooldownUntil, usageUpdatedAt, createdAt, updatedAt sql.NullString
	err = store.db.QueryRowContext(context.Background(), `
		SELECT expires_at, cooldown_until, usage_updated_at, created_at, updated_at
		FROM codex_accounts
		WHERE account_id = ?`, account.AccountID,
	).Scan(&expiresAt, &cooldownUntil, &usageUpdatedAt, &createdAt, &updatedAt)
	if err != nil {
		t.Fatalf("QueryRowContext(scan times): %v", err)
	}

	if !expiresAt.Valid || expiresAt.String != "2026-04-10T20:01:45.123456+08:00" {
		t.Fatalf("expires_at = %q, want RFC3339Nano", expiresAt.String)
	}
	if !cooldownUntil.Valid || cooldownUntil.String != "2026-04-09T21:01:45+08:00" {
		t.Fatalf("cooldown_until = %q, want RFC3339Nano", cooldownUntil.String)
	}
	if !usageUpdatedAt.Valid || usageUpdatedAt.String != "2026-04-09T10:42:22.95912+08:00" {
		t.Fatalf("usage_updated_at = %q, want RFC3339Nano", usageUpdatedAt.String)
	}
	if !createdAt.Valid || createdAt.String != "2026-03-31T20:01:45.896771+08:00" {
		t.Fatalf("created_at = %q, want RFC3339Nano", createdAt.String)
	}
	if !updatedAt.Valid || updatedAt.String != "2026-04-09T10:42:22.95915+08:00" {
		t.Fatalf("updated_at = %q, want RFC3339Nano", updatedAt.String)
	}
}

func TestOpenCodexAccountStoreNormalizesLegacyTimeStrings(t *testing.T) {
	t.Parallel()

	dbPath := filepath.Join(t.TempDir(), "codex.db")

	store, err := OpenCodexAccountStore(dbPath)
	if err != nil {
		t.Fatalf("OpenCodexAccountStore(first): %v", err)
	}
	_, err = store.queue.ExecWrite(context.Background(), `
		INSERT INTO codex_accounts (
			account_id, refresh_token, expires_at, cooldown_until, usage_updated_at, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		"acct-legacy", "rt-legacy",
		"2026-04-10 20:01:45 +0800 CST",
		"2026-04-09 21:01:45 +0800 CST",
		"2026-04-09 10:42:22.95912 +0800 CST",
		"2026-03-31 20:01:45.896771 +0800 CST",
		"2026-04-09 10:42:22.95915 +0800 CST m=+23849.601805584",
	)
	if err != nil {
		t.Fatalf("ExecWrite(insert legacy row): %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("store.Close(first): %v", err)
	}

	store, err = OpenCodexAccountStore(dbPath)
	if err != nil {
		t.Fatalf("OpenCodexAccountStore(second): %v", err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Fatalf("store.Close(second): %v", err)
		}
	})

	var expiresAt, cooldownUntil, usageUpdatedAt, createdAt, updatedAt sql.NullString
	err = store.db.QueryRowContext(context.Background(), `
		SELECT expires_at, cooldown_until, usage_updated_at, created_at, updated_at
		FROM codex_accounts
		WHERE account_id = ?`, "acct-legacy",
	).Scan(&expiresAt, &cooldownUntil, &usageUpdatedAt, &createdAt, &updatedAt)
	if err != nil {
		t.Fatalf("QueryRowContext(scan normalized times): %v", err)
	}

	if !expiresAt.Valid || expiresAt.String != "2026-04-10T20:01:45+08:00" {
		t.Fatalf("expires_at = %q, want normalized RFC3339", expiresAt.String)
	}
	if !cooldownUntil.Valid || cooldownUntil.String != "2026-04-09T21:01:45+08:00" {
		t.Fatalf("cooldown_until = %q, want normalized RFC3339", cooldownUntil.String)
	}
	if !usageUpdatedAt.Valid || usageUpdatedAt.String != "2026-04-09T10:42:22.95912+08:00" {
		t.Fatalf("usage_updated_at = %q, want normalized RFC3339", usageUpdatedAt.String)
	}
	if !createdAt.Valid || createdAt.String != "2026-03-31T20:01:45.896771+08:00" {
		t.Fatalf("created_at = %q, want normalized RFC3339", createdAt.String)
	}
	if !updatedAt.Valid || updatedAt.String != "2026-04-09T10:42:22.95915+08:00" {
		t.Fatalf("updated_at = %q, want normalized RFC3339", updatedAt.String)
	}
}
