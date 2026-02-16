package xrayplugin

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"sync"
)

const defaultConfigFilename = "xray-config.json"

var defaultConfig = XRayConfig{
	SocksListen:   "127.0.0.1",
	SocksPort:     10808,
	LogLevel:      "warning",
	Subscriptions: []Subscription{},
}

type configStore struct {
	mu       sync.RWMutex
	path     string
	config   *XRayConfig
}

func newConfigStore(configPath string) *configStore {
	return &configStore{
		path:   configPath,
		config: copyConfig(&defaultConfig),
	}
}

func configPathFromAppConfig(appConfigPath string) string {
	if appConfigPath == "" {
		return defaultConfigFilename
	}
	return filepath.Join(filepath.Dir(appConfigPath), defaultConfigFilename)
}

func (cs *configStore) Load() error {
	cs.mu.Lock()
	defer cs.mu.Unlock()

	data, err := os.ReadFile(cs.path)
	if err != nil {
		if os.IsNotExist(err) {
			cs.config = copyConfig(&defaultConfig)
			cs.applyEnvOverrides()
			return nil
		}
		return err
	}

	var cfg XRayConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return err
	}
	if cfg.SocksListen == "" {
		cfg.SocksListen = defaultConfig.SocksListen
	}
	if cfg.SocksPort == 0 {
		cfg.SocksPort = defaultConfig.SocksPort
	}
	if cfg.LogLevel == "" {
		cfg.LogLevel = defaultConfig.LogLevel
	}
	if cfg.Subscriptions == nil {
		cfg.Subscriptions = []Subscription{}
	}
	cs.config = &cfg
	cs.applyEnvOverrides()
	return nil
}

// applyEnvOverrides applies environment variable overrides to the config
func (cs *configStore) applyEnvOverrides() {
	// XRAY_SOCKS_LISTEN environment variable
	if listen := os.Getenv("XRAY_SOCKS_LISTEN"); listen != "" {
		cs.config.SocksListen = listen
	}

	// XRAY_SOCKS_PORT environment variable
	if portStr := os.Getenv("XRAY_SOCKS_PORT"); portStr != "" {
		if port, err := strconv.Atoi(portStr); err == nil && port > 0 && port <= 65535 {
			cs.config.SocksPort = port
		}
	}
}

func (cs *configStore) Save() error {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	return cs.saveLocked()
}

func (cs *configStore) Get() *XRayConfig {
	cs.mu.RLock()
	defer cs.mu.RUnlock()
	return copyConfig(cs.config)
}

func (cs *configStore) Update(fn func(cfg *XRayConfig)) error {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	fn(cs.config)
	return cs.saveLocked()
}

func (cs *configStore) saveLocked() error {
	data, err := json.MarshalIndent(cs.config, "", "  ")
	if err != nil {
		return err
	}
	dir := filepath.Dir(cs.path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	tmpPath := cs.path + ".tmp"
	if err := os.WriteFile(tmpPath, data, 0600); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, cs.path); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	return nil
}

func copyConfig(c *XRayConfig) *XRayConfig {
	if c == nil {
		return copyConfig(&defaultConfig)
	}

	cp := *c
	if c.Subscriptions == nil {
		cp.Subscriptions = []Subscription{}
		return &cp
	}
	cp.Subscriptions = make([]Subscription, len(c.Subscriptions))
	for i := range c.Subscriptions {
		cp.Subscriptions[i] = c.Subscriptions[i]
		cp.Subscriptions[i].Nodes = append([]ProxyNode(nil), c.Subscriptions[i].Nodes...)
	}
	return &cp
}
