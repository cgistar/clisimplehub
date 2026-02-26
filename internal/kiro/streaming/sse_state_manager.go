package streaming

import "sort"

// BlockState 内容块状态
type BlockState struct {
	BlockType string // "text" | "thinking" | "tool_use"
	Started   bool
	Stopped   bool
}

// SseStateManager SSE 事件序列状态机
type SseStateManager struct {
	messageStarted   bool
	messageDeltaSent bool
	activeBlocks     map[int]*BlockState
	messageEnded     bool
	nextBlockIndex   int
	stopReason       string
	hasToolUse       bool
}

// NewSseStateManager 创建状态管理器
func NewSseStateManager() *SseStateManager {
	return &SseStateManager{
		activeBlocks: make(map[int]*BlockState),
	}
}

// NextBlockIndex 分配下一个块索引
func (m *SseStateManager) NextBlockIndex() int {
	idx := m.nextBlockIndex
	m.nextBlockIndex++
	return idx
}

// SetHasToolUse 标记工具调用
func (m *SseStateManager) SetHasToolUse(has bool) { m.hasToolUse = has }

// SetStopReason 设置 stop_reason
func (m *SseStateManager) SetStopReason(reason string) { m.stopReason = reason }

// IsBlockOpenOfType 检查 block 是否处于可接收 delta 的打开状态
func (m *SseStateManager) IsBlockOpenOfType(index int, expectedType string) bool {
	b, ok := m.activeBlocks[index]
	return ok && b.Started && !b.Stopped && b.BlockType == expectedType
}

// HasNonThinkingBlocks 检查是否存在非 thinking 类型的 block
func (m *SseStateManager) HasNonThinkingBlocks() bool {
	for _, b := range m.activeBlocks {
		if b.BlockType != "thinking" {
			return true
		}
	}
	return false
}

// GetStopReason 获取最终 stop_reason
func (m *SseStateManager) GetStopReason() string {
	if m.stopReason != "" {
		return m.stopReason
	}
	if m.hasToolUse {
		return "tool_use"
	}
	return "end_turn"
}

// HandleMessageStart 处理 message_start（防重复）
func (m *SseStateManager) HandleMessageStart(data map[string]any) *SseEvent {
	if m.messageStarted {
		return nil
	}
	m.messageStarted = true
	return NewSseEvent("message_start", data)
}

// HandleContentBlockStart 处理 content_block_start（tool_use 时自动关闭 text block）
func (m *SseStateManager) HandleContentBlockStart(index int, blockType string, data map[string]any) []*SseEvent {
	var events []*SseEvent

	// tool_use 块开始时，关闭之前未关闭的 text 块（按 index 排序确保确定性）
	if blockType == "tool_use" {
		m.hasToolUse = true
		var textIndices []int
		for idx, b := range m.activeBlocks {
			if b.BlockType == "text" && b.Started && !b.Stopped {
				textIndices = append(textIndices, idx)
			}
		}
		sort.Ints(textIndices)
		for _, idx := range textIndices {
			events = append(events, NewSseEvent("content_block_stop", map[string]any{
				"type":  "content_block_stop",
				"index": idx,
			}))
			m.activeBlocks[idx].Stopped = true
		}
	}

	// 检查块是否已存在
	if b, ok := m.activeBlocks[index]; ok {
		if b.Started {
			return events
		}
		b.Started = true
	} else {
		m.activeBlocks[index] = &BlockState{BlockType: blockType, Started: true}
	}

	events = append(events, NewSseEvent("content_block_start", data))
	return events
}

// HandleContentBlockDelta 处理 content_block_delta（校验 block 已打开）
func (m *SseStateManager) HandleContentBlockDelta(index int, data map[string]any) *SseEvent {
	b, ok := m.activeBlocks[index]
	if !ok || !b.Started || b.Stopped {
		return nil
	}
	return NewSseEvent("content_block_delta", data)
}

// HandleContentBlockStop 处理 content_block_stop（防重复关闭）
func (m *SseStateManager) HandleContentBlockStop(index int) *SseEvent {
	b, ok := m.activeBlocks[index]
	if !ok || b.Stopped {
		return nil
	}
	b.Stopped = true
	return NewSseEvent("content_block_stop", map[string]any{
		"type":  "content_block_stop",
		"index": index,
	})
}

// GenerateFinalEvents 生成最终事件序列
func (m *SseStateManager) GenerateFinalEvents(inputTokens, outputTokens int) []*SseEvent {
	var events []*SseEvent

	// 按 index 排序关闭所有未关闭的块（确保输出顺序确定）
	var openIndices []int
	for idx, b := range m.activeBlocks {
		if b.Started && !b.Stopped {
			openIndices = append(openIndices, idx)
		}
	}
	sort.Ints(openIndices)
	for _, idx := range openIndices {
		events = append(events, NewSseEvent("content_block_stop", map[string]any{
			"type":  "content_block_stop",
			"index": idx,
		}))
		m.activeBlocks[idx].Stopped = true
	}

	// message_delta
	if !m.messageDeltaSent {
		m.messageDeltaSent = true
		events = append(events, NewSseEvent("message_delta", map[string]any{
			"type": "message_delta",
			"delta": map[string]any{
				"stop_reason":   m.GetStopReason(),
				"stop_sequence": nil,
			},
			"usage": map[string]any{
				"input_tokens":  inputTokens,
				"output_tokens": outputTokens,
			},
		}))
	}

	// message_stop
	if !m.messageEnded {
		m.messageEnded = true
		events = append(events, NewSseEvent("message_stop", map[string]any{
			"type": "message_stop",
		}))
	}

	return events
}
