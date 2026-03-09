package xrayplugin

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
)

var jsonNull = []byte("null")

// BuildRuntimeJSON generates xray-core runtime JSON for the given node and config.
func BuildRuntimeJSON(node *ProxyNode, cfg *XRayConfig) ([]byte, error) {
	if node == nil {
		return nil, fmt.Errorf("node is nil")
	}

	templateJSON := templateJSONFromConfig(cfg)
	if len(templateJSON) == 0 {
		var err error
		templateJSON, err = BuildRuntimeTemplateJSON(cfg)
		if err != nil {
			return nil, err
		}
	}

	var runtime map[string]interface{}
	if err := json.Unmarshal(templateJSON, &runtime); err != nil {
		return nil, fmt.Errorf("parse runtime template: %w", err)
	}
	if runtime == nil {
		runtime = map[string]interface{}{}
	}

	outbound, err := buildOutbound(node)
	if err != nil {
		return nil, fmt.Errorf("build outbound: %w", err)
	}

	logLevel := defaultConfig.LogLevel
	if cfg != nil && strings.TrimSpace(cfg.LogLevel) != "" {
		logLevel = cfg.LogLevel
	}
	logCfg := ensureObject(runtime, "log")
	logCfg["loglevel"] = logLevel

	setProxyOutbound(runtime, outbound)
	dialerApplied, err := applyDialerProxy(runtime, cfg)
	if err != nil {
		return nil, fmt.Errorf("apply dialer proxy: %w", err)
	}
	if dialerApplied {
		setDNSRoutingOutbound(runtime, "forward")
	}
	setRuntimeSocksInbound(runtime, cfg)

	return json.MarshalIndent(runtime, "", "  ")
}

// BuildRuntimeTemplateJSON builds the default runtime template JSON.
// This template omits socks listen/port so they can be injected at runtime.
func BuildRuntimeTemplateJSON(cfg *XRayConfig) ([]byte, error) {
	logLevel := defaultConfig.LogLevel
	if cfg != nil && strings.TrimSpace(cfg.LogLevel) != "" {
		logLevel = cfg.LogLevel
	}

	runtime := map[string]interface{}{
		"log": map[string]interface{}{
			"loglevel": logLevel,
		},
		"dns": map[string]interface{}{
			"queryStrategy": "UseIPv4",
			"servers": []interface{}{
				map[string]interface{}{
					"address":      "https://1.1.1.1/dns-query",
					"skipFallback": true,
				},
				map[string]interface{}{
					"address":      "https://8.8.8.8/dns-query",
					"skipFallback": true,
				},
			},
		},
		"inbounds": []interface{}{
			defaultSocksInbound(false, cfg),
		},
		"outbounds": []interface{}{
			map[string]interface{}{
				"tag":      "direct",
				"protocol": "freedom",
			},
			map[string]interface{}{
				"tag":      "block",
				"protocol": "blackhole",
			},
		},
		"routing": map[string]interface{}{
			"domainStrategy": "IPIfNonMatch",
			"rules": []interface{}{
				// Route XRay DNS queries through the proxy outbound.
				map[string]interface{}{
					"type":        "field",
					"protocol":    []string{"dns"},
					"outboundTag": "proxy",
				},
				// Private IP ranges (RFC 1918) - direct connection
				map[string]interface{}{
					"type":        "field",
					"ip":          []string{"10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16"},
					"outboundTag": "direct",
				},
				// Localhost and link-local - direct connection
				map[string]interface{}{
					"type":        "field",
					"ip":          []string{"127.0.0.0/8", "169.254.0.0/16", "::1/128", "fe80::/10"},
					"outboundTag": "direct",
				},
			},
		},
	}

	return json.MarshalIndent(runtime, "", "  ")
}

func templateJSONFromConfig(cfg *XRayConfig) []byte {
	if cfg == nil {
		return nil
	}
	raw := bytes.TrimSpace(cfg.Template)
	if len(raw) == 0 || bytes.Equal(raw, jsonNull) {
		return nil
	}
	return append([]byte(nil), raw...)
}

func ensureObject(root map[string]interface{}, key string) map[string]interface{} {
	if obj, ok := root[key].(map[string]interface{}); ok {
		return obj
	}
	obj := map[string]interface{}{}
	root[key] = obj
	return obj
}

func setProxyOutbound(runtime map[string]interface{}, outbound map[string]interface{}) {
	setTaggedOutbound(runtime, "proxy", outbound)
}

