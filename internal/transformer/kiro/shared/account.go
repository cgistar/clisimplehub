package shared

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"
)

// KiroAccountStatus 账号状态枚举
type KiroAccountStatus string

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

const (
	KiroStatusValid     KiroAccountStatus = "valid"
	KiroStatusBanned    KiroAccountStatus = "banned"
	KiroStatusExhausted KiroAccountStatus = "exhausted"
	KiroStatusUnknown   KiroAccountStatus = "unknown"
)

// Rotation mode constants
const (
	RotationFixed       = "fixed"
	RotationFailover    = "failover"
	RotationLoadBalance = "loadbalance"
)

// KiroAccount 多账号管理中的单个账号
// refreshToken 作为主键
type KiroAccount struct {
	// 认证字段
	RefreshToken string    `json:"refreshToken"`
	AccessToken  string    `json:"accessToken,omitempty"`
	ProfileArn   string    `json:"profileArn,omitempty"`
	ExpiresAt    time.Time `json:"expiresAt,omitempty"`
	Region       string    `json:"region,omitempty"`
	AuthMethod   string    `json:"authMethod,omitempty"`
	Provider     string    `json:"provider,omitempty"`
	ClientId     string    `json:"clientId,omitempty"`
	ClientSecret string    `json:"clientSecret,omitempty"`

	// 每账号独有的 machineId（从 refreshToken 计算）
	MachineId string `json:"machineId,omitempty"`

	// 状态与用量
	Status             KiroAccountStatus `json:"status,omitempty"`
	SubscriptionTitle  string            `json:"subscriptionTitle,omitempty"`
	UsageLimit         float64           `json:"usageLimit,omitempty"`
	CurrentUsage       float64           `json:"currentUsage,omitempty"`
	Balance            float64           `json:"balance"`
	UsagePct           float64           `json:"usagePct,omitempty"`
	Email              string            `json:"email,omitempty"`
	UserID             string            `json:"userId,omitempty"`
	DaysUntilReset     *int32            `json:"daysUntilReset,omitempty"`
	NextDateReset      *float64          `json:"nextDateReset,omitempty"`
	LastUsageCheck     time.Time         `json:"lastUsageCheck,omitempty"`
	UsageBreakdownList []byte            `json:"usageBreakdownList,omitempty"`

	// 配置（每账号可独立配置）
	ProxyUrl  string `json:"proxyUrl,omitempty"`
	UserAgent string `json:"userAgent,omitempty"`
	Version   string `json:"version,omitempty"`
	Weight    int    `json:"weight,omitempty"` // WRR 权重，默认 1

	// 元数据
	CreatedAt time.Time `json:"createdAt,omitempty"`
	UpdatedAt time.Time `json:"updatedAt,omitempty"`
}

// KiroMultiConfig 多账号配置文件结构
type KiroMultiConfig struct {
	ActiveRefreshToken string            `json:"activeRefreshToken"`
	RotationMode       string            `json:"rotationMode,omitempty"` // "fixed"(默认), "failover", "loadbalance"
	Region             string            `json:"region,omitempty"`       // 全局默认 region，per-account 可覆盖
	ProxyURL           string            `json:"proxyUrl,omitempty"`
	UserAgent          string            `json:"userAgent,omitempty"`
	Version            string            `json:"version,omitempty"`
	BufferedStream     bool              `json:"bufferedStream,omitempty"`
	ModelMapping       map[string]string `json:"modelMapping,omitempty"`
	Accounts           []KiroAccount     `json:"accounts"`
}

// DefaultKiroModelMapping returns the default Claude model → Kiro model ID mapping.
func DefaultKiroModelMapping() map[string]string {
	return map[string]string{
		"claude-opus-4-6":            "claude-opus-4.6",
		"claude-opus-4-5":            "claude-opus-4.5",
		"claude-opus-4-5-20251101":   "claude-opus-4.5",
		"claude-opus-4-5-20250514":   "claude-opus-4.5",
		"claude-haiku-4.5":           "claude-haiku-4.5",
		"claude-haiku-4-5":           "claude-haiku-4.5",
		"claude-haiku-4-5-20251001":  "claude-haiku-4.5",
		"claude-sonnet-4-5":          "claude-sonnet-4.5",
		"claude-sonnet-4-5-20250929": "claude-sonnet-4.5",
		"claude-sonnet-4-5-20250514": "claude-sonnet-4.5",
		"claude-sonnet-4":            "claude-sonnet-4",
		"claude-sonnet-4-20250514":   "claude-sonnet-4",
		"auto":                       "claude-sonnet-4.5",
	}
}

// ToCredentials 将 KiroAccount 转换为 KiroCredentials
func (a *KiroAccount) ToCredentials() *KiroCredentials {
	return &KiroCredentials{
		AccessToken:  a.AccessToken,
		RefreshToken: a.RefreshToken,
		ProfileArn:   a.ProfileArn,
		ExpiresAt:    a.ExpiresAt,
		Region:       a.Region,
		MachineID:    a.MachineId,
		AuthMethod:   a.AuthMethod,
		Provider:     a.Provider,
		ClientId:     a.ClientId,
		ClientSecret: a.ClientSecret,
	}
}

