package executor

import (
	"context"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"clisimplehub/internal/logger"
	"clisimplehub/internal/plugin"

	"golang.org/x/net/proxy"
)

// DefaultHTTPTimeout 默认 HTTP 超时时间
const DefaultHTTPTimeout = 300 * time.Second

const (
	// DisableHTTPClientTimeout disables http.Client-level total timeout.
	// Passed through normalizeHTTPClientTimeout which converts it to 0.
	DisableHTTPClientTimeout time.Duration = -1

	DefaultHTTPDialTimeout       = 10 * time.Second
	DefaultTLSHandshakeTimeout   = 10 * time.Second
	DefaultResponseHeaderTimeout = 120 * time.Second

	// DefaultStreamReadIdleTimeout is the max idle gap while reading streaming body.
	DefaultStreamReadIdleTimeout = 300 * time.Second
)

var (
	sharedTransportOnce sync.Once
	sharedTransport     *http.Transport
)

func getSharedTransport() *http.Transport {
	sharedTransportOnce.Do(func() {
		sharedTransport = newBaseTransport()
	})
	return sharedTransport
}

// NewHTTPClient 创建 HTTP 客户端，支持代理配置
// 优先级: plugin.GetGlobalProxyURL() > endpoint.ProxyURL > 默认直连
func NewHTTPClient(endpoint *EndpointConfig, timeout time.Duration) *http.Client {
	client := &http.Client{
		Timeout:   normalizeHTTPClientTimeout(timeout),
		Transport: getSharedTransport(),
	}

	// Global proxy takes highest priority.
	if gp := plugin.GetGlobalProxyProviderCached(); gp != nil {
		if gpURL := gp.GetGlobalProxyURL(); gpURL != "" {
			if t := buildProxyTransport(gpURL); t != nil {
				client.Transport = t
				return client
			}
		}
	}

	if endpoint == nil {
		return client
	}

	proxyURL := strings.TrimSpace(endpoint.ProxyURL)
	if proxyURL == "" {
		return client
	}

	transport := buildProxyTransport(proxyURL)
	if transport != nil {
		client.Transport = transport
	}

	return client
}

// NewHTTPClientForcedProxyURL creates an HTTP client that only uses the provided proxy URL.
// When proxyURL is empty, it forces direct connection (does not use environment proxy variables).
func NewHTTPClientForcedProxyURL(proxyURL string, timeout time.Duration) *http.Client {
	transport := newBaseTransport()
	transport.Proxy = nil
	client := &http.Client{
		Timeout:   normalizeHTTPClientTimeout(timeout),
		Transport: transport,
	}

	proxyURL = strings.TrimSpace(proxyURL)
	if proxyURL == "" {
		client.Transport = transport
		return client
	}

	parsedURL, err := url.Parse(proxyURL)
	if err != nil {
		logger.Warn("[Executor] parse proxy URL failed: %v", err)
		client.Transport = transport
		return client
	}

	switch parsedURL.Scheme {
	case "socks5":
		var auth *proxy.Auth
		if parsedURL.User != nil {
			username := parsedURL.User.Username()
			password, _ := parsedURL.User.Password()
			auth = &proxy.Auth{User: username, Password: password}
		}
		baseDialer := &net.Dialer{
			Timeout:   DefaultHTTPDialTimeout,
			KeepAlive: 30 * time.Second,
		}
		dialer, err := proxy.SOCKS5("tcp", parsedURL.Host, auth, baseDialer)
		if err != nil {
			logger.Warn("[Executor] create SOCKS5 dialer failed: %v", err)
			client.Transport = transport
			return client
		}
		transport.DialContext = dialContextFromProxyDialer(dialer)
		client.Transport = transport
		return client
	case "http", "https":
		transport.Proxy = http.ProxyURL(parsedURL)
		client.Transport = transport
		return client
	default:
		logger.Warn("[Executor] unsupported proxy scheme: %s", parsedURL.Scheme)
		client.Transport = transport
		return client
	}
}

