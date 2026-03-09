package xrayplugin

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// clashConfig represents the minimum Clash YAML structure we need.
type clashConfig struct {
	Proxies []map[string]interface{} `yaml:"proxies"`
}

// ParseClashYAML parses a Clash-format YAML subscription into proxy nodes.
func ParseClashYAML(content string, sourceID string) ([]ProxyNode, []string) {
	var warnings []string
	var cfg clashConfig
	if err := yaml.Unmarshal([]byte(content), &cfg); err != nil {
		return nil, []string{fmt.Sprintf("yaml parse error: %v", err)}
	}

	var nodes []ProxyNode
	for _, p := range cfg.Proxies {
		node, err := parseClashProxy(p)
		if err != nil {
			warnings = append(warnings, err.Error())
			continue
		}
		if node != nil {
			node.SourceID = sourceID
			nodes = append(nodes, *node)
		}
	}
	return nodes, warnings
}

func parseClashProxy(p map[string]interface{}) (*ProxyNode, error) {
	proxyType := strings.ToLower(clashStr(p, "type"))
	name := clashStr(p, "name")

	switch proxyType {
	case "trojan":
		return parseClashTrojan(p, name)
	case "vmess":
		return parseClashVmess(p, name)
	case "vless":
		return parseClashVless(p, name)
	case "ss", "shadowsocks":
		return parseClashSS(p, name)
	default:
		return nil, fmt.Errorf("skipped unsupported clash type: %s (%s)", proxyType, name)
	}
}

func parseClashTrojan(p map[string]interface{}, name string) (*ProxyNode, error) {
	node := &ProxyNode{
		Type:     "trojan",
		Name:     name,
		Server:   clashStr(p, "server"),
		Port:     clashInt(p, "port"),
		Password: clashStr(p, "password"),
		Network:  "tcp",
		Security: "tls",
	}

	if sni := clashStr(p, "sni"); sni != "" {
		node.SNI = sni
	}
	node.AllowInsecure = clashBool(p, "skip-cert-verify")
	node.PinnedPeerCertSha256 = clashFirstNonEmptyStr(p, "pcs", "pinned-peer-cert-sha256", "pinnedPeerCertSha256")
	node.VerifyPeerCertByName = clashFirstNonEmptyStr(p, "vcn", "verify-peer-cert-by-name", "verifyPeerCertByName")

	// WebSocket transport
	if network := clashStr(p, "network"); network != "" {
		node.Network = network
	}
	if wsOpts, ok := p["ws-opts"].(map[string]interface{}); ok {
		node.Network = "ws"
		node.Path = clashStr(wsOpts, "path")
		if headers, ok := wsOpts["headers"].(map[string]interface{}); ok {
			node.Host = clashStr(headers, "Host")
		}
	}

	if node.Server == "" || node.Port == 0 || node.Password == "" {
		return nil, fmt.Errorf("trojan missing required fields: %s", name)
	}
	return node, nil
}

func parseClashVmess(p map[string]interface{}, name string) (*ProxyNode, error) {
	node := &ProxyNode{
		Type:    "vmess",
		Name:    name,
		Server:  clashStr(p, "server"),
		Port:    clashInt(p, "port"),
		UUID:    clashStr(p, "uuid"),
		AlterId: clashInt(p, "alterId"),
		Cipher:  clashStr(p, "cipher"),
		Network: "tcp",
	}
	if node.Cipher == "" {
		node.Cipher = "auto"
	}

	if network := clashStr(p, "network"); network != "" {
		node.Network = network
	}
	if clashBool(p, "tls") {
		node.Security = "tls"
	}
	node.SNI = clashStr(p, "sni")
	node.AllowInsecure = clashBool(p, "skip-cert-verify")
	node.PinnedPeerCertSha256 = clashFirstNonEmptyStr(p, "pcs", "pinned-peer-cert-sha256", "pinnedPeerCertSha256")
	node.VerifyPeerCertByName = clashFirstNonEmptyStr(p, "vcn", "verify-peer-cert-by-name", "verifyPeerCertByName")

	if wsOpts, ok := p["ws-opts"].(map[string]interface{}); ok {
		node.Network = "ws"
		node.Path = clashStr(wsOpts, "path")
		if headers, ok := wsOpts["headers"].(map[string]interface{}); ok {
			node.Host = clashStr(headers, "Host")
		}
	}

	if node.Server == "" || node.Port == 0 || node.UUID == "" {
		return nil, fmt.Errorf("vmess missing required fields: %s", name)
	}
	return node, nil
}

