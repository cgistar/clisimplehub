package middleware

import (
	"net/http"
	"runtime"
	"strings"
)

const (
	defaultAnthropicVersion = "2023-06-01"
	defaultBaseBetas        = "claude-code-20250219,oauth-2025-04-20,interleaved-thinking-2025-05-14,context-management-2025-06-27,prompt-caching-scope-2026-01-05"
	defaultUserAgent        = "claude-cli/2.1.63 (external, cli)"
	defaultPackageVersion   = "0.74.0"
	defaultRuntimeVersion   = "v24.3.0"
	defaultTimeout          = "600"
)

// applyClaudeHeaders 构造 Claude Code 兼容的请求头。
func applyClaudeHeaders(r *http.Request, extraBetas []string, isStream bool) {
	h := r.Header

	// Anthropic-Beta: 合并默认 + 下游透传 + body 提取
	h.Set("Anthropic-Beta", mergeAnthropicBetas(h.Get("Anthropic-Beta"), extraBetas))

	// 标准 Anthropic 头
	ensureHeader(h, "Anthropic-Version", defaultAnthropicVersion)
	ensureHeader(h, "Anthropic-Dangerous-Direct-Browser-Access", "true")
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

	// User-Agent 伪装
	if !isClaudeCodeClient(h.Get("User-Agent")) {
		h.Set("User-Agent", defaultUserAgent)
	}

	// 流式 SSE 防压缩
	if isStream {
		h.Set("Accept", "text/event-stream")
		h.Set("Accept-Encoding", "identity")
	}
}

// appendBetaQueryParam 确保 URL 包含 beta=true。
func appendBetaQueryParam(r *http.Request) {
	q := r.URL.Query()
	q.Set("beta", "true")
	r.URL.RawQuery = q.Encode()
}

// mergeAnthropicBetas 合并默认 betas、下游透传 betas 和从 body 提取的 extraBetas，去重。
func mergeAnthropicBetas(downstreamBeta string, extraBetas []string) string {
	var base string
	if downstreamBeta != "" {
		base = downstreamBeta
		// 确保包含 oauth beta
		if !strings.Contains(base, "oauth-") {
			base += ",oauth-2025-04-20"
		}
	} else {
		base = defaultBaseBetas
	}

	if len(extraBetas) == 0 {
		return base
	}

	existing := make(map[string]bool)
	for _, b := range strings.Split(base, ",") {
		if s := strings.TrimSpace(b); s != "" {
			existing[s] = true
		}
	}
	for _, beta := range extraBetas {
		beta = strings.TrimSpace(beta)
		if beta != "" && !existing[beta] {
			base += "," + beta
			existing[beta] = true
		}
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
