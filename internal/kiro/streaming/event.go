package streaming

import (
	"encoding/json"
	"fmt"
)

// EventType 事件类型枚举
type EventType int

const (
	EventAssistantResponse EventType = iota
	EventToolUse
	EventContextUsage
	EventError
	EventException
	EventMetering
	EventUnknown
)

// Event 统一事件类型
type Event struct {
	Type             EventType
	Content          string  // AssistantResponse
	ToolUseID        string  // ToolUse
	ToolName         string  // ToolUse
	ToolInput        string  // ToolUse
	ToolStop         bool    // ToolUse
	ContextUsagePct  float64 // ContextUsage
	ErrorCode        string  // Error
	ErrorMessage     string  // Error
	ExceptionType    string  // Exception
	ExceptionMessage string  // Exception
}

// EventFromFrame 从解码帧解析出事件
func EventFromFrame(frame *Frame) (*Event, error) {
	msgType := frame.MessageType()
	if msgType == "" {
		msgType = "event"
	}

	switch msgType {
	case "event":
		return parseEventFrame(frame)
	case "error":
		return parseErrorFrame(frame), nil
	case "exception":
		return parseExceptionFrame(frame), nil
	default:
		return nil, fmt.Errorf("invalid message type: %s", msgType)
	}
}

func parseEventFrame(frame *Frame) (*Event, error) {
	eventType := frame.EventType()

	switch eventType {
	case "assistantResponseEvent":
		var payload struct {
			Content string `json:"content"`
		}
		if err := json.Unmarshal(frame.Payload, &payload); err != nil {
			return nil, fmt.Errorf("parse assistantResponseEvent: %w", err)
		}
		return &Event{Type: EventAssistantResponse, Content: payload.Content}, nil

	case "toolUseEvent":
		var payload struct {
			Name      string `json:"name"`
			ToolUseID string `json:"toolUseId"`
			Input     string `json:"input"`
			Stop      bool   `json:"stop"`
		}
		if err := json.Unmarshal(frame.Payload, &payload); err != nil {
			return nil, fmt.Errorf("parse toolUseEvent: %w", err)
		}
		return &Event{
			Type:      EventToolUse,
			ToolName:  payload.Name,
			ToolUseID: payload.ToolUseID,
			ToolInput: payload.Input,
			ToolStop:  payload.Stop,
		}, nil

	case "contextUsageEvent":
		var payload struct {
			ContextUsagePercentage float64 `json:"contextUsagePercentage"`
		}
		if err := json.Unmarshal(frame.Payload, &payload); err != nil {
			return nil, fmt.Errorf("parse contextUsageEvent: %w", err)
		}
		return &Event{Type: EventContextUsage, ContextUsagePct: payload.ContextUsagePercentage}, nil

	case "meteringEvent":
		return &Event{Type: EventMetering}, nil

	default:
		return &Event{Type: EventUnknown}, nil
	}
}

func parseErrorFrame(frame *Frame) *Event {
	code := frame.ErrorCode()
	if code == "" {
		code = "UnknownError"
	}
	return &Event{
		Type:         EventError,
		ErrorCode:    code,
		ErrorMessage: string(frame.Payload),
	}
}

func parseExceptionFrame(frame *Frame) *Event {
	exType := frame.ExceptionType()
	if exType == "" {
		exType = "UnknownException"
	}
	return &Event{
		Type:             EventException,
		ExceptionType:    exType,
		ExceptionMessage: string(frame.Payload),
	}
}
