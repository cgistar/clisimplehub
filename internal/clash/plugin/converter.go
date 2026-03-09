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
		if middle != nil {
			if sameNode(entry, middle) || sameNode(exit, middle) {
				return nil, fmt.Errorf("entry/middle/exit cannot reference the same node")
			}
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

	runtimeCfg := map[string]any{
		"socks-port":   socksPort(cfg),
		"bind-address": socksListen(cfg),
		"allow-lan":    false,
		"mode":         "rule",
		"log-level":    clashLogLevel(cfg),
		"ipv6":         false,
		"dns": map[string]any{
			"enable":             true,
			"ipv6":               false,
			"default-nameserver": []string{"223.5.5.5", "1.1.1.1"},
			"nameserver":         []string{"https://1.1.1.1/dns-query", "https://8.8.8.8/dns-query"},
		},
		"proxies": proxies,
		"rules":   []string{"MATCH," + roleEntry},
	}

	return yaml.Marshal(runtimeCfg)
}

func buildProxyMap(name string, node *ProxyNode) (map[string]any, error) {
	if node == nil {
		return nil, fmt.Errorf("node is nil")
	}
	if strings.TrimSpace(node.Server) == "" || node.Port <= 0 {
		return nil, fmt.Errorf("invalid server/port")
	}

	proxy := map[string]any{
		"name":   strings.TrimSpace(name),
		"type":   node.Type,
		"server": node.Server,
		"port":   node.Port,
		"udp":    true,
	}
	if proxy["name"] == "" {
		return nil, fmt.Errorf("proxy name is required")
	}

	switch node.Type {
	case "vmess":
		proxy["uuid"] = node.UUID
		// mihomo vmess parser expects alterId key to exist; default to 0.
		proxy["alterId"] = node.AlterId
		if strings.TrimSpace(node.Cipher) != "" {
			proxy["cipher"] = node.Cipher
		} else {
			proxy["cipher"] = "auto"
		}
	case "vless":
		proxy["uuid"] = node.UUID
		proxy["alterId"] = 0
		proxy["cipher"] = "auto"
		if strings.TrimSpace(node.Flow) != "" {
			proxy["flow"] = node.Flow
		}
	case "trojan":
		proxy["password"] = node.Password
	case "ss":
		proxy["cipher"] = node.Cipher
		proxy["password"] = node.Password
	default:
		return nil, fmt.Errorf("unsupported protocol: %s", node.Type)
	}

	applyNetwork(proxy, node)
	applySecurity(proxy, node)
	return proxy, nil
}

func applyNetwork(proxy map[string]any, node *ProxyNode) {
	network := strings.ToLower(strings.TrimSpace(node.Network))
	if network == "" {
		network = "tcp"
	}

	switch network {
	case "xhttp", "httpupgrade":
		network = "http"
	}

	// Keep default tcp explicit for compatibility with strict config consumers.
	proxy["network"] = network

	switch network {
	case "ws":
		wsOpts := map[string]any{}
		if strings.TrimSpace(node.Path) != "" {
			wsOpts["path"] = node.Path
		}
		if strings.TrimSpace(node.Host) != "" {
			wsOpts["headers"] = map[string]any{"Host": node.Host}
		}
		if len(wsOpts) > 0 {
			proxy["ws-opts"] = wsOpts
		}
	case "grpc":
		if strings.TrimSpace(node.Path) != "" {
			proxy["grpc-opts"] = map[string]any{
				"grpc-service-name": node.Path,
			}
		}
	case "http":
		httpOpts := map[string]any{}
		if strings.TrimSpace(node.Path) != "" {
			httpOpts["path"] = []string{node.Path}
		}
		if strings.TrimSpace(node.Host) != "" {
			httpOpts["headers"] = map[string]any{"Host": []string{node.Host}}
		}
		if len(httpOpts) > 0 {
			proxy["http-opts"] = httpOpts
		}
	}
}

func applySecurity(proxy map[string]any, node *ProxyNode) {
	security := strings.ToLower(strings.TrimSpace(node.Security))
	sni := firstNonEmptyString(node.ServerName, node.SNI)

	switch security {
	case "tls":
		proxy["tls"] = true
		if sni != "" {
			proxy["servername"] = sni
			proxy["sni"] = sni
		}
		if node.AllowInsecure {
			proxy["skip-cert-verify"] = true
		}
		if strings.TrimSpace(node.Fingerprint) != "" {
			proxy["fingerprint"] = node.Fingerprint
		}
	case "reality":
		proxy["tls"] = true
		if sni != "" {
			proxy["servername"] = sni
			proxy["sni"] = sni
		}
		if node.AllowInsecure {
			proxy["skip-cert-verify"] = true
		}
		realityOpts := map[string]any{}
		if strings.TrimSpace(node.PublicKey) != "" {
			realityOpts["public-key"] = node.PublicKey
		}
		if strings.TrimSpace(node.ShortId) != "" {
			realityOpts["short-id"] = node.ShortId
		}
		if len(realityOpts) > 0 {
			proxy["reality-opts"] = realityOpts
		}
		if strings.TrimSpace(node.Fingerprint) != "" {
			proxy["client-fingerprint"] = node.Fingerprint
		}
	default:
		if sni != "" && (node.Type == "trojan" || node.Type == "vless" || node.Type == "vmess") {
			proxy["sni"] = sni
		}
	}
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
	return strings.TrimSpace(a.SourceID) == strings.TrimSpace(b.SourceID) &&
		strings.TrimSpace(a.Name) == strings.TrimSpace(b.Name)
}

func firstNonEmptyString(values ...string) string {
	for i := range values {
		v := strings.TrimSpace(values[i])
		if v != "" {
			return v
		}
	}
	return ""
}
