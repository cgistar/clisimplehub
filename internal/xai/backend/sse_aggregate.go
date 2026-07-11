package backend

import (
	"bytes"
	"strings"

	"github.com/tidwall/gjson"
)

// AggregateResponsesSSE 将上游 Responses SSE 收成非流 JSON。
// 始终 stream 上游，再取 response.completed。
func AggregateResponsesSSE(data []byte) []byte {
	if len(data) == 0 {
		return data
	}
	// 已是 JSON 对象则直接返回
	if gjson.ValidBytes(data) && data[0] == '{' {
		return NormalizeNonStreamReasoning(data)
	}

	var completed []byte
	var fallback []byte
	for _, line := range bytes.Split(data, []byte("\n")) {
		line = bytes.TrimSpace(line)
		if !bytes.HasPrefix(line, dataTag) {
			continue
		}
		payload := bytes.TrimSpace(line[len(dataTag):])
		if len(payload) == 0 || bytes.Equal(payload, []byte("[DONE]")) {
			continue
		}
		payload = NormalizeReasoningSummaryData(payload)
		typ := gjson.GetBytes(payload, "type").String()
		switch typ {
		case "response.completed":
			// 优先返回 response 对象；若不存在则整段 event
			if resp := gjson.GetBytes(payload, "response"); resp.Exists() && resp.Type == gjson.JSON {
				completed = []byte(resp.Raw)
			} else {
				completed = payload
			}
		case "response.failed", "error":
			fallback = payload
		}
		if typ != "" {
			fallback = payload
		}
	}
	if len(completed) > 0 {
		return completed
	}
	if len(fallback) > 0 {
		return fallback
	}
	// 无法解析时原样返回（便于排障）
	return data
}

// LooksLikeSSE 粗判是否为 SSE 文本。
func LooksLikeSSE(data []byte) bool {
	s := string(data)
	return strings.Contains(s, "data:") && (strings.Contains(s, "response.") || strings.Contains(s, "event:"))
}
