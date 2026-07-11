package backend

import (
	"fmt"
	"strings"
	"time"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// BuildCompactionTriggerStreamChunks 将 compact JSON 响应合成 HTTP SSE 帧序列。
func BuildCompactionTriggerStreamChunks(preparedBody []byte, baseModel string, compactData []byte) [][]byte {
	responseID := CompactionResponseID(compactData)
	now := time.Now().Unix()
	createdAt := gjson.GetBytes(compactData, "created_at").Int()
	if createdAt == 0 {
		createdAt = now
	}
	completedAt := gjson.GetBytes(compactData, "completed_at").Int()
	if completedAt == 0 {
		completedAt = now
	}

	item := CompactionOutputItem(compactData, responseID)
	output := make([]byte, 0, len(item)+2)
	output = append(output, '[')
	output = append(output, item...)
	output = append(output, ']')

	createdResponse := buildCompactionBaseResponse(preparedBody, baseModel, compactData, responseID, createdAt, "in_progress")
	inProgressResponse := buildCompactionBaseResponse(preparedBody, baseModel, compactData, responseID, createdAt, "in_progress")
	completedResponse := buildCompactionBaseResponse(preparedBody, baseModel, compactData, responseID, createdAt, "completed")
	completedResponse, _ = sjson.SetBytes(completedResponse, "completed_at", completedAt)
	completedResponse, _ = sjson.SetRawBytes(completedResponse, "output", output)
	if usage := gjson.GetBytes(compactData, "usage"); usage.Exists() {
		completedResponse, _ = sjson.SetRawBytes(completedResponse, "usage", []byte(usage.Raw))
	}

	createdPayload := []byte(`{"type":"response.created","sequence_number":0}`)
	createdPayload, _ = sjson.SetRawBytes(createdPayload, "response", createdResponse)
	inProgressPayload := []byte(`{"type":"response.in_progress","sequence_number":1}`)
	inProgressPayload, _ = sjson.SetRawBytes(inProgressPayload, "response", inProgressResponse)
	addedPayload := []byte(`{"type":"response.output_item.added","sequence_number":2,"output_index":0}`)
	addedPayload, _ = sjson.SetRawBytes(addedPayload, "item", item)
	keepalivePayload := []byte(`{"type":"keepalive","sequence_number":3}`)
	donePayload := []byte(`{"type":"response.output_item.done","sequence_number":4,"output_index":0}`)
	donePayload, _ = sjson.SetRawBytes(donePayload, "item", item)
	completedPayload := []byte(`{"type":"response.completed","sequence_number":5}`)
	completedPayload, _ = sjson.SetRawBytes(completedPayload, "response", completedResponse)

	return [][]byte{
		BuildSSEFrame("response.created", createdPayload),
		BuildSSEFrame("response.in_progress", inProgressPayload),
		BuildSSEFrame("response.output_item.added", addedPayload),
		BuildSSEFrame("keepalive", keepalivePayload),
		BuildSSEFrame("response.output_item.done", donePayload),
		BuildSSEFrame("response.completed", completedPayload),
	}
}

// BuildCompactionTriggerWSEvents 合成 WS 事件（JSON，非 SSE 帧）。
func BuildCompactionTriggerWSEvents(preparedBody []byte, baseModel string, compactData []byte) [][]byte {
	frames := BuildCompactionTriggerStreamChunks(preparedBody, baseModel, compactData)
	out := make([][]byte, 0, len(frames))
	for _, frame := range frames {
		// 提取 data: 行
		for _, line := range strings.Split(string(frame), "\n") {
			if strings.HasPrefix(line, "data: ") {
				out = append(out, []byte(strings.TrimSpace(line[6:])))
				break
			}
		}
	}
	return out
}

func buildCompactionBaseResponse(preparedBody []byte, baseModel string, compactData []byte, responseID string, createdAt int64, status string) []byte {
	response := []byte(`{"id":"","object":"response","created_at":0,"status":"","background":false,"error":null,"incomplete_details":null,"output":[]}`)
	response, _ = sjson.SetBytes(response, "id", responseID)
	response, _ = sjson.SetBytes(response, "created_at", createdAt)
	response, _ = sjson.SetBytes(response, "status", status)
	if model := gjson.GetBytes(compactData, "model").String(); model != "" {
		response, _ = sjson.SetBytes(response, "model", model)
	} else if baseModel != "" {
		response, _ = sjson.SetBytes(response, "model", baseModel)
	}
	for _, field := range []string{
		"instructions", "max_output_tokens", "max_tool_calls", "parallel_tool_calls",
		"previous_response_id", "prompt_cache_key", "reasoning", "text", "tool_choice",
		"tools", "top_logprobs", "top_p", "truncation", "user", "metadata",
	} {
		if value := gjson.GetBytes(preparedBody, field); value.Exists() {
			response, _ = sjson.SetRawBytes(response, field, []byte(value.Raw))
		}
	}
	return response
}

// BuildSSEFrame 构造 SSE event 帧。
func BuildSSEFrame(eventName string, data []byte) []byte {
	out := make([]byte, 0, len(eventName)+len(data)+16)
	out = append(out, "event: "...)
	out = append(out, eventName...)
	out = append(out, '\n')
	out = append(out, "data: "...)
	out = append(out, data...)
	out = append(out, '\n', '\n')
	return out
}

// PrepareCompactBody 为 /responses/compact 准备 body。
func PrepareCompactBody(body []byte, model string) ([]byte, error) {
	prepared, err := PrepareResponsesBody(body, PrepareOptions{
		Stream:    false,
		Model:     model,
		IsCompact: true,
	})
	if err != nil {
		return nil, err
	}
	if prepared == nil {
		return body, nil
	}
	return prepared.Body, nil
}

// SyntheticCompactionStream 将 compact 结果包装为可被 writeUpstreamResult 透传的伪流。
func SyntheticCompactionStream(preparedBody []byte, baseModel string, compactData []byte) []byte {
	chunks := BuildCompactionTriggerStreamChunks(preparedBody, baseModel, compactData)
	total := 0
	for _, c := range chunks {
		total += len(c)
	}
	out := make([]byte, 0, total)
	for _, c := range chunks {
		out = append(out, c...)
	}
	return out
}

// FormatCompactionError 统一错误包装。
func FormatCompactionError(status int, body []byte) error {
	return fmt.Errorf("compact upstream %d: %s", status, strings.TrimSpace(string(body)))
}
