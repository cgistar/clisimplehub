package clashplugin

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

// ParseURIList parses a base64-encoded or raw URI list into Clash proxy nodes.
func ParseURIList(content string, sourceID string) ([]ProxyNode, []string) {
	var warnings []string

	lines := tryBase64Decode(content)
	if lines == nil {
		lines = splitLines(content)
	}

	var nodes []ProxyNode
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		var node *ProxyNode
		var err error

		switch {
		case strings.HasPrefix(line, "vmess://"):
			node, err = parseVmessURI(line)
		case strings.HasPrefix(line, "vless://"):
			node, err = parseVlessURI(line)
		case strings.HasPrefix(line, "trojan://"):
			node, err = parseTrojanURI(line)
		case strings.HasPrefix(line, "ss://"):
			node, err = parseShadowsocksURI(line)
		default:
			scheme := line
			if idx := strings.Index(line, "://"); idx > 0 {
				scheme = line[:idx]
			}
			warnings = append(warnings, fmt.Sprintf("skipped unsupported scheme: %s", scheme))
			continue
		}

		if err != nil {
			warnings = append(warnings, fmt.Sprintf("parse error: %v", err))
			continue
		}
		if node != nil {
			nodes = append(nodes, normalizeProxyNode(*node, sourceID))
		}
	}
	return nodes, warnings
}

func tryBase64Decode(content string) []string {
	content = strings.TrimSpace(content)
	decoded, err := base64.StdEncoding.DecodeString(content)
	if err != nil {
		decoded, err = base64.URLEncoding.DecodeString(content)
		if err != nil {
			decoded, err = base64.RawStdEncoding.DecodeString(content)
			if err != nil {
				return nil
			}
		}
	}
	text := string(decoded)
	if !strings.Contains(text, "://") {
		return nil
	}
	return splitLines(text)
}

func splitLines(s string) []string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	return strings.Split(s, "\n")
}

func parseVmessURI(uri string) (*ProxyNode, error) {
	payload := strings.TrimPrefix(uri, "vmess://")
	if idx := strings.Index(payload, "#"); idx >= 0 {
		payload = payload[:idx]
	}

	decoded, err := base64Decode(payload)
	if err != nil {
		return nil, fmt.Errorf("vmess base64 decode: %w", err)
	}

	var obj map[string]any
	if err := json.Unmarshal(decoded, &obj); err != nil {
		return nil, fmt.Errorf("vmess json parse: %w", err)
	}

	node := ProxyNode{
		"name":    getString(obj, "ps"),
		"type":    "vmess",
		"server":  getString(obj, "add"),
		"port":    getInt(obj, "port"),
		"uuid":    getString(obj, "id"),
		"alterId": getInt(obj, "aid"),
		"cipher":  firstNonEmptyValue(getString(obj, "scy"), "auto"),
	}

	network := getString(obj, "net")
	if network == "" {
		network = "tcp"
	}
	node["network"] = network

	if tls := getString(obj, "tls"); tls == "tls" {
		node["tls"] = true
	}
	if sni := getString(obj, "sni"); sni != "" {
		node["sni"] = sni
	}
	if insecure, ok := firstBoolFromObject(obj, "allowInsecure", "insecure", "skip-cert-verify", "skipCertVerify"); ok {
		node["skip-cert-verify"] = insecure
	}
	if udp, ok := firstBoolFromObject(obj, "udp", "enableUDP", "enable_udp"); ok {
		node["udp"] = udp
	}
	if fingerprint := getString(obj, "fp"); fingerprint != "" {
		node["fingerprint"] = fingerprint
	}
	if pcs := firstNonEmptyValue(getString(obj, "pcs"), getString(obj, "pinnedPeerCertSha256")); pcs != "" {
		node["pinned-peer-cert-sha256"] = pcs
	}
	if vcn := firstNonEmptyValue(getString(obj, "vcn"), getString(obj, "verifyPeerCertByName")); vcn != "" {
		node["verify-peer-cert-by-name"] = vcn
	}

	path := getString(obj, "path")
	host := getString(obj, "host")
	switch network {
	case "ws":
		wsOpts := map[string]any{}
		if path != "" {
			wsOpts["path"] = path
		}
		if host != "" {
			wsOpts["headers"] = map[string]any{"Host": host}
		}
		if len(wsOpts) > 0 {
			node["ws-opts"] = wsOpts
		}
	case "grpc":
		if path != "" {
			node["grpc-opts"] = map[string]any{"grpc-service-name": path}
		}
	case "http", "httpupgrade", "xhttp":
		httpOpts := map[string]any{}
		if path != "" {
			httpOpts["path"] = []string{path}
		}
		if host != "" {
			httpOpts["headers"] = map[string]any{"Host": []string{host}}
		}
		if len(httpOpts) > 0 {
			node["http-opts"] = httpOpts
		}
	}

	if nodeName(node) == "" {
		setNodeString(node, "name", fmt.Sprintf("%s:%d", nodeServer(node), nodePort(node)))
	}
	if err := validateProxyNode(node); err != nil {
		return nil, err
	}
	return &node, nil
}