func setTaggedOutbound(runtime map[string]interface{}, tag string, outbound map[string]interface{}) {
	outbounds, ok := runtime["outbounds"].([]interface{})
	if !ok || len(outbounds) == 0 {
		runtime["outbounds"] = []interface{}{outbound}
		return
	}
	for i := range outbounds {
		ob, ok := outbounds[i].(map[string]interface{})
		if !ok {
			continue
		}
		outboundTag, _ := ob["tag"].(string)
		if strings.TrimSpace(outboundTag) == tag {
			outbounds[i] = outbound
			runtime["outbounds"] = outbounds
			return
		}
	}
	runtime["outbounds"] = append([]interface{}{outbound}, outbounds...)
}

// setNonPrimaryTaggedOutbound updates or appends outbound by tag, and ensures
// the outbound is not placed at index 0 (first outbound is xray default route).
func setNonPrimaryTaggedOutbound(runtime map[string]interface{}, tag string, outbound map[string]interface{}) {
	outbounds, ok := runtime["outbounds"].([]interface{})
	if !ok || len(outbounds) == 0 {
		runtime["outbounds"] = []interface{}{outbound}
		return
	}

	targetIndex := -1
	for i := range outbounds {
		ob, ok := outbounds[i].(map[string]interface{})
		if !ok {
			continue
		}
		outboundTag, _ := ob["tag"].(string)
		if strings.TrimSpace(outboundTag) == tag {
			targetIndex = i
			break
		}
	}

	if targetIndex >= 0 {
		outbounds[targetIndex] = outbound
	} else {
		outbounds = append(outbounds, outbound)
		targetIndex = len(outbounds) - 1
	}

	if targetIndex == 0 && len(outbounds) > 1 {
		first := outbounds[0]
		outbounds = append(outbounds[1:], first)
	}

	runtime["outbounds"] = outbounds
}

func findTaggedOutbound(runtime map[string]interface{}, tag string) map[string]interface{} {
	outbounds, ok := runtime["outbounds"].([]interface{})
	if !ok || len(outbounds) == 0 {
		return nil
	}

	for i := range outbounds {
		ob, ok := outbounds[i].(map[string]interface{})
		if !ok {
			continue
		}
		outboundTag, _ := ob["tag"].(string)
		if strings.TrimSpace(outboundTag) == tag {
			return ob
		}
	}

	return nil
}

func applyDialerProxy(runtime map[string]interface{}, cfg *XRayConfig) (bool, error) {
	if cfg == nil {
		return false, nil
	}

	dialerProxyID := strings.TrimSpace(cfg.DialerProxyID)
	if dialerProxyID == "" {
		return false, nil
	}

	activeIdx := activeSubscriptionIndex(cfg)
	if activeIdx < 0 {
		return false, nil
	}
	activeSub := cfg.Subscriptions[activeIdx]
	if dialerProxyID == activeSub.ID {
		return false, nil
	}

	var dialerSub *Subscription
	for i := range cfg.Subscriptions {
		if cfg.Subscriptions[i].ID == dialerProxyID {
			dialerSub = &cfg.Subscriptions[i]
			break
		}
	}
	if dialerSub == nil || !dialerSub.Enabled {
		return false, nil
	}

	selected := strings.TrimSpace(dialerSub.SelectedNode)
	if selected == "" || !hasNodeByName(dialerSub.Nodes, selected) {
		return false, nil
	}

	var forwardNode *ProxyNode
	for i := range dialerSub.Nodes {
		if dialerSub.Nodes[i].Name == selected {
			nodeCopy := dialerSub.Nodes[i]
			forwardNode = &nodeCopy
			break
		}
	}
	if forwardNode == nil {
		return false, nil
	}

	proxyOutbound := findTaggedOutbound(runtime, "proxy")
	if proxyOutbound == nil {
		return false, fmt.Errorf("proxy outbound not found")
	}
	streamSettings := ensureObject(proxyOutbound, "streamSettings")
	sockopt := ensureObject(streamSettings, "sockopt")
	sockopt["dialerProxy"] = "forward"

	forwardOutbound, err := buildOutbound(forwardNode)
	if err != nil {
		return false, fmt.Errorf("build forward outbound: %w", err)
	}
	forwardOutbound["tag"] = "forward"
	setNonPrimaryTaggedOutbound(runtime, "forward", forwardOutbound)

	return true, nil
}

