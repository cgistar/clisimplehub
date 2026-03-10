package clashplugin

import (
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

const defaultExternalController = ":9090"

func defaultRuntimeDNSConfig() map[string]any {
	return map[string]any{
		"enable":             true,
		"ipv6":               false,
		"default-nameserver": []string{"223.5.5.5", "1.1.1.1"},
		"nameserver":         []string{"https://1.1.1.1/dns-query", "https://8.8.8.8/dns-query"},
	}
}

func buildRuntimeBaseConfig(cfg *ClashConfig) map[string]any {
	return map[string]any{
		"mixed-port":          socksPort(cfg),
		"bind-address":        socksListen(cfg),
		"allow-lan":           allowLAN(cfg),
		"mode":                "rule",
		"log-level":           clashLogLevel(cfg),
		"ipv6":                false,
		"dns":                 defaultRuntimeDNSConfig(),
		"external-controller": defaultExternalController,
	}
}

func mergeRuntimeConfigWithUserYAML(base map[string]any, cfg *ClashConfig, protectedKeys ...string) (map[string]any, error) {
	runtimeCfg := cloneAnyMap(base)
	userCfg, err := parseUserYAMLOverride(cfg)
	if err != nil {
		return nil, err
	}

	if len(userCfg) > 0 {
		mergeAnyMap(runtimeCfg, userCfg)
	}

	delete(runtimeCfg, "socks-port")
	for _, key := range protectedKeys {
		value, ok := base[key]
		if !ok {
			continue
		}
		runtimeCfg[key] = cloneAnyValue(value)
	}

	return runtimeCfg, nil
}

func parseUserYAMLOverride(cfg *ClashConfig) (map[string]any, error) {
	if cfg == nil || strings.TrimSpace(cfg.UserYAML) == "" {
		return nil, nil
	}

	var userCfg map[string]any
	if err := yaml.Unmarshal([]byte(cfg.UserYAML), &userCfg); err != nil {
		return nil, fmt.Errorf("parse user yaml: %w", err)
	}
	if userCfg == nil {
		return map[string]any{}, nil
	}
	return userCfg, nil
}

func allowLAN(cfg *ClashConfig) bool {
	return strings.TrimSpace(socksListen(cfg)) != "127.0.0.1"
}

func mergeAnyMap(dst, src map[string]any) {
	for key, value := range src {
		dstMap, dstIsMap := dst[key].(map[string]any)
		srcMap, srcIsMap := value.(map[string]any)
		if dstIsMap && srcIsMap && key != "dns" {
			mergeAnyMap(dstMap, srcMap)
			dst[key] = dstMap
			continue
		}
		dst[key] = cloneAnyValue(value)
	}
}

func cloneAnyMap(src map[string]any) map[string]any {
	if src == nil {
		return map[string]any{}
	}
	dst := make(map[string]any, len(src))
	for key, value := range src {
		dst[key] = cloneAnyValue(value)
	}
	return dst
}

func cloneAnyValue(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		return cloneAnyMap(typed)
	case []any:
		items := make([]any, len(typed))
		for i := range typed {
			items[i] = cloneAnyValue(typed[i])
		}
		return items
	case []string:
		items := make([]string, len(typed))
		copy(items, typed)
		return items
	default:
		return typed
	}
}
