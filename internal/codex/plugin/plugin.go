package codexplugin

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"time"

	codex "clisimplehub/internal/codex"
	codexAuth "clisimplehub/internal/codex/auth"
	codexShared "clisimplehub/internal/codex/shared"
	"clisimplehub/internal/dbconfig"
	"clisimplehub/internal/executor"
	"clisimplehub/internal/plugin"
	"clisimplehub/internal/storage"
	"clisimplehub/internal/transformer"

	"github.com/google/uuid"
)

func init() {
	plugin.Register(&CodexPlugin{})
}

type pluginStorageAccessor struct {
	store  storage.Storage
	reload func()
}

func (a *pluginStorageAccessor) GetStorage() storage.Storage { return a.store }
func (a *pluginStorageAccessor) TriggerReload() {
	if a.reload != nil {
		a.reload()
	}
}

type CodexPlugin struct {
	desktopFacade
	service       *CodexService
	codexJsonPath string
	accountStore  codexShared.CodexAccountStore
	mu            sync.RWMutex
}

func (p *CodexPlugin) Name() string { return "codex-accounts" }

func (p *CodexPlugin) GetService() *CodexService {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.service
}

func (p *CodexPlugin) GetAccountStore() codexShared.CodexAccountStore {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.accountStore
}

func (p *CodexPlugin) Init(cfg plugin.InitConfig) error {
	codexJsonPath := codexJsonPathFromConfig(cfg.ConfigPath)

	// Open CodexAccountStore using the same configured database as usage stats.
	dbCfg, err := dbconfig.Resolve(cfg.ConfigPath, cfg.ConfigGetter)
	var accountStore codexShared.CodexAccountStore
	if err != nil {
		fmt.Printf("[codex-plugin] Warning: failed to resolve account store config: %v\n", err)
	} else if store, err := codexShared.OpenCodexAccountStoreWithConfig(dbCfg); err != nil {
		// Non-fatal: log and continue without store.
		fmt.Printf("[codex-plugin] Warning: failed to open account store (%s): %v\n", dbconfig.DisplaySource(dbCfg), err)
	} else {
		accountStore = store
	}

	_ = codex.InitPool(codexJsonPath, accountStore)

	transformer.RegisterAvailability("codex", func() map[string][]string {
		return map[string][]string{
			"codex": {"openai/codex"},
			"chat":  {"openai/codex"},
		}
	})

	p.mu.Lock()
	p.codexJsonPath = codexJsonPath
	p.accountStore = accountStore
	p.service = NewCodexService()
	p.service.SetAccountStore(accountStore)
	p.service.enableUsageRefresh()
	p.mu.Unlock()

	if cfg.Storage != nil {
		p.service.SetStorageAccessor(&pluginStorageAccessor{
			store:  cfg.Storage,
			reload: cfg.TriggerReload,
		})
	}

	return nil
}

func (p *CodexPlugin) RegisterRoutes(r plugin.RouteRegistrar) {
	p.mu.RLock()
	svc := p.service
	p.mu.RUnlock()
	if svc == nil {
		return
	}
	r.HandleFunc("/codex", r.RequireAuth(p.handleCodexRoute))
	r.HandleFunc("/codex/*", r.RequireAuth(p.handleCodexRoute))
	r.HandleFunc("/v0/management/codex-auth-url", r.RequireAuth(p.handleAuthURL))
	r.HandleFunc("/v0/management/oauth-callback", r.RequireAuth(p.handleOAuthCallback))
}

func (p *CodexPlugin) HandleResponsesWebsocket(w http.ResponseWriter, r *http.Request, endpoint *storage.Endpoint) {
	p.mu.RLock()
	svc := p.service
	p.mu.RUnlock()
	if svc == nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{
			"error": "codex plugin not initialized",
		})
		return
	}
	svc.HandleResponsesWebsocket(w, r, endpoint)
}

func (p *CodexPlugin) Reload() error {
	pool := codex.GetPool()
	if pool != nil {
		pool.Reload()
	}
	return nil
}

