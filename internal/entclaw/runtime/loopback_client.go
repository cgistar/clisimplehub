package entclawruntime

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"net/http"
	"strings"
)

const (
	internalLoopbackHeader = "X-Entclaw-Internal"
	fallbackLoopbackURL    = "http://127.0.0.1:5600"
)

type LoopbackClient interface {
	Do(ctx context.Context, source *http.Request, path string, body []byte) (*http.Response, error)
}

type HTTPClientLoopback struct {
	Client *http.Client
}

func (c HTTPClientLoopback) Do(ctx context.Context, source *http.Request, path string, body []byte) (*http.Response, error) {
	trimmedPath := strings.TrimSpace(path)
	if trimmedPath == "" {
		return nil, fmt.Errorf("loopback path is required")
	}

	method := http.MethodPost
	headers := make(http.Header)
	if source != nil {
		if source.Method != "" {
			method = source.Method
		}
		headers = source.Header.Clone()
	}
	headers.Set(internalLoopbackHeader, "1")

	req, err := http.NewRequestWithContext(ctx, method, loopbackBaseURL(source)+trimmedPath, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create loopback request: %w", err)
	}
	req.Header = headers

	client := c.Client
	if client == nil {
		client = http.DefaultClient
	}
	return client.Do(req)
}

func loopbackBaseURL(source *http.Request) string {
	if source == nil {
		return fallbackLoopbackURL
	}

	localAddr, _ := source.Context().Value(http.LocalAddrContextKey).(net.Addr)
	if localAddr == nil {
		return fallbackLoopbackURL
	}

	if tcpAddr, ok := localAddr.(*net.TCPAddr); ok && tcpAddr.Port > 0 {
		return fmt.Sprintf("http://127.0.0.1:%d", tcpAddr.Port)
	}

	_, port, err := net.SplitHostPort(localAddr.String())
	if err != nil || port == "" {
		return fallbackLoopbackURL
	}
	return "http://127.0.0.1:" + port
}
