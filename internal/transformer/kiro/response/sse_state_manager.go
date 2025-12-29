package response

import (
	"fmt"
)

// BlockState 内容块状态，跟踪每个 content_block 的生命周期
type BlockState struct {
	Index     int    `json:"index"`
	Type      string `json:"type"` // "text" | "tool_use" | "thinking"
	Started   bool   `json:"started"`
	Stopped   bool   `json:"stopped"`
	ToolUseID string `json:"tool_use_id,omitempty"` // 仅用于工具块
}

// SSEStateManager 管理 Claude SSE 事件序列状态，确保事件符合规范
// Claude 规范要求：
//   - message_start 必须首先发送，且只能发送一次
//   - content_block_start 必须在 message_start 之后
//   - content_block_delta 必须在对应的 content_block_start 之后
//   - content_block_stop 必须在所有 delta 之后
//   - message_delta 必须在所有 content_block_stop 之后
//   - message_stop 必须最后发送
type SSEStateManager struct {
	messageStarted   bool
	messageDeltaSent bool
	activeBlocks     map[int]*BlockState
	messageEnded     bool
	nextBlockIndex   int
	strictMode       bool
}

// NewSSEStateManager 创建 SSE 状态管理器
func NewSSEStateManager(strictMode bool) *SSEStateManager {
	return &SSEStateManager{
		activeBlocks: make(map[int]*BlockState),
		strictMode:   strictMode,
	}
}

// Reset 重置状态管理器
func (ssm *SSEStateManager) Reset() {
	ssm.messageStarted = false
	ssm.messageDeltaSent = false
	ssm.messageEnded = false
	ssm.activeBlocks = make(map[int]*BlockState)
	ssm.nextBlockIndex = 0
}

// ValidateEventResult 验证结果
type ValidateEventResult struct {
	Valid        bool
	Error        string
	AutoEvents   []map[string]any // 需要自动生成的事件（如自动关闭块）
	ShouldSkip   bool             // 是否应跳过该事件（非严格模式）
	ModifiedData map[string]any   // 修改后的事件数据
}

// ValidateEvent 验证事件是否符合 Claude 规范
// 返回验证结果，包含可能需要自动生成的事件
func (ssm *SSEStateManager) ValidateEvent(eventData map[string]any) ValidateEventResult {
	eventType, ok := eventData["type"].(string)
	if !ok {
		return ValidateEventResult{Valid: false, Error: "无效的事件类型"}
	}

	switch eventType {
	case "message_start":
		return ssm.validateMessageStart(eventData)
	case "content_block_start":
		return ssm.validateContentBlockStart(eventData)
	case "content_block_delta":
		return ssm.validateContentBlockDelta(eventData)
	case "content_block_stop":
		return ssm.validateContentBlockStop(eventData)
	case "message_delta":
		return ssm.validateMessageDelta(eventData)
	case "message_stop":
		return ssm.validateMessageStop(eventData)
	default:
		// 其他事件类型直接通过
		return ValidateEventResult{Valid: true}
	}
}

// ApplyEvent 应用事件到状态（在验证通过后调用）
func (ssm *SSEStateManager) ApplyEvent(eventData map[string]any) {
	eventType, _ := eventData["type"].(string)

	switch eventType {
	case "message_start":
		ssm.messageStarted = true
	case "content_block_start":
		index := ssm.extractIndex(eventData)
		blockType := ssm.extractBlockType(eventData)
		toolUseID := ssm.extractToolUseID(eventData)

		ssm.activeBlocks[index] = &BlockState{
			Index:     index,
			Type:      blockType,
			Started:   true,
			Stopped:   false,
			ToolUseID: toolUseID,
		}
		if index >= ssm.nextBlockIndex {
			ssm.nextBlockIndex = index + 1
		}
	case "content_block_stop":
		index := ssm.extractIndex(eventData)
		if block, exists := ssm.activeBlocks[index]; exists {
			block.Stopped = true
		}
	case "message_delta":
		ssm.messageDeltaSent = true
	case "message_stop":
		ssm.messageEnded = true
	}
}

// validateMessageStart 验证 message_start 事件
func (ssm *SSEStateManager) validateMessageStart(eventData map[string]any) ValidateEventResult {
	if ssm.messageStarted {
		errMsg := "违规：message_start 只能出现一次"
		if ssm.strictMode {
			return ValidateEventResult{Valid: false, Error: errMsg}
		}
		return ValidateEventResult{Valid: true, ShouldSkip: true}
	}
	return ValidateEventResult{Valid: true}
}

