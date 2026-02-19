package codexplugin

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	codex "clisimplehub/internal/codex"
	codexAuth "clisimplehub/internal/codex/auth"
	codexShared "clisimplehub/internal/codex/shared"
	"clisimplehub/internal/plugin"
)

type desktopFacade struct{}

var _ plugin.CodexDesktopProvider = (*desktopFacade)(nil)

func (d *desktopFacade) DefaultMultiConfigBasename() string {
	return codexShared.GetDefaultCodexMultiConfigPath()
}

func (d *desktopFacade) GetCodexGlobalConfig(configPath string) (json.RawMessage, error) {
	mc, err := codexShared.LoadCodexMultiConfig(configPath)
	if err != nil {
		if !os.IsNotExist(err) {
			return nil, err
		}
		mc = &codexShared.CodexMultiConfig{}
	}
	dto := map[string]any{
		"rotationMode":  mc.GetRotationMode(),
		"proxyUrl":      mc.ProxyUrl,
		"baseURL":       mc.GetBaseURL(),
		"clientVersion": mc.GetClientVersion(),
		"userAgent":     mc.GetUserAgent(),
		"originator":    mc.GetOriginator(),
	}
	return json.Marshal(dto)
}

func (d *desktopFacade) SaveCodexGlobalConfig(configPath string, dtoJSON json.RawMessage) error {
	mc, err := codexShared.LoadCodexMultiConfig(configPath)
	if err != nil {
		if !os.IsNotExist(err) {
			return err
		}
		mc = &codexShared.CodexMultiConfig{}
	}
	var dto struct {
		RotationMode  string `json:"rotationMode"`
		ProxyUrl      string `json:"proxyUrl"`
		BaseURL       string `json:"baseURL"`
		ClientVersion string `json:"clientVersion"`
		UserAgent     string `json:"userAgent"`
		Originator    string `json:"originator"`
	}
	if err := json.Unmarshal(dtoJSON, &dto); err != nil {
		return err
	}

	// Normalize and validate fields
	mc.RotationMode = strings.TrimSpace(dto.RotationMode)
	mc.ProxyUrl = strings.TrimSpace(dto.ProxyUrl)
	mc.Config.BaseURL = strings.TrimSpace(dto.BaseURL)
	mc.Config.ClientVersion = strings.TrimSpace(dto.ClientVersion)
	mc.Config.UserAgent = strings.TrimSpace(dto.UserAgent)
	mc.Config.Originator = strings.TrimSpace(dto.Originator)

	if err := codexShared.SaveCodexMultiConfig(configPath, mc); err != nil {
		return err
	}
	if pool := codex.GetPool(); pool != nil {
		pool.Reload()
	}
	return nil
}

func (d *desktopFacade) GetAccounts(configPath string) (json.RawMessage, error) {
	mc, err := codexShared.LoadCodexMultiConfig(configPath)
	if err != nil {
		empty := map[string]any{
			"activeRefreshToken": "",
			"accounts":          []any{},
		}
		return json.Marshal(empty)
	}
	return codexShared.MarshalAccountsResponse(mc)
}

func (d *desktopFacade) GetActiveAccount(configPath string) (json.RawMessage, error) {
	mc, err := codexShared.LoadCodexMultiConfig(configPath)
	if err != nil {
		return json.RawMessage("null"), nil
	}
	active := mc.GetActiveAccount()
	if active == nil {
		return json.RawMessage("null"), nil
	}
	isActive := active.RefreshToken == mc.ActiveRefreshToken
	return json.Marshal(codexShared.MarshalAccountForFrontend(active, isActive))
}

func (d *desktopFacade) SetActiveAccount(configPath, refreshToken string) error {
	mc, err := codexShared.LoadCodexMultiConfig(configPath)
	if err != nil {
		return err
	}
	if mc.FindAccountByRefreshToken(refreshToken) == nil {
		return fmt.Errorf("account not found")
	}
	mc.ActiveRefreshToken = refreshToken
	if err := codexShared.SaveCodexMultiConfig(configPath, mc); err != nil {
		return err
	}
	if pool := codex.GetPool(); pool != nil {
		pool.Reload()
	}
	return nil
}

