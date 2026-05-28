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

func (p *CodexPlugin) HandleResponsesWebsocket(w http.ResponseWriter, r *http.Request) {
	p.mu.RLock()
	svc := p.service
	p.mu.RUnlock()
	if svc == nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{
			"error": "codex plugin not initialized",
		})
		return
	}
	svc.HandleResponsesWebsocket(w, r)
}

func (p *CodexPlugin) Reload() error {
	pool := codex.GetPool()
	if pool != nil {
		pool.Reload()
	}
	return nil
}

func (p *CodexPlugin) Close() error {
	p.mu.Lock()
	store := p.accountStore
	p.accountStore = nil
	if p.service != nil {
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
	normalizeCodexAccountImportDTO(&dto)

	if dto.RefreshToken == "" && dto.AccessToken == "" {
		return nil, fmt.Errorf("either refreshToken or accessToken is required")
	}
	if dto.AccountID == "" {
		dto.AccountID = uuid.NewString()
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

	store := p.GetAccountStore()
	if store == nil {
		return nil, fmt.Errorf("account store not initialized")
	}

	existing, _ := store.GetByID(context.Background(), localID)
	if existing != nil {
		return nil, fmt.Errorf("account with this id already exists")
	}
	if dto.RefreshToken != "" {
		if rt, _ := store.GetByRefreshToken(context.Background(), dto.RefreshToken); rt != nil {
			return nil, fmt.Errorf("account with this refreshToken already exists")
		}
	}

	now := time.Now()
	enabled := true
	if dto.Enabled != nil {
		enabled = *dto.Enabled
	}
	account := codexShared.CodexAccount{
		ID:           localID,
		RefreshToken: dto.RefreshToken,
		AccessToken:  dto.AccessToken,
		IDToken:      dto.IDToken,
		AccountID:    dto.AccountID,
		Email:        dto.Email,
		PlanType:     dto.PlanType,
		Enabled:      enabled,
		Websockets:   dto.Websockets,
		Password:     dto.Password,
		MFACode:      dto.MFACode,
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

	if err := store.Insert(context.Background(), &account); err != nil {
		return nil, err
	}

	// Update active account in codex.json if first account
	p.mu.RLock()
	codexJsonPath := p.codexJsonPath
	p.mu.RUnlock()

	mc, _ := codexShared.LoadCodexMultiConfig(codexJsonPath)
	if mc == nil {
		mc = &codexShared.CodexMultiConfig{}
	}
	if mc.ActiveAccountID == "" {
		mc.ActiveAccountID = account.ID
		_ = codexShared.SaveCodexMultiConfig(codexJsonPath, mc)
	}

	if svc := p.GetService(); svc != nil {
		svc.ensureCodexEndpoint()
	}

	if pool := codex.GetPool(); pool != nil {
		pool.Reload()
	}

	isActive := account.ID == mc.ActiveAccountID
	return json.Marshal(codexShared.MarshalAccountForFrontend(&account, isActive))
}

type codexAccountImportDTO struct {
	RefreshToken string `json:"refreshToken"`
	Email        string `json:"email"`
	PlanType     string `json:"planType"`
	AccountID    string `json:"accountId"`
	Enabled      *bool  `json:"enabled,omitempty"`
	Websockets   bool   `json:"websockets"`
	AccessToken  string `json:"accessToken"`
	IDToken      string `json:"idToken"`
	ExpiresAt    string `json:"expiresAt"`
	ProxyUrl     string `json:"proxyUrl"`
	Password     string `json:"password"`
	MFACode      string `json:"mfaCode"`
	Weight       int    `json:"weight"`
	Expired      string `json:"expired"`
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
