package backend

import (
	"bytes"
	"net/http"
	"strings"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

const CodexEmptyIncompleteStreamMessage = "stream error: upstream terminated with incomplete empty response (0 tokens)"

func NewEmptyIncompleteStreamError() StatusError {
	body := []byte(`{"error":{}}`)
	body, _ = sjson.SetBytes(body, "error.message", CodexEmptyIncompleteStreamMessage)
	body, _ = sjson.SetBytes(body, "error.type", "server_error")
	body, _ = sjson.SetBytes(body, "error.code", "empty_incomplete_response")
	return StatusError{Code: http.StatusBadGateway, Body: body, retryable: true}
}

func HasMeaningfulCodexOutputDelta(eventData []byte) bool {
	switch gjson.GetBytes(eventData, "type").String() {
	case "response.output_text.delta", "response.reasoning_text.delta", "response.reasoning_summary_text.delta", "response.function_call_arguments.delta":
		delta := gjson.GetBytes(eventData, "delta")
		return delta.Exists() && len(strings.TrimSpace(delta.String())) > 0
	}
	return false
}

func IsCodexTerminalEmptyIncomplete(eventData []byte, outputItemsCount int, sawOutputDelta bool) bool {
	if gjson.GetBytes(eventData, "type").String() != "response.incomplete" {
		return false
	}
	if sawOutputDelta || outputItemsCount > 0 {
		return false
	}
	output := gjson.GetBytes(eventData, "response.output")
	if output.Exists() && output.IsArray() && len(output.Array()) > 0 {
		return false
	}
	outputTokens := gjson.GetBytes(eventData, "response.usage.output_tokens")
	if !outputTokens.Exists() || outputTokens.Type != gjson.Number {
		return false
	}
	if outputTokens.Num != 0 || strings.TrimSpace(outputTokens.Raw) != "0" {
		return false
	}
	return true
}

func detectEmptyIncompleteInSSE(data []byte) bool {
	if len(data) == 0 {
		return false
	}
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) > 0 && trimmed[0] == '{' {
		return IsCodexTerminalEmptyIncomplete(trimmed, 0, false)
	}
	outputItems := 0
	sawDelta := false
	for _, line := range bytes.Split(data, []byte("\n")) {
		line = bytes.TrimSpace(line)
		if !bytes.HasPrefix(line, []byte("data:")) {
			continue
		}
		payload := bytes.TrimSpace(line[len("data:"):])
		if len(payload) == 0 || payload[0] != '{' {
			continue
		}
		if HasMeaningfulCodexOutputDelta(payload) {
			sawDelta = true
		}
		if gjson.GetBytes(payload, "type").String() == "response.output_item.done" {
			item := gjson.GetBytes(payload, "item")
			if item.Exists() && item.IsObject() && item.Get("type").String() != "" {
				outputItems++
			}
		}
		if IsCodexTerminalEmptyIncomplete(payload, outputItems, sawDelta) {
			return true
		}
	}
	return false
}
