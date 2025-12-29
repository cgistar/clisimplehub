package response

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"hash/crc32"

	kirotypes "clisimplehub/internal/transformer/kiro/types"
)

const (
	maxEventStreamBuffer = 4 * 1024 * 1024

	eventStreamPreludeSize  = 12
	eventStreamMinFrameSize = eventStreamPreludeSize + 4
	eventStreamMaxFrameSize = 16 * 1024 * 1024
)

// EventStreamParser parses AWS EventStream binary format from Kiro API
type EventStreamParser struct {
	buffer       []byte
	lastContent  string
	parseErrors  int                   // Track number of parse errors for monitoring
	droppedBytes int                   // Track dropped bytes for monitoring
	maxErrors    int                   // 最大错误次数限制（迁移自 kiro2api）
	toolManager  *ToolLifecycleManager // 工具生命周期管理器（迁移自 kiro2api）
}

// NewEventStreamParser creates a new EventStream parser
func NewEventStreamParser() *EventStreamParser {
	return &EventStreamParser{
		buffer:      make([]byte, 0, 64*1024),
		maxErrors:   100, // 默认最大错误次数
		toolManager: NewToolLifecycleManager(),
	}
}

// Feed processes incoming data and returns parsed events
func (p *EventStreamParser) Feed(chunk []byte) ([]*kirotypes.StreamEvent, error) {
	p.buffer = append(p.buffer, chunk...)

	// Prevent unbounded buffer growth
	if len(p.buffer) > maxEventStreamBuffer {
		dropped := len(p.buffer) / 2
		p.buffer = append([]byte(nil), p.buffer[dropped:]...)
		p.droppedBytes += dropped
	}

	var events []*kirotypes.StreamEvent
	for {
		framePayload, consumed, needMore, err := parseEventStreamFrame(p.buffer)
		if needMore {
			break
		}
		if err != nil {
			p.parseErrors++
			// Recover by skipping one byte to find the next boundary (Rust parity).
			if len(p.buffer) > 0 {
				p.droppedBytes++
				p.buffer = p.buffer[1:]
				continue
			}
			break
		}

		p.buffer = p.buffer[consumed:]

		if len(framePayload) == 0 {
			continue
		}
		event, err := p.parseEvent(framePayload)
		if err != nil {
			p.parseErrors++
			continue
		}
		if event != nil {
			events = append(events, event)
		}
	}

	return events, nil
}

// parseEventStreamFrame parses a single AWS EventStream frame and returns its JSON payload.
// It validates CRC32 (ISO-HDLC/IEEE) checksums to avoid mis-detecting random bytes as frames.
func parseEventStreamFrame(buf []byte) (payload []byte, consumed int, needMore bool, err error) {
	if len(buf) < eventStreamPreludeSize {
		return nil, 0, true, nil
	}

	totalLenU32 := binary.BigEndian.Uint32(buf[0:4])
	headerLenU32 := binary.BigEndian.Uint32(buf[4:8])
	preludeCRC := binary.BigEndian.Uint32(buf[8:12])

	if totalLenU32 < eventStreamMinFrameSize {
		return nil, 0, false, fmt.Errorf("eventstream frame too small: %d", totalLenU32)
	}
	if totalLenU32 > eventStreamMaxFrameSize {
		return nil, 0, false, fmt.Errorf("eventstream frame too large: %d", totalLenU32)
	}

	totalLen := int(totalLenU32)
	headerLen := int(headerLenU32)

	if len(buf) < totalLen {
		return nil, 0, true, nil
	}

	// Validate prelude CRC over first 8 bytes.
	if crc32.ChecksumIEEE(buf[0:8]) != preludeCRC {
		return nil, 0, false, fmt.Errorf("eventstream prelude CRC mismatch")
	}

	// Validate message CRC over entire frame excluding the last 4 bytes.
	messageCRC := binary.BigEndian.Uint32(buf[totalLen-4 : totalLen])
	if crc32.ChecksumIEEE(buf[0:totalLen-4]) != messageCRC {
		return nil, 0, false, fmt.Errorf("eventstream message CRC mismatch")
	}

	headersStart := eventStreamPreludeSize
	headersEnd := headersStart + headerLen
	if headersEnd > totalLen-4 {
		return nil, 0, false, fmt.Errorf("eventstream header length out of bounds: %d", headerLen)
	}

	// Best-effort header parse: we only need to skip them correctly.
	if _, err := parseEventStreamHeaders(buf[headersStart:headersEnd], headerLen); err != nil {
		return nil, 0, false, err
	}

	payloadStart := headersEnd
	payloadEnd := totalLen - 4
	if payloadStart > payloadEnd {
		return nil, 0, false, fmt.Errorf("eventstream payload bounds invalid")
	}

	return buf[payloadStart:payloadEnd], totalLen, false, nil
}

