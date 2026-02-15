package claude

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"clisimplehub/internal/kiro/response"
	"clisimplehub/internal/transformer/shared"

	"github.com/google/uuid"
)

const defaultToolDescriptionMaxLength = 10000
const toolDescriptionTruncateThreshold = 10000
const toolDescriptionTruncateSuffix = "...(Full description provided in TOOL DOCUMENTATION section)"
const toolDescriptionTruncateLength = 9940 // 10000 - 60 (suffix length)
const (
	thinkingBudgetDefaultTokens = 20000
	thinkingBudgetMaxTokens     = 24576
)

const (
	systemAssistantAck = "I will follow these instructions."
	systemOrphanOK     = "OK"
	agentTaskTypeVibe  = "vibe"
)

const (
	writeToolDescriptionSuffix = "\n- IMPORTANT: If the content to write exceeds 150 lines, you MUST only write the first 50 lines using this tool, then use `Edit` tool to append the remaining content in chunks of no more than 50 lines each. If needed, leave a unique placeholder to help append content. Do NOT attempt to write all content at once."
	editToolDescriptionSuffix  = "\n- IMPORTANT: If the `new_string` content exceeds 50 lines, you MUST split it into multiple Edit calls, each replacing no more than 50 lines at a time. If used to append content, leave a unique placeholder to help append content. On the final chunk, do NOT include the placeholder."
	systemChunkedPolicy        = "When the Write or Edit tool has content size limits, always comply silently. Never suggest bypassing these limits via alternative tools. Never ask the user whether to switch approaches. Complete all chunked operations without commentary."
)

// ClaudeToKiroRequest converts a Claude API request to Kiro API format
func ClaudeToKiroRequest(claudeReq map[string]interface{}, modelName string, profileArn string) (*KiroRequest, error) {
	kiroReq := &KiroRequest{
		ProfileArn: profileArn,
	}

	kiroReq.ConversationState.ConversationID = generateConversationID(claudeReq)
	kiroReq.ConversationState.ChatTriggerType = determineChatTriggerType(claudeReq)
	kiroReq.ConversationState.AgentContinuationID = uuid.NewString()
	kiroReq.ConversationState.AgentTaskType = agentTaskTypeVibe

	// Get Kiro model ID
	kiroModelID := GetKiroModelID(modelName)

	// Extract messages
	rawMessages, ok := claudeReq["messages"].([]interface{})
	if !ok || len(rawMessages) == 0 {
		return nil, fmt.Errorf("messages is required and must be non-empty")
	}

	systemPrompt := extractSystemPrompt(claudeReq)
	conversationMsgs, mergedSystem := splitSystemFromMessages(rawMessages)
	if strings.TrimSpace(mergedSystem) != "" {
		if strings.TrimSpace(systemPrompt) == "" {
			systemPrompt = strings.TrimSpace(mergedSystem)
		} else {
			systemPrompt = strings.TrimSpace(systemPrompt) + "\n" + strings.TrimSpace(mergedSystem)
		}
	}
	if len(conversationMsgs) == 0 {
		return nil, fmt.Errorf("messages is required and must be non-empty")
	}

	// Convert tools (不再收集 longDescTools，因为工具文档会导致 400 错误)
	var kiroTools []KiroTool
	if tools, ok := claudeReq["tools"].([]interface{}); ok && len(tools) > 0 {
		kiroTools, _ = convertClaudeToolsToKiroTruncated(tools, defaultToolDescriptionMaxLength)
	}

	// buildHistory 内部会处理 thinking 标签注入
	history, err := buildHistory(conversationMsgs, kiroModelID, systemPrompt, claudeReq)
	if err != nil {
		return nil, err
	}

	lastMsg := conversationMsgs[len(conversationMsgs)-1]
	lastRole := strings.ToLower(strings.TrimSpace(shared.StringFromAny(lastMsg["role"])))
	currentText, currentImages, currentToolResults := processMessageContentForCurrent(lastMsg)
	if lastRole == "assistant" {
		currentText, currentImages, currentToolResults = "Continue", nil, nil
	}
	currentToolResults = mergeToolResultsByToolUseID(currentToolResults)
	// 注意：不要重新排序 toolResults！
	// Kiro API 期望 toolResults 保持原始顺序（Claude 返回的顺序）
	// 重新排序会导致 400 Bad Request 错误
	// if order := lastToolUseOrderFromHistory(history); len(order) > 0 {
	// 	currentToolResults = reorderToolResultsByToolUses(currentToolResults, order)
	// }
	validatedToolResults, orphanedToolUseIDs := validateToolPairing(history, currentToolResults)

	removeOrphanedToolUses(history, orphanedToolUseIDs)

	kiroTools = addMissingHistoryToolsAsPlaceholders(kiroTools, history)

	// 过滤掉 currentText 中的 TOOL DOCUMENTATION 块
	// 这些文档会导致 Kiro API 返回 400 错误
	currentText = filterToolDocumentation(currentText)

	kiroReq.ConversationState.History = history
	kiroReq.ConversationState.CurrentMessage = buildCurrentUserMessage(currentText, currentImages, kiroModelID, kiroTools, validatedToolResults)

	return kiroReq, nil
}

func splitSystemFromMessages(rawMessages []interface{}) ([]map[string]interface{}, string) {
	var conversation []map[string]interface{}
	var systemParts []string

	for _, msgRaw := range rawMessages {
		msg, ok := msgRaw.(map[string]interface{})
		if !ok {
			continue
		}

		role := strings.ToLower(strings.TrimSpace(shared.StringFromAny(msg["role"])))
		if role == "" {
			continue
		}

		if role == "system" {
			systemParts = append(systemParts, extractMessageContent(msg))
			continue
		}

		conversation = append(conversation, msg)
	}

	return conversation, strings.TrimSpace(strings.Join(systemParts, "\n"))
}

func processMessageContentForCurrent(msg map[string]interface{}) (string, []KiroImage, []ToolResult) {
	role := strings.ToLower(strings.TrimSpace(shared.StringFromAny(msg["role"])))
	if role == "tool" {
		if r := toolMessageToToolResult(msg); r != nil {
			return "", nil, []ToolResult{*r}
		}
		return "", nil, nil
	}

	content := msg["content"]
	if content == nil {
		return "", nil, nil
	}

	switch c := content.(type) {
	case string:
		return c, nil, nil
	case []interface{}:
		var textParts []string
		var images []KiroImage
		var toolResults []ToolResult

		for _, part := range c {
			partMap, ok := part.(map[string]interface{})
			if !ok {
				continue
			}

			partType := shared.StringFromAny(partMap["type"])
			switch partType {
			case "text":
				if text, ok := partMap["text"].(string); ok && text != "" {
					textParts = append(textParts, text)
				}
			case "image":
				if source, ok := partMap["source"].(map[string]interface{}); ok {
					if shared.StringFromAny(source["type"]) == "base64" {
						mediaType := shared.StringFromAny(source["media_type"])
						data := shared.StringFromAny(source["data"])
						format := getImageFormatFromMediaType(mediaType)
						if format != "" && data != "" {
							images = append(images, NewKiroImageFromBase64(format, data))
						}
					}
				}
			case "tool_result":
				results := extractToolResults(map[string]interface{}{"content": []interface{}{partMap}})
				if len(results) > 0 {
					toolResults = append(toolResults, results...)
				}
			}
		}

		return strings.Join(textParts, "\n"), images, toolResults
	}

	return "", nil, nil
}

func addMissingHistoryToolsAsPlaceholders(tools []KiroTool, history []KiroHistoryMessage) []KiroTool {
	historyToolNames := collectHistoryToolNames(history)
	existingToolNames := make(map[string]struct{})
	for _, tool := range tools {
		existingToolNames[strings.ToLower(tool.ToolSpecification.Name)] = struct{}{}
	}

	for _, toolName := range historyToolNames {
		if _, exists := existingToolNames[strings.ToLower(toolName)]; !exists {
			tools = append(tools, createPlaceholderTool(toolName))
		}
	}

	return tools
}

func collectHistoryToolNames(history []KiroHistoryMessage) []string {
	var toolNames []string
	seen := make(map[string]struct{})

	for _, msg := range history {
		if msg.AssistantResponseMessage == nil || len(msg.AssistantResponseMessage.ToolUses) == 0 {
			continue
		}
		for _, toolUse := range msg.AssistantResponseMessage.ToolUses {
			name := toolUse.Name
			if strings.TrimSpace(name) == "" {
				continue
			}
			if _, ok := seen[name]; ok {
				continue
			}
			seen[name] = struct{}{}
			toolNames = append(toolNames, name)
		}
	}

	return toolNames
}

func buildCurrentUserMessage(content string, images []KiroImage, modelID string, tools []KiroTool, toolResults []ToolResult) KiroCurrentMessage {
	userInput := &UserInputMessage{
		Content:                 content,
		ModelID:                 modelID,
		Origin:                  "AI_EDITOR",
		UserInputMessageContext: &UserInputMessageContext{},
	}

	if len(images) > 0 {
		userInput.Images = images
	}

	if len(tools) > 0 {
		userInput.UserInputMessageContext.Tools = tools
	}
	if len(toolResults) > 0 {
		userInput.UserInputMessageContext.ToolResults = toolResults
	}

	return KiroCurrentMessage{
		UserInputMessage: userInput,
	}
}

