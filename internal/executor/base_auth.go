package executor

import (
	"net/http"
	"strings"
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

	ApplyAuthForInterfaceType(req, endpoint.APIKey, endpoint.InterfaceType, isStreaming)
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
	case "kiro":
		// Kiro auth handled by transformer's auth manager
		// Do NOT set Authorization here - handled by kiro-specific logic
		return
	case "codex", "chat":
		req.Header.Set("Authorization", "Bearer "+key)
	default:
		req.Header.Set("Authorization", "Bearer "+key)
		req.Header.Set("x-api-key", key)
	}
}
