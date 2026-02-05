package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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

func (a *App) getKiroAuthTokenPath() string {
	if a != nil && a.configLoader != nil {
		if p := strings.TrimSpace(a.configLoader.GetPath()); p != "" {
			return filepath.Join(filepath.Dir(p), filepath.Base(kiroShared.GetDefaultKiroCredentialsPath()))
		}
	}
	return kiroShared.GetDefaultKiroCredentialsPath()
}

// GetKiroAuthTokenJSON 返回 kiro-auth-token.json 的原始 JSON 内容用于备份
// 文件不存在时返回 (nil, nil)
func (a *App) GetKiroAuthTokenJSON() (map[string]interface{}, error) {
	credsPath := kiroShared.ExpandTilde(a.getKiroAuthTokenPath())
	data, err := os.ReadFile(credsPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("read kiro-auth-token.json: %w", err)
	}

	var token map[string]interface{}
	if err := json.Unmarshal(data, &token); err != nil {
		return nil, fmt.Errorf("parse kiro-auth-token.json: %w", err)
	}
	return token, nil
}

// SaveKiroAuthTokenJSON 从备份恢复 kiro-auth-token.json
// 采用原子写入 + 0600 权限
func (a *App) SaveKiroAuthTokenJSON(token map[string]interface{}) error {
	if len(token) == 0 {
		return nil
	}

	refreshToken, _ := token["refreshToken"].(string)
	if strings.TrimSpace(refreshToken) == "" {
		return fmt.Errorf("invalid kiro auth token: refreshToken is required")
	}

	credsPath := kiroShared.ExpandTilde(a.getKiroAuthTokenPath())
	data, err := json.MarshalIndent(token, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal kiro auth token: %w", err)
	}
	if err := writeFileAtomic0600(credsPath, data); err != nil {
		return fmt.Errorf("write kiro-auth-token.json: %w", err)
	}
	return nil
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

// GetKiroConfig retrieves Kiro configuration
func (a *App) GetKiroConfig() (*KiroConfig, error) {
	if a.storage == nil {
		return nil, fmt.Errorf("storage not initialized")
	}

	config := &KiroConfig{
		Region:     "us-east-1",
		AuthMethod: "social",
	}

	credsPath := a.getKiroAuthTokenPath()
	if creds, err := kiroShared.LoadKiroCredentials(credsPath); err == nil && creds != nil {
		config.RefreshToken = creds.RefreshToken
		config.ProfileArn = creds.ProfileArn
		config.AccessToken = creds.AccessToken
		config.AuthMethod = creds.AuthMethod
		config.Provider = creds.Provider
		config.ClientId = creds.ClientId
		config.ClientSecret = creds.ClientSecret
		if !creds.ExpiresAt.IsZero() {
			config.ExpiresAt = creds.ExpiresAt.Format(time.RFC3339Nano)
		}
		if strings.TrimSpace(creds.Region) != "" {
			config.Region = creds.Region
		}
		if strings.TrimSpace(config.AuthMethod) == "" {
			config.AuthMethod = "social"
		}
	}
	if proxy, err := a.storage.GetConfig("kiro.proxyUrl"); err == nil {
		config.ProxyURL = proxy
	}
	if ua, err := a.storage.GetConfig("kiro.userAgent"); err == nil {
		config.UserAgent = ua
	}
	if v, err := a.storage.GetConfig("kiro.version"); err == nil {
		config.Version = v
	}
	if bs, err := a.storage.GetConfig("kiro.bufferedStream"); err == nil {
		config.BufferedStream = bs == "true"
	}

	return config, nil
}

// SaveKiroConfig saves Kiro configuration
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

	// Persist to credentials file next to config.json (single source of truth for Kiro auth).
	credsPath := a.getKiroAuthTokenPath()
	var creds *kiroShared.KiroCredentials
	if existing, err := kiroShared.LoadKiroCredentials(credsPath); err == nil {
		creds = existing
	} else {
		creds = &kiroShared.KiroCredentials{}
	}

	oldRefreshToken := strings.TrimSpace(creds.RefreshToken)
	oldProfileArn := strings.TrimSpace(creds.ProfileArn)

	newRefreshToken := strings.TrimSpace(config.RefreshToken)
	newProfileArn := strings.TrimSpace(config.ProfileArn)
	newRegion := strings.TrimSpace(config.Region)

	refreshTokenChanged := oldRefreshToken != "" && newRefreshToken != "" && oldRefreshToken != newRefreshToken

	machineID := kiroCore.ComputeMachineID(newRefreshToken)
	if err := a.storage.SetConfig("kiro.machineId", machineID); err != nil {
		return fmt.Errorf("failed to save machine id: %w", err)
	}

	creds.RefreshToken = newRefreshToken
	creds.Region = newRegion
	if creds.Region == "" {
		creds.Region = "us-east-1"
	}
	creds.AuthMethod = config.AuthMethod
	creds.Provider = config.Provider
	creds.ClientId = config.ClientId
	creds.ClientSecret = config.ClientSecret

	accessToken := strings.TrimSpace(config.AccessToken)
	expiresAtStr := strings.TrimSpace(config.ExpiresAt)
	if accessToken != "" || expiresAtStr != "" {
		if accessToken == "" {
			return fmt.Errorf("accessToken is required when expiresAt is provided")
		}
		if expiresAtStr == "" {
			return fmt.Errorf("expiresAt is required when accessToken is provided")
		}
		expiresAt, err := time.Parse(time.RFC3339Nano, expiresAtStr)
		if err != nil {
			expiresAt, err = time.Parse(time.RFC3339, expiresAtStr)
		}
		if err != nil {
			return fmt.Errorf("invalid expiresAt: %w", err)
		}
		creds.AccessToken = accessToken
		creds.ExpiresAt = expiresAt
	}

	// If the refresh token was changed by config update, require fresh auth state from UI test.
	if refreshTokenChanged {
		if strings.TrimSpace(config.AccessToken) == "" {
			return fmt.Errorf("accessToken is required when refreshToken changes; please test first")
		}
		if strings.TrimSpace(config.ExpiresAt) == "" {
			return fmt.Errorf("expiresAt is required when refreshToken changes; please test first")
		}
		creds.ProfileArn = strings.TrimSpace(config.ProfileArn)
		creds.Provider = ""
	} else {
		// Keep/overwrite profileArn only if explicitly provided.
		if newProfileArn != "" && newProfileArn != oldProfileArn {
			creds.ProfileArn = newProfileArn
		}
	}

	// IdC tokens do not require (and typically do not return) a CodeWhisperer profileArn.
	// Keeping a stale profileArn (e.g. from a prior Social login) can cause confusing upstream auth errors.
	if authMethod == "idc" || authMethod == "builder-id" {
		creds.ProfileArn = ""
	}
	if err := kiroShared.SaveKiroCredentials(credsPath, creds); err != nil {
		return fmt.Errorf("failed to save kiro credentials: %w", err)
	}

	// 同步更新 kiro.json 多账号配置
	if err := a.syncToMultiAccountConfig(creds, config); err != nil {
		// 不阻止保存流程，只记录警告
		fmt.Printf("warning: failed to sync to kiro.json: %v\n", err)
	}

	if err := a.storage.SetConfig("kiro.proxyUrl", config.ProxyURL); err != nil {
		return fmt.Errorf("failed to save proxy URL: %w", err)
	}
	if err := a.storage.SetConfig("kiro.userAgent", config.UserAgent); err != nil {
		return fmt.Errorf("failed to save user agent: %w", err)
	}
	if err := a.storage.SetConfig("kiro.version", config.Version); err != nil {
		return fmt.Errorf("failed to save version: %w", err)
	}
	if err := a.storage.SetConfig("kiro.bufferedStream", fmt.Sprintf("%v", config.BufferedStream)); err != nil {
		return fmt.Errorf("failed to save buffered stream: %w", err)
	}

	return nil
}

