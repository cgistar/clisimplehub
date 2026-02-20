package main

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"clisimplehub/internal/plugin"
)

func codexProvider() plugin.CodexDesktopProvider {
	return plugin.GetCodexDesktopProviderCached()
}

func (a *App) IsCodexAccountsAvailable() bool {
	return codexProvider() != nil
}

func (a *App) getCodexMultiConfigPath() string {
	cp := codexProvider()
	if cp == nil {
		return ""
	}
	defaultPath := cp.DefaultMultiConfigBasename()
	if a != nil && a.configLoader != nil {
		if p := strings.TrimSpace(a.configLoader.GetPath()); p != "" {
			return filepath.Join(filepath.Dir(p), filepath.Base(defaultPath))
		}
	}
	return defaultPath
}

// CodexAccountDTO represents a Codex account for frontend
type CodexAccountDTO struct {
	RefreshToken      string         `json:"refreshToken"`
	Email             string         `json:"email,omitempty"`
	PlanType          string         `json:"planType,omitempty"`
	AccessToken       string         `json:"accessToken,omitempty"`
	IDToken           string         `json:"idToken,omitempty"`
	AccountID         string         `json:"accountId,omitempty"`
	Status            string         `json:"status"`
	Weight            int            `json:"weight,omitempty"`
	ProxyUrl          string         `json:"proxyUrl,omitempty"`
	ExpiresAt         string         `json:"expiresAt,omitempty"`
	CooldownUntil     string         `json:"cooldownUntil,omitempty"`
	CooldownReason    string         `json:"cooldownReason,omitempty"`
	CooldownRemaining int            `json:"cooldownRemaining,omitempty"`
	CodexUsage        map[string]any `json:"codexUsage,omitempty"`
	CreatedAt         string         `json:"createdAt,omitempty"`
	UpdatedAt         string         `json:"updatedAt,omitempty"`
	IsActive          bool           `json:"isActive"`
}

type CodexAccountsResponse struct {
	ActiveRefreshToken string            `json:"activeRefreshToken"`
	ActiveAccountID    string            `json:"activeAccountId"`
	Accounts           []CodexAccountDTO `json:"accounts"`
}

type CodexGlobalConfigDTO struct {
	RotationMode  string `json:"rotationMode"`
	ProxyUrl      string `json:"proxyUrl"`
	BaseURL       string `json:"baseURL"`
	ClientVersion string `json:"clientVersion"`
	UserAgent     string `json:"userAgent"`
	Originator    string `json:"originator"`
}

type CodexTestResult struct {
	AccessToken string `json:"accessToken"`
	AccountID   string `json:"accountId,omitempty"`
	Email       string `json:"email,omitempty"`
	PlanType    string `json:"planType,omitempty"`
	ExpiresAt   string `json:"expiresAt"`
}

type CodexLoginResultDTO struct {
	RefreshToken string `json:"refreshToken"`
	AccessToken  string `json:"accessToken"`
	IDToken      string `json:"idToken,omitempty"`
	AccountID    string `json:"accountId,omitempty"`
	Email        string `json:"email,omitempty"`
	PlanType     string `json:"planType,omitempty"`
	ExpiresAt    string `json:"expiresAt,omitempty"`
}

