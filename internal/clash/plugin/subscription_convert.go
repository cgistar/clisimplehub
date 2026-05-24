package clashplugin

import (
	"bytes"
	"context"
	"crypto/md5"
	"crypto/tls"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"path"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	mihomoconvert "github.com/metacubex/mihomo/common/convert"
	"gopkg.in/yaml.v3"
)

const maxMergedSubscriptionURLs = 8

type subscriptionTarget string

const (
	targetMihomo  subscriptionTarget = "mihomo"
	targetSingBox subscriptionTarget = "sing-box"
	targetBase64  subscriptionTarget = "base64"
)

type subscriptionOutput struct {
	Body        []byte
	ContentType string
	Headers     map[string]string
}

type fetchedSubscription struct {
	URL     string
	Content string
	Headers map[string]string
}

type converterWarning struct {
	URL string
	Err error
}

type subscriptionServerIPInfo struct {
	IP        []string
	ServerIPs []string
	Servers   []string
	Domain    []string
}

type subscriptionConverter struct {
	settings *converterSettings
	client   *http.Client
	baseURL  string
	options  converterOptions

	allowPrivateFetch bool
}

type converterOptions struct {
	SocksPort int
	NoRPRX    bool
	Landing   bool
	FixedNode string
	Tun       bool
	Version   string
}

type ruleHostCacheEntry struct {
	rules     []string
	expiresAt time.Time
}

type singBoxRouteBuild struct {
	Rules    []any
	RuleSets []any
}

var ruleHostCache = struct {
	sync.Mutex
	items map[string]ruleHostCacheEntry
}{items: map[string]ruleHostCacheEntry{}}

func newSubscriptionConverter(settings *converterSettings, client *http.Client, baseURL string) *subscriptionConverter {
	if settings == nil {
		settings = defaultConverterSettings()
	}
	if client == nil {
		client = defaultSubscriptionHTTPClient()
	}
	return &subscriptionConverter{
		settings: settings,
		client:   client,
		baseURL:  strings.TrimRight(strings.TrimSpace(baseURL), "/"),
	}
}

func defaultSubscriptionHTTPClient() *http.Client {
	return &http.Client{
		Timeout:   30 * time.Second,
		Transport: publicFetchTransport(),
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 5 {
				return fmt.Errorf("stopped after 5 redirects")
			}
			return validatePublicFetchURL(req.Context(), req.URL.String())
		},
	}
}

func publicFetchTransport() *http.Transport {
	base := http.DefaultTransport.(*http.Transport).Clone()
	base.DialContext = publicFetchDialContext
	base.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12}
	return base
}

func publicFetchDialContext(ctx context.Context, network, address string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, err
	}
	ips, err := resolvePublicHost(ctx, host)
	if err != nil {
		return nil, err
	}
	dialer := &net.Dialer{Timeout: 30 * time.Second}
	var lastErr error
	for _, ip := range ips {
		conn, err := dialer.DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
		if err == nil {
			return conn, nil
		}
		lastErr = err
	}
	if lastErr != nil {
		return nil, lastErr
	}
	return nil, fmt.Errorf("no public addresses for %s", host)
}

func normalizeSubscriptionTarget(raw string) (subscriptionTarget, error) {
	value := strings.ToLower(strings.TrimSpace(raw))
	if strings.Contains(value, "mihomo") || strings.HasPrefix(value, "clash.meta/") {
		return targetMihomo, nil
	}
	switch value {
	case "", "clash", "mihomo", "meta", "clash.meta", "clashmeta", "clashverge", "clash-verge":
		return targetMihomo, nil
	case "sing-box", "singbox":
		return targetSingBox, nil
	case "base64", "uri", "v2ray":
		return targetBase64, nil
	default:
		return "", fmt.Errorf("unsupported target: %s", raw)
	}
}

func splitSubscriptionURLs(values []string) []string {
	var out []string
	seen := map[string]struct{}{}
	for _, value := range values {
		for _, part := range strings.Split(value, "|") {
			part = strings.TrimSpace(part)
			if part == "" {
				continue
			}
			if _, ok := seen[part]; ok {
				continue
			}
			seen[part] = struct{}{}
			out = append(out, part)
		}
	}
	return out
}

func (c *subscriptionConverter) Convert(ctx context.Context, urls []string, target subscriptionTarget) (*subscriptionOutput, error) {
	if len(urls) == 0 {
		return nil, fmt.Errorf("url is required")
	}
	if len(urls) > maxMergedSubscriptionURLs {
		return nil, fmt.Errorf("too many subscription urls: max %d", maxMergedSubscriptionURLs)
	}

	var allNodes []ProxyNode
	var fetched []fetchedSubscription
	var failures []converterWarning
	for i, rawURL := range urls {
		sub, err := c.fetch(ctx, rawURL)
		if err != nil {
			failures = append(failures, converterWarning{URL: rawURL, Err: err})
			log.Printf("[clash] subscription convert fetch skipped %s: %v", rawURL, err)
			continue
		}
		fetched = append(fetched, *sub)
		nodes, warnings := parseSubscriptionNodes(sub.Content, fmt.Sprintf("sub-%d", i+1))
		for _, warning := range warnings {
			log.Printf("[clash] subscription convert %s: %s", rawURL, warning)
		}
		allNodes = append(allNodes, nodes...)
	}

	nodes := normalizeMergedNodes(allNodes)
	if c.options.NoRPRX {
		nodes = filterRPRXVisionNodes(nodes)
	}
	if len(nodes) == 0 {
		if len(failures) > 0 {
			return nil, fmt.Errorf("no valid nodes parsed from subscriptions: %s", warningSummary(failures))
		}
		return nil, fmt.Errorf("no valid nodes parsed from subscriptions")
	}

	headers := outputHeadersForSubscriptions(target, fetched)
	switch target {
	case targetSingBox:
		body, err := c.renderSingBox(nodes)
		if err != nil {
			return nil, err
		}
		return &subscriptionOutput{Body: body, ContentType: "application/json; charset=UTF-8", Headers: headers}, nil
	case targetBase64:
		body, err := renderBase64Subscription(nodes)
		if err != nil {
			return nil, err
		}
		return &subscriptionOutput{Body: body, ContentType: "text/plain; charset=UTF-8", Headers: headers}, nil
	default:
		body, err := c.renderMihomo(nodes)
		if err != nil {
			return nil, err
		}
		return &subscriptionOutput{Body: body, ContentType: "application/yaml; charset=UTF-8", Headers: headers}, nil
	}
}

func (c *subscriptionConverter) subscriptionServerIPs(ctx context.Context, urls []string) (subscriptionServerIPInfo, error) {
	if len(urls) == 0 {
		return subscriptionServerIPInfo{}, fmt.Errorf("url is required")
	}
	if len(urls) > maxMergedSubscriptionURLs {
		return subscriptionServerIPInfo{}, fmt.Errorf("too many subscription urls: max %d", maxMergedSubscriptionURLs)
	}

	var servers []string
	var failures []converterWarning
	for i, rawURL := range urls {
		sub, err := c.fetch(ctx, rawURL)
		if err != nil {
			failures = append(failures, converterWarning{URL: rawURL, Err: err})
			log.Printf("[clash] subscription ip fetch skipped %s: %v", rawURL, err)
			continue
		}
		nodes, warnings := parseSubscriptionNodes(sub.Content, fmt.Sprintf("sub-%d", i+1))
		for _, warning := range warnings {
			log.Printf("[clash] subscription ip %s: %s", rawURL, warning)
		}
		for _, node := range nodes {
			if server := nodeServer(node); server != "" {
				servers = append(servers, server)
			}
		}
	}
	if len(servers) == 0 {
		if len(failures) > 0 {
			return subscriptionServerIPInfo{}, fmt.Errorf("no valid nodes parsed from subscriptions: %s", warningSummary(failures))
		}
		return subscriptionServerIPInfo{}, fmt.Errorf("no valid node servers parsed from subscriptions")
	}
	return resolveSubscriptionServerIPs(ctx, servers), nil
}

func resolveSubscriptionServerIPs(ctx context.Context, servers []string) subscriptionServerIPInfo {
	resolvedIPs := map[string]struct{}{}
	serverIPs := map[string]struct{}{}
	serverNames := map[string]struct{}{}
	domains := map[string]struct{}{}

	for _, server := range compactStrings(servers) {
		if ip := net.ParseIP(server); ip != nil {
			if ip4 := ip.To4(); ip4 != nil {
				serverIPs[ip4.String()] = struct{}{}
			}
			continue
		}
		if !strings.Contains(server, ".") {
			continue
		}
		serverNames[server] = struct{}{}
		if parts := strings.Split(server, "."); len(parts) > 1 {
			if domain := strings.Join(parts[1:], "."); domain != "" {
				domains[domain] = struct{}{}
			}
		}
		addrs, err := net.DefaultResolver.LookupIPAddr(ctx, server)
		if err != nil {
			log.Printf("[clash] subscription ip resolve skipped %s: %v", server, err)
			continue
		}
		for _, addr := range addrs {
			if ip4 := addr.IP.To4(); ip4 != nil {
				resolvedIPs[ip4.String()] = struct{}{}
			}
		}
	}

	return subscriptionServerIPInfo{
		IP:        sortedStringSet(resolvedIPs),
		ServerIPs: sortedStringSet(serverIPs),
		Servers:   sortedStringSet(serverNames),
		Domain:    sortedStringSet(domains),
	}
}