// validateContentBlockStart 验证 content_block_start 事件
func (ssm *SSEStateManager) validateContentBlockStart(eventData map[string]any) ValidateEventResult {
	result := ValidateEventResult{Valid: true}

	if !ssm.messageStarted {
		errMsg := "违规：content_block_start 必须在 message_start 之后"
		if ssm.strictMode {
			return ValidateEventResult{Valid: false, Error: errMsg}
		}
	}

	if ssm.messageEnded {
		errMsg := "违规：message 已结束，不能发送 content_block_start"
		if ssm.strictMode {
			return ValidateEventResult{Valid: false, Error: errMsg}
		}
		return ValidateEventResult{Valid: true, ShouldSkip: true}
	}

	index := ssm.extractIndex(eventData)
	if block, exists := ssm.activeBlocks[index]; exists && block.Started && !block.Stopped {
		errMsg := fmt.Sprintf("违规：索引 %d 的 content_block 已经 started 但未 stopped", index)
		if ssm.strictMode {
			return ValidateEventResult{Valid: false, Error: errMsg}
		}
		return ValidateEventResult{Valid: true, ShouldSkip: true}
	}

	// 关键优化：工具块启动前自动关闭未关闭的文本块
	blockType := ssm.extractBlockType(eventData)
	if blockType == "tool_use" {
		for blockIndex, block := range ssm.activeBlocks {
			if block.Type == "text" && block.Started && !block.Stopped {
				stopEvent := map[string]any{
					"type":  "content_block_stop",
					"index": blockIndex,
				}
				result.AutoEvents = append(result.AutoEvents, stopEvent)
				block.Stopped = true // 预先标记为已关闭
			}
		}
	}

	return result
}

// validateContentBlockDelta 验证 content_block_delta 事件
func (ssm *SSEStateManager) validateContentBlockDelta(eventData map[string]any) ValidateEventResult {
	index := ssm.extractIndex(eventData)
	if index < 0 {
		errMsg := "content_block_delta 缺少有效索引"
		if ssm.strictMode {
			return ValidateEventResult{Valid: false, Error: errMsg}
		}
		return ValidateEventResult{Valid: true, ShouldSkip: true}
	}

	block, exists := ssm.activeBlocks[index]

	// 自动启动：如果块未启动，生成 content_block_start
	if !exists || !block.Started {
		blockType := ssm.inferBlockTypeFromDelta(eventData)
		startEvent := map[string]any{
			"type":  "content_block_start",
			"index": index,
			"content_block": map[string]any{
				"type": blockType,
			},
		}
		if blockType == "text" {
			startEvent["content_block"].(map[string]any)["text"] = ""
		} else if blockType == "tool_use" {
			startEvent["content_block"].(map[string]any)["id"] = fmt.Sprintf("tooluse_auto_%d", index)
			startEvent["content_block"].(map[string]any)["name"] = "auto_detected"
			startEvent["content_block"].(map[string]any)["input"] = map[string]any{}
		}

		// 预创建块状态
		ssm.activeBlocks[index] = &BlockState{
			Index:   index,
			Type:    blockType,
			Started: true,
			Stopped: false,
		}
		if index >= ssm.nextBlockIndex {
			ssm.nextBlockIndex = index + 1
		}

		return ValidateEventResult{
			Valid:      true,
			AutoEvents: []map[string]any{startEvent},
		}
	}

	if block.Stopped {
		errMsg := fmt.Sprintf("违规：索引 %d 的 content_block 已停止，不能发送 delta", index)
		if ssm.strictMode {
			return ValidateEventResult{Valid: false, Error: errMsg}
		}
		return ValidateEventResult{Valid: true, ShouldSkip: true}
	}

	return ValidateEventResult{Valid: true}
}

// validateContentBlockStop 验证 content_block_stop 事件
func (ssm *SSEStateManager) validateContentBlockStop(eventData map[string]any) ValidateEventResult {
	index := ssm.extractIndex(eventData)
	if index < 0 {
		errMsg := "content_block_stop 缺少有效索引"
		if ssm.strictMode {
			return ValidateEventResult{Valid: false, Error: errMsg}
		}
		return ValidateEventResult{Valid: true, ShouldSkip: true}
	}

	block, exists := ssm.activeBlocks[index]
	if !exists || !block.Started {
		errMsg := fmt.Sprintf("违规：索引 %d 的 content_block 未启动就发送 stop", index)
		if ssm.strictMode {
			return ValidateEventResult{Valid: false, Error: errMsg}
		}
		return ValidateEventResult{Valid: true, ShouldSkip: true}
	}

	if block.Stopped {
		errMsg := fmt.Sprintf("违规：索引 %d 的 content_block 重复停止", index)
		if ssm.strictMode {
			return ValidateEventResult{Valid: false, Error: errMsg}
		}
		return ValidateEventResult{Valid: true, ShouldSkip: true}
	}

	return ValidateEventResult{Valid: true}
}

