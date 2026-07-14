package shared

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

const (
	// DefaultAPIBaseURL 官方 API（OAuth 默认、WebSocket、图片/视频）。
	DefaultAPIBaseURL = "https://api.x.ai/v1"
	// CLIChatProxyBaseURL Grok CLI chat-proxy；非媒体 HTTP 聊天在 base 为默认官方地址时改写到此。
	CLIChatProxyBaseURL = "https://cli-chat-proxy.grok.com/v1"
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

// 账号档位（由 grok.com/rest/rate-limits 的 totalQueries 推断）。
const (
	PoolBasic = "basic"
	PoolSuper = "super"
	PoolHeavy = "heavy"
)

// XaiQuotaWindow 单 mode 额度窗口（与上游 rate-limits 对齐）。
type XaiQuotaWindow struct {
	Remaining     int   `json:"remaining"`
	Total         int   `json:"total"`
	WindowSeconds int   `json:"windowSeconds"`
	ResetAt       int64 `json:"resetAt,omitempty"`  // unix ms
	SyncedAt      int64 `json:"syncedAt,omitempty"` // unix ms
}

// XaiQuota 各 mode 额度集合。
type XaiQuota struct {
	Auto   *XaiQuotaWindow `json:"auto,omitempty"`
	Fast   *XaiQuotaWindow `json:"fast,omitempty"`
	Expert *XaiQuotaWindow `json:"expert,omitempty"`
	Heavy  *XaiQuotaWindow `json:"heavy,omitempty"`
	Grok43 *XaiQuotaWindow `json:"grok43,omitempty"` // grok-420-computer-use-sa
}

type XaiAccount struct {
	ID           string `json:"id,omitempty"`
	Email        string `json:"email,omitempty"`
	Subject      string `json:"subject,omitempty"`
	AccessToken  string `json:"accessToken,omitempty"`
	RefreshToken string `json:"refreshToken,omitempty"`
	IDToken      string `json:"idToken,omitempty"`
	AuthKind     string `json:"authKind,omitempty"`
	APIKey       string `json:"apiKey,omitempty"`
	// SSO 浏览器 grok.com / accounts.x.ai 的 sso Cookie 值（JWT，含 session_id）。
	SSO     string `json:"sso,omitempty"`
	Enabled bool   `json:"enabled"`
	// Websockets：nil/缺省=默认开启；显式 false 可关闭。
	Websockets *bool `json:"websockets,omitempty"`
	// UsingAPI：nil 时按 authKind 默认（oauth=false→chat-proxy；api_key=true→官方 API）。
	// true=非媒体 HTTP 文本走官方 api.x.ai；false=走 cli-chat-proxy（Grok Build 额度）。
	// responses/compact 走 chat base；图片/视频/WebSocket 走官方 API。
	UsingAPI *bool  `json:"usingApi,omitempty"`
	Weight   int    `json:"weight,omitempty"`
	ProxyUrl string `json:"proxyUrl,omitempty"`
	// CustomHeaders 账号级自定义头（后写覆盖全局 customHeaders）
	CustomHeaders map[string]string `json:"customHeaders,omitempty"`
	// Pool 账号类型：basic / super / heavy（由 rate-limits 推断）。
	Pool string `json:"pool,omitempty"`
	// Quota grok.com rate-limits 同步的额度。
	Quota *XaiQuota `json:"quota,omitempty"`
	// LastQuotaSync 上次额度同步时间。
	LastQuotaSync  time.Time        `json:"lastQuotaSync,omitempty"`
	Status         XaiAccountStatus `json:"status,omitempty"`
	ExpiresAt      time.Time        `json:"expiresAt,omitempty"`
	LastRefresh    time.Time        `json:"lastRefresh,omitempty"`
	CooldownUntil  time.Time        `json:"cooldownUntil,omitempty"`
	CooldownReason string           `json:"cooldownReason,omitempty"`
	CreatedAt      time.Time        `json:"createdAt,omitempty"`
	UpdatedAt      time.Time        `json:"updatedAt,omitempty"`
}

type XaiConfig struct {
	BaseURL string `json:"baseURL,omitempty"`
	// ClientVersion maps to x-grok-client-version (Grok CLI version).
	ClientVersion string `json:"clientVersion,omitempty"`
	// UserAgent maps to User-Agent (default xai-grok-cli/<version>).
	UserAgent string `json:"userAgent,omitempty"`
	// TokenAuth maps to X-XAI-Token-Auth (default xai-grok-cli for OAuth).
	TokenAuth string `json:"tokenAuth,omitempty"`
	// ClientSurface maps to x-grok-client-surface (default grok-cli).
	ClientSurface string `json:"clientSurface,omitempty"`
	// DynamicStatsig：nil/缺省=true
	// 控制 grok.com rate-limits 等请求的 x-statsig-id 是否动态生成。
	DynamicStatsig *bool `json:"dynamicStatsig,omitempty"`
	// AutoRefreshToken：nil/缺省=false；开启后后台定期刷新临近过期的 OAuth token。
	AutoRefreshToken *bool             `json:"autoRefreshToken,omitempty"`
	CustomHeaders    map[string]string `json:"customHeaders,omitempty"`
}

// DynamicStatsigEnabled 是否动态生成 x-statsig-id（默认 true）。
func (c XaiConfig) DynamicStatsigEnabled() bool {
	if c.DynamicStatsig == nil {
		return true
	}
	return *c.DynamicStatsig
}

// SetDynamicStatsig 写入 dynamicStatsig 开关。
func (c *XaiConfig) SetDynamicStatsig(enabled bool) {
	if c == nil {
		return
	}
	v := enabled
	c.DynamicStatsig = &v
}

// AutoRefreshTokenEnabled 是否启用后台 OAuth token 自动刷新（默认 false）。
func (c XaiConfig) AutoRefreshTokenEnabled() bool {
	return c.AutoRefreshToken != nil && *c.AutoRefreshToken
}

// SetAutoRefreshToken 写入自动刷新开关。
func (c *XaiConfig) SetAutoRefreshToken(enabled bool) {
	if c == nil {
		return
	}
	v := enabled
	c.AutoRefreshToken = &v
}

type XaiMultiConfig struct {
	ActiveAccountID string       `json:"activeAccountId,omitempty"`
	RotationMode    string       `json:"rotationMode,omitempty"`
	ProxyUrl        string       `json:"proxyUrl,omitempty"`
	Config          XaiConfig    `json:"config,omitempty"`
	Accounts        []XaiAccount `json:"accounts"`
}

func DefaultXaiConfig() XaiConfig {
	return XaiConfig{
		BaseURL:        DefaultAPIBaseURL,
		DynamicStatsig: BoolPtr(true),
	}
}

func GetDefaultXaiMultiConfigPath() string {
	return "xai.json"
}

func GenerateXaiLocalID(email, subject, apiKey, sso string) string {
	email = strings.ToLower(strings.TrimSpace(email))
	subject = strings.TrimSpace(subject)
	apiKey = strings.TrimSpace(apiKey)
	sso = strings.TrimSpace(sso)
	seed := ""
	switch {
	case email != "" || subject != "":
		seed = email + "\x00" + subject
	case apiKey != "":
		seed = "api_key\x00" + apiKey
	case sso != "":
		seed = "sso\x00" + sso
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
		a.ID = GenerateXaiLocalID(a.Email, a.Subject, a.APIKey, a.SSO)
	}
	return a.ID
}

func (a *XaiAccount) EffectiveWeight() int {
	if a == nil || a.Weight <= 0 {
		return 1
	}
	return a.Weight
}

// WebsocketsEnabled 账号是否允许走上游 WebSocket（默认 true）。
func (a *XaiAccount) WebsocketsEnabled() bool {
	if a == nil || a.Websockets == nil {
		return true
	}
	return *a.Websockets
}

// SetWebsockets 设置 websockets 开关（写入时会显式落盘）。
func (a *XaiAccount) SetWebsockets(enabled bool) {
	if a == nil {
		return
	}
	v := enabled
	a.Websockets = &v
}

// UsingAPIEnabled 是否对非媒体 HTTP 文本走官方 API。
// 显式 usingApi 优先；缺省：oauth=false，api_key/其它=true；account=nil 视为 true
func (a *XaiAccount) UsingAPIEnabled() bool {
	if a == nil {
		return true
	}
	if a.UsingAPI != nil {
		return *a.UsingAPI
	}
	kind := strings.ToLower(strings.TrimSpace(a.AuthKind))
	if kind == "" {
		if strings.TrimSpace(a.APIKey) != "" && strings.TrimSpace(a.RefreshToken) == "" && strings.TrimSpace(a.AccessToken) == "" {
			kind = AuthKindAPIKey
		} else {
			kind = AuthKindOAuth
		}
	}
	return kind != AuthKindOAuth
}

// SetUsingAPI 设置 usingApi 开关（写入时会显式落盘）。
func (a *XaiAccount) SetUsingAPI(enabled bool) {
	if a == nil {
		return
	}
	v := enabled
	a.UsingAPI = &v
}

func BoolPtr(v bool) *bool { return &v }

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

// EffectiveBaseURL 仅使用全局 config.baseURL（账号级 baseURL 已废弃）。
func (a *XaiAccount) EffectiveBaseURL(global XaiConfig) string {
	if base := strings.TrimSpace(global.BaseURL); base != "" {
		return strings.TrimRight(base, "/")
	}
	return DefaultAPIBaseURL
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
	account.SSO = strings.TrimSpace(account.SSO)
	account.ProxyUrl = strings.TrimSpace(account.ProxyUrl)
	account.AuthKind = strings.TrimSpace(account.AuthKind)
	// SSO JWT 常含 session_id；无 subject 时补上便于展示与去重
	if account.Subject == "" && account.SSO != "" {
		if sid := sessionIDFromSSOJWT(account.SSO); sid != "" {
			account.Subject = sid
		}
	}
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
	switch strings.ToLower(strings.TrimSpace(account.Pool)) {
	case PoolBasic, PoolSuper, PoolHeavy:
		account.Pool = strings.ToLower(strings.TrimSpace(account.Pool))
	case "":
		// 保留空：导入后由 rate-limits 推断
	default:
		account.Pool = ""
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

// sessionIDFromSSOJWT 从 sso Cookie JWT payload 解析 session_id。
func sessionIDFromSSOJWT(token string) string {
	token = strings.TrimSpace(token)
	parts := strings.Split(token, ".")
	if len(parts) < 2 {
		return ""
	}
	payload := parts[1]
	switch len(payload) % 4 {
	case 2:
		payload += "=="
	case 3:
		payload += "="
	}
	raw, err := base64.URLEncoding.DecodeString(payload)
	if err != nil {
		raw, err = base64.RawURLEncoding.DecodeString(parts[1])
	}
	if err != nil || len(raw) == 0 {
		return ""
	}
	var claims map[string]any
	if err := json.Unmarshal(raw, &claims); err != nil {
		return ""
	}
	if v, ok := claims["session_id"].(string); ok {
		return strings.TrimSpace(v)
	}
	return ""
}
