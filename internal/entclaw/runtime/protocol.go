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

type ProtocolAdapter interface {
	LoopbackPath() string
	WithStreamFlag(body []byte, stream bool) ([]byte, error)
	ParseToolCalls(body []byte) ([]ToolCall, string, error)
	AppendToolResults(body []byte, rounds []ToolRound) ([]byte, error)
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
