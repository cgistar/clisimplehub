package converters

import (
	"encoding/json"
	"fmt"
	"strings"
)

// AnthropicToKiro converts an Anthropic Messages API request to Kiro API payload.
func AnthropicToKiro(req *AnthropicRequest, conversationID, profileArn string) (*KiroPayload, error) {
	unifiedMessages := convertAnthropicMessages(req.Messages)
	unifiedTools := convertAnthropicTools(req.Tools)
	systemPrompt := extractSystemPrompt(req.System)
	modelID := NormalizeModelID(req.Model)

	result, err := BuildKiroPayload(
		unifiedMessages,
		systemPrompt,
		modelID,
		unifiedTools,
		conversationID,
		profileArn,
		req.Thinking,
		req.OutputConfig,
	)
	if err != nil {
		return nil, err
	}
	return result.Payload, nil
}

// extractSystemPrompt extracts system prompt text from Anthropic system field.
// Supports string or list of content blocks (for prompt caching).
func extractSystemPrompt(system json.RawMessage) string {
	if len(system) == 0 {
		return ""
	}

	// Try string
	var s string
	if err := json.Unmarshal(system, &s); err == nil {
		return s
	}

	// Try list of content blocks
	var blocks []map[string]any
	if err := json.Unmarshal(system, &blocks); err == nil {
		var parts []string
		for _, block := range blocks {
			t, _ := block["type"].(string)
			if t == "text" {
				if text, ok := block["text"].(string); ok {
					parts = append(parts, text)
				}
			}
		}
		return strings.Join(parts, "\n")
	}

	return string(system)
}

// convertAnthropicContentToText extracts text from Anthropic message content.
// For assistant messages, it also extracts thinking blocks and formats them.
func convertAnthropicContentToText(content json.RawMessage, role string) string {
	if len(content) == 0 {
		return ""
	}

	// Try string
	var s string
	if err := json.Unmarshal(content, &s); err == nil {
		return s
	}

	// Try list of content blocks
	var blocks []map[string]any
	if err := json.Unmarshal(content, &blocks); err == nil {
		var thinkingParts []string
		var textParts []string

		for _, block := range blocks {
			t, _ := block["type"].(string)
			switch t {
			case "thinking":
				if thinking, ok := block["thinking"].(string); ok {
					thinkingParts = append(thinkingParts, thinking)
				}
			case "text":
				if text, ok := block["text"].(string); ok {
					textParts = append(textParts, text)
				}
			}
		}

		// For assistant messages, format thinking blocks
		if role == "assistant" && len(thinkingParts) > 0 {
			thinkingContent := strings.Join(thinkingParts, "\n")
			// 对于 assistant 消息，直接连接 text blocks
			textContent := strings.Join(textParts, "")

			if textContent != "" {
				return fmt.Sprintf("<thinking>%s</thinking>\n\n%s", thinkingContent, textContent)
			}
			return fmt.Sprintf("<thinking>%s</thinking>", thinkingContent)
		}

		// 对于 user 消息，使用换行符连接
		return strings.Join(textParts, "\n")
	}

	return ""
}

// extractToolUsesFromAnthropicContent extracts tool_use blocks from assistant content.
func extractToolUsesFromAnthropicContent(content json.RawMessage) []ToolCall {
	var blocks []map[string]any
	if err := json.Unmarshal(content, &blocks); err != nil {
		return nil
	}

	var toolCalls []ToolCall
	for _, block := range blocks {
		t, _ := block["type"].(string)
		if t != "tool_use" {
			continue
		}
		id, _ := block["id"].(string)
		name, _ := block["name"].(string)
		input := block["input"]
		if id == "" || name == "" {
			continue
		}
		tc := ToolCall{
			ID:   id,
			Type: "function",
		}
		tc.Function.Name = name
		tc.Function.Arguments = input
		toolCalls = append(toolCalls, tc)
	}
	return toolCalls
}

// extractToolResultsFromAnthropicContent extracts tool_result blocks from user content.
func extractToolResultsFromAnthropicContent(content json.RawMessage) []ToolResultRef {
	var blocks []map[string]any
	if err := json.Unmarshal(content, &blocks); err != nil {
		return nil
	}

	var results []ToolResultRef
	for _, block := range blocks {
		t, _ := block["type"].(string)
		if t != "tool_result" {
			continue
		}
		toolUseID, _ := block["tool_use_id"].(string)
		if toolUseID == "" {
			continue
		}

		// 读取 is_error 字段
		isError := false
		if val, ok := block["is_error"].(bool); ok {
			isError = val
		}

		var resultContent string
		switch c := block["content"].(type) {
		case string:
			resultContent = c
		case []any:
			resultContent = extractTextFromSlice(c)
		default:
			if c != nil {
				resultContent = fmt.Sprintf("%v", c)
			}
		}

		results = append(results, ToolResultRef{
			Type:      "tool_result",
			ToolUseID: toolUseID,
			Content:   resultContent,
			IsError:   isError,
		})
	}
	return results
}

// extractImagesFromToolResults extracts images from tool_result content blocks.
func extractImagesFromToolResults(content json.RawMessage) []ImageRef {
	var blocks []map[string]any
	if err := json.Unmarshal(content, &blocks); err != nil {
		return nil
	}

	var images []ImageRef
	for _, block := range blocks {
		t, _ := block["type"].(string)
		if t != "tool_result" {
			continue
		}
		resultContent, ok := block["content"]
		if !ok {
			continue
		}
		resultArr, ok := resultContent.([]any)
		if !ok {
			continue
		}
		// Convert []any to []map[string]any for image extraction
		var items []map[string]any
		for _, item := range resultArr {
			if m, ok := item.(map[string]any); ok {
				items = append(items, m)
			}
		}
		raw, _ := json.Marshal(items)
		extracted := ExtractImagesFromContent(json.RawMessage(raw))
		images = append(images, extracted...)
	}
	return images
}

// convertAnthropicMessages converts Anthropic messages to unified format.
func convertAnthropicMessages(messages []AnthropicMessage) []UnifiedMessage {
	var unified []UnifiedMessage
	for _, msg := range messages {
		textContent := convertAnthropicContentToText(msg.Content, msg.Role)
		var toolCalls []ToolCall
		var toolResults []ToolResultRef
		var images []ImageRef

		switch msg.Role {
		case "assistant":
			toolCalls = extractToolUsesFromAnthropicContent(msg.Content)
			// If only tool_use and no text/thinking, add placeholder
			if len(toolCalls) > 0 && textContent == "" {
				textContent = " "
			}
		case "user":
			toolResults = extractToolResultsFromAnthropicContent(msg.Content)
			images = ExtractImagesFromContent(msg.Content)
			toolResultImages := extractImagesFromToolResults(msg.Content)
			images = append(images, toolResultImages...)
		}

		u := UnifiedMessage{
			Role:    msg.Role,
			Content: textContent,
		}
		if len(toolCalls) > 0 {
			u.ToolCalls = toolCalls
		}
		if len(toolResults) > 0 {
			u.ToolResults = toolResults
		}
		if len(images) > 0 {
			u.Images = images
		}
		unified = append(unified, u)
	}
	return unified
}

// convertAnthropicTools converts Anthropic tools to unified format.
func convertAnthropicTools(tools []AnthropicTool) []UnifiedTool {
	if len(tools) == 0 {
		return nil
	}
	var unified []UnifiedTool
	for _, tool := range tools {
		unified = append(unified, UnifiedTool{
			Name:        tool.Name,
			Description: tool.Description,
			InputSchema: tool.InputSchema,
		})
	}
	return unified
}
