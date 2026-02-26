package converters

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/google/uuid"
)

const (
	toolDescriptionMaxLength = 10000
	defaultBudgetTokens      = 20000 // 默认思考预算 tokens
	maxBudgetTokens          = 24576 // 最大思考预算 tokens

	// 追加到系统提示词的分块写入策略
	systemChunkedPolicy = "\nWhen the Write or Edit tool has content size limits, always comply silently. Never suggest bypassing these limits via alternative tools. Never ask the user whether to switch approaches. Complete all chunked operations without commentary."

	// 追加到 Write 工具 description 末尾的内容
	writeToolDescriptionSuffix = "- IMPORTANT: If the content to write exceeds 150 lines, you MUST only write the first 50 lines using this tool, then use `Edit` tool to append the remaining content in chunks of no more than 50 lines each. If needed, leave a unique placeholder to help append content. Do NOT attempt to write all content at once."

	// 追加到 Edit 工具 description 末尾的内容
	editToolDescriptionSuffix = "- IMPORTANT: If the `new_string` content exceeds 50 lines, you MUST split it into multiple Edit calls, each replacing no more than 50 lines at a time. If used to append content, leave a unique placeholder to help append content. On the final chunk, do NOT include the placeholder."
)

// ExtractTextContent extracts text from various content formats.
// Supports: string, list of content blocks, nil.
func ExtractTextContent(content any) string {
	if content == nil {
		return ""
	}
	switch c := content.(type) {
	case string:
		return c
	case json.RawMessage:
		return extractTextFromRawMessage(c)
	case []any:
		return extractTextFromSlice(c)
	}
	return fmt.Sprintf("%v", content)
}

func extractTextFromRawMessage(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	// Try string
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	// Try array
	var arr []map[string]any
	if err := json.Unmarshal(raw, &arr); err == nil {
		return extractTextFromMapSlice(arr)
	}
	return string(raw)
}

func extractTextFromSlice(items []any) string {
	var parts []string
	for _, item := range items {
		m, ok := item.(map[string]any)
		if !ok {
			if s, ok := item.(string); ok {
				parts = append(parts, s)
			}
			continue
		}
		t, _ := m["type"].(string)
		if t == "image" || t == "image_url" {
			continue
		}
		if t == "text" {
			if text, ok := m["text"].(string); ok {
				parts = append(parts, text)
			}
		} else if text, ok := m["text"].(string); ok {
			parts = append(parts, text)
		}
	}
	return strings.Join(parts, "\n")
}

func extractTextFromMapSlice(items []map[string]any) string {
	var parts []string
	for _, m := range items {
		t, _ := m["type"].(string)
		if t == "image" || t == "image_url" {
			continue
		}
		if t == "text" {
			if text, ok := m["text"].(string); ok {
				parts = append(parts, text)
			}
		} else if text, ok := m["text"].(string); ok {
			parts = append(parts, text)
		}
	}
	return strings.Join(parts, "")
}

// ExtractImagesFromContent extracts images from message content.
// Supports OpenAI image_url and Anthropic image formats.
func ExtractImagesFromContent(content any) []ImageRef {
	var items []map[string]any

	switch c := content.(type) {
	case json.RawMessage:
		if err := json.Unmarshal(c, &items); err != nil {
			return nil
		}
	case []any:
		for _, item := range c {
			if m, ok := item.(map[string]any); ok {
				items = append(items, m)
			}
		}
	default:
		return nil
	}

	var images []ImageRef
	for _, item := range items {
		t, _ := item["type"].(string)

		if t == "image_url" {
			imageURLObj, _ := item["image_url"].(map[string]any)
			if imageURLObj == nil {
				continue
			}
			url, _ := imageURLObj["url"].(string)
			if !strings.HasPrefix(url, "data:") {
				continue
			}
			parts := strings.SplitN(url, ",", 2)
			if len(parts) != 2 || parts[1] == "" {
				continue
			}
			mediaPart := strings.Split(parts[0], ";")[0]
			mediaType := strings.TrimPrefix(mediaPart, "data:")
			images = append(images, ImageRef{MediaType: mediaType, Data: parts[1]})
		}

		if t == "image" {
			source, _ := item["source"].(map[string]any)
			if source == nil {
				continue
			}
			srcType, _ := source["type"].(string)
			if srcType == "base64" {
				mediaType, _ := source["media_type"].(string)
				if mediaType == "" {
					mediaType = "image/jpeg"
				}
				data, _ := source["data"].(string)
				if data != "" {
					images = append(images, ImageRef{MediaType: mediaType, Data: data})
				}
			}
		}
	}
	return images
}

