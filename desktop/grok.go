package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"clisimplehub/internal/executor"
	grokAuth "clisimplehub/internal/transformer/grok"
	grokShared "clisimplehub/internal/transformer/grok/shared"
)

// =============================================================================
// Grok Account Management Methods
// =============================================================================

// GrokAccountDTO 前端账号数据传输对象
// ssoToken 作为主键
type GrokAccountDTO struct {
	SsoToken     string `json:"ssoToken"`
	Status       string `json:"status"`
	Tier         string `json:"tier,omitempty"`
	Quota        int    `json:"quota,omitempty"`
	CurrentUsage int    `json:"currentUsage,omitempty"`
	IsPlus       bool   `json:"isPlus,omitempty"`
	FailCount    int    `json:"failCount,omitempty"`
	ProxyUrl     string `json:"proxyUrl,omitempty"`
	Weight       int    `json:"weight,omitempty"`
	CreatedAt    string `json:"createdAt,omitempty"`
	UpdatedAt    string `json:"updatedAt,omitempty"`
	IsActive     bool   `json:"isActive"`
}

// GrokAccountsResponse 多账号列表响应
type GrokAccountsResponse struct {
	ActiveSsoToken string           `json:"activeSsoToken"`
	Accounts       []GrokAccountDTO `json:"accounts"`
}

// GrokGlobalConfigDTO Grok 全局配置传输对象
type GrokGlobalConfigDTO struct {
	RotationMode string `json:"rotationMode"`
	ProxyURL     string `json:"proxyUrl"`
}

func grokAccountToDTO(account *grokShared.GrokAccount, isActive bool) GrokAccountDTO {
	dto := GrokAccountDTO{
		SsoToken:     account.SsoToken,
		Status:       string(account.Status),
		Tier:         string(account.Tier),
		Quota:        account.Quota,
		CurrentUsage: account.CurrentUsage,
		IsPlus:       account.IsPlus,
		FailCount:    account.FailCount,
		ProxyUrl:     account.ProxyUrl,
		Weight:       account.Weight,
		IsActive:     isActive,
	}
	if !account.CreatedAt.IsZero() {
		dto.CreatedAt = account.CreatedAt.Format(time.RFC3339Nano)
	}
	if !account.UpdatedAt.IsZero() {
		dto.UpdatedAt = account.UpdatedAt.Format(time.RFC3339Nano)
	}
	return dto
}

