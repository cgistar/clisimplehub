package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"clisimplehub/internal/executor"
	kiroCore "clisimplehub/internal/transformer/kiro"
	kiroClaude "clisimplehub/internal/transformer/kiro/claude"
	kiroShared "clisimplehub/internal/transformer/kiro/shared"
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

// writeFileAtomic0600 原子写入文件并设置 0600 权限
func writeFileAtomic0600(path string, data []byte) error {
	path = strings.TrimSpace(path)
	if path == "" {
		return fmt.Errorf("empty path")
	}

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}

	tmp, err := os.CreateTemp(dir, filepath.Base(path)+".tmp-*")
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

	if runtime.GOOS != "windows" {
		if err := tmp.Chmod(0600); err != nil {
			return err
		}
	}
	if _, err := tmp.Write(data); err != nil {
		return err
	}
	if err := tmp.Sync(); err != nil {
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}

	// Windows: 删除目标文件后再重命名
	if runtime.GOOS == "windows" {
		_ = os.Remove(path)
	}

	if err := os.Rename(tmpName, path); err != nil {
		return err
	}
	renamed = true

	if runtime.GOOS != "windows" {
		if err := os.Chmod(path, 0600); err != nil {
			return err
		}
	}
	return nil
}

// GetKiroConfig retrieves Kiro configuration from kiro.json active account
func (a *App) GetKiroConfig() (*KiroConfig, error) {
	if a.storage == nil {
		return nil, fmt.Errorf("storage not initialized")
	}

	config := &KiroConfig{
		Region:     "us-east-1",
		AuthMethod: "social",
	}

	// Read from kiro.json active account
	mc, err := a.loadOrCreateMultiConfig()
	if err == nil && mc != nil {
		config.ProxyURL = mc.ProxyURL
		config.UserAgent = mc.UserAgent
		config.Version = mc.Version
		config.BufferedStream = mc.BufferedStream

		if account := mc.GetActiveAccount(); account != nil {
			config.RefreshToken = account.RefreshToken
			config.ProfileArn = account.ProfileArn
			config.AccessToken = account.AccessToken
			config.AuthMethod = account.AuthMethod
			config.Provider = account.Provider
			config.ClientId = account.ClientId
			config.ClientSecret = account.ClientSecret
			if !account.ExpiresAt.IsZero() {
				config.ExpiresAt = account.ExpiresAt.Format(time.RFC3339Nano)
			}
			if strings.TrimSpace(account.Region) != "" {
				config.Region = account.Region
			}
			if strings.TrimSpace(config.AuthMethod) == "" {
				config.AuthMethod = "social"
			}
		}
	}

	return config, nil
}

