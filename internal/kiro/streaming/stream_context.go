package streaming

import (
	"fmt"
	"math"
	"strings"

	"github.com/google/uuid"
)

const contextWindowSize = 200_000

// StreamContext 核心有状态流式处理器
type StreamContext struct {
	stateManager *SseStateManager
	model        string
	messageID    string

	// Token 计数
	inputTokens        int
	contextInputTokens *int // 来自 contextUsageEvent
	outputTokens       int

	// Tool 累积
	toolBlockIndices map[string]int

	// Thinking
	thinkingEnabled             bool
	thinkingBuffer              string
	inThinkingBlock             bool
	thinkingExtracted           bool
	thinkingBlockIndex          *int
	textBlockIndex              *int
	stripThinkingLeadingNewline bool
}

// NewStreamContext 创建流式处理上下文
func NewStreamContext(model string, inputTokens int, thinkingEnabled bool) *StreamContext {
	return &StreamContext{
		stateManager:     NewSseStateManager(),
		model:            model,
		messageID:        fmt.Sprintf("msg_%s", strings.ReplaceAll(uuid.New().String(), "-", "")),
		inputTokens:      inputTokens,
		toolBlockIndices: make(map[string]int),
		thinkingEnabled:  thinkingEnabled,
	}
}

// GenerateInitialEvents 生成初始事件序列
func (c *StreamContext) GenerateInitialEvents() []*SseEvent {
	var events []*SseEvent

	msgStart := c.createMessageStartEvent()
	if ev := c.stateManager.HandleMessageStart(msgStart); ev != nil {
		events = append(events, ev)
	}

	// thinking 模式下延迟创建文本块
	if c.thinkingEnabled {
		return events
	}

	textIdx := c.stateManager.NextBlockIndex()
	c.textBlockIndex = &textIdx
	blockEvents := c.stateManager.HandleContentBlockStart(textIdx, "text", map[string]any{
		"type":  "content_block_start",
		"index": textIdx,
		"content_block": map[string]any{
			"type": "text",
			"text": "",
		},
	})
	events = append(events, blockEvents...)
	return events
}

// ProcessEvent 处理 Kiro 事件并转换为 Anthropic SSE 事件
func (c *StreamContext) ProcessEvent(event *Event) []*SseEvent {
	switch event.Type {
	case EventAssistantResponse:
		return c.processAssistantResponse(event.Content)
	case EventToolUse:
		return c.processToolUse(event)
	case EventContextUsage:
		pct := event.ContextUsagePct
		if math.IsNaN(pct) || math.IsInf(pct, 0) || pct < 0 {
			pct = 0
		}
		actualInputTokens := int(pct * float64(contextWindowSize) / 100.0)
		c.contextInputTokens = &actualInputTokens
		if pct >= 100.0 {
			c.stateManager.SetStopReason("model_context_window_exceeded")
		}
		return nil
	case EventError:
		return nil
	case EventException:
		if event.ExceptionType == "ContentLengthExceededException" {
			c.stateManager.SetStopReason("max_tokens")
		}
		return nil
	default:
		return nil
	}
}

// GenerateFinalEvents 生成最终事件序列
func (c *StreamContext) GenerateFinalEvents() []*SseEvent {
	var events []*SseEvent

	// Flush thinking_buffer 中的剩余内容
	if c.thinkingEnabled && c.thinkingBuffer != "" {
		if c.inThinkingBlock {
			if endPos := findRealThinkingEndTagAtBufferEnd(c.thinkingBuffer); endPos >= 0 {
				thinkingContent := c.thinkingBuffer[:endPos]
				if thinkingContent != "" && c.thinkingBlockIndex != nil {
					events = append(events, c.createThinkingDeltaEvent(*c.thinkingBlockIndex, thinkingContent))
				}
				if c.thinkingBlockIndex != nil {
					events = append(events, c.createThinkingDeltaEvent(*c.thinkingBlockIndex, ""))
					if ev := c.stateManager.HandleContentBlockStop(*c.thinkingBlockIndex); ev != nil {
						events = append(events, ev)
					}
				}
				afterPos := endPos + len(thinkingEndTag)
				remaining := strings.TrimLeft(c.thinkingBuffer[afterPos:], " \t\n\r")
				c.thinkingBuffer = ""
				c.inThinkingBlock = false
				c.thinkingExtracted = true
				if remaining != "" {
					events = append(events, c.createTextDeltaEvents(remaining)...)
				}
			} else {
				// 还在 thinking 块内，发送剩余内容
				if c.thinkingBlockIndex != nil {
					events = append(events, c.createThinkingDeltaEvent(*c.thinkingBlockIndex, c.thinkingBuffer))
					events = append(events, c.createThinkingDeltaEvent(*c.thinkingBlockIndex, ""))
					if ev := c.stateManager.HandleContentBlockStop(*c.thinkingBlockIndex); ev != nil {
						events = append(events, ev)
					}
				}
			}
		} else {
			bufferContent := c.thinkingBuffer
			events = append(events, c.createTextDeltaEvents(bufferContent)...)
		}
		c.thinkingBuffer = ""
	}

	// thinking-only 输出 → 补发 text " " + stop_reason "max_tokens"
	if c.thinkingEnabled && c.thinkingBlockIndex != nil && !c.stateManager.HasNonThinkingBlocks() {
		c.stateManager.SetStopReason("max_tokens")
		events = append(events, c.createTextDeltaEvents(" ")...)
	}

	// 使用 contextUsageEvent 计算的 input_tokens
	finalInputTokens := c.inputTokens
	if c.contextInputTokens != nil {
		finalInputTokens = *c.contextInputTokens
	}

	events = append(events, c.stateManager.GenerateFinalEvents(finalInputTokens, c.outputTokens)...)
	return events
}

