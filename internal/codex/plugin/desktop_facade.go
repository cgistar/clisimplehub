package codexplugin

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	codex "clisimplehub/internal/codex"
	codexAuth "clisimplehub/internal/codex/auth"
	"clisimplehub/internal/codex/auth/mailprovider"
	codexBackend "clisimplehub/internal/codex/backend"
	codexShared "clisimplehub/internal/codex/shared"
	"clisimplehub/internal/executor"
	"clisimplehub/internal/plugin"

	"github.com/google/uuid"
)

type desktopFacade struct{}

var _ plugin.CodexDesktopProvider = (*desktopFacade)(nil)

func (d *desktopFacade) DefaultMultiConfigBasename() string {
	return codexShared.GetDefaultCodexMultiConfigPath()
}

func getStore() codexShared.CodexAccountStore {
	pool := codex.GetPool()
	if pool == nil {
		return nil
	}
	return pool.Store()
}

func (d *desktopFacade) GetAccounts(configPath string) (json.RawMessage, error) {
	store := getStore()
	if store == nil {
		return json.Marshal(map[string]any{
			"activeAccountId": "",
			"accounts":        []any{},
		})
	}

	accounts, err := store.ListAccounts(context.Background())
	if err != nil {
		return nil, err
	}

	mc, _ := codexShared.LoadCodexMultiConfig(configPath)
	activeID := ""
	if mc != nil {
		activeID = mc.ActiveAccountID
	}

	statsByAccount := map[string]codexShared.CodexAccountStatsSummary{}
	costByAccount := map[string]*float64{}
	if len(accounts) > 0 {
		accountIDs := make([]string, 0, len(accounts))
		for i := range accounts {
			accountIDs = append(accountIDs, accounts[i].ID)
		}
		if statsMap, err := store.GetStatsSummaryMap(context.Background(), accountIDs, "today"); err == nil && statsMap != nil {
			statsByAccount = statsMap
		}
		if costMap, err := store.GetTodayEstimatedCostMap(context.Background(), accountIDs); err == nil && costMap != nil {
			costByAccount = costMap
		}
	}

	for i := range accounts {
		if summary, ok := statsByAccount[accounts[i].ID]; ok {
			accounts[i].TodayRequests = summary.RequestCount
			accounts[i].TodayTokens = summary.TotalTokens
			accounts[i].TodayCachedTokens = summary.CachedTokens
			accounts[i].TodayReasoningTokens = summary.ReasoningTokens
		}
		accounts[i].TodayEstimatedCost = costByAccount[accounts[i].ID]
	}

	return codexShared.MarshalAccountsResponse(activeID, accounts)
}

func (d *desktopFacade) GetAccountsPage(configPath string, offset, limit int) (json.RawMessage, error) {
	store := getStore()
	if store == nil {
		return json.Marshal(map[string]any{
			"activeAccountId": "",
			"accounts":        []any{},
			"offset":          0,
			"limit":           20,
			"nextOffset":      0,
			"total":           0,
			"hasMore":         false,
		})
	}

	if offset < 0 {
		offset = 0
	}
	if limit <= 0 {
		limit = 20
	}
	if limit > 200 {
		limit = 200
	}

	accounts, err := store.ListAccountsPage(context.Background(), offset, limit)
	if err != nil {
		return nil, err
	}
	total, err := store.CountAccounts(context.Background())
	if err != nil {
		return nil, err
	}

	mc, _ := codexShared.LoadCodexMultiConfig(configPath)
	activeID := ""
	if mc != nil {
		activeID = mc.ActiveAccountID
	}

	statsByAccount := map[string]codexShared.CodexAccountStatsSummary{}
	costByAccount := map[string]*float64{}
	if len(accounts) > 0 {
		accountIDs := make([]string, 0, len(accounts))
		for i := range accounts {
			accountIDs = append(accountIDs, accounts[i].ID)
		}
		if statsMap, err := store.GetStatsSummaryMap(context.Background(), accountIDs, "today"); err == nil && statsMap != nil {
			statsByAccount = statsMap
		}
		if costMap, err := store.GetTodayEstimatedCostMap(context.Background(), accountIDs); err == nil && costMap != nil {
			costByAccount = costMap
		}
	}

	list := make([]map[string]any, 0, len(accounts))
	for i := range accounts {
		a := &accounts[i]
		if summary, ok := statsByAccount[a.ID]; ok {
			a.TodayRequests = summary.RequestCount
			a.TodayTokens = summary.TotalTokens
			a.TodayCachedTokens = summary.CachedTokens
			a.TodayReasoningTokens = summary.ReasoningTokens
		}
		a.TodayEstimatedCost = costByAccount[a.ID]
		isActive := activeID != "" && strings.TrimSpace(a.ID) == strings.TrimSpace(activeID)
		list = append(list, codexShared.MarshalAccountForFrontend(a, isActive))
	}

	nextOffset := offset + len(accounts)
	hasMore := nextOffset < total

	return json.Marshal(map[string]any{
		"activeAccountId": activeID,
		"accounts":        list,
		"offset":          offset,
		"limit":           limit,
		"nextOffset":      nextOffset,
		"total":           total,
		"hasMore":         hasMore,
	})
}

func (d *desktopFacade) GetActiveAccount(configPath string) (json.RawMessage, error) {
	store := getStore()
	if store == nil {
		return json.RawMessage("null"), nil
	}

	mc, _ := codexShared.LoadCodexMultiConfig(configPath)
	if mc == nil || mc.ActiveAccountID == "" {
		return json.RawMessage("null"), nil
	}

	account, err := store.GetByID(context.Background(), mc.ActiveAccountID)
	if err != nil || account == nil {
		return json.RawMessage("null"), nil
	}

	return json.Marshal(codexShared.MarshalAccountForFrontend(account, true))
}

