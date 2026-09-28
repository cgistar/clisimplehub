package clashplugin

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

type singBoxINI struct {
	Groups    []singBoxINIGroup
	Rules     []singBoxINIRule
	Generate  bool
	Overwrite bool
}
type singBoxINIGroup struct {
	Name      string
	Type      string
	Members   []string
	Patterns  []*regexp.Regexp
	URL       string
	Interval  string
	Tolerance int
}
type singBoxINIRule struct{ Outbound, Source string }

func parseSubscriptionNodeOptions(r *http.Request, options *converterOptions) error {
	for _, ch := range r.URL.Query().Get("ua") {
		if ch < 32 || ch == 127 {
			return fmt.Errorf("ua must not contain control characters")
		}
	}

	for key, dst := range map[string]**bool{"scv": &options.SkipCertVerify, "udp": &options.UDP} {
		if !r.URL.Query().Has(key) {
			continue
		}
		value, err := strconv.ParseBool(r.URL.Query().Get(key))
		if err != nil {
			return fmt.Errorf("%s must be true or false", key)
		}
		*dst = &value
	}
	return nil
}

func applySingBoxNodeOptions(out map[string]any, options converterOptions) {
	if tls, ok := out["tls"].(map[string]any); ok && options.SkipCertVerify != nil {
		tls["insecure"] = *options.SkipCertVerify
	}
	if options.UDP == nil {
		return
	}
	// HTTP 和 SSH 出站不支持 UDP，也不接受 network 字段。
	switch out["type"] {
	case "shadowsocks", "vmess", "vless", "trojan", "hysteria", "hysteria2", "tuic", "socks":
		if *options.UDP {
			delete(out, "network")
		} else {
			out["network"] = "tcp"
		}
	}
}

type singBoxCustomSettings struct {
	Rules     []string `json:"ruleset"`
	Groups    []string `json:"custom_proxy_group"`
	Generate  bool     `json:"enable_rule_generator"`
	Overwrite bool     `json:"overwrite_original_rules"`
}

func (c *subscriptionConverter) loadSingBoxSettings(dataDir string) error {
	data, err := os.ReadFile(filepath.Join(dataDir, "setting.json"))
	if err != nil {
		return fmt.Errorf("read setting.json: %w", err)
	}
	var settings struct {
		SingBox map[string]any         `json:"singbox"`
		Custom  *singBoxCustomSettings `json:"custom"`
	}
	if err := json.Unmarshal(data, &settings); err != nil {
		return fmt.Errorf("parse setting.json: %w", err)
	}
	if len(settings.SingBox) == 0 {
		return fmt.Errorf("setting.json: singbox is required")
	}
	if settings.Custom == nil {
		return fmt.Errorf("setting.json: custom is required")
	}
	loaded, err := parseConverterSettings(data)
	if err != nil {
		return err
	}
	var dynamicNames []string
	for _, matcher := range append(append([]converterMatcher{}, loaded.CustomGroups...), loaded.Countries...) {
		dynamicNames = append(dynamicNames, matcher.Name)
	}
	ini, err := parseSingBoxCustom(*settings.Custom, dynamicNames...)
	if err != nil {
		return fmt.Errorf("setting.json custom: %w", err)
	}
	c.settings, c.ini = loaded, ini
	return nil
}

func parseSingBoxCustom(custom singBoxCustomSettings, dynamicNames ...string) (*singBoxINI, error) {
	ini := &singBoxINI{Generate: custom.Generate, Overwrite: custom.Overwrite}
	names := map[string]bool{"DIRECT": true, "REJECT": true, "dns-out": true, "GLOBAL": true}
	for _, name := range dynamicNames {
		if names[name] {
			return nil, fmt.Errorf("duplicate or reserved classification group: %s", name)
		}
		names[name] = true
	}
	for i, value := range custom.Groups {
		group, err := parseSingBoxINIGroup(value)
		if err != nil {
			return nil, fmt.Errorf("custom_proxy_group[%d]: %w", i, err)
		}
		if names[group.Name] {
			return nil, fmt.Errorf("duplicate or reserved group: %s", group.Name)
		}
		names[group.Name] = true
		ini.Groups = append(ini.Groups, group)
	}
	for i, value := range custom.Rules {
		outbound, source, ok := strings.Cut(value, ",")
		if !ok || strings.TrimSpace(outbound) == "" || strings.TrimSpace(source) == "" {
			return nil, fmt.Errorf("ruleset[%d]: invalid ruleset", i)
		}
		ini.Rules = append(ini.Rules, singBoxINIRule{strings.TrimSpace(outbound), strings.TrimSpace(source)})
	}
	if len(ini.Groups) == 0 {
		return nil, fmt.Errorf("config has no custom_proxy_group")
	}
	for _, group := range ini.Groups {
		for _, member := range group.Members {
			if member == "REJECT" || member == "dns-out" {
				return nil, fmt.Errorf("group %s: %s is not an outbound; use a rule action", group.Name, member)
			}
			if !names[member] && member != "@节点组" && member != "@自动选择" && member != "@全部节点" {
				return nil, fmt.Errorf("group %s: unknown outbound %s", group.Name, member)
			}
		}
	}
	for _, rule := range ini.Rules {
		if !names[rule.Outbound] || rule.Outbound == "dns-out" {
			return nil, fmt.Errorf("ruleset: unknown outbound %s", rule.Outbound)
		}
	}
	return ini, nil
}

