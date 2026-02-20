package codexplugin

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
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
	if len(accounts) > 0 {
		accountIDs := make([]string, 0, len(accounts))
		for i := range accounts {
			accountIDs = append(accountIDs, accounts[i].AccountID)
		}
		if statsMap, err := store.GetStatsSummaryMap(context.Background(), accountIDs, "today"); err == nil && statsMap != nil {
			statsByAccount = statsMap
		}
	}

	list := make([]map[string]any, 0, len(accounts))
	for i := range accounts {
		a := &accounts[i]
		if summary, ok := statsByAccount[a.AccountID]; ok {
			a.TodayRequests = summary.RequestCount
			a.TodayTokens = summary.TotalTokens
		}
		isActive := activeID != "" && strings.TrimSpace(a.AccountID) == strings.TrimSpace(activeID)
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

func (d *desktopFacade) UpdateAccount(configPath string, dtoJSON json.RawMessage) error {
	var dto struct {
		AccountID    string  `json:"accountId"`
		RefreshToken *string `json:"refreshToken,omitempty"`
		Email        *string `json:"email,omitempty"`
		PlanType     *string `json:"planType,omitempty"`
		Password     *string `json:"password,omitempty"`
		MFACode      *string `json:"mfaCode,omitempty"`
		ProxyUrl     *string `json:"proxyUrl,omitempty"`
		Weight       *int    `json:"weight,omitempty"`
		Status       *string `json:"status,omitempty"`
	}
	if err := json.Unmarshal(dtoJSON, &dto); err != nil {
		return err
	}

	dto.AccountID = strings.TrimSpace(dto.AccountID)
	if dto.AccountID == "" {
		return fmt.Errorf("accountId is required")
	}

	store := getStore()
	if store == nil {
		return fmt.Errorf("account store not initialized")
	}

	account, err := store.GetByID(context.Background(), dto.AccountID)
	if err != nil || account == nil {
		return fmt.Errorf("account not found: %s", dto.AccountID)
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

	mc, _ := codexShared.LoadCodexMultiConfig(configPath)
	if mc != nil && mc.ActiveAccountID == accountId {
		accounts, _ := store.ListAccounts(context.Background())
		if len(accounts) > 0 {
			mc.ActiveAccountID = accounts[0].AccountID
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

	_ = store.UpdateTokens(context.Background(), account.AccountID, accessToken, idToken, account.RefreshToken, expiresAt)
	if email != "" || planType != "" {
		account.Email = email
		account.PlanType = planType
		_ = store.Update(context.Background(), account)
	}

	_ = store.UpdateCooldown(context.Background(), account.AccountID, time.Time{}, "")
	_ = store.UpdateStatus(context.Background(), account.AccountID, codexShared.CodexStatusValid)

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

	return json.Marshal(map[string]string{
		"rotationMode":  mc.GetRotationMode(),
		"proxyUrl":      mc.ProxyUrl,
		"baseURL":       mc.Config.BaseURL,
		"clientVersion": mc.Config.ClientVersion,
		"userAgent":     mc.Config.UserAgent,
		"originator":    mc.Config.Originator,
	})
}

func (d *desktopFacade) SaveCodexGlobalConfig(configPath string, dtoJSON json.RawMessage) error {
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

	mc, _ := codexShared.LoadCodexMultiConfig(configPath)
	if mc == nil {
		mc = &codexShared.CodexMultiConfig{}
	}

	mc.RotationMode = dto.RotationMode
	mc.ProxyUrl = dto.ProxyUrl
	mc.Config.BaseURL = dto.BaseURL
	mc.Config.ClientVersion = dto.ClientVersion
	mc.Config.UserAgent = dto.UserAgent
	mc.Config.Originator = dto.Originator

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
	authURL, waitFn, cleanupFn, err := codexAuth.StartCodexLoginWithURL(ctx, proxyURL)
	if err != nil {
		return "", err
	}

	getService().storeLoginSession(waitFn, cleanupFn)
	return authURL, nil
}

func (d *desktopFacade) WaitForLoginCallback(ctx context.Context) (json.RawMessage, error) {
	waitFn, cleanupFn := getService().popLoginSession()
	if waitFn == nil {
		return nil, fmt.Errorf("no login session")
	}
	defer cleanupFn()

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

func (d *desktopFacade) GetAccountUsage(ctx context.Context, configPath, accountId string) (json.RawMessage, error) {
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
	if proxyURL == "" {
		mc, _ := codexShared.LoadCodexMultiConfig(configPath)
		if mc != nil {
			proxyURL = mc.ProxyUrl
		}
	}

	svc := getService()
	mgr := svc.GetOrCreateAuthManager(accountId, configPath, proxyURL)
	accessToken, acctID, err := mgr.GetAccessToken()
	if err != nil {
		return nil, fmt.Errorf("auth failed: %v", err)
	}

	usage, err := fetchCodexUsage(ctx, accessToken, acctID, proxyURL)
	if err != nil {
		return nil, err
	}

	if usage != nil {
		pool := codex.GetPool()
		if pool != nil {
			pool.UpdateUsageSnapshot(accountId, usage)
		}
	}

	return json.Marshal(formatUsageResult(usage))
}

func formatUsageResult(usage *codexShared.CodexUsageSnapshot) map[string]any {
	if usage == nil {
		return map[string]any{}
	}
	result := map[string]any{}
	if usage.PrimaryUsedPercent > 0 || usage.PrimaryResetAfterSeconds > 0 {
		_, remaining := codexShared.ComputeResetMeta(usage.UpdatedAt, usage.PrimaryResetAfterSeconds)
		result["primary"] = map[string]any{
			"usedPercent":      usage.PrimaryUsedPercent,
			"remainingSeconds": remaining,
		}
	}
	if usage.SecondaryUsedPercent > 0 || usage.SecondaryResetAfterSeconds > 0 {
		_, remaining := codexShared.ComputeResetMeta(usage.UpdatedAt, usage.SecondaryResetAfterSeconds)
		result["secondary"] = map[string]any{
			"usedPercent":      usage.SecondaryUsedPercent,
			"remainingSeconds": remaining,
		}
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

// --- Login session management ---

var (
	loginWaitFn    func() (*codexAuth.CodexLoginResult, error)
	loginCleanupFn func()
)

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

func (s *CodexService) storeLoginSession(waitFn func() (*codexAuth.CodexLoginResult, error), cleanupFn func()) {
	s.mu.Lock()
	defer s.mu.Unlock()
	loginWaitFn = waitFn
	loginCleanupFn = cleanupFn
}

func (s *CodexService) popLoginSession() (func() (*codexAuth.CodexLoginResult, error), func()) {
	s.mu.Lock()
	defer s.mu.Unlock()
	w, c := loginWaitFn, loginCleanupFn
	loginWaitFn = nil
	loginCleanupFn = nil
	return w, c
}

func fetchCodexUsage(ctx context.Context, accessToken, accountID, proxyURL string) (*codexShared.CodexUsageSnapshot, error) {
	client := executor.NewHTTPClientForcedProxyURL(proxyURL, 30*time.Second)
	resp, err := codexAuth.FetchUsage(ctx, client, codexAuth.UsageQuery{
		AccessToken: accessToken,
		AccountID:   accountID,
		ProxyURL:    proxyURL,
	})
	if err != nil {
		return nil, err
	}
	if resp == nil || resp.RateLimit == nil {
		return nil, nil
	}

	snapshot := &codexShared.CodexUsageSnapshot{
		UpdatedAt: time.Now(),
	}
	if resp.RateLimit.PrimaryWindow != nil {
		snapshot.PrimaryUsedPercent = resp.RateLimit.PrimaryWindow.UsedPercent
		snapshot.PrimaryResetAfterSeconds = resp.RateLimit.PrimaryWindow.ResetAfterSeconds
		snapshot.PrimaryWindowMinutes = resp.RateLimit.PrimaryWindow.LimitWindowSeconds / 60
	}
	if resp.RateLimit.SecondaryWindow != nil {
		snapshot.SecondaryUsedPercent = resp.RateLimit.SecondaryWindow.UsedPercent
		snapshot.SecondaryResetAfterSeconds = resp.RateLimit.SecondaryWindow.ResetAfterSeconds
		snapshot.SecondaryWindowMinutes = resp.RateLimit.SecondaryWindow.LimitWindowSeconds / 60
	}
	return snapshot, nil
}
