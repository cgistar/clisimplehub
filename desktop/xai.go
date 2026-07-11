package main

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"clisimplehub/internal/plugin"

	"github.com/wailsapp/wails/v2/pkg/runtime"
)

func xaiProvider() plugin.XaiDesktopProvider {
	return plugin.GetXaiDesktopProviderCached()
}

func (a *App) IsXaiAccountsAvailable() bool {
	return xaiProvider() != nil
}

func (a *App) getXaiMultiConfigPath() string {
	xp := xaiProvider()
	if xp == nil {
		return ""
	}
	defaultPath := xp.DefaultMultiConfigBasename()
	if a != nil && a.configLoader != nil {
		if p := strings.TrimSpace(a.configLoader.GetPath()); p != "" {
			return filepath.Join(filepath.Dir(p), filepath.Base(defaultPath))
		}
	}
	return defaultPath
}

func (a *App) resolveXaiLoginProxy() string {
	xp := xaiProvider()
	if xp == nil {
		return ""
	}
	raw, err := xp.GetXaiGlobalConfig(a.getXaiMultiConfigPath())
	if err != nil {
		return ""
	}
	var cfg struct {
		ProxyUrl string `json:"proxyUrl"`
	}
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return ""
	}
	return strings.TrimSpace(cfg.ProxyUrl)
}

type XaiAccountDTO struct {
	ID                string `json:"id,omitempty"`
	Email             string `json:"email,omitempty"`
	Subject           string `json:"subject,omitempty"`
	AccessToken       string `json:"accessToken,omitempty"`
	RefreshToken      string `json:"refreshToken,omitempty"`
	IDToken           string `json:"idToken,omitempty"`
	AuthKind          string `json:"authKind,omitempty"`
	APIKey            string `json:"apiKey,omitempty"`
	BaseURL           string `json:"baseURL,omitempty"`
	TokenEndpoint     string `json:"tokenEndpoint,omitempty"`
	RedirectURI       string `json:"redirectURI,omitempty"`
	Enabled           bool   `json:"enabled"`
	Websockets        bool   `json:"websockets,omitempty"`
	Status            string `json:"status"`
	Weight            int    `json:"weight,omitempty"`
	ProxyUrl          string `json:"proxyUrl,omitempty"`
	ExpiresAt         string `json:"expiresAt,omitempty"`
	LastRefresh       string `json:"lastRefresh,omitempty"`
	CooldownUntil      string `json:"cooldownUntil,omitempty"`
	CooldownReason     string `json:"cooldownReason,omitempty"`
	CooldownRemaining  int    `json:"cooldownRemaining,omitempty"`
	CreatedAt         string `json:"createdAt,omitempty"`
	UpdatedAt         string `json:"updatedAt,omitempty"`
	IsActive          bool   `json:"isActive"`
}

type XaiAccountsResponse struct {
	ActiveAccountID string         `json:"activeAccountId"`
	Accounts        []XaiAccountDTO `json:"accounts"`
}

type XaiAccountsPageResponse struct {
	ActiveAccountID string         `json:"activeAccountId"`
	Accounts        []XaiAccountDTO `json:"accounts"`
	Offset          int            `json:"offset"`
	Limit           int            `json:"limit"`
	NextOffset      int            `json:"nextOffset"`
	Total           int            `json:"total"`
	HasMore         bool           `json:"hasMore"`
}

type XaiGlobalConfigDTO struct {
	RotationMode  string            `json:"rotationMode"`
	ProxyUrl      string            `json:"proxyUrl"`
	BaseURL       string            `json:"baseURL"`
	CustomHeaders map[string]string `json:"customHeaders,omitempty"`
}

type XaiLoginResultDTO struct {
	AccessToken   string `json:"accessToken"`
	RefreshToken  string `json:"refreshToken"`
	IDToken       string `json:"idToken,omitempty"`
	Email         string `json:"email,omitempty"`
	Subject       string `json:"subject,omitempty"`
	ExpiresAt     string `json:"expiresAt,omitempty"`
	BaseURL       string `json:"baseURL,omitempty"`
	RedirectURI   string `json:"redirectURI,omitempty"`
	TokenEndpoint string `json:"tokenEndpoint,omitempty"`
	LastRefresh   string `json:"lastRefresh,omitempty"`
}

type XaiTestResult struct {
	Success bool           `json:"success"`
	Account *XaiAccountDTO `json:"account,omitempty"`
	Error   string         `json:"error,omitempty"`
}

