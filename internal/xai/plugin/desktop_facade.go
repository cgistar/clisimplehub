package xaiplugin

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"clisimplehub/internal/executor"
	"clisimplehub/internal/plugin"
	xai "clisimplehub/internal/xai"
	xaiAuth "clisimplehub/internal/xai/auth"
	xaiBackend "clisimplehub/internal/xai/backend"
	xaiShared "clisimplehub/internal/xai/shared"
)

type desktopFacade struct{}

var _ plugin.XaiDesktopProvider = (*desktopFacade)(nil)

func (d *desktopFacade) DefaultMultiConfigBasename() string {
	return xaiShared.GetDefaultXaiMultiConfigPath()
}

func getService() *XaiService {
	p := plugin.ByName("xai-accounts")
	if p == nil {
		return nil
	}
	xp, ok := p.(*XaiPlugin)
	if !ok {
		return nil
	}
	return xp.GetService()
}

func ensurePool(configPath string) (*xai.XaiAccountPool, error) {
	pool := xai.GetPool()
	if pool != nil {
		return pool, nil
	}
	path := resolveConfigPath(configPath)
	if path == "" {
		path = xaiShared.GetDefaultXaiMultiConfigPath()
	}
	if err := xai.InitPool(path); err != nil {
		return nil, err
	}
	pool = xai.GetPool()
	if pool == nil {
		return nil, fmt.Errorf("xai pool not initialized")
	}
	return pool, nil
}

func (d *desktopFacade) GetAccounts(configPath string) (json.RawMessage, error) {
	pool, err := ensurePool(configPath)
	if err != nil {
		return nil, err
	}
	// 从 xai.json 重新加载，保证外部改文件后列表与磁盘一致
	if err := pool.Reload(); err != nil {
		return nil, err
	}
	accounts := pool.ListAccounts()
	activeID := pool.ActiveAccountID()
	dtos := make([]map[string]any, 0, len(accounts))
	for _, acc := range accounts {
		dtos = append(dtos, accountToDTO(acc, activeID))
	}
	return json.Marshal(map[string]any{
		"activeAccountId": activeID,
		"accounts":        dtos,
	})
}

func (d *desktopFacade) GetAccountsPage(configPath string, offset, limit int) (json.RawMessage, error) {
	if offset < 0 {
		offset = 0
	}
	if limit <= 0 {
		limit = 50
	}
	pool, err := ensurePool(configPath)
	if err != nil {
		return nil, err
	}
	// 从 xai.json 重新加载，保证外部改文件后列表与磁盘一致
	if err := pool.Reload(); err != nil {
		return nil, err
	}
	accounts := pool.ListAccounts()
	activeID := pool.ActiveAccountID()
	total := len(accounts)
	if offset > total {
		offset = total
	}
	end := offset + limit
	if end > total {
		end = total
	}
	page := accounts[offset:end]
	dtos := make([]map[string]any, 0, len(page))
	for _, acc := range page {
		dtos = append(dtos, accountToDTO(acc, activeID))
	}
	nextOffset := end
	hasMore := end < total
	return json.Marshal(map[string]any{
		"activeAccountId": activeID,
		"accounts":        dtos,
		"offset":          offset,
		"limit":           limit,
		"nextOffset":      nextOffset,
		"total":           total,
		"hasMore":         hasMore,
	})
}

func (d *desktopFacade) GetActiveAccount(configPath string) (json.RawMessage, error) {
	pool, err := ensurePool(configPath)
	if err != nil {
		return nil, err
	}
	snap := pool.Snapshot()
	if snap == nil {
		return json.Marshal(nil)
	}
	acc := snap.GetActiveAccount()
	if acc == nil {
		return json.Marshal(nil)
	}
	return json.Marshal(accountToDTO(*acc, pool.ActiveAccountID()))
}

func (d *desktopFacade) SetActiveAccount(configPath, accountID string) error {
	pool, err := ensurePool(configPath)
	if err != nil {
		return err
	}
	return pool.SetActiveAccount(accountID)
}