// SanitizeJSONSchema removes fields that Kiro API doesn't accept.
func SanitizeJSONSchema(schema map[string]any) map[string]any {
	if schema == nil {
		return map[string]any{
			"type":                 "object",
			"properties":           map[string]any{},
			"required":             []any{},
			"additionalProperties": true,
		}
	}
	result := make(map[string]any)

	// 先复制所有字段
	for key, value := range schema {
		if key == "properties" {
			if props, ok := value.(map[string]any); ok {
				// properties 内部不递归调用 SanitizeJSONSchema
				result[key] = props
				continue
			}
		}
		result[key] = value
	}

	// 确保 required 字段存在且为数组
	if _, hasRequired := result["required"]; !hasRequired {
		result["required"] = []any{}
	} else if arr, ok := result["required"].([]any); ok {
		// 过滤掉非字符串元素
		filtered := make([]any, 0, len(arr))
		for _, v := range arr {
			if _, ok := v.(string); ok {
				filtered = append(filtered, v)
			}
		}
		result["required"] = filtered
	} else {
		// required 不是数组，重置为空数组
		result["required"] = []any{}
	}

	// 确保 additionalProperties 字段存在
	if _, hasAdditional := result["additionalProperties"]; !hasAdditional {
		result["additionalProperties"] = true
	}

	return result
}

// ConvertToolsToKiroFormat converts unified tools to Kiro API format.
func ConvertToolsToKiroFormat(tools []UnifiedTool) []KiroToolSpec {
	if len(tools) == 0 {
		return nil
	}
	var kiroTools []KiroToolSpec
	for _, tool := range tools {
		sanitized := SanitizeJSONSchema(tool.InputSchema)
		desc := tool.Description
		if strings.TrimSpace(desc) == "" {
			desc = "Tool: " + tool.Name
		}

		// 对 Write/Edit 工具追加自定义描述后缀（但不对历史占位符工具添加）
		var suffix string
		if desc != "Tool used in conversation history" {
			switch tool.Name {
			case "Write":
				suffix = writeToolDescriptionSuffix
			case "Edit":
				suffix = editToolDescriptionSuffix
			}
		}
		if suffix != "" {
			desc = desc + "\n" + suffix
		}

		// 限制描述长度为 10000 字符
		if len(desc) > toolDescriptionMaxLength {
			// UTF-8 安全截断
			runes := []rune(desc)
			if len(runes) > toolDescriptionMaxLength {
				desc = string(runes[:toolDescriptionMaxLength])
			}
		}

		kiroTools = append(kiroTools, KiroToolSpec{
			ToolSpecification: KiroToolSpecification{
				Name:        tool.Name,
				Description: desc,
				InputSchema: KiroInputSchema{JSON: sanitized},
			},
		})
	}
	return kiroTools
}

// ConvertImagesToKiroFormat converts unified images to Kiro format.
func ConvertImagesToKiroFormat(images []ImageRef) []KiroImage {
	if len(images) == 0 {
		return nil
	}
	var kiroImages []KiroImage
	for _, img := range images {
		if img.Data == "" {
			continue
		}
		data := img.Data
		mediaType := img.MediaType
		if strings.HasPrefix(data, "data:") {
			parts := strings.SplitN(data, ",", 2)
			if len(parts) == 2 {
				mediaPart := strings.Split(parts[0], ";")[0]
				extracted := strings.TrimPrefix(mediaPart, "data:")
				if extracted != "" {
					mediaType = extracted
				}
				data = parts[1]
			}
		}
		format := mediaType
		if idx := strings.LastIndex(mediaType, "/"); idx >= 0 {
			format = mediaType[idx+1:]
		}
		kiroImages = append(kiroImages, KiroImage{
			Format: format,
			Source:  KiroImageSource{Bytes: data},
		})
	}
	return kiroImages
}