// syncToMultiAccountConfig 同步更新 kiro.json 多账号配置
// 如果存在相同 refreshToken 的账号则更新，否则添加
func (a *App) syncToMultiAccountConfig(creds *kiroShared.KiroCredentials, config *KiroConfig) error {
	if creds == nil || config == nil {
		return nil
	}

	refreshToken := strings.TrimSpace(creds.RefreshToken)
	if refreshToken == "" {
		return nil
	}

	authMethod := strings.ToLower(strings.TrimSpace(creds.AuthMethod))
	profileArn := creds.ProfileArn
	provider := creds.Provider
	clientId := creds.ClientId
	clientSecret := creds.ClientSecret
	if authMethod == "social" {
		clientId = ""
		clientSecret = ""
	} else if authMethod == "idc" || authMethod == "builder-id" {
		profileArn = ""
	}

	// 加载或创建多账号配置
	multiConfig, err := a.loadOrCreateMultiConfig()
	if err != nil {
		return fmt.Errorf("load multi config: %w", err)
	}

	// 查找是否存在相同 refreshToken 的账号
	existingAccount := multiConfig.FindAccountByRefreshToken(refreshToken)

	if existingAccount != nil {
		// 更新现有账号
		existingAccount.AccessToken = creds.AccessToken
		existingAccount.ProfileArn = profileArn
		existingAccount.ExpiresAt = creds.ExpiresAt
		existingAccount.Region = creds.Region
		existingAccount.AuthMethod = creds.AuthMethod
		existingAccount.Provider = provider
		existingAccount.ClientId = clientId
		existingAccount.ClientSecret = clientSecret
		existingAccount.ProxyUrl = config.ProxyURL
		existingAccount.UserAgent = config.UserAgent
		existingAccount.Version = config.Version
		existingAccount.Status = kiroShared.KiroStatusValid
		existingAccount.UpdatedAt = time.Now()

		if !multiConfig.UpdateAccount(existingAccount) {
			return fmt.Errorf("failed to update account in config")
		}
	} else {
		// 添加新账号
		now := time.Now()
		newAccount := &kiroShared.KiroAccount{
			RefreshToken: refreshToken,
			AccessToken:  creds.AccessToken,
			ProfileArn:   profileArn,
			ExpiresAt:    creds.ExpiresAt,
			Region:       creds.Region,
			AuthMethod:   creds.AuthMethod,
			Provider:     provider,
			ClientId:     clientId,
			ClientSecret: clientSecret,
			MachineId:    kiroCore.ComputeMachineID(refreshToken),
			ProxyUrl:     config.ProxyURL,
			UserAgent:    config.UserAgent,
			Version:      config.Version,
			Status:       kiroShared.KiroStatusValid,
			CreatedAt:    now,
			UpdatedAt:    now,
		}

		if newAccount.Region == "" {
			newAccount.Region = "us-east-1"
		}

		multiConfig.Accounts = append(multiConfig.Accounts, *newAccount)
	}

	// 将当前保存的账号设为激活账号
	multiConfig.ActiveRefreshToken = refreshToken

	// 保存多账号配置
	multiPath := a.getKiroMultiConfigPath()
	if err := kiroShared.SaveKiroMultiConfig(multiPath, multiConfig); err != nil {
		return fmt.Errorf("save multi config: %w", err)
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
	if v := strings.TrimSpace(input.RefreshToken); v != "" {
		machineID = kiroCore.ComputeMachineID(v)
	} else if a != nil && a.storage != nil {
		if v, getErr := a.storage.GetConfig("kiro.machineId"); getErr == nil {
			machineID = strings.TrimSpace(v)
		}
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
	SubscriptionTitle  string          `json:"subscriptionTitle,omitempty"`
	UsageLimit         float64         `json:"usageLimit,omitempty"`
	CurrentUsage       float64         `json:"currentUsage,omitempty"`
	Balance            float64         `json:"balance,omitempty"`
	UsagePct           float64         `json:"usagePct,omitempty"`
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
		SubscriptionTitle:  account.SubscriptionTitle,
		UsageLimit:         account.UsageLimit,
		CurrentUsage:       account.CurrentUsage,
		Balance:            account.Balance,
		UsagePct:           account.UsagePct,
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
		SubscriptionTitle:  dto.SubscriptionTitle,
		UsageLimit:         dto.UsageLimit,
		CurrentUsage:       dto.CurrentUsage,
		Balance:            dto.Balance,
		UsagePct:           dto.UsagePct,
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

// loadOrCreateMultiConfig 加载或创建多账号配置，自动执行迁移
func (a *App) loadOrCreateMultiConfig() (*kiroShared.KiroMultiConfig, error) {
	multiPath := a.getKiroMultiConfigPath()
	oldPath := a.getKiroAuthTokenPath()

	// 尝试从单账号迁移
	migrated, err := kiroShared.MigrateFromSingleAccount(oldPath, multiPath, kiroCore.ComputeMachineID)
	if err != nil {
		// 迁移失败不阻止继续，只记录日志
		fmt.Printf("kiro migration warning: %v\n", err)
	}
	if migrated {
		fmt.Println("kiro: migrated from single account to multi-account config")
	}

	// 加载多账号配置
	config, err := kiroShared.LoadKiroMultiConfig(multiPath)
	if err != nil {
		if os.IsNotExist(err) {
			// 文件不存在，返回空配置
			return &kiroShared.KiroMultiConfig{
				Accounts: []kiroShared.KiroAccount{},
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

	// 同步到旧的单账号配置文件（兼容性）
	if account := config.GetActiveAccount(); account != nil {
		oldPath := a.getKiroAuthTokenPath()
		creds := account.ToCredentials()
		if err := kiroShared.SaveKiroCredentials(oldPath, creds); err != nil {
			fmt.Printf("warning: failed to sync to legacy config: %v\n", err)
		}

		// 更新 config.json 中的 machineId
		if a.storage != nil && account.MachineId != "" {
			_ = a.storage.SetConfig("kiro.machineId", account.MachineId)
		}
	}

	multiPath := a.getKiroMultiConfigPath()
	return kiroShared.SaveKiroMultiConfig(multiPath, config)
}

// AddKiroAccount 添加新的 Kiro 账号
func (a *App) AddKiroAccount(dto *KiroAccountDTO) (*KiroAccountDTO, error) {
	if dto == nil {
		return nil, fmt.Errorf("account data is required")
	}
	if strings.TrimSpace(dto.RefreshToken) == "" {
		return nil, fmt.Errorf("refreshToken is required")
	}

	config, err := a.loadOrCreateMultiConfig()
	if err != nil {
		return nil, fmt.Errorf("failed to load kiro config: %w", err)
	}

	// 检查是否已存在相同 refreshToken 的账号
	if config.FindAccountByRefreshToken(dto.RefreshToken) != nil {
		return nil, fmt.Errorf("account with this refreshToken already exists")
	}

	// 创建新账号
	now := time.Now()
	account := dtoToAccount(dto)
	account.MachineId = kiroCore.ComputeMachineID(account.RefreshToken)
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
	// 保留 machineId
	if account.MachineId == "" {
		account.MachineId = existing.MachineId
	}

	if !config.UpdateAccount(account) {
		return fmt.Errorf("failed to update account")
	}

	// 如果更新的是激活账号，同步到旧配置
	if dto.RefreshToken == config.ActiveRefreshToken {
		oldPath := a.getKiroAuthTokenPath()
		creds := account.ToCredentials()
		if err := kiroShared.SaveKiroCredentials(oldPath, creds); err != nil {
			fmt.Printf("warning: failed to sync to legacy config: %v\n", err)
		}
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

	// 如果删除后还有账号，同步新的激活账号到旧配置
	if account := config.GetActiveAccount(); account != nil {
		oldPath := a.getKiroAuthTokenPath()
		creds := account.ToCredentials()
		if err := kiroShared.SaveKiroCredentials(oldPath, creds); err != nil {
			fmt.Printf("warning: failed to sync to legacy config: %v\n", err)
		}
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
	// 注意：refreshToken 变更时需要重新计算 machineId
	if result.RefreshToken != "" && result.RefreshToken != account.RefreshToken {
		account.RefreshToken = result.RefreshToken
		account.MachineId = kiroCore.ComputeMachineID(result.RefreshToken)
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
	input := &KiroUsageInput{
		AccessToken:  account.AccessToken,
		RefreshToken: account.RefreshToken,
		ProfileArn:   account.ProfileArn,
		Region:       account.Region,
		ProxyURL:     account.ProxyUrl,
		UserAgent:    account.UserAgent,
		Version:      account.Version,
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