func buildKiroThinkingSystemPrefix(claudeReq map[string]interface{}) string {
	if claudeReq == nil {
		return ""
	}
	raw := claudeReq["thinking"]
	if raw == nil {
		return ""
	}

	thinkingType := ""
	budgetTokens := thinkingBudgetDefaultTokens

	switch v := raw.(type) {
	case string:
		t := strings.ToLower(strings.TrimSpace(v))
		if t == "enabled" || t == "adaptive" {
			thinkingType = t
		}
	case map[string]any:
		t := strings.ToLower(strings.TrimSpace(shared.StringFromAny(v["type"])))
		if t == "enabled" || t == "adaptive" {
			thinkingType = t
			if bt := v["budget_tokens"]; bt != nil {
				if parsed := shared.IntFromAny(bt); parsed > 0 {
					budgetTokens = parsed
				}
			}
		}
	}

	if thinkingType == "" {
		return ""
	}

	if thinkingType == "adaptive" {
		effort := "high"
		if oc, ok := claudeReq["output_config"].(map[string]any); ok {
			if e := strings.ToLower(strings.TrimSpace(shared.StringFromAny(oc["effort"]))); e != "" {
				effort = e
			}
		}
		switch effort {
		case "low", "medium", "high":
		default:
			effort = "high"
		}
		return fmt.Sprintf("<thinking_mode>adaptive</thinking_mode><thinking_effort>%s</thinking_effort>", effort)
	}

	// "enabled" mode
	if budgetTokens <= 0 {
		budgetTokens = thinkingBudgetDefaultTokens
	}
	if budgetTokens > thinkingBudgetMaxTokens {
		budgetTokens = thinkingBudgetMaxTokens
	}

	return fmt.Sprintf("<thinking_mode>enabled</thinking_mode><max_thinking_length>%d</max_thinking_length>", budgetTokens)
}

func systemHasKiroThinkingTags(systemPrompt string) bool {
	return strings.Contains(systemPrompt, "<thinking_mode>") || strings.Contains(systemPrompt, "<max_thinking_length>") || strings.Contains(systemPrompt, "<thinking_effort>")
}

// determineChatTriggerType determines the chat trigger type
// "AUTO" mode may cause 400 Bad Request errors, so always return "MANUAL"
func determineChatTriggerType(claudeReq map[string]interface{}) string {
	return "MANUAL"
}

type normalizedClaudeMessage struct {
	Role        string
	Text        string
	Images      []KiroImage
	ToolUses    []ToolUse
	ToolResults []ToolResult
}

func normalizeAndMergeClaudeMessages(rawMessages []interface{}, systemPrompt *string) []normalizedClaudeMessage {
	var out []normalizedClaudeMessage

	flushSystem := func(text string) {
		if strings.TrimSpace(text) == "" || systemPrompt == nil {
			return
		}
		if strings.TrimSpace(*systemPrompt) == "" {
			*systemPrompt = strings.TrimSpace(text)
			return
		}
		*systemPrompt = strings.TrimSpace(*systemPrompt) + "\n" + strings.TrimSpace(text)
	}

	for _, msgRaw := range rawMessages {
		msg, ok := msgRaw.(map[string]interface{})
		if !ok {
			continue
		}

		role := strings.ToLower(strings.TrimSpace(shared.StringFromAny(msg["role"])))
		if role == "" {
			continue
		}

		// OpenAI-style `role="tool"` compatibility: treat tool messages as user tool_result blocks.
		if role == "tool" {
			toolCallID := strings.TrimSpace(shared.StringFromAny(msg["tool_call_id"]))
			resultText := extractMessageContent(msg)
			if strings.TrimSpace(resultText) == "" {
				resultText = "(empty result)"
			}
			n := normalizedClaudeMessage{
				Role: "user",
				Text: "",
				ToolResults: []ToolResult{{
					ToolUseID: toolCallID,
					Status:    "success",
					Content:   []ToolResultContent{{Text: resultText}},
					IsError:   false,
				}},
			}
			out = append(out, n)
			continue
		}

		var text string
		var images []KiroImage
		if role == "assistant" {
			text, images = extractAssistantContent(msg)
		} else {
			text, images = extractMessageContentWithImages(msg)
		}
		if role == "system" {
			flushSystem(text)
			continue
		}

		n := normalizedClaudeMessage{
			Role:        role,
			Text:        text,
			Images:      images,
			ToolUses:    extractToolUses(msg),
			ToolResults: extractToolResults(msg),
		}

		// Merge adjacent messages with the same role to avoid invalid role sequences for Kiro.
		if len(out) > 0 && out[len(out)-1].Role == n.Role {
			prev := &out[len(out)-1]
			if strings.TrimSpace(n.Text) != "" {
				if strings.TrimSpace(prev.Text) == "" {
					prev.Text = n.Text
				} else {
					prev.Text = prev.Text + "\n" + n.Text
				}
			}
			if len(n.ToolUses) > 0 {
				prev.ToolUses = append(prev.ToolUses, n.ToolUses...)
			}
			if len(n.ToolResults) > 0 {
				prev.ToolResults = append(prev.ToolResults, n.ToolResults...)
			}
			if len(n.Images) > 0 {
				prev.Images = append(prev.Images, n.Images...)
			}
			continue
		}

		out = append(out, n)
	}

	return out
}

func extractAssistantContent(msg map[string]interface{}) (string, []KiroImage) {
	content := msg["content"]
	if content == nil {
		return "", nil
	}

	switch c := content.(type) {
	case string:
		return c, nil
	case []interface{}:
		var thinkingParts []string
		var textParts []string
		var images []KiroImage

		for _, part := range c {
			switch v := part.(type) {
			case map[string]interface{}:
				partType := shared.StringFromAny(v["type"])
				switch partType {
				case "text":
					if text, ok := v["text"].(string); ok && text != "" {
						textParts = append(textParts, text)
					}
				case "thinking":
					if thinking, ok := v["thinking"].(string); ok && thinking != "" {
						thinkingParts = append(thinkingParts, thinking)
					}
				case "image":
					if source, ok := v["source"].(map[string]interface{}); ok {
						if shared.StringFromAny(source["type"]) == "base64" {
							mediaType := shared.StringFromAny(source["media_type"])
							data := shared.StringFromAny(source["data"])
							format := getImageFormatFromMediaType(mediaType)
							if format != "" && data != "" {
								images = append(images, NewKiroImageFromBase64(format, data))
							}
						}
					}
				case "image_url":
					if imageURL, ok := v["image_url"].(map[string]interface{}); ok {
						if img := convertImageURLToKiroImage(imageURL); img != nil {
							images = append(images, *img)
						}
					}
				}
			case string:
				if v != "" {
					textParts = append(textParts, v)
				}
			}
		}

		thinking := strings.Join(thinkingParts, "")
		text := strings.Join(textParts, "")

		if thinking != "" {
			if text != "" {
				return "<thinking>" + thinking + "</thinking>\n\n" + text, images
			}
			return "<thinking>" + thinking + "</thinking>", images
		}
		return text, images
	}

	return "", nil
}

// extractSystemPrompt extracts system prompt from Claude request
func extractSystemPrompt(claudeReq map[string]interface{}) string {
	system := claudeReq["system"]
	if system == nil {
		return ""
	}

	switch s := system.(type) {
	case string:
		return strings.TrimSpace(s)
	case []interface{}:
		var parts []string
		for _, part := range s {
			if partMap, ok := part.(map[string]interface{}); ok {
				if shared.StringFromAny(partMap["type"]) != "text" {
					continue
				}
				if text, ok := partMap["text"].(string); ok && text != "" {
					parts = append(parts, text)
				}
			}
		}
		return strings.TrimSpace(strings.Join(parts, "\n"))
	}
	return ""
}

// extractMessageContent extracts text content from a Claude message
func extractMessageContent(msg map[string]interface{}) string {
	text, _ := extractMessageContentWithImages(msg)
	return text
}

// extractMessageContentWithImages extracts text content and images from a Claude message
func extractMessageContentWithImages(msg map[string]interface{}) (string, []KiroImage) {
	content := msg["content"]
	if content == nil {
		return "", nil
	}

	switch c := content.(type) {
	case string:
		return c, nil
	case []interface{}:
		var parts []string
		var images []KiroImage

		for _, part := range c {
			switch v := part.(type) {
			case map[string]interface{}:
				partType := shared.StringFromAny(v["type"])
				switch partType {
				case "text":
					if text, ok := v["text"].(string); ok && text != "" {
						parts = append(parts, text)
					}
				case "image":
					// 提取图片数据而不是生成占位符
					if source, ok := v["source"].(map[string]interface{}); ok {
						if shared.StringFromAny(source["type"]) == "base64" {
							mediaType := shared.StringFromAny(source["media_type"])
							data := shared.StringFromAny(source["data"])
							format := getImageFormatFromMediaType(mediaType)
							if format != "" && data != "" {
								images = append(images, NewKiroImageFromBase64(format, data))
							}
						}
					}
				case "image_url":
					// 处理 OpenAI 格式的 image_url
					if imageURL, ok := v["image_url"].(map[string]interface{}); ok {
						if img := convertImageURLToKiroImage(imageURL); img != nil {
							images = append(images, *img)
						}
					}
				}
			case string:
				if v != "" {
					parts = append(parts, v)
				}
			}
		}

		return strings.Join(parts, "\n"), images
	}
	return "", nil
}