func (d *desktopFacade) AddAccount(configPath string, dtoJSON json.RawMessage) (json.RawMessage, error) {
	var dto map[string]any
	if err := json.Unmarshal(dtoJSON, &dto); err != nil {
		return nil, err
	}
	account := dtoToAccount(dto)
	xaiShared.NormalizeAccount(&account)
	if account.AuthKind == xaiShared.AuthKindAPIKey {
		if strings.TrimSpace(account.APIKey) == "" {
			return nil, fmt.Errorf("apiKey is required for api_key auth")
		}
	} else if strings.TrimSpace(account.RefreshToken) == "" &&
		strings.TrimSpace(account.AccessToken) == "" &&
		strings.TrimSpace(account.SSO) == "" {
		return nil, fmt.Errorf("refreshToken, accessToken or sso is required")
	}
	if strings.TrimSpace(account.ID) == "" {
		return nil, fmt.Errorf("unable to derive account id")
	}

	pool, err := ensurePool(configPath)
	if err != nil {
		return nil, err
	}
	// merge with existing if same id
	if existing := findAccount(pool, account.ID); existing != nil {
		if account.CreatedAt.IsZero() {
			account.CreatedAt = existing.CreatedAt
		}
		if strings.TrimSpace(account.Email) == "" {
			account.Email = existing.Email
		}
		if strings.TrimSpace(account.Subject) == "" {
			account.Subject = existing.Subject
		}
		if strings.TrimSpace(account.SSO) == "" {
			account.SSO = existing.SSO
		}
		if strings.TrimSpace(account.Pool) == "" {
			account.Pool = existing.Pool
		}
		if account.Quota == nil {
			account.Quota = existing.Quota
		}
		if account.LastQuotaSync.IsZero() {
			account.LastQuotaSync = existing.LastQuotaSync
		}
	}
	saved, err := pool.UpsertAccount(account)
	if err != nil {
		return nil, err
	}
	// 导入后尽力同步额度与账号类型（需要 sso；失败不阻断导入）
	if refreshed, refreshErr := d.syncAccountQuota(context.Background(), pool, saved); refreshErr == nil && refreshed != nil {
		saved = refreshed
	}
	return json.Marshal(accountToDTO(*saved, pool.ActiveAccountID()))
}

func (d *desktopFacade) UpdateAccount(configPath string, dtoJSON json.RawMessage) error {
	var dto map[string]any
	if err := json.Unmarshal(dtoJSON, &dto); err != nil {
		return err
	}
	id := strings.TrimSpace(asString(dto["id"]))
	if id == "" {
		return fmt.Errorf("id is required")
	}
	pool, err := ensurePool(configPath)
	if err != nil {
		return err
	}
	existing := findAccount(pool, id)
	if existing == nil {
		return fmt.Errorf("account not found: %s", id)
	}
	account := *existing
	if v, ok := dto["email"]; ok {
		account.Email = strings.TrimSpace(asString(v))
	}
	if v, ok := dto["subject"]; ok {
		account.Subject = strings.TrimSpace(asString(v))
	}
	if v, ok := dto["accessToken"]; ok {
		account.AccessToken = strings.TrimSpace(asString(v))
	}
	if v, ok := dto["refreshToken"]; ok {
		if rt := strings.TrimSpace(asString(v)); rt != "" {
			account.RefreshToken = rt
		}
	}
	if v, ok := dto["idToken"]; ok {
		account.IDToken = strings.TrimSpace(asString(v))
	}
	if v, ok := dto["apiKey"]; ok {
		account.APIKey = strings.TrimSpace(asString(v))
	}
	if v, ok := dto["sso"]; ok {
		account.SSO = strings.TrimSpace(asString(v))
	}
	if v, ok := dto["authKind"]; ok {
		if kind := strings.TrimSpace(asString(v)); kind != "" {
			account.AuthKind = kind
		}
	}
	if v, ok := dto["proxyUrl"]; ok {
		account.ProxyUrl = strings.TrimSpace(asString(v))
	}
	if v, ok := dto["customHeaders"]; ok {
		account.CustomHeaders = asStringMap(v)
	}
	if v, ok := dto["enabled"]; ok {
		account.Enabled = asBool(v, account.Enabled)
	}
	if v, ok := dto["websockets"]; ok {
		account.SetWebsockets(asBool(v, account.WebsocketsEnabled()))
	}
	if v, ok := dto["usingApi"]; ok {
		account.SetUsingAPI(asBool(v, account.UsingAPIEnabled()))
	}
	if v, ok := dto["weight"]; ok {
		if w := asInt(v); w > 0 {
			account.Weight = w
		}
	}
	if v, ok := dto["status"]; ok {
		if status := strings.TrimSpace(asString(v)); status != "" {
			account.Status = xaiShared.XaiAccountStatus(status)
		}
	}
	if v, ok := dto["expiresAt"]; ok {
		account.ExpiresAt = parseTimeString(asString(v))
	}
	_, err = pool.UpsertAccount(account)
	return err
}

func (d *desktopFacade) DeleteAccount(configPath, accountID string) error {
	return d.DeleteAccounts(configPath, []string{accountID})
}

func (d *desktopFacade) DeleteAccounts(configPath string, accountIDs []string) error {
	pool, err := ensurePool(configPath)
	if err != nil {
		return err
	}
	_, err = pool.DeleteAccounts(accountIDs)
	return err
}

