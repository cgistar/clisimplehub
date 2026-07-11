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

type XaiQuotaWindowDTO struct {
	Remaining     int   `json:"remaining"`
	Total         int   `json:"total"`
	WindowSeconds int   `json:"windowSeconds,omitempty"`
	ResetAt       int64 `json:"resetAt,omitempty"`
	SyncedAt      int64 `json:"syncedAt,omitempty"`
}

type XaiQuotaDTO struct {
	Auto   *XaiQuotaWindowDTO `json:"auto,omitempty"`
	Fast   *XaiQuotaWindowDTO `json:"fast,omitempty"`
	Expert *XaiQuotaWindowDTO `json:"expert,omitempty"`
	Heavy  *XaiQuotaWindowDTO `json:"heavy,omitempty"`
	Grok43 *XaiQuotaWindowDTO `json:"grok43,omitempty"`
}

type XaiAccountDTO struct {
	ID               string            `json:"id,omitempty"`
	Email            string            `json:"email,omitempty"`
	Subject          string            `json:"subject,omitempty"`
	AccessToken      string            `json:"accessToken,omitempty"`
	RefreshToken     string            `json:"refreshToken,omitempty"`
	IDToken          string            `json:"idToken,omitempty"`
	AuthKind         string            `json:"authKind,omitempty"`
	APIKey           string            `json:"apiKey,omitempty"`
	SSO              string            `json:"sso,omitempty"`
	Enabled          bool              `json:"enabled"`
	Websockets       bool              `json:"websockets,omitempty"`
	// UsingApi：true=官方 API；false=cli-chat-proxy（OAuth 默认 false）
	UsingApi         bool              `json:"usingApi"`
	Pool             string            `json:"pool,omitempty"`
	Quota            *XaiQuotaDTO      `json:"quota,omitempty"`
	LastQuotaSync    string            `json:"lastQuotaSync,omitempty"`
	Status           string            `json:"status"`
	Weight           int               `json:"weight,omitempty"`
	ProxyUrl         string            `json:"proxyUrl,omitempty"`
	CustomHeaders    map[string]string `json:"customHeaders,omitempty"`
	ExpiresAt        string            `json:"expiresAt,omitempty"`
	LastRefresh      string            `json:"lastRefresh,omitempty"`
	CooldownUntil     string            `json:"cooldownUntil,omitempty"`
	CooldownReason    string            `json:"cooldownReason,omitempty"`
	CooldownRemaining int               `json:"cooldownRemaining,omitempty"`
	CreatedAt        string            `json:"createdAt,omitempty"`
	UpdatedAt        string            `json:"updatedAt,omitempty"`
	IsActive         bool              `json:"isActive"`
}