func renderSubscriptionIPReport(info subscriptionServerIPInfo) []byte {
	sections := []struct {
		name   string
		values []string
	}{
		{name: "IP", values: info.IP},
		{name: "SERVER_IPS", values: info.ServerIPs},
		{name: "SERVERS", values: info.Servers},
		{name: "DOMAIN", values: info.Domain},
	}
	lines := make([]string, 0, len(info.IP)+len(info.ServerIPs)+len(info.Servers)+len(info.Domain)+len(sections)*2)
	for _, section := range sections {
		lines = append(lines, section.name+":")
		lines = append(lines, section.values...)
		lines = append(lines, "")
	}
	return []byte(strings.Join(lines, "\n"))
}

func renderRouterOSIPScript(info subscriptionServerIPInfo) []byte {
	ips := map[string]struct{}{}
	for _, ip := range info.IP {
		ips[ip] = struct{}{}
	}
	for _, ip := range info.ServerIPs {
		ips[ip] = struct{}{}
	}
	lines := []string{
		`/log info "Loading vps ipv4 address list"`,
		"/ip firewall address-list remove [/ip firewall address-list find list=vps]",
		"/ip firewall address-list",
	}
	for _, ip := range sortedStringSet(ips) {
		lines = append(lines, fmt.Sprintf(":do { add address=%s/32 list=vps } on-error={}", ip))
	}
	lines = append(lines, "")
	return []byte(strings.Join(lines, "\n"))
}