func (d *desktopFacade) AddAccount(configPath string, dtoJSON json.RawMessage) (json.RawMessage, error) {
	var dto struct {
		RefreshToken string `json:"refreshToken"`
		Email        string `json:"email"`
		PlanType     string `json:"planType"`
		AccountID    string `json:"accountId"`
		AccessToken  string `json:"accessToken"`
		IDToken      string `json:"idToken"`
		ExpiresAt    string `json:"expiresAt"`
		ProxyUrl     string `json:"proxyUrl"`
		Weight       int    `json:"weight"`
	}
	if err := json.Unmarshal(dtoJSON, &dto); err != nil {
		return nil, err
	}
	if strings.TrimSpace(dto.RefreshToken) == "" {
		return nil, fmt.Errorf("refreshToken is required")
	}

	mc, err := codexShared.LoadCodexMultiConfig(configPath)
	if err != nil {
		if !os.IsNotExist(err) {
			return nil, err
		}
		mc = &codexShared.CodexMultiConfig{}
	}

	// Check duplicate
	if mc.FindAccountByRefreshToken(dto.RefreshToken) != nil {
		return nil, fmt.Errorf("account already exists")
	}

	now := time.Now()
	account := codexShared.CodexAccount{
		RefreshToken: dto.RefreshToken,
		AccessToken:  dto.AccessToken,
		IDToken:      dto.IDToken,
		AccountID:    dto.AccountID,
		Email:        dto.Email,
		PlanType:     dto.PlanType,
		ProxyUrl:     dto.ProxyUrl,
		Weight:       dto.Weight,
		Status:       codexShared.CodexStatusValid,
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	if dto.ExpiresAt != "" {
		if t, err := time.Parse(time.RFC3339, dto.ExpiresAt); err == nil {
			account.ExpiresAt = t
		}
	}

	mc.Accounts = append(mc.Accounts, account)
	if mc.ActiveRefreshToken == "" {
		mc.ActiveRefreshToken = account.RefreshToken
	}

	if err := codexShared.SaveCodexMultiConfig(configPath, mc); err != nil {
		return nil, err
	}
	if pool := codex.GetPool(); pool != nil {
		pool.Reload()
	}

	isActive := account.RefreshToken == mc.ActiveRefreshToken
	return json.Marshal(codexShared.MarshalAccountForFrontend(&account, isActive))
}

func (d *desktopFacade) UpdateAccount(configPath string, dtoJSON json.RawMessage) error {
	var dto struct {
		RefreshToken string `json:"refreshToken"`
		Email        string `json:"email"`
		PlanType     string `json:"planType"`
		ProxyUrl     string `json:"proxyUrl"`
		Weight       int    `json:"weight"`
		Status       string `json:"status"`
	}
	if err := json.Unmarshal(dtoJSON, &dto); err != nil {
		return err
	}

	mc, err := codexShared.LoadCodexMultiConfig(configPath)
	if err != nil {
		return err
	}
	account := mc.FindAccountByRefreshToken(dto.RefreshToken)
	if account == nil {
		return fmt.Errorf("account not found")
	}

	if dto.Email != "" {
		account.Email = dto.Email
	}
	if dto.PlanType != "" {
		account.PlanType = dto.PlanType
	}
	account.ProxyUrl = dto.ProxyUrl
	account.Weight = dto.Weight
	if dto.Status != "" {
		account.Status = codexShared.CodexAccountStatus(dto.Status)
	}
	account.UpdatedAt = time.Now()

	if err := codexShared.SaveCodexMultiConfig(configPath, mc); err != nil {
		return err
	}
	if pool := codex.GetPool(); pool != nil {
		pool.Reload()
	}
	return nil
}

func (d *desktopFacade) DeleteAccount(configPath, refreshToken string) error {
	mc, err := codexShared.LoadCodexMultiConfig(configPath)
	if err != nil {
		return err
	}
	if !mc.DeleteAccount(refreshToken) {
		return fmt.Errorf("account not found")
	}
	if err := codexShared.SaveCodexMultiConfig(configPath, mc); err != nil {
		return err
	}
	if pool := codex.GetPool(); pool != nil {
		pool.Reload()
	}
	return nil
}

func (d *desktopFacade) StartLogin(ctx context.Context, proxyURL string) (json.RawMessage, error) {
	result, err := codexAuth.StartCodexLogin(ctx, proxyURL)
	if err != nil {
		return nil, err
	}
	return json.Marshal(result)
}

var (
	loginWaitFn   func() (*codexAuth.CodexLoginResult, error)
	loginCleanup  func()
	loginMu       sync.Mutex
)

func (d *desktopFacade) StartLoginWithURL(ctx context.Context, proxyURL string) (authURL string, err error) {
	loginMu.Lock()
	defer loginMu.Unlock()

	// Cleanup previous session if exists
	if loginCleanup != nil {
		loginCleanup()
		loginCleanup = nil
		loginWaitFn = nil
	}

	authURL, waitFn, cleanup, err := codexAuth.StartCodexLoginWithURL(ctx, proxyURL)
	if err != nil {
		return "", err
	}

	loginWaitFn = waitFn
	loginCleanup = cleanup
	return authURL, nil
}

func (d *desktopFacade) WaitForLoginCallback(ctx context.Context) (json.RawMessage, error) {
	loginMu.Lock()
	waitFn := loginWaitFn
	cleanup := loginCleanup
	loginMu.Unlock()

	if waitFn == nil {
		return nil, fmt.Errorf("no login session active")
	}

	defer func() {
		loginMu.Lock()
		if cleanup != nil {
			cleanup()
		}
		loginWaitFn = nil
		loginCleanup = nil
		loginMu.Unlock()
	}()

	result, err := waitFn()
	if err != nil {
		return nil, err
	}
	return json.Marshal(result)
}

func (d *desktopFacade) TestAccount(configPath, refreshToken string) (json.RawMessage, error) {
	mc, err := codexShared.LoadCodexMultiConfig(configPath)
	if err != nil {
		return nil, err
	}
	account := mc.FindAccountByRefreshToken(refreshToken)
	if account == nil {
		return nil, fmt.Errorf("account not found")
	}

	proxyURL := strings.TrimSpace(account.ProxyUrl)
	if proxyURL == "" {
		proxyURL = strings.TrimSpace(mc.ProxyUrl)
	}
	if gp := plugin.GetGlobalProxyProviderCached(); gp != nil {
		if gpURL := gp.GetGlobalProxyURL(); gpURL != "" {
			proxyURL = gpURL
		}
	}

	accessToken, idToken, accountID, email, planType, expiresAt, err := codexAuth.RefreshAndTest(refreshToken, proxyURL, configPath)
	if err != nil {
		return nil, fmt.Errorf("test failed: %w", err)
	}

	// Update account
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
	account.Status = codexShared.CodexStatusValid
	account.CooldownUntil = time.Time{}
	account.CooldownReason = ""
	account.UpdatedAt = time.Now()

	_ = codexShared.SaveCodexMultiConfig(configPath, mc)
	if pool := codex.GetPool(); pool != nil {
		pool.Reload()
	}

	result := map[string]any{
		"accessToken": accessToken,
		"accountId":   accountID,
		"email":       email,
		"planType":    planType,
		"expiresAt":   expiresAt.Format(time.RFC3339),
	}
	return json.Marshal(result)
}