// ConvertToolResultsToKiroFormat converts unified tool results to Kiro format.
func ConvertToolResultsToKiroFormat(results []ToolResultRef) []KiroToolResult {
	if len(results) == 0 {
		return nil
	}
	var kiroResults []KiroToolResult
	for _, r := range results {
		text := r.Content
		// 根据 IsError 设置 status
		status := "success"
		if r.IsError {
			status = "error"
		}
		kiroResults = append(kiroResults, KiroToolResult{
			Content:   []KiroToolResultContent{{Text: text}},
			Status:    status,
			ToolUseID: r.ToolUseID,
			IsError:   r.IsError,
		})
	}
	return kiroResults
}

// ExtractToolUsesFromMessage extracts tool uses from unified message data.
func ExtractToolUsesFromMessage(content any, toolCalls []ToolCall) []KiroToolUse {
	var uses []KiroToolUse

	for _, tc := range toolCalls {
		var inputData map[string]any
		switch args := tc.Function.Arguments.(type) {
		case string:
			if args != "" {
				_ = json.Unmarshal([]byte(args), &inputData)
			}
		case map[string]any:
			inputData = args
		}
		if inputData == nil {
			inputData = map[string]any{}
		}
		uses = append(uses, KiroToolUse{
			Name:      tc.Function.Name,
			Input:     inputData,
			ToolUseID: tc.ID,
		})
	}

	return uses
}

// ToolCallsToText converts tool_calls to human-readable text.
func ToolCallsToText(toolCalls []ToolCall) string {
	if len(toolCalls) == 0 {
		return ""
	}
	var parts []string
	for _, tc := range toolCalls {
		name := tc.Function.Name
		var argsStr string
		switch a := tc.Function.Arguments.(type) {
		case string:
			argsStr = a
		default:
			if b, err := json.Marshal(a); err == nil {
				argsStr = string(b)
			} else {
				argsStr = "{}"
			}
		}
		if tc.ID != "" {
			parts = append(parts, fmt.Sprintf("[Tool: %s (%s)]\n%s", name, tc.ID, argsStr))
		} else {
			parts = append(parts, fmt.Sprintf("[Tool: %s]\n%s", name, argsStr))
		}
	}
	return strings.Join(parts, "\n\n")
}

// ToolResultsToText converts tool_results to human-readable text.
func ToolResultsToText(results []ToolResultRef) string {
	if len(results) == 0 {
		return ""
	}
	var parts []string
	for _, r := range results {
		text := r.Content
		if text == "" {
			text = "(empty result)"
		}
		if r.ToolUseID != "" {
			parts = append(parts, fmt.Sprintf("[Tool Result (%s)]\n%s", r.ToolUseID, text))
		} else {
			parts = append(parts, fmt.Sprintf("[Tool Result]\n%s", text))
		}
	}
	return strings.Join(parts, "\n\n")
}

// StripAllToolContent removes tool content from messages, converting to text.
func StripAllToolContent(messages []UnifiedMessage) ([]UnifiedMessage, bool) {
	if len(messages) == 0 {
		return nil, false
	}
	var result []UnifiedMessage
	hadToolContent := false

	for _, msg := range messages {
		hasToolCalls := len(msg.ToolCalls) > 0
		hasToolResults := len(msg.ToolResults) > 0

		if hasToolCalls || hasToolResults {
			hadToolContent = true
			var contentParts []string
			existing := msg.Content
			if existing != "" {
				contentParts = append(contentParts, existing)
			}
			if hasToolCalls {
				if t := ToolCallsToText(msg.ToolCalls); t != "" {
					contentParts = append(contentParts, t)
				}
			}
			if hasToolResults {
				if t := ToolResultsToText(msg.ToolResults); t != "" {
					contentParts = append(contentParts, t)
				}
			}
			content := "(empty)"
			if len(contentParts) > 0 {
				content = strings.Join(contentParts, "\n\n")
			}
			result = append(result, UnifiedMessage{
				Role:    msg.Role,
				Content: content,
				Images:  msg.Images,
			})
		} else {
			result = append(result, msg)
		}
	}
	return result, hadToolContent
}