func (d *desktopFacade) GetXaiGlobalConfig(configPath string) (json.RawMessage, error) {
	pool, err := ensurePool(configPath)
	if err != nil {
		return nil, err
	}
	// 从 xai.json 重载，保证 UI 与磁盘一致（含 rotationMode）
	if err := pool.Reload(); err != nil {
		return nil, err
	}
	snap := pool.Snapshot()
	if snap == nil {
		snap = &xaiShared.XaiMultiConfig{Config: xaiShared.DefaultXaiConfig()}
	}
	baseURL := strings.TrimSpace(snap.Config.BaseURL)
	if baseURL == "" {
		baseURL = xaiAuth.DefaultAPIBaseURL
	}
	return json.Marshal(map[string]any{
		"rotationMode":     snap.GetRotationMode(),
		"proxyUrl":         snap.ProxyUrl,
		"baseURL":          baseURL,
		"clientVersion":    snap.Config.ClientVersion,
		"userAgent":        snap.Config.UserAgent,
		"tokenAuth":        snap.Config.TokenAuth,
		"clientSurface":    snap.Config.ClientSurface,
		"dynamicStatsig":   snap.Config.DynamicStatsigEnabled(),
		"autoRefreshToken": snap.Config.AutoRefreshTokenEnabled(),
		"customHeaders":    snap.Config.CustomHeaders,
	})
}

func (d *desktopFacade) SaveXaiGlobalConfig(configPath string, dtoJSON json.RawMessage) error {
	var dto struct {
		RotationMode     string            `json:"rotationMode"`
		ProxyUrl         string            `json:"proxyUrl"`
		BaseURL          string            `json:"baseURL"`
		ClientVersion    string            `json:"clientVersion"`
		UserAgent        string            `json:"userAgent"`
		TokenAuth        string            `json:"tokenAuth"`
		ClientSurface    string            `json:"clientSurface"`
		DynamicStatsig   *bool             `json:"dynamicStatsig"`
		AutoRefreshToken *bool             `json:"autoRefreshToken"`
		CustomHeaders    map[string]string `json:"customHeaders"`
	}
	if err := json.Unmarshal(dtoJSON, &dto); err != nil {
		return err
	}
	pool, err := ensurePool(configPath)
	if err != nil {
		return err
	}
	cfg := xaiShared.XaiConfig{
		BaseURL:       strings.TrimSpace(dto.BaseURL),
		ClientVersion: strings.TrimSpace(dto.ClientVersion),
		UserAgent:     strings.TrimSpace(dto.UserAgent),
		TokenAuth:     strings.TrimSpace(dto.TokenAuth),
		ClientSurface: strings.TrimSpace(dto.ClientSurface),
		CustomHeaders: dto.CustomHeaders,
	}
	// 未传字段时保持默认 true
	if dto.DynamicStatsig != nil {
		cfg.SetDynamicStatsig(*dto.DynamicStatsig)
	} else {
		cfg.SetDynamicStatsig(true)
	}
	if dto.AutoRefreshToken != nil {
		cfg.SetAutoRefreshToken(*dto.AutoRefreshToken)
	}
	// 先写盘再 Reload，确保 rotationMode 落盘且运行时池同步
	if err := pool.SaveGlobalConfig(dto.RotationMode, dto.ProxyUrl, cfg); err != nil {
		return err
	}
	if err := pool.Reload(); err != nil {
		return err
	}
	if svc := getService(); svc != nil {
		svc.reconcileTokenRefreshScheduler(pool)
	}
	return nil
}

func (d *desktopFacade) SetAutoRefreshToken(configPath string, enabled bool) error {
	pool, err := ensurePool(configPath)
	if err != nil {
		return err
	}
	if err := pool.SetAutoRefreshToken(enabled); err != nil {
		return err
	}
	if svc := getService(); svc != nil {
		svc.reconcileTokenRefreshScheduler(pool)
	}
	return nil
}

func (d *desktopFacade) StartLoginWithURL(ctx context.Context, proxyURL string) (string, error) {
	svc := getService()
	if svc == nil {
		return "", fmt.Errorf("xai service not available")
	}
	svc.cancelLoginSession()

	authURL, waitFn, cleanupFn, err := xaiAuth.StartXAILoginWithURL(ctx, proxyURL)
	if err != nil {
		return "", err
	}
	svc.storeLoginSession(waitFn, cleanupFn)
	return authURL, nil
}

func (d *desktopFacade) SubmitLoginCallbackURL(ctx context.Context, callbackURL string) error {
	return xaiAuth.SubmitCallbackURL(ctx, callbackURL)
}