func parseVlessURI(uri string) (*ProxyNode, error) {
	parsed, err := url.Parse(uri)
	if err != nil {
		return nil, fmt.Errorf("vless url parse: %w", err)
	}

	port, _ := strconv.Atoi(parsed.Port())
	q := parsed.Query()

	node := ProxyNode{
		"name":   decodeFragment(parsed.Fragment),
		"type":   "vless",
		"server": parsed.Hostname(),
		"port":   port,
		"uuid":   parsed.User.Username(),
	}

	network := q.Get("type")
	if network == "" {
		network = "tcp"
	}
	node["network"] = network

	if flow := q.Get("flow"); flow != "" {
		node["flow"] = flow
	}
	if mode := q.Get("mode"); mode != "" {
		node["mode"] = mode
	}
	if security := q.Get("security"); security == "tls" {
		node["tls"] = true
	} else if security == "reality" {
		node["tls"] = true
		realityOpts := map[string]any{}
		if pk := q.Get("pbk"); pk != "" {
			realityOpts["public-key"] = pk
		}
		if sid := q.Get("sid"); sid != "" {
			realityOpts["short-id"] = sid
		}
		if len(realityOpts) > 0 {
			node["reality-opts"] = realityOpts
		}
	}
	if sni := q.Get("sni"); sni != "" {
		node["sni"] = sni
		node["servername"] = firstNonEmptyValue(q.Get("servername"), q.Get("serverName"), sni)
	}
	if insecure, ok := firstBoolFromQuery(q, "allowInsecure", "insecure", "skip-cert-verify", "skipCertVerify"); ok {
		node["skip-cert-verify"] = insecure
	}
	if udp, ok := firstBoolFromQuery(q, "udp"); ok {
		node["udp"] = udp
	}
	if fp := q.Get("fp"); fp != "" {
		node["client-fingerprint"] = fp
	}
	if spx := firstNonEmptyQueryValue(q, "spx", "spiderx", "spiderX"); spx != "" {
		realityOpts, _ := node["reality-opts"].(map[string]any)
		if realityOpts == nil {
			realityOpts = map[string]any{}
		}
		realityOpts["spider-x"] = spx
		node["reality-opts"] = realityOpts
	}
	if pcs := firstNonEmptyQueryValue(q, "pcs", "pinnedPeerCertSha256"); pcs != "" {
		node["pinned-peer-cert-sha256"] = pcs
	}
	if vcn := firstNonEmptyQueryValue(q, "vcn", "verifyPeerCertByName"); vcn != "" {
		node["verify-peer-cert-by-name"] = vcn
	}

	path := q.Get("path")
	host := q.Get("host")
	switch network {
	case "ws":
		wsOpts := map[string]any{}
		if path != "" {
			wsOpts["path"] = path
		}
		if host != "" {
			wsOpts["headers"] = map[string]any{"Host": host}
		}
		if len(wsOpts) > 0 {
			node["ws-opts"] = wsOpts
		}
	case "grpc":
		if path != "" {
			node["grpc-opts"] = map[string]any{"grpc-service-name": path}
		}
	case "http", "httpupgrade", "xhttp":
		httpOpts := map[string]any{}
		if path != "" {
			httpOpts["path"] = []string{path}
		}
		if host != "" {
			httpOpts["headers"] = map[string]any{"Host": []string{host}}
		}
		if len(httpOpts) > 0 {
			node["http-opts"] = httpOpts
		}
	}

	if nodeName(node) == "" {
		setNodeString(node, "name", fmt.Sprintf("%s:%d", nodeServer(node), nodePort(node)))
	}
	if err := validateProxyNode(node); err != nil {
		return nil, err
	}
	return &node, nil
}

