package claude

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

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
