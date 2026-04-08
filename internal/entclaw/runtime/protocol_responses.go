package entclawruntime

import "encoding/json"

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
	finalText := ""
	for _, item := range payload.Output {
		if item.Type == "function_call" {
			calls = append(calls, ToolCall{
				ID:        item.CallID,
				Name:      item.Name,
				Arguments: item.Arguments,
			})
		}
		if item.Type == "message" && finalText == "" && len(item.Content) > 0 {
			finalText = item.Content[0].Text
		}
	}

	return calls, finalText, nil
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
				"output":  round.Result.Content,
			})
		}
		payload["input"] = raw
		return nil
	})
}