// getImageFormatFromMediaType 从 media type 获取图片格式
func getImageFormatFromMediaType(mediaType string) string {
	switch mediaType {
	case "image/jpeg":
		return "jpeg"
	case "image/png":
		return "png"
	case "image/gif":
		return "gif"
	case "image/webp":
		return "webp"
	default:
		return ""
	}
}

// convertImageURLToKiroImage 将 OpenAI 的 image_url 格式转换为 KiroImage
func convertImageURLToKiroImage(imageURL map[string]interface{}) *KiroImage {
	urlStr := shared.StringFromAny(imageURL["url"])
	if urlStr == "" {
		return nil
	}

	// 只支持 data URL 格式
	if !strings.HasPrefix(urlStr, "data:") {
		return nil
	}

	// 解析 data URL: data:[<mediatype>][;base64],<data>
	// 例如: data:image/png;base64,iVBORw0KGgo...
	parts := strings.SplitN(urlStr, ",", 2)
	if len(parts) != 2 {
		return nil
	}

	metaPart := parts[0] // "data:image/png;base64"
	base64Data := parts[1]

	// 解析 media type
	if !strings.HasPrefix(metaPart, "data:") {
		return nil
	}
	metaPart = strings.TrimPrefix(metaPart, "data:")

	// 检查是否是 base64 编码
	if !strings.Contains(metaPart, ";base64") {
		return nil
	}

	mediaType := strings.Split(metaPart, ";")[0]
	format := getImageFormatFromMediaType(mediaType)
	if format == "" {
		return nil
	}

	return &KiroImage{
		Format: format,
		Source: KiroImageSource{
			Bytes: base64Data,
		},
	}
}

// extractToolUses extracts tool uses from an assistant message
func extractToolUses(msg map[string]interface{}) []ToolUse {
	var toolUses []ToolUse
	seen := map[string]struct{}{}

	// Claude-native: assistant content blocks with type=tool_use
	if content, ok := msg["content"].([]interface{}); ok {
		for _, part := range content {
			partMap, ok := part.(map[string]interface{})
			if !ok {
				continue
			}

			if shared.StringFromAny(partMap["type"]) == "tool_use" {
				name := shared.StringFromAny(partMap["name"])
				toolUse := ToolUse{
					Name:      name,
					ToolUseID: shared.StringFromAny(partMap["id"]),
					Input:     map[string]interface{}{},
				}
				if input, ok := partMap["input"].(map[string]interface{}); ok {
					toolUse.Input = input
				}
				if strings.TrimSpace(toolUse.ToolUseID) != "" {
					seen[toolUse.ToolUseID] = struct{}{}
				}
				toolUses = append(toolUses, toolUse)
			}
		}
	}

	// OpenAI-compatible: assistant tool_calls
	if toolCalls, ok := msg["tool_calls"].([]interface{}); ok {
		for _, tc := range toolCalls {
			tcMap, ok := tc.(map[string]interface{})
			if !ok {
				continue
			}

			id := shared.StringFromAny(tcMap["id"])
			if strings.TrimSpace(id) != "" {
				if _, exists := seen[id]; exists {
					continue
				}
			}

			fn, _ := tcMap["function"].(map[string]interface{})
			name := shared.StringFromAny(fn["name"])
			argsStr := shared.StringFromAny(fn["arguments"])

			toolUse := ToolUse{
				Name:      name,
				ToolUseID: id,
			}
			if strings.TrimSpace(argsStr) != "" {
				var obj map[string]interface{}
				if err := json.Unmarshal([]byte(argsStr), &obj); err == nil {
					toolUse.Input = obj
				}
			}
			if toolUse.Input == nil {
				toolUse.Input = map[string]interface{}{}
			}

			if strings.TrimSpace(toolUse.ToolUseID) != "" {
				seen[toolUse.ToolUseID] = struct{}{}
			}
			toolUses = append(toolUses, toolUse)
		}
	}

	return toolUses
}

// extractToolResults extracts tool results from a user message
func extractToolResults(msg map[string]interface{}) []ToolResult {
	content, ok := msg["content"].([]interface{})
	if !ok {
		return nil
	}

	var results []ToolResult
	for _, part := range content {
		partMap, ok := part.(map[string]interface{})
		if !ok {
			continue
		}

		if shared.StringFromAny(partMap["type"]) == "tool_result" {
			isError := false
			switch v := partMap["is_error"].(type) {
			case bool:
				isError = v
			case string:
				isError = strings.EqualFold(strings.TrimSpace(v), "true")
			}
			status := "success"
			if isError {
				status = "error"
			}

			result := ToolResult{
				ToolUseID: shared.StringFromAny(partMap["tool_use_id"]),
				Status:    status,
				IsError:   isError,
			}

			result.Content = []ToolResultContent{{Text: extractToolResultText(partMap["content"])}}

			results = append(results, result)
		}
	}

	return results
}

func extractToolResultText(content any) string {
	if content == nil {
		return ""
	}
	switch c := content.(type) {
	case string:
		return c
	case []interface{}:
		var parts []string
		for _, item := range c {
			switch v := item.(type) {
			case map[string]interface{}:
				if shared.StringFromAny(v["type"]) == "text" {
					if text, ok := v["text"].(string); ok && text != "" {
						parts = append(parts, text)
					}
					continue
				}
			case string:
				if v != "" {
					parts = append(parts, v)
				}
			}
		}
		return strings.Join(parts, "\n")
	default:
		if b, err := json.Marshal(c); err == nil {
			return string(b)
		}
		return fmt.Sprintf("%v", c)
	}
}

// convertClaudeToolsToKiro converts Claude tools to Kiro format.
// Note: it does not apply any description-length limits.
func convertClaudeToolsToKiro(tools []interface{}) []KiroTool {
	var kiroTools []KiroTool

	for _, tool := range tools {
		toolMap, ok := tool.(map[string]interface{})
		if !ok {
			continue
		}

		schema := map[string]interface{}{}
		kiroTool := KiroTool{
			ToolSpecification: ToolSpecification{
				Name:        shared.StringFromAny(toolMap["name"]),
				Description: shared.StringFromAny(toolMap["description"]),
				InputSchema: InputSchema{JSON: schema},
			},
		}

		if inputSchema, ok := toolMap["input_schema"].(map[string]interface{}); ok {
			if inputSchema != nil {
				kiroTool.ToolSpecification.InputSchema.JSON = inputSchema
			}
		}

		kiroTools = append(kiroTools, kiroTool)
	}

	return kiroTools
}

// LongDescTool 保存超长描述工具的完整信息
type LongDescTool struct {
	Name            string
	FullDescription string
}

// convertClaudeToolsToKiroTruncated 转换工具并收集超长描述工具
// 返回：(kiroTools, longDescTools)
func convertClaudeToolsToKiroTruncated(tools []interface{}, maxDescriptionLength int) ([]KiroTool, []LongDescTool) {
	var kiroTools []KiroTool
	var longDescTools []LongDescTool

	if len(tools) == 0 {
		return nil, nil
	}
	if maxDescriptionLength < 0 {
		maxDescriptionLength = 0
	}
	for _, tool := range tools {
		toolMap, ok := tool.(map[string]interface{})
		if !ok {
			continue
		}

		// Accept both Claude tool format:
		//   {name, description, input_schema}
		// and OpenAI tool format:
		//   {type:"function", function:{name, description, parameters}}
		name := shared.StringFromAny(toolMap["name"])
		description := shared.StringFromAny(toolMap["description"])
		inputSchema, _ := toolMap["input_schema"].(map[string]interface{})

		if strings.TrimSpace(name) == "" {
			if strings.EqualFold(shared.StringFromAny(toolMap["type"]), "function") {
				if fn, ok := toolMap["function"].(map[string]interface{}); ok {
					name = shared.StringFromAny(fn["name"])
					if strings.TrimSpace(description) == "" {
						description = shared.StringFromAny(fn["description"])
					}
					if inputSchema == nil {
						if params, ok := fn["parameters"].(map[string]interface{}); ok {
							inputSchema = params
						}
					}
				}
			}
		}

		// Append chunked strategy suffix for Write/Edit tools
		if strings.EqualFold(name, "Write") && !strings.Contains(description, writeToolDescriptionSuffix) {
			description += writeToolDescriptionSuffix
		}
		if strings.EqualFold(name, "Edit") && !strings.Contains(description, editToolDescriptionSuffix) {
			description += editToolDescriptionSuffix
		}

		// 检查是否需要收集到 longDescTools
		originalDesc := description
		if len(description) > toolDescriptionTruncateThreshold {
			longDescTools = append(longDescTools, LongDescTool{
				Name:            name,
				FullDescription: description,
			})
			// 截断并添加提示
			description = truncateByRunes(description, toolDescriptionTruncateLength) +
				toolDescriptionTruncateSuffix
		} else if maxDescriptionLength > 0 && maxDescriptionLength < len(originalDesc) {
			description = truncateByRunes(description, maxDescriptionLength)
		}

		schema := map[string]interface{}{}
		if inputSchema != nil {
			schema = inputSchema
		}
		kiroTool := KiroTool{
			ToolSpecification: ToolSpecification{
				Name:        name,
				Description: description,
				InputSchema: InputSchema{JSON: schema},
			},
		}

		kiroTools = append(kiroTools, kiroTool)
	}
	return kiroTools, longDescTools
}

