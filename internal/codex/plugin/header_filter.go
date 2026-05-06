package codexplugin

import (
	"net/http"
	"slices"
	"strings"
)

var cdnHeaders = []string{
	"x-real-ip",
	"x-forwarded-for",
	"x-forwarded-proto",
	"x-forwarded-host",
	"x-forwarded-port",
	"x-accel-buffering",
	"cf-ray",
	"cf-connecting-ip",
	"cf-ipcountry",
	"cf-visitor",
	"cf-request-id",
	"cdn-loop",
	"true-client-ip",
}

// filterClientHeaders 使用白名单过滤入站请求头，只保留 Codex 客户端需要的稳定字段。
func filterClientHeaders(clientHeaders http.Header) http.Header {
	// 只允许客户端显式传入的 Codex 协议头穿透到上游。
	allowedKeys := []string{
		"version",
		"openai-beta",
		"session_id",
		"x-codex-beta-features",
		"x-codex-turn-metadata",
		"x-client-request-id",
		"originator",
	}

	filtered := make(http.Header)
	if clientHeaders == nil {
		return filtered
	}

	for _, key := range allowedKeys {
		if val := strings.TrimSpace(clientHeaders.Get(key)); val != "" {
			filtered.Set(key, val)
		}
	}

	return filtered
}

// shouldSkipHeader checks if a header should be skipped (blacklist approach).
// Used for additional filtering beyond the whitelist.
func shouldSkipHeader(key string) bool {
	lowerKey := strings.ToLower(key)

	// Skip sensitive headers
	skipList := []string{
		"host",
		"content-length",
		"authorization",
		"x-api-key",
		"x-cr-api-key",
		"connection",
		"upgrade",
		"sec-websocket-key",
		"sec-websocket-version",
		"sec-websocket-extensions",
	}

	// Skip CDN headers
	skipList = append(skipList, cdnHeaders...)

	return slices.Contains(skipList, lowerKey)
}