// SaveKiroConfig saves Kiro configuration to kiro.json
func (a *App) SaveKiroConfig(config *KiroConfig) error {
	if a.storage == nil {
		return fmt.Errorf("storage not initialized")
	}

	if strings.TrimSpace(config.RefreshToken) == "" {
		return fmt.Errorf("refresh token is required")
	}

	// Validate IdC fields
	authMethod := strings.ToLower(strings.TrimSpace(config.AuthMethod))
	if authMethod == "idc" {
		if strings.TrimSpace(config.ClientId) == "" || strings.TrimSpace(config.ClientSecret) == "" {
			return fmt.Errorf("clientId and clientSecret are required for IdC authentication")
		}
	}

	multiConfig, err := a.loadOrCreateMultiConfig()
	if err != nil {
		return fmt.Errorf("failed to load kiro multi config: %w", err)
	}

	newRefreshToken := strings.TrimSpace(config.RefreshToken)
	newRegion := strings.TrimSpace(config.Region)
	if newRegion == "" {
		newRegion = "us-east-1"
	}
	fallbackMachineID := kiroCore.ComputeMachineID(newRefreshToken)

	// Find or create account in kiro.json
	account := multiConfig.FindAccountByRefreshToken(newRefreshToken)
	if account == nil {
		// Check if there's an existing active account whose refreshToken is being replaced
		activeAccount := multiConfig.GetActiveAccount()
		if activeAccount != nil {
			oldRefreshToken := strings.TrimSpace(activeAccount.RefreshToken)
			refreshTokenChanged := oldRefreshToken != "" && oldRefreshToken != newRefreshToken
			if refreshTokenChanged {
				if strings.TrimSpace(config.AccessToken) == "" {
					return fmt.Errorf("accessToken is required when refreshToken changes; please test first")
				}
				if strings.TrimSpace(config.ExpiresAt) == "" {
					return fmt.Errorf("expiresAt is required when refreshToken changes; please test first")
				}
				// Update existing active account with new refreshToken
				account = activeAccount
			}
		}
	}

	if account != nil {
		// Update existing account
		account.RefreshToken = newRefreshToken
		account.Region = newRegion
		if mid := kiroShared.NormalizeMachineID(account.MachineId); mid != "" {
			account.MachineId = mid
		} else {
			account.MachineId = fallbackMachineID
		}
		account.AuthMethod = config.AuthMethod
		account.Provider = config.Provider
		account.ClientId = config.ClientId
		account.ClientSecret = config.ClientSecret

		accessToken := strings.TrimSpace(config.AccessToken)
		expiresAtStr := strings.TrimSpace(config.ExpiresAt)
		if accessToken != "" && expiresAtStr != "" {
			expiresAt, err := time.Parse(time.RFC3339Nano, expiresAtStr)
			if err != nil {
				expiresAt, err = time.Parse(time.RFC3339, expiresAtStr)
			}
			if err != nil {
				return fmt.Errorf("invalid expiresAt: %w", err)
			}
			account.AccessToken = accessToken
			account.ExpiresAt = expiresAt
		}

		if strings.TrimSpace(config.ProfileArn) != "" {
			account.ProfileArn = config.ProfileArn
		}
		if authMethod == "idc" {
			account.ProfileArn = ""
		}

		account.Status = kiroShared.KiroStatusValid
		account.UpdatedAt = time.Now()
		multiConfig.UpdateAccount(account)
	} else {
		// New account
		now := time.Now()
		newAccount := &kiroShared.KiroAccount{
			RefreshToken: newRefreshToken,
			Region:       newRegion,
			MachineId:    fallbackMachineID,
			AuthMethod:   config.AuthMethod,
			Provider:     config.Provider,
			ClientId:     config.ClientId,
			ClientSecret: config.ClientSecret,
			Status:       kiroShared.KiroStatusValid,
			CreatedAt:    now,
			UpdatedAt:    now,
		}

		accessToken := strings.TrimSpace(config.AccessToken)
		expiresAtStr := strings.TrimSpace(config.ExpiresAt)
		if accessToken != "" && expiresAtStr != "" {
			expiresAt, err := time.Parse(time.RFC3339Nano, expiresAtStr)
			if err != nil {
				expiresAt, err = time.Parse(time.RFC3339, expiresAtStr)
			}
			if err != nil {
				return fmt.Errorf("invalid expiresAt: %w", err)
			}
			newAccount.AccessToken = accessToken
			newAccount.ExpiresAt = expiresAt
		}

		if strings.TrimSpace(config.ProfileArn) != "" {
			newAccount.ProfileArn = config.ProfileArn
		}
		if authMethod == "idc" {
			newAccount.ProfileArn = ""
		}

		multiConfig.Accounts = append(multiConfig.Accounts, *newAccount)
	}

	// Set as active account
	multiConfig.ActiveRefreshToken = newRefreshToken

	// Save global settings
	multiConfig.ProxyURL = config.ProxyURL
	multiConfig.UserAgent = config.UserAgent
	multiConfig.Version = config.Version
	multiConfig.BufferedStream = config.BufferedStream

	multiPath := a.getKiroMultiConfigPath()
	return kiroShared.SaveKiroMultiConfig(multiPath, multiConfig)
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
	mc, err := a.loadOrCreateMultiConfig()
	if err != nil {
		return nil, fmt.Errorf("failed to load kiro config: %w", err)
	}
	mapping := mc.ModelMapping
	if len(mapping) == 0 {
		mapping = kiroShared.DefaultKiroModelMapping()
	}
	clone := make(map[string]string, len(mapping))
	for k, v := range mapping {
		clone[k] = v
	}
	return &KiroGlobalConfigDTO{
		Region:         mc.GetRegion(),
		ProxyURL:       mc.ProxyURL,
		UserAgent:      mc.UserAgent,
		Version:        mc.Version,
		BufferedStream: mc.BufferedStream,
		RotationMode:   mc.GetRotationMode(),
		ModelMapping:   clone,
	}, nil
}

// SaveKiroGlobalConfig saves Kiro global settings + model mapping to kiro.json and reloads transformers
func (a *App) SaveKiroGlobalConfig(dto *KiroGlobalConfigDTO) error {
	if dto == nil {
		return fmt.Errorf("nil config")
	}
	mc, err := a.loadOrCreateMultiConfig()
	if err != nil {
		return fmt.Errorf("failed to load kiro config: %w", err)
	}
	mc.Region = strings.TrimSpace(dto.Region)
	mc.ProxyURL = strings.TrimSpace(dto.ProxyURL)
	mc.UserAgent = strings.TrimSpace(dto.UserAgent)
	mc.Version = strings.TrimSpace(dto.Version)
	mc.BufferedStream = dto.BufferedStream
	mc.RotationMode = strings.TrimSpace(dto.RotationMode)
	if dto.ModelMapping != nil {
		sanitized := make(map[string]string, len(dto.ModelMapping))
		for k, v := range dto.ModelMapping {
			alias := strings.TrimSpace(k)
			name := strings.TrimSpace(v)
			if alias != "" && name != "" {
				sanitized[alias] = name
			}
		}
		mc.ModelMapping = sanitized
	}

	multiPath := a.getKiroMultiConfigPath()
	if err := kiroShared.SaveKiroMultiConfig(multiPath, mc); err != nil {
		return fmt.Errorf("failed to save kiro config: %w", err)
	}
	if err := kiroClaude.ReloadAllTransformers(); err != nil {
		return fmt.Errorf("saved kiro config but failed to reload transformers: %w", err)
	}
	return nil
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
	SubscriptionTitle string                        `json:"subscriptionTitle,omitempty"`
	UsageLimit        float64                       `json:"usageLimit"`
	CurrentUsage      float64                       `json:"currentUsage"`
	Balance           float64                       `json:"balance"`
	UsagePct          float64                       `json:"usagePct"`
	IsLowBalance      bool                          `json:"isLowBalance"`
	Details           *kiroCore.UsageLimitsResponse `json:"details,omitempty"`
}