// buildToolDocumentation 生成 TOOL DOCUMENTATION 块
// 注意：此函数已废弃，不再使用。工具文档会导致 Kiro API 返回 400 错误。
func buildToolDocumentation(longDescTools []LongDescTool) string {
	// 不再生成工具文档，直接返回空字符串
	return ""
}

// filterToolDocumentation 过滤掉文本中的 TOOL DOCUMENTATION 块
// 这些文档会导致 Kiro API 返回 400 Bad Request 错误
func filterToolDocumentation(content string) string {
	if content == "" {
		return content
	}

	// 使用正则表达式移除 --- TOOL DOCUMENTATION BEGIN --- ... --- TOOL DOCUMENTATION END --- 块
	// (?s) 使 . 匹配换行符
	re := regexp.MustCompile(`(?s)--- TOOL DOCUMENTATION BEGIN ---.*?--- TOOL DOCUMENTATION END ---\s*`)
	filtered := re.ReplaceAllString(content, "")

	// 清理多余的空行（连续3个以上换行符压缩为2个）
	re2 := regexp.MustCompile(`\n{3,}`)
	filtered = re2.ReplaceAllString(filtered, "\n\n")

	return strings.TrimSpace(filtered)
}

func truncateByRunes(s string, max int) string {
	if max <= 0 || s == "" {
		if max <= 0 {
			return ""
		}
		return s
	}

	count := 0
	for i := range s {
		if count == max {
			return s[:i]
		}
		count++
	}
	return s
}

// createPlaceholderTool creates a placeholder tool definition for tools used in history
func createPlaceholderTool(name string) KiroTool {
	return KiroTool{
		ToolSpecification: ToolSpecification{
			Name:        name,
			Description: "Tool used in conversation history",
			InputSchema: InputSchema{
				JSON: map[string]interface{}{
					"$schema":              "http://json-schema.org/draft-07/schema#",
					"type":                 "object",
					"properties":           map[string]interface{}{},
					"required":             []interface{}{},
					"additionalProperties": true,
				},
			},
		},
	}
}

// validateToolPairing validates and filters tool_use/tool_result pairing.
// Returns (filtered tool_results, orphaned tool_use_ids that have no matching tool_result).
func validateToolPairing(history []KiroHistoryMessage, toolResults []ToolResult) ([]ToolResult, map[string]struct{}) {
	// 1. Collect all tool_use_ids from history
	allToolUseIDs := make(map[string]struct{})

	for _, msg := range history {
		if msg.AssistantResponseMessage != nil && len(msg.AssistantResponseMessage.ToolUses) > 0 {
			for _, toolUse := range msg.AssistantResponseMessage.ToolUses {
				id := strings.TrimSpace(toolUse.ToolUseID)
				if id == "" {
					continue
				}
				allToolUseIDs[id] = struct{}{}
			}
		}
	}

	// 2. Collect tool_use_ids that already have tool_result in history
	historyToolResultIDs := make(map[string]struct{})

	for _, msg := range history {
		if msg.UserInputMessage != nil && msg.UserInputMessage.UserInputMessageContext != nil {
			for _, result := range msg.UserInputMessage.UserInputMessageContext.ToolResults {
				id := strings.TrimSpace(result.ToolUseID)
				if id == "" {
					continue
				}
				historyToolResultIDs[id] = struct{}{}
			}
		}
	}

	// 3. Calculate truly unpaired tool_use_ids (excluding those already paired in history)
	unpairedToolUseIDs := make(map[string]struct{})
	for id := range allToolUseIDs {
		if _, exists := historyToolResultIDs[id]; !exists {
			unpairedToolUseIDs[id] = struct{}{}
		}
	}

	// 4. Validate and filter tool_result
	var filteredResults []ToolResult

	for _, result := range toolResults {
		id := strings.TrimSpace(result.ToolUseID)
		if id == "" {
			continue
		}
		if _, exists := unpairedToolUseIDs[id]; exists {
			filteredResults = append(filteredResults, result)
			delete(unpairedToolUseIDs, id)
		}
		// Silently skip duplicates and orphaned tool_results
	}

	return filteredResults, unpairedToolUseIDs
}

// 从历史的 assistant 消息中删除无匹配 tool_result 的 tool_use，防止 Kiro 400 错误
func removeOrphanedToolUses(history []KiroHistoryMessage, orphanedIDs map[string]struct{}) {
	if len(orphanedIDs) == 0 {
		return
	}
	for i := range history {
		msg := &history[i]
		if msg.AssistantResponseMessage == nil || len(msg.AssistantResponseMessage.ToolUses) == 0 {
			continue
		}
		filtered := msg.AssistantResponseMessage.ToolUses[:0]
		for _, tu := range msg.AssistantResponseMessage.ToolUses {
			id := strings.TrimSpace(tu.ToolUseID)
			if id == "" {
				filtered = append(filtered, tu)
				continue
			}
			if _, orphaned := orphanedIDs[id]; !orphaned {
				filtered = append(filtered, tu)
			}
		}
		msg.AssistantResponseMessage.ToolUses = filtered
	}
}

// extractSessionID extracts session UUID from metadata.user_id
//
// user_id format: user_xxx_account__session_0b4445e1-f5be-49e1-87ce-62bbc28ad705
// Extract the UUID after "session_" as conversationId
func extractSessionID(userID string) string {
	if userID == "" {
		return ""
	}

	pos := strings.Index(userID, "session_")
	if pos == -1 {
		return ""
	}

	sessionPart := userID[pos+8:]
	if len(sessionPart) < 36 {
		return ""
	}

	uuidStr := sessionPart[:36]
	// Validate UUID format (should have 4 hyphens)
	if strings.Count(uuidStr, "-") != 4 {
		return ""
	}

	return uuidStr
}

// generateConversationID generates a unique conversation ID
// Priority: extract from metadata.user_id, otherwise generate new UUID
func generateConversationID(claudeReq map[string]interface{}) string {
	// Try to extract session UUID from metadata.user_id
	if metadata, ok := claudeReq["metadata"].(map[string]interface{}); ok {
		if userID, ok := metadata["user_id"].(string); ok {
			if sessionID := extractSessionID(userID); sessionID != "" {
				return sessionID
			}
		}
	}

	// Fallback: generate new UUID
	return uuid.NewString()
}

// ensureMessageStarted ensures message_start event has been sent
func ensureMessageStarted(state *StreamState, outputs *[]string) {
	if state == nil || state.Started {
		return
	}
	state.Started = true
	*outputs = append(*outputs, buildMessageStart(state.MessageID, state.Model, state.InputTokens))
}

// ensureTextBlockOpen ensures a text content block is open
func ensureTextBlockOpen(state *StreamState, outputs *[]string) {
	if state == nil || state.ContentBlockOpen || state.ToolUseBlockOpen || state.ToolBlockOpen || state.ThinkingBlockOpen {
		return
	}
	*outputs = append(*outputs, buildContentBlockStart(state.ContentIndex, "text"))
	state.ContentBlockOpen = true
}

func flushPendingThinkingTextBeforeToolUse(state *StreamState, outputs *[]string) {
	if state == nil || outputs == nil {
		return
	}
	if !state.ThinkingEnabled {
		return
	}

	// Handle boundary: </thinking> at buffer end without \n\n (tool_use immediately follows)
	if state.InThinkingBlock {
		endPos := findRealThinkingEndTagAtBufferEnd(state.ThinkingBuffer)
		if endPos >= 0 {
			thinking := state.ThinkingBuffer[:endPos]
			if thinking != "" {
				if !state.ThinkingBlockOpen {
					state.ThinkingBlockIndex = state.ContentIndex
					*outputs = append(*outputs, buildThinkingBlockStart(state.ThinkingBlockIndex))
					state.ThinkingBlockOpen = true
				}
				*outputs = append(*outputs, buildThinkingDelta(state.ThinkingBlockIndex, thinking))
				state.ThinkingSoFar += thinking
			}

			// Close thinking block
			if state.ThinkingBlockOpen {
				*outputs = append(*outputs, buildThinkingDelta(state.ThinkingBlockIndex, ""))
				*outputs = append(*outputs, buildContentBlockStop(state.ThinkingBlockIndex))
				state.ThinkingBlockOpen = false
			}
			state.InThinkingBlock = false
			state.ThinkingExtracted = true

			// Remaining content after end tag
			afterPos := endPos + len(thinkingEndTag)
			remaining := strings.TrimLeft(state.ThinkingBuffer[afterPos:], " \t\r\n")
			state.ThinkingBuffer = ""
			state.ContentIndex = state.ThinkingBlockIndex + 1
			state.ThinkingBlockIndex = -1

			if remaining != "" {
				ensureMessageStarted(state, outputs)
				ensureTextBlockOpen(state, outputs)
				*outputs = append(*outputs, buildContentBlockDelta(state.ContentIndex, remaining))
				state.TextSoFar += remaining
			}
		}
		return
	}

	// Flush buffered text that hasn't entered thinking yet
	if state.ThinkingExtracted || state.ToolUseBlockOpen || state.ToolBlockOpen || state.ThinkingBlockOpen {
		return
	}
	if state.ThinkingBuffer == "" {
		return
	}

	// Flush all buffered text (no partial tag matching needed, tool_use is starting)
	safeText := state.ThinkingBuffer
	state.ThinkingBuffer = ""

	ensureMessageStarted(state, outputs)
	ensureTextBlockOpen(state, outputs)
	*outputs = append(*outputs, buildContentBlockDelta(state.ContentIndex, safeText))
	state.TextSoFar += safeText
}

