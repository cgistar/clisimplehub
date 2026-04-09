package entclawruntime

import (
	"encoding/json"
	"strings"
)

type chatAdapter struct{}

func (chatAdapter) LoopbackPath() string { return "/v1/chat/completions" }

func (chatAdapter) WithStreamFlag(body []byte, stream bool) ([]byte, error) {
	return mutateJSON(body, func(payload map[string]any) error {
		payload["stream"] = stream
		return nil
	})
}

func (chatAdapter) ParseToolCalls(body []byte) (AssistantTurn, error) {
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
		return AssistantTurn{}, err
	}
	if len(payload.Choices) == 0 {
		return AssistantTurn{}, nil
	}

	turn := AssistantTurn{
		Parts: make([]AssistantTurnPart, 0, len(payload.Choices[0].Message.ToolCalls)+1),
	}
	if payload.Choices[0].Message.Content != "" {
		turn.Parts = append(turn.Parts, assistantTextPart(payload.Choices[0].Message.Content))
	}
	for _, toolCall := range payload.Choices[0].Message.ToolCalls {
		turn.Parts = append(turn.Parts, assistantToolCallPart(ToolCall{
			ID:        toolCall.ID,
			Name:      toolCall.Function.Name,
			Arguments: json.RawMessage(toolCall.Function.Arguments),
		}))
	}

	return turn, nil
}

func (chatAdapter) AppendToolResults(body []byte, turn AssistantTurn, rounds []ToolRound) ([]byte, error) {
	return mutateJSON(body, func(payload map[string]any) error {
		raw, _ := payload["messages"].([]any)
		parts := assistantTurnPartsForLoopback(turn, rounds)
		if len(parts) > 0 {
			toolCalls := make([]any, 0, len(parts))
			var content strings.Builder
			for _, part := range parts {
				if part.Type == assistantTurnPartText {
					content.WriteString(part.Text)
					continue
				}
				if part.Type != assistantTurnPartToolCall {
					continue
				}
				toolCalls = append(toolCalls, map[string]any{
					"id":   part.Call.ID,
					"type": "function",
					"function": map[string]any{
						"name":      part.Call.Name,
						"arguments": stringifyToolArguments(part.Call.Arguments),
					},
				})
			}
			message := map[string]any{"role": "assistant"}
			if content.Len() > 0 {
				message["content"] = content.String()
			}
			if len(toolCalls) > 0 {
				message["tool_calls"] = toolCalls
			}
			raw = append(raw, message)
		}
		for _, round := range rounds {
			raw = append(raw, map[string]any{
				"role":         "tool",
				"tool_call_id": round.Call.ID,
				"content":      stringifyToolResultContent(round.Result.Content),
			})
		}
		payload["messages"] = raw
		return nil
	})
}
