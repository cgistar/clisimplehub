package clashplugin

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"clisimplehub/internal/executor"
)

const (
	routerOSChinaIPv4URL = "https://raw.githubusercontent.com/pexcn/daily/refs/heads/gh-pages/chnroute/chnroute.txt"
	routerOSChinaIPv6URL = "https://raw.githubusercontent.com/pexcn/daily/refs/heads/gh-pages/chnroute/chnroute6.txt"
	maxChinaRouteBytes   = 2 << 20
)

type routerOSChinaRoutes struct {
	IPv4 []string
	IPv6 []string
}

func fetchRouterOSChinaRoutes(ctx context.Context, svc *ClashService) (*routerOSChinaRoutes, error) {
	routes, directErr := downloadRouterOSChinaRoutes(ctx, defaultSubscriptionHTTPClient(), routerOSChinaIPv4URL, routerOSChinaIPv6URL)
	if directErr == nil {
		return routes, nil
	}

	proxyURL := routerOSSocks5ProxyURL(svc)
	if proxyURL == "" {
		return nil, fmt.Errorf("download China routes directly: %w; the built-in SOCKS5 proxy is not running", directErr)
	}

	log.Printf("[clash] direct China route download failed, retrying through built-in SOCKS5 proxy %s", proxyURL)
	client := executor.NewHTTPClientForcedProxyURL(proxyURL, 30*time.Second)
	routes, proxyErr := downloadRouterOSChinaRoutes(ctx, client, routerOSChinaIPv4URL, routerOSChinaIPv6URL)
	if proxyErr == nil {
		return routes, nil
	}
	return nil, fmt.Errorf("download China routes directly: %v; built-in SOCKS5 retry failed: %w", directErr, proxyErr)
}

func downloadRouterOSChinaRoutes(ctx context.Context, client *http.Client, ipv4URL, ipv6URL string) (*routerOSChinaRoutes, error) {
	ipv4, err := downloadCIDRs(ctx, client, ipv4URL, false)
	if err != nil {
		return nil, fmt.Errorf("download IPv4 routes: %w", err)
	}
	ipv6, err := downloadCIDRs(ctx, client, ipv6URL, true)
	if err != nil {
		return nil, fmt.Errorf("download IPv6 routes: %w", err)
	}
	return &routerOSChinaRoutes{IPv4: ipv4, IPv6: ipv6}, nil
}

func downloadCIDRs(ctx context.Context, client *http.Client, rawURL string, wantIPv6 bool) ([]string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", subscriptionUserAgent)
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4<<10))
		return nil, fmt.Errorf("upstream returned %s", resp.Status)
	}

	limited := io.LimitReader(resp.Body, maxChinaRouteBytes+1)
	data, err := io.ReadAll(limited)
	if err != nil {
		return nil, err
	}
	if len(data) > maxChinaRouteBytes {
		return nil, fmt.Errorf("response exceeds %d bytes", maxChinaRouteBytes)
	}

	routes := make([]string, 0, bytes.Count(data, []byte{'\n'}))
	seen := make(map[string]struct{})
	family := 4
	if wantIPv6 {
		family = 6
	}
	scanner := bufio.NewScanner(bytes.NewReader(data))
	for scanner.Scan() {
		route := strings.TrimSpace(scanner.Text())
		if route == "" {
			continue
		}
		ip, _, err := net.ParseCIDR(route)
		if err != nil || (ip.To4() == nil) != wantIPv6 {
			return nil, fmt.Errorf("invalid IPv%d CIDR %q", family, route)
		}
		if _, ok := seen[route]; ok {
			continue
		}
		seen[route] = struct{}{}
		routes = append(routes, route)
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	if len(routes) == 0 {
		return nil, fmt.Errorf("upstream returned no routes")
	}
	return routes, nil
}

func routerOSSocks5ProxyURL(svc *ClashService) string {
	if svc == nil {
		return ""
	}
	svc.mu.RLock()
	running := svc.running
	svc.mu.RUnlock()
	if !running || svc.config == nil {
		return ""
	}
	cfg := svc.config.Get()
	host := strings.TrimSpace(cfg.SocksListen)
	if cfg.SocksPort <= 0 || cfg.SocksPort > 65535 {
		return ""
	}
	switch host {
	case "", "0.0.0.0", "::", "[::]":
		host = "127.0.0.1"
	}
	return "socks5://" + net.JoinHostPort(host, strconv.Itoa(cfg.SocksPort))
}

func validRouterOSAddressListName(name string) bool {
	if len(name) == 0 || len(name) > 64 {
		return false
	}
	for _, r := range name {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("._-", r) {
			continue
		}
		return false
	}
	return true
}

func renderRouterOSChinaRoutes(name string, routes *routerOSChinaRoutes) []byte {
	var out strings.Builder
	fmt.Fprintf(&out, "/ip firewall address-list remove [/ip firewall address-list find list=%s]\n", name)
	out.WriteString("/ip firewall address-list\n")
	// fmt.Fprintf(&out, "add address=10.0.0.0/8 list=%s comment=private-network\n", name)
	// fmt.Fprintf(&out, "add address=172.16.0.0/12 list=%s comment=private-network\n", name)
	// fmt.Fprintf(&out, "add address=192.168.0.0/16 list=%s comment=private-network\n", name)
	for _, route := range routes.IPv4 {
		fmt.Fprintf(&out, ":do { add address=%s list=%s } on-error={}\n", route, name)
	}
	out.WriteString("/ipv6 firewall address-list remove [/ipv6 firewall address-list find list=" + name + "]\n")
	out.WriteString("/ipv6 firewall address-list\n")
	fmt.Fprintf(&out, "add address=fe80::/10 list=%s comment=private-network\n", name)
	for _, route := range routes.IPv6 {
		fmt.Fprintf(&out, ":do { add address=%s list=%s } on-error={}\n", route, name)
	}
	return []byte(out.String())
}
