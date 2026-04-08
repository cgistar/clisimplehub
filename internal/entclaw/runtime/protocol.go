package entclawruntime

import "encoding/json"

type ToolCall struct {
	ID        string
	Name      string
	Arguments json.RawMessage
}

type ToolResult struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Content string `json:"content"`
	IsError bool   `json:"is_error"`
}

type ProtocolAdapter interface {
	LoopbackPath() string
	WithStreamFlag(body []byte, stream bool) ([]byte, error)
	ParseToolCalls(body []byte) ([]ToolCall, string, error)
	AppendToolResults(body []byte, results []ToolResult) ([]byte, error)
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
