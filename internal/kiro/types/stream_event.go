package types

// StreamEventType 表示从 Kiro EventStream（AWS EventStream 二进制帧）规范化后的事件类型。
type StreamEventType string

const (
	StreamEventContent      StreamEventType = "content"
	StreamEventToolStart    StreamEventType = "tool_start"
	StreamEventToolInput    StreamEventType = "tool_input"
	StreamEventToolStop     StreamEventType = "tool_stop"
	StreamEventToolUses     StreamEventType = "tool_uses"
	StreamEventUsage        StreamEventType = "usage"
	StreamEventContextUsage StreamEventType = "context_usage"
	StreamEventStopReason   StreamEventType = "stop_reason"
	StreamEventError        StreamEventType = "error"
)

// StreamEvent 是 Kiro 流式返回的统一事件结构；不同 Type 对应的 Data 结构不同。
type StreamEvent struct {
	Type StreamEventType
	Data any
}
