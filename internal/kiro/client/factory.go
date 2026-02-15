package client

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"golang.org/x/net/proxy"
)

// Factory HTTP 客户端工厂，负责创建支持代理的 HTTP 客户端
type Factory struct {
	configGetter ConfigGetter
}

// NewFactory 创建 HTTP 客户端工厂
func NewFactory(configGetter ConfigGetter) *Factory {
	return &Factory{
		configGetter: configGetter,
	}
}

// NewHTTPClient 创建支持代理的 HTTP 客户端
// 从 ConfigGetter 读取代理配置，支持 SOCKS5、HTTP、HTTPS 代理
func (f *Factory) NewHTTPClient(timeout time.Duration) *http.Client {
	if timeout <= 0 {
		timeout = 30 * time.Second
	}

	client := &http.Client{Timeout: timeout}

	// 获取代理配置
	proxyURL := ""
	if f.configGetter != nil {
		proxyURL = strings.TrimSpace(f.configGetter.GetProxyURL())
	}

	// 始终设置显式的 transport，避免意外使用环境变量代理
	// Kiro 代理必须来自显式配置（kiro.json），不隐式使用环境变量代理
	if proxyURL == "" {
		transport := http.DefaultTransport.(*http.Transport).Clone()
		transport.Proxy = nil
		client.Transport = transport
		return client
	}

	// 构建代理 transport
	transport := buildProxyTransport(proxyURL)
	if transport == nil {
		// 代理配置无效，使用直连
		transport = http.DefaultTransport.(*http.Transport).Clone()
		transport.Proxy = nil
	}
	client.Transport = transport
	return client
}

// NewHTTPClientWithProxy 创建支持代理的 HTTP 客户端（静态代理 URL）
// 这是一个便捷函数，用于不需要 ConfigGetter 的场景
func NewHTTPClientWithProxy(proxyURL string, timeout time.Duration) *http.Client {
	if timeout <= 0 {
		timeout = 30 * time.Second
	}

	client := &http.Client{Timeout: timeout}

	// 始终设置显式的 transport，避免意外使用环境变量代理
	proxyURL = strings.TrimSpace(proxyURL)
	if proxyURL == "" {
		transport := http.DefaultTransport.(*http.Transport).Clone()
		transport.Proxy = nil
		client.Transport = transport
		return client
	}

	// 构建代理 transport
	transport := buildProxyTransport(proxyURL)
	if transport == nil {
		// 代理配置无效，使用直连
		transport = http.DefaultTransport.(*http.Transport).Clone()
		transport.Proxy = nil
	}
	client.Transport = transport
	return client
}

// buildProxyTransport 创建支持代理的 HTTP transport
// 支持 socks5、http、https 代理协议
func buildProxyTransport(proxyURL string) *http.Transport {
	if proxyURL == "" {
		return nil
	}

	base := http.DefaultTransport.(*http.Transport).Clone()
	base.Proxy = nil

	parsedURL, err := url.Parse(proxyURL)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Warning: failed to parse kiro proxy URL: %v\n", err)
		return nil
	}

	switch parsedURL.Scheme {
	case "socks5":
		return buildSOCKS5Transport(base, parsedURL)
	case "http", "https":
		base.Proxy = http.ProxyURL(parsedURL)
		return base
	default:
		fmt.Fprintf(os.Stderr, "Warning: unsupported kiro proxy scheme: %s\n", parsedURL.Scheme)
		return nil
	}
}

// buildSOCKS5Transport 创建 SOCKS5 代理 transport
func buildSOCKS5Transport(base *http.Transport, parsedURL *url.URL) *http.Transport {
	if base == nil {
		base = http.DefaultTransport.(*http.Transport).Clone()
		base.Proxy = nil
	}

	var auth *proxy.Auth
	if parsedURL.User != nil {
		username := parsedURL.User.Username()
		password, _ := parsedURL.User.Password()
		auth = &proxy.Auth{User: username, Password: password}
	}

	dialer, err := proxy.SOCKS5("tcp", parsedURL.Host, auth, proxy.Direct)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Warning: failed to create kiro SOCKS5 dialer: %v\n", err)
		return nil
	}

	base.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
		return dialer.Dial(network, addr)
	}
	return base
}
