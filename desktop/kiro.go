package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"path/filepath"
	"strings"

	"clisimplehub/internal/plugin"
)

// =============================================================================
// Kiro Configuration Methods
// =============================================================================

// KiroConfig represents Kiro-specific configuration
type KiroConfig struct {
	RefreshToken   string `json:"refreshToken"`
	ProfileArn     string `json:"profileArn"`
	Region         string `json:"region"`
	ProxyURL       string `json:"proxyUrl"`
	UserAgent      string `json:"userAgent"`
	Version        string `json:"version"`
	BufferedStream bool   `json:"bufferedStream"`
	AuthMethod     string `json:"authMethod"`
	Provider       string `json:"provider"`
	ClientId       string `json:"clientId"`
	ClientSecret   string `json:"clientSecret"`
	AccessToken    string `json:"accessToken,omitempty"`
	ExpiresAt      string `json:"expiresAt,omitempty"`
}

// kiroProvider returns the cached KiroDesktopProvider or nil.
func kiroProvider() plugin.KiroDesktopProvider {
	return plugin.GetKiroDesktopProviderCached()
}

// GetKiroConfig retrieves Kiro configuration from kiro.json active account
func (a *App) GetKiroConfig() (*KiroConfig, error) {
	kp := kiroProvider()
	if kp == nil {
		return nil, fmt.Errorf("kiro plugin not available")
	}
	raw, err := kp.GetKiroConfig(a.getKiroMultiConfigPath())
	if err != nil {
		return nil, err
	}
	var config KiroConfig
	if err := json.Unmarshal(raw, &config); err != nil {
		return nil, err
	}
	return &config, nil
}

// SaveKiroConfig saves Kiro configuration to kiro.json
func (a *App) SaveKiroConfig(config *KiroConfig) error {
	if config == nil {
		return fmt.Errorf("nil config")
	}
	kp := kiroProvider()
	if kp == nil {
		return fmt.Errorf("kiro plugin not available")
	}
	configJSON, err := json.Marshal(config)
	if err != nil {
		return err
	}
	_, err = kp.SaveKiroConfig(a.getKiroMultiConfigPath(), configJSON)
	return err
}

// KiroGlobalConfigDTO represents Kiro global settings + model mapping for frontend
type KiroGlobalConfigDTO struct {
	Region         string            `json:"region"`
	ProxyURL       string            `json:"proxyUrl"`
	UserAgent      string            `json:"userAgent"`
	Version        string            `json:"version"`
	BufferedStream bool              `json:"bufferedStream"`
	RotationMode   string            `json:"rotationMode"`
	ModelMapping   map[string]string `json:"modelMapping"`
}

// GetKiroGlobalConfig returns the Kiro global configuration and model mapping from kiro.json
func (a *App) GetKiroGlobalConfig() (*KiroGlobalConfigDTO, error) {
	kp := kiroProvider()
	if kp == nil {
		return nil, fmt.Errorf("kiro plugin not available")
	}
	raw, err := kp.GetKiroGlobalConfig(a.getKiroMultiConfigPath())
	if err != nil {
		return nil, err
	}
	var dto KiroGlobalConfigDTO
	if err := json.Unmarshal(raw, &dto); err != nil {
		return nil, err
	}
	return &dto, nil
}

// SaveKiroGlobalConfig saves Kiro global settings + model mapping to kiro.json and reloads transformers
func (a *App) SaveKiroGlobalConfig(dto *KiroGlobalConfigDTO) error {
	if dto == nil {
		return fmt.Errorf("nil config")
	}
	kp := kiroProvider()
	if kp == nil {
		return fmt.Errorf("kiro plugin not available")
	}
	dtoJSON, err := json.Marshal(dto)
	if err != nil {
		return err
	}
	return kp.SaveKiroGlobalConfig(a.getKiroMultiConfigPath(), dtoJSON)
}