func (p *CodexPlugin) ReplaceAccountStore(store codexShared.CodexAccountStore) (codexShared.CodexAccountStore, error) {
	p.mu.RLock()
	codexJsonPath := p.codexJsonPath
	p.mu.RUnlock()
	if err := codex.InitPool(codexJsonPath, store); err != nil {
		return nil, err
	}

	p.mu.Lock()
	old := p.accountStore
	p.accountStore = store
	if p.service != nil {
		p.service.SetAccountStore(store)
	}
	p.mu.Unlock()
	return old, nil
}

func (p *CodexPlugin) Close() error {
	p.mu.Lock()
	store := p.accountStore
	p.accountStore = nil
	if p.service != nil {
		p.service.Close()
		p.service.SetAccountStore(nil)
	}
	p.mu.Unlock()
	if store == nil {
		return nil
	}
	return store.Close()
}

// --- TransformerRoundTripperProvider ---

func (p *CodexPlugin) TransformerRoundTripperSpecs() []string {
	return []string{"openai/codex"}
}

// --- executor.UpstreamRoundTripper ---

func (p *CodexPlugin) RoundTrip(ctx context.Context, req *executor.UpstreamRequest) *executor.UpstreamRoundTripResult {
	p.mu.RLock()
	svc := p.service
	p.mu.RUnlock()
	if svc == nil {
		return &executor.UpstreamRoundTripResult{
			StatusCode: http.StatusInternalServerError,
			Error:      fmt.Errorf("codex plugin not initialized"),
		}
	}
	return svc.RoundTrip(ctx, req)
}

type codexSyncPayload struct {
	MultiConfig codexShared.CodexMultiConfig `json:"multiConfig"`
	Accounts    []codexShared.CodexAccount   `json:"accounts"`
}

// --- ConfigSyncExporter / ConfigSyncImporter / ConfigSyncDecoder ---

func (p *CodexPlugin) SyncExport(configPath string) (string, json.RawMessage, error) {
	store := p.GetAccountStore()
	if store == nil {
		return "", nil, fmt.Errorf("account store not initialized")
	}

	accounts, err := store.ListAccounts(context.Background())
	if err != nil {
		return "", nil, fmt.Errorf("list codex accounts: %w", err)
	}

	mc, err := codexShared.LoadCodexMultiConfig(codexJsonPathFromConfig(configPath))
	if err != nil || mc == nil {
		mc = &codexShared.CodexMultiConfig{}
	}

	data, err := json.Marshal(codexSyncPayload{
		MultiConfig: *mc,
		Accounts:    accounts,
	})
	if err != nil {
		return "", nil, err
	}
	return "codexConfig", data, nil
}

