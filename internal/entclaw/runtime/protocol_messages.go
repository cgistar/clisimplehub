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

func (messagesAdapter) ParseToolCalls(body []byte) (AssistantTurn, error) {
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
		return AssistantTurn{}, err
	}

	turn := AssistantTurn{
		Parts: make([]AssistantTurnPart, 0, len(payload.Content)),
	}
	for _, item := range payload.Content {
		if item.Type == "tool_use" {
			turn.Parts = append(turn.Parts, assistantToolCallPart(ToolCall{
				ID:        item.ID,
				Name:      item.Name,
				Arguments: item.Input,
			}))
		}
		if item.Type == "text" {
			turn.Parts = append(turn.Parts, assistantTextPart(item.Text))
		}
	}

	return turn, nil
}

func (messagesAdapter) AppendToolResults(body []byte, turn AssistantTurn, rounds []ToolRound) ([]byte, error) {
	return mutateJSON(body, func(payload map[string]any) error {
		raw, _ := payload["messages"].([]any)
		parts := assistantTurnPartsForLoopback(turn, rounds)
		if len(parts) > 0 {
			assistantContent := make([]any, 0, len(parts))
			for _, part := range parts {
				switch part.Type {
				case assistantTurnPartText:
					assistantContent = append(assistantContent, map[string]any{
						"type": "text",
						"text": part.Text,
					})
				case assistantTurnPartToolCall:
					assistantContent = append(assistantContent, map[string]any{
						"type":  "tool_use",
						"id":    part.Call.ID,
						"name":  part.Call.Name,
						"input": rawJSONObjectOrEmpty(part.Call.Arguments),
					})
				}
			}
			raw = append(raw, map[string]any{
				"role":    "assistant",
				"content": assistantContent,
			})
		}
		if len(rounds) > 0 {
			userContent := make([]any, 0, len(rounds))
			for _, round := range rounds {
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
				"role":    "user",
				"content": userContent,
			})
		}
		payload["messages"] = raw
		return nil
	})
}
