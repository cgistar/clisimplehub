package middleware

import (
	"net/http"
	"runtime"
	"strings"

	"clisimplehub/internal/storage"
)

const (
	defaultAnthropicVersion = "2023-06-01"
	defaultBaseBetas        = "claude-code-20250219,oauth-2025-04-20,interleaved-thinking-2025-05-14,context-management-2025-06-27,prompt-caching-scope-2026-01-05,structured-outputs-2025-12-15,fast-mode-2026-02-01,redact-thinking-2026-02-12,token-efficient-tools-2026-03-28"
	defaultUserAgent        = "claude-cli/2.1.63 (external, cli)"
	defaultPackageVersion   = "0.74.0"
	defaultRuntimeVersion   = "v24.3.0"
	defaultTimeout          = "600"
)

// applyClaudeHeaders 构造 Claude Code 兼容的请求头。
func applyClaudeHeaders(r *http.Request, endpoint *storage.Endpoint, cfg resolvedClaudeMessagesConfig, extraBetas []string, isStream bool) {
	h := r.Header
	authMode := resolveClaudeMessagesAuthMode(endpoint, cfg)
	isOfficialAnthropic := isOfficialAnthropicBaseURL(endpoint)
	hasClaude1MHeader := headerPresent(h, "X-CPA-CLAUDE-1M")
	deleteHeaderCaseInsensitive(h, "X-CPA-CLAUDE-1M")

	// Anthropic-Beta: 合并默认 + 下游透传 + body 提取
	h.Set("Anthropic-Beta", mergeAnthropicBetas(h.Get("Anthropic-Beta"), extraBetas, hasClaude1MHeader))

	// 标准 Anthropic 头
	ensureHeader(h, "Anthropic-Version", defaultAnthropicVersion)
	if authMode == "api_key" {
		ensureHeader(h, "Anthropic-Dangerous-Direct-Browser-Access", "true")
	} else {
		h.Del("Anthropic-Dangerous-Direct-Browser-Access")
	}
	ensureHeader(h, "Content-Type", "application/json")

	// Claude Code 标识头
	ensureHeader(h, "X-App", "cli")
	ensureHeader(h, "X-Stainless-Retry-Count", "0")
	ensureHeader(h, "X-Stainless-Runtime-Version", defaultRuntimeVersion)
	ensureHeader(h, "X-Stainless-Package-Version", defaultPackageVersion)
	ensureHeader(h, "X-Stainless-Runtime", "node")
	ensureHeader(h, "X-Stainless-Lang", "js")
	ensureHeader(h, "X-Stainless-Arch", mapStainlessArch())
	ensureHeader(h, "X-Stainless-Os", mapStainlessOS())
	ensureHeader(h, "X-Stainless-Timeout", defaultTimeout)
	ensureHeader(h, "X-Claude-Code-Session-Id", resolveClaudeMessagesSessionID(endpoint, cfg))
	if isOfficialAnthropic {
		ensureHeader(h, "x-client-request-id", randomUUID())
	}
	h.Set("Connection", "keep-alive")

	// User-Agent 伪装
	if !isClaudeCodeClient(h.Get("User-Agent")) {
		h.Set("User-Agent", defaultUserAgent)
	}

	// 流式 SSE 防压缩
	if isStream {
		h.Set("Accept", "text/event-stream")
		h.Set("Accept-Encoding", "identity")
	} else {
		h.Set("Accept", "application/json")
		h.Set("Accept-Encoding", "gzip, deflate, br, zstd")
	}
}

// appendBetaQueryParam 确保 URL 包含 beta=true。
func appendBetaQueryParam(r *http.Request) {
	q := r.URL.Query()
	q.Set("beta", "true")
	r.URL.RawQuery = q.Encode()
}

// mergeAnthropicBetas 合并默认 betas、下游透传 betas 和从 body 提取的 extraBetas，去重。
func mergeAnthropicBetas(downstreamBeta string, extraBetas []string, hasClaude1MHeader bool) string {
	base := defaultBaseBetas
	if downstreamBeta != "" {
		base = downstreamBeta
	}
	existing := make(map[string]bool)
	for _, b := range strings.Split(base, ",") {
		if s := strings.TrimSpace(b); s != "" {
			existing[s] = true
		}
	}
	if !existing["oauth-2025-04-20"] {
		base += ",oauth-2025-04-20"
		existing["oauth-2025-04-20"] = true
	}
	if !existing["interleaved-thinking-2025-05-14"] {
		base += ",interleaved-thinking-2025-05-14"
		existing["interleaved-thinking-2025-05-14"] = true
	}
	for _, beta := range extraBetas {
		beta = strings.TrimSpace(beta)
		if beta != "" && !existing[beta] {
			base += "," + beta
			existing[beta] = true
		}
	}
	if hasClaude1MHeader && !existing["context-1m-2025-08-07"] {
		base += ",context-1m-2025-08-07"
	}
	return base
}

// ensureHeader 当目标 header 不存在时设置默认值。
func ensureHeader(h http.Header, key, defaultValue string) {
	if strings.TrimSpace(h.Get(key)) != "" {
		return
	}
	h.Set(key, defaultValue)
}

func headerPresent(h http.Header, key string) bool {
	if h == nil {
		return false
	}
	for existingKey := range h {
		if strings.EqualFold(existingKey, key) {
			return true
		}
	}
	return false
}

func deleteHeaderCaseInsensitive(h http.Header, key string) {
	if h == nil {
		return
	}
	for existingKey := range h {
		if strings.EqualFold(existingKey, key) {
			delete(h, existingKey)
		}
	}
}

func isOfficialAnthropicBaseURL(endpoint *storage.Endpoint) bool {
	if endpoint == nil {
		return false
	}
	base := strings.ToLower(strings.TrimSpace(endpoint.APIURL))
	base = strings.TrimPrefix(base, "https://")
	base = strings.TrimPrefix(base, "http://")
	base = strings.TrimRight(base, "/")
	return base == "api.anthropic.com"
}

func mapStainlessArch() string {
	switch runtime.GOARCH {
	case "amd64":
		return "x64"
	case "arm64":
		return "arm64"
	case "386":
		return "x86"
	default:
		return runtime.GOARCH
	}
}

func mapStainlessOS() string {
	switch runtime.GOOS {
	case "darwin":
		return "Mac OS X"
	case "linux":
		return "Linux"
	case "windows":
		return "Windows"
	default:
		return runtime.GOOS
	}
}
