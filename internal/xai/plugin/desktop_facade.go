package xaiplugin

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	xai "clisimplehub/internal/xai"
	xaiAuth "clisimplehub/internal/xai/auth"
	xaiShared "clisimplehub/internal/xai/shared"
	"clisimplehub/internal/executor"
	"clisimplehub/internal/plugin"
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
	} else if strings.TrimSpace(account.RefreshToken) == "" && strings.TrimSpace(account.AccessToken) == "" {
		return nil, fmt.Errorf("refreshToken or accessToken is required")
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
	}
	saved, err := pool.UpsertAccount(account)
	if err != nil {
		return nil, err
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
	if v, ok := dto["authKind"]; ok {
		if kind := strings.TrimSpace(asString(v)); kind != "" {
			account.AuthKind = kind
		}
	}
	if v, ok := dto["baseURL"]; ok {
		account.BaseURL = strings.TrimSpace(asString(v))
	}
	if v, ok := dto["tokenEndpoint"]; ok {
		account.TokenEndpoint = strings.TrimSpace(asString(v))
	}
	if v, ok := dto["redirectURI"]; ok {
		account.RedirectURI = strings.TrimSpace(asString(v))
	}
	if v, ok := dto["proxyUrl"]; ok {
		account.ProxyUrl = strings.TrimSpace(asString(v))
	}
	if v, ok := dto["enabled"]; ok {
		account.Enabled = asBool(v, account.Enabled)
	}
	if v, ok := dto["websockets"]; ok {
		account.Websockets = asBool(v, account.Websockets)
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
	snap := pool.Snapshot()
	if snap == nil {
		snap = &xaiShared.XaiMultiConfig{Config: xaiShared.DefaultXaiConfig()}
	}
	baseURL := strings.TrimSpace(snap.Config.BaseURL)
	if baseURL == "" {
		baseURL = xaiAuth.DefaultAPIBaseURL
	}
	return json.Marshal(map[string]any{
		"rotationMode":  snap.GetRotationMode(),
		"proxyUrl":      snap.ProxyUrl,
		"baseURL":       baseURL,
		"customHeaders": snap.Config.CustomHeaders,
	})
}

func (d *desktopFacade) SaveXaiGlobalConfig(configPath string, dtoJSON json.RawMessage) error {
	var dto struct {
		RotationMode  string            `json:"rotationMode"`
		ProxyUrl      string            `json:"proxyUrl"`
		BaseURL       string            `json:"baseURL"`
		CustomHeaders map[string]string `json:"customHeaders"`
	}
	if err := json.Unmarshal(dtoJSON, &dto); err != nil {
		return err
	}
	pool, err := ensurePool(configPath)
	if err != nil {
		return err
	}
	return pool.SaveGlobalConfig(dto.RotationMode, dto.ProxyUrl, xaiShared.XaiConfig{
		BaseURL:       strings.TrimSpace(dto.BaseURL),
		CustomHeaders: dto.CustomHeaders,
	})
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
	return json.Marshal(result)
}

func (d *desktopFacade) CancelLogin() error {
	svc := getService()
	if svc == nil {
		return fmt.Errorf("xai service not available")
	}
	svc.cancelLoginSession()
	return nil
}

func (d *desktopFacade) TestAccount(configPath, accountID string) (json.RawMessage, error) {
	return d.RefreshAccountToken(context.Background(), configPath, accountID)
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
		baseURL := account.EffectiveBaseURL(snap.Config)
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
		baseURL := account.EffectiveBaseURL(snap.Config)
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

	svc := xaiAuth.NewXAIAuth(proxyURL)
	td, err := svc.RefreshTokens(ctx, account.RefreshToken, account.TokenEndpoint)
	if err != nil {
		account.Status = xaiShared.XaiStatusUnknown
		_, _ = pool.UpsertAccount(*account)
		return nil, err
	}
	account.AccessToken = td.AccessToken
	if td.RefreshToken != "" {
		account.RefreshToken = td.RefreshToken
	}
	if td.IDToken != "" {
		account.IDToken = td.IDToken
	}
	if td.Email != "" {
		account.Email = td.Email
	}
	if td.Subject != "" {
		account.Subject = td.Subject
	}
	if td.Expire != "" {
		account.ExpiresAt = parseTimeString(td.Expire)
	}
	account.LastRefresh = time.Now().UTC()
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
		BaseURL:       strings.TrimSpace(asString(dto["baseURL"])),
		TokenEndpoint: strings.TrimSpace(asString(dto["tokenEndpoint"])),
		RedirectURI:   strings.TrimSpace(asString(dto["redirectURI"])),
		ProxyUrl:      strings.TrimSpace(asString(dto["proxyUrl"])),
		Enabled:       asBool(dto["enabled"], true),
		Websockets:    asBool(dto["websockets"], false),
		Weight:        asInt(dto["weight"]),
		Status:        xaiShared.XaiAccountStatus(strings.TrimSpace(asString(dto["status"]))),
		ExpiresAt:     parseTimeString(asString(dto["expiresAt"])),
		LastRefresh:   parseTimeString(asString(dto["lastRefresh"])),
	}
	return account
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
