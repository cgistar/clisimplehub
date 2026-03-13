package middleware

import (
	"log"
	"net/http"
	"strings"

	"github.com/tidwall/gjson"
)

// IsClaudeMessagesPath 判断请求路径是否为 POST /v1/messages（精确匹配，排除子路径如 count_tokens）。
func IsClaudeMessagesPath(path string) bool {
	p := strings.ToLower(strings.TrimRight(strings.TrimSpace(path), "/"))
	return strings.HasSuffix(p, "/v1/messages")
}

// ClaudeMessagesAdaptMiddleware 在网关入口统一规范化 /v1/messages 请求。
// 处理链: model suffix → cloaking → thinking constraint → cache control → betas → headers
func ClaudeMessagesAdaptMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || !IsClaudeMessagesPath(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}

		body, err := readRequestBody(r)
		if err != nil {
			log.Printf("[claude-messages-middleware] read body error: %v", err)
			restoreRequestBody(r, nil) // 恢复空 body，避免下游 EOF
			next.ServeHTTP(w, r)
			return
		}
		if len(body) == 0 {
			next.ServeHTTP(w, r)
			return
		}

		original := body // 保留原始 body 用于 fallback
		isStream := detectStream(r, body)
		userAgent := r.Header.Get("User-Agent")

		// Body 改写链
		body, _ = normalizeModel(body)
		body = applyCloaking(body, userAgent)
		body = disableThinkingIfToolChoiceForced(body)
		body = ensureCacheControl(body)
		body = enforceCacheControlLimit(body, 4)
		body = normalizeCacheControlTTL(body)
		extraBetas, body := extractAndRemoveBetas(body)

		// 防护: 如果处理链导致 body 变 nil，回退到原始 body
		if body == nil {
			body = original
		}

		restoreRequestBody(r, body)

		// Header 改写
		applyClaudeHeaders(r, extraBetas, isStream)
		appendBetaQueryParam(r)

		next.ServeHTTP(w, r)
	})
}

// detectStream 检测请求是否为流式。
func detectStream(r *http.Request, body []byte) bool {
	if strings.Contains(strings.ToLower(r.Header.Get("Accept")), "text/event-stream") {
		return true
	}
	if v := strings.TrimSpace(r.URL.Query().Get("stream")); strings.EqualFold(v, "true") || v == "1" {
		return true
	}
	return gjson.GetBytes(body, "stream").Bool()
}
