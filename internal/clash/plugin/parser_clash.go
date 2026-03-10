package clashplugin

import (
	"encoding/json"
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

// clashConfig represents the minimum Clash YAML structure we need.
type clashConfig struct {
	Proxies []map[string]any `yaml:"proxies"`
}

// ParseClashYAML parses a Clash-format YAML subscription into proxy nodes.
func ParseClashYAML(content string, sourceID string) ([]ProxyNode, []string) {
	var cfg clashConfig
	if err := yaml.Unmarshal([]byte(content), &cfg); err != nil {
		return nil, []string{fmt.Sprintf("yaml parse error: %v", err)}
	}

	return parseClashProxyList(cfg.Proxies, sourceID)
}

func parseClashProxy(p map[string]any) (*ProxyNode, error) {
	node := normalizeProxyNode(ProxyNode(p), "")
	if err := validateProxyNode(node); err != nil {
		return nil, err
	}
	return &node, nil
}

// DetectAndParse auto-detects the subscription format and parses accordingly.
func DetectAndParse(content string, format string, sourceID string) ([]ProxyNode, []string) {
	format = strings.ToLower(strings.TrimSpace(format))

	if format == "clash" {
		return ParseClashYAML(content, sourceID)
	}
	if format == "uri" {
		if looksLikeClashYAML(content) {
			return ParseClashYAML(content, sourceID)
		}
		return ParseURIList(content, sourceID)
	}

	trimmed := strings.TrimSpace(content)
	if looksLikeClashYAML(trimmed) {
		return ParseClashYAML(content, sourceID)
	}

	if (strings.HasPrefix(trimmed, "{") && strings.HasSuffix(trimmed, "}")) ||
		(strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]")) {
		if nodes, warnings := parseClashJSON(trimmed, sourceID); len(nodes) > 0 {
			return nodes, warnings
		}
	}

	if strings.HasPrefix(trimmed, "- ") {
		var proxies []map[string]any
		if err := yaml.Unmarshal([]byte(trimmed), &proxies); err == nil && len(proxies) > 0 {
			return parseClashProxyList(proxies, sourceID)
		}
	}

	return ParseURIList(content, sourceID)
}

func looksLikeClashYAML(content string) bool {
	trimmed := strings.TrimSpace(content)
	if trimmed == "" {
		return false
	}
	if strings.HasPrefix(trimmed, "{") || strings.HasPrefix(trimmed, "[") {
		return false
	}

	var obj map[string]any
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
	if strings.HasPrefix(content, "[") {
		var proxies []map[string]any
		if err := json.Unmarshal([]byte(content), &proxies); err == nil && len(proxies) > 0 {
			return parseClashProxyList(proxies, sourceID)
		}
	}

	var obj map[string]any
	if err := json.Unmarshal([]byte(content), &obj); err == nil {
		if raw, ok := obj["proxies"]; ok {
			if arr, ok := raw.([]any); ok {
				var proxies []map[string]any
				for _, item := range arr {
					if m, ok := item.(map[string]any); ok {
						proxies = append(proxies, m)
					}
				}
				if len(proxies) > 0 {
					return parseClashProxyList(proxies, sourceID)
				}
			}
		}
		if _, ok := obj["type"]; ok {
			return parseClashProxyList([]map[string]any{obj}, sourceID)
		}
	}

	return nil, nil
}

// parseClashProxyList parses a list of Clash proxy maps into ProxyNodes.
func parseClashProxyList(proxies []map[string]any, sourceID string) ([]ProxyNode, []string) {
	var nodes []ProxyNode
	var warnings []string
	for _, p := range proxies {
		node, err := parseClashProxy(p)
		if err != nil {
			warnings = append(warnings, err.Error())
			continue
		}
		if node != nil {
			normalized := normalizeProxyNode(*node, sourceID)
			nodes = append(nodes, normalized)
		}
	}
	return nodes, warnings
}