func setDNSRoutingOutbound(runtime map[string]interface{}, outboundTag string) {
	routing := ensureObject(runtime, "routing")
	rules, ok := routing["rules"].([]interface{})
	if !ok || len(rules) == 0 {
		routing["rules"] = []interface{}{
			map[string]interface{}{
				"type":        "field",
				"protocol":    []string{"dns"},
				"outboundTag": outboundTag,
			},
		}
		return
	}

	for i := range rules {
		rule, ok := rules[i].(map[string]interface{})
		if !ok {
			continue
		}
		if !containsDNSProtocol(rule["protocol"]) {
			continue
		}
		rule["outboundTag"] = outboundTag
		routing["rules"] = rules
		return
	}

	rules = append([]interface{}{
		map[string]interface{}{
			"type":        "field",
			"protocol":    []string{"dns"},
			"outboundTag": outboundTag,
		},
	}, rules...)
	routing["rules"] = rules
}

func containsDNSProtocol(protocol interface{}) bool {
	switch v := protocol.(type) {
	case string:
		return strings.EqualFold(strings.TrimSpace(v), "dns")
	case []string:
		for i := range v {
			if strings.EqualFold(strings.TrimSpace(v[i]), "dns") {
				return true
			}
		}
	case []interface{}:
		for i := range v {
			s, ok := v[i].(string)
			if ok && strings.EqualFold(strings.TrimSpace(s), "dns") {
				return true
			}
		}
	}
	return false
}

func setRuntimeSocksInbound(runtime map[string]interface{}, cfg *XRayConfig) {
	inbounds, ok := runtime["inbounds"].([]interface{})
	if !ok || len(inbounds) == 0 {
		runtime["inbounds"] = []interface{}{defaultSocksInbound(true, cfg)}
		return
	}

	targetIndex := -1
	for i := range inbounds {
		ib, ok := inbounds[i].(map[string]interface{})
		if !ok {
			continue
		}
		tag, _ := ib["tag"].(string)
		if strings.TrimSpace(tag) == "socks-in" {
			targetIndex = i
			break
		}
		protocol, _ := ib["protocol"].(string)
		if strings.EqualFold(strings.TrimSpace(protocol), "socks") {
			targetIndex = i
			break
		}
	}

	if targetIndex < 0 {
		runtime["inbounds"] = append(inbounds, defaultSocksInbound(true, cfg))
		return
	}

	target, ok := inbounds[targetIndex].(map[string]interface{})
	if !ok {
		inbounds[targetIndex] = defaultSocksInbound(true, cfg)
		runtime["inbounds"] = inbounds
		return
	}

	target["listen"] = socksListen(cfg)
	target["port"] = socksPort(cfg)
	if _, ok := target["protocol"]; !ok {
		target["protocol"] = "socks"
	}
}

func defaultSocksInbound(includeListenPort bool, cfg *XRayConfig) map[string]interface{} {
	inbound := map[string]interface{}{
		"tag":      "socks-in",
		"protocol": "socks",
		"settings": map[string]interface{}{
			"auth": "noauth",
			"udp":  true,
		},
		"sniffing": map[string]interface{}{
			"enabled":      true,
			"destOverride": []string{"http", "tls"},
		},
	}
	if includeListenPort {
		inbound["listen"] = socksListen(cfg)
		inbound["port"] = socksPort(cfg)
	}
	return inbound
}

func socksListen(cfg *XRayConfig) string {
	if cfg == nil || strings.TrimSpace(cfg.SocksListen) == "" {
		return defaultConfig.SocksListen
	}
	return cfg.SocksListen
}

func socksPort(cfg *XRayConfig) int {
	if cfg == nil || cfg.SocksPort <= 0 {
		return defaultConfig.SocksPort
	}
	return cfg.SocksPort
}

func buildOutbound(node *ProxyNode) (map[string]interface{}, error) {
	outbound := map[string]interface{}{
		"tag": "proxy",
	}

	switch node.Type {
	case "vmess":
		outbound["protocol"] = "vmess"
		outbound["settings"] = buildVmessSettings(node)
	case "vless":
		outbound["protocol"] = "vless"
		outbound["settings"] = buildVlessSettings(node)
	case "trojan":
		outbound["protocol"] = "trojan"
		outbound["settings"] = buildTrojanSettings(node)
	case "ss":
		outbound["protocol"] = "shadowsocks"
		outbound["settings"] = buildSSSettings(node)
	default:
		return nil, fmt.Errorf("unsupported protocol: %s", node.Type)
	}

	streamSettings := buildStreamSettings(node)
	if streamSettings != nil {
		outbound["streamSettings"] = streamSettings
	}

	return outbound, nil
}