func parseSingBoxINIGroup(value string) (singBoxINIGroup, error) {
	parts := strings.Split(value, "`")
	group := singBoxINIGroup{}
	if len(parts) < 3 {
		return group, fmt.Errorf("invalid custom_proxy_group")
	}
	group.Name, group.Type = strings.TrimSpace(parts[0]), parts[1]
	if group.Name == "" {
		return group, fmt.Errorf("empty group name")
	}
	end := len(parts)
	switch group.Type {
	case "select":
		group.Type = "selector"
	case "url-test":
		group.Type = "urltest"
		if len(parts) < 5 {
			return group, fmt.Errorf("url-test needs URL and interval")
		}
		group.URL = parts[end-2]
		timings := strings.Split(parts[end-1], ",")
		seconds, err := strconv.Atoi(timings[0])
		if err != nil || seconds <= 0 {
			return group, fmt.Errorf("invalid url-test interval")
		}
		group.Interval = (time.Duration(seconds) * time.Second).String()
		if len(timings) > 2 && timings[2] != "" {
			tolerance, err := strconv.Atoi(timings[2])
			if err != nil || tolerance < 0 {
				return group, fmt.Errorf("invalid url-test tolerance")
			}
			group.Tolerance = tolerance
		}
		end -= 2
	default:
		return group, fmt.Errorf("unsupported group type %s", group.Type)
	}
	for _, part := range parts[2:end] {
		if strings.HasPrefix(part, "[]") || part == "@节点组" || part == "@自动选择" || part == "@全部节点" {
			group.Members = append(group.Members, strings.TrimPrefix(part, "[]"))
			continue
		}
		pattern, err := regexp.Compile(part)
		if err != nil {
			return group, fmt.Errorf("group %s regex: %w", group.Name, err)
		}
		group.Patterns = append(group.Patterns, pattern)
	}
	return group, nil
}