// ContextInputTokens 返回从 contextUsageEvent 计算的 input_tokens（用于外部校正）
func (c *StreamContext) ContextInputTokens() *int { return c.contextInputTokens }

// InputTokens 返回估算的 input_tokens
func (c *StreamContext) InputTokens() int { return c.inputTokens }

// OutputTokens 返回估算的 output_tokens
func (c *StreamContext) OutputTokens() int { return c.outputTokens }

// TokenUsage 返回当前 token 统计（input 优先使用 contextUsage 修正值）
func (c *StreamContext) TokenUsage() (int, int) {
	input := c.inputTokens
	if c.contextInputTokens != nil {
		input = *c.contextInputTokens
	}
	return input, c.outputTokens
}

// --- 内部方法 ---

func (c *StreamContext) createMessageStartEvent() map[string]any {
	return map[string]any{
		"type": "message_start",
		"message": map[string]any{
			"id":            c.messageID,
			"type":          "message",
			"role":          "assistant",
			"content":       []any{},
			"model":         c.model,
			"stop_reason":   nil,
			"stop_sequence": nil,
			"usage": map[string]any{
				"input_tokens":  c.inputTokens,
				"output_tokens": 1,
			},
		},
	}
}

func (c *StreamContext) processAssistantResponse(content string) []*SseEvent {
	if content == "" {
		return nil
	}
	c.outputTokens += estimateTokensForContent(content)

	if c.thinkingEnabled {
		return c.processContentWithThinking(content)
	}
	return c.createTextDeltaEvents(content)
}

func (c *StreamContext) processContentWithThinking(content string) []*SseEvent {
	var events []*SseEvent
	c.thinkingBuffer += content

	for {
		if !c.inThinkingBlock && !c.thinkingExtracted {
			startPos := findRealThinkingStartTag(c.thinkingBuffer)
			if startPos >= 0 {
				// <thinking> 之前的非空白内容作为 text_delta
				beforeThinking := c.thinkingBuffer[:startPos]
				if beforeThinking != "" && strings.TrimSpace(beforeThinking) != "" {
					events = append(events, c.createTextDeltaEvents(beforeThinking)...)
				}

				c.inThinkingBlock = true
				c.stripThinkingLeadingNewline = true
				c.thinkingBuffer = c.thinkingBuffer[startPos+len(thinkingStartTag):]

				thinkingIdx := c.stateManager.NextBlockIndex()
				c.thinkingBlockIndex = &thinkingIdx
				blockEvents := c.stateManager.HandleContentBlockStart(thinkingIdx, "thinking", map[string]any{
					"type":  "content_block_start",
					"index": thinkingIdx,
					"content_block": map[string]any{
						"type":     "thinking",
						"thinking": "",
					},
				})
				events = append(events, blockEvents...)
			} else {
				// 保留可能是部分标签的尾部
				targetLen := len(c.thinkingBuffer) - len(thinkingStartTag)
				if targetLen < 0 {
					targetLen = 0
				}
				safeLen := findCharBoundary(c.thinkingBuffer, targetLen)
				if safeLen > 0 {
					safeContent := c.thinkingBuffer[:safeLen]
					// adaptive 模式空白前缀不创建 text block
					if safeContent != "" && strings.TrimSpace(safeContent) != "" {
						events = append(events, c.createTextDeltaEvents(safeContent)...)
						c.thinkingBuffer = c.thinkingBuffer[safeLen:]
					}
				}
				break
			}
		} else if c.inThinkingBlock {
			// 剥离 <thinking> 后的前导换行
			if c.stripThinkingLeadingNewline {
				if strings.HasPrefix(c.thinkingBuffer, "\n") {
					c.thinkingBuffer = c.thinkingBuffer[1:]
					c.stripThinkingLeadingNewline = false
				} else if c.thinkingBuffer != "" {
					c.stripThinkingLeadingNewline = false
				}
				// buffer 为空时保留标志等待下个 chunk
			}

			endPos := findRealThinkingEndTag(c.thinkingBuffer)
			if endPos >= 0 {
				thinkingContent := c.thinkingBuffer[:endPos]
				if thinkingContent != "" && c.thinkingBlockIndex != nil {
					events = append(events, c.createThinkingDeltaEvent(*c.thinkingBlockIndex, thinkingContent))
				}

				c.inThinkingBlock = false
				c.thinkingExtracted = true

				if c.thinkingBlockIndex != nil {
					events = append(events, c.createThinkingDeltaEvent(*c.thinkingBlockIndex, ""))
					if ev := c.stateManager.HandleContentBlockStop(*c.thinkingBlockIndex); ev != nil {
						events = append(events, ev)
					}
				}

				// 剥离 "</thinking>\n\n"
				c.thinkingBuffer = c.thinkingBuffer[endPos+len(thinkingEndTag)+2:]
			} else {
				// 保留可能的 "</thinking>\n\n" 尾部
				targetLen := len(c.thinkingBuffer) - len("</thinking>\n\n")
				if targetLen < 0 {
					targetLen = 0
				}
				safeLen := findCharBoundary(c.thinkingBuffer, targetLen)
				if safeLen > 0 {
					safeContent := c.thinkingBuffer[:safeLen]
					if safeContent != "" && c.thinkingBlockIndex != nil {
						events = append(events, c.createThinkingDeltaEvent(*c.thinkingBlockIndex, safeContent))
					}
					c.thinkingBuffer = c.thinkingBuffer[safeLen:]
				}
				break
			}
		} else {
			// thinking 已提取完成
			if c.thinkingBuffer != "" {
				remaining := c.thinkingBuffer
				c.thinkingBuffer = ""
				events = append(events, c.createTextDeltaEvents(remaining)...)
			}
			break
		}
	}

	return events
}