// GetActiveAccount 获取当前激活的账号
func (c *KiroMultiConfig) GetActiveAccount() *KiroAccount {
	if c == nil || len(c.Accounts) == 0 {
		return nil
	}
	for i := range c.Accounts {
		if c.Accounts[i].RefreshToken == c.ActiveRefreshToken {
			return &c.Accounts[i]
		}
	}
	// 如果没有找到激活账号，返回第一个
	if len(c.Accounts) > 0 {
		return &c.Accounts[0]
	}
	return nil
}

// FindAccountByRefreshToken 根据 refreshToken 查找账号
func (c *KiroMultiConfig) FindAccountByRefreshToken(refreshToken string) *KiroAccount {
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

// UpdateAccount 更新账号信息
func (c *KiroMultiConfig) UpdateAccount(account *KiroAccount) bool {
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

// DeleteAccount 删除账号
func (c *KiroMultiConfig) DeleteAccount(refreshToken string) bool {
	if c == nil {
		return false
	}
	for i := range c.Accounts {
		if c.Accounts[i].RefreshToken == refreshToken {
			c.Accounts = append(c.Accounts[:i], c.Accounts[i+1:]...)
			// 如果删除的是激活账号，切换到第一个
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

// IsMonthlyRequestCountError 检查错误消息是否为 MONTHLY_REQUEST_COUNT 错误
// 1. 直接字符串包含匹配
// 2. JSON reason 字段
// 3. JSON 嵌套 error.reason 字段
func IsMonthlyRequestCountError(body string) bool {
	// 1. 直接字符串包含匹配
	if strings.Contains(body, "MONTHLY_REQUEST_COUNT") {
		return true
	}

	// 尝试解析 JSON
	var data map[string]interface{}
	if err := json.Unmarshal([]byte(body), &data); err != nil {
		// 不是有效 JSON，只能依赖字符串匹配
		return false
	}

	// 2. JSON reason 字段
	if reason, ok := data["reason"].(string); ok && reason == "MONTHLY_REQUEST_COUNT" {
		return true
	}

	// 3. JSON 嵌套 error.reason 字段
	if errorObj, ok := data["error"].(map[string]interface{}); ok {
		if reason, ok := errorObj["reason"].(string); ok && reason == "MONTHLY_REQUEST_COUNT" {
			return true
		}
	}

	return false
}

// IsTemporarilySuspendedError 检查错误消息是否为 TEMPORARILY_SUSPENDED 错误（账号被封禁）
// 参考当前项目 desktop/kiro.go 的实现，支持两种检测方式：
// 1. 直接字符串包含匹配
// 2. JSON reason 字段
func IsTemporarilySuspendedError(body string) bool {
	// 1. 直接字符串包含匹配
	if strings.Contains(body, "TEMPORARILY_SUSPENDED") {
		return true
	}

	// 尝试解析 JSON
	var data map[string]interface{}
	if err := json.Unmarshal([]byte(body), &data); err != nil {
		// 不是有效 JSON，只能依赖字符串匹配
		return false
	}

	// 2. JSON reason 字段
	if reason, ok := data["reason"].(string); ok && reason == "TEMPORARILY_SUSPENDED" {
		return true
	}

	// 3. JSON 嵌套 error.reason 字段（完整性考虑）
	if errorObj, ok := data["error"].(map[string]interface{}); ok {
		if reason, ok := errorObj["reason"].(string); ok && reason == "TEMPORARILY_SUSPENDED" {
			return true
		}
	}

	return false
}

// UpdateAccountStatusByRefreshToken 更新指定 refreshToken 的账号状态
// 同时更新 kiro.json 中匹配的账号
func UpdateAccountStatusByRefreshToken(refreshToken string, status KiroAccountStatus, multiConfigPath string) error {
	if refreshToken == "" {
		return fmt.Errorf("refreshToken is required")
	}

	// 更新 kiro.json
	multiConfig, err := LoadKiroMultiConfig(multiConfigPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("failed to load kiro.json: %w", err)
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
		if err := SaveKiroMultiConfig(multiConfigPath, multiConfig); err != nil {
			return fmt.Errorf("failed to save kiro.json: %w", err)
		}
	}

	return nil
}

// GetAvailableAccounts 返回状态不是 banned/exhausted 的可用账号
func (c *KiroMultiConfig) GetAvailableAccounts() []*KiroAccount {
	if c == nil {
		return nil
	}
	var result []*KiroAccount
	for i := range c.Accounts {
		s := c.Accounts[i].Status
		if s != KiroStatusBanned && s != KiroStatusExhausted {
			result = append(result, &c.Accounts[i])
		}
	}
	return result
}

// GetRegion 返回全局 region，空值默认 "us-east-1"
func (c *KiroMultiConfig) GetRegion() string {
	if c == nil || strings.TrimSpace(c.Region) == "" {
		return "us-east-1"
	}
	return strings.TrimSpace(c.Region)
}

// GetRotationMode 返回 rotationMode，空值默认 "fixed"
func (c *KiroMultiConfig) GetRotationMode() string {
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

// EffectiveWeight 返回 Weight，<=0 时返回 1
func (a *KiroAccount) EffectiveWeight() int {
	if a == nil || a.Weight <= 0 {
		return 1
	}
	return a.Weight
}
