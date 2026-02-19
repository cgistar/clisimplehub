package xrayplugin

import (
	"bytes"
	"compress/gzip"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"strconv"
	"strings"
	"sync"

	"clisimplehub/internal/plugin"
)

func init() {
	plugin.Register(&XRayPlugin{})
}

// XRayPlugin implements plugin.Plugin for XRay client proxy.
type XRayPlugin struct {
	desktopFacade
	service *XRayService
	mu      sync.RWMutex
}

func (p *XRayPlugin) Name() string { return "xray" }

func (p *XRayPlugin) Init(cfg plugin.InitConfig) error {
	cfgPath := configPathFromAppConfig(cfg.ConfigPath)

	svc, err := NewXRayService(cfgPath)
	if err != nil {
		return err
	}

	p.mu.Lock()
	p.service = svc
	p.mu.Unlock()

	// Auto-start on app startup when the active (or first enabled) subscription
	// already has a valid selected node.
	conf := svc.config.Get()
	idx := activeSubscriptionIndex(conf)
	if idx < 0 {
		idx = firstEnabledSubscriptionIndex(conf)
	}
	if idx >= 0 {
		sub := conf.Subscriptions[idx]
		selected := strings.TrimSpace(sub.SelectedNode)
		if selected != "" && hasNodeByName(sub.Nodes, selected) {
			if err := svc.Start(); err != nil {
				log.Printf("[xray] auto-start failed: %v", err)
			} else {
				log.Printf("[xray] auto-started with subscription %s", sub.Name)
			}
		}
	}

	return nil
}

func (p *XRayPlugin) RegisterRoutes(r plugin.RouteRegistrar) {
	p.mu.RLock()
	svc := p.service
	p.mu.RUnlock()
	if svc == nil {
		return
	}
	registerRoutes(r, svc)
}

func (p *XRayPlugin) Reload() error {
	p.mu.RLock()
	svc := p.service
	p.mu.RUnlock()
	if svc == nil {
		return nil
	}
	return svc.Reload()
}

func (p *XRayPlugin) getService() *XRayService {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.service
}

// GetGlobalProxyURL implements plugin.GlobalProxyProvider.
// Returns the SOCKS5 proxy URL when globalProxy is enabled and xray is running; otherwise "".
func (p *XRayPlugin) GetGlobalProxyURL() string {
	svc := p.getService()
	if svc == nil {
		return ""
	}
	svc.mu.RLock()
	defer svc.mu.RUnlock()
	cfg := svc.config.Get()
	if !cfg.GlobalProxy || !svc.running {
		return ""
	}
	if cfg.SocksPort <= 0 || cfg.SocksPort > 65535 {
		return ""
	}
	listen := strings.TrimSpace(cfg.SocksListen)
	if listen == "" {
		listen = "127.0.0.1"
	}
	return "socks5://" + net.JoinHostPort(listen, strconv.Itoa(cfg.SocksPort))
}

// --- ConfigSyncExporter / ConfigSyncImporter / ConfigSyncDecoder ---

func (p *XRayPlugin) SyncExport(_ string) (string, json.RawMessage, error) {
	svc := p.getService()
	if svc == nil {
		return "", nil, fmt.Errorf("xray service not initialized")
	}
	data, err := json.Marshal(svc.ExportSyncData())
	if err != nil {
		return "", nil, err
	}
	return "xrayConfig", data, nil
}

func (p *XRayPlugin) SyncImport(_ string, data json.RawMessage) error {
	svc := p.getService()
	if svc == nil {
		return fmt.Errorf("xray service not initialized")
	}
	var sd XRaySyncData
	if err := json.Unmarshal(data, &sd); err != nil {
		return err
	}
	return svc.ImportSyncData(&sd)
}

func (p *XRayPlugin) SyncDecode(encoded string) (json.RawMessage, error) {
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
