package middleware

import (
	"log"
	"net/http"
	"strings"
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

		body, headers, rawQuery := NormalizeClaudeMessagesRequest(body, r.Header, r.URL.RawQuery)
		restoreRequestBody(r, body)
		r.Header = headers
		r.URL.RawQuery = rawQuery

		next.ServeHTTP(w, r)
	})
}
