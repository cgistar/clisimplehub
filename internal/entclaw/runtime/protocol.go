package entclawruntime

import (
	"encoding/json"
	"fmt"
	"strings"
)

type ToolCall struct {
	ID        string
	Name      string
	Arguments json.RawMessage
}

type ToolResult struct {
	Content json.RawMessage
	IsError bool
}

type ToolRound struct {
	Call   ToolCall
	Result ToolResult
}

const (
	assistantTurnPartText     = "text"
	assistantTurnPartToolCall = "tool_call"
)

type AssistantTurnPart struct {
	Type string
	Text string
	Call ToolCall
}

type AssistantTurn struct {
	Parts []AssistantTurnPart
}

func (turn AssistantTurn) FinalText() string {
	var text strings.Builder
	for _, part := range turn.Parts {
		if part.Type == assistantTurnPartText {
			text.WriteString(part.Text)
		}
	}
	return text.String()
}

func (turn AssistantTurn) ToolCalls() []ToolCall {
	calls := make([]ToolCall, 0, len(turn.Parts))
	for _, part := range turn.Parts {
		if part.Type == assistantTurnPartToolCall {
			calls = append(calls, part.Call)
		}
	}
	return calls
}

type ProtocolAdapter interface {
	LoopbackPath() string
	WithStreamFlag(body []byte, stream bool) ([]byte, error)
	ParseToolCalls(body []byte) (AssistantTurn, error)
	AppendToolResults(body []byte, turn AssistantTurn, rounds []ToolRound) ([]byte, error)
}

func adapterForFormat(format RequestFormat) ProtocolAdapter {
	switch format {
	case FormatMessages:
		return messagesAdapter{}
	case FormatChatCompletions:
		return chatAdapter{}
	case FormatResponses:
		return responsesAdapter{}
	default:
		return nil
	}
}

func mutateJSON(body []byte, fn func(map[string]any) error) ([]byte, error) {
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, err
	}
	if err := fn(payload); err != nil {
		return nil, err
	}
	return json.Marshal(payload)
}

func rawJSONObjectOrEmpty(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 {
		return json.RawMessage(`{}`)
	}
	return raw
}

func stringifyToolArguments(raw json.RawMessage) string {
	return string(rawJSONObjectOrEmpty(raw))
}

func normalizeResponsesArguments(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 {
		return json.RawMessage(`{}`)
	}

	var value string
	if err := json.Unmarshal(raw, &value); err == nil {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			return json.RawMessage(`{}`)
		}
		return json.RawMessage(trimmed)
	}

	return raw
}

func decodeToolPayload(raw json.RawMessage) any {
	if len(raw) == 0 {
		return ""
	}

	var value any
	if err := json.Unmarshal(raw, &value); err == nil {
		return value
	}
	return string(raw)
}

func stringifyToolResultContent(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}

	var value string
	if err := json.Unmarshal(raw, &value); err == nil {
		return value
	}

	return string(raw)
}

func assistantTextPart(text string) AssistantTurnPart {
	return AssistantTurnPart{
		Type: assistantTurnPartText,
		Text: text,
	}
}

func assistantToolCallPart(call ToolCall) AssistantTurnPart {
	return AssistantTurnPart{
		Type: assistantTurnPartToolCall,
		Call: call,
	}
}

func assistantTurnPartsForLoopback(turn AssistantTurn, rounds []ToolRound) []AssistantTurnPart {
	if len(turn.Parts) > 0 {
		return turn.Parts
	}

	parts := make([]AssistantTurnPart, 0, len(rounds))
	for _, round := range rounds {
		parts = append(parts, assistantToolCallPart(round.Call))
	}
	return parts
}
func normalizeResponsesInput(input any) ([]any, error) {
	switch value := input.(type) {
	case nil:
		return nil, nil
	case []any:
		return append([]any(nil), value...), nil
	case string:
		return []any{
			map[string]any{
				"type":    "message",
				"role":    "user",
				"content": value,
			},
		}, nil
	default:
		return nil, fmt.Errorf("unsupported responses input type %T", input)
	}
}
