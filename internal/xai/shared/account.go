package shared

import (
	"crypto/sha256"
	"fmt"
	"strings"
	"time"

	xaiAuth "clisimplehub/internal/xai/auth"
)

type XaiAccountStatus string

const (
	XaiStatusValid     XaiAccountStatus = "valid"
	XaiStatusBanned    XaiAccountStatus = "banned"
	XaiStatusExhausted XaiAccountStatus = "exhausted"
	XaiStatusUnknown   XaiAccountStatus = "unknown"
)

const (
	RotationFixed       = "fixed"
	RotationFailover    = "failover"
	RotationLoadBalance = "loadbalance"
)

const (
	AuthKindOAuth  = "oauth"
	AuthKindAPIKey = "api_key"
)

type XaiAccount struct {
	ID            string          `json:"id,omitempty"`
	Email         string          `json:"email,omitempty"`
	Subject       string          `json:"subject,omitempty"`
	AccessToken   string          `json:"accessToken,omitempty"`
	RefreshToken  string          `json:"refreshToken,omitempty"`
	IDToken       string          `json:"idToken,omitempty"`
	AuthKind      string          `json:"authKind,omitempty"`
	APIKey        string          `json:"apiKey,omitempty"`
	BaseURL       string          `json:"baseURL,omitempty"`
	TokenEndpoint string          `json:"tokenEndpoint,omitempty"`
	RedirectURI   string          `json:"redirectURI,omitempty"`
	Enabled       bool            `json:"enabled"`
	Websockets    bool            `json:"websockets,omitempty"`
	Weight        int             `json:"weight,omitempty"`
	ProxyUrl      string          `json:"proxyUrl,omitempty"`
	Status        XaiAccountStatus `json:"status,omitempty"`
	ExpiresAt     time.Time       `json:"expiresAt,omitempty"`
	LastRefresh   time.Time       `json:"lastRefresh,omitempty"`
	CooldownUntil  time.Time       `json:"cooldownUntil,omitempty"`
	CooldownReason string          `json:"cooldownReason,omitempty"`
	CreatedAt     time.Time       `json:"createdAt,omitempty"`
	UpdatedAt     time.Time       `json:"updatedAt,omitempty"`
}

type XaiConfig struct {
	BaseURL       string            `json:"baseURL,omitempty"`
	CustomHeaders map[string]string `json:"customHeaders,omitempty"`
}

type XaiMultiConfig struct {
	ActiveAccountID string       `json:"activeAccountId,omitempty"`
	RotationMode    string       `json:"rotationMode,omitempty"`
	ProxyUrl        string       `json:"proxyUrl,omitempty"`
	Config          XaiConfig    `json:"config,omitempty"`
	Accounts        []XaiAccount `json:"accounts"`
}

func DefaultXaiConfig() XaiConfig {
	return XaiConfig{BaseURL: xaiAuth.DefaultAPIBaseURL}
}

func GetDefaultXaiMultiConfigPath() string {
	return "xai.json"
}

func GenerateXaiLocalID(email, subject, apiKey string) string {
	email = strings.ToLower(strings.TrimSpace(email))
	subject = strings.TrimSpace(subject)
	apiKey = strings.TrimSpace(apiKey)
	seed := ""
	switch {
	case email != "" || subject != "":
		seed = email + "\x00" + subject
	case apiKey != "":
		seed = "api_key\x00" + apiKey
	default:
		return ""
	}
	sum := sha256.Sum256([]byte(seed))
	return "xai_" + fmt.Sprintf("%x", sum[:])[:32]
}

func EnsureXaiLocalID(a *XaiAccount) string {
	if a == nil {
		return ""
	}
	a.ID = strings.TrimSpace(a.ID)
	if a.ID == "" {
		a.ID = GenerateXaiLocalID(a.Email, a.Subject, a.APIKey)
	}
	return a.ID
}

func (a *XaiAccount) EffectiveWeight() int {
	if a == nil || a.Weight <= 0 {
		return 1
	}
	return a.Weight
}

