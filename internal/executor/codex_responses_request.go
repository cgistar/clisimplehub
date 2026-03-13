package executor

import (
	"net/http"
	"net/url"
	"strings"

	"github.com/google/uuid"
)

const (
	defaultCodexVersionHeader   = "0.101.0"
	defaultCodexUserAgentHeader = "codex_cli_rs/0.101.0 (Mac OS 26.0.1; arm64) Apple_Terminal/464"
)

func normalizeTransformerRawQuery(targetInterfaceType, targetPath, rawQuery string) string {
	if !shouldApplyCodexResponsesTransform(targetInterfaceType, targetPath) {
		return rawQuery
	}
	return stripQueryParam(rawQuery, "beta")
}

func applyTransformerTargetHeaders(req *http.Request, src http.Header, targetInterfaceType, targetPath string, isStreaming bool) {
	if !shouldApplyCodexResponsesTransform(targetInterfaceType, targetPath) {
		copyRequestHeaders(req, src)
		return
	}
	applyCodexResponsesHeaders(req, src, isStreaming)
}

func shouldApplyCodexResponsesTransform(targetInterfaceType, targetPath string) bool {
	if !strings.EqualFold(strings.TrimSpace(targetInterfaceType), "codex") {
		return false
	}

	path := strings.ToLower(strings.TrimSpace(targetPath))
	return strings.HasSuffix(path, "/responses") || strings.HasSuffix(path, "/responses/compact")
}

func stripQueryParam(rawQuery, key string) string {
	rawQuery = strings.TrimSpace(rawQuery)
	key = strings.TrimSpace(key)
	if rawQuery == "" || key == "" {
		return rawQuery
	}

	values, err := url.ParseQuery(rawQuery)
	if err != nil {
		return rawQuery
	}
	values.Del(key)
	return values.Encode()
}

func applyCodexResponsesHeaders(req *http.Request, src http.Header, isStreaming bool) {
	req.Header = make(http.Header)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Version", headerValueOrDefault(src, "Version", defaultCodexVersionHeader))

	sessionID := strings.TrimSpace(src.Get("Session_id"))
	if sessionID == "" {
		sessionID = uuid.NewString()
	}
	req.Header.Set("Session_id", sessionID)

	if openAIBeta := strings.TrimSpace(src.Get("Openai-Beta")); openAIBeta != "" {
		req.Header.Set("Openai-Beta", openAIBeta)
	}

	req.Header.Set("User-Agent", defaultCodexUserAgentHeader)
	req.Header.Set("Connection", "close")
	if isStreaming {
		req.Header.Set("Accept", "text/event-stream")
		return
	}
	req.Header.Set("Accept", "application/json")
}

func headerValueOrDefault(h http.Header, key, fallback string) string {
	if h == nil {
		return fallback
	}
	if value := strings.TrimSpace(h.Get(key)); value != "" {
		return value
	}
	return fallback
}