// TestKiroRefreshToken validates the provided refreshToken by exchanging it for a new accessToken.
// This is a best-effort network call and does not persist anything by itself.
func (a *App) TestKiroRefreshToken(config *KiroConfig) (*KiroTestResult, error) {
	if config == nil {
		return nil, fmt.Errorf("nil config")
	}
	refreshToken := strings.TrimSpace(config.RefreshToken)
	if refreshToken == "" {
		return nil, fmt.Errorf("refresh token is required")
	}

	// Validate IdC fields
	authMethod := strings.ToLower(strings.TrimSpace(config.AuthMethod))
	if authMethod == "idc" {
		if strings.TrimSpace(config.ClientId) == "" || strings.TrimSpace(config.ClientSecret) == "" {
			return nil, fmt.Errorf("clientId and clientSecret are required for IdC authentication")
		}
	}

	region := strings.TrimSpace(config.Region)
	if region == "" {
		region = "us-east-1"
	}

	creds := &kiroShared.KiroCredentials{
		RefreshToken: refreshToken,
		Region:       region,
		AuthMethod:   config.AuthMethod,
		ClientId:     config.ClientId,
		ClientSecret: config.ClientSecret,
	}

	mgr := kiroClaude.NewKiroAuthManager(creds, "", strings.TrimSpace(config.ProxyURL), strings.TrimSpace(config.Version))
	accessToken, err := mgr.ForceRefresh()
	if err != nil {
		return nil, err
	}

	expiresAt := ""
	if !creds.ExpiresAt.IsZero() {
		expiresAt = creds.ExpiresAt.Format(time.RFC3339Nano)
	}

	return &KiroTestResult{
		AccessToken:  accessToken,
		RefreshToken: strings.TrimSpace(creds.RefreshToken),
		ExpiresAt:    expiresAt,
		ProfileArn:   strings.TrimSpace(creds.ProfileArn),
		Region:       strings.TrimSpace(creds.Region),
	}, nil
}

// GetKiroUsage fetches current account usage limits based on the provided accessToken.
func (a *App) GetKiroUsage(input *KiroUsageInput) (*KiroUsageResult, error) {
	if input == nil {
		return nil, fmt.Errorf("nil input")
	}
	accessToken := strings.TrimSpace(input.AccessToken)
	if accessToken == "" {
		return nil, fmt.Errorf("accessToken is required")
	}

	machineID := ""
	if v := strings.TrimSpace(input.MachineID); v != "" {
		machineID = v
	} else if v := strings.TrimSpace(input.RefreshToken); v != "" {
		machineID = kiroCore.ComputeMachineID(v)
	}
	machineID = strings.TrimSpace(machineID)
	if machineID == "" {
		return nil, fmt.Errorf("machineId is required (or provide refreshToken to compute it)")
	}
	if strings.ContainsAny(machineID, "\r\n") {
		return nil, fmt.Errorf("invalid machineId")
	}


	baseCtx := a.ctx
	if baseCtx == nil {
		baseCtx = context.Background()
	}
	ctx, cancel := context.WithTimeout(baseCtx, 12*time.Second)
	defer cancel()

	proxyURL := strings.TrimSpace(input.ProxyURL)
	client := executor.NewHTTPClientForcedProxyURL(proxyURL, 10*time.Second)

	result, err := kiroCore.FetchUsage(ctx, client, kiroCore.UsageQuery{
		AccessToken:   accessToken,
		ProfileArn:    strings.TrimSpace(input.ProfileArn),
		Region:        strings.TrimSpace(input.Region),
		MachineID:     machineID,
		UserAgentBase: strings.TrimSpace(input.UserAgent),
		Version:       strings.TrimSpace(input.Version),
	})
	if err != nil {
		return nil, err
	}

	return &KiroUsageResult{
		SubscriptionTitle: strings.TrimSpace(result.SubscriptionTitle),
		UsageLimit:        result.UsageLimit,
		CurrentUsage:      result.CurrentUsage,
		Balance:           result.Balance,
		UsagePct:          result.UsagePct,
		IsLowBalance:      result.IsLowBalance,
		Details:           result.Details,
	}, nil
}

// =============================================================================
// Kiro Multi-Account Management Methods
// =============================================================================