func (a *XaiAccount) IsEnabled() bool {
	return a == nil || a.Enabled
}

func (a *XaiAccount) IsCoolingDown() bool {
	if a == nil || a.CooldownUntil.IsZero() {
		return false
	}
	return time.Now().Before(a.CooldownUntil)
}

func (a *XaiAccount) ClearCooldownIfExpired() bool {
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

func (a *XaiAccount) BearerToken() string {
	if a == nil {
		return ""
	}
	if strings.EqualFold(strings.TrimSpace(a.AuthKind), AuthKindAPIKey) {
		return strings.TrimSpace(a.APIKey)
	}
	if token := strings.TrimSpace(a.AccessToken); token != "" {
		return token
	}
	return strings.TrimSpace(a.APIKey)
}

func (a *XaiAccount) EffectiveBaseURL(global XaiConfig) string {
	if a != nil {
		if base := strings.TrimSpace(a.BaseURL); base != "" {
			return strings.TrimRight(base, "/")
		}
	}
	if base := strings.TrimSpace(global.BaseURL); base != "" {
		return strings.TrimRight(base, "/")
	}
	return xaiAuth.DefaultAPIBaseURL
}

func (c *XaiMultiConfig) GetRotationMode() string {
	if c == nil {
		return RotationFixed
	}
	mode := strings.ToLower(strings.TrimSpace(c.RotationMode))
	switch mode {
	case RotationFixed, RotationFailover, RotationLoadBalance:
		return mode
	default:
		return RotationFixed
	}
}

func (c *XaiMultiConfig) GetActiveAccount() *XaiAccount {
	if c == nil || len(c.Accounts) == 0 {
		return nil
	}
	activeID := strings.TrimSpace(c.ActiveAccountID)
	if activeID != "" {
		for i := range c.Accounts {
			if strings.TrimSpace(c.Accounts[i].ID) == activeID {
				return &c.Accounts[i]
			}
		}
	}
	return &c.Accounts[0]
}

func (c *XaiMultiConfig) FindAccountByID(id string) *XaiAccount {
	if c == nil {
		return nil
	}
	id = strings.TrimSpace(id)
	if id == "" {
		return nil
	}
	for i := range c.Accounts {
		if strings.TrimSpace(c.Accounts[i].ID) == id {
			return &c.Accounts[i]
		}
	}
	return nil
}

func NormalizeAccount(account *XaiAccount) {
	if account == nil {
		return
	}
	account.Email = strings.TrimSpace(account.Email)
	account.Subject = strings.TrimSpace(account.Subject)
	account.AccessToken = strings.TrimSpace(account.AccessToken)
	account.RefreshToken = strings.TrimSpace(account.RefreshToken)
	account.IDToken = strings.TrimSpace(account.IDToken)
	account.APIKey = strings.TrimSpace(account.APIKey)
	account.BaseURL = strings.TrimSpace(account.BaseURL)
	account.TokenEndpoint = strings.TrimSpace(account.TokenEndpoint)
	account.RedirectURI = strings.TrimSpace(account.RedirectURI)
	account.ProxyUrl = strings.TrimSpace(account.ProxyUrl)
	account.AuthKind = strings.TrimSpace(account.AuthKind)
	if account.AuthKind == "" {
		if account.APIKey != "" && account.RefreshToken == "" && account.AccessToken == "" {
			account.AuthKind = AuthKindAPIKey
		} else {
			account.AuthKind = AuthKindOAuth
		}
	}
	if account.Weight <= 0 {
		account.Weight = 1
	}
	switch account.Status {
	case XaiStatusValid, XaiStatusBanned, XaiStatusExhausted, XaiStatusUnknown:
	default:
		account.Status = XaiStatusValid
	}
	EnsureXaiLocalID(account)
	now := time.Now()
	if account.CreatedAt.IsZero() {
		account.CreatedAt = now
	}
	if account.UpdatedAt.IsZero() {
		account.UpdatedAt = account.CreatedAt
	}
}
