package shared

import (
	"os"
	"strings"
	"time"
)

// SyncTokenToKiroJson syncs auth fields from credentials into kiro.json at the given path.
func SyncTokenToKiroJson(kiroJsonPath string, previousRefreshToken string, creds *KiroCredentials) error {
	if strings.TrimSpace(kiroJsonPath) == "" || creds == nil {
		return nil
	}

	nextRefreshToken := strings.TrimSpace(creds.RefreshToken)
	if nextRefreshToken == "" {
		return nil
	}

	if _, err := os.Stat(kiroJsonPath); err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}

	multiConfig, err := LoadKiroMultiConfig(kiroJsonPath)
	if err != nil || multiConfig == nil {
		return err
	}

	prevRefreshToken := strings.TrimSpace(previousRefreshToken)
	var account *KiroAccount
	if prevRefreshToken != "" && prevRefreshToken != nextRefreshToken {
		account = multiConfig.FindAccountByRefreshToken(prevRefreshToken)
	}
	if account == nil {
		account = multiConfig.FindAccountByRefreshToken(nextRefreshToken)
	}
	if account == nil {
		return nil
	}

	// Update auth fields only; preserve other per-account config/usage fields.
	account.AccessToken = creds.AccessToken
	account.ExpiresAt = creds.ExpiresAt
	if strings.TrimSpace(creds.MachineID) != "" {
		account.MachineId = strings.TrimSpace(creds.MachineID)
	}

	authMethod := strings.ToLower(strings.TrimSpace(creds.AuthMethod))
	switch authMethod {
	case "social":
		account.ClientId = ""
		account.ClientSecret = ""
	case "idc":
		account.ProfileArn = ""
	}

	// refreshToken is the primary key; update it if the refresh flow rotates the token.
	account.RefreshToken = nextRefreshToken

	if authMethod == "idc" && strings.TrimSpace(creds.ProfileArn) == "" {
		// IdC does not rely on profileArn; keep it absent unless explicitly provided.
	} else if strings.TrimSpace(creds.ProfileArn) != "" {
		account.ProfileArn = creds.ProfileArn
	}

	if creds.Region != "" {
		account.Region = creds.Region
	}
	if creds.AuthMethod != "" {
		account.AuthMethod = creds.AuthMethod
	}
	if creds.Provider != "" {
		account.Provider = creds.Provider
	}
	if creds.ClientId != "" && authMethod == "idc" {
		account.ClientId = creds.ClientId
	}
	if creds.ClientSecret != "" && authMethod == "idc" {
		account.ClientSecret = creds.ClientSecret
	}
	if creds.Status != "" {
		account.Status = creds.Status
	}

	// Keep file consistent: no access token implies no expiry.
	if strings.TrimSpace(creds.AccessToken) == "" {
		account.ExpiresAt = time.Time{}
	}

	// If we matched by the previous refresh token, also update the active marker.
	if prevRefreshToken != "" && prevRefreshToken != nextRefreshToken && strings.TrimSpace(multiConfig.ActiveRefreshToken) == prevRefreshToken {
		multiConfig.ActiveRefreshToken = nextRefreshToken
	}

	return SaveKiroMultiConfig(kiroJsonPath, multiConfig)
}
