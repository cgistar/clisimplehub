package clashplugin

import (
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

const converterSettingsFilename = "clash-subscription-settings.json"

type converterSettings struct {
	ProxyTestURL    string
	TestInterval    int
	Countries       []converterMatcher
	CustomGroups    []converterMatcher
	ProxyGroups     []converterProxyGroup
	OtherRules      []converterRuleGroup
	GroupBaseOption map[string]any
	LandingNodeURL  string
	WSOpts          map[string]any
	Mihomo          map[string]any
	SingBox         map[string]any
}

type converterMatcher struct {
	Key     string
	Name    string
	Keys    []string
	Pattern *regexp.Regexp
	NotAuto bool
}

type converterProxyGroup struct {
	Name        string   `json:"name"`
	Type        string   `json:"type"`
	Proxies     []string `json:"proxies"`
	Rules       []string `json:"rules,omitempty"`
	Hosts       []string `json:"hosts,omitempty"`
	RuleSet     []string `json:"rule_set,omitempty"`
	Default     bool     `json:"default,omitempty"`
	SkipResolve bool     `json:"skip_resolve,omitempty"`
}

type converterRuleGroup struct {
	Path    string   `json:"path"`
	Rules   []string `json:"rules,omitempty"`
	Hosts   []string `json:"hosts,omitempty"`
	RuleSet []string `json:"rule_set,omitempty"`
}

type converterSettingsFile struct {
	ProxyTestURL    string                          `json:"proxy-test-url"`
	TestInterval    int                             `json:"test-interval"`
	Countries       map[string][]string             `json:"countrys"`
	CountriesAlt    map[string][]string             `json:"countries"`
	CustomGroup     map[string]converterCustomGroup `json:"custom_group"`
	ProxyGroups     []converterProxyGroup           `json:"proxy_groups"`
	OtherRules      []converterRuleGroup            `json:"other_rules"`
	GroupBaseOption map[string]any                  `json:"groupBaseOption"`
	LandingNodeURL  string                          `json:"landingNode"`
	WSOpts          map[string]any                  `json:"ws-opts"`
	Mihomo          map[string]any                  `json:"mihomo"`
	SingBox         map[string]any                  `json:"singbox"`
}

type converterCustomGroup struct {
	Name    string   `json:"name"`
	Keys    []string `json:"keys"`
	NotAuto bool     `json:"notAuto"`
}

func loadConverterSettings(dataDir string) *converterSettings {
	settings := defaultConverterSettings()
	for _, name := range []string{converterSettingsFilename, "setting.json"} {
		path := filepath.Join(dataDir, name)
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		if loaded, err := parseConverterSettings(data); err == nil {
			return loaded
		} else {
			log.Printf("[clash] subscription settings parse failed %s: %v", path, err)
		}
	}
	return settings
}

func parseConverterSettings(data []byte) (*converterSettings, error) {
	var file converterSettingsFile
	if err := json.Unmarshal(data, &file); err != nil {
		return nil, err
	}

	settings := defaultConverterSettings()
	if strings.TrimSpace(file.ProxyTestURL) != "" {
		settings.ProxyTestURL = strings.TrimSpace(file.ProxyTestURL)
	}
	if file.TestInterval > 0 {
		settings.TestInterval = file.TestInterval
	}
	if len(file.GroupBaseOption) > 0 {
		settings.GroupBaseOption = cloneAnyMap(file.GroupBaseOption)
	}
	if len(file.Mihomo) > 0 {
		settings.Mihomo = cloneAnyMap(file.Mihomo)
	}
	if len(file.SingBox) > 0 {
		settings.SingBox = cloneAnyMap(file.SingBox)
	}
	if len(file.ProxyGroups) > 0 {
		settings.ProxyGroups = normalizeConverterProxyGroups(file.ProxyGroups)
	}
	if len(file.OtherRules) > 0 {
		settings.OtherRules = normalizeConverterRuleGroups(file.OtherRules)
	}
	if strings.TrimSpace(file.LandingNodeURL) != "" {
		settings.LandingNodeURL = strings.TrimSpace(file.LandingNodeURL)
	}
	if len(file.WSOpts) > 0 {
		settings.WSOpts = cloneAnyMap(file.WSOpts)
	}
	if len(file.CountriesAlt) > 0 {
		file.Countries = file.CountriesAlt
	}
	if len(file.Countries) > 0 {
		settings.Countries = matchersFromCountryMap(file.Countries)
	}
	if len(file.CustomGroup) > 0 {
		settings.CustomGroups = matchersFromCustomGroupMap(file.CustomGroup)
	}
	return settings, nil
}

func defaultConverterSettings() *converterSettings {
	countries := []converterMatcher{
		newMatcher("hk", "香港节点", []string{"🇭🇰", "香港", "港", "HK", "Hong Kong", "HongKong"}),
		newMatcher("tw", "台湾节点", []string{"🇹🇼", "台湾", "台", "TW", "Taiwan"}),
		newMatcher("sg", "新加坡节点", []string{"🇸🇬", "新加坡", "狮城", "SG", "Singapore"}),
		newMatcher("jp", "日本节点", []string{"🇯🇵", "日本", "日", "东京", "大阪", "JP", "Japan"}),
		newMatcher("kr", "韩国节点", []string{"🇰🇷", "韩国", "KR", "Korea", "首尔", "韩", "韓"}),
		newMatcher("us", "美国节点", []string{"🇺🇸", "美国", "美", "US", "United States", "洛杉矶", "圣何塞", "西雅图", "纽约"}),
		newMatcher("gb", "英国节点", []string{"🇬🇧", "英国", "UK", "GB", "United Kingdom"}),
		newMatcher("de", "德国节点", []string{"🇩🇪", "德国", "DE", "Germany"}),
		newMatcher("fr", "法国节点", []string{"🇫🇷", "法国", "FR", "France"}),
		newMatcher("ca", "加拿大节点", []string{"🇨🇦", "加拿大", "CA", "Canada"}),
		newMatcher("au", "澳大利亚节点", []string{"🇦🇺", "澳大利亚", "AU", "Australia"}),
		newMatcher("ru", "俄罗斯节点", []string{"🇷🇺", "俄罗斯", "RU", "Russia"}),
		newMatcher("in", "印度节点", []string{"🇮🇳", "印度", "IN", "India"}),
		newMatcher("my", "马来西亚节点", []string{"🇲🇾", "马来西亚", "MY", "Malaysia"}),
		newMatcher("th", "泰国节点", []string{"🇹🇭", "泰国", "TH", "Thailand"}),
		newMatcher("vn", "越南节点", []string{"🇻🇳", "越南", "VN", "Vietnam"}),
		newMatcher("id", "印度尼西亚节点", []string{"🇮🇩", "印度尼西亚", "印尼", "ID", "Indonesia"}),
		newMatcher("ae", "阿联酋节点", []string{"🇦🇪", "阿联酋", "迪拜", "AE", "UAE", "Dubai"}),
	}
	custom := []converterMatcher{
		newCustomMatcher("test", "🪲测试线路", []string{"TEST", "测试"}, false),
		newCustomMatcher("vip", "👑VIP专线", []string{"VIP"}, false),
		newCustomMatcher("iplc", "🎉IPLC专线", []string{"IPLC"}, false),
		newCustomMatcher("iepl", "🎉IEPL线路", []string{"IEPL"}, false),
		newCustomMatcher("vps", "🚀VPS组", []string{"VPS"}, true),
	}
	return &converterSettings{
		ProxyTestURL: "http://www.gstatic.com/generate_204",
		TestInterval: 600,
		Countries:    countries,
		CustomGroups: custom,
		GroupBaseOption: map[string]any{
			"url":       "http://www.gstatic.com/generate_204",
			"interval":  600,
			"tolerance": 50,
		},
		ProxyGroups: []converterProxyGroup{
			{Name: "🔰 节点选择", Type: "select", Proxies: []string{"@全部节点"}, Default: true},
			{Name: "♻️ 自动选择", Type: "url-test", Proxies: []string{"@全部节点"}},
			{Name: "🔰 组选择", Type: "select", Proxies: []string{"@节点组"}},
			{Name: "🤖 人工智能", Type: "select", Proxies: []string{"🔰 节点选择", "♻️ 自动选择", "@节点组"}, Rules: []string{"DOMAIN-SUFFIX,openai.com", "DOMAIN-SUFFIX,chatgpt.com", "DOMAIN-SUFFIX,anthropic.com"}},
			{Name: "📲 电报消息", Type: "select", Proxies: []string{"🔰 节点选择", "♻️ 自动选择", "@节点组"}, Rules: []string{"DOMAIN-SUFFIX,telegram.org", "DOMAIN-SUFFIX,t.me", "IP-CIDR,149.154.160.0/20,no-resolve", "IP-CIDR,91.108.0.0/16,no-resolve"}},
			{Name: "🎬 流媒体", Type: "select", Proxies: []string{"🔰 节点选择", "♻️ 自动选择", "@节点组"}, Rules: []string{"DOMAIN-SUFFIX,netflix.com", "DOMAIN-SUFFIX,youtube.com", "DOMAIN-SUFFIX,disneyplus.com", "DOMAIN-SUFFIX,spotify.com"}},
			{Name: "Ⓜ️ 微软服务", Type: "select", Proxies: []string{"DIRECT", "🔰 节点选择", "♻️ 自动选择"}, Rules: []string{"DOMAIN-SUFFIX,microsoft.com", "DOMAIN-SUFFIX,windows.net", "DOMAIN-SUFFIX,office.com"}},
			{Name: "🍎 苹果服务", Type: "select", Proxies: []string{"DIRECT", "🔰 节点选择", "♻️ 自动选择"}, Rules: []string{"DOMAIN-SUFFIX,apple.com", "DOMAIN-SUFFIX,icloud.com"}},
			{Name: "全球直连", Type: "select", Proxies: []string{"DIRECT"}},
		},
		Mihomo:  defaultMihomoTemplate(),
		SingBox: defaultSingBoxTemplate(),
	}
}

func matchersFromCountryMap(input map[string][]string) []converterMatcher {
	out := make([]converterMatcher, 0, len(input))
	for name, keys := range input {
		out = append(out, newMatcher(name, name, keys))
	}
	return out
}

func matchersFromCustomGroupMap(input map[string]converterCustomGroup) []converterMatcher {
	out := make([]converterMatcher, 0, len(input))
	for key, group := range input {
		name := strings.TrimSpace(group.Name)
		if name == "" {
			name = key
		}
		out = append(out, newCustomMatcher(key, name, group.Keys, group.NotAuto))
	}
	return out
}

func normalizeConverterProxyGroups(groups []converterProxyGroup) []converterProxyGroup {
	out := make([]converterProxyGroup, 0, len(groups))
	for _, group := range groups {
		group.Name = strings.TrimSpace(group.Name)
		group.Type = strings.TrimSpace(group.Type)
		if group.Name == "" || len(group.Proxies) == 0 {
			continue
		}
		if group.Type == "" {
			group.Type = "select"
		}
		out = append(out, group)
	}
	return out
}

func normalizeConverterRuleGroups(groups []converterRuleGroup) []converterRuleGroup {
	out := make([]converterRuleGroup, 0, len(groups))
	for _, group := range groups {
		group.Path = strings.TrimSpace(group.Path)
		if group.Path == "" {
			continue
		}
		group.Rules = compactStrings(group.Rules)
		group.Hosts = compactStrings(group.Hosts)
		group.RuleSet = compactStrings(group.RuleSet)
		if len(group.Rules) == 0 && len(group.Hosts) == 0 && len(group.RuleSet) == 0 {
			continue
		}
		out = append(out, group)
	}
	return out
}

func newCustomMatcher(key, name string, keys []string, notAuto bool) converterMatcher {
	m := newMatcher(key, name, keys)
	m.NotAuto = notAuto
	return m
}

func newMatcher(key, name string, keys []string) converterMatcher {
	m := converterMatcher{
		Key:  strings.TrimSpace(key),
		Name: strings.TrimSpace(name),
		Keys: compactStrings(keys),
	}
	m.Pattern = compileKeywords(m.Keys)
	return m
}

func compileKeywords(keys []string) *regexp.Regexp {
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		escaped := regexp.QuoteMeta(key)
		if regexp.MustCompile(`^[A-Za-z]{2,3}$`).MatchString(key) {
			parts = append(parts, `(?:^|[^A-Za-z])`+escaped+`(?:$|[^A-Za-z])`)
		} else {
			parts = append(parts, escaped)
		}
	}
	if len(parts) == 0 {
		return regexp.MustCompile("$^")
	}
	return regexp.MustCompile("(?i)(" + strings.Join(parts, "|") + ")")
}

func compactStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		key := strings.ToLower(value)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, value)
	}
	return out
}

func defaultMihomoTemplate() map[string]any {
	return map[string]any{
		"mixed-port":                7890,
		"allow-lan":                 true,
		"mode":                      "rule",
		"log-level":                 "info",
		"unified-delay":             true,
		"tcp-concurrent":            true,
		"global-client-fingerprint": "chrome",
		"profile": map[string]any{
			"store-selected": true,
		},
		"dns": map[string]any{
			"enable":                  true,
			"ipv6":                    false,
			"respect-rules":           true,
			"use-system-hosts":        false,
			"default-nameserver":      []string{"223.5.5.5", "119.29.29.29", "1.2.4.8"},
			"nameserver":              []string{"https://cloudflare-dns.com/dns-query", "https://8.8.4.4/dns-query"},
			"direct-nameserver":       []string{"https://223.5.5.5/dns-query", "https://doh.pub/dns-query"},
			"proxy-server-nameserver": []string{"https://cn.ali-oss.cn:44443/dns-query/6dafe708-d9d6-48cc-a768-e6ed3018a9ec", "https://hk.ali-oss.cn:44443/dns-query/6dafe708-d9d6-48cc-a768-e6ed3018a9ec"},
		},
		"proxies":      []any{},
		"proxy-groups": []any{},
		"rules":        []string{},
	}
}

func defaultSingBoxTemplate() map[string]any {
	return map[string]any{
		"log": map[string]any{"level": "warn", "timestamp": true},
		"inbounds": []any{
			map[string]any{"tag": "mixed-in", "type": "mixed", "listen": "::", "listen_port": 7890, "sniff": true},
		},
		"outbounds": []any{},
		"route": map[string]any{
			"auto_detect_interface": true,
			"rules": []any{
				map[string]any{"ip_is_private": true, "outbound": "DIRECT"},
			},
		},
	}
}
