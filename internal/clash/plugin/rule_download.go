package clashplugin

import (
	"context"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"clisimplehub/internal/executor"
)

func builtInSocks5ProxyURL(svc *ClashService) string {
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
	if cfg.SocksPort <= 0 || cfg.SocksPort > 65535 {
		return ""
	}
	host := strings.TrimSpace(cfg.SocksListen)
	switch host {
	case "", "0.0.0.0", "::", "[::]":
		host = "127.0.0.1"
	}
	return "socks5://" + net.JoinHostPort(host, strconv.Itoa(cfg.SocksPort))
}

func (c *subscriptionConverter) isConfiguredRuleURL(rawURL string) bool {
	if c.ini != nil {
		for _, rule := range c.ini.Rules {
			if rule.Source == rawURL {
				return true
			}
		}
	}
	if c.settings == nil {
		return false
	}
	for _, group := range c.settings.ProxyGroups {
		for _, host := range group.Hosts {
			if host == rawURL {
				return true
			}
		}
	}
	for _, group := range c.settings.OtherRules {
		for _, host := range group.Hosts {
			if host == rawURL {
				return true
			}
		}
	}
	return false
}

func (c *subscriptionConverter) retryRuleThroughProxy(ctx context.Context, rawURL string, directErr error) (string, error) {
	response, err := c.retryRuleThroughProxyConditional(ctx, rawURL, "", "", directErr)
	return response.body, err
}

func (c *subscriptionConverter) retryRuleThroughProxyConditional(ctx context.Context, rawURL, etag, lastModified string, directErr error) (ruleHTTPResponse, error) {
	origin, err := validateProxyRuleURL(rawURL)
	if err != nil {
		return ruleHTTPResponse{}, fmt.Errorf("direct rule download failed: %v; proxy retry refused: %w", directErr, err)
	}
	proxyURL := c.ruleProxyURL()
	if proxyURL == "" {
		return ruleHTTPResponse{}, fmt.Errorf("direct rule download failed: %w; the built-in SOCKS5 proxy is not running", directErr)
	}
	log.Printf("[clash] direct rule download failed for %s: %v; retrying through built-in SOCKS5 proxy %s", rawURL, directErr, proxyURL)
	client := executor.NewHTTPClientForcedProxyURL(proxyURL, 30*time.Second)
	defer client.CloseIdleConnections()
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) >= 5 {
			return fmt.Errorf("stopped after 5 redirects")
		}
		target, err := validateProxyRuleURL(req.URL.String())
		if err != nil {
			return err
		}
		// 仅管理员配置的来源及其同源跳转允许由 SOCKS5 端解析，防止本地 fake-IP 阻断重试。
		if target.Scheme == origin.Scheme && strings.EqualFold(target.Host, origin.Host) {
			return nil
		}
		return validatePublicFetchURL(req.Context(), target.String())
	}
	response, proxyErr := fetchRuleTextConditional(ctx, client, rawURL, etag, lastModified)
	if proxyErr != nil {
		return ruleHTTPResponse{}, fmt.Errorf("direct rule download failed: %v; built-in SOCKS5 retry failed: %w", directErr, proxyErr)
	}
	return response, nil
}

func validateProxyRuleURL(rawURL string) (*url.URL, error) {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return nil, err
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return nil, fmt.Errorf("unsupported rule URL scheme: %s", parsed.Scheme)
	}
	host := strings.TrimSuffix(strings.ToLower(parsed.Hostname()), ".")
	if host == "" || parsed.User != nil {
		return nil, fmt.Errorf("invalid rule URL")
	}
	if ip := net.ParseIP(host); ip != nil {
		if !isPublicIP(ip) {
			return nil, fmt.Errorf("blocked non-public rule host: %s", host)
		}
	} else if !strings.Contains(host, ".") || strings.HasSuffix(host, ".localhost") || strings.HasSuffix(host, ".local") {
		return nil, fmt.Errorf("blocked local rule host: %s", host)
	}
	return parsed, nil
}
