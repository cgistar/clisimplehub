package shared

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// LoadKiroCredentials loads credentials from a JSON file
func LoadKiroCredentials(path string) (*KiroCredentials, error) {
	expandedPath := ExpandTilde(path)

	// Check file permissions (Unix only)
	if runtime.GOOS != "windows" {
		if info, err := os.Stat(expandedPath); err == nil {
			if info.Mode().Perm()&0o077 != 0 {
				return nil, fmt.Errorf("insecure kiro credentials file permissions: path=%s mode=%o (expected 0600)", expandedPath, info.Mode().Perm())
			}
		}
	}

	data, err := os.ReadFile(expandedPath)
	if err != nil {
		return nil, err
	}

	var raw credentialsJSON
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, err
	}

	// Validate required fields
	if raw.RefreshToken == "" {
		return nil, errors.New("refreshToken is required in kiro credentials")
	}

	creds := &KiroCredentials{
		AccessToken:  raw.AccessToken,
		RefreshToken: raw.RefreshToken,
		ProfileArn:   raw.ProfileArn,
		Region:       raw.Region,
		AuthMethod:   raw.AuthMethod,
		Provider:     raw.Provider,
	}

	// Parse expiresAt
	if raw.ExpiresAt != "" {
		expiresAt, err := parseExpiresAt(raw.ExpiresAt)
		if err == nil {
			creds.ExpiresAt = expiresAt
		}
	}

	// Default region
	if creds.Region == "" {
		creds.Region = "us-east-1"
	}

	return creds, nil
}

// SaveKiroCredentials saves credentials to a JSON file
func SaveKiroCredentials(path string, creds *KiroCredentials) error {
	expandedPath := ExpandTilde(path)
	if strings.TrimSpace(expandedPath) == "" {
		return errors.New("empty credentials path")
	}
	if creds == nil {
		return errors.New("nil credentials")
	}

	// Read existing data to preserve other fields
	existingData := make(map[string]interface{})
	if data, err := os.ReadFile(expandedPath); err == nil {
		_ = json.Unmarshal(data, &existingData)
	}

	prevRefreshToken := strings.TrimSpace(sharedString(existingData["refreshToken"]))
	nextRefreshToken := strings.TrimSpace(creds.RefreshToken)
	refreshTokenChanged := prevRefreshToken != "" && nextRefreshToken != "" && prevRefreshToken != nextRefreshToken

	// If refresh token was changed by config update, clear cached auth state unless the caller
	// is also providing fresh values (e.g. token refresh flow).
	if refreshTokenChanged && strings.TrimSpace(creds.AccessToken) == "" {
		existingData["accessToken"] = ""
		existingData["expiresAt"] = ""
		if strings.TrimSpace(creds.ProfileArn) == "" {
			existingData["profileArn"] = ""
		}
		if strings.TrimSpace(creds.AuthMethod) == "" {
			existingData["authMethod"] = ""
		}
		if strings.TrimSpace(creds.Provider) == "" {
			existingData["provider"] = ""
		}
	}

	// Update fields
	existingData["accessToken"] = creds.AccessToken
	existingData["refreshToken"] = creds.RefreshToken
	if creds.ProfileArn != "" {
		existingData["profileArn"] = creds.ProfileArn
	}
	if !creds.ExpiresAt.IsZero() {
		existingData["expiresAt"] = creds.ExpiresAt.Format(time.RFC3339Nano)
	} else if strings.TrimSpace(creds.AccessToken) == "" {
		// Keep file consistent: no access token implies no expiry.
		existingData["expiresAt"] = ""
	}
	if creds.Region != "" {
		existingData["region"] = creds.Region
	}
	if creds.AuthMethod != "" {
		existingData["authMethod"] = creds.AuthMethod
	}
	if creds.Provider != "" {
		existingData["provider"] = creds.Provider
	}

	// Ensure directory exists
	dir := filepath.Dir(expandedPath)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}

	// Marshal with indentation
	data, err := json.MarshalIndent(existingData, "", "  ")
	if err != nil {
		return err
	}

	// Atomic write: temp file + rename
	tmp, err := os.CreateTemp(dir, filepath.Base(expandedPath)+".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	renamed := false
	defer func() {
		_ = tmp.Close()
		if !renamed {
			_ = os.Remove(tmpName)
		}
	}()

	if err := tmp.Chmod(0600); err != nil {
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpName, expandedPath); err != nil {
		return err
	}
	renamed = true

	if runtime.GOOS != "windows" {
		if err := os.Chmod(expandedPath, 0600); err != nil {
			return err
		}
	}
	return nil
}

func sharedString(v interface{}) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

// ExpandTilde expands ~ to user home directory
func ExpandTilde(path string) string {
	if strings.TrimSpace(path) == "~" {
		home, err := os.UserHomeDir()
		if err == nil {
			return home
		}
		return path
	}

	if strings.HasPrefix(path, "~"+string(filepath.Separator)) || strings.HasPrefix(path, "~/") {
		home, err := os.UserHomeDir()
		if err == nil {
			return filepath.Join(home, path[2:])
		}
	}
	return path
}

// parseExpiresAt parses various date formats for expiresAt
func parseExpiresAt(s string) (time.Time, error) {
	// Try ISO 8601 with Z suffix
	if strings.HasSuffix(s, "Z") {
		s = strings.TrimSuffix(s, "Z") + "+00:00"
	}

	// Try RFC3339
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t, nil
	}

	// Try RFC3339Nano
	if t, err := time.Parse(time.RFC3339Nano, s); err == nil {
		return t, nil
	}

	// Try common formats
	formats := []string{
		"2006-01-02T15:04:05.000Z07:00",
		"2006-01-02T15:04:05Z07:00",
		"2006-01-02T15:04:05",
	}

	for _, format := range formats {
		if t, err := time.Parse(format, s); err == nil {
			return t, nil
		}
	}

	return time.Time{}, errors.New("unable to parse expiresAt: " + s)
}

// GetDefaultKiroCredentialsPath returns the default path for kiro credentials
func GetDefaultKiroCredentialsPath() string {
	return "kiro-auth-token.json"
}

func isValidProfileArn(profileArn string) bool {
	return profileArn != "" && strings.HasPrefix(profileArn, "arn:aws") && strings.Contains(profileArn, "profile/")
}
