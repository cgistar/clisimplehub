package entclawruntime

import (
	"encoding/json"
	"strings"
)

type responsesAdapter struct{}

func (responsesAdapter) LoopbackPath() string { return "/v1/responses" }

func (responsesAdapter) WithStreamFlag(body []byte, stream bool) ([]byte, error) {
	return mutateJSON(body, func(payload map[string]any) error {
		payload["stream"] = stream
		return nil
	})
}

func (responsesAdapter) ParseToolCalls(body []byte) ([]ToolCall, string, error) {
	var payload struct {
		Output []struct {
			Type      string          `json:"type"`
			CallID    string          `json:"call_id"`
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
			Content   []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		} `json:"output"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, "", err
	}

	calls := make([]ToolCall, 0, len(payload.Output))
	var finalText strings.Builder
	for _, item := range payload.Output {
		if item.Type == "function_call" {
			calls = append(calls, ToolCall{
				ID:        item.CallID,
				Name:      item.Name,
				Arguments: normalizeResponsesArguments(item.Arguments),
			})
		}
		if item.Type == "message" {
			for _, block := range item.Content {
				finalText.WriteString(block.Text)
			}
		}
	}

	return calls, finalText.String(), nil
}

func (responsesAdapter) AppendToolResults(body []byte, rounds []ToolRound) ([]byte, error) {
	return mutateJSON(body, func(payload map[string]any) error {
		raw, err := normalizeResponsesInput(payload["input"])
		if err != nil {
			return err
		}
		for _, round := range rounds {
			raw = append(raw, map[string]any{
				"type":      "function_call",
				"call_id":   round.Call.ID,
				"name":      round.Call.Name,
				"arguments": stringifyToolArguments(round.Call.Arguments),
			})
		}
		for _, round := range rounds {
			raw = append(raw, map[string]any{
				"type":    "function_call_output",
				"call_id": round.Call.ID,
				"output":  stringifyToolResultContent(round.Result.Content),
			})
		}
		payload["input"] = raw
		return nil
	})
}
