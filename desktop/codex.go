package main

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	codexShared "clisimplehub/internal/codex/shared"
	"clisimplehub/internal/plugin"

	"github.com/wailsapp/wails/v2/pkg/runtime"
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
	ID                string         `json:"id,omitempty"`
	RefreshToken      string         `json:"refreshToken"`
	Email             string         `json:"email,omitempty"`
	PlanType          string         `json:"planType,omitempty"`
	AccessToken       string         `json:"accessToken,omitempty"`
	IDToken           string         `json:"idToken,omitempty"`
	AccountID         string         `json:"accountId,omitempty"`
	Enabled           bool           `json:"enabled"`
	Websockets        bool           `json:"websockets,omitempty"`
	Status            string         `json:"status"`
	Weight            int            `json:"weight,omitempty"`
	ProxyUrl          string         `json:"proxyUrl,omitempty"`
	Password          string         `json:"password,omitempty"`
	MFACode           string         `json:"mfaCode,omitempty"`
	ExpiresAt         string         `json:"expiresAt,omitempty"`
	CooldownUntil     string         `json:"cooldownUntil,omitempty"`
	CooldownReason    string         `json:"cooldownReason,omitempty"`
	CooldownRemaining int            `json:"cooldownRemaining,omitempty"`
	CodexUsage        map[string]any `json:"codexUsage,omitempty"`
	CreatedAt         string         `json:"createdAt,omitempty"`
	UpdatedAt         string         `json:"updatedAt,omitempty"`
	TodayRequests     int64          `json:"todayRequests,omitempty"`
	TodayTotalTokens  int64          `json:"todayTotalTokens,omitempty"`
	IsActive          bool           `json:"isActive"`
}

type CodexAccountsResponse struct {
	ActiveRefreshToken string            `json:"activeRefreshToken"`
	ActiveAccountID    string            `json:"activeAccountId"`
	Accounts           []CodexAccountDTO `json:"accounts"`
}

