package clashplugin

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
)

const defaultConfigFilename = "clash-config.json"

var defaultConfig = ClashConfig{
	SocksListen:   "127.0.0.1",
	SocksPort:     10808,
	LogLevel:      "warning",
	Chain:         ChainConfig{},
	Subscriptions: []Subscription{},
}

type configStore struct {
	mu     sync.RWMutex
	path   string
	config *ClashConfig
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
			cfg := copyConfig(&defaultConfig)
			cs.applyEnvOverrides(cfg)
			cs.config = cfg
			return nil
		}
		return err
	}

	var payload struct {
		ClashConfig
		DialerProxyID string `json:"dialerProxyId"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		return err
	}
	cfg := payload.ClashConfig
	if strings.TrimSpace(cfg.Chain.Exit.SubscriptionID) == "" {
		cfg.Chain.Exit.SubscriptionID = strings.TrimSpace(payload.DialerProxyID)
	}
	normalizeConfig(&cfg)
	cfgCopy := copyConfig(&cfg)
	cs.applyEnvOverrides(cfgCopy)
	cs.config = cfgCopy
	return nil
}

func normalizeConfig(cfg *ClashConfig) {
	if cfg.SocksListen == "" {
		cfg.SocksListen = defaultConfig.SocksListen
	}
	if cfg.SocksPort <= 0 || cfg.SocksPort > 65535 {
		cfg.SocksPort = defaultConfig.SocksPort
	}
	if strings.TrimSpace(cfg.LogLevel) == "" {
		cfg.LogLevel = defaultConfig.LogLevel
	}
	cfg.LogLevel = strings.ToLower(strings.TrimSpace(cfg.LogLevel))
	cfg.UserYAML = strings.ReplaceAll(cfg.UserYAML, "\r\n", "\n")
	if cfg.Subscriptions == nil {
		cfg.Subscriptions = []Subscription{}
	}

	for i := range cfg.Subscriptions {
		cfg.Subscriptions[i].ID = strings.TrimSpace(cfg.Subscriptions[i].ID)
		cfg.Subscriptions[i].Name = strings.TrimSpace(cfg.Subscriptions[i].Name)
		cfg.Subscriptions[i].URL = strings.TrimSpace(cfg.Subscriptions[i].URL)
		cfg.Subscriptions[i].SelectedNode = strings.TrimSpace(cfg.Subscriptions[i].SelectedNode)
		cfg.Subscriptions[i].Format = strings.TrimSpace(cfg.Subscriptions[i].Format)
		if cfg.Subscriptions[i].Format == "" {
			cfg.Subscriptions[i].Format = "auto"
		}
		if cfg.Subscriptions[i].Nodes == nil {
			cfg.Subscriptions[i].Nodes = []ProxyNode{}
		} else {
			cfg.Subscriptions[i].Nodes = normalizeSubscriptionNodes(cfg.Subscriptions[i].Nodes, cfg.Subscriptions[i].ID)
		}
	}

	cfg.Chain.Entry.SubscriptionID = strings.TrimSpace(cfg.Chain.Entry.SubscriptionID)
	cfg.Chain.Entry.NodeName = strings.TrimSpace(cfg.Chain.Entry.NodeName)
	cfg.Chain.Exit.SubscriptionID = strings.TrimSpace(cfg.Chain.Exit.SubscriptionID)
	cfg.Chain.Exit.NodeName = strings.TrimSpace(cfg.Chain.Exit.NodeName)
	if cfg.Chain.Middle != nil {
		m := &NodeRef{
			SubscriptionID: strings.TrimSpace(cfg.Chain.Middle.SubscriptionID),
			NodeName:       strings.TrimSpace(cfg.Chain.Middle.NodeName),
		}
		if m.SubscriptionID == "" {
			cfg.Chain.Middle = nil
		} else {
			cfg.Chain.Middle = m
		}
	}

	clampChainReferences(cfg)
}

func (cs *configStore) applyEnvOverrides(cfg *ClashConfig) {
	if cfg == nil {
		return
	}

	if listen := strings.TrimSpace(os.Getenv("CLASH_SOCKS_LISTEN")); listen != "" {
		cfg.SocksListen = listen
	} else if listen := strings.TrimSpace(os.Getenv("XRAY_SOCKS_LISTEN")); listen != "" {
		cfg.SocksListen = listen
	}

	portFromEnv := strings.TrimSpace(os.Getenv("CLASH_SOCKS_PORT"))
	if portFromEnv == "" {
		portFromEnv = strings.TrimSpace(os.Getenv("XRAY_SOCKS_PORT"))
	}
	if portFromEnv != "" {
		if port, err := strconv.Atoi(portFromEnv); err == nil && port > 0 && port <= 65535 {
			cfg.SocksPort = port
		}
	}
}

func (cs *configStore) Save() error {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	return cs.saveLocked()
}

func (cs *configStore) Get() *ClashConfig {
	cs.mu.RLock()
	defer cs.mu.RUnlock()
	return copyConfig(cs.config)
}

func (cs *configStore) Update(fn func(cfg *ClashConfig)) error {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	fn(cs.config)
	normalizeConfig(cs.config)
	return cs.saveLocked()
}

func (cs *configStore) saveLocked() error {
	data, err := json.MarshalIndent(cs.config, "", "  ")
	if err != nil {
		return err
	}
	dir := filepath.Dir(cs.path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmpPath := cs.path + ".tmp"
	if err := os.WriteFile(tmpPath, data, 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, cs.path); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	return nil
}

func copyConfig(c *ClashConfig) *ClashConfig {
	if c == nil {
		return copyConfig(&defaultConfig)
	}

	cp := *c
	cp.Subscriptions = make([]Subscription, len(c.Subscriptions))
	for i := range c.Subscriptions {
		cp.Subscriptions[i] = c.Subscriptions[i]
		cp.Subscriptions[i].Nodes = make([]ProxyNode, len(c.Subscriptions[i].Nodes))
		for j := range c.Subscriptions[i].Nodes {
			cp.Subscriptions[i].Nodes[j] = cloneNode(c.Subscriptions[i].Nodes[j])
		}
	}
	if c.Chain.Middle != nil {
		mid := *c.Chain.Middle
		cp.Chain.Middle = &mid
	}
	return &cp
}
