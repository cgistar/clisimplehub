package xaiplugin

import (
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"sync"

	xai "clisimplehub/internal/xai"
	xaiShared "clisimplehub/internal/xai/shared"
	"clisimplehub/internal/plugin"
	"clisimplehub/internal/storage"
)

func init() {
	plugin.Register(&XaiPlugin{})
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

type XaiPlugin struct {
	desktopFacade
	service      *XaiService
	xaiJsonPath  string
	mu           sync.RWMutex
}

func (p *XaiPlugin) Name() string { return "xai-accounts" }

func (p *XaiPlugin) GetService() *XaiService {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.service
}

func (p *XaiPlugin) Init(cfg plugin.InitConfig) error {
	xaiJsonPath := xaiJsonPathFromConfig(cfg.ConfigPath)
	_ = xai.InitPool(xaiJsonPath)

	p.mu.Lock()
	p.xaiJsonPath = xaiJsonPath
	p.service = NewXaiService()
	p.mu.Unlock()

	if cfg.Storage != nil {
		p.service.SetStorageAccessor(&pluginStorageAccessor{
			store:  cfg.Storage,
			reload: cfg.TriggerReload,
		})
	}
	return nil
}

func (p *XaiPlugin) RegisterRoutes(r plugin.RouteRegistrar) {
	p.mu.RLock()
	svc := p.service
	p.mu.RUnlock()
	if svc == nil {
		return
	}
	r.HandleFunc("/xai", r.RequireAuth(p.handleXaiRoute))
	r.HandleFunc("/xai/*", r.RequireAuth(p.handleXaiRoute))
}

func (p *XaiPlugin) Reload() error {
	pool := xai.GetPool()
	if pool != nil {
		return pool.Reload()
	}
	return nil
}

func (p *XaiPlugin) SyncExport(configPath string) (string, json.RawMessage, error) {
	path := xaiJsonPathFromConfig(configPath)
	cfg, err := xaiShared.LoadXaiMultiConfig(path)
	if err != nil {
		cfg = &xaiShared.XaiMultiConfig{Config: xaiShared.DefaultXaiConfig(), Accounts: []xaiShared.XaiAccount{}}
	}
	data, err := json.Marshal(cfg)
	if err != nil {
		return "", nil, err
	}
	return "xaiConfig", data, nil
}

func (p *XaiPlugin) SyncImport(configPath string, data json.RawMessage) error {
	var cfg xaiShared.XaiMultiConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return err
	}
	for i := range cfg.Accounts {
		xaiShared.NormalizeAccount(&cfg.Accounts[i])
	}
	path := xaiJsonPathFromConfig(configPath)
	if err := xaiShared.SaveXaiMultiConfig(path, &cfg); err != nil {
		return err
	}
	if pool := xai.GetPool(); pool != nil {
		return pool.Reload()
	}
	return xai.InitPool(path)
}

func (p *XaiPlugin) handleXaiRoute(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/xai")
	path = strings.Trim(path, "/")
	switch {
	case path == "" || path == "accounts":
		if r.Method == http.MethodGet {
			raw, err := p.GetAccounts(p.xaiJsonPath)
			if err != nil {
				writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
				return
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(raw)
			return
		}
	case path == "config":
		if r.Method == http.MethodGet {
			raw, err := p.GetXaiGlobalConfig(p.xaiJsonPath)
			if err != nil {
				writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
				return
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(raw)
			return
		}
	}
	writeJSON(w, http.StatusNotFound, map[string]any{"error": fmt.Sprintf("unknown xai route: %s", path)})
}

// ensure config dir helper used by facade when path is relative
func resolveConfigPath(configPath string) string {
	configPath = strings.TrimSpace(configPath)
	if configPath == "" {
		return xaiShared.GetDefaultXaiMultiConfigPath()
	}
	if filepath.Base(configPath) == configPath {
		return configPath
	}
	return configPath
}