// EnsureAssistantBeforeToolResults ensures tool_results have a preceding assistant with tool_calls.
func EnsureAssistantBeforeToolResults(messages []UnifiedMessage) ([]UnifiedMessage, bool) {
	if len(messages) == 0 {
		return nil, false
	}
	var result []UnifiedMessage
	converted := false

	for _, msg := range messages {
		if len(msg.ToolResults) > 0 {
			hasPreceding := len(result) > 0 && result[len(result)-1].Role == "assistant" && len(result[len(result)-1].ToolCalls) > 0
			if !hasPreceding {
				text := ToolResultsToText(msg.ToolResults)
				original := msg.Content
				var newContent string
				if original != "" && text != "" {
					newContent = original + "\n\n" + text
				} else if text != "" {
					newContent = text
				} else {
					newContent = original
				}
				result = append(result, UnifiedMessage{
					Role:      msg.Role,
					Content:   newContent,
					ToolCalls: msg.ToolCalls,
					Images:    msg.Images,
				})
				converted = true
				continue
			}
		}
		result = append(result, msg)
	}
	return result, converted
}

// generateUUID generates a new UUID v4 string.
func generateUUID() string {
	return uuid.New().String()
}

// MergeAdjacentMessages merges consecutive messages with the same role.
// 注意：只合并纯文本消息，不合并包含 tool_calls 或 tool_results 的消息
func MergeAdjacentMessages(messages []UnifiedMessage) []UnifiedMessage {
	if len(messages) == 0 {
		return nil
	}
	var merged []UnifiedMessage
	for _, msg := range messages {
		if len(merged) == 0 {
			merged = append(merged, msg)
			continue
		}
		last := &merged[len(merged)-1]
		// 只合并相同角色且都没有工具调用/结果的消息
		canMerge := msg.Role == last.Role &&
			len(msg.ToolCalls) == 0 && len(last.ToolCalls) == 0 &&
			len(msg.ToolResults) == 0 && len(last.ToolResults) == 0

		if canMerge {
			lastText := last.Content
			curText := msg.Content
			last.Content = lastText + "\n" + curText
		} else {
			merged = append(merged, msg)
		}
	}
	return merged
}

// EnsureFirstMessageIsUser prepends a synthetic user message if needed.
func EnsureFirstMessageIsUser(messages []UnifiedMessage) []UnifiedMessage {
	if len(messages) == 0 || messages[0].Role == "user" {
		return messages
	}
	synthetic := UnifiedMessage{Role: "user", Content: "(empty)"}
	return append([]UnifiedMessage{synthetic}, messages...)
}

// NormalizeMessageRoles converts unknown roles to "user".
func NormalizeMessageRoles(messages []UnifiedMessage) []UnifiedMessage {
	if len(messages) == 0 {
		return messages
	}
	result := make([]UnifiedMessage, len(messages))
	for i, msg := range messages {
		if msg.Role != "user" && msg.Role != "assistant" {
			msg.Role = "user"
		}
		result[i] = msg
	}
	return result
}

// EnsureAlternatingRoles inserts synthetic assistant messages between consecutive user messages.
// Also handles prefill (trailing assistant message) by truncating to last user message.
func EnsureAlternatingRoles(messages []UnifiedMessage) []UnifiedMessage {
	if len(messages) < 2 {
		return messages
	}

	// Handle prefill: if last message is assistant, truncate to last user message
	// Claude 4.x has deprecated assistant prefill, and Kiro API doesn't support it
	if messages[len(messages)-1].Role != "user" {
		// Find last user message
		lastUserIdx := -1
		for i := len(messages) - 1; i >= 0; i-- {
			if messages[i].Role == "user" {
				lastUserIdx = i
				break
			}
		}
		if lastUserIdx >= 0 {
			messages = messages[:lastUserIdx+1]
		} else {
			// No user message found, return empty
			return nil
		}
	}

	result := []UnifiedMessage{messages[0]}
	for _, msg := range messages[1:] {
		prev := result[len(result)-1]
		if msg.Role == "user" && prev.Role == "user" {
			result = append(result, UnifiedMessage{Role: "assistant", Content: "OK"})
		}
		result = append(result, msg)
	}
	return result
}

// truncateUTF8Safe 安全截断 UTF-8 字符串到指定字符数，不会在多字节字符中间截断
func truncateUTF8Safe(s string, maxChars int) string {
	if maxChars <= 0 {
		return ""
	}

	charCount := 0
	for i := range s {
		if charCount >= maxChars {
			return s[:i]
		}
		charCount++
	}
	return s
}