func (p *CodexPlugin) SyncImport(configPath string, data json.RawMessage) error {
	hasEnabledField := bytes.Contains(data, []byte(`"enabled"`))
	var payload codexSyncPayload
	if err := json.Unmarshal(data, &payload); err != nil {
		return err
	}

	now := time.Now()
	seenAccountIDs := make(map[string]struct{}, len(payload.Accounts))
	legacyAccountIDs := make(map[string]string, len(payload.Accounts))
	seenRefreshTokens := make(map[string]struct{}, len(payload.Accounts))

	for i := range payload.Accounts {
		account := &payload.Accounts[i]
		account.AccountID = strings.TrimSpace(account.AccountID)
		account.Email = strings.TrimSpace(account.Email)
		account.RefreshToken = strings.TrimSpace(account.RefreshToken)
		account.AccessToken = strings.TrimSpace(account.AccessToken)
		account.IDToken = strings.TrimSpace(account.IDToken)
		account.PlanType = strings.TrimSpace(account.PlanType)
		account.ProxyUrl = strings.TrimSpace(account.ProxyUrl)
		account.CooldownReason = strings.TrimSpace(account.CooldownReason)
		if account.Weight <= 0 {
			account.Weight = 1
		}
		if !hasEnabledField {
			account.Enabled = true
		}
		switch account.Status {
		case codexShared.CodexStatusValid, codexShared.CodexStatusBanned, codexShared.CodexStatusExhausted, codexShared.CodexStatusReused, codexShared.CodexStatusUnknown:
		default:
			account.Status = codexShared.CodexStatusValid
		}
		if account.CreatedAt.IsZero() {
			account.CreatedAt = now
		}
		if account.UpdatedAt.IsZero() {
			account.UpdatedAt = account.CreatedAt
		}

		if account.AccountID == "" {
			return fmt.Errorf("codex account[%d] missing accountId", i)
		}
		account.ID = strings.TrimSpace(account.ID)
		if account.ID == "" {
			account.ID = codexShared.GenerateCodexLocalID(account.AccountID, account.Email)
		}
		if account.ID == "" {
			account.ID = account.AccountID
		}
		if account.ID == "" {
			return fmt.Errorf("codex account[%d] missing id inputs", i)
		}
		if _, exists := seenAccountIDs[account.ID]; exists {
			return fmt.Errorf("duplicate codex account id: %s", account.ID)
		}
		seenAccountIDs[account.ID] = struct{}{}
		if _, exists := legacyAccountIDs[account.AccountID]; !exists {
			legacyAccountIDs[account.AccountID] = account.ID
		}

		if account.RefreshToken != "" {
			if _, exists := seenRefreshTokens[account.RefreshToken]; exists {
				return fmt.Errorf("duplicate codex refreshToken for accountId: %s", account.AccountID)
			}
			seenRefreshTokens[account.RefreshToken] = struct{}{}
		}
	}

	store := p.GetAccountStore()
	if store == nil {
		return fmt.Errorf("account store not initialized")
	}

	type replaceAllAccountsStore interface {
		ReplaceAllAccounts(ctx context.Context, accounts []codexShared.CodexAccount) error
	}

	ctx := context.Background()
	replacer, ok := store.(replaceAllAccountsStore)
	if !ok {
		return fmt.Errorf("account store does not support full-replace sync without touching usage history")
	}
	if err := replacer.ReplaceAllAccounts(ctx, payload.Accounts); err != nil {
		return fmt.Errorf("replace codex accounts: %w", err)
	}
	svc := p.GetService()
	if svc != nil {
		svc.ClearAuthManagers()
	}

	payload.MultiConfig.ActiveAccountID = strings.TrimSpace(payload.MultiConfig.ActiveAccountID)
	if len(payload.Accounts) == 0 {
		payload.MultiConfig.ActiveAccountID = ""
	} else if _, ok := seenAccountIDs[payload.MultiConfig.ActiveAccountID]; !ok {
		if localID, ok := legacyAccountIDs[payload.MultiConfig.ActiveAccountID]; ok {
			payload.MultiConfig.ActiveAccountID = localID
		} else {
			payload.MultiConfig.ActiveAccountID = payload.Accounts[0].ID
		}
	}

	if err := codexShared.SaveCodexMultiConfig(codexJsonPathFromConfig(configPath), &payload.MultiConfig); err != nil {
		return fmt.Errorf("save codex global config: %w", err)
	}

	if svc != nil {
		if len(payload.Accounts) > 0 {
			svc.ensureCodexEndpoint()
		}
	}
	if pool := codex.GetPool(); pool != nil {
		pool.Reload()
	}
	return nil
}

func (p *CodexPlugin) SyncDecode(encoded string) (json.RawMessage, error) {
	compressed, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return nil, fmt.Errorf("base64 decode: %w", err)
	}
	gz, err := gzip.NewReader(bytes.NewReader(compressed))
	if err != nil {
		return nil, fmt.Errorf("gzip reader: %w", err)
	}
	defer gz.Close()
	raw, err := io.ReadAll(gz)
	if err != nil {
		return nil, fmt.Errorf("gzip decompress: %w", err)
	}
	return json.RawMessage(raw), nil
}