func sortedStringSet(values map[string]struct{}) []string {
	out := make([]string, 0, len(values))
	for value := range values {
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

func (c *subscriptionConverter) fetch(ctx context.Context, rawURL string) (*fetchedSubscription, error) {
	if !c.allowPrivateFetch {
		if err := validatePublicFetchURL(ctx, rawURL); err != nil {
			return nil, err
		}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, fmt.Errorf("create request %s: %w", rawURL, err)
	}
	req.Header.Set("User-Agent", subscriptionUserAgent)

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch subscription %s: %w", rawURL, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("fetch subscription %s: http %d", rawURL, resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxSubscriptionBytes))
	if err != nil {
		return nil, fmt.Errorf("read subscription %s: %w", rawURL, err)
	}
	return &fetchedSubscription{
		URL:     rawURL,
		Content: string(body),
		Headers: subscriptionResponseHeaders(resp.Header),
	}, nil
}

func warningSummary(warnings []converterWarning) string {
	parts := make([]string, 0, len(warnings))
	for _, warning := range warnings {
		parts = append(parts, warning.Err.Error())
	}
	return strings.Join(parts, "; ")
}

func validatePublicFetchURL(ctx context.Context, rawURL string) error {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("invalid url %s: %w", rawURL, err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return fmt.Errorf("unsupported url scheme: %s", parsed.Scheme)
	}
	host := strings.TrimSpace(parsed.Hostname())
	if host == "" {
		return fmt.Errorf("url host is required")
	}
	_, err = resolvePublicHost(ctx, host)
	return err
}

func resolvePublicHost(ctx context.Context, host string) ([]net.IP, error) {
	ips := []net.IP{}
	if ip := net.ParseIP(host); ip != nil {
		ips = append(ips, ip)
	} else {
		resolved, err := net.DefaultResolver.LookupIP(ctx, "ip", host)
		if err != nil {
			return nil, fmt.Errorf("resolve %s: %w", host, err)
		}
		ips = append(ips, resolved...)
	}
	if len(ips) == 0 {
		return nil, fmt.Errorf("resolve %s: no addresses", host)
	}
	for _, ip := range ips {
		if !isPublicIP(ip) {
			return nil, fmt.Errorf("blocked non-public subscription host: %s", host)
		}
	}
	return ips, nil
}

func isPublicIP(ip net.IP) bool {
	if ip == nil {
		return false
	}
	return !(ip.IsLoopback() ||
		ip.IsPrivate() ||
		ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() ||
		ip.IsUnspecified() ||
		ip.IsMulticast())
}

func subscriptionResponseHeaders(headers http.Header) map[string]string {
	allowed := []string{"content-disposition", "subscription-userinfo", "profile-update-interval", "profile-web-page-url"}
	out := map[string]string{}
	for _, key := range allowed {
		if value := headers.Get(key); strings.TrimSpace(value) != "" {
			out[key] = value
		}
	}
	return out
}

func outputHeadersForSubscriptions(target subscriptionTarget, fetched []fetchedSubscription) map[string]string {
	headers := map[string]string{
		"content-disposition": fmt.Sprintf("attachment; filename=%s", outputFilename(target)),
	}
	if len(fetched) == 1 {
		for _, key := range []string{"subscription-userinfo", "profile-update-interval", "profile-web-page-url"} {
			if value := fetched[0].Headers[key]; strings.TrimSpace(value) != "" {
				headers[key] = value
			}
		}
	}
	return headers
}

func outputFilename(target subscriptionTarget) string {
	switch target {
	case targetSingBox:
		return "clisimplehub-sing-box.json"
	case targetBase64:
		return "clisimplehub-base64.txt"
	default:
		return "clisimplehub-mihomo.yaml"
	}
}

func parseSubscriptionNodes(content string, sourceID string) ([]ProxyNode, []string) {
	content = strings.TrimSpace(content)
	if content == "" {
		return nil, []string{"empty subscription content"}
	}
	if nodes, warnings := parseSingBoxJSON(content, sourceID); len(nodes) > 0 {
		return nodes, warnings
	}
	if looksLikeClashYAML(content) || looksLikeClashJSON(content) {
		return DetectAndParse(content, "auto", sourceID)
	}
	if nodes, warnings := ParseURIList(content, sourceID); len(nodes) > 0 {
		return nodes, warnings
	}
	if nodes, warnings := parseMihomoURIList(content, sourceID); len(nodes) > 0 {
		return nodes, warnings
	}
	return DetectAndParse(content, "auto", sourceID)
}

func looksLikeClashJSON(content string) bool {
	trimmed := strings.TrimSpace(content)
	if !strings.HasPrefix(trimmed, "{") {
		return false
	}
	var obj map[string]any
	if err := json.Unmarshal([]byte(trimmed), &obj); err != nil {
		return false
	}
	_, ok := obj["proxies"]
	return ok
}

func parseMihomoURIList(content string, sourceID string) ([]ProxyNode, []string) {
	proxies, err := mihomoconvert.ConvertsV2Ray([]byte(content))
	if err != nil {
		return nil, []string{err.Error()}
	}
	nodes := make([]ProxyNode, 0, len(proxies))
	var warnings []string
	for _, proxy := range proxies {
		node, err := parseClashProxy(proxy)
		if err != nil {
			warnings = append(warnings, err.Error())
			continue
		}
		nodes = append(nodes, normalizeProxyNode(*node, sourceID))
	}
	return nodes, warnings
}

func parseSingBoxJSON(content string, sourceID string) ([]ProxyNode, []string) {
	if !strings.HasPrefix(strings.TrimSpace(content), "{") {
		return nil, nil
	}
	var cfg struct {
		Outbounds []map[string]any `json:"outbounds"`
	}
	if err := json.Unmarshal([]byte(content), &cfg); err != nil || len(cfg.Outbounds) == 0 {
		return nil, nil
	}
	var nodes []ProxyNode
	var warnings []string
	for _, outbound := range cfg.Outbounds {
		node, err := singBoxOutboundToMihomoNode(outbound)
		if err != nil {
			warnings = append(warnings, err.Error())
			continue
		}
		if node != nil {
			nodes = append(nodes, normalizeProxyNode(*node, sourceID))
		}
	}
	return nodes, warnings
}

func singBoxOutboundToMihomoNode(outbound map[string]any) (*ProxyNode, error) {
	proxyType := strings.ToLower(stringFromMap(outbound, "type"))
	name := firstNonEmptyValue(stringFromMap(outbound, "tag"), stringFromMap(outbound, "name"))
	if isSingBoxNonNodeOutbound(proxyType) {
		return nil, nil
	}
	node := ProxyNode{
		"name":   name,
		"server": stringFromMap(outbound, "server"),
		"port":   intFromAny(firstAny(outbound, "server_port", "server-port", "port")),
	}
	switch proxyType {
	case "shadowsocks":
		node["type"] = "ss"
		node["cipher"] = firstNonEmptyValue(stringFromMap(outbound, "method"), stringFromMap(outbound, "cipher"))
		node["password"] = stringFromMap(outbound, "password")
	case "vmess":
		node["type"] = "vmess"
		node["uuid"] = stringFromMap(outbound, "uuid")
		node["alterId"] = intFromAny(firstAny(outbound, "alter_id", "alterId"))
		node["cipher"] = firstNonEmptyValue(stringFromMap(outbound, "security"), "auto")
	case "vless":
		node["type"] = "vless"
		node["uuid"] = stringFromMap(outbound, "uuid")
		if flow := stringFromMap(outbound, "flow"); flow != "" {
			node["flow"] = flow
		}
	case "trojan":
		node["type"] = "trojan"
		node["password"] = stringFromMap(outbound, "password")
	case "hysteria":
		node["type"] = "hysteria"
		copyFirstPresentAs(node, outbound, "auth_str", "auth_str", "auth-str", "auth")
		copyFirstPresentAs(node, outbound, "up", "up", "up_mbps", "upMbps")
		copyFirstPresentAs(node, outbound, "down", "down", "down_mbps", "downMbps")
		copyIfPresent(node, outbound, "obfs")
	case "hysteria2":
		node["type"] = "hysteria2"
		node["password"] = stringFromMap(outbound, "password")
		copyFirstPresentAs(node, outbound, "up", "up", "up_mbps", "upMbps")
		copyFirstPresentAs(node, outbound, "down", "down", "down_mbps", "downMbps")
	case "tuic":
		node["type"] = "tuic"
		copyIfPresent(node, outbound, "uuid")
		copyIfPresent(node, outbound, "password")
		copyIfPresent(node, outbound, "token")
	case "anytls":
		node["type"] = "anytls"
		node["password"] = stringFromMap(outbound, "password")
	case "wireguard":
		node["type"] = "wireguard"
		copyFirstPresentAs(node, outbound, "private-key", "private-key", "private_key")
		copyFirstPresentAs(node, outbound, "public-key", "public-key", "peer_public_key")
		copyFirstPresentAs(node, outbound, "pre-shared-key", "pre-shared-key", "pre_shared_key")
		copyFirstPresentAs(node, outbound, "ip", "ip", "local_address")
		copyIfPresent(node, outbound, "ipv6", "reserved")
	case "ssh":
		node["type"] = "ssh"
		copyFirstPresentAs(node, outbound, "username", "username", "user")
		copyIfPresent(node, outbound, "password")
		copyFirstPresentAs(node, outbound, "private-key", "private-key", "private_key")
		copyFirstPresentAs(node, outbound, "private-key-passphrase", "private-key-passphrase", "private_key_passphrase")
	case "http":
		node["type"] = "http"
		copyIfPresent(node, outbound, "username")
		copyIfPresent(node, outbound, "password")
	case "socks":
		node["type"] = "socks5"
		copyIfPresent(node, outbound, "username")
		copyIfPresent(node, outbound, "password")
	default:
		return nil, fmt.Errorf("skipped unsupported sing-box outbound type: %s (%s)", proxyType, name)
	}
	applySingBoxTLS(node, outbound)
	applySingBoxTransport(node, outbound)
	if err := validateProxyNode(node); err != nil {
		return nil, err
	}
	return &node, nil
}

func isSingBoxNonNodeOutbound(proxyType string) bool {
	switch proxyType {
	case "", "direct", "block", "dns", "selector", "urltest", "fallback", "loadbalance":
		return true
	default:
		return false
	}
}

func applySingBoxTLS(node ProxyNode, outbound map[string]any) {
	tlsMap, ok := firstAny(outbound, "tls").(map[string]any)
	if !ok || len(tlsMap) == 0 {
		return
	}
	if enabled, ok := parseBoolAny(tlsMap["enabled"]); ok && !enabled {
		return
	}
	node["tls"] = true
	if sni := firstNonEmptyValue(stringFromMap(tlsMap, "server_name"), stringFromMap(tlsMap, "serverName")); sni != "" {
		node["sni"] = sni
		node["servername"] = sni
	}
	if insecure, ok := parseBoolAny(tlsMap["insecure"]); ok {
		node["skip-cert-verify"] = insecure
	}
	if reality, ok := tlsMap["reality"].(map[string]any); ok {
		if enabled, ok := parseBoolAny(reality["enabled"]); !ok || enabled {
			opts := map[string]any{}
			if publicKey := stringFromMap(reality, "public_key"); publicKey != "" {
				opts["public-key"] = publicKey
			}
			if shortID := stringFromMap(reality, "short_id"); shortID != "" {
				opts["short-id"] = shortID
			}
			if len(opts) > 0 {
				node["reality-opts"] = opts
			}
		}
	}
	if utls, ok := tlsMap["utls"].(map[string]any); ok {
		if fp := stringFromMap(utls, "fingerprint"); fp != "" {
			node["client-fingerprint"] = fp
		}
	}
}

func applySingBoxTransport(node ProxyNode, outbound map[string]any) {
	transport, ok := outbound["transport"].(map[string]any)
	if !ok || len(transport) == 0 {
		return
	}
	network := stringFromMap(transport, "type")
	if network == "" {
		return
	}
	node["network"] = network
	switch network {
	case "ws":
		opts := map[string]any{}
		if path := stringFromMap(transport, "path"); path != "" {
			opts["path"] = path
		}
		if headers, ok := transport["headers"].(map[string]any); ok && len(headers) > 0 {
			opts["headers"] = headers
		}
		if len(opts) > 0 {
			node["ws-opts"] = opts
		}
	case "grpc":
		if service := firstNonEmptyValue(stringFromMap(transport, "service_name"), stringFromMap(transport, "grpc-service-name")); service != "" {
			node["grpc-opts"] = map[string]any{"grpc-service-name": service}
		}
	case "http", "httpupgrade", "h2":
		opts := map[string]any{}
		if path := stringFromMap(transport, "path"); path != "" {
			opts["path"] = []string{path}
		}
		if host := firstAny(transport, "host"); host != nil {
			opts["host"] = host
		}
		if len(opts) > 0 {
			node["http-opts"] = opts
		}
	}
}

func normalizeMergedNodes(nodes []ProxyNode) []ProxyNode {
	existing := make([]ProxyNode, 0, len(nodes))
	out := make([]ProxyNode, 0, len(nodes))
	for _, node := range nodes {
		node = normalizeProxyNode(node, "")
		delete(node, nodeFieldSourceID)
		setNodeString(node, nodeFieldName, uniqueNodeName(nodeName(node), existing))
		existing = append(existing, node)
		out = append(out, node)
	}
	return out
}

func filterRPRXVisionNodes(nodes []ProxyNode) []ProxyNode {
	out := make([]ProxyNode, 0, len(nodes))
	for _, node := range nodes {
		if strings.EqualFold(nodeString(node, "flow"), "xtls-rprx-vision") {
			continue
		}
		out = append(out, node)
	}
	return out
}

func (c *subscriptionConverter) renderMihomo(nodes []ProxyNode) ([]byte, error) {
	cfg := cloneAnyMap(c.settings.Mihomo)
	if c.options.SocksPort > 0 {
		cfg["socks-port"] = c.options.SocksPort
	}
	proxies := make([]any, 0, len(nodes))
	for _, node := range nodes {
		proxies = append(proxies, map[string]any(stripNodeMetadata(node)))
	}
	applyMihomoWSOpts(proxies, c.settings.WSOpts)
	landingNodes := c.fetchLandingNodes(context.Background())
	for _, node := range landingNodes {
		proxies = append(proxies, map[string]any(stripNodeMetadata(node)))
	}
	groups, defaultGroup, ruleProviders, rules := c.buildMihomoGroupsAndRules(nodes, landingNodes)
	cfg["proxies"] = proxies
	cfg["proxy-groups"] = groups
	if len(ruleProviders) > 0 {
		cfg["rule-providers"] = ruleProviders
	}
	cfg["rules"] = append(rules, "MATCH,"+defaultGroup)
	return yaml.Marshal(cfg)
}

func (c *subscriptionConverter) buildMihomoGroupsAndRules(nodes []ProxyNode, landingNodes []ProxyNode) ([]any, string, map[string]any, []string) {
	classified := c.classifyNodes(nodes)
	allNodes := nodeNames(nodes)
	autoGroups := classified.autoGroupNames
	feeGroups := classified.notAutoGroupNames
	if len(landingNodes) > 0 {
		feeGroups = append(feeGroups, "🕊️落地组")
	}

	var groups []any
	defaultGroup := "🔰 节点选择"
	for _, group := range c.settings.ProxyGroups {
		if group.Default {
			defaultGroup = group.Name
		}
		rec := map[string]any{"name": group.Name, "type": group.Type}
		proxies := expandProxyPlaceholders(group.Proxies, allNodes, autoGroups, feeGroups)
		if len(proxies) == 0 {
			proxies = append(proxies, "DIRECT")
		}
		rec["proxies"] = proxies
		if group.Type == "url-test" {
			mergeAnyMap(rec, c.settings.GroupBaseOption)
		}
		groups = append(groups, rec)
	}
	for _, group := range classified.groups {
		rec := map[string]any{"name": group.name, "type": group.groupType, "proxies": group.proxies}
		if group.groupType == "url-test" {
			mergeAnyMap(rec, c.settings.GroupBaseOption)
		}
		groups = append(groups, rec)
	}
	if len(landingNodes) > 0 {
		rec := map[string]any{"name": "🕊️落地组", "type": "select", "proxies": nodeNames(landingNodes)}
		mergeAnyMap(rec, c.settings.GroupBaseOption)
		groups = append(groups, rec)
	}

	existingRuleProviders := extractMihomoRuleProviders(c.settings.Mihomo)
	ruleProviders := cloneProviderMap(existingRuleProviders)
	rules := directRulesForProxyServers(nodes)
	rules = append(rules, localNetworkDirectRules()...)
	for _, group := range c.settings.ProxyGroups {
		if len(group.Rules) == 0 && len(group.Hosts) == 0 {
			for _, ruleSet := range group.RuleSet {
				if strings.TrimSpace(ruleSet) != "" {
					rules = append(rules, fmt.Sprintf("RULE-SET,%s,%s", ruleSet, group.Name))
				}
			}
			continue
		}
		ruleURL := c.baseURL + "/grouprule/mihomo?group=" + url.QueryEscape(group.Name) + "&format=yaml"
		name := md5Hex(ruleURL)
		ruleProviders[name] = map[string]any{
			"type":     "http",
			"format":   "yaml",
			"behavior": "classical",
			"url":      ruleURL,
			"path":     "./rules/" + name + ".yaml",
			"interval": 86400,
		}
		rules = append(rules, fmt.Sprintf("RULE-SET,%s,%s", name, group.Name))
	}
	rules = append(rules, mihomoProviderRules(existingRuleProviders, ruleProviders, defaultGroup, groupNames(groups))...)
	removeProviderHelperKeys(ruleProviders)
	return groups, defaultGroup, ruleProviders, dedupeRules(rules)
}

func applyMihomoWSOpts(proxies []any, opts map[string]any) {
	if len(opts) == 0 {
		return
	}
	for _, item := range proxies {
		proxy, ok := item.(map[string]any)
		if !ok {
			continue
		}
		if strings.ToLower(stringFromMap(proxy, "type")) != "vmess" || intFromAny(proxy["port"]) != 80 || strings.ToLower(stringFromMap(proxy, "network")) != "ws" {
			continue
		}
		current, _ := proxy["ws-opts"].(map[string]any)
		if current == nil {
			current = map[string]any{}
		}
		mergeAnyMap(current, cloneAnyMap(opts))
		proxy["ws-opts"] = cleanEmptyAnyMap(current)
	}
}

func (c *subscriptionConverter) fetchLandingNodes(ctx context.Context) []ProxyNode {
	if !c.options.Landing || strings.TrimSpace(c.settings.LandingNodeURL) == "" {
		return nil
	}
	body, err := c.fetchText(ctx, c.settings.LandingNodeURL)
	if err != nil {
		log.Printf("[clash] landing nodes skipped %s: %v", c.settings.LandingNodeURL, err)
		return nil
	}
	var proxies []map[string]any
	if err := json.Unmarshal([]byte(body), &proxies); err != nil {
		log.Printf("[clash] landing nodes parse failed %s: %v", c.settings.LandingNodeURL, err)
		return nil
	}
	nodes, warnings := parseClashProxyList(proxies, "")
	for _, warning := range warnings {
		log.Printf("[clash] landing node skipped: %s", warning)
	}
	return normalizeMergedNodes(nodes)
}

func extractMihomoRuleProviders(mihomo map[string]any) map[string]any {
	raw, ok := mihomo["rule-providers"].(map[string]any)
	if !ok || len(raw) == 0 {
		return map[string]any{}
	}
	out := make(map[string]any, len(raw))
	for key, value := range raw {
		if provider, ok := value.(map[string]any); ok {
			out[key] = cloneAnyMap(provider)
		} else {
			out[key] = cloneAny(value)
		}
	}
	return out
}

func cloneProviderMap(input map[string]any) map[string]any {
	out := make(map[string]any, len(input))
	for key, value := range input {
		if provider, ok := value.(map[string]any); ok {
			out[key] = cloneAnyMap(provider)
		} else {
			out[key] = cloneAny(value)
		}
	}
	return out
}

func directRulesForProxyServers(nodes []ProxyNode) []string {
	seen := map[string]struct{}{}
	var rules []string
	for _, node := range nodes {
		server := nodeServer(node)
		if server == "" {
			continue
		}
		if ip := net.ParseIP(server); ip != nil {
			rule := "IP-CIDR," + ip.String() + "/32,DIRECT,no-resolve"
			if _, ok := seen[rule]; !ok {
				seen[rule] = struct{}{}
				rules = append(rules, rule)
			}
			continue
		}
		suffix := registrableDomain(server)
		if suffix == "" {
			continue
		}
		rule := "DOMAIN-SUFFIX," + suffix + ",DIRECT"
		if _, ok := seen[rule]; !ok {
			seen[rule] = struct{}{}
			rules = append(rules, rule)
		}
	}
	return rules
}

func registrableDomain(host string) string {
	host = strings.Trim(strings.ToLower(strings.TrimSpace(host)), ".")
	parts := strings.Split(host, ".")
	if len(parts) < 2 {
		return ""
	}
	return strings.Join(parts[len(parts)-2:], ".")
}

func localNetworkDirectRules() []string {
	return []string{
		"DOMAIN-SUFFIX,ip6-localhost,DIRECT",
		"DOMAIN-SUFFIX,ip6-loopback,DIRECT",
		"DOMAIN-SUFFIX,lan,DIRECT",
		"DOMAIN-SUFFIX,local,DIRECT",
		"DOMAIN-SUFFIX,localhost,DIRECT",
		"DOMAIN,instant.arubanetworks.com,DIRECT",
		"DOMAIN,setmeup.arubanetworks.com,DIRECT",
		"DOMAIN,router.asus.com,DIRECT",
		"DOMAIN-SUFFIX,hiwifi.com,DIRECT",
		"DOMAIN-SUFFIX,leike.cc,DIRECT",
		"DOMAIN-SUFFIX,miwifi.com,DIRECT",
		"DOMAIN-SUFFIX,my.router,DIRECT",
		"DOMAIN-SUFFIX,p.to,DIRECT",
		"DOMAIN-SUFFIX,peiluyou.com,DIRECT",
		"DOMAIN-SUFFIX,phicomm.me,DIRECT",
		"DOMAIN-SUFFIX,router.ctc,DIRECT",
		"DOMAIN-SUFFIX,routerlogin.com,DIRECT",
		"DOMAIN-SUFFIX,tendawifi.com,DIRECT",
		"DOMAIN-SUFFIX,zte.home,DIRECT",
		"DOMAIN-SUFFIX,tplogin.cn,DIRECT",
	}
}

func (c *subscriptionConverter) otherMihomoRules(ruleProviders map[string]any, defaultGroup string) []string {
	var rules []string
	for _, group := range c.settings.OtherRules {
		outbound := normalizeRuleOutbound(group.Path, defaultGroup, nil)
		if outbound == "" {
			continue
		}
		for _, rule := range group.Rules {
			rules = append(rules, appendRuleOutbound(rule, outbound))
		}
		for _, host := range group.Hosts {
			hostRules, err := c.fetchRuleHost(context.Background(), host)
			if err != nil {
				log.Printf("[clash] other rule host skipped %s: %v", host, err)
				continue
			}
			for _, rule := range hostRules {
				rules = append(rules, appendRuleOutbound(rule, outbound))
			}
		}
		for _, ruleSet := range group.RuleSet {
			name := ruleSetName(ruleSet)
			if name != "" {
				if _, exists := ruleProviders[name]; !exists && looksLikeHTTPURL(ruleSet) {
					ruleProviders[name] = map[string]any{
						"type":     "http",
						"format":   ruleProviderFormat(ruleSet),
						"behavior": "classical",
						"url":      ruleSet,
						"path":     "./rules/" + path.Base(mustURLPath(ruleSet)),
						"interval": 86400,
					}
				}
				rules = append(rules, fmt.Sprintf("RULE-SET,%s,%s", name, outbound))
			}
		}
	}
	return rules
}

func looksLikeHTTPURL(value string) bool {
	parsed, err := url.Parse(strings.TrimSpace(value))
	return err == nil && (parsed.Scheme == "http" || parsed.Scheme == "https")
}

func ruleProviderFormat(value string) string {
	ext := strings.ToLower(path.Ext(mustURLPath(value)))
	if ext == ".yaml" || ext == ".yml" {
		return "yaml"
	}
	return "text"
}

func mihomoProviderRules(sourceProviders, outputProviders map[string]any, defaultGroup string, groups map[string]struct{}) []string {
	var rules []string
	for name, raw := range sourceProviders {
		provider, ok := raw.(map[string]any)
		if !ok {
			rules = append(rules, fmt.Sprintf("RULE-SET,%s,%s", name, defaultGroup))
			continue
		}
		if _, ok := provider["interval"]; !ok {
			if output, ok := outputProviders[name].(map[string]any); ok {
				output["interval"] = 3600
			}
		}
		rawProxy := stringFromMap(provider, "proxy")
		delete(provider, "proxy")
		outbound := normalizeRuleOutbound(rawProxy, defaultGroup, groups)
		if outbound == "" {
			outbound = "DIRECT"
		}
		rules = append(rules, fmt.Sprintf("RULE-SET,%s,%s", name, outbound))
	}
	return rules
}

func removeProviderHelperKeys(providers map[string]any) {
	for _, raw := range providers {
		if provider, ok := raw.(map[string]any); ok {
			delete(provider, "proxy")
		}
	}
}

func groupNames(groups []any) map[string]struct{} {
	out := map[string]struct{}{}
	for _, item := range groups {
		if group, ok := item.(map[string]any); ok {
			if name := stringFromMap(group, "name"); name != "" {
				out[name] = struct{}{}
			}
		}
	}
	return out
}

func normalizeRuleOutbound(raw string, defaultGroup string, groups map[string]struct{}) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	switch strings.ToLower(raw) {
	case "direct":
		return "DIRECT"
	case "reject":
		return "REJECT"
	case "proxy":
		return defaultGroup
	default:
		if groups != nil {
			if _, ok := groups[raw]; !ok {
				return defaultGroup
			}
		}
		return raw
	}
}

func appendRuleOutbound(rule, outbound string) string {
	rule = strings.TrimSpace(rule)
	if rule == "" {
		return ""
	}
	parts := strings.Split(rule, ",")
	if len(parts) >= 3 && !strings.EqualFold(strings.TrimSpace(parts[len(parts)-1]), "no-resolve") {
		return rule
	}
	if len(parts) >= 3 && strings.EqualFold(strings.TrimSpace(parts[len(parts)-1]), "no-resolve") {
		if len(parts) >= 4 {
			return rule
		}
		return strings.Join(parts[:len(parts)-1], ",") + "," + outbound + ",no-resolve"
	}
	return rule + "," + outbound
}

func ruleSetName(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if parsed, err := url.Parse(value); err == nil && parsed.Scheme != "" {
		name := path.Base(parsed.Path)
		ext := path.Ext(name)
		return strings.TrimSuffix(name, ext)
	}
	return value
}

func singBoxRemoteRuleSet(value string) map[string]any {
	if !looksLikeHTTPURL(value) {
		return nil
	}
	name := ruleSetName(value)
	if name == "" {
		return nil
	}
	format := "source"
	if strings.EqualFold(path.Ext(mustURLPath(value)), ".srs") {
		format = "binary"
	}
	return map[string]any{
		"tag":             name,
		"type":            "remote",
		"format":          format,
		"download_detour": "DIRECT",
		"url":             value,
	}
}

func dedupeSingBoxRuleSets(ruleSets []any) []any {
	seen := map[string]struct{}{}
	out := make([]any, 0, len(ruleSets))
	for _, item := range ruleSets {
		rec, ok := item.(map[string]any)
		if !ok {
			out = append(out, item)
			continue
		}
		tag := stringFromMap(rec, "tag")
		if tag == "" {
			out = append(out, item)
			continue
		}
		if _, ok := seen[tag]; ok {
			continue
		}
		seen[tag] = struct{}{}
		out = append(out, item)
	}
	return out
}

func (c *subscriptionConverter) renderSingBox(nodes []ProxyNode) ([]byte, error) {
	cfg := cloneAnyMap(c.settings.SingBox)
	applySingBoxOptions(cfg, c.options)
	version := singBoxVersionNumber(c.options.Version)
	outbounds := []any{
		map[string]any{"tag": "DIRECT", "type": "direct"},
		map[string]any{"tag": "REJECT", "type": "block"},
	}
	var convertedNodes []ProxyNode
	for _, node := range nodes {
		outbound, err := mihomoNodeToSingBoxOutbound(node)
		if err != nil {
			log.Printf("[clash] sing-box conversion skipped %s: %v", nodeName(node), err)
			continue
		}
		outbounds = append(outbounds, outbound)
		convertedNodes = append(convertedNodes, node)
	}
	if len(convertedNodes) == 0 {
		return nil, fmt.Errorf("no nodes can be converted to sing-box")
	}
	outbounds = append(outbounds, c.buildSingBoxGroups(convertedNodes)...)
	finalOutbound := c.defaultProxyGroupName()
	if c.options.FixedNode != "" && hasNodeByName(convertedNodes, c.options.FixedNode) {
		finalOutbound = "手动选择"
		outbounds = append(outbounds, map[string]any{
			"tag":       finalOutbound,
			"type":      "selector",
			"outbounds": []string{c.options.FixedNode, c.defaultProxyGroupName(), "DIRECT"},
		})
	}
	cfg["outbounds"] = outbounds
	route, _ := cfg["route"].(map[string]any)
	if route == nil {
		route = map[string]any{}
		cfg["route"] = route
	}
	normalizeSingBoxRouteRules(route, c.defaultProxyGroupName(), version)
	route["final"] = finalOutbound
	routeBuild := c.buildSingBoxRouteRules()
	route["rules"] = appendSingBoxRules(route["rules"], routeBuild.Rules)
	if ruleSets := appendSingBoxRuleSets(route["rule_set"], routeBuild.RuleSets); len(ruleSets) > 0 {
		route["rule_set"] = ruleSets
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	if err := enc.Encode(cfg); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func applySingBoxOptions(cfg map[string]any, options converterOptions) {
	version := singBoxVersionNumber(options.Version)
	if inbounds, ok := cfg["inbounds"].([]any); ok {
		for _, item := range inbounds {
			if inbound, ok := item.(map[string]any); ok {
				inboundType := strings.ToLower(stringFromMap(inbound, "type"))
				isSocksPortPlaceholder := strings.EqualFold(stringFromAny(inbound["listen_port"]), "socks5_port")
				if options.SocksPort > 0 && (inboundType == "socks" || inboundType == "mixed" || inboundType == "http") {
					inbound["listen_port"] = options.SocksPort
				} else if isSocksPortPlaceholder || inboundType == "socks" {
					inbound["listen_port"] = effectiveSocksPort(options)
				}
			}
		}
	}
	if options.Tun {
		tun := map[string]any{
			"tag":          "tun-in",
			"type":         "tun",
			"mtu":          9000,
			"auto_route":   true,
			"strict_route": true,
			"sniff":        true,
		}
		if version >= 1010 {
			tun["address"] = []string{"172.19.0.1/30", "fdfe:dcba:9876::1/126"}
		} else {
			tun["inet4_address"] = "172.19.0.1/30"
			tun["inet6_address"] = "fdfe:dcba:9876::1/126"
		}
		inbounds, _ := cfg["inbounds"].([]any)
		cfg["inbounds"] = append(inbounds, tun)
	}
}

func effectiveSocksPort(options converterOptions) int {
	if options.SocksPort > 0 {
		return options.SocksPort
	}
	return 7891
}

func singBoxVersionNumber(raw string) int {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 1012
	}
	parts := strings.Split(raw, ".")
	if len(parts) == 0 {
		return 1012
	}
	major, _ := strconv.Atoi(parts[0])
	minor := 0
	if len(parts) > 1 {
		minor, _ = strconv.Atoi(parts[1])
	}
	if major == 0 && minor == 0 {
		return 1012
	}
	return major*1000 + minor
}

func normalizeSingBoxRouteRules(route map[string]any, defaultGroup string, version int) {
	rules, ok := route["rules"].([]any)
	if !ok {
		return
	}
	for _, item := range rules {
		rule, ok := item.(map[string]any)
		if !ok {
			continue
		}
		normalizeSingBoxRuleOutbound(rule, defaultGroup, version)
	}
}

func normalizeSingBoxRuleOutbound(rule map[string]any, defaultGroup string, version int) {
	outbound := stringFromMap(rule, "outbound")
	if outbound == "" {
		return
	}
	switch strings.ToLower(outbound) {
	case "proxy":
		rule["outbound"] = defaultGroup
	case "direct":
		rule["outbound"] = "DIRECT"
	case "reject":
		if version >= 1011 {
			delete(rule, "outbound")
			rule["action"] = "reject"
		} else {
			rule["outbound"] = "REJECT"
		}
	}
}

func appendSingBoxRules(existing any, extra []any) []any {
	var rules []any
	if current, ok := existing.([]any); ok {
		rules = append(rules, current...)
	}
	return append(rules, extra...)
}

func appendSingBoxRuleSets(existing any, extra []any) []any {
	var ruleSets []any
	if current, ok := existing.([]any); ok {
		ruleSets = append(ruleSets, current...)
	}
	return append(ruleSets, extra...)
}

func (c *subscriptionConverter) buildSingBoxGroups(nodes []ProxyNode) []any {
	classified := c.classifyNodes(nodes)
	allNodes := nodeNames(nodes)
	autoGroups := classified.autoGroupNames
	feeGroups := classified.notAutoGroupNames
	var out []any
	for _, group := range c.settings.ProxyGroups {
		proxies := expandProxyPlaceholders(group.Proxies, allNodes, autoGroups, feeGroups)
		if len(proxies) == 0 {
			proxies = []string{"DIRECT"}
		}
		groupType := "selector"
		if group.Type == "url-test" || group.Type == "urltest" {
			groupType = "urltest"
		}
		rec := map[string]any{"tag": group.Name, "type": groupType, "outbounds": proxies}
		if groupType == "urltest" {
			rec["url"] = c.settings.ProxyTestURL
			rec["interval"] = "30m"
		}
		out = append(out, rec)
	}
	for _, group := range classified.groups {
		groupType := "selector"
		if group.groupType == "url-test" {
			groupType = "urltest"
		}
		rec := map[string]any{"tag": group.name, "type": groupType, "outbounds": group.proxies}
		if groupType == "urltest" {
			rec["url"] = c.settings.ProxyTestURL
			rec["interval"] = "30m"
		}
		out = append(out, rec)
	}
	return out
}

func (c *subscriptionConverter) buildSingBoxRouteRules() singBoxRouteBuild {
	defaultGroup := c.defaultProxyGroupName()
	var build singBoxRouteBuild
	for _, group := range c.settings.ProxyGroups {
		for _, rule := range group.Rules {
			if converted := mihomoRuleToSingBox(rule, group.Name); len(converted) > 0 {
				build.Rules = append(build.Rules, converted)
			}
		}
		for _, host := range group.Hosts {
			hostRules, err := c.fetchRuleHost(context.Background(), host)
			if err != nil {
				log.Printf("[clash] sing-box group rule host skipped %s: %v", host, err)
				continue
			}
			for _, rule := range hostRules {
				if converted := mihomoRuleToSingBox(rule, group.Name); len(converted) > 0 {
					build.Rules = append(build.Rules, converted)
				}
			}
		}
		for _, ruleSet := range group.RuleSet {
			if name := ruleSetName(ruleSet); name != "" {
				build.Rules = append(build.Rules, map[string]any{"rule_set": []string{name}, "outbound": group.Name})
				if ruleSetDef := singBoxRemoteRuleSet(ruleSet); len(ruleSetDef) > 0 {
					build.RuleSets = append(build.RuleSets, ruleSetDef)
				}
			}
		}
	}
	for _, group := range c.settings.OtherRules {
		outbound := normalizeRuleOutbound(group.Path, defaultGroup, nil)
		if outbound == "" {
			continue
		}
		for _, rule := range group.Rules {
			if converted := mihomoRuleToSingBox(rule, outbound); len(converted) > 0 {
				build.Rules = append(build.Rules, converted)
			}
		}
		for _, host := range group.Hosts {
			hostRules, err := c.fetchRuleHost(context.Background(), host)
			if err != nil {
				log.Printf("[clash] sing-box other rule host skipped %s: %v", host, err)
				continue
			}
			for _, rule := range hostRules {
				if converted := mihomoRuleToSingBox(rule, outbound); len(converted) > 0 {
					build.Rules = append(build.Rules, converted)
				}
			}
		}
		for _, ruleSet := range group.RuleSet {
			if name := ruleSetName(ruleSet); name != "" {
				build.Rules = append(build.Rules, map[string]any{"rule_set": []string{name}, "outbound": outbound})
				if ruleSetDef := singBoxRemoteRuleSet(ruleSet); len(ruleSetDef) > 0 {
					build.RuleSets = append(build.RuleSets, ruleSetDef)
				}
			}
		}
	}
	build.RuleSets = dedupeSingBoxRuleSets(build.RuleSets)
	return build
}

func mihomoRuleToSingBox(rule, outbound string) map[string]any {
	parts := strings.Split(strings.TrimSpace(rule), ",")
	if len(parts) < 2 || outbound == "" {
		return nil
	}
	rec := map[string]any{"outbound": outbound}
	switch strings.ToUpper(strings.TrimSpace(parts[0])) {
	case "DOMAIN-SUFFIX":
		rec["domain_suffix"] = []string{strings.TrimSpace(parts[1])}
	case "DOMAIN":
		rec["domain"] = []string{strings.TrimSpace(parts[1])}
	case "DOMAIN-KEYWORD":
		rec["domain_keyword"] = []string{strings.TrimSpace(parts[1])}
	case "DOMAIN-REGEX":
		rec["domain_regex"] = []string{strings.TrimSpace(parts[1])}
	case "PROCESS-NAME":
		rec["process_name"] = []string{strings.TrimSpace(parts[1])}
	case "IP-CIDR", "IP-CIDR6":
		rec["ip_cidr"] = []string{strings.TrimSpace(parts[1])}
	case "GEOIP":
		rec["geoip"] = []string{strings.TrimSpace(parts[1])}
	default:
		return nil
	}
	if strings.EqualFold(outbound, "REJECT") {
		delete(rec, "outbound")
		rec["action"] = "reject"
	}
	return rec
}

func (c *subscriptionConverter) groupRules(ctx context.Context, groupName string) []string {
	if c.settings == nil {
		c.settings = defaultConverterSettings()
	}
	for _, group := range c.settings.ProxyGroups {
		if group.Name != groupName {
			continue
		}
		rules := append([]string(nil), group.Rules...)
		for _, host := range group.Hosts {
			hostRules, err := c.fetchRuleHost(ctx, host)
			if err != nil {
				log.Printf("[clash] grouprule host skipped %s: %v", host, err)
				continue
			}
			rules = append(rules, hostRules...)
		}
		return dedupeStringList(rules)
	}
	return nil
}

func (c *subscriptionConverter) fetchRuleHost(ctx context.Context, rawURL string) ([]string, error) {
	cacheKey := strings.TrimSpace(rawURL)
	if cached, ok := getCachedRuleHost(cacheKey); ok {
		return cached, nil
	}
	body, err := c.fetchText(ctx, rawURL)
	if err != nil {
		return nil, err
	}
	rules := parseRuleHostContent(path.Ext(mustURLPath(rawURL)), []byte(body))
	setCachedRuleHost(cacheKey, rules)
	return rules, nil
}

func (c *subscriptionConverter) fetchText(ctx context.Context, rawURL string) (string, error) {
	if !c.allowPrivateFetch {
		if err := validatePublicFetchURL(ctx, rawURL); err != nil {
			return "", err
		}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", subscriptionUserAgent)
	resp, err := c.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("http %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxSubscriptionBytes))
	if err != nil {
		return "", err
	}
	return string(body), nil
}

func getCachedRuleHost(key string) ([]string, bool) {
	if key == "" {
		return nil, false
	}
	ruleHostCache.Lock()
	defer ruleHostCache.Unlock()
	entry, ok := ruleHostCache.items[key]
	if !ok || time.Now().After(entry.expiresAt) {
		if ok {
			delete(ruleHostCache.items, key)
		}
		return nil, false
	}
	return append([]string(nil), entry.rules...), true
}

func setCachedRuleHost(key string, rules []string) {
	if key == "" {
		return
	}
	ruleHostCache.Lock()
	defer ruleHostCache.Unlock()
	ruleHostCache.items[key] = ruleHostCacheEntry{
		rules:     append([]string(nil), rules...),
		expiresAt: time.Now().Add(24 * time.Hour),
	}
}

func mustURLPath(rawURL string) string {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return rawURL
	}
	return parsed.Path
}

func parseRuleHostContent(ext string, body []byte) []string {
	ext = strings.ToLower(strings.TrimSpace(ext))
	if ext == ".yaml" || ext == ".yml" {
		var payload struct {
			Payload []string `yaml:"payload"`
		}
		if err := yaml.Unmarshal(body, &payload); err == nil && len(payload.Payload) > 0 {
			return payload.Payload
		}
	}
	lines := splitLines(string(body))
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		out = append(out, line)
	}
	return out
}

func mihomoNodeToSingBoxOutbound(node ProxyNode) (map[string]any, error) {
	name := nodeName(node)
	out := map[string]any{
		"tag":         name,
		"type":        nodeType(node),
		"server":      nodeServer(node),
		"server_port": nodePort(node),
	}
	switch nodeType(node) {
	case "ss":
		out["type"] = "shadowsocks"
		out["method"] = nodeString(node, "cipher")
		out["password"] = nodeString(node, "password")
	case "vmess":
		out["uuid"] = nodeString(node, "uuid")
		out["alter_id"] = nodeInt(node, "alterId")
		out["security"] = firstNonEmptyValue(nodeString(node, "cipher"), "auto")
		out["packet_encoding"] = "packetaddr"
	case "vless":
		out["uuid"] = nodeString(node, "uuid")
		out["packet_encoding"] = "xudp"
		if flow := nodeString(node, "flow"); flow != "" {
			out["flow"] = flow
		}
	case "trojan":
		out["password"] = nodeString(node, "password")
	case "hysteria":
		copyIfNodeString(out, node, firstNodeStringKey(node, "auth-str", "auth_str", "auth"), "auth_str")
		copyIfNodeString(out, node, "up", "up_mbps")
		copyIfNodeString(out, node, "down", "down_mbps")
		copyIfNodeString(out, node, "obfs", "obfs")
	case "hysteria2":
		out["password"] = nodeString(node, "password")
		copyIfNodeString(out, node, "up", "up_mbps")
		copyIfNodeString(out, node, "down", "down_mbps")
	case "tuic":
		copyIfNodeString(out, node, "uuid", "uuid")
		copyIfNodeString(out, node, "password", "password")
		copyIfNodeString(out, node, "token", "token")
	case "anytls":
		out["password"] = nodeString(node, "password")
	case "wireguard":
		out["type"] = "wireguard"
		copyIfNodeAny(out, node, "private-key", "private_key")
		copyIfNodeAny(out, node, "public-key", "peer_public_key")
		copyIfNodeAny(out, node, "pre-shared-key", "pre_shared_key")
		copyIfNodeAny(out, node, "ip", "local_address")
		copyIfNodeAny(out, node, "reserved", "reserved")
	case "ssh":
		out["type"] = "ssh"
		copyIfNodeAny(out, node, "username", "user")
		copyIfNodeAny(out, node, "password", "password")
		copyIfNodeAny(out, node, "private-key", "private_key")
		copyIfNodeAny(out, node, "private-key-passphrase", "private_key_passphrase")
	case "http":
		out["type"] = "http"
		copyIfNodeAny(out, node, "username", "username")
		copyIfNodeAny(out, node, "password", "password")
	case "socks5":
		out["type"] = "socks"
		copyIfNodeAny(out, node, "username", "username")
		copyIfNodeAny(out, node, "password", "password")
	default:
		return nil, fmt.Errorf("unsupported type %s", nodeType(node))
	}
	applyMihomoTLS(out, node)
	applyMihomoTransport(out, node)
	return out, nil
}

func applyMihomoTLS(out map[string]any, node ProxyNode) {
	tlsEnabled := false
	if tls, ok := parseBoolAny(node["tls"]); ok {
		tlsEnabled = tls
	}
	if !tlsEnabled && nodeType(node) != "trojan" && len(mapFromNode(node, "reality-opts")) == 0 {
		return
	}
	tlsCfg := map[string]any{"enabled": true}
	if sni := firstNonEmptyValue(nodeString(node, "servername"), nodeString(node, "sni")); sni != "" {
		tlsCfg["server_name"] = sni
	}
	if insecure, ok := parseBoolAny(node["skip-cert-verify"]); ok {
		tlsCfg["insecure"] = insecure
	}
	if fp := firstNonEmptyValue(nodeString(node, "client-fingerprint"), nodeString(node, "fingerprint")); fp != "" {
		tlsCfg["utls"] = map[string]any{"enabled": true, "fingerprint": fp}
	}
	if reality := mapFromNode(node, "reality-opts"); len(reality) > 0 {
		realityCfg := map[string]any{"enabled": true}
		if publicKey := stringFromMap(reality, "public-key"); publicKey != "" {
			realityCfg["public_key"] = publicKey
		}
		if shortID := stringFromMap(reality, "short-id"); shortID != "" {
			realityCfg["short_id"] = shortID
		}
		tlsCfg["reality"] = realityCfg
	}
	out["tls"] = tlsCfg
}

func applyMihomoTransport(out map[string]any, node ProxyNode) {
	network := nodeString(node, "network")
	switch network {
	case "ws":
		opts := mapFromNode(node, "ws-opts")
		transport := map[string]any{"type": "ws"}
		if path := stringFromMap(opts, "path"); path != "" {
			transport["path"] = path
		}
		if headers := mapFromNode(ProxyNode(opts), "headers"); len(headers) > 0 {
			transport["headers"] = headers
		}
		out["transport"] = transport
	case "grpc":
		opts := mapFromNode(node, "grpc-opts")
		transport := map[string]any{"type": "grpc"}
		if service := stringFromMap(opts, "grpc-service-name"); service != "" {
			transport["service_name"] = service
		}
		out["transport"] = transport
	case "http", "h2":
		opts := mapFromNode(node, "http-opts")
		if len(opts) == 0 {
			opts = mapFromNode(node, "h2-opts")
		}
		transport := map[string]any{"type": "http"}
		if path := firstStringFromAny(opts["path"]); path != "" {
			transport["path"] = path
		}
		out["transport"] = transport
	}
}

type classifiedProxyGroups struct {
	groups            []classifiedProxyGroup
	autoGroupNames    []string
	notAutoGroupNames []string
}

type classifiedProxyGroup struct {
	name      string
	groupType string
	proxies   []string
}

func (c *subscriptionConverter) classifyNodes(nodes []ProxyNode) classifiedProxyGroups {
	remaining := nodeNames(nodes)
	groups := make(map[string][]string)
	matchers := append(append([]converterMatcher{}, c.settings.CustomGroups...), c.settings.Countries...)
	autoGroupSet := map[string]struct{}{}
	notAutoGroupSet := map[string]struct{}{}

	for _, matcher := range matchers {
		var matched []string
		var nextRemaining []string
		for _, name := range remaining {
			if isSubscriptionInfoName(name) || !matcher.Pattern.MatchString(name) {
				nextRemaining = append(nextRemaining, name)
				continue
			}
			matched = append(matched, name)
		}
		if len(matched) > 0 {
			groups[matcher.Name] = append(groups[matcher.Name], matched...)
			if matcher.NotAuto {
				notAutoGroupSet[matcher.Name] = struct{}{}
			} else {
				autoGroupSet[matcher.Name] = struct{}{}
			}
		}
		remaining = nextRemaining
	}
	if len(remaining) > 0 {
		groups["其它"] = append(groups["其它"], remaining...)
	}

	var out classifiedProxyGroups
	for _, matcher := range c.settings.CustomGroups {
		if proxies := groups[matcher.Name]; len(proxies) > 0 {
			out.groups = append(out.groups, classifiedProxyGroup{name: matcher.Name, groupType: "select", proxies: dedupeStringList(proxies)})
		}
	}
	for _, matcher := range c.settings.Countries {
		if proxies := groups[matcher.Name]; len(proxies) > 0 {
			out.groups = append(out.groups, classifiedProxyGroup{name: matcher.Name, groupType: "url-test", proxies: dedupeStringList(proxies)})
		}
	}
	if proxies := groups["其它"]; len(proxies) > 0 {
		out.groups = append(out.groups, classifiedProxyGroup{name: "其它", groupType: "select", proxies: dedupeStringList(proxies)})
	}
	out.autoGroupNames = sortedSetInsertionOrder(out.groups, autoGroupSet)
	out.notAutoGroupNames = sortedSetInsertionOrder(out.groups, notAutoGroupSet)
	return out
}

func sortedSetInsertionOrder(groups []classifiedProxyGroup, set map[string]struct{}) []string {
	var out []string
	for _, group := range groups {
		if _, ok := set[group.name]; ok {
			out = append(out, group.name)
		}
	}
	return out
}

func expandProxyPlaceholders(input, allNodes, autoGroups, notAutoGroups []string) []string {
	var out []string
	for _, proxy := range input {
		switch proxy {
		case "@全部节点":
			out = append(out, allNodes...)
		case "@自动选择":
			out = append(out, autoGroups...)
		case "@节点组":
			out = append(out, autoGroups...)
			out = append(out, notAutoGroups...)
		default:
			out = append(out, proxy)
		}
	}
	return dedupeStringList(out)
}

func (c *subscriptionConverter) defaultProxyGroupName() string {
	for _, group := range c.settings.ProxyGroups {
		if group.Default {
			return group.Name
		}
	}
	if len(c.settings.ProxyGroups) > 0 {
		return c.settings.ProxyGroups[0].Name
	}
	return "DIRECT"
}

func renderBase64Subscription(nodes []ProxyNode) ([]byte, error) {
	var lines []string
	for _, node := range nodes {
		line, err := nodeToShareURI(node)
		if err != nil {
			log.Printf("[clash] base64 conversion skipped %s: %v", nodeName(node), err)
			continue
		}
		lines = append(lines, line)
	}
	if len(lines) == 0 {
		return nil, fmt.Errorf("no nodes can be converted to base64 URI list")
	}
	encoded := base64.StdEncoding.EncodeToString([]byte(strings.Join(lines, "\n")))
	return []byte(encoded), nil
}

func nodeToShareURI(node ProxyNode) (string, error) {
	switch nodeType(node) {
	case "vmess":
		payload := map[string]any{
			"v":    "2",
			"ps":   nodeName(node),
			"add":  nodeServer(node),
			"port": nodePort(node),
			"id":   nodeString(node, "uuid"),
			"aid":  nodeInt(node, "alterId"),
			"scy":  firstNonEmptyValue(nodeString(node, "cipher"), "auto"),
			"net":  firstNonEmptyValue(nodeString(node, "network"), "tcp"),
			"tls":  "",
		}
		if tls, ok := parseBoolAny(node["tls"]); ok && tls {
			payload["tls"] = "tls"
		}
		if sni := firstNonEmptyValue(nodeString(node, "sni"), nodeString(node, "servername")); sni != "" {
			payload["sni"] = sni
		}
		if opts := mapFromNode(node, "ws-opts"); len(opts) > 0 {
			payload["path"] = stringFromMap(opts, "path")
			if headers := mapFromNode(ProxyNode(opts), "headers"); len(headers) > 0 {
				payload["host"] = stringFromMap(headers, "Host")
			}
		}
		data, _ := json.Marshal(payload)
		return "vmess://" + base64.StdEncoding.EncodeToString(data), nil
	case "vless", "trojan", "hysteria", "hysteria2", "tuic", "anytls":
		q := url.Values{}
		if network := nodeString(node, "network"); network != "" && nodeType(node) != "hysteria" && nodeType(node) != "hysteria2" && nodeType(node) != "tuic" && nodeType(node) != "anytls" {
			q.Set("type", network)
		}
		if sni := firstNonEmptyValue(nodeString(node, "sni"), nodeString(node, "servername")); sni != "" {
			q.Set("sni", sni)
		}
		if flow := nodeString(node, "flow"); flow != "" {
			q.Set("flow", flow)
		}
		if fp := firstNonEmptyValue(nodeString(node, "client-fingerprint"), nodeString(node, "fingerprint")); fp != "" {
			q.Set("fp", fp)
		}
		if insecure, ok := parseBoolAny(node["skip-cert-verify"]); ok && insecure {
			q.Set("insecure", "1")
		}
		for _, key := range []string{"obfs", "obfs-password", "up", "down", "congestion-controller", "udp-relay-mode"} {
			if value := nodeString(node, key); value != "" {
				q.Set(key, value)
			}
		}
		if alpn := nodeStringList(node["alpn"]); len(alpn) > 0 {
			q.Set("alpn", strings.Join(alpn, ","))
		}
		if reality := mapFromNode(node, "reality-opts"); len(reality) > 0 {
			q.Set("security", "reality")
			if pk := stringFromMap(reality, "public-key"); pk != "" {
				q.Set("pbk", pk)
			}
			if sid := stringFromMap(reality, "short-id"); sid != "" {
				q.Set("sid", sid)
			}
		} else if tls, ok := parseBoolAny(node["tls"]); ok && tls {
			q.Set("security", "tls")
		}
		user := nodeString(node, "uuid")
		switch nodeType(node) {
		case "vless":
			user = nodeString(node, "uuid")
		case "hysteria":
			user = firstNonEmptyValue(nodeString(node, "auth-str"), nodeString(node, "auth_str"), nodeString(node, "auth"))
		case "tuic":
			if nodeString(node, "token") != "" {
				user = nodeString(node, "token")
			} else {
				user = nodeString(node, "uuid") + ":" + nodeString(node, "password")
			}
		default:
			user = nodeString(node, "password")
		}
		userInfo := url.User(user)
		if nodeType(node) == "tuic" && nodeString(node, "token") == "" {
			userInfo = url.UserPassword(nodeString(node, "uuid"), nodeString(node, "password"))
		}
		u := &url.URL{Scheme: nodeType(node), User: userInfo, Host: fmt.Sprintf("%s:%d", nodeServer(node), nodePort(node)), RawQuery: q.Encode(), Fragment: nodeName(node)}
		return u.String(), nil
	case "ss":
		userInfo := base64.RawURLEncoding.EncodeToString([]byte(nodeString(node, "cipher") + ":" + nodeString(node, "password")))
		u := &url.URL{Scheme: "ss", User: url.User(userInfo), Host: fmt.Sprintf("%s:%d", nodeServer(node), nodePort(node)), Fragment: nodeName(node)}
		return u.String(), nil
	default:
		return "", fmt.Errorf("unsupported type %s", nodeType(node))
	}
}

func groupRules(settings *converterSettings, groupName string) []string {
	if settings == nil {
		settings = defaultConverterSettings()
	}
	return newSubscriptionConverter(settings, nil, "").groupRules(context.Background(), groupName)
}

func isSubscriptionInfoName(name string) bool {
	keywords := []string{"剩余流量", "上传流量", "下载流量", "套餐流量", "总流量", "已用", "到期", "过期", "有效期", "重置", "订阅信息"}
	for _, keyword := range keywords {
		if strings.Contains(name, keyword) {
			return true
		}
	}
	return false
}

func nodeNames(nodes []ProxyNode) []string {
	out := make([]string, 0, len(nodes))
	for _, node := range nodes {
		if name := nodeName(node); name != "" {
			out = append(out, name)
		}
	}
	return out
}

func dedupeStringList(values []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}

func md5Hex(value string) string {
	sum := md5.Sum([]byte(value))
	return hex.EncodeToString(sum[:])
}

func stringFromMap(m map[string]any, key string) string {
	if m == nil {
		return ""
	}
	return stringFromAny(m[key])
}

func stringFromAny(value any) string {
	switch v := value.(type) {
	case string:
		return strings.TrimSpace(v)
	case int:
		return strconv.Itoa(v)
	case int64:
		return strconv.FormatInt(v, 10)
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64)
	default:
		if v == nil {
			return ""
		}
		return strings.TrimSpace(fmt.Sprint(v))
	}
}

func nodeStringList(value any) []string {
	switch typed := value.(type) {
	case []string:
		return compactStrings(typed)
	case []any:
		out := make([]string, 0, len(typed))
		for _, item := range typed {
			if value := stringFromAny(item); value != "" {
				out = append(out, value)
			}
		}
		return compactStrings(out)
	case string:
		var out []string
		for _, part := range strings.Split(typed, ",") {
			if part = strings.TrimSpace(part); part != "" {
				out = append(out, part)
			}
		}
		return compactStrings(out)
	default:
		return nil
	}
}

func firstAny(m map[string]any, keys ...string) any {
	for _, key := range keys {
		if value, ok := m[key]; ok && value != nil {
			return value
		}
	}
	return nil
}

func intFromAny(value any) int {
	switch v := value.(type) {
	case int:
		return v
	case int64:
		return int(v)
	case float64:
		return int(v)
	case json.Number:
		i, _ := v.Int64()
		return int(i)
	case string:
		i, _ := strconv.Atoi(strings.TrimSpace(v))
		return i
	default:
		return 0
	}
}

func firstStringFromAny(value any) string {
	switch v := value.(type) {
	case []any:
		if len(v) > 0 {
			return stringFromAny(v[0])
		}
	case []string:
		if len(v) > 0 {
			return strings.TrimSpace(v[0])
		}
	default:
		return stringFromAny(v)
	}
	return ""
}

func copyIfPresent(dst ProxyNode, src map[string]any, keys ...string) {
	for _, key := range keys {
		if value, ok := src[key]; ok && value != nil {
			dst[key] = value
			return
		}
	}
}

func copyFirstPresentAs(dst ProxyNode, src map[string]any, dstKey string, srcKeys ...string) {
	for _, key := range srcKeys {
		if value, ok := src[key]; ok && value != nil {
			dst[dstKey] = normalizeCopiedValue(value)
			return
		}
	}
}

func normalizeCopiedValue(value any) any {
	switch typed := value.(type) {
	case []any:
		if len(typed) == 1 {
			return typed[0]
		}
	case []string:
		if len(typed) == 1 {
			return typed[0]
		}
	}
	return value
}

func copyIfNodeString(dst map[string]any, src ProxyNode, from, to string) {
	if value := nodeString(src, from); value != "" {
		dst[to] = value
	}
}

func copyIfNodeAny(dst map[string]any, src ProxyNode, from, to string) {
	if value, ok := src[from]; ok && value != nil {
		dst[to] = cloneAny(value)
	}
}

func firstNodeStringKey(node ProxyNode, keys ...string) string {
	for _, key := range keys {
		if nodeString(node, key) != "" {
			return key
		}
	}
	return ""
}

func mapFromNode(node ProxyNode, key string) map[string]any {
	raw, ok := node[key]
	if !ok || raw == nil {
		return nil
	}
	if typed, ok := raw.(map[string]any); ok {
		return typed
	}
	return nil
}

func cleanEmptyAnyMap(input map[string]any) map[string]any {
	out := map[string]any{}
	for key, value := range input {
		switch typed := value.(type) {
		case map[string]any:
			cleaned := cleanEmptyAnyMap(typed)
			if len(cleaned) > 0 {
				out[key] = cleaned
			}
		case []any:
			if len(typed) > 0 {
				out[key] = typed
			}
		case []string:
			if len(typed) > 0 {
				out[key] = typed
			}
		case string:
			if strings.TrimSpace(typed) != "" {
				out[key] = typed
			}
		case nil:
			continue
		default:
			out[key] = value
		}
	}
	return out
}
