package shared

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
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
	RefreshToken  string             `json:"refreshToken"`
	AccessToken   string             `json:"accessToken,omitempty"`
	IDToken       string             `json:"idToken,omitempty"`
	AccountID     string             `json:"accountId,omitempty"`
	Email         string             `json:"email,omitempty"`
	PlanType      string             `json:"planType,omitempty"`
	ExpiresAt     time.Time          `json:"expiresAt,omitempty"`
	Status        CodexAccountStatus `json:"status,omitempty"`
	Weight        int                `json:"weight,omitempty"`
	ProxyUrl      string             `json:"proxyUrl,omitempty"`
	CooldownUntil time.Time          `json:"cooldownUntil,omitempty"`
	CooldownReason string            `json:"cooldownReason,omitempty"`
	CodexUsage    *CodexUsageSnapshot `json:"codexUsage,omitempty"`
	CreatedAt     time.Time          `json:"createdAt,omitempty"`
	UpdatedAt     time.Time          `json:"updatedAt,omitempty"`
}

type CodexMultiConfig struct {
	ActiveRefreshToken string         `json:"activeRefreshToken"`
	RotationMode       string         `json:"rotationMode,omitempty"`
	ProxyUrl           string         `json:"proxyUrl,omitempty"`
	Accounts           []CodexAccount `json:"accounts"`
	Config             CodexConfig    `json:"config,omitempty"`
}

// CodexConfig holds configurable parameters for Codex API requests
type CodexConfig struct {
	BaseURL       string `json:"baseURL,omitempty"`       // Upstream API base URL
	ClientVersion string `json:"clientVersion,omitempty"` // Version header value
	UserAgent     string `json:"userAgent,omitempty"`     // User-Agent header value
	Originator    string `json:"originator,omitempty"`    // Originator header value
}

func (a *CodexAccount) EffectiveWeight() int {
	if a == nil || a.Weight <= 0 {
		return 1
	}
	return a.Weight
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

func (c *CodexMultiConfig) GetActiveAccount() *CodexAccount {
	if c == nil || len(c.Accounts) == 0 {
		return nil
	}
	for i := range c.Accounts {
		if c.Accounts[i].RefreshToken == c.ActiveRefreshToken {
			return &c.Accounts[i]
		}
	}
	if len(c.Accounts) > 0 {
		return &c.Accounts[0]
	}
	return nil
}

func (c *CodexMultiConfig) FindAccountByRefreshToken(refreshToken string) *CodexAccount {
	if c == nil {
		return nil
	}
	for i := range c.Accounts {
		if c.Accounts[i].RefreshToken == refreshToken {
			return &c.Accounts[i]
		}
	}
	return nil
}

func (c *CodexMultiConfig) UpdateAccount(account *CodexAccount) bool {
	if c == nil || account == nil {
		return false
	}
	for i := range c.Accounts {
		if c.Accounts[i].RefreshToken == account.RefreshToken {
			c.Accounts[i] = *account
			return true
		}
	}
	return false
}

func (c *CodexMultiConfig) DeleteAccount(refreshToken string) bool {
	if c == nil {
		return false
	}
	for i := range c.Accounts {
		if c.Accounts[i].RefreshToken == refreshToken {
			c.Accounts = append(c.Accounts[:i], c.Accounts[i+1:]...)
			if c.ActiveRefreshToken == refreshToken && len(c.Accounts) > 0 {
				c.ActiveRefreshToken = c.Accounts[0].RefreshToken
			} else if len(c.Accounts) == 0 {
				c.ActiveRefreshToken = ""
			}
			return true
		}
	}
	return false
}

func (c *CodexMultiConfig) GetAvailableAccounts() []*CodexAccount {
	if c == nil {
		return nil
	}
	var result []*CodexAccount
	for i := range c.Accounts {
		a := &c.Accounts[i]
		if a.Status == CodexStatusBanned || a.Status == CodexStatusExhausted || a.Status == CodexStatusReused {
			continue
		}
		a.ClearCooldownIfExpired()
		if a.IsCoolingDown() {
			continue
		}
		result = append(result, a)
	}
	return result
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
		return "https://chatgpt.com/backend-api/codex"
	}
	return strings.TrimSpace(c.Config.BaseURL)
}

// GetClientVersion returns the configured client version or default
func (c *CodexMultiConfig) GetClientVersion() string {
	if c == nil || strings.TrimSpace(c.Config.ClientVersion) == "" {
		return "0.101.0"
	}
	return strings.TrimSpace(c.Config.ClientVersion)
}

