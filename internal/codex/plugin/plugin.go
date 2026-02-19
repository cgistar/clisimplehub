package codexplugin

import (
	"path/filepath"
	"sync"

	codex "clisimplehub/internal/codex"
	codexShared "clisimplehub/internal/codex/shared"
	"clisimplehub/internal/plugin"
)

func init() {
	plugin.Register(&CodexPlugin{})
}

type CodexPlugin struct {
	desktopFacade
	service       *CodexService
	codexJsonPath string
	mu            sync.RWMutex
}

func (p *CodexPlugin) Name() string { return "codex-accounts" }

func (p *CodexPlugin) Init(cfg plugin.InitConfig) error {
	codexJsonPath := codexJsonPathFromConfig(cfg.ConfigPath)
	_ = codex.InitPool(codexJsonPath) // non-fatal

	p.mu.Lock()
	p.codexJsonPath = codexJsonPath
	p.service = NewCodexService()
	p.mu.Unlock()

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

func codexJsonPathFromConfig(configPath string) string {
	if configPath == "" {
		return ""
	}
	return filepath.Join(filepath.Dir(configPath), filepath.Base(codexShared.GetDefaultCodexMultiConfigPath()))
}
