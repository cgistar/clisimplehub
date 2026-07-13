package executor

import (
	"net/http"
	"net/url"
	"strings"

	codexShared "clisimplehub/internal/codex/shared"

	"github.com/google/uuid"
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
	if version := strings.TrimSpace(src.Get("Version")); version != "" {
		req.Header.Set("Version", version)
	}

	// UA：客户端透传 → 默认
	userAgent := ""
	if src != nil {
		userAgent = strings.TrimSpace(src.Get("User-Agent"))
	}
	if userAgent == "" {
		userAgent = codexShared.DefaultCodexUserAgent
	}
	req.Header.Set("User-Agent", userAgent)

	// Session_id：仅 Mac UA Ensure。
	if strings.Contains(userAgent, "Mac OS") {
		sessionID := strings.TrimSpace(src.Get("Session_id"))
		if sessionID == "" {
			sessionID = uuid.NewString()
		}
		req.Header.Set("Session_id", sessionID)
	} else if sessionID := strings.TrimSpace(src.Get("Session_id")); sessionID != "" {
		req.Header.Set("Session_id", sessionID)
	}

	if openAIBeta := strings.TrimSpace(src.Get("Openai-Beta")); openAIBeta != "" {
		req.Header.Set("Openai-Beta", openAIBeta)
	}
	copyCodexHeaderIfPresent(req.Header, src, "X-Codex-Beta-Features")
	copyCodexHeaderIfPresent(req.Header, src, "X-Codex-Turn-Metadata")
	copyCodexHeaderIfPresent(req.Header, src, "X-Client-Request-Id")
	copyCodexHeaderIfPresent(req.Header, src, "Originator")

	req.Header.Set("Connection", "Keep-Alive")
	if isStreaming {
		req.Header.Set("Accept", "text/event-stream")
		return
	}
	req.Header.Set("Accept", "application/json")
}

func copyCodexHeaderIfPresent(dst, src http.Header, key string) {
	if dst == nil || src == nil {
		return
	}
	if value := strings.TrimSpace(src.Get(key)); value != "" {
		dst.Set(key, value)
	}
}
