package clashplugin

import (
	"fmt"
	"strconv"
	"strings"
)

const (
	nodeFieldName     = "name"
	nodeFieldType     = "type"
	nodeFieldServer   = "server"
	nodeFieldPort     = "port"
	nodeFieldSourceID = "sourceId"
	nodeFieldLatency  = "latency"
)

func cloneNode(node ProxyNode) ProxyNode {
	if node == nil {
		return ProxyNode{}
	}
	cloned, ok := cloneAny(map[string]any(node)).(map[string]any)
	if !ok {
		return ProxyNode{}
	}
	return ProxyNode(cloned)
}

func cloneAny(value any) any {
	switch v := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(v))
		for key, item := range v {
			out[key] = cloneAny(item)
		}
		return out
	case []any:
		out := make([]any, len(v))
		for i := range v {
			out[i] = cloneAny(v[i])
		}
		return out
	case []string:
		out := make([]string, len(v))
		copy(out, v)
		return out
	default:
		return v
	}
}

func nodeString(node ProxyNode, key string) string {
	if node == nil {
		return ""
	}
	raw, ok := node[key]
	if !ok || raw == nil {
		return ""
	}
	switch value := raw.(type) {
	case string:
		return strings.TrimSpace(value)
	case fmt.Stringer:
		return strings.TrimSpace(value.String())
	case int:
		return strconv.Itoa(value)
	case int64:
		return strconv.FormatInt(value, 10)
	case float64:
		return strconv.FormatFloat(value, 'f', -1, 64)
	case bool:
		if value {
			return "true"
		}
		return "false"
	default:
		return strings.TrimSpace(fmt.Sprintf("%v", value))
	}
}

func nodeInt(node ProxyNode, key string) int {
	if node == nil {
		return 0
	}
	raw, ok := node[key]
	if !ok || raw == nil {
		return 0
	}
	switch value := raw.(type) {
	case int:
		return value
	case int8:
		return int(value)
	case int16:
		return int(value)
	case int32:
		return int(value)
	case int64:
		return int(value)
	case float32:
		return int(value)
	case float64:
		return int(value)
	case string:
		if i, err := strconv.Atoi(strings.TrimSpace(value)); err == nil {
			return i
		}
	}
	return 0
}

func nodeName(node ProxyNode) string {
	return nodeString(node, nodeFieldName)
}

func nodeType(node ProxyNode) string {
	proxyType := strings.ToLower(nodeString(node, nodeFieldType))
	if proxyType == "shadowsocks" {
		return "ss"
	}
	return proxyType
}

func nodeServer(node ProxyNode) string {
	return nodeString(node, nodeFieldServer)
}

func nodePort(node ProxyNode) int {
	return nodeInt(node, nodeFieldPort)
}

func nodeSourceID(node ProxyNode) string {
	return nodeString(node, nodeFieldSourceID)
}

func nodeLatency(node ProxyNode) int {
	return nodeInt(node, nodeFieldLatency)
}

func setNodeString(node ProxyNode, key, value string) {
	if node == nil {
		return
	}
	value = strings.TrimSpace(value)
	if value == "" {
		delete(node, key)
		return
	}
	node[key] = value
}

func setNodeInt(node ProxyNode, key string, value int) {
	if node == nil {
		return
	}
	node[key] = value
}

func stripNodeMetadata(node ProxyNode) ProxyNode {
	cloned := cloneNode(node)
	delete(cloned, nodeFieldSourceID)
	delete(cloned, nodeFieldLatency)
	return cloned
}

