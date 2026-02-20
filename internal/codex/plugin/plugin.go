package codexplugin

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
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

// pluginStorageAccessor bridges plugin.InitConfig to the StorageAccessor interface.
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
	mu            sync.RWMutex
}

func (p *CodexPlugin) Name() string { return "codex-accounts" }

// GetService returns the CodexService instance (for internal use).
func (p *CodexPlugin) GetService() *CodexService {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.service
}

func (p *CodexPlugin) Init(cfg plugin.InitConfig) error {
	codexJsonPath := codexJsonPathFromConfig(cfg.ConfigPath)
	_ = codex.InitPool(codexJsonPath) // non-fatal

	// Register transformer availability
	transformer.RegisterAvailability("codex", func() map[string][]string {
		return map[string][]string{
			"codex": {"openai/codex"},
		}
	})

	p.mu.Lock()
	p.codexJsonPath = codexJsonPath
	p.service = NewCodexService()
	p.mu.Unlock()

	// Inject storage accessor if available
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
	// Register both standard and compact endpoints (aligned with claude-relay-service)
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
	// Parse the DTO to get account info
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
	if strings.TrimSpace(dto.RefreshToken) == "" {
		return nil, fmt.Errorf("refreshToken is required")
	}

	mc, err := codexShared.LoadCodexMultiConfig(configPath)
	if err != nil {
		if !os.IsNotExist(err) {
			return nil, err
		}
		mc = &codexShared.CodexMultiConfig{}
	}

	// Check duplicate
	if mc.FindAccountByRefreshToken(dto.RefreshToken) != nil {
		return nil, fmt.Errorf("account already exists")
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
	if mc.ActiveRefreshToken == "" {
		mc.ActiveRefreshToken = account.RefreshToken
	}

	// Use SaveConfigAndEnsureEndpoint to save and create endpoint
	svc := p.GetService()
	if svc != nil {
		if err := svc.SaveConfigAndEnsureEndpoint(configPath, mc); err != nil {
			return nil, err
		}
	} else {
		// Fallback
		if err := codexShared.SaveCodexMultiConfig(configPath, mc); err != nil {
			return nil, err
		}
		if pool := codex.GetPool(); pool != nil {
			pool.Reload()
		}
	}

	isActive := account.RefreshToken == mc.ActiveRefreshToken
	return json.Marshal(codexShared.MarshalAccountForFrontend(&account, isActive))
}

func codexJsonPathFromConfig(configPath string) string {
	if configPath == "" {
		return ""
	}
	return filepath.Join(filepath.Dir(configPath), filepath.Base(codexShared.GetDefaultCodexMultiConfigPath()))
}