func parseClashVless(p map[string]interface{}, name string) (*ProxyNode, error) {
	node := &ProxyNode{
		Type:    "vless",
		Name:    name,
		Server:  clashStr(p, "server"),
		Port:    clashInt(p, "port"),
		UUID:    clashStr(p, "uuid"),
		Flow:    clashStr(p, "flow"),
		Network: "tcp",
	}

	if network := clashStr(p, "network"); network != "" {
		node.Network = network
	}

	if clashBool(p, "tls") {
		node.Security = "tls"
	}
	node.SNI = clashStr(p, "sni")
	node.AllowInsecure = clashBool(p, "skip-cert-verify")
	node.PinnedPeerCertSha256 = clashFirstNonEmptyStr(p, "pcs", "pinned-peer-cert-sha256", "pinnedPeerCertSha256")
	node.VerifyPeerCertByName = clashFirstNonEmptyStr(p, "vcn", "verify-peer-cert-by-name", "verifyPeerCertByName")

	if realityOpts, ok := p["reality-opts"].(map[string]interface{}); ok {
		node.Security = "reality"
		node.PublicKey = clashStr(realityOpts, "public-key")
		node.ShortId = clashStr(realityOpts, "short-id")
		node.ServerName = clashFirstNonEmptyStr(realityOpts, "server-name", "serverName")
		node.SpiderX = clashFirstNonEmptyStr(realityOpts, "spider-x", "spiderX", "spx")
	}

	if wsOpts, ok := p["ws-opts"].(map[string]interface{}); ok {
		node.Network = "ws"
		node.Path = clashStr(wsOpts, "path")
		if headers, ok := wsOpts["headers"].(map[string]interface{}); ok {
			node.Host = clashStr(headers, "Host")
		}
	}

	if node.Server == "" || node.Port == 0 || node.UUID == "" {
		return nil, fmt.Errorf("vless missing required fields: %s", name)
	}
	return node, nil
}

func parseClashSS(p map[string]interface{}, name string) (*ProxyNode, error) {
	node := &ProxyNode{
		Type:     "ss",
		Name:     name,
		Server:   clashStr(p, "server"),
		Port:     clashInt(p, "port"),
		Password: clashStr(p, "password"),
		Cipher:   clashStr(p, "cipher"),
		Network:  "tcp",
	}
	if node.Server == "" || node.Port == 0 || node.Password == "" || node.Cipher == "" {
		return nil, fmt.Errorf("ss missing required fields: %s", name)
	}
	return node, nil
}

// DetectAndParse auto-detects the subscription format and parses accordingly.
func DetectAndParse(content string, format string, sourceID string) ([]ProxyNode, []string) {
	format = strings.ToLower(strings.TrimSpace(format))

	if format == "clash" {
		return ParseClashYAML(content, sourceID)
	}
	if format == "uri" {
		// Some legacy subscriptions are saved as "uri" while server actually returns Clash YAML.
		// Prefer YAML parsing when the payload clearly looks like a Clash config.
		if looksLikeClashYAML(content) {
			return ParseClashYAML(content, sourceID)
		}
		return ParseURIList(content, sourceID)
	}

	// Auto-detect
	trimmed := strings.TrimSpace(content)

	// Prefer Clash YAML parsing when payload has Clash-style top-level keys.
	if looksLikeClashYAML(trimmed) {
		return ParseClashYAML(content, sourceID)
	}

	// Check for JSON: single object {} or array []
	if (strings.HasPrefix(trimmed, "{") && strings.HasSuffix(trimmed, "}")) ||
		(strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]")) {
		if nodes, warnings := parseClashJSON(trimmed, sourceID); len(nodes) > 0 {
			return nodes, warnings
		}
	}

	// Check for bare YAML array (starts with "- ")
	if strings.HasPrefix(trimmed, "- ") {
		var proxies []map[string]interface{}
		if err := yaml.Unmarshal([]byte(trimmed), &proxies); err == nil && len(proxies) > 0 {
			return parseClashProxyList(proxies, sourceID)
		}
	}

	// Try as URI list (possibly base64 encoded)
	return ParseURIList(content, sourceID)
}