func (d *desktopFacade) WaitForLoginCallback(ctx context.Context) (json.RawMessage, error) {
	svc := getService()
	if svc == nil {
		return nil, fmt.Errorf("xai service not available")
	}
	waitFn, cleanupFn, sessionID := svc.popLoginSession()
	if waitFn == nil {
		return nil, fmt.Errorf("no login session")
	}
	defer func() {
		if cleanupFn != nil {
			cleanupFn()
		}
		svc.clearLoginSession(sessionID)
	}()

	result, err := waitFn()
	if err != nil {
		return nil, err
	}
	if svc != nil {
		svc.ensureXaiEndpoints()
	}
	return json.Marshal(result)
}

func (d *desktopFacade) CancelLogin() error {
	svc := getService()
	if svc == nil {
		return fmt.Errorf("xai service not available")
	}
	svc.cancelLoginSession()
	svc.cancelDeviceSession()
	return nil
}

// StartDeviceLogin 发起 device code 登录，返回 user_code / verification_uri。
func (d *desktopFacade) StartDeviceLogin(ctx context.Context, proxyURL string) (json.RawMessage, error) {
	svc := getService()
	if svc == nil {
		return nil, fmt.Errorf("xai service not available")
	}
	svc.cancelDeviceSession()

	auth := xaiAuth.NewXAIAuth(proxyURL)
	deviceCode, err := auth.StartDeviceFlow(ctx)
	if err != nil {
		return nil, err
	}
	pollCtx, cancel := context.WithCancel(context.Background())
	waitFn := func() (*xaiAuth.LoginResult, error) {
		bundle, err := auth.WaitForAuthorization(pollCtx, deviceCode)
		if err != nil {
			return nil, err
		}
		return &xaiAuth.LoginResult{
			AccessToken:   bundle.TokenData.AccessToken,
			RefreshToken:  bundle.TokenData.RefreshToken,
			IDToken:       bundle.TokenData.IDToken,
			Email:         bundle.TokenData.Email,
			Subject:       bundle.TokenData.Subject,
			ExpiresAt:     bundle.TokenData.Expire,
			BaseURL:       bundle.BaseURL,
			TokenEndpoint: bundle.TokenEndpoint,
			LastRefresh:   bundle.LastRefresh,
		}, nil
	}
	svc.storeDeviceSession(deviceCode, waitFn, cancel)

	return json.Marshal(map[string]any{
		"deviceCode":              deviceCode.DeviceCode,
		"userCode":                deviceCode.UserCode,
		"verificationUri":         deviceCode.VerificationURI,
		"verificationUriComplete": deviceCode.VerificationURIComplete,
		"expiresIn":               deviceCode.ExpiresIn,
		"interval":                deviceCode.Interval,
	})
}

// WaitForDeviceLogin 等待用户完成 device 授权。
func (d *desktopFacade) WaitForDeviceLogin(ctx context.Context) (json.RawMessage, error) {
	svc := getService()
	if svc == nil {
		return nil, fmt.Errorf("xai service not available")
	}
	_, waitFn, cancel, sessionID := svc.popDeviceSession()
	if waitFn == nil {
		return nil, fmt.Errorf("no device login session")
	}
	defer func() {
		if cancel != nil {
			cancel()
		}
		_ = sessionID
	}()

	// 允许外部 ctx 取消
	done := make(chan struct{})
	var result *xaiAuth.LoginResult
	var waitErr error
	go func() {
		result, waitErr = waitFn()
		close(done)
	}()
	select {
	case <-ctx.Done():
		if cancel != nil {
			cancel()
		}
		<-done
		return nil, ctx.Err()
	case <-done:
		if waitErr != nil {
			return nil, waitErr
		}
		// 成功后确保 endpoints
		svc.ensureXaiEndpoints()
		return json.Marshal(result)
	}
}

func (d *desktopFacade) TestAccount(configPath, accountID string) (json.RawMessage, error) {
	return d.RefreshAccountToken(context.Background(), configPath, accountID)
}

