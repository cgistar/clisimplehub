package claude

import (
	"os"
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
	credsPath := ""

	if configPath, err := getConfig("configPath"); err == nil && strings.TrimSpace(configPath) != "" {
		credsPath = filepath.Join(filepath.Dir(kiroShared.ExpandTilde(configPath)), filepath.Base(kiroShared.GetDefaultKiroCredentialsPath()))
	}
	if strings.TrimSpace(credsPath) == "" {
		credsPath = kiroShared.GetDefaultKiroCredentialsPath()
	}

	expanded := kiroShared.ExpandTilde(credsPath)
	if _, err := os.Stat(expanded); err != nil {
		return false
	}

	creds, err := kiroShared.LoadKiroCredentials(expanded)
	if err != nil || creds == nil {
		return false
	}

	if strings.TrimSpace(creds.RefreshToken) == "" {
		return false
	}

	if strings.TrimSpace(creds.AccessToken) == "" {
		return false
	}

	if !creds.ExpiresAt.IsZero() && time.Now().After(creds.ExpiresAt) {
		return false
	}

	return true
}
