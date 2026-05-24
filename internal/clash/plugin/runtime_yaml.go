package clashplugin

import (
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

const defaultExternalController = ":9090"

var defaultHubRuntimeRulesHead = []string{
	"IP-CIDR,1.1.1.1/32," + runtimeGroupHub + ",no-resolve",
	"IP-CIDR,8.8.4.4/32," + runtimeGroupHub + ",no-resolve",
	"IP-CIDR,8.8.8.8/32," + runtimeGroupHub + ",no-resolve",
}

var defaultHubRuntimeRulesTail = []string{
	"IP-CIDR,127.0.0.0/8,DIRECT",
	"IP-CIDR,172.16.0.0/12,DIRECT",
	"IP-CIDR,192.168.0.0/16,DIRECT",
	"IP-CIDR,10.0.0.0/8,DIRECT",
	"IP-CIDR,17.0.0.0/8,DIRECT",
	"IP-CIDR,100.64.0.0/10,DIRECT",
	"IP-CIDR,224.0.0.0/4,DIRECT",
	"IP-CIDR6,fe80::/10,DIRECT",
	"DOMAIN-SUFFIX,cn,DIRECT",
	"DOMAIN-KEYWORD,-cn,DIRECT",
}

func defaultRuntimeDNSConfig() map[string]any {
	return map[string]any{
		"enable":                  true,
		"ipv6":                    false,
		"use-system-hosts":        false,
		"respect-rules":           true,
		"default-nameserver":      []string{"223.5.5.5", "119.29.29.29", "1.2.4.8"},
		"nameserver":              []string{"https://cloudflare-dns.com/dns-query", "https://77.88.8.8/dns-query"},
		"direct-nameserver":       []string{"https://223.5.5.5/dns-query", "https://doh.pub/dns-query"},
		"proxy-server-nameserver": []string{"https://cn.ali-oss.cn:44443/dns-query/6dafe708-d9d6-48cc-a768-e6ed3018a9ec", "https://hk.ali-oss.cn:44443/dns-query/6dafe708-d9d6-48cc-a768-e6ed3018a9ec"},
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
	userCfg, err := parseUserYAMLOverride(cfg)
	if err != nil {
		return nil, err
	}
	return mergeRuntimeConfigWithUserYAMLMap(base, userCfg, protectedKeys...), nil
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

func mergeRuntimeConfigWithUserYAMLPrependRules(base map[string]any, cfg *ClashConfig, protectedKeys ...string) (map[string]any, error) {
	userCfg, err := parseUserYAMLOverride(cfg)
	if err != nil {
		return nil, err
	}

	baseRules, _, err := parseRuntimeRuleList(base["rules"])
	if err != nil {
		return nil, err
	}

	userRules, hasUserRules, err := parseRuntimeRuleList(nil)
	if userCfg != nil {
		userRules, hasUserRules, err = parseRuntimeRuleList(userCfg["rules"])
	}
	if err != nil {
		return nil, err
	}

	// Keep base rules (MATCH,...) as the last rule(s) to avoid breaking group selection.
	mergedRules := baseRules
	if hasRuntimeProxyGroup(base, runtimeGroupHub) {
		headSet := make(map[string]struct{}, len(defaultHubRuntimeRulesHead))
		for _, r := range defaultHubRuntimeRulesHead {
			headSet[strings.TrimSpace(r)] = struct{}{}
		}
		tailSet := make(map[string]struct{}, len(defaultHubRuntimeRulesTail))
		for _, r := range defaultHubRuntimeRulesTail {
			tailSet[strings.TrimSpace(r)] = struct{}{}
		}

		filteredUser := make([]string, 0, len(userRules))
		for _, r := range userRules {
			key := strings.TrimSpace(r)
			if key == "" {
				continue
			}
			if _, ok := headSet[key]; ok {
				continue
			}
			if _, ok := tailSet[key]; ok {
				continue
			}
			filteredUser = append(filteredUser, key)
		}
		filteredUser = dedupeRules(filteredUser)

		combined := make([]string, 0, len(defaultHubRuntimeRulesHead)+len(filteredUser)+len(defaultHubRuntimeRulesTail))
		combined = append(combined, defaultHubRuntimeRulesHead...)
		combined = append(combined, filteredUser...)
		combined = append(combined, defaultHubRuntimeRulesTail...)
		mergedRules = append(combined, baseRules...)
	} else if hasUserRules && len(userRules) > 0 {
		mergedRules = append(append([]string(nil), dedupeRules(userRules)...), baseRules...)
	}

	filteredProtected := removeProtectedKey(protectedKeys, "rules")
	runtimeCfg := mergeRuntimeConfigWithUserYAMLMap(base, userCfg, filteredProtected...)
	runtimeCfg["rules"] = mergedRules
	return runtimeCfg, nil
}

func hasRuntimeProxyGroup(cfg map[string]any, groupName string) bool {
	groupName = strings.TrimSpace(groupName)
	if cfg == nil || groupName == "" {
		return false
	}
	raw := cfg["proxy-groups"]
	if raw == nil {
		return false
	}

	switch typed := raw.(type) {
	case []any:
		for i := range typed {
			m, ok := typed[i].(map[string]any)
			if !ok || m == nil {
				continue
			}
			if strings.TrimSpace(fmt.Sprint(m["name"])) == groupName {
				return true
			}
		}
	}
	return false
}

func dedupeRules(rules []string) []string {
	if len(rules) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(rules))
	out := make([]string, 0, len(rules))
	for _, rule := range rules {
		rule = strings.TrimSpace(rule)
		if rule == "" {
			continue
		}
		if _, ok := seen[rule]; ok {
			continue
		}
		seen[rule] = struct{}{}
		out = append(out, rule)
	}
	return out
}

func mergeRuntimeConfigWithUserYAMLMap(base map[string]any, userCfg map[string]any, protectedKeys ...string) map[string]any {
	runtimeCfg := cloneAnyMap(base)
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

	return runtimeCfg
}

func removeProtectedKey(keys []string, key string) []string {
	if len(keys) == 0 {
		return nil
	}
	key = strings.TrimSpace(key)
	if key == "" {
		return append([]string(nil), keys...)
	}
	out := make([]string, 0, len(keys))
	for _, k := range keys {
		if strings.TrimSpace(k) == key {
			continue
		}
		out = append(out, k)
	}
	return out
}

func parseRuntimeRuleList(value any) ([]string, bool, error) {
	if value == nil {
		return nil, false, nil
	}

	switch typed := value.(type) {
	case string:
		v := strings.TrimSpace(typed)
		if v == "" {
			return nil, true, nil
		}
		return []string{v}, true, nil
	case []string:
		out := make([]string, 0, len(typed))
		for _, item := range typed {
			v := strings.TrimSpace(item)
			if v == "" {
				continue
			}
			out = append(out, v)
		}
		return out, true, nil
	case []any:
		out := make([]string, 0, len(typed))
		for i := range typed {
			s, ok := typed[i].(string)
			if !ok {
				return nil, true, fmt.Errorf("rules[%d] must be a string", i)
			}
			v := strings.TrimSpace(s)
			if v == "" {
				continue
			}
			out = append(out, v)
		}
		return out, true, nil
	default:
		return nil, true, fmt.Errorf("rules must be a string list, got %T", value)
	}
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