// AddAccount wraps desktopFacade.AddAccount to ensure endpoint creation.
func (p *CodexPlugin) AddAccount(configPath string, dtoJSON json.RawMessage) (json.RawMessage, error) {
	var dto codexAccountImportDTO
	if err := json.Unmarshal(dtoJSON, &dto); err != nil {
		return nil, err
	}
	account, err := p.buildAccountFromImportDTO(&dto)
	if err != nil {
		return nil, err
	}

	store := p.GetAccountStore()
	if store == nil {
		return nil, fmt.Errorf("account store not initialized")
	}
	ctx := context.Background()
	if existing, _ := store.GetByID(ctx, account.ID); existing != nil {
		return nil, fmt.Errorf("account with this id already exists")
	}
	if account.RefreshToken != "" {
		if rt, _ := store.GetByRefreshToken(ctx, account.RefreshToken); rt != nil {
			return nil, fmt.Errorf("account with this refreshToken already exists")
		}
	}
	if err := store.Insert(ctx, account); err != nil {
		return nil, err
	}
	p.afterAccountsImported([]*codexShared.CodexAccount{account})

	p.mu.RLock()
	codexJsonPath := p.codexJsonPath
	p.mu.RUnlock()
	mc, _ := codexShared.LoadCodexMultiConfig(codexJsonPath)
	activeID := ""
	if mc != nil {
		activeID = mc.ActiveAccountID
	}
	return json.Marshal(codexShared.MarshalAccountForFrontend(account, account.ID == activeID))
}

// ImportAccounts 批量校验并一次落库导入账号，避免逐个保存触发多次 UI/pool 刷新。
func (p *CodexPlugin) ImportAccounts(configPath string, dtoJSON json.RawMessage) (json.RawMessage, error) {
	var rawItems []json.RawMessage
	if err := json.Unmarshal(dtoJSON, &rawItems); err != nil {
		// 兼容 { "accounts": [...] }
		var wrapper struct {
			Accounts []json.RawMessage `json:"accounts"`
		}
		if err2 := json.Unmarshal(dtoJSON, &wrapper); err2 != nil || len(wrapper.Accounts) == 0 {
			return nil, fmt.Errorf("invalid import payload: expect account array")
		}
		rawItems = wrapper.Accounts
	}
	if len(rawItems) == 0 {
		return nil, fmt.Errorf("no accounts to import")
	}

	store := p.GetAccountStore()
	if store == nil {
		return nil, fmt.Errorf("account store not initialized")
	}
	ctx := context.Background()

	existingAccounts, err := store.ListAccounts(ctx)
	if err != nil {
		return nil, fmt.Errorf("list existing accounts: %w", err)
	}
	existingIDs := make(map[string]struct{}, len(existingAccounts))
	existingRefresh := make(map[string]struct{}, len(existingAccounts))
	for i := range existingAccounts {
		a := &existingAccounts[i]
		if id := strings.TrimSpace(a.ID); id != "" {
			existingIDs[id] = struct{}{}
		}
		if rt := strings.TrimSpace(a.RefreshToken); rt != "" {
			existingRefresh[rt] = struct{}{}
		}
	}

	type importFailure struct {
		Index  int    `json:"index"`
		Email  string `json:"email,omitempty"`
		Reason string `json:"reason"`
	}
	result := struct {
		Success  int             `json:"success"`
		Failed   int             `json:"failed"`
		Skipped  int             `json:"skipped"`
		Errors   []importFailure `json:"errors,omitempty"`
		Message  string          `json:"message"`
		Imported int             `json:"imported"`
	}{
		Errors: make([]importFailure, 0),
	}

	batchIDs := make(map[string]struct{}, len(rawItems))
	batchRefresh := make(map[string]struct{}, len(rawItems))
	toInsert := make([]*codexShared.CodexAccount, 0, len(rawItems))

	for i, raw := range rawItems {
		var dto codexAccountImportDTO
		if err := json.Unmarshal(raw, &dto); err != nil {
			result.Failed++
			result.Errors = append(result.Errors, importFailure{Index: i + 1, Reason: "invalid account json"})
			continue
		}
		account, buildErr := p.buildAccountFromImportDTO(&dto)
		if buildErr != nil {
			result.Failed++
			result.Errors = append(result.Errors, importFailure{
				Index:  i + 1,
				Email:  strings.TrimSpace(dto.Email),
				Reason: buildErr.Error(),
			})
			continue
		}

		if _, ok := existingIDs[account.ID]; ok {
			result.Skipped++
			result.Errors = append(result.Errors, importFailure{
				Index:  i + 1,
				Email:  account.Email,
				Reason: "account with this id already exists",
			})
			continue
		}
		if account.RefreshToken != "" {
			if _, ok := existingRefresh[account.RefreshToken]; ok {
				result.Skipped++
				result.Errors = append(result.Errors, importFailure{
					Index:  i + 1,
					Email:  account.Email,
					Reason: "account with this refreshToken already exists",
				})
				continue
			}
		}
		if _, ok := batchIDs[account.ID]; ok {
			result.Failed++
			result.Errors = append(result.Errors, importFailure{
				Index:  i + 1,
				Email:  account.Email,
				Reason: "duplicate account id in import batch",
			})
			continue
		}
		if account.RefreshToken != "" {
			if _, ok := batchRefresh[account.RefreshToken]; ok {
				result.Failed++
				result.Errors = append(result.Errors, importFailure{
					Index:  i + 1,
					Email:  account.Email,
					Reason: "duplicate refreshToken in import batch",
				})
				continue
			}
		}

		batchIDs[account.ID] = struct{}{}
		if account.RefreshToken != "" {
			batchRefresh[account.RefreshToken] = struct{}{}
		}
		toInsert = append(toInsert, account)
	}

	if len(toInsert) > 0 {
		if err := store.InsertMany(ctx, toInsert); err != nil {
			return nil, fmt.Errorf("batch insert accounts: %w", err)
		}
		p.afterAccountsImported(toInsert)
		result.Success = len(toInsert)
		result.Imported = len(toInsert)
	}

	result.Message = fmt.Sprintf("JSON 导入完成：成功 %d，失败 %d，跳过重复 %d", result.Success, result.Failed, result.Skipped)
	return json.Marshal(result)
}