// createTextDeltaEvents 创建 text_delta 事件（自动创建/恢复 text block）
func (c *StreamContext) createTextDeltaEvents(text string) []*SseEvent {
	var events []*SseEvent

	// 如果 text block 已被关闭，清除索引以创建新的
	if c.textBlockIndex != nil && !c.stateManager.IsBlockOpenOfType(*c.textBlockIndex, "text") {
		c.textBlockIndex = nil
	}

	// 获取或创建文本块
	var textIdx int
	if c.textBlockIndex != nil {
		textIdx = *c.textBlockIndex
	} else {
		idx := c.stateManager.NextBlockIndex()
		c.textBlockIndex = &idx
		textIdx = idx
		blockEvents := c.stateManager.HandleContentBlockStart(idx, "text", map[string]any{
			"type":  "content_block_start",
			"index": idx,
			"content_block": map[string]any{
				"type": "text",
				"text": "",
			},
		})
		events = append(events, blockEvents...)
	}

	if ev := c.stateManager.HandleContentBlockDelta(textIdx, map[string]any{
		"type":  "content_block_delta",
		"index": textIdx,
		"delta": map[string]any{
			"type": "text_delta",
			"text": text,
		},
	}); ev != nil {
		events = append(events, ev)
	}

	return events
}

func (c *StreamContext) createThinkingDeltaEvent(index int, thinking string) *SseEvent {
	return NewSseEvent("content_block_delta", map[string]any{
		"type":  "content_block_delta",
		"index": index,
		"delta": map[string]any{
			"type":     "thinking_delta",
			"thinking": thinking,
		},
	})
}