func parseTrojanURI(uri string) (*ProxyNode, error) {
	parsed, err := url.Parse(uri)
	if err != nil {
		return nil, fmt.Errorf("trojan url parse: %w", err)
	}

	port, _ := strconv.Atoi(parsed.Port())
	q := parsed.Query()
	node := ProxyNode{
		"name":     decodeFragment(parsed.Fragment),
		"type":     "trojan",
		"server":   parsed.Hostname(),
		"port":     port,
		"password": parsed.User.Username(),
	}

	network := q.Get("type")
	if network == "" {
		network = "tcp"
	}
	node["network"] = network
	node["tls"] = true
	if sni := q.Get("sni"); sni != "" {
		node["sni"] = sni
		node["servername"] = sni
	}
	if q.Get("security") == "none" {
		delete(node, "tls")
	}
	if insecure, ok := firstBoolFromQuery(q, "allowInsecure", "insecure", "skip-cert-verify", "skipCertVerify"); ok {
		node["skip-cert-verify"] = insecure
	}
	if udp, ok := firstBoolFromQuery(q, "udp"); ok {
		node["udp"] = udp
	}
	if pcs := firstNonEmptyQueryValue(q, "pcs", "pinnedPeerCertSha256"); pcs != "" {
		node["pinned-peer-cert-sha256"] = pcs
	}
	if vcn := firstNonEmptyQueryValue(q, "vcn", "verifyPeerCertByName"); vcn != "" {
		node["verify-peer-cert-by-name"] = vcn
	}

	path := q.Get("path")
	host := q.Get("host")
	if network == "ws" {
		wsOpts := map[string]any{}
		if path != "" {
			wsOpts["path"] = path
		}
		if host != "" {
			wsOpts["headers"] = map[string]any{"Host": host}
		}
		if len(wsOpts) > 0 {
			node["ws-opts"] = wsOpts
		}
	}

	if nodeName(node) == "" {
		setNodeString(node, "name", fmt.Sprintf("%s:%d", nodeServer(node), nodePort(node)))
	}
	if err := validateProxyNode(node); err != nil {
		return nil, err
	}
	return &node, nil
}

func parseShadowsocksURI(uri string) (*ProxyNode, error) {
	rest := strings.TrimPrefix(uri, "ss://")

	name := ""
	if idx := strings.LastIndex(rest, "#"); idx >= 0 {
		name, _ = url.PathUnescape(rest[idx+1:])
		rest = rest[:idx]
	}

	var method, password, host string
	var port int

	if atIdx := strings.LastIndex(rest, "@"); atIdx >= 0 {
		userInfo := rest[:atIdx]
		serverPart := rest[atIdx+1:]

		decoded, err := base64Decode(userInfo)
		if err != nil {
			return nil, fmt.Errorf("ss userinfo base64 decode: %w", err)
		}
		parts := strings.SplitN(string(decoded), ":", 2)
		if len(parts) != 2 {
			return nil, fmt.Errorf("ss invalid userinfo format")
		}
		method = parts[0]
		password = parts[1]

		parsed, err := url.Parse("ss://" + "user@" + serverPart)
		if err != nil {
			return nil, fmt.Errorf("ss server parse: %w", err)
		}
		host = parsed.Hostname()
		port, _ = strconv.Atoi(parsed.Port())
	} else {
		decoded, err := base64Decode(rest)
		if err != nil {
			return nil, fmt.Errorf("ss legacy base64 decode: %w", err)
		}
		text := string(decoded)
		colonIdx := strings.Index(text, ":")
		atIdx := strings.LastIndex(text, "@")
		if colonIdx < 0 || atIdx < 0 || colonIdx >= atIdx {
			return nil, fmt.Errorf("ss invalid legacy format")
		}
		method = text[:colonIdx]
		password = text[colonIdx+1 : atIdx]
		serverPart := text[atIdx+1:]
		lastColon := strings.LastIndex(serverPart, ":")
		if lastColon < 0 {
			return nil, fmt.Errorf("ss missing port")
		}
		host = serverPart[:lastColon]
		port, _ = strconv.Atoi(serverPart[lastColon+1:])
	}

	node := ProxyNode{
		"name":     name,
		"type":     "ss",
		"server":   host,
		"port":     port,
		"cipher":   method,
		"password": password,
		"udp":      true,
	}
	if nodeName(node) == "" {
		setNodeString(node, "name", fmt.Sprintf("%s:%d", host, port))
	}
	if err := validateProxyNode(node); err != nil {
		return nil, err
	}
	return &node, nil
}