func parseEventStreamHeaders(data []byte, headerLen int) (map[string]any, error) {
	// We parse just enough to validate boundaries; callers don't currently need header values.
	if headerLen < 0 || len(data) < headerLen {
		return nil, fmt.Errorf("eventstream incomplete headers: need=%d have=%d", headerLen, len(data))
	}

	out := make(map[string]any)
	offset := 0
	for offset < headerLen {
		if offset >= len(data) {
			break
		}
		nameLen := int(data[offset])
		offset++
		if nameLen <= 0 {
			return nil, fmt.Errorf("eventstream invalid header name length")
		}
		if offset+nameLen > len(data) {
			return nil, fmt.Errorf("eventstream header name out of bounds")
		}
		name := string(data[offset : offset+nameLen])
		offset += nameLen
		if offset >= len(data) {
			return nil, fmt.Errorf("eventstream incomplete header value type")
		}
		valueType := data[offset]
		offset++

		switch valueType {
		case 0: // bool true
			out[name] = true
		case 1: // bool false
			out[name] = false
		case 2: // byte
			if offset+1 > len(data) {
				return nil, fmt.Errorf("eventstream incomplete byte header")
			}
			out[name] = int8(data[offset])
			offset++
		case 3: // short
			if offset+2 > len(data) {
				return nil, fmt.Errorf("eventstream incomplete short header")
			}
			out[name] = int16(binary.BigEndian.Uint16(data[offset : offset+2]))
			offset += 2
		case 4: // int
			if offset+4 > len(data) {
				return nil, fmt.Errorf("eventstream incomplete int header")
			}
			out[name] = int32(binary.BigEndian.Uint32(data[offset : offset+4]))
			offset += 4
		case 5: // long
			if offset+8 > len(data) {
				return nil, fmt.Errorf("eventstream incomplete long header")
			}
			out[name] = int64(binary.BigEndian.Uint64(data[offset : offset+8]))
			offset += 8
		case 6: // byte array
			if offset+2 > len(data) {
				return nil, fmt.Errorf("eventstream incomplete byte array header length")
			}
			l := int(binary.BigEndian.Uint16(data[offset : offset+2]))
			offset += 2
			if offset+l > len(data) {
				return nil, fmt.Errorf("eventstream incomplete byte array header value")
			}
			out[name] = append([]byte(nil), data[offset:offset+l]...)
			offset += l
		case 7: // string
			if offset+2 > len(data) {
				return nil, fmt.Errorf("eventstream incomplete string header length")
			}
			l := int(binary.BigEndian.Uint16(data[offset : offset+2]))
			offset += 2
			if offset+l > len(data) {
				return nil, fmt.Errorf("eventstream incomplete string header value")
			}
			out[name] = string(data[offset : offset+l])
			offset += l
		case 8: // timestamp (millis since epoch)
			if offset+8 > len(data) {
				return nil, fmt.Errorf("eventstream incomplete timestamp header")
			}
			out[name] = int64(binary.BigEndian.Uint64(data[offset : offset+8]))
			offset += 8
		case 9: // uuid (16 bytes)
			if offset+16 > len(data) {
				return nil, fmt.Errorf("eventstream incomplete uuid header")
			}
			out[name] = append([]byte(nil), data[offset:offset+16]...)
			offset += 16
		default:
			return nil, fmt.Errorf("eventstream invalid header type=%d", valueType)
		}
	}

	return out, nil
}

