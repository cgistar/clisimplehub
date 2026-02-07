package claude

import (
	"path/filepath"
	"strings"
	"time"

	kiroShared "clisimplehub/internal/transformer/kiro/shared"
)

// HasValidLocalAccessToken reports whether local Kiro credentials exist and contain
// a non-empty accessToken that is not expired (if expiresAt is present).
//
// This is intentionally a local-only check (no token refresh / network calls), so callers
// can use it to decide whether to surface Kiro transformer options in UIs.
func HasValidLocalAccessToken() bool {
	kiroJsonPath := ""
	if configPath, err := getConfig("configPath"); err == nil && strings.TrimSpace(configPath) != "" {
		kiroJsonPath = filepath.Join(filepath.Dir(kiroShared.ExpandTilde(configPath)), filepath.Base(kiroShared.GetDefaultKiroMultiConfigPath()))
	}
	if strings.TrimSpace(kiroJsonPath) == "" {
		return false
	}

	mc, err := kiroShared.LoadKiroMultiConfig(kiroJsonPath)
	if err != nil || mc == nil {
		return false
	}

	account := mc.GetActiveAccount()
	if account == nil {
		return false
	}

	if strings.TrimSpace(account.RefreshToken) == "" {
		return false
	}

	if strings.TrimSpace(account.AccessToken) == "" {
		return false
	}

	if !account.ExpiresAt.IsZero() && time.Now().After(account.ExpiresAt) {
		return false
	}

	return true
}