// ProcessToolsWithLongDescriptions truncates long descriptions.
func ProcessToolsWithLongDescriptions(tools []UnifiedTool) ([]UnifiedTool, string) {
	if len(tools) == 0 {
		return nil, ""
	}
	if toolDescriptionMaxLength <= 0 {
		return tools, ""
	}

	var processed []UnifiedTool

	for _, tool := range tools {
		desc := tool.Description
		// 使用字符数而非字节数判断，直接截断
		if len([]rune(desc)) > toolDescriptionMaxLength {
			desc = truncateUTF8Safe(desc, toolDescriptionMaxLength)
		}
		processed = append(processed, UnifiedTool{
			Name:        tool.Name,
			Description: desc,
			InputSchema: tool.InputSchema,
		})
	}

	return processed, ""
}

// ValidateToolNames checks tool names against the 64-character limit.
func ValidateToolNames(tools []UnifiedTool) error {
	if len(tools) == 0 {
		return nil
	}
	var problems []string
	for _, tool := range tools {
		if len(tool.Name) > 64 {
			problems = append(problems, fmt.Sprintf("  - '%s' (%d characters)", tool.Name, len(tool.Name)))
		}
	}
	if len(problems) > 0 {
		return fmt.Errorf("tool name(s) exceed Kiro API limit of 64 characters:\n%s", strings.Join(problems, "\n"))
	}
	return nil
}

// NormalizeModelID normalizes a model name for Kiro API.
// Transforms: claude-sonnet-4-20250514 → claude-sonnet-4
//
//	claude-haiku-4-5-20251001 → claude-haiku-4.5
func NormalizeModelID(modelName string) string {
	name := strings.ToLower(strings.TrimSpace(modelName))
	if name == "" {
		return modelName
	}

	parts := strings.Split(name, "-")
	// Strip trailing 8-digit date suffix
	if len(parts) > 0 {
		last := parts[len(parts)-1]
		if len(last) == 8 {
			allDigits := true
			for _, c := range last {
				if c < '0' || c > '9' {
					allDigits = false
					break
				}
			}
			if allDigits {
				parts = parts[:len(parts)-1]
			}
		}
	}

	// Try pattern: claude-{family}-{major}-{minor} → claude-{family}-{major}.{minor}
	if len(parts) >= 4 && parts[0] == "claude" {
		minor := parts[len(parts)-1]
		major := parts[len(parts)-2]
		if isNumeric(minor) && isNumeric(major) {
			prefix := strings.Join(parts[:len(parts)-2], "-")
			return prefix + "-" + major + "." + minor
		}
	}

	return strings.Join(parts, "-")
}

