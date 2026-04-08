package entclawruntime

import (
	"encoding/json"
	"strings"
)

type messagesAdapter struct{}

func (messagesAdapter) LoopbackPath() string { return "/v1/messages" }

func (messagesAdapter) WithStreamFlag(body []byte, stream bool) ([]byte, error) {
	return mutateJSON(body, func(payload map[string]any) error {
		payload["stream"] = stream
		return nil
	})
}

func (messagesAdapter) ParseToolCalls(body []byte) ([]ToolCall, string, error) {
	var payload struct {
		Content []struct {
			Type  string          `json:"type"`
			ID    string          `json:"id"`
			Name  string          `json:"name"`
			Input json.RawMessage `json:"input"`
			Text  string          `json:"text"`
		} `json:"content"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, "", err
	}

	calls := make([]ToolCall, 0, len(payload.Content))
	var finalText strings.Builder
	for _, item := range payload.Content {
		if item.Type == "tool_use" {
			calls = append(calls, ToolCall{
				ID:        item.ID,
				Name:      item.Name,
				Arguments: item.Input,
			})
		}
		if item.Type == "text" {
			finalText.WriteString(item.Text)
		}
	}

	return calls, finalText.String(), nil
}

func (messagesAdapter) AppendToolResults(body []byte, rounds []ToolRound) ([]byte, error) {
	return mutateJSON(body, func(payload map[string]any) error {
		raw, _ := payload["messages"].([]any)
		if len(rounds) > 0 {
			assistantContent := make([]any, 0, len(rounds))
			userContent := make([]any, 0, len(rounds))
			for _, round := range rounds {
				assistantContent = append(assistantContent, map[string]any{
					"type":  "tool_use",
					"id":    round.Call.ID,
					"name":  round.Call.Name,
					"input": rawJSONObjectOrEmpty(round.Call.Arguments),
				})

				item := map[string]any{
					"type":        "tool_result",
					"tool_use_id": round.Call.ID,
					"content":     stringifyToolResultContent(round.Result.Content),
				}
				if round.Result.IsError {
					item["is_error"] = true
				}
				userContent = append(userContent, item)
			}
			raw = append(raw, map[string]any{
				"role":    "assistant",
				"content": assistantContent,
			})
			raw = append(raw, map[string]any{
				"role":    "user",
				"content": userContent,
			})
		}
		payload["messages"] = raw
		return nil
	})
}