// ConvertSSOToAuth 使用账号已有的 SSO Cookie 获取并原子回写 OAuth 凭据。
func (d *desktopFacade) ConvertSSOToAuth(ctx context.Context, configPath, accountID string) (json.RawMessage, error) {
	accountID = strings.TrimSpace(accountID)
	if accountID == "" {
		return nil, fmt.Errorf("accountId is required")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	pool, err := ensurePool(configPath)
	if err != nil {
		return nil, err
	}
	account := findAccount(pool, accountID)
	if account == nil {
		return nil, fmt.Errorf("account not found: %s", accountID)
	}
	if strings.TrimSpace(account.SSO) == "" {
		return nil, fmt.Errorf("sso cookie is required")
	}

	value, err, _ := ssoAuthGroup.Do(accountID, func() (any, error) {
		current := findAccount(pool, accountID)
		if current == nil {
			return nil, fmt.Errorf("account not found: %s", accountID)
		}
		sso := strings.TrimSpace(current.SSO)
		if sso == "" {
			return nil, fmt.Errorf("sso cookie is required")
		}
		result, exchangeErr := xaiAuth.ExchangeSSOForTokens(ctx, sso, resolveAccountProxy(pool, current))
		if exchangeErr != nil {
			return nil, exchangeErr
		}
		token := result.TokenData
		expiresAt := parseTimeString(token.Expire)
		saved, saveErr := pool.ApplySSOAuthCredentials(
			accountID,
			token.AccessToken,
			token.RefreshToken,
			token.IDToken,
			token.Email,
			token.Subject,
			expiresAt,
		)
		if saveErr != nil {
			return nil, saveErr
		}
		return map[string]any{
			"success": true,
			"account": accountToDTO(*saved, pool.ActiveAccountID()),
			"warning": strings.TrimSpace(result.Warning),
		}, nil
	})
	if err != nil {
		return nil, err
	}
	payload, ok := value.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("unexpected sso2auth result")
	}
	return json.Marshal(payload)
}