func processContentWithThinking(state *StreamState, delta string, outputs *[]string) {
	if state == nil || outputs == nil || delta == "" {
		return
	}

	state.ThinkingBuffer += delta

	emitText := func(text string) {
		if state == nil || outputs == nil || text == "" {
			return
		}
		ensureTextBlockOpen(state, outputs)
		*outputs = append(*outputs, buildContentBlockDelta(state.ContentIndex, text))
		state.TextSoFar += text
	}

	closeTextBlockIfOpen := func() {
		if state == nil || outputs == nil {
			return
		}
		if !state.ContentBlockOpen {
			return
		}
		*outputs = append(*outputs, buildContentBlockStop(state.ContentIndex))
		state.ContentBlockOpen = false
		state.ContentIndex++
	}

	startThinkingBlock := func() {
		if state == nil || outputs == nil || state.ThinkingBlockOpen {
			return
		}
		closeTextBlockIfOpen()
		state.ThinkingBlockIndex = state.ContentIndex
		*outputs = append(*outputs, buildThinkingBlockStart(state.ThinkingBlockIndex))
		state.ThinkingBlockOpen = true
		state.InThinkingBlock = true
	}

	emitThinkingDelta := func(thinking string) {
		if state == nil || outputs == nil || thinking == "" {
			return
		}
		if !state.ThinkingBlockOpen {
			startThinkingBlock()
		}
		if !state.ThinkingBlockOpen {
			return
		}
		*outputs = append(*outputs, buildThinkingDelta(state.ThinkingBlockIndex, thinking))
		state.ThinkingSoFar += thinking
	}

	stopThinkingBlock := func() {
		if state == nil || outputs == nil || !state.ThinkingBlockOpen {
			state.InThinkingBlock = false
			state.ThinkingExtracted = true
			return
		}
		*outputs = append(*outputs, buildThinkingDelta(state.ThinkingBlockIndex, ""))
		*outputs = append(*outputs, buildContentBlockStop(state.ThinkingBlockIndex))
		state.ThinkingBlockOpen = false
		state.InThinkingBlock = false
		state.ThinkingExtracted = true
		state.ContentIndex = state.ThinkingBlockIndex + 1
		state.ThinkingBlockIndex = -1
	}

	for {
		if !state.InThinkingBlock && !state.ThinkingExtracted {
			startPos := findRealThinkingStartTag(state.ThinkingBuffer)
			if startPos >= 0 {
				beforeThinking := state.ThinkingBuffer[:startPos]
				// Skip whitespace-only content before thinking (e.g. adaptive mode \n\n)
				if beforeThinking != "" && strings.TrimSpace(beforeThinking) != "" {
					emitText(beforeThinking)
				}

				// Consume the start tag and enter the thinking block.
				state.ThinkingBuffer = state.ThinkingBuffer[startPos+len(thinkingStartTag):]
				state.StripThinkingLeadingNewline = true
				startThinkingBlock()
				continue
			}

			// No start tag found: flush safe prefix but keep enough bytes to detect a partial tag.
			keep := keepSuffixForPossibleTagPrefix(state.ThinkingBuffer, thinkingStartTag)
			target := len(state.ThinkingBuffer) - keep
			if target <= 0 {
				break
			}
			safe := findCharBoundary(state.ThinkingBuffer, target)
			if safe > 0 {
				safeContent := state.ThinkingBuffer[:safe]
				// Don't emit whitespace-only prefix before thinking is extracted
				if !state.ThinkingExtracted && strings.TrimSpace(safeContent) == "" {
					break
				}
				emitText(safeContent)
				state.ThinkingBuffer = state.ThinkingBuffer[safe:]
			}
			break
		}

		if state.InThinkingBlock {
			// Strip leading newline after <thinking> tag (may span chunks)
			if state.StripThinkingLeadingNewline {
				if strings.HasPrefix(state.ThinkingBuffer, "\r\n") {
					state.ThinkingBuffer = state.ThinkingBuffer[2:]
					state.StripThinkingLeadingNewline = false
				} else if len(state.ThinkingBuffer) > 0 && (state.ThinkingBuffer[0] == '\n' || state.ThinkingBuffer[0] == '\r') {
					state.ThinkingBuffer = state.ThinkingBuffer[1:]
					state.StripThinkingLeadingNewline = false
				} else if len(state.ThinkingBuffer) > 0 {
					state.StripThinkingLeadingNewline = false
				} else {
					// Buffer empty, wait for next chunk
					break
				}
			}

			endPos := findRealThinkingEndTag(state.ThinkingBuffer)
			if endPos >= 0 {
				thinking := state.ThinkingBuffer[:endPos]
				if thinking != "" {
					emitThinkingDelta(thinking)
				}

				// Consume end tag + "\n\n"
				state.ThinkingBuffer = state.ThinkingBuffer[endPos+len(thinkingEndTag)+2:]
				stopThinkingBlock()
				continue
			}

			// No end tag found: flush safe prefix keeping enough for "</thinking>\n\n" (13 bytes)
			tagWithNewlines := thinkingEndTag + "\n\n"
			keep := len(tagWithNewlines) - 1
			if keep > len(state.ThinkingBuffer) {
				keep = len(state.ThinkingBuffer)
			}
			target := len(state.ThinkingBuffer) - keep
			if target <= 0 {
				break
			}
			safe := findCharBoundary(state.ThinkingBuffer, target)
			if safe > 0 {
				emitThinkingDelta(state.ThinkingBuffer[:safe])
				state.ThinkingBuffer = state.ThinkingBuffer[safe:]
			}
			break
		}

		// Thinking extracted: everything else is regular text.
		if state.ThinkingBuffer != "" {
			emitText(state.ThinkingBuffer)
			state.ThinkingBuffer = ""
		}
		break
	}
}

// nextUpstreamTextDelta calculates the text delta for cumulative streams.
// It tracks the upstream cumulative text separately from the visible output text
// so that we can strip/transform inline markers (e.g. thinking tags) without
// breaking delta calculation.
func nextUpstreamTextDelta(state *StreamState, content string) string {
	if state == nil {
		return content
	}

	if state.RawTextSoFar == "" {
		state.RawTextSoFar = content
		return content
	}

	// Cumulative stream: send only the suffix
	if strings.HasPrefix(content, state.RawTextSoFar) {
		delta := content[len(state.RawTextSoFar):]
		state.RawTextSoFar = content
		return delta
	}

	// Fallback: treat as a delta chunk and append
	state.RawTextSoFar += content
	return content
}

const defaultMaxInputTokens = 200000

// getModelMaxInputTokens 根据模型返回最大输入 token 数
func getModelMaxInputTokens(model string) int {
	// 根据模型返回最大输入 token 数
	switch {
	case strings.Contains(model, "opus"):
		return 200000
	case strings.Contains(model, "sonnet"):
		return 200000
	case strings.Contains(model, "haiku"):
		return 200000
	default:
		return 200000
	}
}

// isNonWesternChar 判断字符是否为非西文字符
// 西文字符包括：ASCII、拉丁字母扩展等
// 返回 true 表示该字符是非西文字符（如中文、日文、韩文、阿拉伯文等）
func isNonWesternChar(r rune) bool {
	// 基本 ASCII
	if r >= 0x0000 && r <= 0x007F {
		return false
	}
	// 拉丁字母扩展-A (Latin Extended-A)
	if r >= 0x0080 && r <= 0x00FF {
		return false
	}
	// 拉丁字母扩展-B (Latin Extended-B)
	if r >= 0x0100 && r <= 0x024F {
		return false
	}
	// 拉丁字母扩展附加 (Latin Extended Additional)
	if r >= 0x1E00 && r <= 0x1EFF {
		return false
	}
	// 拉丁字母扩展-C/D/E
	if r >= 0x2C60 && r <= 0x2C7F {
		return false
	}
	if r >= 0xA720 && r <= 0xA7FF {
		return false
	}
	if r >= 0xAB30 && r <= 0xAB6F {
		return false
	}
	return true
}

