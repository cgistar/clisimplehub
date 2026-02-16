package xrayplugin

import (
	"encoding/json"
	"fmt"
)

// BuildRuntimeJSON generates xray-core runtime JSON for the given node and config.
func BuildRuntimeJSON(node *ProxyNode, cfg *XRayConfig) ([]byte, error) {
	outbound, err := buildOutbound(node)
	if err != nil {
		return nil, fmt.Errorf("build outbound: %w", err)
	}

	runtime := map[string]interface{}{
		"log": map[string]interface{}{
			"loglevel": cfg.LogLevel,
		},
		"inbounds": []interface{}{
			map[string]interface{}{
				"tag":      "socks-in",
				"listen":   cfg.SocksListen,
				"port":     cfg.SocksPort,
				"protocol": "socks",
				"settings": map[string]interface{}{
					"auth": "noauth",
					"udp":  true,
				},
				"sniffing": map[string]interface{}{
					"enabled":      true,
					"destOverride": []string{"http", "tls"},
				},
			},
		},
		"outbounds": []interface{}{
			outbound,
			map[string]interface{}{
				"tag":      "direct",
				"protocol": "freedom",
			},
			map[string]interface{}{
				"tag":      "block",
				"protocol": "blackhole",
			},
		},
	}

	return json.MarshalIndent(runtime, "", "  ")
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
		if node.AllowInsecure {
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
