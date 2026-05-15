package shared

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

type CodexAccountStatus string

const (
	CodexStatusValid     CodexAccountStatus = "valid"
	CodexStatusBanned    CodexAccountStatus = "banned"
	CodexStatusExhausted CodexAccountStatus = "exhausted"
	CodexStatusReused    CodexAccountStatus = "reused"
	CodexStatusUnknown   CodexAccountStatus = "unknown"
)

const (
	RotationFixed       = "fixed"
	RotationFailover    = "failover"
	RotationLoadBalance = "loadbalance"
)

const (
	DefaultCodexBaseURL       = "https://chatgpt.com/backend-api/codex"
	DefaultCodexClientVersion = "0.128.0"
	DefaultCodexOriginator    = "codex_cli_rs"
	DefaultCodexUserAgent     = DefaultCodexOriginator + "/" + DefaultCodexClientVersion + " (Mac OS 26.0.1; arm64) Apple_Terminal/464"
)

type CodexUsageSnapshot struct {
	PrimaryUsedPercent          float64   `json:"primaryUsedPercent,omitempty"`
	PrimaryResetAfterSeconds    int       `json:"primaryResetAfterSeconds,omitempty"`
	PrimaryWindowMinutes        int       `json:"primaryWindowMinutes,omitempty"`
	SecondaryUsedPercent        float64   `json:"secondaryUsedPercent,omitempty"`
	SecondaryResetAfterSeconds  int       `json:"secondaryResetAfterSeconds,omitempty"`
	SecondaryWindowMinutes      int       `json:"secondaryWindowMinutes,omitempty"`
	PrimaryOverSecondaryPercent float64   `json:"primaryOverSecondaryPercent,omitempty"`
	UpdatedAt                   time.Time `json:"updatedAt,omitempty"`
}

func ComputeResetMeta(updatedAt time.Time, resetAfterSeconds int) (resetAt time.Time, remainingSeconds int) {
	resetAt = updatedAt.Add(time.Duration(resetAfterSeconds) * time.Second)
	remaining := time.Until(resetAt)
	if remaining < 0 {
		remaining = 0
	}
	return resetAt, int(remaining.Seconds())
}

type CodexAccount struct {
	ID             string              `json:"id,omitempty"`
	RefreshToken   string              `json:"refreshToken"`
	AccessToken    string              `json:"accessToken,omitempty"`
	IDToken        string              `json:"idToken,omitempty"`
	AccountID      string              `json:"accountId,omitempty"`
	Email          string              `json:"email,omitempty"`
	PlanType       string              `json:"planType,omitempty"`
	Enabled        bool                `json:"enabled"`
	Websockets     bool                `json:"websockets,omitempty"`
	Password       string              `json:"password,omitempty"`
	MFACode        string              `json:"mfaCode,omitempty"`
	ExpiresAt      time.Time           `json:"expiresAt,omitempty"`
	Status         CodexAccountStatus  `json:"status,omitempty"`
	Weight         int                 `json:"weight,omitempty"`
	ProxyUrl       string              `json:"proxyUrl,omitempty"`
	CooldownUntil  time.Time           `json:"cooldownUntil,omitempty"`
	CooldownReason string              `json:"cooldownReason,omitempty"`
	CodexUsage     *CodexUsageSnapshot `json:"codexUsage,omitempty"`
	CreatedAt      time.Time           `json:"createdAt,omitempty"`
	UpdatedAt      time.Time           `json:"updatedAt,omitempty"`
	TodayRequests  int64               `json:"todayRequests,omitempty"`
	TodayTokens    int64               `json:"todayTotalTokens,omitempty"`
}

func GenerateCodexLocalID(accountID, email string) string {
	accountID = strings.ToLower(strings.TrimSpace(accountID))
	email = strings.ToLower(strings.TrimSpace(email))
	if accountID == "" || email == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(accountID + "\x00" + email))
	return "codex_" + fmt.Sprintf("%x", sum[:])[:32]
}

