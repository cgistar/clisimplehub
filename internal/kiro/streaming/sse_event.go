package streaming

import (
	"encoding/json"
	"fmt"
)

// SseEvent SSE 事件
type SseEvent struct {
	Event string
	Data  map[string]any
}

// NewSseEvent 构造 SSE 事件
func NewSseEvent(event string, data map[string]any) *SseEvent {
	return &SseEvent{Event: event, Data: data}
}

// ToSSEString 格式化为 SSE 字符串
func (e *SseEvent) ToSSEString() string {
	jsonBytes, _ := json.Marshal(e.Data)
	return fmt.Sprintf("event: %s\ndata: %s\n\n", e.Event, string(jsonBytes))
}
