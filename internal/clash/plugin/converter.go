package clashplugin

import (
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

const (
	roleEntry  = "entry"
	roleMiddle = "middle"
	roleExit   = "exit"
)

func BuildRuntimeYAMLForSingle(node *ProxyNode, cfg *ClashConfig) ([]byte, error) {
	if node == nil {
		return nil, fmt.Errorf("node is nil")
	}
	return buildRuntimeYAML(cfg, node, nil, node, true)
}

func BuildRuntimeYAMLForChain(entry, middle, exit *ProxyNode, cfg *ClashConfig) ([]byte, error) {
	if entry == nil || exit == nil {
		return nil, fmt.Errorf("entry/exit node is nil")
	}
	return buildRuntimeYAML(cfg, entry, middle, exit, false)
}

func buildRuntimeYAML(cfg *ClashConfig, entry, middle, exit *ProxyNode, allowSingle bool) ([]byte, error) {
	if !allowSingle {
		if sameNode(entry, exit) {
			return nil, fmt.Errorf("entry and exit cannot be the same node")
		}
		if middle != nil && (sameNode(entry, middle) || sameNode(exit, middle)) {
			return nil, fmt.Errorf("entry/middle/exit cannot reference the same node")
		}
	}

	entryProxy, err := buildProxyMap(roleEntry, entry)
	if err != nil {
		return nil, fmt.Errorf("build entry proxy: %w", err)
	}

	var middleProxy map[string]any
	if middle != nil {
		middleProxy, err = buildProxyMap(roleMiddle, middle)
		if err != nil {
			return nil, fmt.Errorf("build middle proxy: %w", err)
		}
	}

	exitProxy, err := buildProxyMap(roleExit, exit)
	if err != nil {
		return nil, fmt.Errorf("build exit proxy: %w", err)
	}

	if middle != nil {
		entryProxy["dialer-proxy"] = roleMiddle
		middleProxy["dialer-proxy"] = roleExit
	} else if !allowSingle || !sameNode(entry, exit) {
		entryProxy["dialer-proxy"] = roleExit
	}

	proxies := []any{entryProxy}
	if middleProxy != nil {
		proxies = append(proxies, middleProxy)
	}
	if !(allowSingle && sameNode(entry, exit)) {
		proxies = append(proxies, exitProxy)
	}

	runtimeCfg := buildRuntimeBaseConfig(cfg)
	runtimeCfg["proxies"] = proxies
	runtimeCfg["rules"] = []string{"MATCH," + roleEntry}

	runtimeCfg, err = mergeRuntimeConfigWithUserYAML(runtimeCfg, cfg,
		"mixed-port", "bind-address", "allow-lan", "mode", "log-level", "ipv6", "proxies", "rules",
	)
	if err != nil {
		return nil, err
	}

	return yaml.Marshal(runtimeCfg)
}

func buildProxyMap(name string, node *ProxyNode) (map[string]any, error) {
	if node == nil {
		return nil, fmt.Errorf("node is nil")
	}
	cloned := stripNodeMetadata(*node)
	if err := validateProxyNode(cloned); err != nil {
		return nil, err
	}
	if strings.TrimSpace(name) == "" {
		return nil, fmt.Errorf("proxy name is required")
	}
	cloned["name"] = strings.TrimSpace(name)
	cloned["type"] = nodeType(cloned)
	if _, ok := cloned["udp"]; !ok {
		cloned["udp"] = true
	}
	return map[string]any(cloned), nil
}

func clashLogLevel(cfg *ClashConfig) string {
	if cfg == nil {
		return "warning"
	}
	switch strings.ToLower(strings.TrimSpace(cfg.LogLevel)) {
	case "debug", "info", "warning", "error", "silent":
		return strings.ToLower(strings.TrimSpace(cfg.LogLevel))
	case "none":
		return "silent"
	default:
		return "warning"
	}
}

func socksListen(cfg *ClashConfig) string {
	if cfg == nil || strings.TrimSpace(cfg.SocksListen) == "" {
		return defaultConfig.SocksListen
	}
	return cfg.SocksListen
}

func socksPort(cfg *ClashConfig) int {
	if cfg == nil || cfg.SocksPort <= 0 {
		return defaultConfig.SocksPort
	}
	return cfg.SocksPort
}

func sameNode(a, b *ProxyNode) bool {
	if a == nil || b == nil {
		return false
	}
	return strings.TrimSpace(nodeSourceID(*a)) == strings.TrimSpace(nodeSourceID(*b)) &&
		strings.TrimSpace(nodeName(*a)) == strings.TrimSpace(nodeName(*b))
}