func (p *CodexPlugin) buildAccountFromImportDTO(dto *codexAccountImportDTO) (*codexShared.CodexAccount, error) {
	if dto == nil {
		return nil, fmt.Errorf("account data is required")
	}
	normalizeCodexAccountImportDTO(dto)

	if dto.RefreshToken == "" && dto.AccessToken == "" {
		return nil, fmt.Errorf("either refreshToken or accessToken is required")
	}
	if dto.Email == "" {
		return nil, fmt.Errorf("email is required")
	}
	if dto.RefreshToken == "" && dto.AccessToken != "" && dto.ExpiresAt == "" {
		return nil, fmt.Errorf("expiresAt or accessToken exp is required for temporary account")
	}
	localID := codexShared.GenerateCodexLocalID(dto.AccountID, dto.Email)
	if localID == "" {
		return nil, fmt.Errorf("account id is required")
	}

	now := time.Now()
	enabled := true
	if dto.Enabled != nil {
		enabled = *dto.Enabled
	}
	account := &codexShared.CodexAccount{
		ID:             localID,
		RefreshToken:   dto.RefreshToken,
		AccessToken:    dto.AccessToken,
		IDToken:        dto.IDToken,
		AccountID:      dto.AccountID,
		Email:          dto.Email,
		PlanType:       dto.PlanType,
		Enabled:        enabled,
		Websockets:     resolveNewAccountWebsockets(dto.Websockets),
		Password:       dto.Password,
		MFACode:        dto.MFACode,
		ProxyUrl:       dto.ProxyUrl,
		Weight:         dto.Weight,
		Status:         codexShared.CodexStatusValid,
		CooldownUntil:  time.Time{},
		CooldownReason: "",
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	if dto.ExpiresAt != "" {
		if t, err := time.Parse(time.RFC3339, dto.ExpiresAt); err == nil {
			account.ExpiresAt = t
		}
	}
	return account, nil
}

func (p *CodexPlugin) afterAccountsImported(accounts []*codexShared.CodexAccount) {
	if len(accounts) == 0 {
		return
	}

	p.mu.RLock()
	codexJsonPath := p.codexJsonPath
	p.mu.RUnlock()

	mc, _ := codexShared.LoadCodexMultiConfig(codexJsonPath)
	if mc == nil {
		mc = &codexShared.CodexMultiConfig{}
	}
	if strings.TrimSpace(mc.ActiveAccountID) == "" {
		mc.ActiveAccountID = accounts[0].ID
		_ = codexShared.SaveCodexMultiConfig(codexJsonPath, mc)
	}

	if svc := p.GetService(); svc != nil {
		for _, account := range accounts {
			if account != nil {
				svc.RemoveAuthManager(account.ID)
			}
		}
		svc.ensureCodexEndpoint()
	}

	if pool := codex.GetPool(); pool != nil {
		pool.Reload()
	}
}

type codexAccountImportDTO struct {
	RefreshToken string `json:"refreshToken"`
	Email        string `json:"email"`
	PlanType     string `json:"planType"`
	AccountID    string `json:"accountId"`
	Enabled      *bool  `json:"enabled,omitempty"`
	Websockets   *bool  `json:"websockets,omitempty"`
	AccessToken  string `json:"accessToken"`
	IDToken      string `json:"idToken"`
	ExpiresAt    string `json:"expiresAt"`
	ProxyUrl     string `json:"proxyUrl"`
	Password     string `json:"password"`
	MFACode      string `json:"mfaCode"`
	Weight       int    `json:"weight"`
	Expired      string `json:"expired"`
}

func resolveNewAccountWebsockets(value *bool) bool {
	return value == nil || *value
}

func normalizeCodexAccountImportDTO(dto *codexAccountImportDTO) {
	if dto == nil {
		return
	}
	dto.RefreshToken = strings.TrimSpace(dto.RefreshToken)
	dto.AccessToken = strings.TrimSpace(dto.AccessToken)
	dto.IDToken = strings.TrimSpace(dto.IDToken)
	dto.AccountID = strings.TrimSpace(dto.AccountID)
	dto.Email = strings.TrimSpace(dto.Email)
	dto.PlanType = strings.TrimSpace(dto.PlanType)
	dto.ProxyUrl = strings.TrimSpace(dto.ProxyUrl)
	dto.ExpiresAt = strings.TrimSpace(dto.ExpiresAt)
	dto.Expired = strings.TrimSpace(dto.Expired)
	if dto.ExpiresAt == "" {
		dto.ExpiresAt = dto.Expired
	}

	if dto.AccessToken != "" {
		claims, err := codexAuth.ParseJWTToken(dto.AccessToken)
		if err == nil && claims != nil {
			if dto.AccountID == "" {
				dto.AccountID = strings.TrimSpace(claims.CodexAuth.ChatgptAccountID)
			}
			if dto.Email == "" {
				dto.Email = strings.TrimSpace(claims.Email)
				if dto.Email == "" {
					dto.Email = strings.TrimSpace(claims.Profile.Email)
				}
			}
			if dto.PlanType == "" {
				dto.PlanType = strings.TrimSpace(claims.CodexAuth.ChatgptPlanType)
			}
			if dto.ExpiresAt == "" && claims.Exp > 0 {
				dto.ExpiresAt = time.Unix(claims.Exp, 0).UTC().Format(time.RFC3339)
			}
		}
	}
	if dto.AccountID == "" {
		dto.AccountID = uuid.NewString()
	}
}

func codexJsonPathFromConfig(configPath string) string {
	if configPath == "" {
		return ""
	}
	return filepath.Join(filepath.Dir(configPath), filepath.Base(codexShared.GetDefaultCodexMultiConfigPath()))
}