func (d *desktopFacade) SetActiveAccount(configPath, accountId string) error {
	accountId = strings.TrimSpace(accountId)
	if accountId == "" {
		return fmt.Errorf("accountId is required")
	}

	store := getStore()
	if store == nil {
		return fmt.Errorf("account store not initialized")
	}

	account, err := store.GetByID(context.Background(), accountId)
	if err != nil || account == nil {
		return fmt.Errorf("account not found: %s", accountId)
	}

	mc, _ := codexShared.LoadCodexMultiConfig(configPath)
	if mc == nil {
		mc = &codexShared.CodexMultiConfig{}
	}
	mc.ActiveAccountID = accountId
	if err := codexShared.SaveCodexMultiConfig(configPath, mc); err != nil {
		return err
	}

	if pool := codex.GetPool(); pool != nil {
		pool.Reload()
	}
	return nil
}

// AddAccount is overridden by CodexPlugin.AddAccount in plugin.go
func (d *desktopFacade) AddAccount(configPath string, dtoJSON json.RawMessage) (json.RawMessage, error) {
	return nil, fmt.Errorf("AddAccount must be called via CodexPlugin")
}

// ImportAccounts is overridden by CodexPlugin.ImportAccounts in plugin.go
func (d *desktopFacade) ImportAccounts(configPath string, dtoJSON json.RawMessage) (json.RawMessage, error) {
	return nil, fmt.Errorf("ImportAccounts must be called via CodexPlugin")
}

func (d *desktopFacade) UpdateAccount(configPath string, dtoJSON json.RawMessage) error {
	var dto struct {
		ID           string  `json:"id"`
		AccountID    string  `json:"accountId"`
		RefreshToken *string `json:"refreshToken,omitempty"`
		Email        *string `json:"email,omitempty"`
		PlanType     *string `json:"planType,omitempty"`
		Enabled      *bool   `json:"enabled,omitempty"`
		Websockets   *bool   `json:"websockets,omitempty"`
		Password     *string `json:"password,omitempty"`
		MFACode      *string `json:"mfaCode,omitempty"`
		ProxyUrl     *string `json:"proxyUrl,omitempty"`
		Weight       *int    `json:"weight,omitempty"`
		Status       *string `json:"status,omitempty"`
	}
	if err := json.Unmarshal(dtoJSON, &dto); err != nil {
		return err
	}

	dto.ID = strings.TrimSpace(dto.ID)
	if dto.ID == "" {
		return fmt.Errorf("id is required")
	}

	store := getStore()
	if store == nil {
		return fmt.Errorf("account store not initialized")
	}

	account, err := store.GetByID(context.Background(), dto.ID)
	if err != nil || account == nil {
		return fmt.Errorf("account not found: %s", dto.ID)
	}

	if dto.RefreshToken != nil {
		if refreshToken := strings.TrimSpace(*dto.RefreshToken); refreshToken != "" {
			account.RefreshToken = refreshToken
		}
	}
	if dto.Email != nil {
		if email := strings.TrimSpace(*dto.Email); email != "" {
			account.Email = email
		}
	}
	if dto.PlanType != nil {
		if planType := strings.TrimSpace(*dto.PlanType); planType != "" {
			account.PlanType = planType
		}
	}
	if dto.Enabled != nil {
		account.Enabled = *dto.Enabled
	}
	if dto.Websockets != nil {
		account.Websockets = *dto.Websockets
	}
	if dto.Password != nil {
		account.Password = *dto.Password
	}
	if dto.MFACode != nil {
		account.MFACode = strings.TrimSpace(*dto.MFACode)
	}
	if dto.ProxyUrl != nil {
		account.ProxyUrl = strings.TrimSpace(*dto.ProxyUrl)
	}
	if dto.Weight != nil && *dto.Weight > 0 {
		account.Weight = *dto.Weight
	}
	if dto.Status != nil {
		if status := strings.TrimSpace(*dto.Status); status != "" {
			account.Status = codexShared.CodexAccountStatus(status)
		}
	}

	if err := store.Update(context.Background(), account); err != nil {
		return err
	}

	if svc := getService(); svc != nil {
		svc.RemoveAuthManager(account.ID)
	}
	if pool := codex.GetPool(); pool != nil {
		pool.Reload()
	}
	return nil
}