func (c *StreamContext) processToolUse(event *Event) []*SseEvent {
	var events []*SseEvent

	c.stateManager.SetHasToolUse(true)

	// tool_use 前 flush thinking buffer（边界场景）
	if c.thinkingEnabled && c.inThinkingBlock {
		if endPos := findRealThinkingEndTagAtBufferEnd(c.thinkingBuffer); endPos >= 0 {
			thinkingContent := c.thinkingBuffer[:endPos]
			if thinkingContent != "" && c.thinkingBlockIndex != nil {
				events = append(events, c.createThinkingDeltaEvent(*c.thinkingBlockIndex, thinkingContent))
			}

			c.inThinkingBlock = false
			c.thinkingExtracted = true

			if c.thinkingBlockIndex != nil {
				events = append(events, c.createThinkingDeltaEvent(*c.thinkingBlockIndex, ""))
				if ev := c.stateManager.HandleContentBlockStop(*c.thinkingBlockIndex); ev != nil {
					events = append(events, ev)
				}
			}

			afterPos := endPos + len(thinkingEndTag)
			remaining := strings.TrimLeft(c.thinkingBuffer[afterPos:], " \t\n\r")
			c.thinkingBuffer = ""
			if remaining != "" {
				events = append(events, c.createTextDeltaEvents(remaining)...)
			}
		}
	}

	// flush 暂存的 thinking buffer 文本（尚未进入 thinking block 的情况）
	if c.thinkingEnabled && !c.inThinkingBlock && !c.thinkingExtracted && c.thinkingBuffer != "" {
		buffered := c.thinkingBuffer
		c.thinkingBuffer = ""
		events = append(events, c.createTextDeltaEvents(buffered)...)
	}

	// 分配或获取 tool block 索引
	blockIndex, exists := c.toolBlockIndices[event.ToolUseID]
	if !exists {
		blockIndex = c.stateManager.NextBlockIndex()
		c.toolBlockIndices[event.ToolUseID] = blockIndex
	}

	// content_block_start
	startEvents := c.stateManager.HandleContentBlockStart(blockIndex, "tool_use", map[string]any{
		"type":  "content_block_start",
		"index": blockIndex,
		"content_block": map[string]any{
			"type":  "tool_use",
			"id":    event.ToolUseID,
			"name":  event.ToolName,
			"input": map[string]any{},
		},
	})
	events = append(events, startEvents...)

	// input_json_delta
	if event.ToolInput != "" {
		c.outputTokens += (len(event.ToolInput) + 3) / 4

		if ev := c.stateManager.HandleContentBlockDelta(blockIndex, map[string]any{
			"type":  "content_block_delta",
			"index": blockIndex,
			"delta": map[string]any{
				"type":         "input_json_delta",
				"partial_json": event.ToolInput,
			},
		}); ev != nil {
			events = append(events, ev)
		}
	}

	// content_block_stop
	if event.ToolStop {
		if ev := c.stateManager.HandleContentBlockStop(blockIndex); ev != nil {
			events = append(events, ev)
		}
	}

	return events
}

// estimateTokensForContent 简单 token 估算
func estimateTokensForContent(text string) int {
	chineseCount := 0
	otherCount := 0
	for _, r := range text {
		if r >= '\u4E00' && r <= '\u9FFF' {
			chineseCount++
		} else {
			otherCount++
		}
	}
	chineseTokens := (chineseCount*2 + 2) / 3
	otherTokens := (otherCount + 3) / 4
	result := chineseTokens + otherTokens
	if result < 1 {
		return 1
	}
	return result
}

// BufferedStreamContext 缓冲模式（/cc/v1）
type BufferedStreamContext struct {
	inner                  *StreamContext
	eventBuffer            []*SseEvent
	estimatedInputTokens   int
	initialEventsGenerated bool
}

// NewBufferedStreamContext 创建缓冲流上下文
func NewBufferedStreamContext(model string, estimatedInputTokens int, thinkingEnabled bool) *BufferedStreamContext {
	return &BufferedStreamContext{
		inner:                NewStreamContext(model, estimatedInputTokens, thinkingEnabled),
		estimatedInputTokens: estimatedInputTokens,
	}
}

// ProcessAndBuffer 处理 Kiro 事件并缓冲结果
func (bc *BufferedStreamContext) ProcessAndBuffer(event *Event) {
	if !bc.initialEventsGenerated {
		bc.eventBuffer = append(bc.eventBuffer, bc.inner.GenerateInitialEvents()...)
		bc.initialEventsGenerated = true
	}
	events := bc.inner.ProcessEvent(event)
	bc.eventBuffer = append(bc.eventBuffer, events...)
}

// FinishAndGetAllEvents 完成处理并返回所有事件（已校正 input_tokens）
func (bc *BufferedStreamContext) FinishAndGetAllEvents() []*SseEvent {
	if !bc.initialEventsGenerated {
		bc.eventBuffer = append(bc.eventBuffer, bc.inner.GenerateInitialEvents()...)
		bc.initialEventsGenerated = true
	}

	bc.eventBuffer = append(bc.eventBuffer, bc.inner.GenerateFinalEvents()...)

	// 校正 message_start 中的 input_tokens
	finalInputTokens := bc.estimatedInputTokens
	if bc.inner.contextInputTokens != nil {
		finalInputTokens = *bc.inner.contextInputTokens
	}

	for _, ev := range bc.eventBuffer {
		if ev.Event == "message_start" {
			if msg, ok := ev.Data["message"].(map[string]any); ok {
				if usage, ok := msg["usage"].(map[string]any); ok {
					usage["input_tokens"] = finalInputTokens
				}
			}
		}
	}

	result := bc.eventBuffer
	bc.eventBuffer = nil
	return result
}

// TokenUsage 返回当前 token 统计。
func (bc *BufferedStreamContext) TokenUsage() (int, int) {
	if bc == nil || bc.inner == nil {
		return 0, 0
	}
	return bc.inner.TokenUsage()
}
