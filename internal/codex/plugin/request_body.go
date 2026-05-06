package codexplugin

import (
	"encoding/json"

	appmiddleware "clisimplehub/internal/middleware"
)

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

// adaptConvertedResponsesBody applies non-CLI adaptations (instructions injection,
// field cleanup) to a Responses API body that was converted from Chat Completions.
func adaptConvertedResponsesBody(body []byte, userAgent string) []byte {
	if appmiddleware.IsCodexCLI(userAgent) {
		return body
	}
	var reqBody map[string]any
	if err := json.Unmarshal(body, &reqBody); err != nil {
		return body
	}
	appmiddleware.RemoveFieldsForCodexUpstream(reqBody)
	appmiddleware.AdaptResponsesPayloadForNonCLI(reqBody)
	adapted, err := json.Marshal(reqBody)
	if err != nil {
		return body
	}
	return adapted
}