type KiroTestResult struct {
	AccessToken  string `json:"accessToken"`
	RefreshToken string `json:"refreshToken,omitempty"`
	ExpiresAt    string `json:"expiresAt"`
	ProfileArn   string `json:"profileArn,omitempty"`
	Region       string `json:"region,omitempty"`
}

type KiroUsageInput struct {
	AccessToken  string `json:"accessToken"`
	RefreshToken string `json:"refreshToken,omitempty"`
	MachineID    string `json:"machineId,omitempty"`
	ProfileArn   string `json:"profileArn,omitempty"`
	Region       string `json:"region,omitempty"`
	ProxyURL     string `json:"proxyUrl,omitempty"`
	UserAgent    string `json:"userAgent,omitempty"`
	Version      string `json:"version,omitempty"`
}

type KiroUsageResult struct {
	SubscriptionTitle string          `json:"subscriptionTitle,omitempty"`
	UsageLimit        float64         `json:"usageLimit"`
	CurrentUsage      float64         `json:"currentUsage"`
	Balance           float64         `json:"balance"`
	UsagePct          float64         `json:"usagePct"`
	IsLowBalance      bool            `json:"isLowBalance"`
	Details           json.RawMessage `json:"details,omitempty"`
}

// TestKiroRefreshToken validates the provided refreshToken by exchanging it for a new accessToken.
func (a *App) TestKiroRefreshToken(config *KiroConfig) (*KiroTestResult, error) {
	if config == nil {
		return nil, fmt.Errorf("nil config")
	}
	kp := kiroProvider()
	if kp == nil {
		return nil, fmt.Errorf("kiro plugin not available")
	}
	configJSON, err := json.Marshal(config)
	if err != nil {
		return nil, err
	}
	raw, err := kp.TestRefreshToken(configJSON)
	if err != nil {
		return nil, err
	}
	var result KiroTestResult
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// GetKiroUsage fetches current account usage limits based on the provided accessToken.
func (a *App) GetKiroUsage(input *KiroUsageInput) (*KiroUsageResult, error) {
	if input == nil {
		return nil, fmt.Errorf("nil input")
	}
	kp := kiroProvider()
	if kp == nil {
		return nil, fmt.Errorf("kiro plugin not available")
	}
	inputJSON, err := json.Marshal(input)
	if err != nil {
		return nil, err
	}
	ctx := a.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	raw, err := kp.GetKiroUsage(ctx, inputJSON)
	if err != nil {
		return nil, err
	}
	var result KiroUsageResult
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// =============================================================================
// Kiro Multi-Account Management Methods
// =============================================================================

func (a *App) getKiroMultiConfigPath() string {
	kp := kiroProvider()
	if kp == nil {
		return ""
	}
	defaultPath := kp.DefaultMultiConfigBasename()
	if a != nil && a.configLoader != nil {
		if p := strings.TrimSpace(a.configLoader.GetPath()); p != "" {
			return filepath.Join(filepath.Dir(p), filepath.Base(defaultPath))
		}
	}
	return defaultPath
}

// KiroAccountDTO 前端账号数据传输对象
// refreshToken 作为主键
type KiroAccountDTO struct {
	RefreshToken       string          `json:"refreshToken"`
	AccessToken        string          `json:"accessToken,omitempty"`
	ProfileArn         string          `json:"profileArn,omitempty"`
	ExpiresAt          string          `json:"expiresAt,omitempty"`
	Region             string          `json:"region,omitempty"`
	AuthMethod         string          `json:"authMethod,omitempty"`
	Provider           string          `json:"provider,omitempty"`
	ClientId           string          `json:"clientId,omitempty"`
	ClientSecret       string          `json:"clientSecret,omitempty"`
	MachineId          string          `json:"machineId,omitempty"`
	Status             string          `json:"status"`
	Weight             int             `json:"weight,omitempty"`
	SubscriptionTitle  string          `json:"subscriptionTitle,omitempty"`
	UsageLimit         float64         `json:"usageLimit,omitempty"`
	CurrentUsage       float64         `json:"currentUsage,omitempty"`
	Balance            float64         `json:"balance"`
	UsagePct           float64         `json:"usagePct,omitempty"`
	Email              string          `json:"email,omitempty"`
	UserID             string          `json:"userId,omitempty"`
	DaysUntilReset     *int32          `json:"daysUntilReset,omitempty"`
	NextDateReset      *float64        `json:"nextDateReset,omitempty"`
	LastUsageCheck     string          `json:"lastUsageCheck,omitempty"`
	UsageBreakdownList json.RawMessage `json:"usageBreakdownList,omitempty"`
	ProxyUrl           string          `json:"proxyUrl,omitempty"`
	UserAgent          string          `json:"userAgent,omitempty"`
	Version            string          `json:"version,omitempty"`
	CreatedAt          string          `json:"createdAt,omitempty"`
	UpdatedAt          string          `json:"updatedAt,omitempty"`
	IsActive           bool            `json:"isActive"`
}

// KiroAccountsResponse 多账号列表响应
type KiroAccountsResponse struct {
	ActiveRefreshToken string           `json:"activeRefreshToken"`
	Accounts           []KiroAccountDTO `json:"accounts"`
}

// KiroMultiConfigDTO 多账号配置传输对象（用于 WebDAV 备份/恢复）
type KiroMultiConfigDTO struct {
	ActiveRefreshToken string            `json:"activeRefreshToken"`
	Region             string            `json:"region,omitempty"`
	ProxyURL           string            `json:"proxyUrl,omitempty"`
	UserAgent          string            `json:"userAgent,omitempty"`
	Version            string            `json:"version,omitempty"`
	BufferedStream     bool              `json:"bufferedStream,omitempty"`
	RotationMode       string            `json:"rotationMode,omitempty"`
	ModelMapping       map[string]string `json:"modelMapping,omitempty"`
	Accounts           []KiroAccountDTO  `json:"accounts"`
	ReplaceMode        bool              `json:"replaceMode,omitempty"` // 替换模式：清空现有账号
}

// GetKiroAccounts 获取所有 Kiro 账号
func (a *App) GetKiroAccounts() (*KiroAccountsResponse, error) {
	kp := kiroProvider()
	if kp == nil {
		return nil, fmt.Errorf("kiro plugin not available")
	}
	raw, err := kp.GetAccounts(a.getKiroMultiConfigPath())
	if err != nil {
		return nil, err
	}
	var resp KiroAccountsResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// GetActiveKiroAccount 获取当前激活的 Kiro 账号
func (a *App) GetActiveKiroAccount() (*KiroAccountDTO, error) {
	kp := kiroProvider()
	if kp == nil {
		return nil, fmt.Errorf("kiro plugin not available")
	}
	raw, err := kp.GetActiveAccount(a.getKiroMultiConfigPath())
	if err != nil {
		return nil, err
	}
	if string(raw) == "null" {
		return nil, nil
	}
	var dto KiroAccountDTO
	if err := json.Unmarshal(raw, &dto); err != nil {
		return nil, err
	}
	return &dto, nil
}

// SetActiveKiroAccount 设置激活的 Kiro 账号（参数为 refreshToken）
func (a *App) SetActiveKiroAccount(refreshToken string) error {
	kp := kiroProvider()
	if kp == nil {
		return fmt.Errorf("kiro plugin not available")
	}
	return kp.SetActiveAccount(a.getKiroMultiConfigPath(), refreshToken)
}

// AddKiroAccount 添加新的 Kiro 账号
func (a *App) AddKiroAccount(dto *KiroAccountDTO) (*KiroAccountDTO, error) {
	if dto == nil {
		log.Printf("Kiro AddKiroAccount: rejected: nil dto")
		return nil, fmt.Errorf("account data is required")
	}
	kp := kiroProvider()
	if kp == nil {
		return nil, fmt.Errorf("kiro plugin not available")
	}
	dtoJSON, err := json.Marshal(dto)
	if err != nil {
		return nil, err
	}
	raw, err := kp.AddAccount(a.getKiroMultiConfigPath(), dtoJSON)
	if err != nil {
		return nil, err
	}
	var result KiroAccountDTO
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// UpdateKiroAccount 更新 Kiro 账号
func (a *App) UpdateKiroAccount(dto *KiroAccountDTO) error {
	if dto == nil {
		return fmt.Errorf("account data is required")
	}
	kp := kiroProvider()
	if kp == nil {
		return fmt.Errorf("kiro plugin not available")
	}
	dtoJSON, err := json.Marshal(dto)
	if err != nil {
		return err
	}
	return kp.UpdateAccount(a.getKiroMultiConfigPath(), dtoJSON)
}

// DeleteKiroAccount 删除 Kiro 账号（参数为 refreshToken）
func (a *App) DeleteKiroAccount(refreshToken string) error {
	kp := kiroProvider()
	if kp == nil {
		return fmt.Errorf("kiro plugin not available")
	}
	return kp.DeleteAccount(a.getKiroMultiConfigPath(), refreshToken)
}

// TestKiroAccount 测试指定账号的连接（参数为 refreshToken）
func (a *App) TestKiroAccount(refreshToken string) (*KiroTestResult, error) {
	kp := kiroProvider()
	if kp == nil {
		return nil, fmt.Errorf("kiro plugin not available")
	}
	raw, err := kp.TestAccount(a.getKiroMultiConfigPath(), refreshToken)
	if err != nil {
		return nil, err
	}
	var result KiroTestResult
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// GetKiroAccountUsage 获取指定账号的用量信息（参数为 refreshToken）
func (a *App) GetKiroAccountUsage(refreshToken string) (*KiroUsageResult, error) {
	kp := kiroProvider()
	if kp == nil {
		return nil, fmt.Errorf("kiro plugin not available")
	}
	ctx := a.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	raw, err := kp.GetAccountUsage(ctx, a.getKiroMultiConfigPath(), refreshToken)
	if err != nil {
		return nil, err
	}
	var result KiroUsageResult
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// =============================================================================
// Kiro Multi-Account WebDAV Backup/Restore Methods
// =============================================================================

// SaveKiroMultiConfigFromBackup 从 WebDAV 备份恢复多账号配置
// 支持替换模式（ReplaceMode=true）和合并模式（ReplaceMode=false）
func (a *App) SaveKiroMultiConfigFromBackup(dto *KiroMultiConfigDTO) error {
	if dto == nil {
		return fmt.Errorf("config is nil")
	}
	kp := kiroProvider()
	if kp == nil {
		return fmt.Errorf("kiro plugin not available")
	}
	dtoJSON, err := json.Marshal(dto)
	if err != nil {
		return err
	}
	return kp.SaveMultiConfigFromBackup(a.getKiroMultiConfigPath(), dtoJSON)
}

// kiroSyncExportRaw exports the raw kiro multi config JSON for sync/backup.
// Uses the ConfigSyncExporter plugin interface to avoid direct kiro imports.
// Returns nil when there are no accounts (preserving original skip-on-empty behavior).
func (a *App) kiroSyncExportRaw() json.RawMessage {
	p := plugin.ByName("kiro")
	if p == nil {
		return nil
	}
	if exporter, ok := p.(plugin.ConfigSyncExporter); ok {
		configPath := ""
		if a.configLoader != nil {
			configPath = a.configLoader.GetPath()
		}
		_, data, err := exporter.SyncExport(configPath)
		if err != nil || len(data) == 0 {
			return nil
		}
		// Skip export when config has no accounts to avoid overwriting remote data
		var probe struct {
			Accounts []json.RawMessage `json:"accounts"`
		}
		if json.Unmarshal(data, &probe) != nil || len(probe.Accounts) == 0 {
			return nil
		}
		return data
	}
	return nil
}
