package codexplugin

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"time"

	codex "clisimplehub/internal/codex"
	codexShared "clisimplehub/internal/codex/shared"
	"clisimplehub/internal/executor"
	"clisimplehub/internal/plugin"
	"clisimplehub/internal/storage"
	"clisimplehub/internal/transformer"
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

	// Open CodexAccountStore using same data.sqlite as usage stats
	dataDir := filepath.Dir(cfg.ConfigPath)
	dbPath := filepath.Join(dataDir, "data.sqlite")
	accountStore, err := codexShared.OpenCodexAccountStore(dbPath)
	if err != nil {
		// Non-fatal: log and continue without store
		fmt.Printf("[codex-plugin] Warning: failed to open account store (%s): %v\n", dbPath, err)
	}

	_ = codex.InitPool(codexJsonPath, accountStore)

	transformer.RegisterAvailability("codex", func() map[string][]string {
		return map[string][]string{
			"codex": {"openai/codex"},
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
	r.HandleFunc("/codex/v1/responses", r.RequireAuth(svc.HandleResponses))
	r.HandleFunc("/codex/v1/responses/compact", r.RequireAuth(svc.HandleResponses))
	r.HandleFunc("/v0/management/codex-auth-url", r.RequireAuth(p.handleAuthURL))
	r.HandleFunc("/v0/management/oauth-callback", r.RequireAuth(p.handleOAuthCallback))
}

func (p *CodexPlugin) Reload() error {
	pool := codex.GetPool()
	if pool != nil {
		pool.Reload()
	}
	return nil
}

// --- TransformerForwarderProvider ---

func (p *CodexPlugin) TransformerForwarderSpecs() []string {
	return []string{"openai/codex"}
}

// --- executor.TransformerForwarder ---

func (p *CodexPlugin) Forward(ctx context.Context, body []byte, model string, isStreaming bool, w http.ResponseWriter, requestPath string) *executor.ForwardResult {
	p.mu.RLock()
	svc := p.service
	p.mu.RUnlock()
	if svc == nil {
		return &executor.ForwardResult{
			StatusCode: http.StatusInternalServerError,
			Error:      fmt.Errorf("codex plugin not initialized"),
		}
	}
	return svc.Forward(ctx, body, model, isStreaming, w, requestPath)
}

// AddAccount wraps desktopFacade.AddAccount to ensure endpoint creation.
func (p *CodexPlugin) AddAccount(configPath string, dtoJSON json.RawMessage) (json.RawMessage, error) {
	var dto struct {
		RefreshToken string `json:"refreshToken"`
		Email        string `json:"email"`
		PlanType     string `json:"planType"`
		AccountID    string `json:"accountId"`
		AccessToken  string `json:"accessToken"`
		IDToken      string `json:"idToken"`
		ExpiresAt    string `json:"expiresAt"`
		ProxyUrl     string `json:"proxyUrl"`
		Password     string `json:"password"`
		MFACode      string `json:"mfaCode"`
		Weight       int    `json:"weight"`
	}
	if err := json.Unmarshal(dtoJSON, &dto); err != nil {
		return nil, err
	}

	dto.RefreshToken = strings.TrimSpace(dto.RefreshToken)
	dto.AccessToken = strings.TrimSpace(dto.AccessToken)
	dto.IDToken = strings.TrimSpace(dto.IDToken)
	dto.AccountID = strings.TrimSpace(dto.AccountID)
	dto.Email = strings.TrimSpace(dto.Email)
	dto.PlanType = strings.TrimSpace(dto.PlanType)
	dto.ProxyUrl = strings.TrimSpace(dto.ProxyUrl)

	if dto.RefreshToken == "" && dto.AccessToken == "" {
		return nil, fmt.Errorf("either refreshToken or accessToken is required")
	}
	if dto.AccountID == "" {
		return nil, fmt.Errorf("accountId is required")
	}

	store := p.GetAccountStore()
	if store == nil {
		return nil, fmt.Errorf("account store not initialized")
	}

	existing, _ := store.GetByID(context.Background(), dto.AccountID)
	if existing != nil {
		return nil, fmt.Errorf("account with this accountId already exists")
	}
	if dto.RefreshToken != "" {
		if rt, _ := store.GetByRefreshToken(context.Background(), dto.RefreshToken); rt != nil {
			return nil, fmt.Errorf("account with this refreshToken already exists")
		}
	}

	now := time.Now()
	account := codexShared.CodexAccount{
		RefreshToken: dto.RefreshToken,
		AccessToken:  dto.AccessToken,
		IDToken:      dto.IDToken,
		AccountID:    dto.AccountID,
		Email:        dto.Email,
		PlanType:     dto.PlanType,
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
		mc.ActiveAccountID = account.AccountID
		_ = codexShared.SaveCodexMultiConfig(codexJsonPath, mc)
	}

	if svc := p.GetService(); svc != nil {
		svc.ensureCodexEndpoint()
	}

	if pool := codex.GetPool(); pool != nil {
		pool.Reload()
	}

	isActive := account.AccountID == mc.ActiveAccountID
	return json.Marshal(codexShared.MarshalAccountForFrontend(&account, isActive))
}

func codexJsonPathFromConfig(configPath string) string {
	if configPath == "" {
		return ""
	}
	return filepath.Join(filepath.Dir(configPath), filepath.Base(codexShared.GetDefaultCodexMultiConfigPath()))
}