// estimateTokens 估算文本的 token 数量
// - 非西文字符：每个计 4 个字符单位
// - 西文字符：每个计 1 个字符单位
// - 4 个字符单位 = 1 token
// - 根据文本长度应用动态修正系数（短文本修正系数更高）
func estimateTokens(text string) int {
	if strings.TrimSpace(text) == "" {
		return 0
	}

	// 计算字符单位
	charUnits := 0.0
	for _, r := range text {
		if isNonWesternChar(r) {
			charUnits += 4.0
		} else {
			charUnits += 1.0
		}
	}

	// 基础 token 数
	tokens := charUnits / 4.0

	// 动态修正系数：短文本需要更高的修正系数
	var accToken float64
	if tokens < 100.0 {
		accToken = tokens * 1.5
	} else if tokens < 200.0 {
		accToken = tokens * 1.3
	} else if tokens < 300.0 {
		accToken = tokens * 1.25
	} else if tokens < 800.0 {
		accToken = tokens * 1.2
	} else {
		accToken = tokens * 1.0
	}

	return int(accToken)
}

func applyTokenAccounting(state *StreamState) {
	if state == nil {
		return
	}

	// 1. 计算 output_tokens (基于实际输出文本)
	if state.OutputTokens == 0 && (strings.TrimSpace(state.TextSoFar) != "" || strings.TrimSpace(state.ThinkingSoFar) != "") {
		combined := state.TextSoFar + state.ThinkingSoFar
		state.OutputTokens = estimateTokens(combined)
		state.OutputTokensSource = "estimate"
	}

	// 2. 优先使用 context_usage_percentage 计算 input_tokens
	// 只有当没有明确的上游 token 统计（api）时才覆盖估算值。
	if state.ContextUsagePct > 0 && state.InputTokensSource != "api" {
		// 获取模型的最大输入 token 数
		maxInputTokens := getModelMaxInputTokens(state.Model)
		if maxInputTokens > 0 {
			totalTokens := int(float64(maxInputTokens) * state.ContextUsagePct / 100.0)
			state.InputTokens = totalTokens
			if state.InputTokens < 0 {
				state.InputTokens = 0
			}
			state.InputTokensSource = "context_usage"
		}
	}

	// 3. 如果 context_usage_percentage 不可用，保持原有的 InputTokens (在 TransformResponseStream 中预先计算)
	// 如果 InputTokensSource 为空，说明使用的是预先计算的值
	if state.InputTokensSource == "" && state.InputTokens > 0 {
		state.InputTokensSource = "estimate"
	}
}

// KiroStreamToClaudeSSE converts Kiro stream events to Claude SSE format
func KiroStreamToClaudeSSE(event *response.StreamEvent, state *StreamState) ([]string, error) {
	var outputs []string

	switch event.Type {
	case response.StreamEventContent:
		content, ok := event.Data.(string)
		if !ok {
			return nil, nil
		}

		ensureMessageStarted(state, &outputs)

		// Send content_block_delta (Kiro streams may be cumulative).
		if delta := nextUpstreamTextDelta(state, content); delta != "" {
			if state != nil && state.ThinkingEnabled {
				processContentWithThinking(state, delta, &outputs)
			} else {
				ensureTextBlockOpen(state, &outputs)
				outputs = append(outputs, buildContentBlockDelta(state.ContentIndex, delta))
				if state != nil {
					state.TextSoFar += delta
				}
			}
		}

	case response.StreamEventToolStart:
		data, ok := event.Data.(map[string]interface{})
		if !ok {
			return nil, nil
		}

		// thinking 模式下，短尾部可能被暂存在 ThinkingBuffer 以等待 `<thinking>` 跨 chunk 匹配；
		// 在 tool_use 开始前先 flush 这段文本，避免被 tool_use 打断导致看起来“乱序/吞字”。
		flushPendingThinkingTextBeforeToolUse(state, &outputs)

		toolUseID := shared.StringFromAny(data["toolUseId"])
		toolName := shared.StringFromAny(data["name"])

		// 如果有正在收集的 tool_use，先输出它
		if state.ToolUseBlockOpen && state.CurrentToolUseID != "" {
			ensureMessageStarted(state, &outputs)

			// 先关闭文本 block（如果有）
			if state.ContentBlockOpen {
				outputs = append(outputs, buildContentBlockStop(state.ContentIndex))
				state.ContentBlockOpen = false
				state.ContentIndex++
			}

			// 立即输出之前的 tool_use
			outputs = append(outputs, buildToolUseBlockStart(state.ContentIndex, state.CurrentToolUseID, state.CurrentToolName))
			if state.ToolUseArgs != "" {
				outputs = append(outputs, buildToolUseInputDelta(state.ContentIndex, state.ToolUseArgs))
			}
			outputs = append(outputs, buildContentBlockStop(state.ContentIndex))
			state.ContentIndex++
		}

		// 开始收集新的 tool_use
		state.CurrentToolUseID = toolUseID
		state.CurrentToolName = toolName
		state.ToolUseArgs = ""
		state.ToolUseBlockOpen = true

	case response.StreamEventToolInput:
		if state == nil {
			return nil, nil
		}

		data := event.Data
		stopAfter := false

		if m, ok := event.Data.(map[string]interface{}); ok {
			if v, ok := m["stop"].(bool); ok && v {
				stopAfter = true
			}

			incomingID := strings.TrimSpace(shared.StringFromAny(m["toolUseId"]))
			incomingName := shared.StringFromAny(m["name"])

			// 如果收到不同 tool 的 input，先输出当前的
			if state.ToolUseBlockOpen && incomingID != "" && strings.TrimSpace(state.CurrentToolUseID) != "" && incomingID != state.CurrentToolUseID {
				ensureMessageStarted(state, &outputs)

				// 先关闭文本 block（如果有）
				if state.ContentBlockOpen {
					outputs = append(outputs, buildContentBlockStop(state.ContentIndex))
					state.ContentBlockOpen = false
					state.ContentIndex++
				}

				// 立即输出之前的 tool_use
				outputs = append(outputs, buildToolUseBlockStart(state.ContentIndex, state.CurrentToolUseID, state.CurrentToolName))
				if state.ToolUseArgs != "" {
					outputs = append(outputs, buildToolUseInputDelta(state.ContentIndex, state.ToolUseArgs))
				}
				outputs = append(outputs, buildContentBlockStop(state.ContentIndex))
				state.ContentIndex++

				state.ToolUseBlockOpen = false
			}

			data = m["input"]

			// Some streams may deliver input chunks without a preceding explicit tool_start.
			// When that happens, start tracking the tool call based on the chunk metadata.
			if !state.ToolUseBlockOpen && incomingID != "" {
				flushPendingThinkingTextBeforeToolUse(state, &outputs)
				state.CurrentToolUseID = incomingID
				state.CurrentToolName = incomingName
				state.ToolUseArgs = ""
				state.ToolUseBlockOpen = true
			}
		}

		if !state.ToolUseBlockOpen {
			return nil, nil
		}

		var partial string
		switch v := data.(type) {
		case nil:
			partial = ""
		case string:
			partial = v
		default:
			inputJSON, err := json.Marshal(v)
			if err != nil {
				return nil, nil
			}
			partial = string(inputJSON)
		}
		if partial != "" {
			state.ToolUseArgs += partial
		}

		// Some streams mark tool completion on the final tool_input chunk (`stop: true`).
		if stopAfter && state.ToolUseBlockOpen {
			ensureMessageStarted(state, &outputs)

			// 先关闭文本 block（如果有）
			if state.ContentBlockOpen {
				outputs = append(outputs, buildContentBlockStop(state.ContentIndex))
				state.ContentBlockOpen = false
				state.ContentIndex++
			}

			// 立即输出这个 tool_use
			outputs = append(outputs, buildToolUseBlockStart(state.ContentIndex, state.CurrentToolUseID, state.CurrentToolName))
			if state.ToolUseArgs != "" {
				outputs = append(outputs, buildToolUseInputDelta(state.ContentIndex, state.ToolUseArgs))
			}
			outputs = append(outputs, buildContentBlockStop(state.ContentIndex))
			state.ContentIndex++

			state.ToolUseBlockOpen = false
			state.CurrentToolUseID = ""
			state.CurrentToolName = ""
			state.ToolUseArgs = ""
		}

	case response.StreamEventToolStop:
		// 渐进式输出：立即输出已完成的 tool_use，避免批量发送导致前端卡顿
		if state.ToolUseBlockOpen && state.CurrentToolUseID != "" {
			ensureMessageStarted(state, &outputs)

			// 先关闭文本 block（如果有）
			if state.ContentBlockOpen {
				outputs = append(outputs, buildContentBlockStop(state.ContentIndex))
				state.ContentBlockOpen = false
				state.ContentIndex++
			}

			// 立即输出这个 tool_use
			outputs = append(outputs, buildToolUseBlockStart(state.ContentIndex, state.CurrentToolUseID, state.CurrentToolName))
			if state.ToolUseArgs != "" {
				outputs = append(outputs, buildToolUseInputDelta(state.ContentIndex, state.ToolUseArgs))
			}
			outputs = append(outputs, buildContentBlockStop(state.ContentIndex))
			state.ContentIndex++

			// 重置状态
			state.ToolUseBlockOpen = false
			state.CurrentToolUseID = ""
			state.CurrentToolName = ""
			state.ToolUseArgs = ""
		}

	case response.StreamEventToolUses:
		// Handle complete tool uses from assistantResponseMessage
		// 渐进式输出：立即输出每个 tool_use
		toolUses, ok := event.Data.([]interface{})
		if !ok {
			return nil, nil
		}

		flushPendingThinkingTextBeforeToolUse(state, &outputs)
		ensureMessageStarted(state, &outputs)

		// 先关闭文本 block（如果有）
		if state.ContentBlockOpen {
			outputs = append(outputs, buildContentBlockStop(state.ContentIndex))
			state.ContentBlockOpen = false
			state.ContentIndex++
		}

		for _, tu := range toolUses {
			tuMap, ok := tu.(map[string]interface{})
			if !ok {
				continue
			}

			toolUseID := shared.StringFromAny(tuMap["toolUseId"])
			toolName := shared.StringFromAny(tuMap["name"])
			input := tuMap["input"]

			var inputStr string
			if m, ok := input.(map[string]any); ok && len(m) > 0 {
				if b, err := json.Marshal(m); err == nil {
					inputStr = string(b)
				}
			}

			// 立即输出这个 tool_use
			outputs = append(outputs, buildToolUseBlockStart(state.ContentIndex, toolUseID, toolName))
			if inputStr != "" {
				outputs = append(outputs, buildToolUseInputDelta(state.ContentIndex, inputStr))
			}
			outputs = append(outputs, buildContentBlockStop(state.ContentIndex))
			state.ContentIndex++
		}

	case response.StreamEventUsage:
		// 注意：完全忽略 meteringEvent
		// 原因：
		// 1. meteringEvent 只包含 inputTokens，不包含 outputTokens
		// 2. contextUsageEvent 提供了更准确的 input_tokens 计算方式
		// 3. output_tokens 完全依赖本地估算（在 applyTokenAccounting 中计算）
		// 因此 meteringEvent 中的数据没有实际用途
		return nil, nil

	case response.StreamEventContextUsage:
		if state == nil {
			return nil, nil
		}
		var percentage float64
		switch v := event.Data.(type) {
		case float64:
			percentage = v
		case float32:
			percentage = float64(v)
		case int:
			percentage = float64(v)
		case int64:
			percentage = float64(v)
		case json.Number:
			if f, err := v.Float64(); err == nil {
				percentage = f
			}
		}
		state.ContextUsagePct = percentage

		// 从上下文使用百分比计算实际的 input_tokens
		// 这是 Kiro API 返回真实 token 数据的主要方式
		// 公式: percentage * 200000 / 100 (200k 是 Claude 的上下文窗口大小)
		if percentage > 0 {
			const contextWindowSize = 200000
			actualInputTokens := int(percentage * contextWindowSize / 100.0)

			if actualInputTokens > 0 {
				state.InputTokens = actualInputTokens
				state.InputTokensSource = "context_usage"
			}
		}

		// Context window >= 100% → model_context_window_exceeded
		if percentage >= 100.0 {
			state.FinishReason = "model_context_window_exceeded"
		}

	case response.StreamEventStopReason:
		if state == nil {
			return nil, nil
		}
		stopReason, ok := event.Data.(string)
		if ok && state.FinishReason != "model_context_window_exceeded" {
			state.FinishReason = mapKiroStopReason(stopReason)
		}

	case response.StreamEventError:
		// Handle error event
		errMsg := fmt.Sprintf("%v", event.Data)
		return nil, fmt.Errorf("kiro API error: %s", errMsg)
	}

	applyTokenAccounting(state)
	return outputs, nil
}