// buildProxyTransport 根据代理 URL 创建 HTTP Transport
// 支持 socks5, http, https 代理协议
func buildProxyTransport(proxyURL string) *http.Transport {
	if proxyURL == "" {
		return nil
	}

	parsedURL, err := url.Parse(proxyURL)
	if err != nil {
		logger.Warn("[Executor] parse proxy URL failed: %v", err)
		return nil
	}

	switch parsedURL.Scheme {
	case "socks5":
		return buildSOCKS5Transport(parsedURL)
	case "http", "https":
		transport := newBaseTransport()
		transport.Proxy = http.ProxyURL(parsedURL)
		return transport
	default:
		logger.Warn("[Executor] unsupported proxy scheme: %s", parsedURL.Scheme)
		return nil
	}
}

// buildSOCKS5Transport 创建 SOCKS5 代理 Transport
func buildSOCKS5Transport(parsedURL *url.URL) *http.Transport {
	var auth *proxy.Auth
	if parsedURL.User != nil {
		username := parsedURL.User.Username()
		password, _ := parsedURL.User.Password()
		auth = &proxy.Auth{User: username, Password: password}
	}

	baseDialer := &net.Dialer{
		Timeout:   DefaultHTTPDialTimeout,
		KeepAlive: 30 * time.Second,
	}
	dialer, err := proxy.SOCKS5("tcp", parsedURL.Host, auth, baseDialer)
	if err != nil {
		logger.Warn("[Executor] create SOCKS5 dialer failed: %v", err)
		return nil
	}

	transport := newBaseTransport()
	transport.DialContext = dialContextFromProxyDialer(dialer)
	return transport
}

// dialContextFromProxyDialer converts a proxy.Dialer into a DialContext function.
// If the dialer implements proxy.ContextDialer (e.g. modern x/net SOCKS5), context
// cancellation and deadlines are fully propagated. Otherwise falls back to Dial.
func dialContextFromProxyDialer(d proxy.Dialer) func(ctx context.Context, network, addr string) (net.Conn, error) {
	if cd, ok := d.(proxy.ContextDialer); ok {
		return cd.DialContext
	}
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		return d.Dial(network, addr)
	}
}

// ApplyEndpointHeaders 应用端点配置的自定义 headers
func ApplyEndpointHeaders(req *http.Request, endpoint *EndpointConfig) {
	if endpoint == nil || len(endpoint.Headers) == 0 {
		return
	}

	for key, value := range endpoint.Headers {
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		if key != "" && value != "" {
			req.Header.Set(key, value)
		}
	}
}

// ResolveUpstreamModel 根据配置解析上游模型名称
// 如果配置了模型映射，返回映射后的模型名；否则返回原始模型名
func ResolveUpstreamModel(requestModel string, endpoint *EndpointConfig) string {
	if endpoint == nil {
		return requestModel
	}

	if strings.TrimSpace(requestModel) == "" {
		if endpoint.Model != "" {
			return endpoint.Model
		}
		return ""
	}

	// 检查模型映射
	for _, mapping := range endpoint.Models {
		alias := strings.TrimSpace(mapping.Alias)
		name := strings.TrimSpace(mapping.Name)

		// 如果请求的模型匹配别名，返回实际模型名
		if alias != "" && strings.EqualFold(alias, requestModel) {
			if name != "" {
				return name
			}
			return requestModel
		}

		// 如果请求的模型匹配实际名称，直接返回
		if name != "" && strings.EqualFold(name, requestModel) {
			return name
		}
	}

	// 如果端点配置了默认模型，使用默认模型
	if endpoint.Model != "" {
		return endpoint.Model
	}

	return requestModel
}

func normalizeHTTPClientTimeout(timeout time.Duration) time.Duration {
	switch {
	case timeout < 0:
		return 0
	case timeout == 0:
		return DefaultHTTPTimeout
	default:
		return timeout
	}
}

func newBaseTransport() *http.Transport {
	transport := http.DefaultTransport.(*http.Transport).Clone()

	dialer := &net.Dialer{
		Timeout:   DefaultHTTPDialTimeout,
		KeepAlive: 30 * time.Second,
	}
	transport.DialContext = dialer.DialContext
	transport.TLSHandshakeTimeout = DefaultTLSHandshakeTimeout
	transport.ResponseHeaderTimeout = DefaultResponseHeaderTimeout

	transport.DisableKeepAlives = false
	transport.MaxIdleConns = 100
	transport.MaxIdleConnsPerHost = 10
	transport.IdleConnTimeout = 90 * time.Second
	return transport
}
