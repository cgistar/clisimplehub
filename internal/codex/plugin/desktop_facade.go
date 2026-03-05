package codexplugin

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	codex "clisimplehub/internal/codex"
	codexAuth "clisimplehub/internal/codex/auth"
	"clisimplehub/internal/codex/auth/mailprovider"
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

	if err := store.UpdateTokens(context.Background(), account.AccountID, accessToken, idToken, account.RefreshToken, expiresAt); err != nil {
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

	if err := store.UpdateCooldown(context.Background(), account.AccountID, time.Time{}, ""); err != nil {
		return nil, fmt.Errorf("clear cooldown failed: %w", err)
	}
	if err := store.UpdateStatus(context.Background(), account.AccountID, codexShared.CodexStatusValid); err != nil {
		return nil, fmt.Errorf("update account status failed: %w", err)
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
