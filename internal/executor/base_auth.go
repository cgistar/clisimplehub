package executor

import (
	"net/http"
	"strings"

	appmiddleware "clisimplehub/internal/middleware"
)

// AuthApplier 用于将端点鉴权信息应用到上游请求（扩展点）
type AuthApplier interface {
	Apply(req *http.Request, endpoint *EndpointConfig, isStreaming bool)
}

type defaultAuthApplier struct{}

func (defaultAuthApplier) Apply(req *http.Request, endpoint *EndpointConfig, isStreaming bool) {
	if req == nil || endpoint == nil {
		return
	}

	ApplyAuthForEndpoint(req, endpoint, isStreaming)
}

func ApplyAuthForInterfaceType(req *http.Request, apiKey string, interfaceType string, isStreaming bool) {
	if req == nil {
		return
	}

	key := strings.TrimSpace(apiKey)
	if key == "" {
		return
	}
	// Prevent header/query injection; net/http will likely reject these, but fail closed early.
	if strings.ContainsAny(key, "\r\n") {
		return
	}

	switch strings.ToLower(strings.TrimSpace(interfaceType)) {
	case "gemini":
		q := req.URL.Query()
		q.Set("key", key)
		if isStreaming {
			q.Set("alt", "sse")
		}
		req.URL.RawQuery = q.Encode()
	case "codex", "chat":
		req.Header.Set("Authorization", "Bearer "+key)
	default:
		req.Header.Set("Authorization", "Bearer "+key)
		req.Header.Set("x-api-key", key)
	}
}

func ApplyAuthForEndpoint(req *http.Request, endpoint *EndpointConfig, isStreaming bool) {
	if req == nil || endpoint == nil {
		return
	}

	key := strings.TrimSpace(endpoint.APIKey)
	if key == "" || strings.ContainsAny(key, "\r\n") {
		return
	}

	interfaceType := strings.ToLower(strings.TrimSpace(endpoint.InterfaceType))
	switch interfaceType {
	case "gemini":
		q := req.URL.Query()
		q.Set("key", key)
		if isStreaming {
			q.Set("alt", "sse")
		}
		req.URL.RawQuery = q.Encode()
		return
	case "codex", "chat":
		req.Header.Del("x-api-key")
		req.Header.Set("Authorization", "Bearer "+key)
		return
	}

	if isClaudeMessagesEndpointRequest(req, endpoint) {
		authMode := appmiddleware.ResolveClaudeMessagesAuthModeForEndpoint(endpoint)
		if isOfficialAnthropicRequest(req, endpoint) && authMode == "api_key" {
			req.Header.Del("Authorization")
			req.Header.Set("x-api-key", key)
			return
		}
		req.Header.Del("x-api-key")
		req.Header.Set("Authorization", "Bearer "+key)
		return
	}

	ApplyAuthForInterfaceType(req, key, endpoint.InterfaceType, isStreaming)
}

func isClaudeMessagesEndpointRequest(req *http.Request, endpoint *EndpointConfig) bool {
	if req == nil || req.URL == nil {
		return false
	}
	return strings.HasSuffix(strings.ToLower(strings.TrimSpace(req.URL.Path)), "/v1/messages")
}

func isOfficialAnthropicRequest(req *http.Request, endpoint *EndpointConfig) bool {
	if req != nil && req.URL != nil {
		host := strings.ToLower(strings.TrimSpace(req.URL.Host))
		if host == "api.anthropic.com" {
			return true
		}
	}
	if endpoint == nil {
		return false
	}
	base := strings.ToLower(strings.TrimSpace(endpoint.APIURL))
	base = strings.TrimPrefix(base, "https://")
	base = strings.TrimPrefix(base, "http://")
	base = strings.TrimRight(base, "/")
	return base == "api.anthropic.com"
}