// ImportSSOAccount 使用原始 SSO 获取 OAuth 凭据，并按账号身份新增或更新账号。
func (d *desktopFacade) ImportSSOAccount(ctx context.Context, configPath, sso string) (json.RawMessage, error) {
	sso = strings.TrimSpace(sso)
	if sso == "" {
		return nil, fmt.Errorf("sso cookie is required")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	pool, err := ensurePool(configPath)
	if err != nil {
		return nil, err
	}

	var existing *xaiShared.XaiAccount
	for _, account := range pool.ListAccounts() {
		if strings.TrimSpace(account.SSO) == sso {
			copy := account
			existing = &copy
			break
		}
	}
	result, err := xaiAuth.ExchangeSSOForTokens(ctx, sso, resolveAccountProxy(pool, existing))
	if err != nil {
		return nil, err
	}
	token := result.TokenData
	saved, created, err := pool.UpsertImportedSSOAccount(xaiShared.XaiAccount{
		Email:        token.Email,
		Subject:      token.Subject,
		AccessToken:  token.AccessToken,
		RefreshToken: token.RefreshToken,
		IDToken:      token.IDToken,
		SSO:          sso,
		ExpiresAt:    parseTimeString(token.Expire),
	})
	if err != nil {
		return nil, err
	}
	action := "updated"
	if created {
		action = "created"
	}
	return json.Marshal(map[string]any{
		"success": true,
		"action":  action,
		"account": accountToDTO(*saved, pool.ActiveAccountID()),
		"warning": strings.TrimSpace(result.Warning),
	})
}

// RefreshAccountQuota 拉取 grok.com rate-limits，更新 pool + quota。
func (d *desktopFacade) RefreshAccountQuota(ctx context.Context, configPath, accountID string) (json.RawMessage, error) {
	accountID = strings.TrimSpace(accountID)
	if accountID == "" {
		return nil, fmt.Errorf("accountId is required")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	pool, err := ensurePool(configPath)
	if err != nil {
		return nil, err
	}
	account := findAccount(pool, accountID)
	if account == nil {
		return nil, fmt.Errorf("account not found: %s", accountID)
	}
	saved, err := d.syncAccountQuota(ctx, pool, account)
	if err != nil {
		return json.Marshal(map[string]any{
			"success": false,
			"error":   err.Error(),
		})
	}
	return json.Marshal(map[string]any{
		"success": true,
		"account": accountToDTO(*saved, pool.ActiveAccountID()),
	})
}

// syncAccountQuota 用 sso 调用 rate-limits 并落盘。
func (d *desktopFacade) syncAccountQuota(
	ctx context.Context,
	pool *xai.XaiAccountPool,
	account *xaiShared.XaiAccount,
) (*xaiShared.XaiAccount, error) {
	if account == nil {
		return nil, fmt.Errorf("account is nil")
	}
	sso := strings.TrimSpace(account.SSO)
	if sso == "" {
		return nil, fmt.Errorf("sso cookie is required to refresh quota")
	}
	proxyURL := resolveAccountProxy(pool, account)
	dynamicStatsig := true
	if snap := pool.Snapshot(); snap != nil {
		dynamicStatsig = snap.Config.DynamicStatsigEnabled()
	}
	windows, err := xaiAuth.FetchAllRateLimits(ctx, sso, proxyURL, xaiAuth.RateLimitFetchOptions{
		DynamicStatsig: dynamicStatsig,
	})
	if err != nil {
		return nil, err
	}
	updated := *account
	xaiAuth.ApplyRateLimitsToAccount(&updated, windows)
	saved, err := pool.UpsertAccount(updated)
	if err != nil {
		return nil, err
	}
	return saved, nil
}

func (d *desktopFacade) RefreshAccountToken(ctx context.Context, configPath, accountID string) (json.RawMessage, error) {
	accountID = strings.TrimSpace(accountID)
	if accountID == "" {
		return nil, fmt.Errorf("accountId is required")
	}
	pool, err := ensurePool(configPath)
	if err != nil {
		return nil, err
	}
	account := findAccount(pool, accountID)
	if account == nil {
		return nil, fmt.Errorf("account not found: %s", accountID)
	}

	snap := pool.Snapshot()
	proxyURL := strings.TrimSpace(account.ProxyUrl)
	if proxyURL == "" && snap != nil {
		proxyURL = strings.TrimSpace(snap.ProxyUrl)
	}

	// API Key：轻量校验 models 接口
	if strings.EqualFold(account.AuthKind, xaiShared.AuthKindAPIKey) || (account.APIKey != "" && account.RefreshToken == "") {
		token := account.BearerToken()
		if token == "" {
			return nil, fmt.Errorf("api key is empty")
		}
		// probe 走 chat base（按账号 usingApi），与线上转发一致
		baseURL := xaiBackend.ResolveChatBaseURL(snap, account)
		if err := probeModels(ctx, baseURL, token, proxyURL); err != nil {
			account.Status = xaiShared.XaiStatusUnknown
			_, _ = pool.UpsertAccount(*account)
			return nil, err
		}
		account.Status = xaiShared.XaiStatusValid
		account.LastRefresh = time.Now().UTC()
		saved, err := pool.UpsertAccount(*account)
		if err != nil {
			return nil, err
		}
		return json.Marshal(map[string]any{
			"success": true,
			"account": accountToDTO(*saved, pool.ActiveAccountID()),
		})
	}

	if strings.TrimSpace(account.RefreshToken) == "" {
		// 仅有 access token 时做 probe
		token := account.BearerToken()
		if token == "" {
			return nil, fmt.Errorf("refreshToken is required")
		}
		baseURL := xaiBackend.ResolveChatBaseURL(snap, account)
		if err := probeModels(ctx, baseURL, token, proxyURL); err != nil {
			return nil, err
		}
		account.Status = xaiShared.XaiStatusValid
		saved, err := pool.UpsertAccount(*account)
		if err != nil {
			return nil, err
		}
		return json.Marshal(map[string]any{
			"success": true,
			"account": accountToDTO(*saved, pool.ActiveAccountID()),
		})
	}

	// tokenEndpoint 不按账号存储，走 OIDC discovery；与请求链路和定时任务共享账号级 singleflight。
	updated, err := refreshOAuthAccount(ctx, pool, account, proxyURL, true)
	if err != nil {
		account.Status = xaiShared.XaiStatusUnknown
		_, _ = pool.UpsertAccount(*account)
		return nil, err
	}
	account = updated
	account.Status = xaiShared.XaiStatusValid
	account.AuthKind = xaiShared.AuthKindOAuth
	saved, err := pool.UpsertAccount(*account)
	if err != nil {
		return nil, err
	}
	return json.Marshal(map[string]any{
		"success": true,
		"account": accountToDTO(*saved, pool.ActiveAccountID()),
	})
}

func probeModels(ctx context.Context, baseURL, token, proxyURL string) error {
	if ctx == nil {
		ctx = context.Background()
	}
	url := strings.TrimRight(baseURL, "/") + "/models"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")
	// cli-chat-proxy：附加 CLI 身份头
	if xaiBackend.IsCLIChatProxyBaseURL(baseURL) {
		req.Header.Set(xaiBackend.HeaderClientVersion, xaiBackend.DefaultClientVersion)
		req.Header.Set("User-Agent", xaiBackend.DefaultUserAgent)
		req.Header.Set(xaiBackend.HeaderTokenAuth, xaiBackend.DefaultTokenAuth)
	}
	client := executor.NewHTTPClientForcedProxyURL(proxyURL, 20*time.Second)
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("probe failed (HTTP %d): %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return nil
}

// ProbeAccountStream 对该账号发起 responses SSE 探测
func (d *desktopFacade) ProbeAccountStream(ctx context.Context, configPath, accountID string) (json.RawMessage, error) {
	accountID = strings.TrimSpace(accountID)
	if accountID == "" {
		return nil, fmt.Errorf("accountId is required")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	pool, err := ensurePool(configPath)
	if err != nil {
		return nil, err
	}
	account := findAccount(pool, accountID)
	if account == nil {
		return nil, fmt.Errorf("account not found: %s", accountID)
	}
	snap := pool.Snapshot()
	proxyURL := resolveAccountProxy(pool, account)

	// 确保 access token 可用
	token, err := ensureAccessToken(ctx, pool, account, proxyURL)
	if err != nil {
		return json.Marshal(map[string]any{"success": false, "error": err.Error()})
	}
	// ensureAccessToken 可能已刷新账号
	if refreshed := findAccount(pool, accountID); refreshed != nil {
		account = refreshed
	}

	baseURL := xaiBackend.ResolveChatBaseURL(snap, account)
	url := strings.TrimRight(baseURL, "/") + "/responses"
	conv := fmt.Sprintf("probe-%d", time.Now().UnixNano())
	bodyObj := map[string]any{
		"model":            "grok-4.5",
		"stream":           true,
		"input":            "Say hi in one word.",
		"prompt_cache_key": conv,
	}
	bodyBytes, err := json.Marshal(bodyObj)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, strings.NewReader(string(bodyBytes)))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set(xaiBackend.HeaderGrokConvID, conv)
	// CLI 身份头：usingApi=false + chat-proxy
	if !account.UsingAPIEnabled() && xaiBackend.IsCLIChatProxyBaseURL(baseURL) {
		authKind := strings.TrimSpace(account.AuthKind)
		version := xaiBackend.DefaultClientVersion
		tokenAuth := xaiBackend.DefaultTokenAuth
		userAgent := xaiBackend.DefaultUserAgent
		if snap != nil {
			if v := strings.TrimSpace(snap.Config.ClientVersion); v != "" {
				version = v
				userAgent = "xai-grok-workspace/" + v
			}
			if v := strings.TrimSpace(snap.Config.TokenAuth); v != "" {
				tokenAuth = v
			}
			if v := strings.TrimSpace(snap.Config.UserAgent); v != "" {
				userAgent = v
			}
		}
		if authKind == "" || authKind == xaiShared.AuthKindOAuth {
			req.Header.Set(xaiBackend.HeaderTokenAuth, tokenAuth)
		}
		req.Header.Set(xaiBackend.HeaderClientVersion, version)
		req.Header.Set("User-Agent", userAgent)
	}

	// 总超时 45s；SSE 拿到首批事件后立即结束
	client := executor.NewHTTPClientForcedProxyURL(proxyURL, 45*time.Second)
	resp, err := client.Do(req)
	if err != nil {
		return json.Marshal(map[string]any{"success": false, "error": err.Error()})
	}
	defer resp.Body.Close()

	raw, readErr := readSSEProbeBody(resp.Body, 64*1024, 20*time.Second)
	text := string(raw)
	preview := text
	if len(preview) > 240 {
		preview = preview[:240] + "…"
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return json.Marshal(map[string]any{
			"success":    false,
			"statusCode": resp.StatusCode,
			"error":      fmt.Sprintf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(preview)),
			"detail":     preview,
		})
	}
	hasEvent := strings.Contains(text, "data:") || strings.Contains(text, "response.")
	if !hasEvent {
		errDetail := "response missing SSE events"
		if readErr != nil {
			errDetail = errDetail + ": " + readErr.Error()
		}
		return json.Marshal(map[string]any{
			"success":    false,
			"statusCode": resp.StatusCode,
			"error":      errDetail,
			"detail":     preview,
		})
	}
	pool.ReportSuccess(account.ID)
	return json.Marshal(map[string]any{
		"success":    true,
		"statusCode": resp.StatusCode,
		"detail":     fmt.Sprintf("sse_len=%d has_event=true", len(raw)),
		"account":    accountToDTO(*account, pool.ActiveAccountID()),
	})
}

// readSSEProbeBody 读取 SSE 直到出现事件、达到上限或超时。
func readSSEProbeBody(r io.Reader, maxBytes int, idleTimeout time.Duration) ([]byte, error) {
	if maxBytes <= 0 {
		maxBytes = 64 * 1024
	}
	type chunk struct {
		b   []byte
		err error
	}
	ch := make(chan chunk, 1)
	go func() {
		buf := make([]byte, 0, 4096)
		tmp := make([]byte, 2048)
		for len(buf) < maxBytes {
			n, err := r.Read(tmp)
			if n > 0 {
				remain := maxBytes - len(buf)
				if n > remain {
					n = remain
				}
				buf = append(buf, tmp[:n]...)
				if strings.Contains(string(buf), "data:") || strings.Contains(string(buf), "response.") {
					ch <- chunk{b: buf, err: nil}
					return
				}
			}
			if err != nil {
				ch <- chunk{b: buf, err: err}
				return
			}
			if len(buf) >= maxBytes {
				ch <- chunk{b: buf, err: nil}
				return
			}
		}
		ch <- chunk{b: buf, err: nil}
	}()
	timer := time.NewTimer(idleTimeout)
	defer timer.Stop()
	select {
	case c := <-ch:
		return c.b, c.err
	case <-timer.C:
		return nil, fmt.Errorf("sse probe timeout after %s", idleTimeout)
	}
}

func findAccount(pool *xai.XaiAccountPool, id string) *xaiShared.XaiAccount {
	if pool == nil {
		return nil
	}
	for _, acc := range pool.ListAccounts() {
		if strings.TrimSpace(acc.ID) == strings.TrimSpace(id) {
			cp := acc
			return &cp
		}
	}
	return nil
}

func dtoToAccount(dto map[string]any) xaiShared.XaiAccount {
	account := xaiShared.XaiAccount{
		ID:            strings.TrimSpace(asString(dto["id"])),
		Email:         strings.TrimSpace(asString(dto["email"])),
		Subject:       strings.TrimSpace(asString(dto["subject"])),
		AccessToken:   strings.TrimSpace(asString(dto["accessToken"])),
		RefreshToken:  strings.TrimSpace(asString(dto["refreshToken"])),
		IDToken:       strings.TrimSpace(asString(dto["idToken"])),
		AuthKind:      strings.TrimSpace(asString(dto["authKind"])),
		APIKey:        strings.TrimSpace(asString(dto["apiKey"])),
		SSO:           strings.TrimSpace(asString(dto["sso"])),
		ProxyUrl:      strings.TrimSpace(asString(dto["proxyUrl"])),
		CustomHeaders: asStringMap(dto["customHeaders"]),
		Enabled:       asBool(dto["enabled"], true),
		// 引入账号默认开启 websockets
		Websockets:    xaiShared.BoolPtr(asBool(dto["websockets"], true)),
		Weight:        asInt(dto["weight"]),
		Pool:          strings.TrimSpace(asString(dto["pool"])),
		Status:        xaiShared.XaiAccountStatus(strings.TrimSpace(asString(dto["status"]))),
		ExpiresAt:     parseTimeString(asString(dto["expiresAt"])),
		LastRefresh:   parseTimeString(asString(dto["lastRefresh"])),
		LastQuotaSync: parseTimeString(asString(dto["lastQuotaSync"])),
		Quota:         parseQuota(dto["quota"]),
	}
	if _, ok := dto["usingApi"]; ok {
		account.SetUsingAPI(asBool(dto["usingApi"], account.UsingAPIEnabled()))
	}
	return account
}

func parseQuota(v any) *xaiShared.XaiQuota {
	if v == nil {
		return nil
	}
	raw, err := json.Marshal(v)
	if err != nil {
		return nil
	}
	var q xaiShared.XaiQuota
	if err := json.Unmarshal(raw, &q); err != nil {
		return nil
	}
	if q.Auto == nil && q.Fast == nil && q.Expert == nil && q.Heavy == nil && q.Grok43 == nil {
		return nil
	}
	return &q
}

func asStringMap(v any) map[string]string {
	if v == nil {
		return nil
	}
	switch t := v.(type) {
	case map[string]string:
		if len(t) == 0 {
			return nil
		}
		out := make(map[string]string, len(t))
		for k, val := range t {
			k = strings.TrimSpace(k)
			val = strings.TrimSpace(val)
			if k != "" && val != "" {
				out[k] = val
			}
		}
		if len(out) == 0 {
			return nil
		}
		return out
	case map[string]any:
		out := make(map[string]string, len(t))
		for k, val := range t {
			k = strings.TrimSpace(k)
			s := strings.TrimSpace(asString(val))
			if k != "" && s != "" {
				out[k] = s
			}
		}
		if len(out) == 0 {
			return nil
		}
		return out
	default:
		return nil
	}
}

func asString(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case fmt.Stringer:
		return t.String()
	case nil:
		return ""
	default:
		return fmt.Sprintf("%v", t)
	}
}

func asBool(v any, fallback bool) bool {
	switch t := v.(type) {
	case bool:
		return t
	case string:
		switch strings.ToLower(strings.TrimSpace(t)) {
		case "true", "1", "yes":
			return true
		case "false", "0", "no":
			return false
		}
	case float64:
		return t != 0
	}
	return fallback
}

func asInt(v any) int {
	switch t := v.(type) {
	case int:
		return t
	case int64:
		return int(t)
	case float64:
		return int(t)
	case json.Number:
		i, _ := t.Int64()
		return int(i)
	case string:
		var n int
		_, _ = fmt.Sscanf(strings.TrimSpace(t), "%d", &n)
		return n
	default:
		return 0
	}
}