func grokDTOToAccount(dto *GrokAccountDTO) *grokShared.GrokAccount {
	account := &grokShared.GrokAccount{
		SsoToken:     dto.SsoToken,
		Status:       grokShared.GrokAccountStatus(dto.Status),
		Tier:         grokShared.GrokTier(dto.Tier),
		Quota:        dto.Quota,
		CurrentUsage: dto.CurrentUsage,
		IsPlus:       dto.IsPlus,
		FailCount:    dto.FailCount,
		ProxyUrl:     dto.ProxyUrl,
		Weight:       dto.Weight,
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

func (a *App) getGrokMultiConfigPath() string {
	if a != nil && a.configLoader != nil {
		if p := strings.TrimSpace(a.configLoader.GetPath()); p != "" {
			return filepath.Join(filepath.Dir(p), filepath.Base(grokShared.GetDefaultGrokMultiConfigPath()))
		}
	}
	return grokShared.GetDefaultGrokMultiConfigPath()
}

func (a *App) loadOrCreateGrokMultiConfig() (*grokShared.GrokMultiConfig, error) {
	multiPath := a.getGrokMultiConfigPath()
	config, err := grokShared.LoadGrokMultiConfig(multiPath)
	if err != nil {
		if os.IsNotExist(err) {
			return &grokShared.GrokMultiConfig{
				Accounts: []grokShared.GrokAccount{},
			}, nil
		}
		return nil, err
	}
	return config, nil
}

// GetGrokAccounts 获取所有 Grok 账号
func (a *App) GetGrokAccounts() (*GrokAccountsResponse, error) {
	config, err := a.loadOrCreateGrokMultiConfig()
	if err != nil {
		return nil, fmt.Errorf("failed to load grok config: %w", err)
	}
	accounts := make([]GrokAccountDTO, 0, len(config.Accounts))
	for i := range config.Accounts {
		isActive := config.Accounts[i].SsoToken == config.ActiveSsoToken
		accounts = append(accounts, grokAccountToDTO(&config.Accounts[i], isActive))
	}
	return &GrokAccountsResponse{
		ActiveSsoToken: config.ActiveSsoToken,
		Accounts:       accounts,
	}, nil
}

// SetActiveGrokAccount 设置激活的 Grok 账号
func (a *App) SetActiveGrokAccount(ssoToken string) error {
	ssoToken = grokShared.NormalizeSsoToken(ssoToken)
	if ssoToken == "" {
		return fmt.Errorf("ssoToken is required")
	}
	config, err := a.loadOrCreateGrokMultiConfig()
	if err != nil {
		return fmt.Errorf("failed to load grok config: %w", err)
	}
	if config.FindAccountBySsoToken(ssoToken) == nil {
		return fmt.Errorf("account not found")
	}
	config.ActiveSsoToken = ssoToken
	if err := grokShared.SaveGrokMultiConfig(a.getGrokMultiConfigPath(), config); err != nil {
		return err
	}
	// 重新加载 pool
	a.reloadGrokPool()
	return nil
}

// AddGrokAccount 添加新的 Grok 账号
func (a *App) AddGrokAccount(dto *GrokAccountDTO) (*GrokAccountDTO, error) {
	if dto == nil {
		return nil, fmt.Errorf("account data is required")
	}
	ssoToken := grokShared.NormalizeSsoToken(dto.SsoToken)
	if ssoToken == "" {
		return nil, fmt.Errorf("ssoToken is required")
	}
	dto.SsoToken = ssoToken

	config, err := a.loadOrCreateGrokMultiConfig()
	if err != nil {
		return nil, fmt.Errorf("failed to load grok config: %w", err)
	}
	if config.FindAccountBySsoToken(ssoToken) != nil {
		return nil, fmt.Errorf("account with this ssoToken already exists")
	}

	now := time.Now()
	account := grokDTOToAccount(dto)
	account.CreatedAt = now
	account.UpdatedAt = now

	// 默认状态设为 valid，而不是 unknown
	if account.Status == "" {
		account.Status = grokShared.GrokStatusValid
	}
	if account.Quota == 0 {
		account.Quota = grokShared.DefaultQuota(account.Tier)
	}

	config.Accounts = append(config.Accounts, *account)
	if len(config.Accounts) == 1 {
		config.ActiveSsoToken = account.SsoToken
	}

	if err := grokShared.SaveGrokMultiConfig(a.getGrokMultiConfigPath(), config); err != nil {
		return nil, fmt.Errorf("failed to save grok config: %w", err)
	}

	// 重新加载或初始化 pool
	a.reloadGrokPool()

	// 添加后自动测试账号，更新真实状态和配额
	// 测试失败不影响添加操作，只是状态可能不准确
	go func() {
		if testErr := a.TestGrokAccount(account.SsoToken); testErr != nil {
			log.Printf("warning: failed to test newly added grok account: %v", testErr)
		}
	}()

	isActive := account.SsoToken == config.ActiveSsoToken
	result := grokAccountToDTO(account, isActive)
	return &result, nil
}

// UpdateGrokAccount 更新 Grok 账号
func (a *App) UpdateGrokAccount(dto *GrokAccountDTO) error {
	if dto == nil {
		return fmt.Errorf("account data is required")
	}
	if grokShared.NormalizeSsoToken(dto.SsoToken) == "" {
		return fmt.Errorf("ssoToken is required")
	}
	config, err := a.loadOrCreateGrokMultiConfig()
	if err != nil {
		return fmt.Errorf("failed to load grok config: %w", err)
	}
	existing := config.FindAccountBySsoToken(dto.SsoToken)
	if existing == nil {
		return fmt.Errorf("account not found")
	}

	account := grokDTOToAccount(dto)
	account.CreatedAt = existing.CreatedAt
	account.UpdatedAt = time.Now()
	if !config.UpdateAccount(account) {
		return fmt.Errorf("failed to update account")
	}
	if err := grokShared.SaveGrokMultiConfig(a.getGrokMultiConfigPath(), config); err != nil {
		return err
	}
	// 重新加载 pool
	a.reloadGrokPool()
	return nil
}

// DeleteGrokAccount 删除 Grok 账号
func (a *App) DeleteGrokAccount(ssoToken string) error {
	ssoToken = grokShared.NormalizeSsoToken(ssoToken)
	if ssoToken == "" {
		return fmt.Errorf("ssoToken is required")
	}
	config, err := a.loadOrCreateGrokMultiConfig()
	if err != nil {
		return fmt.Errorf("failed to load grok config: %w", err)
	}
	if !config.DeleteAccount(ssoToken) {
		return fmt.Errorf("account not found")
	}
	if err := grokShared.SaveGrokMultiConfig(a.getGrokMultiConfigPath(), config); err != nil {
		return err
	}
	// 重新加载 pool
	a.reloadGrokPool()
	return nil
}

// TestGrokAccount 测试 Grok SSO Token 是否有效（通过 rate-limits 接口）
func (a *App) TestGrokAccount(ssoToken string) error {
	ssoToken = grokShared.NormalizeSsoToken(ssoToken)
	if ssoToken == "" {
		return fmt.Errorf("ssoToken is required")
	}

	config, err := a.loadOrCreateGrokMultiConfig()
	if err != nil {
		return fmt.Errorf("failed to load grok config: %w", err)
	}

	account := config.FindAccountBySsoToken(ssoToken)

	proxyURL := ""
	if account != nil && account.ProxyUrl != "" {
		proxyURL = account.ProxyUrl
	} else if config.ProxyURL != "" {
		proxyURL = config.ProxyURL
	}

	baseCtx := a.ctx
	if baseCtx == nil {
		baseCtx = context.Background()
	}
	ctx, cancel := context.WithTimeout(baseCtx, 15*time.Second)
	defer cancel()

	client := executor.NewHTTPClientForcedProxyURL(proxyURL, 12*time.Second)

	payload := map[string]string{
		"requestKind": "DEFAULT",
		"modelName":   "grok-4-1-thinking-1129",
	}
	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed to marshal payload: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://grok.com/rest/rate-limits", bytes.NewReader(payloadBytes))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	applier := grokAuth.NewAuthApplier(&grokTestAuthSource{token: ssoToken, proxy: proxyURL})
	if err := applier.Apply(req); err != nil {
		return fmt.Errorf("failed to apply auth: %w", err)
	}

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("grok returned %d: %s", resp.StatusCode, string(body))
	}

	// 解析 rate-limits 响应，更新账号信息
	if account != nil {
		var rl rateLimitsResponse
		if err := json.Unmarshal(body, &rl); err == nil {
			updateAccountFromRateLimits(account, &rl)
		}
		account.Status = grokShared.GrokStatusValid
		account.FailCount = 0
		account.UpdatedAt = time.Now()
		config.UpdateAccount(account)
		if saveErr := grokShared.SaveGrokMultiConfig(a.getGrokMultiConfigPath(), config); saveErr != nil {
			log.Printf("warning: failed to save grok account after test: %v", saveErr)
		}
	}

	return nil
}

type grokTestAuthSource struct {
	token string
	proxy string
}

func (s *grokTestAuthSource) GetSsoToken() string  { return s.token }
func (s *grokTestAuthSource) GrokProxyURL() string  { return s.proxy }
func (s *grokTestAuthSource) GetSettings() *grokShared.GrokSettings { return nil }

type rateLimitsResponse struct {
	RateLimits      []rateLimitEntry `json:"rateLimits"`
	RemainingTokens int              `json:"remainingTokens"`
}

type rateLimitEntry struct {
	WindowSizeSeconds int    `json:"windowSizeSeconds"`
	NumRemaining      int    `json:"numRemaining"`
	TotalLimit        int    `json:"totalLimit"`
	ModelName         string `json:"modelName,omitempty"`
}

func updateAccountFromRateLimits(account *grokShared.GrokAccount, rl *rateLimitsResponse) {
	if rl == nil {
		return
	}

	// 优先使用 remainingTokens 字段（新格式）
	if rl.RemainingTokens > 0 {
		// 假设默认配额（可以根据实际情况调整）
		if account.Quota == 0 {
			account.Quota = 1000 // 默认配额
		}
		account.CurrentUsage = account.Quota - rl.RemainingTokens
		return
	}

	// 回退到 rateLimits 数组（旧格式）
	if len(rl.RateLimits) == 0 {
		return
	}
	// 取第一个条目作为总体配额参考
	entry := rl.RateLimits[0]
	account.Quota = entry.TotalLimit
	account.CurrentUsage = entry.TotalLimit - entry.NumRemaining
	// 推断 tier
	if entry.TotalLimit >= 140 {
		account.Tier = grokShared.TierSuper
	} else {
		account.Tier = grokShared.TierBasic
	}
}

// GetGrokGlobalConfig 获取 Grok 全局配置
func (a *App) GetGrokGlobalConfig() (*GrokGlobalConfigDTO, error) {
	config, err := a.loadOrCreateGrokMultiConfig()
	if err != nil {
		return nil, fmt.Errorf("failed to load grok config: %w", err)
	}
	return &GrokGlobalConfigDTO{
		RotationMode: config.GetRotationMode(),
		ProxyURL:     config.ProxyURL,
	}, nil
}

// SaveGrokGlobalConfig 保存 Grok 全局配置
func (a *App) SaveGrokGlobalConfig(dto *GrokGlobalConfigDTO) error {
	if dto == nil {
		return fmt.Errorf("nil config")
	}
	config, err := a.loadOrCreateGrokMultiConfig()
	if err != nil {
		return fmt.Errorf("failed to load grok config: %w", err)
	}
	config.RotationMode = strings.TrimSpace(dto.RotationMode)
	config.ProxyURL = strings.TrimSpace(dto.ProxyURL)

	if err := grokShared.SaveGrokMultiConfig(a.getGrokMultiConfigPath(), config); err != nil {
		return fmt.Errorf("failed to save grok config: %w", err)
	}

	// 重新加载 pool
	pool := grokAuth.GetPool()
	if pool != nil {
		pool.Reload()
	}
	return nil
}

// ReviveAllGrokAccounts 将所有 Grok 账号状态改为 valid
func (a *App) ReviveAllGrokAccounts() error {
	config, err := a.loadOrCreateGrokMultiConfig()
	if err != nil {
		return fmt.Errorf("failed to load grok config: %w", err)
	}

	if len(config.Accounts) == 0 {
		return fmt.Errorf("no accounts to revive")
	}

	now := time.Now()
	for i := range config.Accounts {
		config.Accounts[i].Status = grokShared.GrokStatusValid
		config.Accounts[i].FailCount = 0
		config.Accounts[i].UpdatedAt = now
	}

	if err := grokShared.SaveGrokMultiConfig(a.getGrokMultiConfigPath(), config); err != nil {
		return fmt.Errorf("failed to save grok config: %w", err)
	}

	a.reloadGrokPool()
	return nil
}

// reloadGrokPool 重新加载或初始化 Grok 账号池
func (a *App) reloadGrokPool() {
	pool := grokAuth.GetPool()
	if pool != nil {
		// pool 已存在，重新加载配置
		pool.Reload()
	} else {
		// pool 不存在，尝试初始化
		grokJsonPath := a.getGrokMultiConfigPath()
		if err := grokAuth.InitPool(grokJsonPath); err != nil {
			log.Printf("Warning: failed to initialize grok pool: %v", err)
		}
	}
}