func validateProxyNode(node ProxyNode) error {
	proxyType := nodeType(node)
	name := nodeName(node)

	switch proxyType {
	case "direct", "reject", "dns":
		return fmt.Errorf("skipped non-proxy type: %s (%s)", proxyType, name)
	case "trojan":
		if nodeServer(node) == "" || nodePort(node) == 0 || nodeString(node, "password") == "" {
			return fmt.Errorf("trojan missing required fields: %s", name)
		}
	case "vmess":
		if nodeServer(node) == "" || nodePort(node) == 0 || nodeString(node, "uuid") == "" {
			return fmt.Errorf("vmess missing required fields: %s", name)
		}
	case "vless":
		if nodeServer(node) == "" || nodePort(node) == 0 || nodeString(node, "uuid") == "" {
			return fmt.Errorf("vless missing required fields: %s", name)
		}
	case "ss":
		if nodeServer(node) == "" || nodePort(node) == 0 || nodeString(node, "password") == "" || nodeString(node, "cipher") == "" {
			return fmt.Errorf("ss missing required fields: %s", name)
		}
	case "anytls":
		if nodeServer(node) == "" || nodePort(node) == 0 || nodeString(node, "password") == "" {
			return fmt.Errorf("anytls missing required fields: %s", name)
		}
	case "hysteria2":
		if name == "" || nodeServer(node) == "" || nodePort(node) == 0 || nodeString(node, "password") == "" {
			return fmt.Errorf("hysteria2 missing required fields: %s", name)
		}
	case "tuic":
		if name == "" || nodeServer(node) == "" || nodePort(node) == 0 ||
			(nodeString(node, "token") == "" && (nodeString(node, "uuid") == "" || nodeString(node, "password") == "")) {
			return fmt.Errorf("tuic missing required fields: %s", name)
		}
	case "ssr":
		if name == "" || nodeServer(node) == "" || nodePort(node) == 0 || nodeString(node, "cipher") == "" ||
			nodeString(node, "password") == "" || nodeString(node, "obfs") == "" || nodeString(node, "protocol") == "" {
			return fmt.Errorf("ssr missing required fields: %s", name)
		}
	case "socks5", "http":
		if name == "" || nodeServer(node) == "" || nodePort(node) == 0 {
			return fmt.Errorf("%s missing required fields: %s", proxyType, name)
		}
	case "hysteria":
		if name == "" || nodeServer(node) == "" || nodePort(node) == 0 ||
			(nodeString(node, "auth_str") == "" && nodeString(node, "auth-str") == "" && nodeString(node, "auth") == "") {
			return fmt.Errorf("hysteria missing required fields: %s", name)
		}
	case "wireguard":
		if name == "" || nodeString(node, "private-key") == "" {
			return fmt.Errorf("wireguard missing required fields: %s", name)
		}
	case "ssh":
		if name == "" || nodeServer(node) == "" || nodePort(node) == 0 || nodeString(node, "username") == "" {
			return fmt.Errorf("ssh missing required fields: %s", name)
		}
	case "mieru":
		if name == "" || nodeServer(node) == "" || nodePort(node) == 0 || nodeString(node, "username") == "" || nodeString(node, "password") == "" {
			return fmt.Errorf("mieru missing required fields: %s", name)
		}
	case "snell":
		if name == "" || nodeServer(node) == "" || nodePort(node) == 0 || nodeString(node, "psk") == "" {
			return fmt.Errorf("snell missing required fields: %s", name)
		}
	case "sudoku", "masque", "openvpn":
		if name == "" {
			return fmt.Errorf("%s missing required fields: name", proxyType)
		}
	default:
		return fmt.Errorf("skipped unsupported clash type: %s (%s)", proxyType, name)
	}
	return nil
}

func normalizeProxyNode(node ProxyNode, sourceID string) ProxyNode {
	cloned := cloneNode(node)
	setNodeString(cloned, nodeFieldType, nodeType(cloned))
	applyAnyTLSDefaults(cloned)
	setNodeString(cloned, nodeFieldSourceID, sourceID)
	if nodeLatency(cloned) == 0 {
		setNodeInt(cloned, nodeFieldLatency, 0)
	}
	return cloned
}

func applyAnyTLSDefaults(node ProxyNode) {
	if nodeType(node) != "anytls" {
		return
	}
	delete(node, "username")
	delete(node, "fingerprint")
	if nodeString(node, "client-fingerprint") == "" {
		node["client-fingerprint"] = "chrome"
	}
	if _, ok := node["udp"]; !ok {
		node["udp"] = true
	}
	if len(nodeStringList(node["alpn"])) == 0 {
		node["alpn"] = []string{"h2"}
	}
	if _, ok := node["idle-session-check-interval"]; !ok {
		node["idle-session-check-interval"] = 30
	}
	if _, ok := node["idle-session-timeout"]; !ok {
		node["idle-session-timeout"] = 30
	}
	if _, ok := node["min-idle-session"]; !ok {
		node["min-idle-session"] = 0
	}
	if _, ok := node["tfo"]; !ok {
		node["tfo"] = false
	}
}