func EnsureCodexLocalID(a *CodexAccount) string {
	if a == nil {
		return ""
	}
	a.ID = strings.TrimSpace(a.ID)
	if a.ID == "" {
		a.ID = GenerateCodexLocalID(a.AccountID, a.Email)
	}
	return a.ID
}

type CodexMultiConfig struct {
	ActiveAccountID string      `json:"activeAccountId,omitempty"`
	RotationMode    string      `json:"rotationMode,omitempty"`
	ProxyUrl        string      `json:"proxyUrl,omitempty"`
	Config          CodexConfig `json:"config,omitempty"`
}

// CodexConfig holds configurable parameters for Codex API requests
type CodexConfig struct {
	BaseURL       string            `json:"baseURL,omitempty"`       // Upstream API base URL
	ClientVersion string            `json:"clientVersion,omitempty"` // Version header value
	UserAgent     string            `json:"userAgent,omitempty"`     // User-Agent header value
	Originator    string            `json:"originator,omitempty"`    // Originator header value
	CustomHeaders map[string]string `json:"customHeaders,omitempty"` // Additional headers to inject into upstream requests
}

func DefaultCodexConfig() CodexConfig {
	return CodexConfig{
		BaseURL:       DefaultCodexBaseURL,
		ClientVersion: DefaultCodexClientVersion,
		UserAgent:     DefaultCodexUserAgent,
		Originator:    DefaultCodexOriginator,
	}
}

func NormalizeCodexConfigForStorage(config CodexConfig) CodexConfig {
	config.BaseURL = strings.TrimSpace(config.BaseURL)
	config.ClientVersion = strings.TrimSpace(config.ClientVersion)
	config.UserAgent = strings.TrimSpace(config.UserAgent)
	config.Originator = strings.TrimSpace(config.Originator)
	if config.ClientVersion == DefaultCodexClientVersion {
		config.ClientVersion = ""
	}
	if config.UserAgent == DefaultCodexUserAgent {
		config.UserAgent = ""
	}
	return config
}

func (a *CodexAccount) EffectiveWeight() int {
	if a == nil || a.Weight <= 0 {
		return 1
	}
	return a.Weight
}

func (a *CodexAccount) IsEnabled() bool {
	return a == nil || a.Enabled
}

func (a *CodexAccount) IsCoolingDown() bool {
	if a == nil || a.CooldownUntil.IsZero() {
		return false
	}
	return time.Now().Before(a.CooldownUntil)
}

func (a *CodexAccount) ClearCooldownIfExpired() bool {
	if a == nil || a.CooldownUntil.IsZero() {
		return false
	}
	if !time.Now().Before(a.CooldownUntil) {
		a.CooldownUntil = time.Time{}
		a.CooldownReason = ""
		return true
	}
	return false
}

func (c *CodexMultiConfig) GetRotationMode() string {
	if c == nil || c.RotationMode == "" {
		return RotationFixed
	}
	switch c.RotationMode {
	case RotationFixed, RotationFailover, RotationLoadBalance:
		return c.RotationMode
	default:
		return RotationFixed
	}
}

// GetBaseURL returns the configured base URL or default
func (c *CodexMultiConfig) GetBaseURL() string {
	if c == nil || strings.TrimSpace(c.Config.BaseURL) == "" {
		return DefaultCodexBaseURL
	}
	return strings.TrimSpace(c.Config.BaseURL)
}

// GetClientVersion returns the configured client version or default
func (c *CodexMultiConfig) GetClientVersion() string {
	if c == nil || strings.TrimSpace(c.Config.ClientVersion) == "" {
		return DefaultCodexClientVersion
	}
	return strings.TrimSpace(c.Config.ClientVersion)
}

// GetUserAgent returns the configured user agent or default
func (c *CodexMultiConfig) GetUserAgent() string {
	if c == nil || strings.TrimSpace(c.Config.UserAgent) == "" {
		return DefaultCodexUserAgent
	}
	return strings.TrimSpace(c.Config.UserAgent)
}

// GetOriginator returns the configured originator or default
func (c *CodexMultiConfig) GetOriginator() string {
	if c == nil || strings.TrimSpace(c.Config.Originator) == "" {
		return DefaultCodexOriginator
	}
	return strings.TrimSpace(c.Config.Originator)
}