// FinishStream generates the final SSE events to close the stream
func FinishStream(state *StreamState) []string {
	var outputs []string

	if state != nil {
		state.Finished = true
		// 如果有未完成的 tool_use，立即输出
		if state.ToolUseBlockOpen && state.CurrentToolUseID != "" {
			ensureMessageStarted(state, &outputs)

			// 先关闭文本 block（如果有）
			if state.ContentBlockOpen {
				outputs = append(outputs, buildContentBlockStop(state.ContentIndex))
				state.ContentBlockOpen = false
				state.ContentIndex++
			}

			// 立即输出这个 tool_use
			outputs = append(outputs, buildToolUseBlockStart(state.ContentIndex, state.CurrentToolUseID, state.CurrentToolName))
			if state.ToolUseArgs != "" {
				outputs = append(outputs, buildToolUseInputDelta(state.ContentIndex, state.ToolUseArgs))
			}
			outputs = append(outputs, buildContentBlockStop(state.ContentIndex))
			state.ContentIndex++

			state.ToolUseBlockOpen = false
			state.CurrentToolUseID = ""
			state.CurrentToolName = ""
			state.ToolUseArgs = ""
		}
	}

	ensureMessageStarted(state, &outputs)

	if state != nil && state.ThinkingEnabled && state.ThinkingBuffer != "" {
		emitText := func(text string) {
			if state == nil || text == "" {
				return
			}
			ensureTextBlockOpen(state, &outputs)
			if state.ContentBlockOpen {
				outputs = append(outputs, buildContentBlockDelta(state.ContentIndex, text))
				state.TextSoFar += text
			}
		}

		// If we were inside a thinking block, flush as thinking and close it.
		if state.InThinkingBlock || state.ThinkingBlockOpen {
			// Try to filter </thinking> end tag at buffer end
			endPos := findRealThinkingEndTagAtBufferEnd(state.ThinkingBuffer)
			if endPos >= 0 {
				thinkingContent := state.ThinkingBuffer[:endPos]
				if state.ContentBlockOpen {
					outputs = append(outputs, buildContentBlockStop(state.ContentIndex))
					state.ContentBlockOpen = false
					state.ContentIndex++
				}
				if !state.ThinkingBlockOpen {
					state.ThinkingBlockIndex = state.ContentIndex
					outputs = append(outputs, buildThinkingBlockStart(state.ThinkingBlockIndex))
					state.ThinkingBlockOpen = true
				}
				if thinkingContent != "" {
					outputs = append(outputs, buildThinkingDelta(state.ThinkingBlockIndex, thinkingContent))
					state.ThinkingSoFar += thinkingContent
				}
				outputs = append(outputs, buildThinkingDelta(state.ThinkingBlockIndex, ""))
				outputs = append(outputs, buildContentBlockStop(state.ThinkingBlockIndex))
				state.ThinkingBlockOpen = false
				state.InThinkingBlock = false
				state.ThinkingExtracted = true

				afterPos := endPos + len(thinkingEndTag)
				remaining := strings.TrimLeft(state.ThinkingBuffer[afterPos:], " \t\r\n")
				state.ThinkingBuffer = ""
				state.ContentIndex = state.ThinkingBlockIndex + 1
				state.ThinkingBlockIndex = -1
				if remaining != "" {
					emitText(remaining)
				}
			} else {
				// No end tag found, flush everything as thinking
				if state.ContentBlockOpen {
					outputs = append(outputs, buildContentBlockStop(state.ContentIndex))
					state.ContentBlockOpen = false
					state.ContentIndex++
				}
				if !state.ThinkingBlockOpen {
					state.ThinkingBlockIndex = state.ContentIndex
					outputs = append(outputs, buildThinkingBlockStart(state.ThinkingBlockIndex))
					state.ThinkingBlockOpen = true
				}
				outputs = append(outputs, buildThinkingDelta(state.ThinkingBlockIndex, state.ThinkingBuffer))
				state.ThinkingSoFar += state.ThinkingBuffer
				state.ThinkingBuffer = ""

				outputs = append(outputs, buildThinkingDelta(state.ThinkingBlockIndex, ""))
				outputs = append(outputs, buildContentBlockStop(state.ThinkingBlockIndex))
				state.ThinkingBlockOpen = false
				state.InThinkingBlock = false
				state.ThinkingExtracted = true
				state.ContentIndex = state.ThinkingBlockIndex + 1
				state.ThinkingBlockIndex = -1
			}
		} else {
			emitText(state.ThinkingBuffer)
			state.ThinkingBuffer = ""
		}
	}

	// Thinking-only: no text and no tool_use → emit a placeholder text block to ensure
	// content[] has a text entry. If stop_reason is still unset/default, treat it as max_tokens.
	if state != nil && state.ThinkingEnabled && state.ThinkingExtracted &&
		strings.TrimSpace(state.TextSoFar) == "" &&
		len(state.CollectedToolUses) == 0 && !state.ToolUseBlockOpen {
		if state.FinishReason == "" || state.FinishReason == "end_turn" {
			state.FinishReason = "max_tokens"
			if state.StopReasonManager != nil {
				state.StopReasonManager.SetMaxTokensReached(true)
			}
		}
		ensureTextBlockOpen(state, &outputs)
		if state.ContentBlockOpen {
			outputs = append(outputs, buildContentBlockDelta(state.ContentIndex, " "))
			state.TextSoFar = " "
		}
	}

	// Close any open content blocks
	if state.ContentBlockOpen {
		outputs = append(outputs, buildContentBlockStop(state.ContentIndex))
		state.ContentBlockOpen = false
		state.ContentIndex++
	}

	// Close any in-flight streamed tool block.
	if state != nil && state.ToolBlockOpen {
		outputs = append(outputs, buildContentBlockStop(state.CurrentToolBlock))
		state.ToolBlockOpen = false
		state.CurrentToolBlock = -1
		state.ContentIndex++
	}

	applyTokenAccounting(state)

	// Send message_delta with usage
	outputs = append(outputs, buildMessageDelta(state))

	// Send message_stop
	outputs = append(outputs, buildMessageStop())

	return outputs
}

