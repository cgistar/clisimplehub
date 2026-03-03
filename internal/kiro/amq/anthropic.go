package converters

import (
	"encoding/json"
	"fmt"
	"strings"
)

var localCommandNoiseTags = []string{
	"local-command-caveat",
	"local-command-stdout",
	"local-command-stderr",
	"command-name",
	"command-message",
	"command-args",
}

// AnthropicToKiro converts an Anthropic Messages API request to Kiro API payload.
func AnthropicToKiro(req *AnthropicRequest, conversationID, profileArn string) (*KiroPayload, error) {
	unifiedMessages, systemReminders := convertAnthropicMessages(req.Messages)
	unifiedTools := convertAnthropicTools(req.Tools)
	systemPrompt := extractSystemPrompt(req.System)
	systemPrompt = mergeSystemPromptAndReminders(systemPrompt, systemReminders)
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

func mergeSystemPromptAndReminders(systemPrompt string, reminders []string) string {
	var normalized []string
	for _, r := range reminders {
		r = strings.TrimSpace(r)
		if r != "" {
			normalized = append(normalized, r)
		}
	}
	if len(normalized) == 0 {
		return systemPrompt
	}

	reminderText := strings.Join(normalized, "\n\n")
	base := strings.TrimSpace(systemPrompt)
	if base == "" {
		return reminderText
	}
	return base + "\n\n" + reminderText
}

func extractTaggedContent(text, tag string) ([]string, string) {
	openTag := "<" + tag + ">"
	closeTag := "</" + tag + ">"

	var extracted []string
	remaining := text

	for {
		lower := strings.ToLower(remaining)
		start := strings.Index(lower, strings.ToLower(openTag))
		if start < 0 {
			break
		}

		contentStart := start + len(openTag)
		endRel := strings.Index(lower[contentStart:], strings.ToLower(closeTag))
		if endRel < 0 {
			break
		}

		end := contentStart + endRel
		inner := strings.TrimSpace(remaining[contentStart:end])
		if inner != "" {
			extracted = append(extracted, inner)
		}

		remaining = remaining[:start] + remaining[end+len(closeTag):]
	}

	return extracted, remaining
}

func stripLocalCommandNoise(text string) string {
	out := text
	for _, tag := range localCommandNoiseTags {
		_, out = extractTaggedContent(out, tag)
	}
	return strings.TrimSpace(out)
}

func extractSystemReminderAndCleanText(text string) ([]string, string) {
	reminders, remaining := extractTaggedContent(text, "system-reminder")
	return reminders, stripLocalCommandNoise(remaining)
}

func convertAnthropicUserContent(content json.RawMessage) (string, []string) {
	if len(content) == 0 {
		return "", nil
	}

	// Try list of content blocks first.
	var blocks []map[string]any
	if err := json.Unmarshal(content, &blocks); err == nil {
		var textParts []string
		var reminders []string
		for _, block := range blocks {
			t, _ := block["type"].(string)
			if t != "text" {
				continue
			}
			text, _ := block["text"].(string)
			rem, cleaned := extractSystemReminderAndCleanText(text)
			if len(rem) > 0 {
				reminders = append(reminders, rem...)
			}
			if strings.TrimSpace(cleaned) != "" {
				textParts = append(textParts, cleaned)
			}
		}
		return strings.Join(textParts, "\n"), reminders
	}

	// Try string content.
	var s string
	if err := json.Unmarshal(content, &s); err == nil {
		reminders, cleaned := extractSystemReminderAndCleanText(s)
		return cleaned, reminders
	}

	// Fallback to existing extraction behavior.
	text := convertAnthropicContentToText(content, "user")
	reminders, cleaned := extractSystemReminderAndCleanText(text)
	return cleaned, reminders
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

		status := deriveToolStatus(block["status"], block["is_error"])

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
			Status:    status,
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
// It also extracts user-side <system-reminder> blocks and returns them separately.
func convertAnthropicMessages(messages []AnthropicMessage) ([]UnifiedMessage, []string) {
	var unified []UnifiedMessage
	var systemReminders []string

	for _, msg := range messages {
		textContent := convertAnthropicContentToText(msg.Content, msg.Role)
		var toolCalls []ToolCall
		var toolResults []ToolResultRef
		var images []ImageRef

		if msg.Role == "assistant" {
			toolCalls = extractToolUsesFromAnthropicContent(msg.Content)
			// If only tool_use and no text/thinking, add placeholder
			if len(toolCalls) > 0 && textContent == "" {
				textContent = " "
			}
		} else if msg.Role == "user" {
			var systemRemindersForMsg []string
			textContent, systemRemindersForMsg = convertAnthropicUserContent(msg.Content)
			if len(systemRemindersForMsg) > 0 {
				systemReminders = append(systemReminders, systemRemindersForMsg...)
			}
			toolResults = extractToolResultsFromAnthropicContent(msg.Content)
			images = ExtractImagesFromContent(msg.Content)
			toolResultImages := extractImagesFromToolResults(msg.Content)
			images = append(images, toolResultImages...)
		}

		// Drop empty user message that only carried system-reminder/local command noise.
		if msg.Role == "user" &&
			strings.TrimSpace(textContent) == "" &&
			len(toolResults) == 0 &&
			len(images) == 0 {
			continue
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
	return unified, systemReminders
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