func (a *App) GetXaiAccounts() (*XaiAccountsResponse, error) {
	xp := xaiProvider()
	if xp == nil {
		return nil, fmt.Errorf("xai plugin not available")
	}
	raw, err := xp.GetAccounts(a.getXaiMultiConfigPath())
	if err != nil {
		return nil, err
	}
	var resp XaiAccountsResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

func (a *App) GetXaiAccountsPage(offset int, limit int) (*XaiAccountsPageResponse, error) {
	xp := xaiProvider()
	if xp == nil {
		return nil, fmt.Errorf("xai plugin not available")
	}
	raw, err := xp.GetAccountsPage(a.getXaiMultiConfigPath(), offset, limit)
	if err != nil {
		return nil, err
	}
	var resp XaiAccountsPageResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

func (a *App) GetActiveXaiAccount() (*XaiAccountDTO, error) {
	xp := xaiProvider()
	if xp == nil {
		return nil, fmt.Errorf("xai plugin not available")
	}
	raw, err := xp.GetActiveAccount(a.getXaiMultiConfigPath())
	if err != nil {
		return nil, err
	}
	if string(raw) == "null" {
		return nil, nil
	}
	var dto XaiAccountDTO
	if err := json.Unmarshal(raw, &dto); err != nil {
		return nil, err
	}
	return &dto, nil
}

func (a *App) SetActiveXaiAccount(accountId string) error {
	xp := xaiProvider()
	if xp == nil {
		return fmt.Errorf("xai plugin not available")
	}
	return xp.SetActiveAccount(a.getXaiMultiConfigPath(), accountId)
}

func (a *App) AddXaiAccount(dto *XaiAccountDTO) (*XaiAccountDTO, error) {
	if dto == nil {
		return nil, fmt.Errorf("account data is required")
	}
	xp := xaiProvider()
	if xp == nil {
		return nil, fmt.Errorf("xai plugin not available")
	}
	dtoJSON, err := json.Marshal(dto)
	if err != nil {
		return nil, err
	}
	raw, err := xp.AddAccount(a.getXaiMultiConfigPath(), dtoJSON)
	if err != nil {
		return nil, err
	}
	var result XaiAccountDTO
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

func (a *App) UpdateXaiAccount(dto *XaiAccountDTO) error {
	if dto == nil {
		return fmt.Errorf("account data is required")
	}
	xp := xaiProvider()
	if xp == nil {
		return fmt.Errorf("xai plugin not available")
	}
	dtoJSON, err := json.Marshal(dto)
	if err != nil {
		return err
	}
	return xp.UpdateAccount(a.getXaiMultiConfigPath(), dtoJSON)
}

func (a *App) DeleteXaiAccount(accountId string) error {
	xp := xaiProvider()
	if xp == nil {
		return fmt.Errorf("xai plugin not available")
	}
	return xp.DeleteAccount(a.getXaiMultiConfigPath(), accountId)
}

func (a *App) DeleteXaiAccounts(accountIds []string) error {
	xp := xaiProvider()
	if xp == nil {
		return fmt.Errorf("xai plugin not available")
	}
	return xp.DeleteAccounts(a.getXaiMultiConfigPath(), accountIds)
}

func (a *App) StartXaiLoginWithURL() (string, error) {
	xp := xaiProvider()
	if xp == nil {
		return "", fmt.Errorf("xai plugin not available")
	}
	ctx := a.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	return xp.StartLoginWithURL(ctx, a.resolveXaiLoginProxy())
}

func (a *App) SubmitXaiLoginCallbackURL(callbackURL string) error {
	xp := xaiProvider()
	if xp == nil {
		return fmt.Errorf("xai plugin not available")
	}
	ctx := a.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	return xp.SubmitLoginCallbackURL(ctx, callbackURL)
}

func (a *App) WaitForXaiLoginCallback() (*XaiLoginResultDTO, error) {
	xp := xaiProvider()
	if xp == nil {
		return nil, fmt.Errorf("xai plugin not available")
	}
	ctx := a.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	raw, err := xp.WaitForLoginCallback(ctx)
	if err != nil {
		return nil, err
	}
	var result XaiLoginResultDTO
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

func (a *App) CancelXaiLogin() error {
	xp := xaiProvider()
	if xp == nil {
		return fmt.Errorf("xai plugin not available")
	}
	return xp.CancelLogin()
}

func (a *App) TestXaiAccount(accountId string) (*XaiTestResult, error) {
	xp := xaiProvider()
	if xp == nil {
		return nil, fmt.Errorf("xai plugin not available")
	}
	raw, err := xp.TestAccount(a.getXaiMultiConfigPath(), accountId)
	if err != nil {
		return &XaiTestResult{Success: false, Error: err.Error()}, nil
	}
	var payload struct {
		Success bool          `json:"success"`
		Account XaiAccountDTO `json:"account"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil, err
	}
	return &XaiTestResult{Success: payload.Success, Account: &payload.Account}, nil
}

func (a *App) RefreshXaiAccountToken(accountId string) (*XaiTestResult, error) {
	xp := xaiProvider()
	if xp == nil {
		return nil, fmt.Errorf("xai plugin not available")
	}
	ctx := a.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	raw, err := xp.RefreshAccountToken(ctx, a.getXaiMultiConfigPath(), accountId)
	if err != nil {
		return &XaiTestResult{Success: false, Error: err.Error()}, nil
	}
	var payload struct {
		Success bool          `json:"success"`
		Account XaiAccountDTO `json:"account"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil, err
	}
	return &XaiTestResult{Success: payload.Success, Account: &payload.Account}, nil
}

func (a *App) GetXaiGlobalConfig() (*XaiGlobalConfigDTO, error) {
	xp := xaiProvider()
	if xp == nil {
		return nil, fmt.Errorf("xai plugin not available")
	}
	raw, err := xp.GetXaiGlobalConfig(a.getXaiMultiConfigPath())
	if err != nil {
		return nil, err
	}
	var dto XaiGlobalConfigDTO
	if err := json.Unmarshal(raw, &dto); err != nil {
		return nil, err
	}
	return &dto, nil
}

func (a *App) SaveXaiGlobalConfig(dto *XaiGlobalConfigDTO) error {
	if dto == nil {
		return fmt.Errorf("config is required")
	}
	xp := xaiProvider()
	if xp == nil {
		return fmt.Errorf("xai plugin not available")
	}
	raw, err := json.Marshal(dto)
	if err != nil {
		return err
	}
	return xp.SaveXaiGlobalConfig(a.getXaiMultiConfigPath(), raw)
}

func (a *App) OpenXaiLoginURL(url string) error {
	url = strings.TrimSpace(url)
	if url == "" {
		return fmt.Errorf("url is required")
	}
	ctx := a.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	runtime.BrowserOpenURL(ctx, url)
	return nil
}
