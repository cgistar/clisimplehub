package kiroplugin

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sync"

	"clisimplehub/internal/executor"
	kiroapi "clisimplehub/internal/kiro"
	kiro_claude "clisimplehub/internal/kiro/claude"
	kiro_chat "clisimplehub/internal/kiro/openai/chat-completions"
	kiroShared "clisimplehub/internal/kiro/shared"
	"clisimplehub/internal/plugin"
	"clisimplehub/internal/storage"
	"clisimplehub/internal/transformer"
)

type kiroSyncAbsentMarker struct {
	Absent bool `json:"__clisimplehub_absent"`
}

func init() {
	plugin.Register(&KiroPlugin{})
}

// KiroPlugin implements plugin.Plugin for all Kiro functionality.
type KiroPlugin struct {
	desktopFacade // embeds plugin.KiroDesktopProvider
	service       *KiroService
	mu            sync.RWMutex
}

func (p *KiroPlugin) Name() string { return "kiro" }

func (p *KiroPlugin) Init(cfg plugin.InitConfig) error {
	kiro_claude.SetConfigGetter(cfg.ConfigGetter)

	kiroJsonPath := kiroJsonPathFromConfig(cfg.ConfigPath)
	_ = kiroapi.InitPool(kiroJsonPath) // non-fatal

	transformer.RegisterFactory("claude", "kiro/claude", func() transformer.Transformer {
		return kiro_claude.NewTransformer()
	})
	transformer.RegisterFactory("chat", "kiro/chat", func() transformer.Transformer {
		return kiro_chat.NewTransformer()
	})
	transformer.RegisterAvailability("kiro", func() map[string][]string {
		if !kiro_claude.HasValidLocalAccessToken() {
			return nil
		}
		return map[string][]string{
			"claude": {"kiro/claude"},
			"kiro":   {"claude"},
			"chat":   {"kiro/chat"},
		}
	})

	p.mu.Lock()
	p.service = NewKiroService()
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

func (p *KiroPlugin) RegisterRoutes(r plugin.RouteRegistrar) {
	p.mu.RLock()
	svc := p.service
	p.mu.RUnlock()
	if svc == nil {
		return
	}
	r.HandleFunc("/kiro", r.RequireAuth(p.handleKiroRoute))
	r.HandleFunc("/kiro/*", r.RequireAuth(p.handleKiroRoute))
}

func (p *KiroPlugin) Reload() error {
	p.mu.RLock()
	svc := p.service
	p.mu.RUnlock()
	if svc == nil {
		return nil
	}
	return svc.Reload()
}

// --- TransformerRoundTripperProvider ---

func (p *KiroPlugin) TransformerRoundTripperSpecs() []string {
	return []string{"kiro/claude"}
}

// --- executor.UpstreamRoundTripper ---

func (p *KiroPlugin) RoundTrip(ctx context.Context, req *executor.UpstreamRequest) *executor.UpstreamRoundTripResult {
	p.mu.RLock()
	svc := p.service
	p.mu.RUnlock()
	if svc == nil {
		return &executor.UpstreamRoundTripResult{
			StatusCode: http.StatusInternalServerError,
			Error:      fmt.Errorf("kiro plugin not initialized"),
		}
	}
	return svc.RoundTrip(ctx, req)
}

// --- TokenEstimator ---

func (p *KiroPlugin) EstimateInputTokens(body []byte) int {
	return kiro_claude.EstimateClaudeInputTokens(body)
}

// --- ConfigSync ---

func (p *KiroPlugin) SyncExport(configPath string) (string, json.RawMessage, error) {
	kiroJsonPath := kiroJsonPathFromConfig(configPath)
	mc, err := kiroShared.LoadKiroMultiConfig(kiroJsonPath)
	if err != nil {
		if os.IsNotExist(err) {
			// Snapshot for rollback: file was absent before sync.
			// Keep payload non-empty so rollback calls SyncImport and can delete kiro.json.
			return "kiroConfig", marshalJSON(kiroSyncAbsentMarker{Absent: true}), nil
		}
		return "", nil, err
	}
	data, err := json.Marshal(mc)
	if err != nil {
		return "", nil, err
	}
	return "kiroConfig", data, nil
}

func (p *KiroPlugin) SyncImport(configPath string, data json.RawMessage) error {
	// Support rollback snapshots for the "file absent" case.
	var marker kiroSyncAbsentMarker
	if err := json.Unmarshal(data, &marker); err == nil && marker.Absent {
		kiroJsonPath := kiroJsonPathFromConfig(configPath)
		if kiroJsonPath != "" {
			if err := os.Remove(kiroJsonPath); err != nil && !os.IsNotExist(err) {
				return err
			}
		}
		if pool := kiroapi.GetPool(); pool != nil {
			pool.Reload()
		}
		kiro_claude.SetCachedBufferedStream(false)
		kiro_claude.SetCachedModelMapping(kiroShared.DefaultKiroModelMapping())
		_ = kiro_claude.ReloadAllTransformers()
		return nil
	}

	var mc kiroShared.KiroMultiConfig
	if err := json.Unmarshal(data, &mc); err != nil {
		return err
	}
	kiroJsonPath := kiroJsonPathFromConfig(configPath)
	if mc.Accounts == nil {
		mc.Accounts = []kiroShared.KiroAccount{}
	}
	if err := kiroShared.SaveKiroMultiConfig(kiroJsonPath, &mc); err != nil {
		return err
	}
	kiro_claude.SetCachedBufferedStream(mc.BufferedStream)
	kiro_claude.SetCachedModelMapping(mc.ModelMapping)
	_ = kiro_claude.ReloadAllTransformers()
	return nil
}

func (p *KiroPlugin) SyncDecode(encoded string) (json.RawMessage, error) {
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

// --- helpers ---

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

func kiroJsonPathFromConfig(configPath string) string {
	if configPath == "" {
		return ""
	}
	return filepath.Join(filepath.Dir(configPath), filepath.Base(kiroShared.GetDefaultKiroMultiConfigPath()))
}
