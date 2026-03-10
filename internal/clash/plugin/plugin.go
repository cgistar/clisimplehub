package clashplugin

import (
	"bytes"
	"compress/gzip"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"sync"

	"clisimplehub/internal/plugin"
)

func init() {
	plugin.Register(&ClashPlugin{})
}

// ClashPlugin implements plugin.Plugin for clash client proxy.
type ClashPlugin struct {
	desktopFacade
	service *ClashService
	mu      sync.RWMutex
}

func (p *ClashPlugin) Name() string { return "clash" }

func (p *ClashPlugin) Init(cfg plugin.InitConfig) error {
	cfgPath := configPathFromAppConfig(cfg.ConfigPath)

	svc, err := NewClashService(cfgPath)
	if err != nil {
		return err
	}

	p.mu.Lock()
	p.service = svc
	p.mu.Unlock()

	if runtimeYAML, ready, err := buildRuntimeYAMLForConfig(svc.config.Get()); err != nil {
		log.Printf("[clash] auto-start skipped (invalid chain): %v", err)
	} else if ready {
		if err := svc.Start(); err != nil {
			log.Printf("[clash] auto-start failed: %v", err)
		} else {
			log.Printf("[clash] auto-started")
		}
		_ = runtimeYAML
	}

	return nil
}

func (p *ClashPlugin) RegisterRoutes(r plugin.RouteRegistrar) {
	p.mu.RLock()
	svc := p.service
	p.mu.RUnlock()
	if svc == nil {
		return
	}
	registerRoutes(r, svc)
}

func (p *ClashPlugin) Reload() error {
	p.mu.RLock()
	svc := p.service
	p.mu.RUnlock()
	if svc == nil {
		return nil
	}
	return svc.Reload()
}

func (p *ClashPlugin) getService() *ClashService {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.service
}

func (p *ClashPlugin) SyncExport(_ string) (string, json.RawMessage, error) {
	svc := p.getService()
	if svc == nil {
		return "", nil, fmt.Errorf("clash service not initialized")
	}
	data, err := json.Marshal(svc.ExportSyncData())
	if err != nil {
		return "", nil, err
	}
	return "clashConfig", data, nil
}

func (p *ClashPlugin) SyncImport(_ string, data json.RawMessage) error {
	svc := p.getService()
	if svc == nil {
		return fmt.Errorf("clash service not initialized")
	}
	var sd ClashSyncData
	if err := json.Unmarshal(data, &sd); err != nil {
		return err
	}
	return svc.ImportSyncData(&sd)
}

func (p *ClashPlugin) SyncDecode(encoded string) (json.RawMessage, error) {
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
