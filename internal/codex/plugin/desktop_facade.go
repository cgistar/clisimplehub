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
	"clisimplehub/internal/executor"
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
			"activeAccountId":    "",
			"accounts":           []any{},
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
	isActive := active.AccountID != "" && active.AccountID == mc.ActiveAccountID
	return json.Marshal(codexShared.MarshalAccountForFrontend(active, isActive))
}

func (d *desktopFacade) SetActiveAccount(configPath, accountId string) error {
	accountId = strings.TrimSpace(accountId)
	if accountId == "" {
		return fmt.Errorf("accountId is required")
	}
	mc, err := codexShared.LoadCodexMultiConfig(configPath)
	if err != nil {
		return err
	}
	account := mc.FindAccountByAccountID(accountId)
	if account == nil {
		return fmt.Errorf("account not found")
	}
	mc.ActiveAccountID = account.AccountID
	mc.ActiveRefreshToken = account.RefreshToken
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

	// Trim all string fields
	dto.RefreshToken = strings.TrimSpace(dto.RefreshToken)
	dto.AccessToken = strings.TrimSpace(dto.AccessToken)
	dto.IDToken = strings.TrimSpace(dto.IDToken)
	dto.AccountID = strings.TrimSpace(dto.AccountID)
	dto.Email = strings.TrimSpace(dto.Email)
	dto.PlanType = strings.TrimSpace(dto.PlanType)
	dto.ProxyUrl = strings.TrimSpace(dto.ProxyUrl)

	// Allow temporary accounts with only accessToken (no refreshToken)
	if dto.RefreshToken == "" && dto.AccessToken == "" {
		return nil, fmt.Errorf("either refreshToken or accessToken is required")
	}
	// AccountID is now required for new accounts
	if dto.AccountID == "" {
		return nil, fmt.Errorf("accountId is required")
	}

	mc, err := codexShared.LoadCodexMultiConfig(configPath)
	if err != nil {
		if !os.IsNotExist(err) {
			return nil, err
		}
		mc = &codexShared.CodexMultiConfig{}
	}

	// Check duplicate by AccountID or RefreshToken
	if mc.FindAccountByAccountID(dto.AccountID) != nil {
		return nil, fmt.Errorf("account with this accountId already exists")
	}
	if dto.RefreshToken != "" && mc.FindAccountByRefreshToken(dto.RefreshToken) != nil {
		return nil, fmt.Errorf("account with this refreshToken already exists")
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
	if mc.ActiveAccountID == "" {
		mc.ActiveAccountID = account.AccountID
		mc.ActiveRefreshToken = account.RefreshToken
	}

	if err := codexShared.SaveCodexMultiConfig(configPath, mc); err != nil {
		return nil, err
	}
	if pool := codex.GetPool(); pool != nil {
		pool.Reload()
	}

	isActive := account.AccountID == mc.ActiveAccountID
	return json.Marshal(codexShared.MarshalAccountForFrontend(&account, isActive))
}

func (d *desktopFacade) UpdateAccount(configPath string, dtoJSON json.RawMessage) error {
	var dto struct {
		AccountID    string `json:"accountId"`
		RefreshToken string `json:"refreshToken"`
		AccessToken  string `json:"accessToken"`
		IDToken      string `json:"idToken"`
		ExpiresAt    string `json:"expiresAt"`
		Email        string `json:"email"`
		PlanType     string `json:"planType"`
		ProxyUrl     string `json:"proxyUrl"`
		Weight       int    `json:"weight"`
		Status       string `json:"status"`
	}
	if err := json.Unmarshal(dtoJSON, &dto); err != nil {
		return err
	}
	dto.AccountID = strings.TrimSpace(dto.AccountID)
	if dto.AccountID == "" {
		return fmt.Errorf("accountId is required")
	}

	mc, err := codexShared.LoadCodexMultiConfig(configPath)
	if err != nil {
		return err
	}

	account := mc.FindAccountByAccountID(dto.AccountID)
	if account == nil {
		return fmt.Errorf("account not found")
	}

	// Update OAuth tokens if provided (for OAuth callback updates)
	oauthUpdated := false
	if dto.RefreshToken != "" {
		account.RefreshToken = strings.TrimSpace(dto.RefreshToken)
		oauthUpdated = true
	}
	if dto.AccessToken != "" {
		account.AccessToken = strings.TrimSpace(dto.AccessToken)
		oauthUpdated = true
	}
	if dto.IDToken != "" {
		account.IDToken = strings.TrimSpace(dto.IDToken)
		oauthUpdated = true
	}
	if dto.ExpiresAt != "" {
		if t, err := time.Parse(time.RFC3339, dto.ExpiresAt); err == nil {
			account.ExpiresAt = t
			oauthUpdated = true
		}
	}

	// Update account info
	if dto.Email != "" {
		account.Email = dto.Email
	}
	if dto.PlanType != "" {
		account.PlanType = dto.PlanType
	}
	account.ProxyUrl = dto.ProxyUrl
	account.Weight = dto.Weight

	// If OAuth tokens are updated, always reset status to valid
	if oauthUpdated {
		account.Status = codexShared.CodexStatusValid
	} else if dto.Status != "" {
		// Only update status manually if OAuth tokens are not being updated
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

func (d *desktopFacade) DeleteAccount(configPath, accountId string) error {
	accountId = strings.TrimSpace(accountId)
	if accountId == "" {
		return fmt.Errorf("accountId is required")
	}
	mc, err := codexShared.LoadCodexMultiConfig(configPath)
	if err != nil {
		return err
	}
	// Find and delete by AccountID
	found := false
	for i := range mc.Accounts {
		if strings.TrimSpace(mc.Accounts[i].AccountID) == accountId {
			mc.Accounts = append(mc.Accounts[:i], mc.Accounts[i+1:]...)
			// Update active account if deleted
			if mc.ActiveAccountID == accountId {
				if len(mc.Accounts) > 0 {
					mc.ActiveAccountID = mc.Accounts[0].AccountID
					mc.ActiveRefreshToken = mc.Accounts[0].RefreshToken
				} else {
					mc.ActiveAccountID = ""
					mc.ActiveRefreshToken = ""
				}
			}
			found = true
			break
		}
	}
	if !found {
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

func (d *desktopFacade) TestAccount(configPath, accountId string) (json.RawMessage, error) {
	accountId = strings.TrimSpace(accountId)
	if accountId == "" {
		return nil, fmt.Errorf("accountId is required")
	}
	mc, err := codexShared.LoadCodexMultiConfig(configPath)
	if err != nil {
		return nil, err
	}

	account := mc.FindAccountByAccountID(accountId)
	if account == nil {
		return nil, fmt.Errorf("account not found")
	}

	// If this is a temporary account (no refreshToken), just validate accessToken exists
	if account.RefreshToken == "" {
		if account.AccessToken == "" {
			return nil, fmt.Errorf("temporary account has no access token")
		}
		result := map[string]any{
			"accessToken": account.AccessToken,
			"accountId":   account.AccountID,
			"email":       account.Email,
			"planType":    account.PlanType,
			"expiresAt":   account.ExpiresAt.Format(time.RFC3339),
			"temporary":   true,
		}
		return json.Marshal(result)
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

	accessToken, idToken, accountID, email, planType, expiresAt, err := codexAuth.RefreshAndTest(account.RefreshToken, proxyURL, configPath)
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

func (d *desktopFacade) GetAccountUsage(ctx context.Context, configPath, accountId string) (json.RawMessage, error) {
	accountId = strings.TrimSpace(accountId)
	if accountId == "" {
		return nil, fmt.Errorf("accountId is required")
	}

	mc, err := codexShared.LoadCodexMultiConfig(configPath)
	if err != nil {
		return nil, fmt.Errorf("failed to load codex config: %w", err)
	}

	account := mc.FindAccountByAccountID(accountId)
	if account == nil {
		return nil, fmt.Errorf("account not found")
	}

	// Resolve proxy URL (global proxy has highest priority)
	effectiveProxyURL := ""
	if gp := plugin.GetGlobalProxyProviderCached(); gp != nil {
		if gpURL := gp.GetGlobalProxyURL(); gpURL != "" {
			effectiveProxyURL = gpURL
		}
	}
	if effectiveProxyURL == "" {
		effectiveProxyURL = strings.TrimSpace(account.ProxyUrl)
	}
	if effectiveProxyURL == "" {
		effectiveProxyURL = strings.TrimSpace(mc.ProxyUrl)
	}

	// Check if accessToken is expired or missing
	needsRefresh := false
	if strings.TrimSpace(account.AccessToken) == "" {
		needsRefresh = true
	} else if !account.ExpiresAt.IsZero() && time.Now().Add(codexAuth.TokenRefreshThreshold).After(account.ExpiresAt) {
		needsRefresh = true
	}

	// Refresh token if needed
	if needsRefresh {
		if account.RefreshToken == "" {
			return nil, fmt.Errorf("accessToken expired and no refreshToken available")
		}

		// Refresh the token
		accessToken, idToken, accountID, email, planType, expiresAt, err := codexAuth.RefreshAndTest(account.RefreshToken, effectiveProxyURL, configPath)
		if err != nil {
			return nil, fmt.Errorf("failed to refresh token: %w", err)
		}

		// Update account with new tokens
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
		account.UpdatedAt = time.Now()
		mc.UpdateAccount(account)
		_ = codexShared.SaveCodexMultiConfig(configPath, mc)
	}

	// Create HTTP client with proxy
	client := executor.NewHTTPClientForcedProxyURL(effectiveProxyURL, 10*time.Second)

	// Fetch usage
	usageQuery := codexAuth.UsageQuery{
		AccessToken: account.AccessToken,
		AccountID:   account.AccountID,
		UserAgent:   mc.GetUserAgent(),
		ProxyURL:    effectiveProxyURL,
	}

	usageResp, err := codexAuth.FetchUsage(ctx, client, usageQuery)
	if err != nil {
		// Handle specific error cases
		if strings.Contains(err.Error(), "HTTP 401") || strings.Contains(err.Error(), "HTTP 403") {
			account.Status = codexShared.CodexStatusBanned
			account.UpdatedAt = time.Now()
			mc.UpdateAccount(account)
			_ = codexShared.SaveCodexMultiConfig(configPath, mc)
		}
		return nil, err
	}

	// Update account with usage information
	usageResult := codexAuth.SimplifyUsageResponse(usageResp)
	if usageResult != nil {
		snapshot := &codexShared.CodexUsageSnapshot{
			UpdatedAt: time.Now(),
		}
		if usageResult.Primary != nil {
			snapshot.PrimaryUsedPercent = usageResult.Primary.UsedPercent
			snapshot.PrimaryResetAfterSeconds = usageResult.Primary.RemainingSeconds
			snapshot.PrimaryWindowMinutes = 300 // 5 hours default
		}
		if usageResult.Secondary != nil {
			snapshot.SecondaryUsedPercent = usageResult.Secondary.UsedPercent
			snapshot.SecondaryResetAfterSeconds = usageResult.Secondary.RemainingSeconds
			snapshot.SecondaryWindowMinutes = 10080 // 7 days default
		}
		account.CodexUsage = snapshot
	}

	// Update email and plan type if available
	if usageResp.Email != "" {
		account.Email = usageResp.Email
	}
	if usageResp.PlanType != "" {
		account.PlanType = usageResp.PlanType
	}

	account.UpdatedAt = time.Now()
	mc.UpdateAccount(account)
	_ = codexShared.SaveCodexMultiConfig(configPath, mc)

	// Reload pool
	if pool := codex.GetPool(); pool != nil {
		pool.Reload()
	}

	return json.Marshal(usageResult)
}