// GetUserAgent returns the configured user agent or default
func (c *CodexMultiConfig) GetUserAgent() string {
	if c == nil || strings.TrimSpace(c.Config.UserAgent) == "" {
		return "codex_cli_rs/0.101.0 (Mac OS 26.0.1; arm64) Apple_Terminal/464"
	}
	return strings.TrimSpace(c.Config.UserAgent)
}

// GetOriginator returns the configured originator or default
func (c *CodexMultiConfig) GetOriginator() string {
	if c == nil || strings.TrimSpace(c.Config.Originator) == "" {
		return "codex_cli_rs"
	}
	return strings.TrimSpace(c.Config.Originator)
}

func GetDefaultCodexMultiConfigPath() string {
	return "codex.json"
}

func UpdateAccountStatusByRefreshToken(refreshToken string, status CodexAccountStatus, multiConfigPath string) error {
	if refreshToken == "" {
		return errors.New("refreshToken is required")
	}

	multiConfig, err := LoadCodexMultiConfig(multiConfigPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("failed to load codex.json: %w", err)
	}

	updated := false
	for i := range multiConfig.Accounts {
		if multiConfig.Accounts[i].RefreshToken == refreshToken {
			multiConfig.Accounts[i].Status = status
			multiConfig.Accounts[i].UpdatedAt = time.Now()
			updated = true
		}
	}

	if updated {
		if err := SaveCodexMultiConfig(multiConfigPath, multiConfig); err != nil {
			return fmt.Errorf("failed to save codex.json: %w", err)
		}
	}
	return nil
}

func SetAccountCooldown(refreshToken, reason string, until time.Time, multiConfigPath string) error {
	if strings.TrimSpace(refreshToken) == "" {
		return nil
	}
	multiConfig, err := LoadCodexMultiConfig(multiConfigPath)
	if err != nil {
		return err
	}
	for i := range multiConfig.Accounts {
		if multiConfig.Accounts[i].RefreshToken == refreshToken {
			multiConfig.Accounts[i].CooldownUntil = until
			multiConfig.Accounts[i].CooldownReason = reason
			multiConfig.Accounts[i].UpdatedAt = time.Now()
			break
		}
	}
	return SaveCodexMultiConfig(multiConfigPath, multiConfig)
}

func SyncTokenToCodexJson(codexJsonPath, previousRefreshToken string, accessToken, idToken, accountID, email, planType string, expiresAt time.Time) error {
	if strings.TrimSpace(codexJsonPath) == "" {
		return nil
	}
	nextRefreshToken := strings.TrimSpace(previousRefreshToken)
	if nextRefreshToken == "" {
		return nil
	}

	if _, err := os.Stat(codexJsonPath); err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}

	multiConfig, err := LoadCodexMultiConfig(codexJsonPath)
	if err != nil || multiConfig == nil {
		return err
	}

	account := multiConfig.FindAccountByRefreshToken(nextRefreshToken)
	if account == nil {
		return nil
	}

	account.AccessToken = accessToken
	account.IDToken = idToken
	account.ExpiresAt = expiresAt
	if accountID != "" {
		account.AccountID = accountID
	}
	if email != "" {
		account.Email = email
	}
	if planType != "" {
		account.PlanType = planType
	}
	if strings.TrimSpace(accessToken) == "" {
		account.ExpiresAt = time.Time{}
	}

	return SaveCodexMultiConfig(codexJsonPath, multiConfig)
}

// MarshalAccountForFrontend returns a JSON-safe representation with cooldown remaining info.
func MarshalAccountForFrontend(a *CodexAccount, isActive bool) map[string]interface{} {
	if a == nil {
		return nil
	}
	m := map[string]interface{}{
		"refreshToken": a.RefreshToken,
		"email":        a.Email,
		"planType":     a.PlanType,
		"accountId":    a.AccountID,
		"status":       string(a.Status),
		"weight":       a.Weight,
		"proxyUrl":     a.ProxyUrl,
		"isActive":     isActive,
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

func MarshalAccountsResponse(config *CodexMultiConfig) (json.RawMessage, error) {
	resp := map[string]interface{}{
		"activeRefreshToken": config.ActiveRefreshToken,
	}
	accounts := make([]map[string]interface{}, 0, len(config.Accounts))
	for i := range config.Accounts {
		a := &config.Accounts[i]
		isActive := a.RefreshToken == config.ActiveRefreshToken
		accounts = append(accounts, MarshalAccountForFrontend(a, isActive))
	}
	resp["accounts"] = accounts
	return json.Marshal(resp)
}
