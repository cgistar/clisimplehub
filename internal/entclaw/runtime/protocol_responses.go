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

func (responsesAdapter) ParseToolCalls(body []byte) (AssistantTurn, error) {
	var payload struct {
		ID     string `json:"id"`
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
	if err := json.Unmarshal(body, &payload); err == nil {
		return buildResponsesAssistantTurn(payload.ID, payload.Output), nil
	}

	return parseResponsesSSEToolCalls(body)
}

func buildResponsesAssistantTurn(responseID string, output []struct {
	Type      string          `json:"type"`
	CallID    string          `json:"call_id"`
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
	Content   []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
}) AssistantTurn {
	turn := AssistantTurn{
		ResponseID: strings.TrimSpace(responseID),
		Parts:      make([]AssistantTurnPart, 0, len(output)),
	}
	for _, item := range output {
		if item.Type == "function_call" {
			turn.Parts = append(turn.Parts, assistantToolCallPart(ToolCall{
				ID:        item.CallID,
				Name:      item.Name,
				Arguments: normalizeResponsesArguments(item.Arguments),
			}))
		}
		if item.Type == "message" {
			var text strings.Builder
			for _, block := range item.Content {
				text.WriteString(block.Text)
			}
			if text.Len() > 0 {
				turn.Parts = append(turn.Parts, assistantTextPart(text.String()))
			}
		}
	}
	return turn
}

func parseResponsesSSEToolCalls(body []byte) (AssistantTurn, error) {
	normalized := strings.ReplaceAll(string(body), "\r\n", "\n")
	chunks := strings.Split(normalized, "\n\n")
	turn := AssistantTurn{Parts: make([]AssistantTurnPart, 0, len(chunks))}

	for _, chunk := range chunks {
		chunk = strings.TrimSpace(chunk)
		if chunk == "" {
			continue
		}

		var dataLines []string
		for _, line := range strings.Split(chunk, "\n") {
			if strings.HasPrefix(line, "data:") {
				dataLines = append(dataLines, strings.TrimSpace(strings.TrimPrefix(line, "data:")))
			}
		}
		if len(dataLines) == 0 {
			continue
		}

		var event struct {
			Type string `json:"type"`
			Response struct {
				ID string `json:"id"`
			} `json:"response"`
			Item struct {
				Type      string          `json:"type"`
				Status    string          `json:"status"`
				CallID    string          `json:"call_id"`
				Name      string          `json:"name"`
				Arguments json.RawMessage `json:"arguments"`
				Content   []struct {
					Type string `json:"type"`
					Text string `json:"text"`
				} `json:"content"`
			} `json:"item"`
		}
		if err := json.Unmarshal([]byte(strings.Join(dataLines, "\n")), &event); err != nil {
			continue
		}
		if turn.ResponseID == "" {
			switch event.Type {
			case "response.created", "response.completed", "response.failed":
				turn.ResponseID = strings.TrimSpace(event.Response.ID)
			}
		}
		if event.Type != "response.output_item.done" {
			continue
		}

		switch event.Item.Type {
		case "function_call":
			turn.Parts = append(turn.Parts, assistantToolCallPart(ToolCall{
				ID:        event.Item.CallID,
				Name:      event.Item.Name,
				Arguments: normalizeResponsesArguments(event.Item.Arguments),
			}))
		case "message":
			var text strings.Builder
			for _, block := range event.Item.Content {
				text.WriteString(block.Text)
			}
			if text.Len() > 0 {
				turn.Parts = append(turn.Parts, assistantTextPart(text.String()))
			}
		}
	}

	return turn, nil
}

func (responsesAdapter) AppendToolResults(body []byte, turn AssistantTurn, rounds []ToolRound) ([]byte, error) {
	return mutateJSON(body, func(payload map[string]any) error {
		raw, err := normalizeResponsesInput(payload["input"])
		if err != nil {
			return err
		}
		for _, part := range assistantTurnPartsForLoopback(turn, rounds) {
			if part.Type != assistantTurnPartToolCall {
				continue
			}
			raw = append(raw, map[string]any{
				"type":      "function_call",
				"call_id":   part.Call.ID,
				"name":      part.Call.Name,
				"arguments": stringifyToolArguments(part.Call.Arguments),
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
