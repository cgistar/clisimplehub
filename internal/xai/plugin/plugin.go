package xaiplugin

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"sync"

	xai "clisimplehub/internal/xai"
	xaiBackend "clisimplehub/internal/xai/backend"
	xaiShared "clisimplehub/internal/xai/shared"
	"clisimplehub/internal/executor"
	"clisimplehub/internal/plugin"
	"clisimplehub/internal/storage"
	"clisimplehub/internal/transformer"

	"github.com/tidwall/gjson"
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
	service     *XaiService
	xaiJsonPath string
	mu          sync.RWMutex
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

	xai.SetOnAccountsDeleted(func(accountIDs []string) {
		for _, id := range accountIDs {
			CloseWebsocketSessionsForAccount(id)
		}
	})

	transformer.RegisterAvailability("xai", func() map[string][]string {
		return map[string][]string{
			"codex":  {"openai/xai"},
			"chat":   {"openai/xai"},
			"claude": {"openai/xai"},
		}
	})

	p.mu.Lock()
	p.xaiJsonPath = xaiJsonPath
	p.service = NewXaiService()
	p.mu.Unlock()

	if cfg.Storage != nil {
		p.service.SetStorageAccessor(&pluginStorageAccessor{
			store:  cfg.Storage,
			reload: cfg.TriggerReload,
		})
		// 已有账号时补建 endpoints
		if pool := xai.GetPool(); pool != nil && len(pool.ListAccounts()) > 0 {
			p.service.ensureXaiEndpoints()
		}
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
		if err := pool.Reload(); err != nil {
			return err
		}
	} else if err := xai.InitPool(path); err != nil {
		return err
	}
	if p.service != nil && len(cfg.Accounts) > 0 {
		p.service.ensureXaiEndpoints()
	}
	return nil
}

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

// --- TransformerRoundTripperProvider ---

func (p *XaiPlugin) TransformerRoundTripperSpecs() []string {
	return []string{"openai/xai"}
}

// --- executor.UpstreamRoundTripper ---

func (p *XaiPlugin) RoundTrip(ctx context.Context, req *executor.UpstreamRequest) *executor.UpstreamRoundTripResult {
	p.mu.RLock()
	svc := p.service
	p.mu.RUnlock()
	if svc == nil {
		return &executor.UpstreamRoundTripResult{
			StatusCode: http.StatusInternalServerError,
			Error:      fmt.Errorf("xai plugin not initialized"),
		}
	}
	return svc.RoundTrip(ctx, req)
}

// --- TokenEstimator：Claude /v1/messages/count_tokens ---

func (p *XaiPlugin) EstimateInputTokens(body []byte) int {
	if len(body) == 0 {
		return 0
	}
	// 若已是 Responses 或 Claude messages，都尝试 prepare 后估算
	model := xaiBackend.ExtractModelForCount(body)
	// Claude messages 需先转 Responses 才能对齐上游计费形态；无转换器时退回 body 启发式
	countBody := body
	if gjson.GetBytes(body, "messages").Exists() && !gjson.GetBytes(body, "input").Exists() {
		// 尽量走 claude→responses 转换在 transformer 层；此处用 messages 文本粗估
		// 为与 prepare 一致：若 body 已是 responses 则直接 prepare
		n, _ := xaiBackend.CountTokensForRequest(countBody, model, true, "")
		if n > 0 {
			return n
		}
	}
	n, _ := xaiBackend.CountTokensForRequest(body, model, false, "")
	if n < 1 {
		return 1
	}
	return n
}

// AddAccount 包装 facade，成功后 ensure endpoints。
func (p *XaiPlugin) AddAccount(configPath string, dtoJSON json.RawMessage) (json.RawMessage, error) {
	raw, err := p.desktopFacade.AddAccount(configPath, dtoJSON)
	if err != nil {
		return nil, err
	}
	if svc := p.GetService(); svc != nil {
		svc.ensureXaiEndpoints()
	}
	return raw, nil
}