// parseEvent 把 JSON payload 解析为统一事件结构（定义在 `kiro/types`）。
func (p *EventStreamParser) parseEvent(jsonData []byte) (*kirotypes.StreamEvent, error) {
	var data map[string]any
	if err := json.Unmarshal(jsonData, &data); err != nil {
		return nil, fmt.Errorf("failed to unmarshal JSON: %w", err)
	}

	// 部分网关会把 payload 包一层：{"assistantResponseEvent": {...}}（见文档）。
	// 增强：更完善的旧格式处理（迁移自 kiro2api）
	if inner, ok := data["assistantResponseEvent"].(map[string]any); ok && inner != nil {
		// 检查是否为流式响应（有 content 但无 toolUses）
		if content, hasContent := inner["content"].(string); hasContent {
			if _, hasToolUses := inner["toolUses"]; !hasToolUses {
				// 流式文本响应
				if content != p.lastContent {
					p.lastContent = content
					return &kirotypes.StreamEvent{Type: kirotypes.StreamEventContent, Data: content}, nil
				}
				return nil, nil
			}
		}
		data = inner
	}

	// 增强：识别 completionChunk/delta 事件类型（迁移自 kiro2api）
	// 流式补全块通常有 delta 字段
	if delta, ok := data["delta"].(map[string]any); ok {
		if text, ok := delta["text"].(string); ok && text != "" {
			if text != p.lastContent {
				p.lastContent = text
				return &kirotypes.StreamEvent{Type: kirotypes.StreamEventContent, Data: text}, nil
			}
		}
		// delta 中也可能有 tool 相关内容
		if toolUseID, ok := delta["toolUseId"].(string); ok {
			return &kirotypes.StreamEvent{
				Type: kirotypes.StreamEventToolInput,
				Data: map[string]any{
					"toolUseId": toolUseID,
					"input":     delta["input"],
				},
			}, nil
		}
		return nil, nil
	}

	// content（部分流直接把文本增量放在顶层 content 字段）
	if content, ok := data["content"].(string); ok {
		if content == p.lastContent {
			return nil, nil
		}
		p.lastContent = content
		return &kirotypes.StreamEvent{Type: kirotypes.StreamEventContent, Data: content}, nil
	}

	// assistantResponseMessage：非流式/聚合结构里也可能携带 content + toolUses
	// 增强：识别 completion 事件类型（迁移自 kiro2api）
	if arm, ok := data["assistantResponseMessage"].(map[string]any); ok {
		if content, ok := arm["content"].(string); ok {
			if content != p.lastContent {
				p.lastContent = content
				return &kirotypes.StreamEvent{Type: kirotypes.StreamEventContent, Data: content}, nil
			}
		}
		if toolUses, ok := arm["toolUses"].([]any); ok && len(toolUses) > 0 {
			return &kirotypes.StreamEvent{Type: kirotypes.StreamEventToolUses, Data: toolUses}, nil
		}
	}

	if fp, ok := data["followupPrompt"]; ok && fp != nil {
		return nil, nil
	}

	toolUseID, hasToolUseID := data["toolUseId"].(string)
	name, hasName := data["name"].(string)
	stop, hasStop := data["stop"].(bool)

	// 验证 tool_use_id 完整性（迁移自 kiro2api）
	if hasToolUseID && toolUseID != "" && !IsValidToolUseID(toolUseID) {
		// 记录但不阻止处理，保持容错性
		p.parseErrors++
	}

	// tool_input：Kiro toolUseEvent 可能把 input 分片（字符串/对象）放在 input 字段里
	if input, ok := data["input"]; ok {
		if hasToolUseID {
			// 使用 toolManager 追加输入（迁移自 kiro2api）
			if p.toolManager != nil {
				if inputStr, ok := input.(string); ok {
					p.toolManager.AppendToolInput(toolUseID, inputStr)
				}
			}

			payload := map[string]any{
				"input":     input,
				"toolUseId": toolUseID,
			}
			if hasName {
				payload["name"] = name
			}
			if hasStop && stop {
				payload["stop"] = true
				// 完成工具调用（迁移自 kiro2api）
				if p.toolManager != nil {
					p.toolManager.FinalizeToolCall(toolUseID)
				}
			}
			return &kirotypes.StreamEvent{Type: kirotypes.StreamEventToolInput, Data: payload}, nil
		}
		return &kirotypes.StreamEvent{Type: kirotypes.StreamEventToolInput, Data: input}, nil
	}

	// tool stop：可能是独立对象，或 {"name":...,"toolUseId":...,"stop":true}（无 input）
	if hasStop && stop {
		if hasToolUseID {
			// 完成工具调用（迁移自 kiro2api）
			if p.toolManager != nil {
				p.toolManager.FinalizeToolCall(toolUseID)
			}

			payload := map[string]any{
				"toolUseId": toolUseID,
				"stop":      true,
			}
			if hasName {
				payload["name"] = name
			}
			return &kirotypes.StreamEvent{Type: kirotypes.StreamEventToolInput, Data: payload}, nil
		}
		return &kirotypes.StreamEvent{
			Type: kirotypes.StreamEventToolStop,
			Data: map[string]any{
				"name":      name,
				"toolUseId": toolUseID,
			},
		}, nil
	}

	// tool_start：name + toolUseId 同时存在
	if hasName && hasToolUseID {
		// 使用 toolManager 追踪工具调用（迁移自 kiro2api）
		if p.toolManager != nil {
			p.toolManager.StartToolCall(toolUseID, name)
		}

		return &kirotypes.StreamEvent{
			Type: kirotypes.StreamEventToolStart,
			Data: map[string]any{
				"name":      name,
				"toolUseId": toolUseID,
			},
		}, nil
	}

	// usage（两种字段格式都兼容）
	if usage, ok := data["usage"].(map[string]any); ok {
		return &kirotypes.StreamEvent{Type: kirotypes.StreamEventUsage, Data: usage}, nil
	}
	if meteringData, ok := data["meteringData"].(map[string]any); ok {
		return &kirotypes.StreamEvent{Type: kirotypes.StreamEventUsage, Data: meteringData}, nil
	}

	// context_usage
	if contextUsage, ok := data["contextUsagePercentage"]; ok {
		return &kirotypes.StreamEvent{Type: kirotypes.StreamEventContextUsage, Data: contextUsage}, nil
	}

	// error
	if errMsg, ok := data["error"].(string); ok {
		return &kirotypes.StreamEvent{Type: kirotypes.StreamEventError, Data: errMsg}, nil
	}
	if errObj, ok := data["error"].(map[string]any); ok {
		return &kirotypes.StreamEvent{Type: kirotypes.StreamEventError, Data: errObj}, nil
	}

	// stop_reason
	if stopReason, ok := data["stop_reason"].(string); ok {
		return &kirotypes.StreamEvent{Type: kirotypes.StreamEventStopReason, Data: stopReason}, nil
	}
	if stopReason, ok := data["stopReason"].(string); ok {
		return &kirotypes.StreamEvent{Type: kirotypes.StreamEventStopReason, Data: stopReason}, nil
	}

	return nil, nil
}