func (d *desktopFacade) RestoreAccount(configPath, accountId string) error {
	accountId = strings.TrimSpace(accountId)
	if accountId == "" {
		return fmt.Errorf("accountId is required")
	}

	store := getStore()
	if store == nil {
		return fmt.Errorf("account store not initialized")
	}

	account, err := store.GetByID(context.Background(), accountId)
	if err != nil || account == nil {
		return fmt.Errorf("account not found: %s", accountId)
	}

	if err := store.UpdateCooldown(context.Background(), account.ID, time.Time{}, ""); err != nil {
		return fmt.Errorf("clear cooldown failed: %w", err)
	}
	if err := store.UpdateStatus(context.Background(), account.ID, codexShared.CodexStatusValid); err != nil {
		return fmt.Errorf("update account status failed: %w", err)
	}

	if svc := getService(); svc != nil {
		svc.RemoveAuthManager(account.ID)
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

	store := getStore()
	if store == nil {
		return fmt.Errorf("account store not initialized")
	}

	if err := store.Delete(context.Background(), accountId); err != nil {
		return err
	}

	if svc := getService(); svc != nil {
		svc.RemoveAuthManager(accountId)
	}
	mc, _ := codexShared.LoadCodexMultiConfig(configPath)
	if mc != nil && mc.ActiveAccountID == accountId {
		accounts, _ := store.ListAccounts(context.Background())
		if len(accounts) > 0 {
			mc.ActiveAccountID = accounts[0].ID
		} else {
			mc.ActiveAccountID = ""
		}
		_ = codexShared.SaveCodexMultiConfig(configPath, mc)
	}

	if pool := codex.GetPool(); pool != nil {
		pool.Reload()
	}
	return nil
}

func (d *desktopFacade) DeleteAccounts(configPath string, accountIDs []string) error {
	store := getStore()
	if store == nil {
		return fmt.Errorf("account store not initialized")
	}

	normalized := make([]string, 0, len(accountIDs))
	seen := make(map[string]struct{}, len(accountIDs))
	for _, id := range accountIDs {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		normalized = append(normalized, id)
	}
	if len(normalized) == 0 {
		return nil
	}

	if err := store.DeleteMany(context.Background(), normalized); err != nil {
		return err
	}

	if svc := getService(); svc != nil {
		for _, id := range normalized {
			svc.RemoveAuthManager(id)
		}
	}
	mc, _ := codexShared.LoadCodexMultiConfig(configPath)
	if mc != nil {
		activeID := strings.TrimSpace(mc.ActiveAccountID)
		if activeID != "" {
			for _, id := range normalized {
				if id == activeID {
					accounts, _ := store.ListAccounts(context.Background())
					if len(accounts) > 0 {
						mc.ActiveAccountID = accounts[0].ID
					} else {
						mc.ActiveAccountID = ""
					}
					_ = codexShared.SaveCodexMultiConfig(configPath, mc)
					break
				}
			}
		}
	}

	if pool := codex.GetPool(); pool != nil {
		pool.Reload()
	}
	return nil
}

func (d *desktopFacade) TestAccount(configPath, accountId string) (json.RawMessage, error) {
	accountId = strings.TrimSpace(accountId)
	if accountId == "" {
		return nil, fmt.Errorf("accountId is required")
	}

	store := getStore()
	if store == nil {
		return nil, fmt.Errorf("account store not initialized")
	}

	account, err := store.GetByID(context.Background(), accountId)
	if err != nil || account == nil {
		return nil, fmt.Errorf("account not found: %s", accountId)
	}

	proxyURL := strings.TrimSpace(account.ProxyUrl)
	if proxyURL == "" {
		mc, _ := codexShared.LoadCodexMultiConfig(configPath)
		if mc != nil {
			proxyURL = mc.ProxyUrl
		}
	}

	accessToken, idToken, _, email, planType, expiresAt, err := codexAuth.RefreshAndTest(account.RefreshToken, proxyURL, configPath)
	if err != nil {
		return nil, err
	}

	if err := store.UpdateTokens(context.Background(), account.ID, accessToken, idToken, account.RefreshToken, expiresAt); err != nil {
		return nil, fmt.Errorf("persist refreshed tokens failed: %w", err)
	}

	// Keep in-memory account aligned with refreshed token fields to avoid overwriting expires_at in later full updates.
	account.AccessToken = accessToken
	account.IDToken = idToken
	account.ExpiresAt = expiresAt
	if email != "" || planType != "" {
		account.Email = email
		account.PlanType = planType
		if err := store.Update(context.Background(), account); err != nil {
			return nil, fmt.Errorf("persist refreshed profile failed: %w", err)
		}
	}

	if err := store.UpdateCooldown(context.Background(), account.ID, time.Time{}, ""); err != nil {
		return nil, fmt.Errorf("clear cooldown failed: %w", err)
	}
	if err := store.UpdateStatus(context.Background(), account.ID, codexShared.CodexStatusValid); err != nil {
		return nil, fmt.Errorf("update account status failed: %w", err)
	}

	if svc := getService(); svc != nil {
		svc.RemoveAuthManager(account.ID)
	}
	if pool := codex.GetPool(); pool != nil {
		pool.Reload()
	}

	return json.Marshal(map[string]any{
		"accessToken": accessToken,
		"accountId":   account.AccountID,
		"email":       email,
		"planType":    planType,
		"expiresAt":   expiresAt.Format(time.RFC3339),
	})
}

func (d *desktopFacade) GetCodexGlobalConfig(configPath string) (json.RawMessage, error) {
	mc, err := codexShared.LoadCodexMultiConfig(configPath)
	if err != nil {
		mc = &codexShared.CodexMultiConfig{}
	}

	return json.Marshal(map[string]any{
		"rotationMode":  mc.GetRotationMode(),
		"proxyUrl":      mc.ProxyUrl,
		"baseURL":       mc.GetBaseURL(),
		"clientVersion": strings.TrimSpace(mc.Config.ClientVersion),
		"userAgent":     strings.TrimSpace(mc.Config.UserAgent),
		"originator":    strings.TrimSpace(mc.Config.Originator),
		"betaFeatures":  strings.TrimSpace(mc.Config.BetaFeatures),
		"customHeaders": codexShared.NormalizeCustomHeadersForStorage(mc.Config.CustomHeaders),
	})
}

func (d *desktopFacade) GetCodexModelPrices() (json.RawMessage, error) {
	store := getStore()
	if store == nil {
		return nil, fmt.Errorf("account store not initialized")
	}
	prices, err := store.ListModelPrices(context.Background())
	if err != nil {
		return nil, err
	}
	return json.Marshal(prices)
}

func (d *desktopFacade) SaveCodexModelPrices(dto json.RawMessage) (json.RawMessage, error) {
	if strings.TrimSpace(string(dto)) == "null" {
		return nil, fmt.Errorf("model prices must be an array")
	}
	var input []struct {
		Model            *string  `json:"model"`
		InputPer1M       *float64 `json:"inputPer1M"`
		CachedInputPer1M *float64 `json:"cachedInputPer1M"`
		CacheWritePer1M  *float64 `json:"cacheWritePer1M"`
		OutputPer1M      *float64 `json:"outputPer1M"`
	}
	if err := json.Unmarshal(dto, &input); err != nil {
		return nil, fmt.Errorf("invalid model prices: %w", err)
	}
	prices := make([]codexShared.CodexModelPrice, len(input))
	for i, price := range input {
		if price.Model == nil || price.InputPer1M == nil || price.CachedInputPer1M == nil || price.CacheWritePer1M == nil || price.OutputPer1M == nil {
			return nil, fmt.Errorf("model price at index %d is missing a required field", i)
		}
		prices[i] = codexShared.CodexModelPrice{
			Model:            *price.Model,
			InputPer1M:       *price.InputPer1M,
			CachedInputPer1M: *price.CachedInputPer1M,
			CacheWritePer1M:  *price.CacheWritePer1M,
			OutputPer1M:      *price.OutputPer1M,
		}
	}
	store := getStore()
	if store == nil {
		return nil, fmt.Errorf("account store not initialized")
	}
	saved, err := store.ReplaceModelPrices(context.Background(), prices)
	if err != nil {
		return nil, err
	}
	return json.Marshal(saved)
}

func (d *desktopFacade) SaveCodexGlobalConfig(configPath string, dtoJSON json.RawMessage) error {
	var dto struct {
		RotationMode  string             `json:"rotationMode"`
		ProxyUrl      string             `json:"proxyUrl"`
		BaseURL       string             `json:"baseURL"`
		ClientVersion string             `json:"clientVersion"`
		UserAgent     string             `json:"userAgent"`
		Originator    string             `json:"originator"`
		BetaFeatures  string             `json:"betaFeatures"`
		CustomHeaders *map[string]string `json:"customHeaders"`
	}
	if err := json.Unmarshal(dtoJSON, &dto); err != nil {
		return err
	}

	mc, _ := codexShared.LoadCodexMultiConfig(configPath)
	if mc == nil {
		mc = &codexShared.CodexMultiConfig{}
	}
	existingHeaders := mc.Config.CustomHeaders

	mc.RotationMode = dto.RotationMode
	mc.ProxyUrl = dto.ProxyUrl
	mc.Config = codexShared.NormalizeCodexConfigForStorage(codexShared.CodexConfig{
		BaseURL:       dto.BaseURL,
		ClientVersion: dto.ClientVersion,
		UserAgent:     dto.UserAgent,
		Originator:    dto.Originator,
		BetaFeatures:  dto.BetaFeatures,
	})
	if dto.CustomHeaders != nil {
		mc.Config.CustomHeaders = codexShared.NormalizeCustomHeadersForStorage(*dto.CustomHeaders)
	} else {
		mc.Config.CustomHeaders = existingHeaders
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
	return json.Marshal(map[string]string{
		"refreshToken": result.RefreshToken,
		"accessToken":  result.AccessToken,
		"idToken":      result.IDToken,
		"accountId":    result.AccountID,
		"email":        result.Email,
		"planType":     result.PlanType,
		"expiresAt":    result.ExpiresAt,
	})
}

func (d *desktopFacade) StartLoginWithURL(ctx context.Context, proxyURL string) (string, error) {
	svc := getService()
	if svc == nil {
		return "", fmt.Errorf("codex service not available")
	}

	// Defensive cleanup: if this process still owns an unfinished OAuth callback server,
	// close it before starting a new login session on the fixed redirect port.
	svc.cancelLoginSession()
	cancelWebUILoginSession()

	authURL, waitFn, cleanupFn, err := codexAuth.StartCodexLoginWithURL(ctx, proxyURL)
	if err != nil {
		// Retry once after cleanup to avoid startup race where 1455 is being released.
		if strings.Contains(err.Error(), fmt.Sprintf("port %d in use", codexAuth.OAuthPort)) {
			time.Sleep(150 * time.Millisecond)
			authURL, waitFn, cleanupFn, err = codexAuth.StartCodexLoginWithURL(ctx, proxyURL)
		}
	}
	if err != nil {
		return "", err
	}

	svc.storeLoginSession(waitFn, cleanupFn)
	return authURL, nil
}

func (d *desktopFacade) SubmitLoginCallbackURL(ctx context.Context, callbackURL string) error {
	return codexAuth.SubmitCallbackURL(ctx, callbackURL)
}

func (d *desktopFacade) WaitForLoginCallback(ctx context.Context) (json.RawMessage, error) {
	svc := getService()
	if svc == nil {
		return nil, fmt.Errorf("codex service not available")
	}

	waitFn, cleanupFn, sessionID := svc.popLoginSession()
	if waitFn == nil {
		return nil, fmt.Errorf("no login session")
	}
	defer func() {
		cleanupFn()
		svc.clearLoginSession(sessionID)
	}()

	result, err := waitFn()
	if err != nil {
		return nil, err
	}
	return json.Marshal(map[string]string{
		"refreshToken": result.RefreshToken,
		"accessToken":  result.AccessToken,
		"idToken":      result.IDToken,
		"accountId":    result.AccountID,
		"email":        result.Email,
		"planType":     result.PlanType,
		"expiresAt":    result.ExpiresAt,
	})
}

func (d *desktopFacade) CancelLogin() error {
	svc := getService()
	if svc == nil {
		return fmt.Errorf("codex service not available")
	}
	svc.cancelLoginSession()
	return nil
}

func (d *desktopFacade) GetAccountUsage(ctx context.Context, configPath, accountId string) (json.RawMessage, error) {
	return d.getAccountUsage(ctx, configPath, accountId, fetchCodexUsage)
}

func (d *desktopFacade) GetAccountPrimaryUsage(ctx context.Context, configPath, accountId string) (json.RawMessage, error) {
	return d.getAccountUsage(ctx, configPath, accountId, fetchCodexUsageFromHeaders)
}

func (d *desktopFacade) ConsumeAccountResetCredit(ctx context.Context, configPath, accountId, creditID string) (json.RawMessage, error) {
	accountId = strings.TrimSpace(accountId)
	if accountId == "" {
		return nil, fmt.Errorf("accountId is required")
	}
	creditID = strings.TrimSpace(creditID)
	if creditID == "" {
		return nil, fmt.Errorf("creditId is required")
	}

	accessToken, acctID, client, mc, mgr, err := d.resolveResetCreditsContext(ctx, configPath, accountId)
	if err != nil {
		return nil, err
	}

	redeemID := uuid.NewString()
	reset, err := codexAuth.PostCodexReset(ctx, client, codexAuth.ResetQuery{
		AccessToken: accessToken,
		AccountID:   acctID,
		UserAgent:   mc.Config.UserAgent,
		Originator:  mc.Config.Originator,
		RedeemID:    redeemID,
		CreditID:    creditID,
		ProxyURL:    "",
	})
	if err != nil && isCodexResetUnauthorized(err) {
		if refreshErr := mgr.ForceRefresh(); refreshErr != nil {
			return nil, fmt.Errorf("reset failed and token refresh failed: %w", refreshErr)
		}
		accessToken, acctID, client, _, _, refreshErr := d.resolveResetCreditsContext(ctx, configPath, accountId)
		if refreshErr != nil {
			return nil, fmt.Errorf("auth failed after token refresh: %w", refreshErr)
		}
		reset, err = codexAuth.PostCodexReset(ctx, client, codexAuth.ResetQuery{
			AccessToken: accessToken,
			AccountID:   acctID,
			UserAgent:   mc.Config.UserAgent,
			Originator:  mc.Config.Originator,
			RedeemID:    redeemID,
			CreditID:    creditID,
			ProxyURL:    "",
		})
	}
	if err != nil {
		return nil, err
	}

	return json.Marshal(reset)
}

// ListAccountResetCredits 调用 GET /wham/rate-limit-reset-credits 拉取可用重置次数列表。
func (d *desktopFacade) ListAccountResetCredits(ctx context.Context, configPath, accountId string) (json.RawMessage, error) {
	accountId = strings.TrimSpace(accountId)
	if accountId == "" {
		return nil, fmt.Errorf("accountId is required")
	}

	accessToken, acctID, client, mc, mgr, err := d.resolveResetCreditsContext(ctx, configPath, accountId)
	if err != nil {
		return nil, err
	}

	list, err := codexAuth.ListResetCredits(ctx, client, codexAuth.ResetQuery{
		AccessToken: accessToken,
		AccountID:   acctID,
		UserAgent:   mc.Config.UserAgent,
		Originator:  mc.Config.Originator,
	})
	if err != nil && isCodexResetUnauthorized(err) {
		if refreshErr := mgr.ForceRefresh(); refreshErr != nil {
			return nil, fmt.Errorf("list reset credits failed and token refresh failed: %w", refreshErr)
		}
		accessToken, acctID, client, mc, _, refreshErr := d.resolveResetCreditsContext(ctx, configPath, accountId)
		if refreshErr != nil {
			return nil, fmt.Errorf("auth failed after token refresh: %w", refreshErr)
		}
		list, err = codexAuth.ListResetCredits(ctx, client, codexAuth.ResetQuery{
			AccessToken: accessToken,
			AccountID:   acctID,
			UserAgent:   mc.Config.UserAgent,
			Originator:  mc.Config.Originator,
		})
	}
	if err != nil {
		return nil, err
	}

	return json.Marshal(list)
}

// resolveResetCreditsContext 解析账号 token / 代理 / 多端配置，供 reset-credits 的 list 与 consume 共用。
func (d *desktopFacade) resolveResetCreditsContext(ctx context.Context, configPath, accountId string) (accessToken, accountID string, client *http.Client, mc *codexShared.CodexMultiConfig, mgr *codexAuth.CodexAuthManager, err error) {
	store := getStore()
	if store == nil {
		err = fmt.Errorf("account store not initialized")
		return
	}

	account, getErr := store.GetByID(ctx, accountId)
	if getErr != nil || account == nil {
		err = fmt.Errorf("account not found: %s", accountId)
		return
	}

	proxyURL := strings.TrimSpace(account.ProxyUrl)
	mc, _ = codexShared.LoadCodexMultiConfig(configPath)
	if proxyURL == "" && mc != nil {
		proxyURL = mc.ProxyUrl
	}
	if mc == nil {
		mc = &codexShared.CodexMultiConfig{}
	}

	svc := getService()
	if svc == nil {
		err = fmt.Errorf("codex service not available")
		return
	}
	mgr = svc.GetOrCreateAuthManager(accountId, configPath, proxyURL)
	accessToken, accountID, tokenErr := mgr.GetAccessToken()
	if tokenErr != nil {
		if strings.TrimSpace(account.AccessToken) == "" {
			err = fmt.Errorf("auth failed: %v", tokenErr)
			return
		}
		accessToken = strings.TrimSpace(account.AccessToken)
		accountID = strings.TrimSpace(account.AccountID)
	}
	if strings.TrimSpace(accountID) == "" {
		accountID = strings.TrimSpace(account.AccountID)
	}

	client = executor.NewHTTPClientForcedProxyURL(proxyURL, 30*time.Second)
	return
}

func isCodexResetUnauthorized(err error) bool {
	if err == nil {
		return false
	}
	message := err.Error()
	return strings.Contains(message, "HTTP 401") || strings.Contains(message, "HTTP 403")
}

func (d *desktopFacade) getAccountUsage(ctx context.Context, configPath, accountId string, fetch func(context.Context, string, string, string, *codexShared.CodexMultiConfig) (*codexShared.CodexUsageSnapshot, string, error)) (json.RawMessage, error) {
	accountId = strings.TrimSpace(accountId)
	if accountId == "" {
		return nil, fmt.Errorf("accountId is required")
	}

	store := getStore()
	if store == nil {
		return nil, fmt.Errorf("account store not initialized")
	}

	account, err := store.GetByID(ctx, accountId)
	if err != nil || account == nil {
		return nil, fmt.Errorf("account not found: %s", accountId)
	}

	proxyURL := strings.TrimSpace(account.ProxyUrl)
	var mc *codexShared.CodexMultiConfig
	if proxyURL == "" {
		mc, _ = codexShared.LoadCodexMultiConfig(configPath)
		if mc != nil {
			proxyURL = mc.ProxyUrl
		}
	} else {
		mc, _ = codexShared.LoadCodexMultiConfig(configPath)
	}
	if mc == nil {
		mc = &codexShared.CodexMultiConfig{}
	}

	svc := getService()
	mgr := svc.GetOrCreateAuthManager(accountId, configPath, proxyURL)
	accessToken, acctID, err := mgr.GetAccessToken()
	if err != nil {
		if strings.TrimSpace(account.AccessToken) == "" {
			return nil, fmt.Errorf("auth failed: %v", err)
		}
		accessToken = strings.TrimSpace(account.AccessToken)
		acctID = strings.TrimSpace(account.AccountID)
	}
	if strings.TrimSpace(acctID) == "" {
		acctID = strings.TrimSpace(account.AccountID)
	}

	usage, planType, err := fetch(ctx, accessToken, acctID, proxyURL, mc)
	if err != nil {
		return nil, err
	}

	if usage != nil {
		pool := codex.GetPool()
		if pool != nil {
			pool.UpdateUsageSnapshot(accountId, usage)
		}
	}

	if planType != "" && planType != account.PlanType {
		account.PlanType = planType
		_ = store.Update(ctx, account)
		if pool := codex.GetPool(); pool != nil {
			pool.Reload()
		}
	}

	return json.Marshal(formatUsageResult(usage))
}

func formatUsageResult(usage *codexShared.CodexUsageSnapshot) map[string]any {
	if usage == nil {
		return map[string]any{}
	}
	result := map[string]any{
		"updatedAt": usage.UpdatedAt.Format(time.RFC3339),
	}
	if usage.PrimaryUsedPercent > 0 || usage.PrimaryResetAfterSeconds > 0 {
		resetAt, remaining := codexShared.ComputeResetMeta(usage.UpdatedAt, usage.PrimaryResetAfterSeconds)
		result["primary"] = map[string]any{
			"usedPercent":      usage.PrimaryUsedPercent,
			"windowMinutes":    usage.PrimaryWindowMinutes,
			"resetAt":          resetAt.Format(time.RFC3339),
			"remainingSeconds": remaining,
		}
	}
	if usage.SecondaryUsedPercent > 0 || usage.SecondaryResetAfterSeconds > 0 {
		resetAt, remaining := codexShared.ComputeResetMeta(usage.UpdatedAt, usage.SecondaryResetAfterSeconds)
		result["secondary"] = map[string]any{
			"usedPercent":      usage.SecondaryUsedPercent,
			"windowMinutes":    usage.SecondaryWindowMinutes,
			"resetAt":          resetAt.Format(time.RFC3339),
			"remainingSeconds": remaining,
		}
	}
	if usage.PrimaryOverSecondaryPercent > 0 {
		result["primaryOverSecondaryPercent"] = usage.PrimaryOverSecondaryPercent
	}
	if usage.ResetCreditsAvailableCount > 0 {
		result["resetCreditsAvailableCount"] = usage.ResetCreditsAvailableCount
	}
	return result
}

func (d *desktopFacade) GetCodexAccountStats(ctx context.Context, timeRange string) (json.RawMessage, error) {
	store := getStore()
	if store == nil {
		return json.Marshal([]any{})
	}
	summaries, err := store.GetAllStatsSummary(ctx, timeRange)
	if err != nil {
		return nil, err
	}
	return json.Marshal(summaries)
}

func (d *desktopFacade) StartHeadlessLogin(ctx context.Context, email, password, clientID, proxyURL string, onStep func(string)) (json.RawMessage, error) {
	// Backward compatibility: call the new method with empty provider params
	input := map[string]any{
		"email":          email,
		"password":       password,
		"clientId":       clientID,
		"proxyUrl":       proxyURL,
		"emailProvider":  "",
		"providerParams": map[string]string{},
	}
	rawReq, _ := json.Marshal(input)
	return d.StartHeadlessLoginWithProvider(ctx, rawReq, onStep)
}

func (d *desktopFacade) StartHeadlessLoginWithProvider(ctx context.Context, req json.RawMessage, onStep func(string)) (json.RawMessage, error) {
	var input struct {
		Email          string            `json:"email"`
		Password       string            `json:"password"`
		ClientID       string            `json:"clientId"`
		ProxyURL       string            `json:"proxyUrl"`
		EmailProvider  string            `json:"emailProvider"`
		ProviderParams map[string]string `json:"providerParams"`
	}
	if err := json.Unmarshal(req, &input); err != nil {
		return nil, fmt.Errorf("unmarshal request: %w", err)
	}

	// Create cancellable context
	ctx, cancel := context.WithCancel(ctx)

	// Store cancel function first
	headlessHolder.mu.Lock()
	headlessHolder.session = nil
	headlessHolder.cancelFunc = cancel
	headlessHolder.mu.Unlock()

	// Execute network operation WITHOUT holding lock
	session, err := codexAuth.StartHeadlessLogin(ctx, &codexAuth.HeadlessLoginRequest{
		Email:          input.Email,
		Password:       input.Password,
		ClientID:       input.ClientID,
		ProxyURL:       input.ProxyURL,
		EmailProvider:  input.EmailProvider,
		ProviderParams: input.ProviderParams,
		OnStep:         onStep,
	})

	if err != nil {
		headlessHolder.mu.Lock()
		headlessHolder.cancelFunc = nil
		headlessHolder.mu.Unlock()
		return nil, err
	}

	// Store session
	headlessHolder.mu.Lock()
	headlessHolder.session = session
	headlessHolder.mu.Unlock()

	return marshalHeadlessState(session), nil
}

func (d *desktopFacade) SubmitHeadlessOTP(ctx context.Context, code string) (json.RawMessage, error) {
	headlessHolder.mu.Lock()
	session := headlessHolder.session
	headlessHolder.mu.Unlock()

	if session == nil {
		return nil, fmt.Errorf("no headless login session")
	}

	err := session.SubmitOTP(ctx, code)
	if err != nil {
		return marshalHeadlessState(session), err
	}
	return marshalHeadlessState(session), nil
}

func (d *desktopFacade) CancelHeadlessLogin() error {
	headlessHolder.mu.Lock()
	cancel := headlessHolder.cancelFunc
	headlessHolder.session = nil
	headlessHolder.cancelFunc = nil
	headlessHolder.mu.Unlock()

	if cancel != nil {
		cancel()
	}
	return nil
}

// --- Signup ---

func (d *desktopFacade) StartSignup(ctx context.Context, req json.RawMessage, onStep func(string)) (json.RawMessage, error) {
	var input struct {
		EmailProvider  string            `json:"emailProvider"`
		ProviderParams map[string]string `json:"providerParams"`
		Email          string            `json:"email"`
		Password       string            `json:"password"`
		ClientID       string            `json:"clientId"`
		ProxyURL       string            `json:"proxyUrl"`
	}
	if err := json.Unmarshal(req, &input); err != nil {
		return nil, fmt.Errorf("parse signup request: %w", err)
	}

	ctx, cancel := context.WithCancel(ctx)

	signupHolder.mu.Lock()
	signupHolder.session = nil
	signupHolder.cancelFunc = cancel
	signupHolder.mu.Unlock()

	session, err := codexAuth.StartSignup(ctx, &codexAuth.SignupRequest{
		EmailProvider:  input.EmailProvider,
		ProviderParams: input.ProviderParams,
		Email:          input.Email,
		Password:       input.Password,
		ClientID:       input.ClientID,
		ProxyURL:       input.ProxyURL,
		OnStep:         onStep,
	})

	if err != nil {
		signupHolder.mu.Lock()
		signupHolder.cancelFunc = nil
		signupHolder.mu.Unlock()
		return nil, err
	}

	signupHolder.mu.Lock()
	signupHolder.session = session
	signupHolder.mu.Unlock()

	return marshalSignupState(session), nil
}

func (d *desktopFacade) SubmitSignupOTP(ctx context.Context, code string) (json.RawMessage, error) {
	signupHolder.mu.Lock()
	session := signupHolder.session
	signupHolder.mu.Unlock()

	if session == nil {
		return nil, fmt.Errorf("no signup session")
	}

	err := session.SubmitOTP(ctx, code)
	if err != nil {
		return marshalSignupState(session), err
	}
	return marshalSignupState(session), nil
}

func (d *desktopFacade) CancelSignup() error {
	signupHolder.mu.Lock()
	cancel := signupHolder.cancelFunc
	signupHolder.session = nil
	signupHolder.cancelFunc = nil
	signupHolder.mu.Unlock()

	if cancel != nil {
		cancel()
	}
	return nil
}

func (d *desktopFacade) GetEmailProviders() (json.RawMessage, error) {
	return json.Marshal(mailprovider.AvailableProviders())
}

func (d *desktopFacade) GenerateRandomEmail(provider string, paramsRaw json.RawMessage) (json.RawMessage, error) {
	var params map[string]string
	if err := json.Unmarshal(paramsRaw, &params); err != nil {
		return nil, fmt.Errorf("parse params: %w", err)
	}
	result, err := mailprovider.GenerateAndProvision(provider, params)
	if err != nil {
		return nil, err
	}
	return json.Marshal(result)
}

func (d *desktopFacade) FetchVerificationCode(ctx context.Context, req json.RawMessage) (json.RawMessage, error) {
	var input struct {
		EmailProvider  string            `json:"emailProvider"`
		ProviderParams map[string]string `json:"providerParams"`
		Email          string            `json:"email"`
		TimeoutSec     int               `json:"timeoutSec"`
	}
	if err := json.Unmarshal(req, &input); err != nil {
		return nil, fmt.Errorf("parse verification code request: %w", err)
	}
	if input.TimeoutSec <= 0 {
		input.TimeoutSec = 120
	}
	if input.ProviderParams == nil {
		input.ProviderParams = map[string]string{}
	}

	provider, err := mailprovider.NewProvider(input.EmailProvider)
	if err != nil {
		return nil, err
	}

	if err := prepareProviderForVerificationTest(provider, input.EmailProvider, input.ProviderParams, input.Email); err != nil {
		return nil, err
	}

	code, err := provider.FetchVerificationCode(ctx, input.ProviderParams, input.Email, input.TimeoutSec)
	if err != nil {
		return nil, err
	}
	return json.Marshal(map[string]string{"code": code})
}

func prepareProviderForVerificationTest(provider mailprovider.EmailProvider, providerName string, params map[string]string, email string) error {
	provider.RestoreState(params)

	switch providerName {
	case "outlook":
		_, _, err := provider.CreateEmail(params)
		return err
	case "duckmail":
		if params["_mail_token"] == "" {
			return fmt.Errorf("duckmail test requires generated mailbox state; click random generate first")
		}
		return nil
	case "gptmail":
		if strings.TrimSpace(email) == "" {
			return fmt.Errorf("gptmail test requires email")
		}
		return nil
	case "cloudflare":
		if params["_jwt"] != "" {
			return nil
		}
		if strings.TrimSpace(params["cf_worker_domain"]) == "" {
			return fmt.Errorf("cloudflare test requires worker domain")
		}
		if strings.TrimSpace(params["cf_admin_password"]) == "" {
			return fmt.Errorf("cloudflare test requires admin password")
		}
		return nil
	case "tempmail":
		if params["tempmail_forward_email"] == "" && params["_forward_email"] == "" {
			return fmt.Errorf("tempmail test requires forward email")
		}
		if params["tempmail_forward_email"] == "" {
			params["tempmail_forward_email"] = params["_forward_email"]
		}
		if params["_email"] == "" {
			params["_email"] = email
		}
		_, _, err := provider.CreateEmail(params)
		return err
	default:
		return fmt.Errorf("verification code test does not support provider: %s", providerName)
	}
}

func marshalSignupState(session *codexAuth.SignupSession) json.RawMessage {
	state := map[string]any{
		"state":   int(session.State()),
		"needOTP": session.State() == codexAuth.SignupNeedOTP,
	}
	if session.Error() != nil {
		state["error"] = session.Error().Error()
	}
	if session.Result() != nil {
		r := session.Result()
		state["password"] = r.Password
		state["result"] = map[string]string{
			"refreshToken": r.RefreshToken,
			"accessToken":  r.AccessToken,
			"idToken":      r.IDToken,
			"accountId":    r.AccountID,
			"email":        r.Email,
			"planType":     r.PlanType,
			"expiresAt":    r.ExpiresAt,
		}
	}
	raw, _ := json.Marshal(state)
	return raw
}

func marshalHeadlessState(session *codexAuth.HeadlessLoginSession) json.RawMessage {
	state := map[string]any{
		"state":   int(session.State()),
		"needOTP": session.State() == codexAuth.StateNeedOTP,
	}
	if session.Error() != nil {
		state["error"] = session.Error().Error()
	}
	if session.Result() != nil {
		r := session.Result()
		state["result"] = map[string]string{
			"refreshToken": r.RefreshToken,
			"accessToken":  r.AccessToken,
			"idToken":      r.IDToken,
			"accountId":    r.AccountID,
			"email":        r.Email,
			"planType":     r.PlanType,
			"expiresAt":    r.ExpiresAt,
		}
	}
	raw, _ := json.Marshal(state)
	return raw
}

// --- Login session management ---

var (
	loginSessionID uint64
	loginWaitFn    func() (*codexAuth.CodexLoginResult, error)
	loginCleanupFn func()

	headlessHolder headlessSessionHolder
	signupHolder   signupSessionHolder
)

type headlessSessionHolder struct {
	mu         sync.Mutex
	session    *codexAuth.HeadlessLoginSession
	cancelFunc context.CancelFunc
}

type signupSessionHolder struct {
	mu         sync.Mutex
	session    *codexAuth.SignupSession
	cancelFunc context.CancelFunc
}

func getService() *CodexService {
	p := plugin.ByName("codex-accounts")
	if p == nil {
		return nil
	}
	if cp, ok := p.(*CodexPlugin); ok {
		return cp.GetService()
	}
	return nil
}

func cancelWebUILoginSession() {
	webUILoginMu.Lock()
	cancel := webUILoginCancel
	cleanup := webUILoginCleanup
	webUILoginCancel = nil
	webUILoginCleanup = nil
	webUILoginGen++
	webUILoginMu.Unlock()

	if cancel != nil {
		cancel()
	}
	if cleanup != nil {
		cleanup()
	}
}

func (s *CodexService) storeLoginSession(waitFn func() (*codexAuth.CodexLoginResult, error), cleanupFn func()) {
	s.mu.Lock()
	oldCleanup := loginCleanupFn
	loginSessionID++
	loginWaitFn = waitFn
	loginCleanupFn = cleanupFn
	s.mu.Unlock()

	if oldCleanup != nil {
		oldCleanup()
	}
}

func (s *CodexService) popLoginSession() (func() (*codexAuth.CodexLoginResult, error), func(), uint64) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if loginWaitFn == nil {
		return nil, nil, 0
	}

	w, c := loginWaitFn, loginCleanupFn
	sessionID := loginSessionID
	loginWaitFn = nil
	return w, c, sessionID
}

func (s *CodexService) clearLoginSession(sessionID uint64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if loginSessionID != sessionID {
		return
	}
	loginCleanupFn = nil
}

func (s *CodexService) cancelLoginSession() {
	s.mu.Lock()
	cleanup := loginCleanupFn
	loginSessionID++
	loginWaitFn = nil
	loginCleanupFn = nil
	s.mu.Unlock()

	if cleanup != nil {
		cleanup()
	}
}

func fetchCodexUsage(ctx context.Context, accessToken, accountID, proxyURL string, config *codexShared.CodexMultiConfig) (*codexShared.CodexUsageSnapshot, string, error) {
	if config == nil {
		config = &codexShared.CodexMultiConfig{}
	}
	client := executor.NewHTTPClientForcedProxyURL(proxyURL, 30*time.Second)
	usage, err := codexAuth.FetchUsage(ctx, client, codexAuth.UsageQuery{
		AccessToken: accessToken,
		AccountID:   accountID,
		UserAgent:   config.Config.UserAgent,
		Originator:  config.Config.Originator,
	})
	if err != nil {
		return nil, "", err
	}
	if usage == nil {
		return nil, "", nil
	}

	snapshot := usageResponseToSnapshot(usage)
	return snapshot, usage.PlanType, nil
}

func usageResponseToSnapshot(usage *codexAuth.UsageResponse) *codexShared.CodexUsageSnapshot {
	if usage == nil {
		return nil
	}

	snapshot := &codexShared.CodexUsageSnapshot{
		UpdatedAt: time.Now(),
	}
	if usage.RateLimit != nil {
		if primary := usage.RateLimit.PrimaryWindow; primary != nil {
			snapshot.PrimaryUsedPercent = primary.UsedPercent
			snapshot.PrimaryResetAfterSeconds = primary.ResetAfterSeconds
			snapshot.PrimaryWindowMinutes = primary.LimitWindowSeconds / 60
		}
		if secondary := usage.RateLimit.SecondaryWindow; secondary != nil {
			snapshot.SecondaryUsedPercent = secondary.UsedPercent
			snapshot.SecondaryResetAfterSeconds = secondary.ResetAfterSeconds
			snapshot.SecondaryWindowMinutes = secondary.LimitWindowSeconds / 60
		}
	}
	if usage.RateLimitResetCredits != nil {
		snapshot.ResetCreditsAvailableCount = usage.RateLimitResetCredits.AvailableCount
		snapshot.ResetCreditsAvailable = true
	}

	if snapshot.PrimaryUsedPercent == 0 &&
		snapshot.PrimaryResetAfterSeconds == 0 &&
		snapshot.SecondaryUsedPercent == 0 &&
		snapshot.SecondaryResetAfterSeconds == 0 &&
		snapshot.ResetCreditsAvailableCount == 0 {
		return nil
	}
	return snapshot
}

func fetchCodexUsageFromHeaders(ctx context.Context, accessToken, accountID, proxyURL string, config *codexShared.CodexMultiConfig) (*codexShared.CodexUsageSnapshot, string, error) {
	if config == nil {
		config = &codexShared.CodexMultiConfig{}
	}

	body := []byte(`{"model":"gpt-5.6-luna","reasoning":{"effort":"medium"},"instructions":"You are Codex, based on GPT-5.","input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"Say 'OK' only"}]}],"stream":true,"store":false}`)
	req, _, _, _, _, err := codexBackend.Prepare(ctx, codexBackend.Request{
		Method:      http.MethodPost,
		Path:        "/v1/responses",
		Source:      codexBackend.SourceCodex,
		Model:       "gpt-5.6-luna",
		Body:        body,
		IsStreaming: true,
		Config:      config,
		AccessToken: accessToken,
		AccountID:   accountID,
	})
	if err != nil {
		return nil, "", fmt.Errorf("build request: %w", err)
	}

	client := executor.NewHTTPClientForcedProxyURL(proxyURL, 30*time.Second)
	resp, err := client.Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)

	if resp.StatusCode >= 400 {
		return nil, "", fmt.Errorf("upstream %d: %s", resp.StatusCode, string(respBody))
	}

	planType := resp.Header.Get("X-Codex-Plan-Type")
	snapshot := extractCodexUsageHeaders(resp.Header)
	return snapshot, planType, nil
}
