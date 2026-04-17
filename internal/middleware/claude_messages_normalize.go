package middleware

import (
	"net/http"
	"net/url"
	"strings"

	"clisimplehub/internal/storage"

	"github.com/tidwall/gjson"
)

// NormalizeClaudeMessagesRequest applies the same body/header/query normalization
// used by the /v1/messages gateway middleware to an already-transformed Claude
// Messages request.
func NormalizeClaudeMessagesRequest(body []byte, headers http.Header, rawQuery string) ([]byte, http.Header, string) {
	return NormalizeClaudeMessagesRequestForEndpoint(body, headers, rawQuery, nil)
}

// NormalizeClaudeMessagesRequestForEndpoint 针对目标端点执行 /v1/messages 规范化。
func NormalizeClaudeMessagesRequestForEndpoint(body []byte, headers http.Header, rawQuery string, endpoint *storage.Endpoint) ([]byte, http.Header, string) {
	clonedHeaders := cloneHTTPHeader(headers)
	original := append([]byte(nil), body...)
	isStream := detectStreamFromInputs(clonedHeaders, rawQuery, body)
	cfg := resolveClaudeMessagesConfig(endpoint)

	body, modelSuffix := normalizeModel(body)
	body = applyCloaking(body, clonedHeaders.Get("User-Agent"), endpoint, cfg)
	body = applyClaudeThinkingConfig(body, modelSuffix)
	body = fixClaudeMessages(body)
	body = disableThinkingIfToolChoiceForced(body)
	body = normalizeClaudeTemperatureForThinking(body)
	body = ensureCacheControl(body)
	body = enforceCacheControlLimit(body, 4)
	body = normalizeCacheControlTTL(body)
	extraBetas, body := extractAndRemoveBetas(body)
	if modelSuffix.OneMillionContext {
		extraBetas = append(extraBetas, "context-1m-2025-08-07")
	}
	if shouldRemapClaudeMessagesOAuthTools(endpoint, cfg) {
		body, _ = remapClaudeMessagesOAuthToolNames(body)
	}
	if shouldSignClaudeMessagesBody(endpoint, cfg) {
		body = signClaudeMessagesBody(body)
	}

	if body == nil {
		body = original
	}

	req := &http.Request{
		Header: clonedHeaders,
		URL:    &url.URL{RawQuery: rawQuery},
	}
	applyClaudeHeaders(req, endpoint, cfg, extraBetas, isStream)
	appendBetaQueryParam(req)

	return body, req.Header.Clone(), req.URL.RawQuery
}

func detectStreamFromInputs(headers http.Header, rawQuery string, body []byte) bool {
	if strings.Contains(strings.ToLower(headers.Get("Accept")), "text/event-stream") {
		return true
	}
	if v := strings.TrimSpace(queryValue(rawQuery, "stream")); strings.EqualFold(v, "true") || v == "1" {
		return true
	}
	return gjsonBool(body, "stream")
}

func cloneHTTPHeader(src http.Header) http.Header {
	if src == nil {
		return http.Header{}
	}
	return src.Clone()
}

func queryValue(rawQuery, key string) string {
	values, err := url.ParseQuery(rawQuery)
	if err != nil {
		return ""
	}
	return values.Get(key)
}

func gjsonBool(body []byte, path string) bool {
	return gjson.GetBytes(body, path).Bool()
}