func looksLikeClashYAML(content string) bool {
	trimmed := strings.TrimSpace(content)
	if trimmed == "" {
		return false
	}
	// JSON is handled separately by parseClashJSON.
	if strings.HasPrefix(trimmed, "{") || strings.HasPrefix(trimmed, "[") {
		return false
	}

	var obj map[string]interface{}
	if err := yaml.Unmarshal([]byte(trimmed), &obj); err != nil || len(obj) == 0 {
		return false
	}

	clashKeys := []string{
		"proxies",
		"proxy-groups",
		"rules",
		"proxy-providers",
		"mixed-port",
		"socks-port",
		"redir-port",
		"mode",
	}
	for _, k := range clashKeys {
		if _, ok := obj[k]; ok {
			return true
		}
	}
	return false
}

// parseClashJSON parses JSON content as Clash proxy node(s).
func parseClashJSON(content string, sourceID string) ([]ProxyNode, []string) {
	// Try as array of proxy objects
	if strings.HasPrefix(content, "[") {
		var proxies []map[string]interface{}
		if err := json.Unmarshal([]byte(content), &proxies); err == nil && len(proxies) > 0 {
			return parseClashProxyList(proxies, sourceID)
		}
	}

	// Try as object
	var obj map[string]interface{}
	if err := json.Unmarshal([]byte(content), &obj); err == nil {
		// {"proxies": [...]} wrapper form
		if raw, ok := obj["proxies"]; ok {
			if arr, ok := raw.([]interface{}); ok {
				var proxies []map[string]interface{}
				for _, item := range arr {
					if m, ok := item.(map[string]interface{}); ok {
						proxies = append(proxies, m)
					}
				}
				if len(proxies) > 0 {
					return parseClashProxyList(proxies, sourceID)
				}
			}
		}
		// Single proxy object with "type" key
		if _, ok := obj["type"]; ok {
			return parseClashProxyList([]map[string]interface{}{obj}, sourceID)
		}
	}

	return nil, nil
}

// parseClashProxyList parses a list of Clash proxy maps into ProxyNodes.
func parseClashProxyList(proxies []map[string]interface{}, sourceID string) ([]ProxyNode, []string) {
	var nodes []ProxyNode
	var warnings []string
	for _, p := range proxies {
		node, err := parseClashProxy(p)
		if err != nil {
			warnings = append(warnings, err.Error())
			continue
		}
		if node != nil {
			node.SourceID = sourceID
			nodes = append(nodes, *node)
		}
	}
	return nodes, warnings
}

// clashStr extracts a string value from a Clash proxy map.
func clashStr(m map[string]interface{}, key string) string {
	v, ok := m[key]
	if !ok {
		return ""
	}
	switch val := v.(type) {
	case string:
		return val
	case int:
		return fmt.Sprintf("%d", val)
	case float64:
		return fmt.Sprintf("%v", val)
	default:
		return fmt.Sprintf("%v", val)
	}
}

// clashInt extracts an int value from a Clash proxy map.
func clashInt(m map[string]interface{}, key string) int {
	v, ok := m[key]
	if !ok {
		return 0
	}
	switch val := v.(type) {
	case int:
		return val
	case float64:
		return int(val)
	case string:
		s := strings.TrimSpace(val)
		if s == "" {
			return 0
		}
		if n, err := strconv.Atoi(s); err == nil {
			return n
		}
		return 0
	default:
		return 0
	}
}

// clashBool extracts a bool value from a Clash proxy map.
func clashBool(m map[string]interface{}, key string) bool {
	v, ok := m[key]
	if !ok {
		return false
	}
	switch val := v.(type) {
	case bool:
		return val
	case string:
		return strings.ToLower(val) == "true"
	default:
		return false
	}
}

func clashFirstNonEmptyStr(m map[string]interface{}, keys ...string) string {
	for _, key := range keys {
		if v := strings.TrimSpace(clashStr(m, key)); v != "" {
			return v
		}
	}
	return ""
}
