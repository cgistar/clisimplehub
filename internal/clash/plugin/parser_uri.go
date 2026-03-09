package clashplugin

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"
	"net/url"
	"strconv"
	"strings"
)

// ParseURIList parses a base64-encoded or raw URI list into proxy nodes.
func ParseURIList(content string, sourceID string) ([]ProxyNode, []string) {
	var warnings []string

	// Try base64 decode
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
			node.SourceID = sourceID
			nodes = append(nodes, *node)
		}
	}
	return nodes, warnings
}

func tryBase64Decode(content string) []string {
	content = strings.TrimSpace(content)
	// Try standard base64
	decoded, err := base64.StdEncoding.DecodeString(content)
	if err != nil {
		// Try URL-safe base64
		decoded, err = base64.URLEncoding.DecodeString(content)
		if err != nil {
			// Try without padding
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

// parseVmessURI parses vmess://base64json
func parseVmessURI(uri string) (*ProxyNode, error) {
	payload := strings.TrimPrefix(uri, "vmess://")
	// Remove fragment
	if idx := strings.Index(payload, "#"); idx >= 0 {
		payload = payload[:idx]
	}

	decoded, err := base64Decode(payload)
	if err != nil {
		return nil, fmt.Errorf("vmess base64 decode: %w", err)
	}

	var obj map[string]interface{}
	if err := json.Unmarshal(decoded, &obj); err != nil {
		return nil, fmt.Errorf("vmess json parse: %w", err)
	}

	node := &ProxyNode{
		Type: "vmess",
		Name: getString(obj, "ps"),
	}

	node.Server = getString(obj, "add")
	node.Port = getInt(obj, "port")
	node.UUID = getString(obj, "id")
	node.AlterId = getInt(obj, "aid")
	node.Network = getString(obj, "net")
	if node.Network == "" {
		node.Network = "tcp"
	}
	node.Path = getString(obj, "path")
	node.Host = getString(obj, "host")

	tls := getString(obj, "tls")
	if tls == "tls" {
		node.Security = "tls"
	}
	node.SNI = getString(obj, "sni")
	node.Cipher = getString(obj, "scy")
	if node.Cipher == "" {
		node.Cipher = "auto"
	}
	node.PinnedPeerCertSha256 = firstNonEmptyValue(
		getString(obj, "pcs"),
		getString(obj, "pinnedPeerCertSha256"),
	)
	node.VerifyPeerCertByName = firstNonEmptyValue(
		getString(obj, "vcn"),
		getString(obj, "verifyPeerCertByName"),
	)
	if parseBoolString(getString(obj, "allowInsecure")) {
		node.AllowInsecure = true
	}

	if node.Server == "" || node.Port == 0 || node.UUID == "" {
		return nil, fmt.Errorf("vmess missing required fields (add/port/id)")
	}
	if node.Name == "" {
		node.Name = fmt.Sprintf("%s:%d", node.Server, node.Port)
	}
	return node, nil
}

// parseVlessURI parses vless://uuid@host:port?params#name
func parseVlessURI(uri string) (*ProxyNode, error) {
	parsed, err := url.Parse(uri)
	if err != nil {
		return nil, fmt.Errorf("vless url parse: %w", err)
	}

	port, _ := strconv.Atoi(parsed.Port())
	node := &ProxyNode{
		Type:   "vless",
		Name:   decodeFragment(parsed.Fragment),
		Server: parsed.Hostname(),
		Port:   port,
		UUID:   parsed.User.Username(),
	}

	q := parsed.Query()
	node.Network = q.Get("type")
	if node.Network == "" {
		node.Network = "tcp"
	}
	node.Security = q.Get("security")
	node.SNI = q.Get("sni")
	node.Flow = q.Get("flow")
	node.Mode = q.Get("mode")
	node.Path = q.Get("path")
	node.Host = q.Get("host")
	node.Fingerprint = q.Get("fp")
	node.PublicKey = q.Get("pbk")
	node.ShortId = q.Get("sid")
	node.ServerName = firstNonEmptyQueryValue(q, "servername", "serverName")
	node.SpiderX = firstNonEmptyQueryValue(q, "spx", "spiderx", "spiderX")
	node.PinnedPeerCertSha256 = firstNonEmptyQueryValue(q, "pcs", "pinnedPeerCertSha256")
	node.VerifyPeerCertByName = firstNonEmptyQueryValue(q, "vcn", "verifyPeerCertByName")
	if queryBool(q, "allowInsecure") {
		node.AllowInsecure = true
	}

	if node.Server == "" || node.Port == 0 || node.UUID == "" {
		return nil, fmt.Errorf("vless missing required fields")
	}
	if node.Name == "" {
		node.Name = fmt.Sprintf("%s:%d", node.Server, node.Port)
	}
	return node, nil
}

// parseTrojanURI parses trojan://password@host:port?params#name
func parseTrojanURI(uri string) (*ProxyNode, error) {
	parsed, err := url.Parse(uri)
	if err != nil {
		return nil, fmt.Errorf("trojan url parse: %w", err)
	}

	port, _ := strconv.Atoi(parsed.Port())
	node := &ProxyNode{
		Type:     "trojan",
		Name:     decodeFragment(parsed.Fragment),
		Server:   parsed.Hostname(),
		Port:     port,
		Password: parsed.User.Username(),
	}

	q := parsed.Query()
	node.Network = q.Get("type")
	if node.Network == "" {
		node.Network = "tcp"
	}
	node.Security = q.Get("security")
	if node.Security == "" {
		node.Security = "tls"
	}
	node.SNI = q.Get("sni")
	node.Path = q.Get("path")
	node.Host = q.Get("host")
	node.PinnedPeerCertSha256 = firstNonEmptyQueryValue(q, "pcs", "pinnedPeerCertSha256")
	node.VerifyPeerCertByName = firstNonEmptyQueryValue(q, "vcn", "verifyPeerCertByName")
	if queryBool(q, "allowInsecure") {
		node.AllowInsecure = true
	}

	if node.Server == "" || node.Port == 0 || node.Password == "" {
		return nil, fmt.Errorf("trojan missing required fields")
	}
	if node.Name == "" {
		node.Name = fmt.Sprintf("%s:%d", node.Server, node.Port)
	}
	return node, nil
}

// parseShadowsocksURI parses ss://base64(method:password)@host:port#name (SIP002)
func parseShadowsocksURI(uri string) (*ProxyNode, error) {
	// Remove ss:// prefix
	rest := strings.TrimPrefix(uri, "ss://")

	// Extract fragment (name)
	name := ""
	if idx := strings.LastIndex(rest, "#"); idx >= 0 {
		name, _ = url.PathUnescape(rest[idx+1:])
		rest = rest[:idx]
	}

	// SIP002 format: base64(method:password)@host:port
	// Legacy format: base64(method:password@host:port)
	var method, password, host string
	var port int

	if atIdx := strings.LastIndex(rest, "@"); atIdx >= 0 {
		// SIP002 format
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
		// Legacy format
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

	if host == "" || port == 0 || method == "" || password == "" {
		return nil, fmt.Errorf("ss missing required fields")
	}

	node := &ProxyNode{
		Type:     "ss",
		Name:     name,
		Server:   host,
		Port:     port,
		Password: password,
		Cipher:   method,
		Network:  "tcp",
	}
	if node.Name == "" {
		node.Name = fmt.Sprintf("%s:%d", host, port)
	}
	return node, nil
}

// base64Decode tries multiple base64 encodings.
func base64Decode(s string) ([]byte, error) {
	s = strings.TrimSpace(s)
	// Try standard
	if d, err := base64.StdEncoding.DecodeString(s); err == nil {
		return d, nil
	}
	// Try URL-safe
	if d, err := base64.URLEncoding.DecodeString(s); err == nil {
		return d, nil
	}
	// Try raw standard (no padding)
	if d, err := base64.RawStdEncoding.DecodeString(s); err == nil {
		return d, nil
	}
	// Try raw URL-safe (no padding)
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
		if v := strings.TrimSpace(q.Get(key)); v != "" {
			return v
		}
	}
	return ""
}

func queryBool(q url.Values, key string) bool {
	return parseBoolString(q.Get(key))
}

func parseBoolString(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

func getString(m map[string]interface{}, key string) string {
	v, ok := m[key]
	if !ok {
		return ""
	}
	switch val := v.(type) {
	case string:
		return val
	case float64:
		return strconv.FormatFloat(val, 'f', -1, 64)
	default:
		return fmt.Sprintf("%v", val)
	}
}

func getInt(m map[string]interface{}, key string) int {
	v, ok := m[key]
	if !ok {
		return 0
	}
	switch val := v.(type) {
	case float64:
		return int(val)
	case string:
		n, err := strconv.Atoi(val)
		if err != nil {
			log.Printf("[clash] getInt: key=%s value=%q not a number", key, val)
			return 0
		}
		return n
	default:
		return 0
	}
}
