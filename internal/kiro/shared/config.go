package shared

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// KiroCredentials represents authentication credentials for a Kiro account.
type KiroCredentials struct {
	AccessToken  string            `json:"accessToken"`
	RefreshToken string            `json:"refreshToken"`
	ProfileArn   string            `json:"profileArn"`
	ExpiresAt    time.Time         `json:"expiresAt"`
	Region       string            `json:"region,omitempty"`
	MachineID    string            `json:"machineId,omitempty"`
	AuthMethod   string            `json:"authMethod,omitempty"`
	Provider     string            `json:"provider,omitempty"`
	ClientId     string            `json:"clientId,omitempty"`
	ClientSecret string            `json:"clientSecret,omitempty"`
	Status       KiroAccountStatus `json:"status,omitempty"`
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
