package xaiplugin

import (
	"encoding/json"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	xaiShared "clisimplehub/internal/xai/shared"
)

func xaiJsonPathFromConfig(configPath string) string {
	configPath = strings.TrimSpace(configPath)
	if configPath == "" {
		return xaiShared.GetDefaultXaiMultiConfigPath()
	}
	return filepath.Join(filepath.Dir(configPath), xaiShared.GetDefaultXaiMultiConfigPath())
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func accountToDTO(account xaiShared.XaiAccount, activeID string) map[string]any {
	cooldownRemaining := 0
	if account.IsCoolingDown() {
		cooldownRemaining = int(time.Until(account.CooldownUntil).Seconds())
		if cooldownRemaining < 0 {
			cooldownRemaining = 0
		}
	}

	dto := map[string]any{
		"id":                account.ID,
		"email":             account.Email,
		"subject":           account.Subject,
		"accessToken":       account.AccessToken,
		"refreshToken":      account.RefreshToken,
		"idToken":           account.IDToken,
		"authKind":          account.AuthKind,
		"apiKey":            account.APIKey,
		"sso":               account.SSO,
		"enabled":           account.Enabled,
		"websockets":        account.WebsocketsEnabled(),
		"usingApi":          account.UsingAPIEnabled(),
		"weight":            account.EffectiveWeight(),
		"proxyUrl":          account.ProxyUrl,
		"customHeaders":     account.CustomHeaders,
		"pool":              account.Pool,
		"status":            string(account.Status),
		"cooldownReason":    account.CooldownReason,
		"cooldownRemaining": cooldownRemaining,
		"isActive":          strings.TrimSpace(account.ID) != "" && strings.TrimSpace(account.ID) == strings.TrimSpace(activeID),
	}
	if account.Quota != nil {
		dto["quota"] = quotaToDTO(account.Quota)
	}
	if !account.ExpiresAt.IsZero() {
		dto["expiresAt"] = account.ExpiresAt.UTC().Format(time.RFC3339)
	}
	if !account.LastRefresh.IsZero() {
		dto["lastRefresh"] = account.LastRefresh.UTC().Format(time.RFC3339)
	}
	if !account.LastQuotaSync.IsZero() {
		dto["lastQuotaSync"] = account.LastQuotaSync.UTC().Format(time.RFC3339)
	}
	if !account.CooldownUntil.IsZero() {
		dto["cooldownUntil"] = account.CooldownUntil.UTC().Format(time.RFC3339)
	}
	if !account.CreatedAt.IsZero() {
		dto["createdAt"] = account.CreatedAt.UTC().Format(time.RFC3339)
	}
	if !account.UpdatedAt.IsZero() {
		dto["updatedAt"] = account.UpdatedAt.UTC().Format(time.RFC3339)
	}
	return dto
}

func quotaToDTO(q *xaiShared.XaiQuota) map[string]any {
	if q == nil {
		return nil
	}
	out := map[string]any{}
	if w := quotaWindowToDTO(q.Auto); w != nil {
		out["auto"] = w
	}
	if w := quotaWindowToDTO(q.Fast); w != nil {
		out["fast"] = w
	}
	if w := quotaWindowToDTO(q.Expert); w != nil {
		out["expert"] = w
	}
	if w := quotaWindowToDTO(q.Heavy); w != nil {
		out["heavy"] = w
	}
	if w := quotaWindowToDTO(q.Grok43); w != nil {
		out["grok43"] = w
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func quotaWindowToDTO(w *xaiShared.XaiQuotaWindow) map[string]any {
	if w == nil {
		return nil
	}
	return map[string]any{
		"remaining":     w.Remaining,
		"total":         w.Total,
		"windowSeconds": w.WindowSeconds,
		"resetAt":       w.ResetAt,
		"syncedAt":      w.SyncedAt,
	}
}

func parseTimeString(value string) time.Time {
	value = strings.TrimSpace(value)
	if value == "" {
		return time.Time{}
	}
	if t, err := time.Parse(time.RFC3339, value); err == nil {
		return t
	}
	if t, err := time.Parse(time.RFC3339Nano, value); err == nil {
		return t
	}
	return time.Time{}
}
