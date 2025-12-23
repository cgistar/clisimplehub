package executor

import (
	"context"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"clisimplehub/internal/logger"

	"golang.org/x/net/proxy"
)

// DefaultHTTPTimeout 默认 HTTP 超时时间
const DefaultHTTPTimeout = 300 * time.Second

// NewHTTPClient 创建 HTTP 客户端，支持代理配置
// 优先级: endpoint.ProxyURL > 默认直连
func NewHTTPClient(endpoint *EndpointConfig, timeout time.Duration) *http.Client {
	if timeout <= 0 {
		timeout = DefaultHTTPTimeout
	}

	client := &http.Client{Timeout: timeout}

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
		return &http.Transport{Proxy: http.ProxyURL(parsedURL)}
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

	dialer, err := proxy.SOCKS5("tcp", parsedURL.Host, auth, proxy.Direct)
	if err != nil {
		logger.Warn("[Executor] create SOCKS5 dialer failed: %v", err)
		return nil
	}

	return &http.Transport{
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			return dialer.Dial(network, addr)
		},
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