// Reset clears the parser state
func (p *EventStreamParser) Reset() {
	p.buffer = p.buffer[:0]
	p.lastContent = ""
	p.parseErrors = 0
	p.droppedBytes = 0
	if p.toolManager != nil {
		p.toolManager.Reset()
	}
}

// GetBuffer returns the current buffer (for debugging)
func (p *EventStreamParser) GetBuffer() []byte {
	return p.buffer
}

// GetParseErrors returns the number of parse errors encountered
func (p *EventStreamParser) GetParseErrors() int {
	return p.parseErrors
}

// GetDroppedBytes returns the number of bytes dropped due to buffer overflow or malformed data
func (p *EventStreamParser) GetDroppedBytes() int {
	return p.droppedBytes
}

// SetMaxErrors 设置最大错误次数（迁移自 kiro2api）
func (p *EventStreamParser) SetMaxErrors(maxErrors int) {
	p.maxErrors = maxErrors
}

// GetToolManager 获取工具生命周期管理器（迁移自 kiro2api）
func (p *EventStreamParser) GetToolManager() *ToolLifecycleManager {
	return p.toolManager
}

// IsValidToolUseID 验证 tool_use_id 格式是否有效（迁移自 kiro2api）
// 有效格式：tooluse_ 前缀 + 20-50字符的 base64 编码 ID
func IsValidToolUseID(toolUseID string) bool {
	if len(toolUseID) < 20 || len(toolUseID) > 50 {
		return false
	}
	if len(toolUseID) < 8 || toolUseID[:8] != "tooluse_" {
		return false
	}

	// 字符有效性检查（base64字符 + 下划线和连字符）
	suffix := toolUseID[8:]
	for _, char := range suffix {
		if !((char >= 'a' && char <= 'z') ||
			(char >= 'A' && char <= 'Z') ||
			(char >= '0' && char <= '9') ||
			char == '_' || char == '-') {
			return false
		}
	}

	// 检查是否包含明显的损坏模式
	if len(toolUseID) >= 13 && toolUseID[:13] == "tooluluse_" {
		return false
	}

	return true
}