func buildVmessSettings(node *ProxyNode) map[string]interface{} {
	security := node.Cipher
	if security == "" {
		security = "auto"
	}
	return map[string]interface{}{
		"vnext": []interface{}{
			map[string]interface{}{
				"address": node.Server,
				"port":    node.Port,
				"users": []interface{}{
					map[string]interface{}{
						"id":       node.UUID,
						"alterId":  node.AlterId,
						"security": security,
					},
				},
			},
		},
	}
}

func buildVlessSettings(node *ProxyNode) map[string]interface{} {
	user := map[string]interface{}{
		"id":         node.UUID,
		"encryption": "none",
	}
	if node.Flow != "" {
		user["flow"] = node.Flow
	}
	return map[string]interface{}{
		"vnext": []interface{}{
			map[string]interface{}{
				"address": node.Server,
				"port":    node.Port,
				"users":   []interface{}{user},
			},
		},
	}
}

func buildTrojanSettings(node *ProxyNode) map[string]interface{} {
	return map[string]interface{}{
		"servers": []interface{}{
			map[string]interface{}{
				"address":  node.Server,
				"port":     node.Port,
				"password": node.Password,
			},
		},
	}
}

func buildSSSettings(node *ProxyNode) map[string]interface{} {
	return map[string]interface{}{
		"servers": []interface{}{
			map[string]interface{}{
				"address":  node.Server,
				"port":     node.Port,
				"password": node.Password,
				"method":   node.Cipher,
			},
		},
	}
}

func buildStreamSettings(node *ProxyNode) map[string]interface{} {
	ss := map[string]interface{}{
		"network": node.Network,
	}

	hasContent := false

	// Transport settings
	switch node.Network {
	case "ws":
		wsSettings := map[string]interface{}{}
		if node.Path != "" {
			wsSettings["path"] = node.Path
		}
		if node.Host != "" {
			wsSettings["headers"] = map[string]interface{}{
				"Host": node.Host,
			}
		}
		ss["wsSettings"] = wsSettings
		hasContent = true
	case "grpc":
		grpcSettings := map[string]interface{}{}
		if node.Path != "" {
			grpcSettings["serviceName"] = node.Path
		}
		ss["grpcSettings"] = grpcSettings
		hasContent = true
	case "httpupgrade":
		httpUpgradeSettings := map[string]interface{}{}
		if node.Path != "" {
			httpUpgradeSettings["path"] = node.Path
		}
		if node.Host != "" {
			httpUpgradeSettings["host"] = node.Host
		}
		ss["httpupgradeSettings"] = httpUpgradeSettings
		hasContent = true
	case "xhttp":
		xhttpSettings := map[string]interface{}{}
		if node.Path != "" {
			xhttpSettings["path"] = node.Path
		}
		if node.Host != "" {
			xhttpSettings["host"] = node.Host
		}
		if node.Mode != "" {
			xhttpSettings["mode"] = node.Mode
		}
		ss["xhttpSettings"] = xhttpSettings
		hasContent = true
	}

	// Security settings
	switch node.Security {
	case "tls":
		ss["security"] = "tls"
		tlsSettings := map[string]interface{}{}
		if node.SNI != "" {
			tlsSettings["serverName"] = node.SNI
		}
		pcs := strings.TrimSpace(node.PinnedPeerCertSha256)
		vcn := strings.TrimSpace(node.VerifyPeerCertByName)
		if pcs != "" {
			tlsSettings["pinnedPeerCertSha256"] = pcs
		}
		if vcn != "" {
			tlsSettings["verifyPeerCertByName"] = vcn
		}
		// Prefer new TLS verification fields over deprecated allowInsecure.
		if node.AllowInsecure && pcs == "" && vcn == "" {
			tlsSettings["allowInsecure"] = true
		}
		if node.Fingerprint != "" {
			tlsSettings["fingerprint"] = node.Fingerprint
		}
		ss["tlsSettings"] = tlsSettings
		hasContent = true
	case "reality":
		ss["security"] = "reality"
		realitySettings := map[string]interface{}{}
		if node.PublicKey != "" {
			realitySettings["publicKey"] = node.PublicKey
		}
		if node.ShortId != "" {
			realitySettings["shortId"] = node.ShortId
		}
		if node.Fingerprint != "" {
			realitySettings["fingerprint"] = node.Fingerprint
		}
		if node.SNI != "" {
			realitySettings["serverName"] = node.SNI
		}
		ss["realitySettings"] = realitySettings
		hasContent = true
	}

	if !hasContent && node.Network == "tcp" {
		return nil
	}

	return ss
}
