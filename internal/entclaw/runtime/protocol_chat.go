package entclawruntime

import "encoding/json"

type chatAdapter struct{}

func (chatAdapter) LoopbackPath() string { return "/v1/chat/completions" }

func (chatAdapter) WithStreamFlag(body []byte, stream bool) ([]byte, error) {
	return mutateJSON(body, func(payload map[string]any) error {
		payload["stream"] = stream
		return nil
	})
}

func (chatAdapter) ParseToolCalls(body []byte) ([]ToolCall, string, error) {
	var payload struct {
		Choices []struct {
			Message struct {
				Content   string `json:"content"`
				ToolCalls []struct {
					ID       string `json:"id"`
					Function struct {
						Name      string `json:"name"`
						Arguments string `json:"arguments"`
					} `json:"function"`
				} `json:"tool_calls"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, "", err
	}
	if len(payload.Choices) == 0 {
		return nil, "", nil
	}

	calls := make([]ToolCall, 0, len(payload.Choices[0].Message.ToolCalls))
	for _, toolCall := range payload.Choices[0].Message.ToolCalls {
		calls = append(calls, ToolCall{
			ID:        toolCall.ID,
			Name:      toolCall.Function.Name,
			Arguments: json.RawMessage(toolCall.Function.Arguments),
		})
	}

	return calls, payload.Choices[0].Message.Content, nil
}

func (chatAdapter) AppendToolResults(body []byte, results []ToolResult) ([]byte, error) {
	return mutateJSON(body, func(payload map[string]any) error {
		raw, _ := payload["messages"].([]any)
		for _, result := range results {
			raw = append(raw, map[string]any{
				"role":         "tool",
				"tool_call_id": result.ID,
				"content":      result.Content,
			})
		}
		payload["messages"] = raw
		return nil
	})
}
