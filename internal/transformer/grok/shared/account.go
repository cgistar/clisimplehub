package shared

import (
	"fmt"
	"os"
	"strings"
	"time"
)

type GrokAccountStatus string

const (
	GrokStatusValid    GrokAccountStatus = "valid"
	GrokStatusInvalid  GrokAccountStatus = "invalid"
	GrokStatusCooling  GrokAccountStatus = "cooling"
	GrokStatusUnknown  GrokAccountStatus = "unknown"
)

const (
	RotationFixed       = "fixed"
	RotationFailover    = "failover"
	RotationLoadBalance = "loadbalance"
)

type GrokTier string

const (
	TierBasic GrokTier = "basic"
	TierSuper GrokTier = "super"
)

// GrokAccount represents a single Grok SSO account.
// SsoToken is the primary key.
type GrokAccount struct {
	SsoToken     string            `json:"ssoToken"`
	Status       GrokAccountStatus `json:"status,omitempty"`
	Tier         GrokTier          `json:"tier,omitempty"`
	Quota        int               `json:"quota,omitempty"`
	CurrentUsage int               `json:"currentUsage,omitempty"`
	IsPlus       bool              `json:"isPlus,omitempty"`
	FailCount    int               `json:"failCount,omitempty"`
	ProxyUrl     string            `json:"proxyUrl,omitempty"`
	Weight       int               `json:"weight,omitempty"`
	CreatedAt    time.Time         `json:"createdAt,omitempty"`
	UpdatedAt    time.Time         `json:"updatedAt,omitempty"`
}

type GrokSettings struct {
	Chat     ChatSettings     `json:"chat,omitempty"`
	Security SecuritySettings `json:"security,omitempty"`
	Network  NetworkSettings  `json:"network,omitempty"`
	Image    ImageSettings    `json:"image,omitempty"`
}

type ChatSettings struct {
	Temporary      *bool    `json:"temporary,omitempty"`
	DisableMemory  *bool    `json:"disableMemory,omitempty"`
	FilterTags     []string `json:"filterTags,omitempty"`
	Stream         *bool    `json:"stream,omitempty"`
	Thinking       *bool    `json:"thinking,omitempty"`
	DynamicStatsig *bool    `json:"dynamicStatsig,omitempty"`
}

type SecuritySettings struct {
	CfClearance string `json:"cfClearance,omitempty"`
	UserAgent   string `json:"userAgent,omitempty"`
}

type NetworkSettings struct {
	Timeout int `json:"timeout,omitempty"`
}

type ImageSettings struct {
	ImageWs              *bool `json:"imageWs,omitempty"`
	ImageWsNsfw          *bool `json:"imageWsNsfw,omitempty"`
	ImageWsBlockedSeconds int  `json:"imageWsBlockedSeconds,omitempty"`
	ImageWsFinalMinBytes  int  `json:"imageWsFinalMinBytes,omitempty"`
	ImageWsMediumMinBytes int  `json:"imageWsMediumMinBytes,omitempty"`
}

// GrokMultiConfig is the root structure for grok.json.
type GrokMultiConfig struct {
	ActiveSsoToken string        `json:"activeSsoToken"`
	RotationMode   string        `json:"rotationMode,omitempty"`
	ProxyURL       string        `json:"proxyUrl,omitempty"`
	Accounts       []GrokAccount `json:"accounts"`
	Settings       GrokSettings  `json:"settings,omitempty"`
}

// GetActiveAccount returns the currently active account.
func (c *GrokMultiConfig) GetActiveAccount() *GrokAccount {
	if c == nil || len(c.Accounts) == 0 {
		return nil
	}
	for i := range c.Accounts {
		if c.Accounts[i].SsoToken == c.ActiveSsoToken {
			return &c.Accounts[i]
		}
	}
	if len(c.Accounts) > 0 {
		return &c.Accounts[0]
	}
	return nil
}

func (c *GrokMultiConfig) FindAccountBySsoToken(token string) *GrokAccount {
	if c == nil {
		return nil
	}
	for i := range c.Accounts {
		if c.Accounts[i].SsoToken == token {
			return &c.Accounts[i]
		}
	}
	return nil
}

func (c *GrokMultiConfig) UpdateAccount(account *GrokAccount) bool {
	if c == nil || account == nil {
		return false
	}
	for i := range c.Accounts {
		if c.Accounts[i].SsoToken == account.SsoToken {
			c.Accounts[i] = *account
			return true
		}
	}
	return false
}

func (c *GrokMultiConfig) DeleteAccount(ssoToken string) bool {
	if c == nil {
		return false
	}
	for i := range c.Accounts {
		if c.Accounts[i].SsoToken == ssoToken {
			c.Accounts = append(c.Accounts[:i], c.Accounts[i+1:]...)
			if c.ActiveSsoToken == ssoToken && len(c.Accounts) > 0 {
				c.ActiveSsoToken = c.Accounts[0].SsoToken
			} else if len(c.Accounts) == 0 {
				c.ActiveSsoToken = ""
			}
			return true
		}
	}
	return false
}

func (c *GrokMultiConfig) GetAvailableAccounts() []*GrokAccount {
	if c == nil {
		return nil
	}
	var result []*GrokAccount
	for i := range c.Accounts {
		s := c.Accounts[i].Status
		if s != GrokStatusInvalid && s != GrokStatusCooling {
			result = append(result, &c.Accounts[i])
		}
	}
	return result
}

func (c *GrokMultiConfig) GetRotationMode() string {
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

func (a *GrokAccount) EffectiveWeight() int {
	if a == nil || a.Weight <= 0 {
		return 1
	}
	return a.Weight
}

// UpdateAccountStatus updates the status of an account in grok.json.
func UpdateAccountStatus(ssoToken string, status GrokAccountStatus, configPath string) error {
	if ssoToken == "" {
		return fmt.Errorf("ssoToken is required")
	}
	config, err := LoadGrokMultiConfig(configPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("failed to load grok.json: %w", err)
	}
	updated := false
	for i := range config.Accounts {
		if config.Accounts[i].SsoToken == ssoToken {
			config.Accounts[i].Status = status
			config.Accounts[i].UpdatedAt = time.Now()
			updated = true
		}
	}
	if updated {
		if err := SaveGrokMultiConfig(configPath, config); err != nil {
			return fmt.Errorf("failed to save grok.json: %w", err)
		}
	}
	return nil
}

// IsRateLimitError checks if the response body indicates a rate limit.
func IsRateLimitError(statusCode int) bool {
	return statusCode == 429
}

// IsAuthError checks if the response indicates auth failure.
func IsAuthError(statusCode int) bool {
	return statusCode == 401 || statusCode == 403
}

// DefaultQuota returns the default quota for a tier.
func DefaultQuota(tier GrokTier) int {
	switch tier {
	case TierSuper:
		return 140
	default:
		return 80
	}
}

// NormalizeSsoToken strips whitespace and the "sso=" prefix from a token.
func NormalizeSsoToken(s string) string {
	return strings.TrimPrefix(strings.TrimSpace(s), "sso=")
}

// ExpandTilde expands ~ to user home directory.
func ExpandTilde(path string) string {
	if strings.TrimSpace(path) == "~" {
		home, err := os.UserHomeDir()
		if err == nil {
			return home
		}
		return path
	}
	if strings.HasPrefix(path, "~/") {
		home, err := os.UserHomeDir()
		if err == nil {
			return home + path[1:]
		}
	}
	return path
}
