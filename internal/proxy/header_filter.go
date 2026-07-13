package proxy

import (
	"net/http"
	"strings"
)

// gatewayHeaderPrefixes 已知 AI 网关注入的响应头前缀，需剥离避免客户端探测。
var gatewayHeaderPrefixes = []string{
	"x-litellm-",
	"helicone-",
	"x-portkey-",
	"cf-aig-",
	"x-kong-",
	"x-bt-",
}

// hopByHopHeaders RFC 7230 hop-by-hop + 安全敏感 + 由本侧管理的头。
var hopByHopHeaders = map[string]struct{}{
	"Connection":          {},
	"Keep-Alive":          {},
	"Proxy-Authenticate":  {},
	"Proxy-Authorization": {},
	"Te":                  {},
	"Trailer":             {},
	"Transfer-Encoding":   {},
	"Upgrade":             {},
	"Set-Cookie":          {},
	"Content-Length":      {},
	"Content-Encoding":    {},
}

// FilterUpstreamResponseHeaders 过滤上游响应头
func FilterUpstreamResponseHeaders(src http.Header) http.Header {
	if src == nil {
		return nil
	}
	connectionScoped := connectionScopedHeaders(src)
	dst := make(http.Header)
	for key, values := range src {
		canonicalKey := http.CanonicalHeaderKey(key)
		if _, blocked := hopByHopHeaders[canonicalKey]; blocked {
			continue
		}
		if _, scoped := connectionScoped[canonicalKey]; scoped {
			continue
		}
		lowerKey := strings.ToLower(key)
		gatewayMatch := false
		for _, prefix := range gatewayHeaderPrefixes {
			if strings.HasPrefix(lowerKey, prefix) {
				gatewayMatch = true
				break
			}
		}
		if gatewayMatch {
			continue
		}
		dst[key] = append([]string(nil), values...)
	}
	if len(dst) == 0 {
		return nil
	}
	return dst
}

// WriteUpstreamResponseHeaders 写入过滤后的上游响应头，不覆盖 dst 已有键。
func WriteUpstreamResponseHeaders(dst, src http.Header) {
	if dst == nil || src == nil {
		return
	}
	filtered := FilterUpstreamResponseHeaders(src)
	for key, values := range filtered {
		if dst.Get(key) != "" {
			continue
		}
		for _, v := range values {
			dst.Add(key, v)
		}
	}
}

func connectionScopedHeaders(src http.Header) map[string]struct{} {
	scoped := make(map[string]struct{})
	for _, rawValue := range src.Values("Connection") {
		for _, token := range strings.Split(rawValue, ",") {
			headerName := strings.TrimSpace(token)
			if headerName == "" {
				continue
			}
			scoped[http.CanonicalHeaderKey(headerName)] = struct{}{}
		}
	}
	return scoped
}
