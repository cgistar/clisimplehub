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
		return AssistantTurn{}, err
	}

	turn := AssistantTurn{
		Parts: make([]AssistantTurnPart, 0, len(payload.Output)),
	}
	for _, item := range payload.Output {
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

	return turn, nil
}

func (responsesAdapter) AppendToolResults(body []byte, turn AssistantTurn, rounds []ToolRound) ([]byte, error) {
	return mutateJSON(body, func(payload map[string]any) error {
		raw, err := normalizeResponsesInput(payload["input"])
		if err != nil {
			return err
		}
		for _, part := range assistantTurnPartsForLoopback(turn, rounds) {
			switch part.Type {
			case assistantTurnPartText:
				raw = append(raw, map[string]any{
					"type":    "message",
					"role":    "assistant",
					"content": part.Text,
				})
			case assistantTurnPartToolCall:
				raw = append(raw, map[string]any{
					"type":      "function_call",
					"call_id":   part.Call.ID,
					"name":      part.Call.Name,
					"arguments": stringifyToolArguments(part.Call.Arguments),
				})
			}
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