type CodexAccountsPageResponse struct {
	ActiveRefreshToken string            `json:"activeRefreshToken"`
	ActiveAccountID    string            `json:"activeAccountId"`
	Accounts           []CodexAccountDTO `json:"accounts"`
	Offset             int               `json:"offset"`
	Limit              int               `json:"limit"`
	NextOffset         int               `json:"nextOffset"`
	Total              int               `json:"total"`
	HasMore            bool              `json:"hasMore"`
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

type HeadlessLoginStateDTO struct {
	State   int                  `json:"state"`
	NeedOTP bool                 `json:"needOTP,omitempty"`
	Result  *CodexLoginResultDTO `json:"result,omitempty"`
	Error   string               `json:"error,omitempty"`
}

type SignupStateDTO struct {
	State    int                  `json:"state"`
	NeedOTP  bool                 `json:"needOTP,omitempty"`
	Password string               `json:"password,omitempty"`
	Result   *CodexLoginResultDTO `json:"result,omitempty"`
	Error    string               `json:"error,omitempty"`
}

type CodexSignupRequestDTO struct {
	EmailProvider  string            `json:"emailProvider"`
	ProviderParams map[string]string `json:"providerParams"`
	Email          string            `json:"email"`
	Password       string            `json:"password"`
	ClientID       string            `json:"clientId"`
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

func (a *App) GetCodexAccountsPage(offset int, limit int) (*CodexAccountsPageResponse, error) {
	cp := codexProvider()
	if cp == nil {
		return nil, fmt.Errorf("codex plugin not available")
	}
	raw, err := cp.GetAccountsPage(a.getCodexMultiConfigPath(), offset, limit)
	if err != nil {
		return nil, err
	}
	var resp CodexAccountsPageResponse
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

func (a *App) DeleteCodexAccounts(accountIds []string) error {
	cp := codexProvider()
	if cp == nil {
		return fmt.Errorf("codex plugin not available")
	}
	return cp.DeleteAccounts(a.getCodexMultiConfigPath(), accountIds)
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
	proxyURL := a.resolveCodexLoginProxy()
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
	proxyURL := a.resolveCodexLoginProxy()
	return cp.StartLoginWithURL(ctx, proxyURL)
}

func (a *App) SubmitCodexLoginCallbackURL(callbackURL string) error {
	cp := codexProvider()
	if cp == nil {
		return fmt.Errorf("codex plugin not available")
	}
	ctx := a.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	return cp.SubmitLoginCallbackURL(ctx, callbackURL)
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

func (a *App) CancelCodexLogin() error {
	cp := codexProvider()
	if cp == nil {
		return fmt.Errorf("codex plugin not available")
	}
	return cp.CancelLogin()
}

func (a *App) StartCodexHeadlessLogin(email, password, clientID string) (*HeadlessLoginStateDTO, error) {
	return a.StartCodexHeadlessLoginWithProvider(email, password, clientID, "", nil)
}

func (a *App) StartCodexHeadlessLoginWithProvider(email, password, clientID, emailProvider string, providerParams map[string]string) (*HeadlessLoginStateDTO, error) {
	cp := codexProvider()
	if cp == nil {
		return nil, fmt.Errorf("codex plugin not available")
	}
	ctx := a.ctx
	if ctx == nil {
		ctx = context.Background()
	}

	// Resolve proxy URL with priority: appConfig.proxyUrl -> account proxy -> codex.json proxy
	// This follows the same pattern as forward.go:forwardToUpstream
	proxyURL := a.resolveCodexProxyForEmail(email)

	onStep := func(msg string) {
		if a.ctx != nil {
			runtime.EventsEmit(a.ctx, "codex:headless-step", msg)
		}
	}

	input := map[string]any{
		"email":          email,
		"password":       password,
		"clientId":       clientID,
		"proxyUrl":       proxyURL,
		"emailProvider":  emailProvider,
		"providerParams": providerParams,
	}
	rawReq, _ := json.Marshal(input)

	raw, err := cp.StartHeadlessLoginWithProvider(ctx, rawReq, onStep)
	if err != nil {
		return nil, err
	}
	var result HeadlessLoginStateDTO
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// resolveCodexLoginProxy resolves proxy URL for OAuth login (no account context yet).
// Priority: appConfig.proxyUrl -> codex.json global proxy
func (a *App) resolveCodexLoginProxy() string {
	proxyURL := plugin.GetAppProxyURL()
	if proxyURL != "" {
		return proxyURL
	}

	cp := codexProvider()
	if cp == nil {
		return ""
	}
	configPath := a.getCodexMultiConfigPath()
	if configPath == "" {
		return ""
	}
	globalConfigRaw, err := cp.GetCodexGlobalConfig(configPath)
	if err == nil {
		var gc struct {
			ProxyUrl string `json:"proxyUrl"`
		}
		if json.Unmarshal(globalConfigRaw, &gc) == nil {
			proxyURL = strings.TrimSpace(gc.ProxyUrl)
		}
	}
	return proxyURL
}

// resolveCodexProxyForEmail resolves proxy URL for a Codex account by email
// Priority: appConfig.proxyUrl -> account-level proxy -> pool.ProxyURL() (codex.json global proxy)
// This mirrors the logic in internal/codex/plugin/forward.go:forwardToUpstream
func (a *App) resolveCodexProxyForEmail(email string) string {
	configPath := a.getCodexMultiConfigPath()
	if configPath == "" {
		return ""
	}

	cp := codexProvider()
	if cp == nil {
		return ""
	}

	proxyURL := plugin.GetAppProxyURL()

	// Priority 2: Account-level proxy
	if proxyURL == "" {
		accountsRaw, err := cp.GetAccounts(configPath)
		if err == nil {
			var accountsResp struct {
				Accounts []struct {
					Email    string `json:"email"`
					ProxyUrl string `json:"proxyUrl"`
				} `json:"accounts"`
			}
			if json.Unmarshal(accountsRaw, &accountsResp) == nil {
				emailLower := strings.TrimSpace(strings.ToLower(email))
				for _, acc := range accountsResp.Accounts {
					if strings.TrimSpace(strings.ToLower(acc.Email)) == emailLower {
						proxyURL = strings.TrimSpace(acc.ProxyUrl)
						break
					}
				}
			}
		}
	}

	// Priority 3: Codex.json global proxy (pool.ProxyURL())
	if proxyURL == "" {
		globalConfigRaw, err := cp.GetCodexGlobalConfig(configPath)
		if err == nil {
			var globalConfig struct {
				ProxyUrl string `json:"proxyUrl"`
			}
			if json.Unmarshal(globalConfigRaw, &globalConfig) == nil {
				proxyURL = strings.TrimSpace(globalConfig.ProxyUrl)
			}
		}
	}

	return proxyURL
}

func (a *App) SubmitCodexHeadlessOTP(code string) (*HeadlessLoginStateDTO, error) {
	cp := codexProvider()
	if cp == nil {
		return nil, fmt.Errorf("codex plugin not available")
	}
	ctx := a.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	raw, err := cp.SubmitHeadlessOTP(ctx, code)
	if err != nil {
		// Still return the state DTO if available
		if raw != nil {
			var result HeadlessLoginStateDTO
			if jsonErr := json.Unmarshal(raw, &result); jsonErr == nil {
				return &result, err
			}
		}
		return nil, err
	}
	var result HeadlessLoginStateDTO
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

func (a *App) CancelCodexHeadlessLogin() error {
	cp := codexProvider()
	if cp == nil {
		return fmt.Errorf("codex plugin not available")
	}
	return cp.CancelHeadlessLogin()
}

// --- Signup ---

func (a *App) StartCodexSignup(req CodexSignupRequestDTO) (*SignupStateDTO, error) {
	cp := codexProvider()
	if cp == nil {
		return nil, fmt.Errorf("codex plugin not available")
	}
	ctx := a.ctx
	if ctx == nil {
		ctx = context.Background()
	}

	proxyURL := ""
	proxyURL = plugin.GetAppProxyURL()
	if proxyURL == "" {
		globalConfigRaw, err := cp.GetCodexGlobalConfig(a.getCodexMultiConfigPath())
		if err == nil {
			var gc struct {
				ProxyUrl string `json:"proxyUrl"`
			}
			if json.Unmarshal(globalConfigRaw, &gc) == nil {
				proxyURL = strings.TrimSpace(gc.ProxyUrl)
			}
		}
	}

	input := map[string]any{
		"emailProvider":  req.EmailProvider,
		"providerParams": req.ProviderParams,
		"email":          req.Email,
		"password":       req.Password,
		"clientId":       req.ClientID,
		"proxyUrl":       proxyURL,
	}
	rawReq, _ := json.Marshal(input)

	onStep := func(msg string) {
		if a.ctx != nil {
			runtime.EventsEmit(a.ctx, "codex:signup-step", msg)
		}
	}

	raw, err := cp.StartSignup(ctx, rawReq, onStep)
	if err != nil {
		return nil, err
	}
	var result SignupStateDTO
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

func (a *App) SubmitCodexSignupOTP(code string) (*SignupStateDTO, error) {
	cp := codexProvider()
	if cp == nil {
		return nil, fmt.Errorf("codex plugin not available")
	}
	ctx := a.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	raw, err := cp.SubmitSignupOTP(ctx, code)
	if err != nil {
		if raw != nil {
			var result SignupStateDTO
			if jsonErr := json.Unmarshal(raw, &result); jsonErr == nil {
				return &result, err
			}
		}
		return nil, err
	}
	var result SignupStateDTO
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

func (a *App) CancelCodexSignup() error {
	cp := codexProvider()
	if cp == nil {
		return fmt.Errorf("codex plugin not available")
	}
	return cp.CancelSignup()
}

func (a *App) GetCodexEmailProviders() ([]string, error) {
	cp := codexProvider()
	if cp == nil {
		return nil, fmt.Errorf("codex plugin not available")
	}
	raw, err := cp.GetEmailProviders()
	if err != nil {
		return nil, err
	}
	var providers []string
	if err := json.Unmarshal(raw, &providers); err != nil {
		return nil, err
	}
	return providers, nil
}

type GeneratedCredentialsDTO struct {
	Email         string            `json:"email"`
	Password      string            `json:"password"`
	ProviderState map[string]string `json:"providerState"`
}

func (a *App) GenerateCodexRandomEmail(provider string, params map[string]string) (*GeneratedCredentialsDTO, error) {
	cp := codexProvider()
	if cp == nil {
		return nil, fmt.Errorf("codex plugin not available")
	}
	paramsJSON, err := json.Marshal(params)
	if err != nil {
		return nil, err
	}
	raw, err := cp.GenerateRandomEmail(provider, paramsJSON)
	if err != nil {
		return nil, err
	}
	var result GeneratedCredentialsDTO
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

type CodexAccountStatsDTO struct {
	AccountID    string  `json:"accountId"`
	AccountEmail string  `json:"accountEmail"`
	RequestCount int64   `json:"requestCount"`
	InputTokens  int64   `json:"inputTokens"`
	OutputTokens int64   `json:"outputTokens"`
	TotalTokens  int64   `json:"totalTokens"`
	ErrorCount   int64   `json:"errorCount"`
	AvgDuration  float64 `json:"avgDurationMs"`
}

func (a *App) GetCodexAccountStats(timeRange string) ([]CodexAccountStatsDTO, error) {
	cp := codexProvider()
	if cp == nil {
		return nil, fmt.Errorf("codex plugin not available")
	}
	raw, err := cp.GetCodexAccountStats(context.Background(), timeRange)
	if err != nil {
		return nil, err
	}
	var result []CodexAccountStatsDTO
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, err
	}
	return result, nil
}

// codexSyncExportRaw exports codex accounts + global config JSON for server sync.
func (a *App) codexSyncExportRaw() (json.RawMessage, error) {
	p := plugin.ByName("codex-accounts")
	if p == nil {
		return nil, nil
	}
	exporter, ok := p.(plugin.ConfigSyncExporter)
	if !ok {
		return nil, nil
	}

	configPath := ""
	if a.configLoader != nil {
		configPath = a.configLoader.GetPath()
	}
	_, data, err := exporter.SyncExport(configPath)
	if err != nil {
		return nil, err
	}
	if len(data) == 0 {
		return nil, nil
	}
	return data, nil
}

type codexSyncPayloadDTO struct {
	MultiConfig map[string]interface{}   `json:"multiConfig"`
	Accounts    []map[string]interface{} `json:"accounts"`
}

// saveCodexSyncConfigInternal restores codex sync payload from backup.
// replaceMode=true: full replace, replaceMode=false: merge with local accounts/config.
func (a *App) saveCodexSyncConfigInternal(data interface{}, replaceMode bool) error {
	p := plugin.ByName("codex-accounts")
	if p == nil {
		return fmt.Errorf("codex plugin not available")
	}
	importer, ok := p.(plugin.ConfigSyncImporter)
	if !ok {
		return fmt.Errorf("codex plugin does not support sync import")
	}

	payloadRaw, err := json.Marshal(data)
	if err != nil {
		return fmt.Errorf("marshal codex backup data: %w", err)
	}

	if !replaceMode {
		payloadRaw, err = a.mergeCodexSyncPayloadWithLocal(payloadRaw)
		if err != nil {
			return err
		}
	}

	configPath := ""
	if a.configLoader != nil {
		configPath = a.configLoader.GetPath()
	}
	if err := importer.SyncImport(configPath, payloadRaw); err != nil {
		return fmt.Errorf("import codex sync payload: %w", err)
	}
	return nil
}

func (a *App) mergeCodexSyncPayloadWithLocal(remoteRaw json.RawMessage) (json.RawMessage, error) {
	var remotePayload codexSyncPayloadDTO
	if err := json.Unmarshal(remoteRaw, &remotePayload); err != nil {
		return nil, fmt.Errorf("invalid codex backup payload: %w", err)
	}
	normalizeCodexSyncPayload(&remotePayload)

	localRaw, err := a.codexSyncExportRaw()
	if err != nil {
		return nil, fmt.Errorf("export local codex payload: %w", err)
	}
	if len(localRaw) == 0 {
		return json.Marshal(remotePayload)
	}

	var localPayload codexSyncPayloadDTO
	if err := json.Unmarshal(localRaw, &localPayload); err != nil {
		return nil, fmt.Errorf("invalid local codex payload: %w", err)
	}
	normalizeCodexSyncPayload(&localPayload)

	mergedAccounts, err := mergeCodexAccounts(localPayload.Accounts, remotePayload.Accounts)
	if err != nil {
		return nil, err
	}

	mergedMultiConfig := deepMergeJSONMaps(localPayload.MultiConfig, remotePayload.MultiConfig)
	activeAccountID := pickCodexActiveAccountID(mergedAccounts, remotePayload.MultiConfig, localPayload.MultiConfig)
	if activeAccountID == "" {
		delete(mergedMultiConfig, "activeAccountId")
	} else {
		mergedMultiConfig["activeAccountId"] = activeAccountID
	}

	return json.Marshal(codexSyncPayloadDTO{
		MultiConfig: mergedMultiConfig,
		Accounts:    mergedAccounts,
	})
}

func normalizeCodexSyncPayload(payload *codexSyncPayloadDTO) {
	if payload == nil {
		return
	}
	if payload.MultiConfig == nil {
		payload.MultiConfig = map[string]interface{}{}
	}
	if payload.Accounts == nil {
		payload.Accounts = []map[string]interface{}{}
	}
}

func mergeCodexAccounts(localAccounts, remoteAccounts []map[string]interface{}) ([]map[string]interface{}, error) {
	localByID := make(map[string]map[string]interface{}, len(localAccounts))
	for _, account := range localAccounts {
		accountID := getCodexLocalID(account)
		if accountID == "" {
			return nil, fmt.Errorf("local codex payload contains account without id")
		}
		localByID[accountID] = account
	}

	merged := make([]map[string]interface{}, 0, len(localAccounts)+len(remoteAccounts))
	mergedIndex := make(map[string]int, len(remoteAccounts))
	seen := make(map[string]struct{}, len(localAccounts)+len(remoteAccounts))

	for _, remoteAccount := range remoteAccounts {
		accountID := getCodexLocalID(remoteAccount)
		if accountID == "" {
			return nil, fmt.Errorf("backup codex payload contains account without id")
		}

		account := deepMergeJSONMaps(localByID[accountID], remoteAccount)
		account["id"] = accountID

		if idx, exists := mergedIndex[accountID]; exists {
			merged[idx] = account
		} else {
			mergedIndex[accountID] = len(merged)
			merged = append(merged, account)
		}
		seen[accountID] = struct{}{}
	}

	for _, localAccount := range localAccounts {
		accountID := getCodexLocalID(localAccount)
		if _, exists := seen[accountID]; exists {
			continue
		}
		account := deepMergeJSONMaps(nil, localAccount)
		account["id"] = accountID
		merged = append(merged, account)
	}

	return merged, nil
}

func pickCodexActiveAccountID(accounts []map[string]interface{}, remoteMultiConfig, localMultiConfig map[string]interface{}) string {
	validAccountIDs := make(map[string]struct{}, len(accounts))
	legacyAccountIDs := make(map[string]string, len(accounts))
	for _, account := range accounts {
		accountID := getCodexLocalID(account)
		if accountID != "" {
			validAccountIDs[accountID] = struct{}{}
		}
		if upstreamID := getCodexAccountID(account); upstreamID != "" {
			legacyAccountIDs[upstreamID] = accountID
		}
	}

	candidates := []string{
		getCodexActiveAccountID(remoteMultiConfig),
		getCodexActiveAccountID(localMultiConfig),
	}
	for _, candidate := range candidates {
		if _, exists := validAccountIDs[candidate]; exists {
			return candidate
		}
		if localID, exists := legacyAccountIDs[candidate]; exists {
			return localID
		}
	}
	if len(accounts) > 0 {
		return getCodexLocalID(accounts[0])
	}
	return ""
}

func getCodexLocalID(account map[string]interface{}) string {
	if account == nil {
		return ""
	}
	id, _ := account["id"].(string)
	id = strings.TrimSpace(id)
	if id != "" {
		return id
	}
	id = codexShared.GenerateCodexLocalID(getCodexAccountID(account), getCodexAccountEmail(account))
	if id != "" {
		return id
	}
	return getCodexAccountID(account)
}

func getCodexAccountID(account map[string]interface{}) string {
	if account == nil {
		return ""
	}
	accountID, _ := account["accountId"].(string)
	return strings.TrimSpace(accountID)
}

func getCodexAccountEmail(account map[string]interface{}) string {
	if account == nil {
		return ""
	}
	email, _ := account["email"].(string)
	return strings.TrimSpace(email)
}

func getCodexActiveAccountID(multiConfig map[string]interface{}) string {
	if multiConfig == nil {
		return ""
	}
	activeAccountID, _ := multiConfig["activeAccountId"].(string)
	return strings.TrimSpace(activeAccountID)
}

func deepMergeJSONMaps(base, override map[string]interface{}) map[string]interface{} {
	merged := make(map[string]interface{})
	for key, val := range base {
		merged[key] = cloneJSONValue(val)
	}
	for key, val := range override {
		overrideMap, overrideIsMap := val.(map[string]interface{})
		baseMap, baseIsMap := merged[key].(map[string]interface{})
		if overrideIsMap && baseIsMap {
			merged[key] = deepMergeJSONMaps(baseMap, overrideMap)
			continue
		}
		merged[key] = cloneJSONValue(val)
	}
	return merged
}

func cloneJSONValue(value interface{}) interface{} {
	switch v := value.(type) {
	case map[string]interface{}:
		return deepMergeJSONMaps(nil, v)
	case []interface{}:
		cloned := make([]interface{}, len(v))
		for i := range v {
			cloned[i] = cloneJSONValue(v[i])
		}
		return cloned
	default:
		return v
	}
}