func (a *App) GetCodexAccounts() (*CodexAccountsResponse, error) {
	cp := codexProvider()
	if cp == nil {
		return nil, fmt.Errorf("codex plugin not available")
	}
	raw, err := cp.GetAccounts(a.getCodexMultiConfigPath())
	if err != nil {
		return nil, err
	}
	var resp CodexAccountsResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

func (a *App) GetActiveCodexAccount() (*CodexAccountDTO, error) {
	cp := codexProvider()
	if cp == nil {
		return nil, fmt.Errorf("codex plugin not available")
	}
	raw, err := cp.GetActiveAccount(a.getCodexMultiConfigPath())
	if err != nil {
		return nil, err
	}
	if string(raw) == "null" {
		return nil, nil
	}
	var dto CodexAccountDTO
	if err := json.Unmarshal(raw, &dto); err != nil {
		return nil, err
	}
	return &dto, nil
}

func (a *App) SetActiveCodexAccount(accountId string) error {
	cp := codexProvider()
	if cp == nil {
		return fmt.Errorf("codex plugin not available")
	}
	return cp.SetActiveAccount(a.getCodexMultiConfigPath(), accountId)
}

func (a *App) AddCodexAccount(dto *CodexAccountDTO) (*CodexAccountDTO, error) {
	if dto == nil {
		return nil, fmt.Errorf("account data is required")
	}
	cp := codexProvider()
	if cp == nil {
		return nil, fmt.Errorf("codex plugin not available")
	}
	dtoJSON, err := json.Marshal(dto)
	if err != nil {
		return nil, err
	}
	raw, err := cp.AddAccount(a.getCodexMultiConfigPath(), dtoJSON)
	if err != nil {
		return nil, err
	}
	var result CodexAccountDTO
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

func (a *App) UpdateCodexAccount(dto *CodexAccountDTO) error {
	if dto == nil {
		return fmt.Errorf("account data is required")
	}
	cp := codexProvider()
	if cp == nil {
		return fmt.Errorf("codex plugin not available")
	}
	dtoJSON, err := json.Marshal(dto)
	if err != nil {
		return err
	}
	return cp.UpdateAccount(a.getCodexMultiConfigPath(), dtoJSON)
}

func (a *App) DeleteCodexAccount(accountId string) error {
	cp := codexProvider()
	if cp == nil {
		return fmt.Errorf("codex plugin not available")
	}
	return cp.DeleteAccount(a.getCodexMultiConfigPath(), accountId)
}

func (a *App) StartCodexLogin() (*CodexLoginResultDTO, error) {
	cp := codexProvider()
	if cp == nil {
		return nil, fmt.Errorf("codex plugin not available")
	}
	ctx := a.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	proxyURL := ""
	raw, err := cp.StartLogin(ctx, proxyURL)
	if err != nil {
		return nil, err
	}
	var result CodexLoginResultDTO
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

func (a *App) StartCodexLoginWithURL() (string, error) {
	cp := codexProvider()
	if cp == nil {
		return "", fmt.Errorf("codex plugin not available")
	}
	ctx := a.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	proxyURL := ""
	return cp.StartLoginWithURL(ctx, proxyURL)
}

func (a *App) WaitForCodexLoginCallback() (*CodexLoginResultDTO, error) {
	cp := codexProvider()
	if cp == nil {
		return nil, fmt.Errorf("codex plugin not available")
	}
	ctx := a.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	raw, err := cp.WaitForLoginCallback(ctx)
	if err != nil {
		return nil, err
	}
	var result CodexLoginResultDTO
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

func (a *App) TestCodexAccount(accountId string) (*CodexTestResult, error) {
	cp := codexProvider()
	if cp == nil {
		return nil, fmt.Errorf("codex plugin not available")
	}
	raw, err := cp.TestAccount(a.getCodexMultiConfigPath(), accountId)
	if err != nil {
		return nil, err
	}
	var result CodexTestResult
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

func (a *App) GetCodexGlobalConfig() (*CodexGlobalConfigDTO, error) {
	cp := codexProvider()
	if cp == nil {
		return nil, fmt.Errorf("codex plugin not available")
	}
	raw, err := cp.GetCodexGlobalConfig(a.getCodexMultiConfigPath())
	if err != nil {
		return nil, err
	}
	var dto CodexGlobalConfigDTO
	if err := json.Unmarshal(raw, &dto); err != nil {
		return nil, err
	}
	return &dto, nil
}

func (a *App) SaveCodexGlobalConfig(dto *CodexGlobalConfigDTO) error {
	if dto == nil {
		return fmt.Errorf("nil config")
	}
	cp := codexProvider()
	if cp == nil {
		return fmt.Errorf("codex plugin not available")
	}
	dtoJSON, err := json.Marshal(dto)
	if err != nil {
		return err
	}
	return cp.SaveCodexGlobalConfig(a.getCodexMultiConfigPath(), dtoJSON)
}

type CodexUsageResult struct {
	Primary   *CodexUsageWindow `json:"primary,omitempty"`
	Secondary *CodexUsageWindow `json:"secondary,omitempty"`
}

type CodexUsageWindow struct {
	UsedPercent      float64 `json:"usedPercent"`
	RemainingSeconds int     `json:"remainingSeconds"`
}

func (a *App) GetCodexAccountUsage(accountId string) (*CodexUsageResult, error) {
	cp := codexProvider()
	if cp == nil {
		return nil, fmt.Errorf("codex plugin not available")
	}
	ctx := a.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	raw, err := cp.GetAccountUsage(ctx, a.getCodexMultiConfigPath(), accountId)
	if err != nil {
		return nil, err
	}
	var result CodexUsageResult
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, err
	}
	return &result, nil
}
