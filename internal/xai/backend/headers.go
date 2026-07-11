package backend

import (
	"net/http"
	"strings"

	xaiShared "clisimplehub/internal/xai/shared"
)

// cli-chat-proxy 会校验客户端版本；缺省时返回 426（version=none）。
const (
	DefaultClientVersion = "0.2.93"
	DefaultUserAgent     = "xai-grok-cli/" + DefaultClientVersion
	DefaultTokenAuth     = "xai-grok-cli"
	DefaultClientSurface = "grok-cli"
	HeaderClientVersion  = "x-grok-client-version"
	HeaderClientSurface  = "x-grok-client-surface"
	HeaderTokenAuth      = "X-XAI-Token-Auth"
	HeaderGrokConvID     = "x-grok-conv-id"
	HeaderIdempotencyKey = "x-idempotency-key"
)

// 注意：不透传客户端头，避免 x-api-key / User-Agent 污染上游。
func applyXAICoreHeaders(r *http.Request, token string, stream bool, sessionID string) {
	if r == nil {
		return
	}
	// 始终覆盖 Content-Type / Accept
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		r.Header.Set("Content-Type", "application/json")
	}
	if strings.TrimSpace(token) != "" {
		r.Header.Set("Authorization", "Bearer "+strings.TrimSpace(token))
	}
	if stream {
		r.Header.Set("Accept", "text/event-stream")
	} else {
		r.Header.Set("Accept", "application/json")
	}
	r.Header.Set("Connection", "Keep-Alive")
	if sid := strings.TrimSpace(sessionID); sid != "" {
		r.Header.Set(HeaderGrokConvID, sid)
	}
}

func applyGrokCLIIdentityHeaders(r *http.Request, config *xaiShared.XaiMultiConfig, authKind string) {
	if r == nil {
		return
	}
	version := DefaultClientVersion
	tokenAuth := DefaultTokenAuth
	var userAgent, surface string

	if config != nil {
		if v := strings.TrimSpace(config.Config.ClientVersion); v != "" {
			version = v
		}
		if v := strings.TrimSpace(config.Config.UserAgent); v != "" {
			userAgent = v
		}
		if v := strings.TrimSpace(config.Config.TokenAuth); v != "" {
			tokenAuth = v
		}
		if v := strings.TrimSpace(config.Config.ClientSurface); v != "" {
			surface = v
		}
	}

	// chat-proxy 上强制覆盖身份头
	kind := strings.ToLower(strings.TrimSpace(authKind))
	if kind == "" || kind == xaiShared.AuthKindOAuth {
		r.Header.Set(HeaderTokenAuth, tokenAuth)
	}
	r.Header.Set(HeaderClientVersion, version)

	if userAgent != "" {
		r.Header.Set("User-Agent", userAgent)
	}
	if surface != "" {
		r.Header.Set(HeaderClientSurface, surface)
	}
}

func applyCustomHeaders(r *http.Request, headers map[string]string) {
	if r == nil || len(headers) == 0 {
		return
	}
	for k, v := range headers {
		k = strings.TrimSpace(k)
		v = strings.TrimSpace(v)
		if k == "" || v == "" {
			continue
		}
		r.Header.Set(k, v)
	}
}

// copyAllowedInboundHeaders 仅透传上游有意义且安全的头。
func copyAllowedInboundHeaders(dst http.Header, src http.Header) {
	if dst == nil || src == nil {
		return
	}
	for _, key := range []string{HeaderGrokConvID, HeaderIdempotencyKey, "x-request-id"} {
		if v := headerGet(src, key); v != "" {
			dst.Set(key, v)
		}
	}
}

func sessionIDFromHeaders(h http.Header) string {
	return headerGet(h, HeaderGrokConvID)
}

// headerGet 兼容 http.Header 字面量未规范化的 key（map 直写 vs Set/Add）。
func headerGet(h http.Header, key string) string {
	if h == nil {
		return ""
	}
	if v := strings.TrimSpace(h.Get(key)); v != "" {
		return v
	}
	for k, vals := range h {
		if strings.EqualFold(k, key) && len(vals) > 0 {
			return strings.TrimSpace(vals[0])
		}
	}
	return ""
}