func (c *subscriptionConverter) buildCustomSingBoxGroups(nodes []ProxyNode) ([]any, error) {
	classified := c.classifyNodes(nodes)
	names := map[string]bool{"DIRECT": true, "REJECT": true, "dns-out": true, "GLOBAL": true}
	for _, group := range c.ini.Groups {
		names[group.Name] = true
	}
	var allGroups, autoGroups []string
	autoSet := map[string]bool{}
	for _, name := range classified.autoGroupNames {
		autoSet[name] = true
	}
	var dynamic []any
	for _, group := range classified.groups {
		// “其它”不是配置的分类组，不参与自动注入；原始节点仍可通过手动切换访问。
		if group.name == "其它" {
			continue
		}
		if names[group.name] {
			return nil, fmt.Errorf("duplicate group: %s", group.name)
		}
		names[group.name] = true
		allGroups = append(allGroups, group.name)
		if autoSet[group.name] {
			autoGroups = append(autoGroups, group.name)
		}
		kind := "selector"
		if group.groupType == "url-test" {
			kind = "urltest"
		}
		out := map[string]any{"type": kind, "tag": group.name, "outbounds": group.proxies}
		if kind == "urltest" {
			out["url"] = c.settings.ProxyTestURL
			interval := intFromAny(c.settings.GroupBaseOption["interval"])
			if interval <= 0 {
				interval = c.settings.TestInterval
			}
			out["interval"] = (time.Duration(interval) * time.Second).String()
			if tolerance, ok := c.settings.GroupBaseOption["tolerance"]; ok {
				out["tolerance"] = tolerance
			}
		}
		dynamic = append(dynamic, out)
	}
	for _, node := range nodes {
		if names[nodeName(node)] {
			return nil, fmt.Errorf("node name conflicts with outbound: %s", nodeName(node))
		}
	}
	var groups []any
	for _, group := range c.ini.Groups {
		var members []string
		for _, member := range group.Members {
			switch member {
			case "@自动选择":
				members = append(members, autoGroups...)
			case "@节点组":
				members = append(members, allGroups...)
			case "@全部节点":
				members = append(members, nodeNames(nodes)...)
			default:
				members = append(members, member)
			}
		}
		for _, node := range nodes {
			for _, pattern := range group.Patterns {
				if pattern.MatchString(nodeName(node)) {
					members = append(members, nodeName(node))
					break
				}
			}
		}
		out := map[string]any{"type": group.Type, "tag": group.Name, "outbounds": dedupeStringList(members)}
		if group.Type == "urltest" {
			out["url"] = group.URL
			out["interval"] = group.Interval
			out["tolerance"] = group.Tolerance
		}
		groups = append(groups, out)
	}
	groups = append(groups, dynamic...)
	groups = append(groups, map[string]any{"type": "selector", "tag": "GLOBAL", "outbounds": append([]string{"DIRECT"}, nodeNames(nodes)...)})
	// 只保留能到达真实节点或内置出站的组，同时清理对空组的引用。
	reachable := map[string]bool{"DIRECT": true}
	for _, node := range nodes {
		reachable[nodeName(node)] = true
	}
	for changed := true; changed; {
		changed = false
		for _, raw := range groups {
			group := raw.(map[string]any)
			name := group["tag"].(string)
			if reachable[name] {
				continue
			}
			for _, member := range group["outbounds"].([]string) {
				if reachable[member] {
					reachable[name] = true
					changed = true
					break
				}
			}
		}
	}
	var result []any
	for _, raw := range groups {
		group := raw.(map[string]any)
		if !reachable[group["tag"].(string)] {
			continue
		}
		var members []string
		for _, member := range group["outbounds"].([]string) {
			if reachable[member] {
				members = append(members, member)
			}
		}
		group["outbounds"] = members
		result = append(result, group)
	}
	for _, rule := range c.ini.Rules {
		if rule.Outbound != "REJECT" && !reachable[rule.Outbound] {
			return nil, fmt.Errorf("ruleset targets empty group: %s", rule.Outbound)
		}
	}
	if !reachable[c.ini.Groups[0].Name] {
		return nil, fmt.Errorf("default group is empty: %s", c.ini.Groups[0].Name)
	}
	return result, nil
}

func (c *subscriptionConverter) buildINIRules(ctx context.Context) ([]any, string, error) {
	rules := []any{
		map[string]any{"action": "sniff"},
		map[string]any{"clash_mode": "Global", "outbound": "GLOBAL"},
		map[string]any{"clash_mode": "Direct", "outbound": "DIRECT"},
		map[string]any{"protocol": "dns", "action": "hijack-dns"},
	}
	final := ""
	if !c.ini.Generate {
		return rules, final, nil
	}
	prefetched := c.prefetchINIRuleHosts(ctx)
	for _, source := range c.ini.Rules {
		if strings.EqualFold(source.Source, "[]FINAL") || strings.EqualFold(source.Source, "[]MATCH") {
			if source.Outbound == "REJECT" {
				rules = append(rules, singBoxRuleAction(source.Outbound))
				continue
			}
			final = source.Outbound
			continue
		}
		var lines []string
		if strings.HasPrefix(source.Source, "[]") {
			lines = []string{strings.TrimPrefix(source.Source, "[]")}
		} else {
			result := prefetched[source.Source]
			if result.err != nil {
				return nil, "", fmt.Errorf("ruleset %s: %w", source.Source, result.err)
			}
			lines = result.rules
		}
		merged := singBoxRuleAction(source.Outbound)
		for _, line := range lines {
			kind, _, _ := strings.Cut(line, ",")
			kind = strings.ToUpper(strings.TrimSpace(kind))
			// sing-box 无法匹配 HTTP UA 或 URL；与示例一致跳过，并通过响应头明确报告。
			if kind == "USER-AGENT" || kind == "URL-REGEX" {
				c.addSubscriptionWarning("skipped unsupported rule type: " + kind)
				log.Printf("[clash] sing-box ruleset %s: skipped unsupported %s", source.Source, kind)
				continue
			}
			rule := mihomoRuleToSingBox(line, source.Outbound)
			if rule == nil {
				return nil, "", fmt.Errorf("ruleset %s: unsupported rule %q", source.Source, line)
			}
			for key, value := range rule {
				if key == "outbound" || key == "action" {
					continue
				}
				values, ok := value.([]string)
				if !ok {
					return nil, "", fmt.Errorf("unsupported rule field %s", key)
				}
				previous, _ := merged[key].([]string)
				merged[key] = append(previous, values...)
			}
		}
		if len(merged) > 1 {
			rules = append(rules, merged)
		}
	}
	return rules, final, nil
}