func (a *App) getKiroMultiConfigPath() string {
	if a != nil && a.configLoader != nil {
		if p := strings.TrimSpace(a.configLoader.GetPath()); p != "" {
			return filepath.Join(filepath.Dir(p), filepath.Base(kiroShared.GetDefaultKiroMultiConfigPath()))
		}
	}
	return kiroShared.GetDefaultKiroMultiConfigPath()
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

func accountToDTO(account *kiroShared.KiroAccount, isActive bool) KiroAccountDTO {
	dto := KiroAccountDTO{
		RefreshToken:       account.RefreshToken,
		AccessToken:        account.AccessToken,
		ProfileArn:         account.ProfileArn,
		Region:             account.Region,
		AuthMethod:         account.AuthMethod,
		Provider:           account.Provider,
		ClientId:           account.ClientId,
		ClientSecret:       account.ClientSecret,
		MachineId:          account.MachineId,
		Status:             string(account.Status),
		Weight:             account.Weight,
		SubscriptionTitle:  account.SubscriptionTitle,
		UsageLimit:         account.UsageLimit,
		CurrentUsage:       account.CurrentUsage,
		Balance:            account.Balance,
		UsagePct:           account.UsagePct,
		Email:              account.Email,
		UserID:             account.UserID,
		DaysUntilReset:     account.DaysUntilReset,
		NextDateReset:      account.NextDateReset,
		UsageBreakdownList: account.UsageBreakdownList,
		ProxyUrl:           account.ProxyUrl,
		UserAgent:          account.UserAgent,
		Version:            account.Version,
		IsActive:           isActive,
	}
	if !account.ExpiresAt.IsZero() {
		dto.ExpiresAt = account.ExpiresAt.Format(time.RFC3339Nano)
	}
	if !account.LastUsageCheck.IsZero() {
		dto.LastUsageCheck = account.LastUsageCheck.Format(time.RFC3339Nano)
	}
	if !account.CreatedAt.IsZero() {
		dto.CreatedAt = account.CreatedAt.Format(time.RFC3339Nano)
	}
	if !account.UpdatedAt.IsZero() {
		dto.UpdatedAt = account.UpdatedAt.Format(time.RFC3339Nano)
	}
	return dto
}

func dtoToAccount(dto *KiroAccountDTO) *kiroShared.KiroAccount {
	account := &kiroShared.KiroAccount{
		RefreshToken:       dto.RefreshToken,
		AccessToken:        dto.AccessToken,
		ProfileArn:         dto.ProfileArn,
		Region:             dto.Region,
		AuthMethod:         dto.AuthMethod,
		Provider:           dto.Provider,
		ClientId:           dto.ClientId,
		ClientSecret:       dto.ClientSecret,
		MachineId:          dto.MachineId,
		Status:             kiroShared.KiroAccountStatus(dto.Status),
		Weight:             dto.Weight,
		SubscriptionTitle:  dto.SubscriptionTitle,
		UsageLimit:         dto.UsageLimit,
		CurrentUsage:       dto.CurrentUsage,
		Balance:            dto.Balance,
		UsagePct:           dto.UsagePct,
		Email:              dto.Email,
		UserID:             dto.UserID,
		DaysUntilReset:     dto.DaysUntilReset,
		NextDateReset:      dto.NextDateReset,
		UsageBreakdownList: dto.UsageBreakdownList,
		ProxyUrl:           dto.ProxyUrl,
		UserAgent:          dto.UserAgent,
		Version:            dto.Version,
	}
	if dto.ExpiresAt != "" {
		if t, err := time.Parse(time.RFC3339Nano, dto.ExpiresAt); err == nil {
			account.ExpiresAt = t
		}
	}
	if dto.LastUsageCheck != "" {
		if t, err := time.Parse(time.RFC3339Nano, dto.LastUsageCheck); err == nil {
			account.LastUsageCheck = t
		}
	}
	if dto.CreatedAt != "" {
		if t, err := time.Parse(time.RFC3339Nano, dto.CreatedAt); err == nil {
			account.CreatedAt = t
		}
	}
	if dto.UpdatedAt != "" {
		if t, err := time.Parse(time.RFC3339Nano, dto.UpdatedAt); err == nil {
			account.UpdatedAt = t
		}
	}
	return account
}

// loadOrCreateMultiConfig 加载或创建多账号配置
func (a *App) loadOrCreateMultiConfig() (*kiroShared.KiroMultiConfig, error) {
	multiPath := a.getKiroMultiConfigPath()

	// 加载多账号配置
	config, err := kiroShared.LoadKiroMultiConfig(multiPath)
	if err != nil {
		if os.IsNotExist(err) {
			// 文件不存在，返回空配置（含默认 ModelMapping）
			return &kiroShared.KiroMultiConfig{
				ModelMapping: kiroShared.DefaultKiroModelMapping(),
				UserAgent: kiroShared.DefaultKiroUserAgentBase,
				Version:   kiroShared.DefaultKiroVersion,
				Accounts:     []kiroShared.KiroAccount{},
			}, nil
		}
		return nil, err
	}
	return config, nil
}

// GetKiroAccounts 获取所有 Kiro 账号
func (a *App) GetKiroAccounts() (*KiroAccountsResponse, error) {
	config, err := a.loadOrCreateMultiConfig()
	if err != nil {
		return nil, fmt.Errorf("failed to load kiro config: %w", err)
	}

	accounts := make([]KiroAccountDTO, 0, len(config.Accounts))
	for i := range config.Accounts {
		isActive := config.Accounts[i].RefreshToken == config.ActiveRefreshToken
		accounts = append(accounts, accountToDTO(&config.Accounts[i], isActive))
	}

	return &KiroAccountsResponse{
		ActiveRefreshToken: config.ActiveRefreshToken,
		Accounts:           accounts,
	}, nil
}

// GetActiveKiroAccount 获取当前激活的 Kiro 账号
func (a *App) GetActiveKiroAccount() (*KiroAccountDTO, error) {
	config, err := a.loadOrCreateMultiConfig()
	if err != nil {
		return nil, fmt.Errorf("failed to load kiro config: %w", err)
	}

	account := config.GetActiveAccount()
	if account == nil {
		return nil, nil
	}

	dto := accountToDTO(account, true)
	return &dto, nil
}

// SetActiveKiroAccount 设置激活的 Kiro 账号（参数为 refreshToken）
func (a *App) SetActiveKiroAccount(refreshToken string) error {
	if strings.TrimSpace(refreshToken) == "" {
		return fmt.Errorf("refreshToken is required")
	}

	config, err := a.loadOrCreateMultiConfig()
	if err != nil {
		return fmt.Errorf("failed to load kiro config: %w", err)
	}

	// 验证账号存在
	if config.FindAccountByRefreshToken(refreshToken) == nil {
		return fmt.Errorf("account not found")
	}

	config.ActiveRefreshToken = refreshToken

	multiPath := a.getKiroMultiConfigPath()
	return kiroShared.SaveKiroMultiConfig(multiPath, config)
}

// AddKiroAccount 添加新的 Kiro 账号
func (a *App) AddKiroAccount(dto *KiroAccountDTO) (*KiroAccountDTO, error) {
	if dto == nil {
		log.Printf("Kiro AddKiroAccount: rejected: nil dto")
		return nil, fmt.Errorf("account data is required")
	}
	if strings.TrimSpace(dto.RefreshToken) == "" {
		log.Printf("Kiro AddKiroAccount: rejected: empty refreshToken")
		return nil, fmt.Errorf("refreshToken is required")
	}

	config, err := a.loadOrCreateMultiConfig()
	if err != nil {
		log.Printf("Kiro AddKiroAccount: loadOrCreateMultiConfig failed: %v", err)
		return nil, fmt.Errorf("failed to load kiro config: %w", err)
	}

	// 检查是否已存在相同 refreshToken 的账号
	if config.FindAccountByRefreshToken(dto.RefreshToken) != nil {
		log.Printf("Kiro AddKiroAccount: rejected: duplicate account (accounts=%d)", len(config.Accounts))
		return nil, fmt.Errorf("account with this refreshToken already exists")
	}

	// 创建新账号
	now := time.Now()
	account := dtoToAccount(dto)
	if mid := kiroShared.NormalizeMachineID(account.MachineId); mid != "" {
		account.MachineId = mid
	} else {
		account.MachineId = kiroCore.ComputeMachineID(account.RefreshToken)
	}
	account.CreatedAt = now
	account.UpdatedAt = now
	if account.Status == "" {
		account.Status = kiroShared.KiroStatusUnknown
	}
	if account.Region == "" {
		account.Region = "us-east-1"
	}

	config.Accounts = append(config.Accounts, *account)

	// 如果是第一个账号，自动设为激活
	if len(config.Accounts) == 1 {
		config.ActiveRefreshToken = account.RefreshToken
	}

	multiPath := a.getKiroMultiConfigPath()
	if err := kiroShared.SaveKiroMultiConfig(multiPath, config); err != nil {
		return nil, fmt.Errorf("failed to save kiro config: %w", err)
	}

	isActive := account.RefreshToken == config.ActiveRefreshToken
	result := accountToDTO(account, isActive)
	return &result, nil
}

// UpdateKiroAccount 更新 Kiro 账号
func (a *App) UpdateKiroAccount(dto *KiroAccountDTO) error {
	if dto == nil {
		return fmt.Errorf("account data is required")
	}
	if strings.TrimSpace(dto.RefreshToken) == "" {
		return fmt.Errorf("refreshToken is required")
	}

	config, err := a.loadOrCreateMultiConfig()
	if err != nil {
		return fmt.Errorf("failed to load kiro config: %w", err)
	}

	existing := config.FindAccountByRefreshToken(dto.RefreshToken)
	if existing == nil {
		return fmt.Errorf("account not found")
	}

	// 更新字段
	account := dtoToAccount(dto)
	account.CreatedAt = existing.CreatedAt // 保留创建时间
	account.UpdatedAt = time.Now()
	if mid := kiroShared.NormalizeMachineID(account.MachineId); mid != "" {
		account.MachineId = mid
	} else {
		account.MachineId = existing.MachineId
	}

	if !config.UpdateAccount(account) {
		return fmt.Errorf("failed to update account")
	}

	multiPath := a.getKiroMultiConfigPath()
	return kiroShared.SaveKiroMultiConfig(multiPath, config)
}

// DeleteKiroAccount 删除 Kiro 账号（参数为 refreshToken）
func (a *App) DeleteKiroAccount(refreshToken string) error {
	if strings.TrimSpace(refreshToken) == "" {
		return fmt.Errorf("refreshToken is required")
	}

	config, err := a.loadOrCreateMultiConfig()
	if err != nil {
		return fmt.Errorf("failed to load kiro config: %w", err)
	}

	if !config.DeleteAccount(refreshToken) {
		return fmt.Errorf("account not found")
	}

	multiPath := a.getKiroMultiConfigPath()
	return kiroShared.SaveKiroMultiConfig(multiPath, config)
}

// TestKiroAccount 测试指定账号的连接（参数为 refreshToken）
func (a *App) TestKiroAccount(refreshToken string) (*KiroTestResult, error) {
	if strings.TrimSpace(refreshToken) == "" {
		return nil, fmt.Errorf("refreshToken is required")
	}

	config, err := a.loadOrCreateMultiConfig()
	if err != nil {
		return nil, fmt.Errorf("failed to load kiro config: %w", err)
	}

	account := config.FindAccountByRefreshToken(refreshToken)
	if account == nil {
		return nil, fmt.Errorf("account not found")
	}

	// 使用现有的测试逻辑
	testConfig := &KiroConfig{
		RefreshToken: account.RefreshToken,
		Region:       account.Region,
		AuthMethod:   account.AuthMethod,
		ClientId:     account.ClientId,
		ClientSecret: account.ClientSecret,
		ProxyURL:     account.ProxyUrl,
		Version:      account.Version,
	}

	result, err := a.TestKiroRefreshToken(testConfig)
	if err != nil {
		// 更新账号状态为未知
		account.Status = kiroShared.KiroStatusUnknown
		config.UpdateAccount(account)
		multiPath := a.getKiroMultiConfigPath()
		_ = kiroShared.SaveKiroMultiConfig(multiPath, config)
		return nil, err
	}

	// 测试成功，更新账号信息
	account.AccessToken = result.AccessToken
	account.ProfileArn = result.ProfileArn
	if result.ExpiresAt != "" {
		if t, parseErr := time.Parse(time.RFC3339Nano, result.ExpiresAt); parseErr == nil {
			account.ExpiresAt = t
		}
	}
	// refreshToken 变更时，仅当 machineId 为空才重新计算
	if result.RefreshToken != "" && result.RefreshToken != account.RefreshToken {
		account.RefreshToken = result.RefreshToken
		if strings.TrimSpace(account.MachineId) == "" {
			account.MachineId = kiroCore.ComputeMachineID(result.RefreshToken)
		}
	}
	account.Status = kiroShared.KiroStatusValid
	account.UpdatedAt = time.Now()

	config.UpdateAccount(account)
	multiPath := a.getKiroMultiConfigPath()
	if saveErr := kiroShared.SaveKiroMultiConfig(multiPath, config); saveErr != nil {
		fmt.Printf("warning: failed to save account after test: %v\n", saveErr)
	}

	return result, nil
}

// GetKiroAccountUsage 获取指定账号的用量信息（参数为 refreshToken）
func (a *App) GetKiroAccountUsage(refreshToken string) (*KiroUsageResult, error) {
	if strings.TrimSpace(refreshToken) == "" {
		return nil, fmt.Errorf("refreshToken is required")
	}

	config, err := a.loadOrCreateMultiConfig()
	if err != nil {
		return nil, fmt.Errorf("failed to load kiro config: %w", err)
	}

	account := config.FindAccountByRefreshToken(refreshToken)
	if account == nil {
		return nil, fmt.Errorf("account not found")
	}

	if strings.TrimSpace(account.AccessToken) == "" {
		return nil, fmt.Errorf("account has no accessToken, please test first")
	}

	// 使用现有的用量查询逻辑
	effectiveProxyURL := strings.TrimSpace(account.ProxyUrl)
	if effectiveProxyURL == "" {
		effectiveProxyURL = strings.TrimSpace(config.ProxyURL)
	}
	effectiveUserAgent := strings.TrimSpace(account.UserAgent)
	if effectiveUserAgent == "" {
		effectiveUserAgent = strings.TrimSpace(config.UserAgent)
	}
	effectiveVersion := strings.TrimSpace(account.Version)
	if effectiveVersion == "" {
		effectiveVersion = strings.TrimSpace(config.Version)
	}

	input := &KiroUsageInput{
		AccessToken:  account.AccessToken,
		RefreshToken: account.RefreshToken,
		MachineID:    account.MachineId,
		ProfileArn:   account.ProfileArn,
		Region:       account.Region,
		ProxyURL:     effectiveProxyURL,
		UserAgent:    effectiveUserAgent,
		Version:      effectiveVersion,
	}

	result, err := a.GetKiroUsage(input)
	if err != nil {
		// 检查是否为封禁错误
		var httpErr *kiroCore.UsageHTTPError
		if errors.As(err, &httpErr) && httpErr.StatusCode == 403 {
			// 解析错误体，检查是否为 TEMPORARILY_SUSPENDED
			var errBody struct {
				Reason string `json:"reason"`
			}
			if parseErr := json.Unmarshal([]byte(httpErr.Body), &errBody); parseErr == nil {
				if errBody.Reason == "TEMPORARILY_SUSPENDED" {
					account.Status = kiroShared.KiroStatusBanned
					account.LastUsageCheck = time.Now()
					config.UpdateAccount(account)
					multiPath := a.getKiroMultiConfigPath()
					_ = kiroShared.SaveKiroMultiConfig(multiPath, config)
				}
			}
		}
		return nil, err
	}

	// 更新账号用量信息
	account.SubscriptionTitle = result.SubscriptionTitle
	account.UsageLimit = result.UsageLimit
	account.CurrentUsage = result.CurrentUsage
	account.Balance = result.Balance
	account.UsagePct = result.UsagePct
	account.LastUsageCheck = time.Now()

	// 保存 UsageBreakdownList
	if result.Details != nil && len(result.Details.UsageBreakdownList) > 0 {
		if breakdownJSON, marshalErr := json.Marshal(result.Details.UsageBreakdownList); marshalErr == nil {
			account.UsageBreakdownList = breakdownJSON
		}
	}
	if result.Details != nil {
		if result.Details.DaysUntilReset != nil {
			account.DaysUntilReset = result.Details.DaysUntilReset
		}
		if result.Details.NextDateReset != nil {
			account.NextDateReset = result.Details.NextDateReset
		}
		if result.Details.UserInfo != nil {
			if result.Details.UserInfo.Email != nil {
				if v := strings.TrimSpace(*result.Details.UserInfo.Email); v != "" {
					account.Email = v
				}
			}
			if result.Details.UserInfo.UserID != nil {
				if v := strings.TrimSpace(*result.Details.UserInfo.UserID); v != "" {
					account.UserID = v
				}
			}
		}
	}

	// 根据用量更新状态
	if result.Balance <= 0 {
		account.Status = kiroShared.KiroStatusExhausted
	} else {
		account.Status = kiroShared.KiroStatusValid
	}

	config.UpdateAccount(account)
	multiPath := a.getKiroMultiConfigPath()
	if saveErr := kiroShared.SaveKiroMultiConfig(multiPath, config); saveErr != nil {
		fmt.Printf("warning: failed to save account usage: %v\n", saveErr)
	}

	return result, nil
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

	// 验证至少有一个有效账号
	if len(dto.Accounts) == 0 {
		return fmt.Errorf("no accounts in backup")
	}

	hasValidAccount := false
	for _, acc := range dto.Accounts {
		if strings.TrimSpace(acc.RefreshToken) != "" {
			hasValidAccount = true
			break
		}
	}
	if !hasValidAccount {
		return fmt.Errorf("no valid account found in backup")
	}

	multiPath := a.getKiroMultiConfigPath()

	if dto.ReplaceMode {
		// 替换模式：直接覆盖
		return a.replaceKiroMultiConfig(multiPath, dto)
	}

	// 合并模式：合并账号
	return a.mergeKiroMultiConfig(multiPath, dto)
}

// replaceKiroMultiConfig 替换模式：清空现有账号，使用备份的账号
func (a *App) replaceKiroMultiConfig(multiPath string, dto *KiroMultiConfigDTO) error {
	// 转换 DTO 为内部结构
	accounts := make([]kiroShared.KiroAccount, 0, len(dto.Accounts))
	for i := range dto.Accounts {
		accounts = append(accounts, *dtoToAccount(&dto.Accounts[i]))
	}

	// 验证 activeRefreshToken 是否存在于账号列表中
	activeRefreshToken := strings.TrimSpace(dto.ActiveRefreshToken)
	if activeRefreshToken != "" {
		found := false
		for _, acc := range accounts {
			if acc.RefreshToken == activeRefreshToken {
				found = true
				break
			}
		}
		if !found && len(accounts) > 0 {
			// 如果激活账号不存在，使用第一个账号
			activeRefreshToken = accounts[0].RefreshToken
		}
	} else if len(accounts) > 0 {
		// 如果没有指定激活账号，使用第一个
		activeRefreshToken = accounts[0].RefreshToken
	}

	newConfig := &kiroShared.KiroMultiConfig{
		ActiveRefreshToken: activeRefreshToken,
		Region:             dto.Region,
		ProxyURL:           dto.ProxyURL,
		UserAgent:          dto.UserAgent,
		Version:            dto.Version,
		BufferedStream:     dto.BufferedStream,
		RotationMode:       dto.RotationMode,
		ModelMapping:       dto.ModelMapping,
		Accounts:           accounts,
	}

	if err := kiroShared.SaveKiroMultiConfig(multiPath, newConfig); err != nil {
		return fmt.Errorf("failed to save kiro multi config: %w", err)
	}

	return nil
}

// mergeKiroMultiConfig 合并模式：按 refreshToken 去重，远程账号优先
func (a *App) mergeKiroMultiConfig(multiPath string, dto *KiroMultiConfigDTO) error {
	// 加载现有配置
	localConfig, err := a.loadOrCreateMultiConfig()
	if err != nil {
		return fmt.Errorf("failed to load local config: %w", err)
	}

	// 构建本地账号映射（refreshToken -> account）
	localAccountMap := make(map[string]*kiroShared.KiroAccount)
	for i := range localConfig.Accounts {
		rt := strings.TrimSpace(localConfig.Accounts[i].RefreshToken)
		if rt != "" {
			localAccountMap[rt] = &localConfig.Accounts[i]
		}
	}

	// 合并远程账号（远程优先）
	mergedAccounts := make([]kiroShared.KiroAccount, 0, len(dto.Accounts))
	remoteRefreshTokens := make(map[string]bool)

	for i := range dto.Accounts {
		remoteAccount := dtoToAccount(&dto.Accounts[i])
		rt := strings.TrimSpace(remoteAccount.RefreshToken)
		if rt == "" {
			continue
		}

		remoteRefreshTokens[rt] = true

		if localAccount, exists := localAccountMap[rt]; exists {
			// 账号已存在：合并字段（远程优先，但保留本地的用量信息）
			mergedAccount := *remoteAccount

			// 保留本地的用量信息（如果远程没有）
			if remoteAccount.CurrentUsage == 0 && localAccount.CurrentUsage > 0 {
				mergedAccount.CurrentUsage = localAccount.CurrentUsage
				mergedAccount.UsageLimit = localAccount.UsageLimit
				mergedAccount.Balance = localAccount.Balance
				mergedAccount.UsagePct = localAccount.UsagePct
				mergedAccount.LastUsageCheck = localAccount.LastUsageCheck
				mergedAccount.UsageBreakdownList = localAccount.UsageBreakdownList
			}

			// 保留本地的状态（如果远程是 unknown）
			if remoteAccount.Status == kiroShared.KiroStatusUnknown && localAccount.Status != kiroShared.KiroStatusUnknown {
				mergedAccount.Status = localAccount.Status
			}

			// 保留本地的创建时间
			if remoteAccount.CreatedAt.IsZero() && !localAccount.CreatedAt.IsZero() {
				mergedAccount.CreatedAt = localAccount.CreatedAt
			}

			mergedAccounts = append(mergedAccounts, mergedAccount)
		} else {
			// 新账号：直接添加
			mergedAccounts = append(mergedAccounts, *remoteAccount)
		}
	}

	// 添加本地独有的账号（远程没有的）
	for rt, localAccount := range localAccountMap {
		if !remoteRefreshTokens[rt] {
			mergedAccounts = append(mergedAccounts, *localAccount)
		}
	}

	// 确定激活账号
	activeRefreshToken := strings.TrimSpace(dto.ActiveRefreshToken)
	if activeRefreshToken == "" {
		activeRefreshToken = localConfig.ActiveRefreshToken
	}

	// 验证激活账号是否存在
	if activeRefreshToken != "" {
		found := false
		for _, acc := range mergedAccounts {
			if acc.RefreshToken == activeRefreshToken {
				found = true
				break
			}
		}
		if !found && len(mergedAccounts) > 0 {
			// 激活账号不存在，使用第一个
			activeRefreshToken = mergedAccounts[0].RefreshToken
		}
	} else if len(mergedAccounts) > 0 {
		// 没有激活账号，使用第一个
		activeRefreshToken = mergedAccounts[0].RefreshToken
	}

	// 合并全局配置（远程优先，本地兜底）
	region := dto.Region
	if region == "" {
		region = localConfig.Region
	}
	proxyURL := dto.ProxyURL
	if proxyURL == "" {
		proxyURL = localConfig.ProxyURL
	}
	userAgent := dto.UserAgent
	if userAgent == "" {
		userAgent = localConfig.UserAgent
	}
	version := dto.Version
	if version == "" {
		version = localConfig.Version
	}
	bufferedStream := dto.BufferedStream || localConfig.BufferedStream
	rotationMode := dto.RotationMode
	if rotationMode == "" {
		rotationMode = localConfig.RotationMode
	}
	modelMapping := dto.ModelMapping
	if len(modelMapping) == 0 {
		modelMapping = localConfig.ModelMapping
	}

	mergedConfig := &kiroShared.KiroMultiConfig{
		ActiveRefreshToken: activeRefreshToken,
		Region:             region,
		ProxyURL:           proxyURL,
		UserAgent:          userAgent,
		Version:            version,
		BufferedStream:     bufferedStream,
		RotationMode:       rotationMode,
		ModelMapping:       modelMapping,
		Accounts:           mergedAccounts,
	}

	if err := kiroShared.SaveKiroMultiConfig(multiPath, mergedConfig); err != nil {
		return fmt.Errorf("failed to save merged config: %w", err)
	}

	return nil
}