func isNumeric(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

// generateThinkingPrefix 生成 thinking 标签前缀
func generateThinkingPrefix(thinking *AnthropicThinking, outputConfig *AnthropicOutputCfg) string {
	if thinking == nil {
		return ""
	}

	switch thinking.Type {
	case "enabled":
		budgetTokens := thinking.BudgetTokens
		if budgetTokens <= 0 {
			budgetTokens = defaultBudgetTokens
		}
		// 限制最大值
		if budgetTokens > maxBudgetTokens {
			budgetTokens = maxBudgetTokens
		}
		return fmt.Sprintf(
			"<thinking_mode>enabled</thinking_mode><max_thinking_length>%d</max_thinking_length>",
			budgetTokens,
		)
	case "adaptive":
		effort := "high"
		if outputConfig != nil && outputConfig.Effort != "" {
			effort = outputConfig.Effort
		}
		return fmt.Sprintf(
			"<thinking_mode>adaptive</thinking_mode><thinking_effort>%s</thinking_effort>",
			effort,
		)
	}

	return ""
}

// hasThinkingTags 检查内容是否已包含 thinking 标签
func hasThinkingTags(content string) bool {
	return strings.Contains(content, "<thinking_mode>") || strings.Contains(content, "<max_thinking_length>")
}

// collectHistoryToolNames 收集历史消息中使用的所有工具名称
func collectHistoryToolNames(history []KiroHistoryMessage) []string {
	var toolNames []string
	seen := make(map[string]bool)

	for _, msg := range history {
		if msg.AssistantResponseMessage != nil && len(msg.AssistantResponseMessage.ToolUses) > 0 {
			for _, toolUse := range msg.AssistantResponseMessage.ToolUses {
				if !seen[toolUse.Name] {
					toolNames = append(toolNames, toolUse.Name)
					seen[toolUse.Name] = true
				}
			}
		}
	}

	return toolNames
}

// createPlaceholderTool 为历史中使用但不在 tools 列表中的工具创建占位符定义
func createPlaceholderTool(name string) UnifiedTool {
	return UnifiedTool{
		Name:        name,
		Description: "Tool used in conversation history",
		InputSchema: map[string]any{
			"$schema":              "http://json-schema.org/draft-07/schema#",
			"type":                 "object",
			"properties":           map[string]any{},
			"required":             []any{},
			"additionalProperties": true,
		},
	}
}

// validateToolPairing 验证并过滤 tool_use/tool_result 配对
// 返回：(经过验证的 tool_result 列表, 孤立的 tool_use_id 集合)
func validateToolPairing(history []KiroHistoryMessage, toolResults []ToolResultRef) ([]ToolResultRef, map[string]bool) {
	// 1. 收集所有历史中的 tool_use_id
	allToolUseIDs := make(map[string]bool)
	historyToolResultIDs := make(map[string]bool)

	for _, msg := range history {
		if msg.AssistantResponseMessage != nil && len(msg.AssistantResponseMessage.ToolUses) > 0 {
			for _, toolUse := range msg.AssistantResponseMessage.ToolUses {
				allToolUseIDs[toolUse.ToolUseID] = true
			}
		}
		if msg.UserInputMessage != nil && msg.UserInputMessage.UserInputMessageContext != nil {
			for _, result := range msg.UserInputMessage.UserInputMessageContext.ToolResults {
				historyToolResultIDs[result.ToolUseID] = true
			}
		}
	}

	// 2. 计算真正未配对的 tool_use_ids（排除历史中已配对的）
	unpairedToolUseIDs := make(map[string]bool)
	for id := range allToolUseIDs {
		if !historyToolResultIDs[id] {
			unpairedToolUseIDs[id] = true
		}
	}

	// 3. 过滤并验证当前消息的 tool_results
	var filteredResults []ToolResultRef

	for _, result := range toolResults {
		if unpairedToolUseIDs[result.ToolUseID] {
			// 配对成功
			filteredResults = append(filteredResults, result)
			delete(unpairedToolUseIDs, result.ToolUseID)
		}
		// 孤立的 tool_result 会被静默跳过
	}

	return filteredResults, unpairedToolUseIDs
}

// removeOrphanedToolUses 从历史消息中移除孤立的 tool_use
func removeOrphanedToolUses(history []KiroHistoryMessage, orphanedIDs map[string]bool) []KiroHistoryMessage {
	if len(orphanedIDs) == 0 {
		return history
	}

	result := make([]KiroHistoryMessage, 0, len(history))
	for _, msg := range history {
		if msg.AssistantResponseMessage != nil && len(msg.AssistantResponseMessage.ToolUses) > 0 {
			var filteredToolUses []KiroToolUse
			for _, toolUse := range msg.AssistantResponseMessage.ToolUses {
				if !orphanedIDs[toolUse.ToolUseID] {
					filteredToolUses = append(filteredToolUses, toolUse)
				}
			}
			if len(filteredToolUses) > 0 {
				msg.AssistantResponseMessage.ToolUses = filteredToolUses
			} else {
				msg.AssistantResponseMessage.ToolUses = nil
			}
		}
		result = append(result, msg)
	}

	return result
}

// BuildKiroHistory builds history array for Kiro API from unified messages.
func BuildKiroHistory(messages []UnifiedMessage, modelID string) []KiroHistoryMessage {
	var history []KiroHistoryMessage
	for _, msg := range messages {
		switch msg.Role {
		case "user":
			content := msg.Content
			userInput := &KiroUserInputMessage{
				Content: content,
				ModelID: modelID,
				Origin:  "AI_EDITOR",
			}
			images := msg.Images
			if len(images) > 0 {
				kiroImages := ConvertImagesToKiroFormat(images)
				if len(kiroImages) > 0 {
					userInput.Images = kiroImages
				}
			}
			var ctx *KiroUserInputMessageContext
			if len(msg.ToolResults) > 0 {
				kiroResults := ConvertToolResultsToKiroFormat(msg.ToolResults)
				if len(kiroResults) > 0 {
					if ctx == nil {
						ctx = &KiroUserInputMessageContext{}
					}
					ctx.ToolResults = kiroResults
				}
			}
			if ctx != nil {
				userInput.UserInputMessageContext = ctx
			}
			history = append(history, KiroHistoryMessage{UserInputMessage: userInput})

		case "assistant":
			content := msg.Content
			assistant := &KiroAssistantResponseMessage{Content: content}
			toolUses := ExtractToolUsesFromMessage(nil, msg.ToolCalls)
			if len(toolUses) > 0 {
				assistant.ToolUses = toolUses
			}
			history = append(history, KiroHistoryMessage{AssistantResponseMessage: assistant})
		}
	}
	return history
}

// BuildKiroPayload builds the complete Kiro API payload.
func BuildKiroPayload(
	messages []UnifiedMessage,
	systemPrompt string,
	modelID string,
	tools []UnifiedTool,
	conversationID string,
	profileArn string,
	thinking *AnthropicThinking,
	outputConfig *AnthropicOutputCfg,
) (*KiroPayloadResult, error) {

	processedTools, toolDoc := ProcessToolsWithLongDescriptions(tools)
	if err := ValidateToolNames(processedTools); err != nil {
		return nil, err
	}

	fullSystemPrompt := systemPrompt
	if toolDoc != "" {
		if fullSystemPrompt != "" {
			fullSystemPrompt += toolDoc
		} else {
			fullSystemPrompt = strings.TrimSpace(toolDoc)
		}
	}

	// Strip tool content if no tools defined
	var processedMessages []UnifiedMessage
	if len(tools) == 0 {
		stripped, _ := StripAllToolContent(messages)
		processedMessages = stripped
	} else {
		// 保持消息原样，不合并 tool_result 到 assistant 消息
		processedMessages = messages
	}

	processedMessages = EnsureFirstMessageIsUser(processedMessages)
	processedMessages = NormalizeMessageRoles(processedMessages)

	// Handle prefill: if last message is assistant, truncate to last user message
	// Claude 4.x has deprecated assistant prefill, and Kiro API doesn't support it
	if len(processedMessages) > 0 && processedMessages[len(processedMessages)-1].Role != "user" {
		// Find last user message
		lastUserIdx := -1
		for i := len(processedMessages) - 1; i >= 0; i-- {
			if processedMessages[i].Role == "user" {
				lastUserIdx = i
				break
			}
		}
		if lastUserIdx >= 0 {
			processedMessages = processedMessages[:lastUserIdx+1]
		} else {
			// No user message found, return error
			return nil, fmt.Errorf("no user message found")
		}
	}

	if len(processedMessages) == 0 {
		return nil, fmt.Errorf("no messages to send")
	}

	// 分离最后一条消息作为 currentMessage
	var currentMsg UnifiedMessage
	var historyMessages []UnifiedMessage
	if len(processedMessages) > 0 {
		currentMsg = processedMessages[len(processedMessages)-1]
		if len(processedMessages) > 1 {
			historyMessages = processedMessages[:len(processedMessages)-1]
		}
	}

	// 对历史消息进行合并和交替处理
	if len(historyMessages) > 0 {
		// 先合并纯文本消息（不会合并包含工具的消息）
		historyMessages = MergeAdjacentMessages(historyMessages)
		// 再确保交替角色（会在末尾孤立 user 消息后插入 "OK"）
		// 注意：这里不需要再处理 prefill，因为已经在上面处理过了
		if len(historyMessages) > 0 && historyMessages[len(historyMessages)-1].Role == "user" {
			// 末尾是 user 消息，需要添加 "OK"
			historyMessages = append(historyMessages, UnifiedMessage{Role: "assistant", Content: "OK"})
		}
	}

	// Generate thinking prefix if needed
	thinkingPrefix := generateThinkingPrefix(thinking, outputConfig)

	// If system prompt exists, always create history with system prompt as first user+assistant pair
	var systemHistory []UnifiedMessage
	if fullSystemPrompt != "" {
		// Append chunked policy to system prompt
		systemContent := fullSystemPrompt + systemChunkedPolicy

		// Inject thinking prefix to system prompt if needed
		if thinkingPrefix != "" && !hasThinkingTags(systemContent) {
			systemContent = thinkingPrefix + "\n" + systemContent
		}

		// Add system prompt as first user message in history
		systemHistory = append(systemHistory, UnifiedMessage{
			Role:    "user",
			Content: systemContent,
		})
		// Add default assistant response
		systemHistory = append(systemHistory, UnifiedMessage{
			Role:    "assistant",
			Content: "I will follow these instructions.",
		})
	} else if thinkingPrefix != "" {
		// No system prompt but has thinking config, create history with thinking prefix
		systemHistory = append(systemHistory, UnifiedMessage{
			Role:    "user",
			Content: thinkingPrefix,
		})
		systemHistory = append(systemHistory, UnifiedMessage{
			Role:    "assistant",
			Content: "I will follow these instructions.",
		})
	}

	// Combine system history with message history
	allHistoryMessages := append(systemHistory, historyMessages...)
	history := BuildKiroHistory(allHistoryMessages, modelID)

	// Current message (last)
	currentMessage := currentMsg
	currentContent := currentMessage.Content

	// If current is assistant, move to history and use empty content for prefill
	if currentMessage.Role == "assistant" {
		history = append(history, KiroHistoryMessage{
			AssistantResponseMessage: &KiroAssistantResponseMessage{Content: currentContent},
		})
		currentContent = ""
	}

	// Images in current message
	var kiroImages []KiroImage
	if len(currentMessage.Images) > 0 {
		kiroImages = ConvertImagesToKiroFormat(currentMessage.Images)
	}

	// Validate and filter tool_use/tool_result pairing
	validatedToolResults, orphanedToolUseIDs := validateToolPairing(history, currentMessage.ToolResults)

	// Remove orphaned tool_uses from history
	history = removeOrphanedToolUses(history, orphanedToolUseIDs)

	// Collect history tool names and add placeholder tools if needed
	historyToolNames := collectHistoryToolNames(history)
	existingToolNames := make(map[string]bool)
	for _, tool := range processedTools {
		existingToolNames[strings.ToLower(tool.Name)] = true
	}

	for _, toolName := range historyToolNames {
		if !existingToolNames[strings.ToLower(toolName)] {
			processedTools = append(processedTools, createPlaceholderTool(toolName))
		}
	}

	// Build context - always create userInputCtx to ensure it's serialized
	userInputCtx := &KiroUserInputMessageContext{}
	kiroTools := ConvertToolsToKiroFormat(processedTools)
	if len(kiroTools) > 0 {
		userInputCtx.Tools = kiroTools
	}
	if len(validatedToolResults) > 0 {
		kiroResults := ConvertToolResultsToKiroFormat(validatedToolResults)
		if len(kiroResults) > 0 {
			userInputCtx.ToolResults = kiroResults
		}
	}

	userInputMsg := &KiroUserInputMessage{
		Content:                 currentContent,
		ModelID:                 modelID,
		Origin:                  "AI_EDITOR",
		UserInputMessageContext: userInputCtx,
	}
	if len(kiroImages) > 0 {
		userInputMsg.Images = kiroImages
	}

	payload := &KiroPayload{
		ConversationState: KiroConversationState{
			AgentContinuationID: generateUUID(),
			AgentTaskType:       "vibe",
			ChatTriggerType:     "MANUAL",
			ConversationID:      conversationID,
			CurrentMessage: KiroCurrentMessage{
				UserInputMessage: userInputMsg,
			},
		},
	}
	if len(history) > 0 {
		payload.ConversationState.History = history
	}
	if profileArn != "" {
		payload.ProfileArn = profileArn
	}

	return &KiroPayloadResult{
		Payload:           payload,
		ToolDocumentation: toolDoc,
	}, nil
}

// marshalNoEscapeHTML marshals v to JSON without escaping HTML characters (<, >, &).
// This is important for preserving special characters in content like <thinking> tags.
func marshalNoEscapeHTML(v any) ([]byte, error) {
	var buf strings.Builder
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	// Encode appends a trailing newline; strip it.
	s := buf.String()
	if len(s) > 0 && s[len(s)-1] == '\n' {
		s = s[:len(s)-1]
	}
	return []byte(s), nil
}

// marshalIndentNoEscapeHTML marshals v to JSON with indentation, without escaping HTML characters.
func marshalIndentNoEscapeHTML(v any, prefix, indent string) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent(prefix, indent)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	// Encode appends a trailing newline; strip it.
	b := buf.Bytes()
	if len(b) > 0 && b[len(b)-1] == '\n' {
		b = b[:len(b)-1]
	}
	return b, nil
}
