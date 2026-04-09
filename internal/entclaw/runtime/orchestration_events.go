package entclawruntime

import "encoding/json"

type OrchestrationEventType string

const (
	OrchestrationAssistantMessage  OrchestrationEventType = "assistant_message"
	OrchestrationAssistantToolCall OrchestrationEventType = "assistant_tool_call"
	OrchestrationToolStarted       OrchestrationEventType = "tool_started"
	OrchestrationToolCompleted     OrchestrationEventType = "tool_completed"
	OrchestrationFailed            OrchestrationEventType = "failed"
	OrchestrationCompleted         OrchestrationEventType = "completed"
)

type OrchestrationEvent struct {
	Type      OrchestrationEventType `json:"type"`
	CallID    string                 `json:"callId,omitempty"`
	Name      string                 `json:"name,omitempty"`
	Text      string                 `json:"text,omitempty"`
	Arguments json.RawMessage        `json:"arguments,omitempty"`
	Output    json.RawMessage        `json:"output,omitempty"`
	IsError   bool                   `json:"isError,omitempty"`
}

func NewAssistantMessageEvent(text string) OrchestrationEvent {
	return OrchestrationEvent{
		Type: OrchestrationAssistantMessage,
		Text: text,
	}
}

func NewAssistantToolCallEvent(callID, name string, arguments json.RawMessage) OrchestrationEvent {
	return OrchestrationEvent{
		Type:      OrchestrationAssistantToolCall,
		CallID:    callID,
		Name:      name,
		Arguments: cloneOrchestrationRawMessage(arguments),
	}
}

func NewToolStartedEvent(callID string) OrchestrationEvent {
	return OrchestrationEvent{
		Type:   OrchestrationToolStarted,
		CallID: callID,
	}
}

func NewToolCompletedEvent(callID string, output json.RawMessage, isError bool) OrchestrationEvent {
	return OrchestrationEvent{
		Type:    OrchestrationToolCompleted,
		CallID:  callID,
		Output:  cloneOrchestrationRawMessage(output),
		IsError: isError,
	}
}

func NewFailureEvent(callID string, err error) OrchestrationEvent {
	event := OrchestrationEvent{
		Type:    OrchestrationFailed,
		CallID:  callID,
		IsError: true,
	}
	if err == nil {
		return event
	}

	raw, marshalErr := json.Marshal(map[string]string{
		"error": err.Error(),
	})
	if marshalErr == nil {
		event.Output = raw
	}
	return event
}

func NewCompletionEvent() OrchestrationEvent {
	return OrchestrationEvent{Type: OrchestrationCompleted}
}

func cloneOrchestrationRawMessage(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 {
		return nil
	}
	return append(json.RawMessage(nil), raw...)
}