// GetCustomHeaders returns the configured custom headers (may be nil)
func (c *CodexMultiConfig) GetCustomHeaders() map[string]string {
	if c == nil {
		return nil
	}
	return c.Config.CustomHeaders
}

func GetDefaultCodexMultiConfigPath() string {
	return "codex.json"
}

// MarshalAccountForFrontend returns a JSON-safe representation with cooldown remaining info.
func MarshalAccountForFrontend(a *CodexAccount, isActive bool) map[string]interface{} {
	if a == nil {
		return nil
	}
	m := map[string]interface{}{
		"id":               a.ID,
		"refreshToken":     a.RefreshToken,
		"accessToken":      a.AccessToken,
		"idToken":          a.IDToken,
		"email":            a.Email,
		"planType":         a.PlanType,
		"accountId":        a.AccountID,
		"enabled":          a.Enabled,
		"websockets":       a.Websockets,
		"status":           string(a.Status),
		"weight":           a.Weight,
		"proxyUrl":         a.ProxyUrl,
		"password":         a.Password,
		"mfaCode":          a.MFACode,
		"isActive":         isActive,
		"todayRequests":    a.TodayRequests,
		"todayTotalTokens": a.TodayTokens,
	}
	if !a.ExpiresAt.IsZero() {
		m["expiresAt"] = a.ExpiresAt.Format(time.RFC3339)
	}
	if !a.CooldownUntil.IsZero() && time.Now().Before(a.CooldownUntil) {
		m["cooldownUntil"] = a.CooldownUntil.Format(time.RFC3339)
		m["cooldownReason"] = a.CooldownReason
		remaining := time.Until(a.CooldownUntil)
		m["cooldownRemaining"] = int(remaining.Seconds())
	}
	if !a.CreatedAt.IsZero() {
		m["createdAt"] = a.CreatedAt.Format(time.RFC3339)
	}
	if !a.UpdatedAt.IsZero() {
		m["updatedAt"] = a.UpdatedAt.Format(time.RFC3339)
	}
	if a.CodexUsage != nil && !a.CodexUsage.UpdatedAt.IsZero() {
		primaryResetAt, primaryRemaining := ComputeResetMeta(a.CodexUsage.UpdatedAt, a.CodexUsage.PrimaryResetAfterSeconds)
		secondaryResetAt, secondaryRemaining := ComputeResetMeta(a.CodexUsage.UpdatedAt, a.CodexUsage.SecondaryResetAfterSeconds)
		m["codexUsage"] = map[string]any{
			"primary": map[string]any{
				"usedPercent":      a.CodexUsage.PrimaryUsedPercent,
				"windowMinutes":    a.CodexUsage.PrimaryWindowMinutes,
				"resetAt":          primaryResetAt.Format(time.RFC3339),
				"remainingSeconds": primaryRemaining,
			},
			"secondary": map[string]any{
				"usedPercent":      a.CodexUsage.SecondaryUsedPercent,
				"windowMinutes":    a.CodexUsage.SecondaryWindowMinutes,
				"resetAt":          secondaryResetAt.Format(time.RFC3339),
				"remainingSeconds": secondaryRemaining,
			},
			"primaryOverSecondaryPercent": a.CodexUsage.PrimaryOverSecondaryPercent,
			"updatedAt":                   a.CodexUsage.UpdatedAt.Format(time.RFC3339),
		}
	}
	return m
}

func MarshalAccountsResponse(activeAccountID string, accounts []CodexAccount) (json.RawMessage, error) {
	activeAccountID = strings.TrimSpace(activeAccountID)

	resp := map[string]interface{}{
		"activeAccountId": activeAccountID,
	}

	list := make([]map[string]interface{}, 0, len(accounts))
	for i := range accounts {
		a := &accounts[i]
		isActive := activeAccountID != "" && strings.TrimSpace(a.ID) == activeAccountID
		list = append(list, MarshalAccountForFrontend(a, isActive))
	}

	resp["accounts"] = list
	return json.Marshal(resp)
}