func base64Decode(s string) ([]byte, error) {
	s = strings.TrimSpace(s)
	if d, err := base64.StdEncoding.DecodeString(s); err == nil {
		return d, nil
	}
	if d, err := base64.URLEncoding.DecodeString(s); err == nil {
		return d, nil
	}
	if d, err := base64.RawStdEncoding.DecodeString(s); err == nil {
		return d, nil
	}
	d, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return nil, err
	}
	return d, nil
}

func decodeFragment(frag string) string {
	decoded, err := url.PathUnescape(frag)
	if err != nil {
		return frag
	}
	return decoded
}

func firstNonEmptyValue(values ...string) string {
	for _, v := range values {
		v = strings.TrimSpace(v)
		if v != "" {
			return v
		}
	}
	return ""
}

func firstNonEmptyQueryValue(q url.Values, keys ...string) string {
	for _, key := range keys {
		if value := strings.TrimSpace(q.Get(key)); value != "" {
			return value
		}
	}
	return ""
}

func queryBool(q url.Values, key string) bool {
	return parseBoolString(q.Get(key))
}

func firstBoolFromQuery(q url.Values, keys ...string) (bool, bool) {
	for _, key := range keys {
		values, ok := q[key]
		if !ok || len(values) == 0 {
			continue
		}
		if parsed, exists := parseBoolAny(values[0]); exists {
			return parsed, true
		}
		return false, true
	}
	return false, false
}

func firstBoolFromObject(obj map[string]any, keys ...string) (bool, bool) {
	for _, key := range keys {
		raw, ok := obj[key]
		if !ok {
			continue
		}
		if parsed, exists := parseBoolAny(raw); exists {
			return parsed, true
		}
		return false, true
	}
	return false, false
}

func parseBoolAny(value any) (bool, bool) {
	switch v := value.(type) {
	case nil:
		return false, false
	case bool:
		return v, true
	case int:
		return v != 0, true
	case int8:
		return v != 0, true
	case int16:
		return v != 0, true
	case int32:
		return v != 0, true
	case int64:
		return v != 0, true
	case uint:
		return v != 0, true
	case uint8:
		return v != 0, true
	case uint16:
		return v != 0, true
	case uint32:
		return v != 0, true
	case uint64:
		return v != 0, true
	case float32:
		return v != 0, true
	case float64:
		return v != 0, true
	case string:
		text := strings.TrimSpace(v)
		if text == "" {
			return false, false
		}
		return parseBoolString(text), true
	default:
		text := strings.TrimSpace(fmt.Sprintf("%v", v))
		if text == "" {
			return false, false
		}
		return parseBoolString(text), true
	}
}

func parseBoolString(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

func getString(obj map[string]any, key string) string {
	raw, ok := obj[key]
	if !ok || raw == nil {
		return ""
	}
	switch value := raw.(type) {
	case string:
		return strings.TrimSpace(value)
	case float64:
		return strconv.FormatFloat(value, 'f', -1, 64)
	default:
		return strings.TrimSpace(fmt.Sprintf("%v", value))
	}
}

func getInt(obj map[string]any, key string) int {
	raw, ok := obj[key]
	if !ok || raw == nil {
		return 0
	}
	switch value := raw.(type) {
	case int:
		return value
	case float64:
		return int(value)
	case string:
		i, _ := strconv.Atoi(strings.TrimSpace(value))
		return i
	default:
		return 0
	}
}