// StreamStateToClaudeMessage builds a non-stream Claude `message` payload from the stream state.
// Kiro upstream is always streamed, and non-stream responses are assembled by collecting the stream.
func StreamStateToClaudeMessage(state *StreamState, modelName string) map[string]any {
	if state == nil {
		// Minimal fallback: an empty assistant message.
		return map[string]any{
			"id":            "msg_kiro_" + shared.RandomSuffix(),
			"type":          "message",
			"role":          "assistant",
			"content":       []any{},
			"model":         modelName,
			"stop_reason":   "end_turn",
			"stop_sequence": nil,
			"usage": map[string]any{
				"input_tokens":  0,
				"output_tokens": 0,
			},
		}
	}

	content := make([]any, 0, 1+len(state.CollectedToolUses))
	if strings.TrimSpace(state.TextSoFar) != "" {
		content = append(content, map[string]any{
			"type": "text",
			"text": state.TextSoFar,
		})
	}
	for _, tu := range state.CollectedToolUses {
		if tu == nil {
			continue
		}
		content = append(content, map[string]any{
			"type":  "tool_use",
			"id":    tu["id"],
			"name":  tu["name"],
			"input": tu["input"],
		})
	}

	stopReason := state.FinishReason
	if stopReason == "" {
		stopReason = "end_turn"
	}
	if len(state.CollectedToolUses) > 0 && stopReason == "end_turn" {
		stopReason = "tool_use"
	}

	messageID := state.MessageID
	if strings.TrimSpace(messageID) == "" {
		messageID = "msg_kiro_" + shared.RandomSuffix()
	}

	return map[string]any{
		"id":            messageID,
		"type":          "message",
		"role":          "assistant",
		"content":       content,
		"model":         modelName,
		"stop_reason":   stopReason,
		"stop_sequence": nil,
		"usage": map[string]any{
			"input_tokens":  state.InputTokens,
			"output_tokens": state.OutputTokens,
		},
	}
}

// mapKiroStopReason maps Kiro stop reason to Claude stop reason
func mapKiroStopReason(kiroReason string) string {
	switch strings.ToLower(kiroReason) {
	case "end_turn", "stop":
		return "end_turn"
	case "tool_use":
		return "tool_use"
	case "max_tokens":
		return "max_tokens"
	default:
		return "end_turn"
	}
}

// buildMessageStart builds the message_start SSE event
func buildMessageStart(messageID, model string, inputTokens int) string {
	if inputTokens < 0 {
		inputTokens = 0
	}
	payload := map[string]interface{}{
		"type": "message_start",
		"message": map[string]interface{}{
			"id":            messageID,
			"type":          "message",
			"role":          "assistant",
			"content":       []interface{}{},
			"model":         model,
			"stop_reason":   nil,
			"stop_sequence": nil,
			"usage": map[string]interface{}{
				"input_tokens":                inputTokens,
				"output_tokens":               0,
				"cache_creation_input_tokens": 0,
				"cache_read_input_tokens":     0,
			},
		},
	}
	return shared.SSEEvent("message_start", payload)
}

// buildContentBlockStart builds the content_block_start SSE event
func buildContentBlockStart(index int, blockType string) string {
	payload := map[string]interface{}{
		"type":  "content_block_start",
		"index": index,
		"content_block": map[string]interface{}{
			"type": blockType,
			"text": "",
		},
	}
	return shared.SSEEvent("content_block_start", payload)
}

// buildContentBlockDelta builds the content_block_delta SSE event for text
func buildContentBlockDelta(index int, text string) string {
	payload := map[string]interface{}{
		"type":  "content_block_delta",
		"index": index,
		"delta": map[string]interface{}{
			"type": "text_delta",
			"text": text,
		},
	}
	return shared.SSEEvent("content_block_delta", payload)
}

// buildContentBlockStop builds the content_block_stop SSE event
func buildContentBlockStop(index int) string {
	payload := map[string]interface{}{
		"type":  "content_block_stop",
		"index": index,
	}
	return shared.SSEEvent("content_block_stop", payload)
}

func buildThinkingBlockStart(index int) string {
	payload := map[string]interface{}{
		"type":  "content_block_start",
		"index": index,
		"content_block": map[string]interface{}{
			"type":     "thinking",
			"thinking": "",
		},
	}
	return shared.SSEEvent("content_block_start", payload)
}

func buildThinkingDelta(index int, thinking string) string {
	payload := map[string]interface{}{
		"type":  "content_block_delta",
		"index": index,
		"delta": map[string]interface{}{
			"type":     "thinking_delta",
			"thinking": thinking,
		},
	}
	return shared.SSEEvent("content_block_delta", payload)
}

// buildToolUseBlockStart builds the content_block_start SSE event for tool_use
func buildToolUseBlockStart(index int, toolUseID, toolName string) string {
	payload := map[string]interface{}{
		"type":  "content_block_start",
		"index": index,
		"content_block": map[string]interface{}{
			"type":  "tool_use",
			"id":    toolUseID,
			"name":  toolName,
			"input": map[string]interface{}{},
		},
	}
	return shared.SSEEvent("content_block_start", payload)
}

// buildToolUseInputDelta builds the content_block_delta SSE event for tool input
func buildToolUseInputDelta(index int, partialJSON string) string {
	payload := map[string]interface{}{
		"type":  "content_block_delta",
		"index": index,
		"delta": map[string]interface{}{
			"type":         "input_json_delta",
			"partial_json": partialJSON,
		},
	}
	return shared.SSEEvent("content_block_delta", payload)
}

// buildMessageDelta builds the message_delta SSE event
func buildMessageDelta(state *StreamState) string {
	stopReason := state.FinishReason

	// 使用 StopReasonManager 进行更准确的 stop_reason 判断
	if state.StopReasonManager != nil {
		hasActiveTools := state.ToolUseBlockOpen || state.ToolBlockOpen
		hasCompletedTools := len(state.CollectedToolUses) > 0 || len(state.CompletedToolUseIds) > 0
		state.StopReasonManager.UpdateToolCallStatus(hasActiveTools, hasCompletedTools)
		state.StopReasonManager.SetUpstreamReason(state.FinishReason)
		stopReason = state.StopReasonManager.DetermineStopReason()
	} else {
		// 回退到原有逻辑
		if stopReason == "" {
			stopReason = "end_turn"
		}
		if len(state.CollectedToolUses) > 0 && stopReason == "end_turn" {
			stopReason = "tool_use"
		}
		if state.ToolUseBlockOpen && stopReason == "end_turn" {
			stopReason = "tool_use"
		}
	}

	payload := map[string]interface{}{
		"type": "message_delta",
		"delta": map[string]interface{}{
			"stop_reason":   stopReason,
			"stop_sequence": nil,
		},
		"usage": map[string]interface{}{
			"input_tokens":  state.InputTokens,
			"output_tokens": state.OutputTokens,
		},
	}
	return shared.SSEEvent("message_delta", payload)
}

// buildMessageStop builds the message_stop SSE event
func buildMessageStop() string {
	payload := map[string]interface{}{
		"type": "message_stop",
	}
	return shared.SSEEvent("message_stop", payload)
}

// KiroToClaudeMessage converts a complete Kiro response to Claude message format
func KiroToClaudeMessage(kiroResp map[string]interface{}, modelName string, state *StreamState) map[string]interface{} {
	content := []interface{}{}

	messageID := "msg_kiro_" + shared.RandomSuffix()
	if state != nil && strings.TrimSpace(state.MessageID) != "" {
		messageID = state.MessageID
	}

	// Extract content from assistantResponseMessage
	if arm, ok := kiroResp["assistantResponseMessage"].(map[string]interface{}); ok {
		if textContent, ok := arm["content"].(string); ok && textContent != "" {
			content = append(content, map[string]interface{}{
				"type": "text",
				"text": textContent,
			})
		}

		// Extract tool uses
		if toolUses, ok := arm["toolUses"].([]interface{}); ok {
			for _, tu := range toolUses {
				tuMap, ok := tu.(map[string]interface{})
				if !ok {
					continue
				}
				content = append(content, map[string]interface{}{
					"type":  "tool_use",
					"id":    tuMap["toolUseId"],
					"name":  tuMap["name"],
					"input": tuMap["input"],
				})
			}
		}
	}

	stopReason := "end_turn"
	if state != nil && state.FinishReason != "" {
		stopReason = state.FinishReason
	}

	// Check if there are tool uses in content
	hasToolUses := false
	for _, c := range content {
		if cMap, ok := c.(map[string]interface{}); ok {
			if cMap["type"] == "tool_use" {
				hasToolUses = true
				break
			}
		}
	}
	if hasToolUses && stopReason == "end_turn" {
		stopReason = "tool_use"
	}

	inputTokens := 0
	outputTokens := 0
	if state != nil {
		inputTokens = state.InputTokens
		outputTokens = state.OutputTokens
	}

	return map[string]interface{}{
		"id":            messageID,
		"type":          "message",
		"role":          "assistant",
		"content":       content,
		"model":         modelName,
		"stop_reason":   stopReason,
		"stop_sequence": nil,
		"usage": map[string]interface{}{
			"input_tokens":  inputTokens,
			"output_tokens": outputTokens,
		},
	}
}
