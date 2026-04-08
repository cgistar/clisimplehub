package entclawruntime

import "encoding/json"

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
	finalText := ""
	for _, item := range payload.Content {
		if item.Type == "tool_use" {
			calls = append(calls, ToolCall{
				ID:        item.ID,
				Name:      item.Name,
				Arguments: item.Input,
			})
		}
		if item.Type == "text" && finalText == "" {
			finalText = item.Text
		}
	}

	return calls, finalText, nil
}

func (messagesAdapter) AppendToolResults(body []byte, results []ToolResult) ([]byte, error) {
	return mutateJSON(body, func(payload map[string]any) error {
		raw, _ := payload["messages"].([]any)
		raw = append(raw, map[string]any{
			"role":    "user",
			"content": results,
		})
		payload["messages"] = raw
		return nil
	})
}
