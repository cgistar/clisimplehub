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
	default:
		return fmt.Errorf("skipped unsupported clash type: %s (%s)", proxyType, name)
	}
	return nil
}

func normalizeProxyNode(node ProxyNode, sourceID string) ProxyNode {
	cloned := cloneNode(node)
	setNodeString(cloned, nodeFieldType, nodeType(cloned))
	setNodeString(cloned, nodeFieldSourceID, sourceID)
	if nodeLatency(cloned) == 0 {
		setNodeInt(cloned, nodeFieldLatency, 0)
	}
	return cloned
}