type XaiDeviceLoginDTO struct {
	DeviceCode              string `json:"deviceCode,omitempty"`
	UserCode                string `json:"userCode"`
	VerificationUri         string `json:"verificationUri,omitempty"`
	VerificationUriComplete string `json:"verificationUriComplete,omitempty"`
	ExpiresIn               int    `json:"expiresIn,omitempty"`
	Interval                int    `json:"interval,omitempty"`
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
	RotationMode   string            `json:"rotationMode"`
	ProxyUrl       string            `json:"proxyUrl"`
	BaseURL        string            `json:"baseURL"`
	ClientVersion  string            `json:"clientVersion,omitempty"`
	UserAgent      string            `json:"userAgent,omitempty"`
	TokenAuth      string            `json:"tokenAuth,omitempty"`
	ClientSurface  string            `json:"clientSurface,omitempty"`
	DynamicStatsig bool              `json:"dynamicStatsig"`
	CustomHeaders  map[string]string `json:"customHeaders,omitempty"`
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

func (a *App) StartXaiDeviceLogin() (*XaiDeviceLoginDTO, error) {
	xp := xaiProvider()
	if xp == nil {
		return nil, fmt.Errorf("xai plugin not available")
	}
	ctx := a.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	raw, err := xp.StartDeviceLogin(ctx, a.resolveXaiLoginProxy())
	if err != nil {
		return nil, err
	}
	var dto XaiDeviceLoginDTO
	if err := json.Unmarshal(raw, &dto); err != nil {
		return nil, err
	}
	return &dto, nil
}

func (a *App) WaitForXaiDeviceLogin() (*XaiLoginResultDTO, error) {
	xp := xaiProvider()
	if xp == nil {
		return nil, fmt.Errorf("xai plugin not available")
	}
	ctx := a.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	raw, err := xp.WaitForDeviceLogin(ctx)
	if err != nil {
		return nil, err
	}
	var result XaiLoginResultDTO
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, err
	}
	return &result, nil
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

// ProbeXaiAccountStream 对指定账号发起 responses SSE 连通测试。
func (a *App) ProbeXaiAccountStream(accountId string) (*XaiTestResult, error) {
	xp := xaiProvider()
	if xp == nil {
		return nil, fmt.Errorf("xai plugin not available")
	}
	ctx := a.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	raw, err := xp.ProbeAccountStream(ctx, a.getXaiMultiConfigPath(), accountId)
	if err != nil {
		return &XaiTestResult{Success: false, Error: err.Error()}, nil
	}
	var payload struct {
		Success bool           `json:"success"`
		Account *XaiAccountDTO `json:"account"`
		Error   string         `json:"error"`
		Detail  string         `json:"detail"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil, err
	}
	errMsg := strings.TrimSpace(payload.Error)
	if errMsg == "" && !payload.Success {
		errMsg = strings.TrimSpace(payload.Detail)
	}
	return &XaiTestResult{Success: payload.Success, Account: payload.Account, Error: errMsg}, nil
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

// RefreshXaiAccountQuota 拉取 rate-limits 并更新账号类型与额度。
func (a *App) RefreshXaiAccountQuota(accountId string) (*XaiTestResult, error) {
	xp := xaiProvider()
	if xp == nil {
		return nil, fmt.Errorf("xai plugin not available")
	}
	ctx := a.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	raw, err := xp.RefreshAccountQuota(ctx, a.getXaiMultiConfigPath(), accountId)
	if err != nil {
		return &XaiTestResult{Success: false, Error: err.Error()}, nil
	}
	var payload struct {
		Success bool           `json:"success"`
		Account *XaiAccountDTO `json:"account"`
		Error   string         `json:"error"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil, err
	}
	errMsg := strings.TrimSpace(payload.Error)
	if errMsg == "" && !payload.Success {
		errMsg = "refresh quota failed"
	}
	return &XaiTestResult{Success: payload.Success, Account: payload.Account, Error: errMsg}, nil
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

// xaiSyncExportRaw 导出 xai.json 供备份 / 远程同步。
func (a *App) xaiSyncExportRaw() json.RawMessage {
	p := plugin.ByName("xai-accounts")
	if p == nil {
		return nil
	}
	exporter, ok := p.(plugin.ConfigSyncExporter)
	if !ok {
		return nil
	}
	configPath := ""
	if a != nil && a.configLoader != nil {
		configPath = a.configLoader.GetPath()
	}
	_, data, err := exporter.SyncExport(configPath)
	if err != nil || len(data) == 0 {
		return nil
	}
	return data
}

// saveXaiSyncConfigInternal 从备份恢复 xai 配置。
// replaceMode=true 全量替换；false 时按账号 id 合并（远程覆盖同 id）。
func (a *App) saveXaiSyncConfigInternal(data interface{}, replaceMode bool) error {
	p := plugin.ByName("xai-accounts")
	if p == nil {
		return fmt.Errorf("xai plugin not available")
	}
	importer, ok := p.(plugin.ConfigSyncImporter)
	if !ok {
		return fmt.Errorf("xai plugin does not support sync import")
	}
	payloadRaw, err := json.Marshal(data)
	if err != nil {
		return fmt.Errorf("marshal xai backup data: %w", err)
	}
	configPath := ""
	if a != nil && a.configLoader != nil {
		configPath = a.configLoader.GetPath()
	}
	if !replaceMode {
		payloadRaw, err = a.mergeXaiSyncPayloadWithLocal(payloadRaw)
		if err != nil {
			return err
		}
	}
	if err := importer.SyncImport(configPath, payloadRaw); err != nil {
		return fmt.Errorf("import xai sync payload: %w", err)
	}
	return nil
}

func (a *App) mergeXaiSyncPayloadWithLocal(remoteRaw json.RawMessage) (json.RawMessage, error) {
	var remote map[string]interface{}
	if err := json.Unmarshal(remoteRaw, &remote); err != nil {
		return nil, fmt.Errorf("invalid remote xai payload: %w", err)
	}
	localRaw := a.xaiSyncExportRaw()
	if len(localRaw) == 0 {
		return remoteRaw, nil
	}
	var local map[string]interface{}
	if err := json.Unmarshal(localRaw, &local); err != nil {
		return remoteRaw, nil
	}
	// 合并 accounts：以 id 为键，远程覆盖本地
	localAccounts, _ := local["accounts"].([]interface{})
	remoteAccounts, _ := remote["accounts"].([]interface{})
	byID := map[string]interface{}{}
	order := make([]string, 0)
	appendAcc := func(item interface{}) {
		m, ok := item.(map[string]interface{})
		if !ok {
			return
		}
		id := strings.TrimSpace(fmt.Sprint(m["id"]))
		if id == "" || id == "<nil>" {
			id = strings.TrimSpace(fmt.Sprint(m["email"])) + "|" + strings.TrimSpace(fmt.Sprint(m["subject"]))
		}
		if id == "" || id == "|" {
			// 无 id：直接追加
			key := fmt.Sprintf("_anon_%d", len(order))
			byID[key] = m
			order = append(order, key)
			return
		}
		if _, exists := byID[id]; !exists {
			order = append(order, id)
		}
		byID[id] = m
	}
	for _, item := range localAccounts {
		appendAcc(item)
	}
	for _, item := range remoteAccounts {
		appendAcc(item)
	}
	merged := make([]interface{}, 0, len(order))
	for _, id := range order {
		merged = append(merged, byID[id])
	}
	// 顶层字段：远程非空优先
	out := local
	for k, v := range remote {
		if k == "accounts" {
			continue
		}
		out[k] = v
	}
	out["accounts"] = merged
	return json.Marshal(out)
}