// validateMessageDelta 验证 message_delta 事件
func (ssm *SSEStateManager) validateMessageDelta(eventData map[string]any) ValidateEventResult {
	result := ValidateEventResult{Valid: true}

	if !ssm.messageStarted {
		errMsg := "违规：message_delta 必须在 message_start 之后"
		if ssm.strictMode {
			return ValidateEventResult{Valid: false, Error: errMsg}
		}
	}

	if ssm.messageDeltaSent {
		errMsg := "违规：message_delta 只能出现一次"
		if ssm.strictMode {
			return ValidateEventResult{Valid: false, Error: errMsg}
		}
		return ValidateEventResult{Valid: true, ShouldSkip: true}
	}

	// 关键优化：message_delta 前自动关闭所有未关闭的 content_block
	for index, block := range ssm.activeBlocks {
		if block.Started && !block.Stopped {
			stopEvent := map[string]any{
				"type":  "content_block_stop",
				"index": index,
			}
			result.AutoEvents = append(result.AutoEvents, stopEvent)
			block.Stopped = true
		}
	}

	return result
}

// validateMessageStop 验证 message_stop 事件
func (ssm *SSEStateManager) validateMessageStop(eventData map[string]any) ValidateEventResult {
	if !ssm.messageStarted {
		errMsg := "违规：message_stop 必须在 message_start 之后"
		if ssm.strictMode {
			return ValidateEventResult{Valid: false, Error: errMsg}
		}
	}

	if ssm.messageEnded {
		errMsg := "违规：message_stop 只能出现一次"
		if ssm.strictMode {
			return ValidateEventResult{Valid: false, Error: errMsg}
		}
		return ValidateEventResult{Valid: true, ShouldSkip: true}
	}

	return ValidateEventResult{Valid: true}
}

// GetActiveBlocks 获取所有活跃块
func (ssm *SSEStateManager) GetActiveBlocks() map[int]*BlockState {
	return ssm.activeBlocks
}

// GetUnclosedBlocks 获取所有未关闭的块
func (ssm *SSEStateManager) GetUnclosedBlocks() []int {
	var unclosed []int
	for index, block := range ssm.activeBlocks {
		if block.Started && !block.Stopped {
			unclosed = append(unclosed, index)
		}
	}
	return unclosed
}

// IsMessageStarted 检查消息是否已开始
func (ssm *SSEStateManager) IsMessageStarted() bool {
	return ssm.messageStarted
}

// IsMessageEnded 检查消息是否已结束
func (ssm *SSEStateManager) IsMessageEnded() bool {
	return ssm.messageEnded
}

// IsMessageDeltaSent 检查 message_delta 是否已发送
func (ssm *SSEStateManager) IsMessageDeltaSent() bool {
	return ssm.messageDeltaSent
}

// HasToolUseBlocks 检查是否存在工具调用块
func (ssm *SSEStateManager) HasToolUseBlocks() bool {
	for _, block := range ssm.activeBlocks {
		if block.Type == "tool_use" {
			return true
		}
	}
	return false
}

// GetNextBlockIndex 获取下一个可用的块索引
func (ssm *SSEStateManager) GetNextBlockIndex() int {
	return ssm.nextBlockIndex
}

// 辅助方法

func (ssm *SSEStateManager) extractIndex(eventData map[string]any) int {
	if v, ok := eventData["index"].(int); ok {
		return v
	}
	if f, ok := eventData["index"].(float64); ok {
		return int(f)
	}
	return -1
}

func (ssm *SSEStateManager) extractBlockType(eventData map[string]any) string {
	if contentBlock, ok := eventData["content_block"].(map[string]any); ok {
		if cbType, ok := contentBlock["type"].(string); ok {
			return cbType
		}
	}
	return "text"
}

func (ssm *SSEStateManager) extractToolUseID(eventData map[string]any) string {
	if contentBlock, ok := eventData["content_block"].(map[string]any); ok {
		if id, ok := contentBlock["id"].(string); ok {
			return id
		}
	}
	return ""
}

func (ssm *SSEStateManager) inferBlockTypeFromDelta(eventData map[string]any) string {
	if delta, ok := eventData["delta"].(map[string]any); ok {
		if deltaType, ok := delta["type"].(string); ok {
			if deltaType == "input_json_delta" {
				return "tool_use"
			}
		}
	}
	return "text"
}
