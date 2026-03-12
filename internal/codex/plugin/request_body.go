package codexplugin

import appmiddleware "clisimplehub/internal/middleware"

var errCompactStreamingNotSupported = appmiddleware.ErrCompactStreamingNotSupported

// processRequestBody modifies request body for Codex upstream.
// 非 Codex CLI 请求会在公共中间件和这里复用同一套归一化规则。
func processRequestBody(body []byte, requestPath string, userAgent string) ([]byte, error) {
	processedBody, _, err := appmiddleware.NormalizeCodexResponsesRequest(body, requestPath, userAgent)
	return processedBody, err
}

func normalizeStreamingModeForCodexPath(requestPath string, isStreaming bool) bool {
	if isCompactResponsesPath(requestPath) {
		return false
	}
	return isStreaming
}

func compactStreamingErrorPayload() []byte {
	return appmiddleware.CompactStreamingErrorPayload()
}
