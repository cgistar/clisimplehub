package claude

import (
	"encoding/json"
	"fmt"
	"strings"

	kirotypes "clisimplehub/internal/transformer/kiro/types"
	"clisimplehub/internal/transformer/shared"

	"github.com/google/uuid"
)

const defaultToolDescriptionMaxLength = 10000

// ClaudeToKiroRequest converts a Claude API request to Kiro API format
func ClaudeToKiroRequest(claudeReq map[string]interface{}, modelName string, profileArn string) (*KiroRequest, error) {
	kiroReq := &KiroRequest{
		ProfileArn: profileArn,
	}

	kiroReq.ConversationState.ConversationID = generateConversationID()
	kiroReq.ConversationState.ChatTriggerType = determineChatTriggerType(claudeReq)

	// Get Kiro model ID
	kiroModelID := GetKiroModelID(modelName)

	// Extract messages
	rawMessages, ok := claudeReq["messages"].([]interface{})
	if !ok || len(rawMessages) == 0 {
		return nil, fmt.Errorf("messages is required and must be non-empty")
	}

	// Extract system prompt (Claude top-level `system`) and merge in any role=system messages.
	systemPrompt := extractSystemPrompt(claudeReq)
	messages := normalizeAndMergeClaudeMessages(rawMessages, &systemPrompt)
	if len(messages) == 0 {
		return nil, fmt.Errorf("messages is required and must be non-empty")
	}

	// Convert tools (and move overlong tool descriptions into the system prompt)
	var kiroTools []KiroTool
	if tools, ok := claudeReq["tools"].([]interface{}); ok && len(tools) > 0 {
		converted, toolDoc := convertClaudeToolsToKiroWithToolDocs(tools, defaultToolDescriptionMaxLength)
		kiroTools = converted
		if strings.TrimSpace(toolDoc) != "" {
			systemPrompt = appendToolDocumentationToSystemPrompt(systemPrompt, toolDoc)
		}
	}

	// - history = all messages except last
	// - system prompt is injected ONLY into the first history message IF it is a user message
	// - if there is no history, inject system prompt into current message
	historyMsgs := messages[:len(messages)-1]
	currentMsg := messages[len(messages)-1]

	if systemPrompt != "" && len(historyMsgs) > 0 && historyMsgs[0].Role == "user" {
		historyMsgs[0].Text = systemPrompt + "\n\n" + historyMsgs[0].Text
	}

	for _, msg := range historyMsgs {
		kiroReq.ConversationState.History = append(kiroReq.ConversationState.History, buildHistoryMessage(msg, kiroModelID, ""))
	}

	currentContent := currentMsg.Text
	if systemPrompt != "" && len(historyMsgs) == 0 {
		currentContent = systemPrompt + "\n\n" + currentContent
	}

	if currentMsg.Role == "assistant" {
		kiroReq.ConversationState.History = append(kiroReq.ConversationState.History, buildHistoryMessage(currentMsg, kiroModelID, ""))
		currentContent = "Continue"
	}

	toolResults := []ToolResult(nil)
	if currentMsg.Role == "user" {
		toolResults = currentMsg.ToolResults
	}
	kiroReq.ConversationState.CurrentMessage = buildCurrentUserMessage(currentContent, kiroModelID, kiroTools, toolResults)

	return kiroReq, nil
}

func determineChatTriggerType(claudeReq map[string]interface{}) string {
	tools, ok := claudeReq["tools"].([]interface{})
	if !ok || len(tools) == 0 {
		return "MANUAL"
	}

	toolChoice := claudeReq["tool_choice"]
	switch tc := toolChoice.(type) {
	case map[string]interface{}:
		typ := strings.ToLower(strings.TrimSpace(shared.StringFromAny(tc["type"])))
		if typ == "any" || typ == "tool" {
			return "AUTO"
		}
	case string:
		typ := strings.ToLower(strings.TrimSpace(tc))
		if typ == "any" || typ == "tool" {
			return "AUTO"
		}
	}

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
		// The Python gateway does this in its OpenAI merge step.
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
				}},
			}
			out = append(out, n)
			continue
		}

		text, images := extractMessageContentWithImages(msg)
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

		// Keep parity with the reference Python converter: user messages that carry `tool_result`
		// blocks are treated as "tool-results-only" (their free-form text, if any, is ignored).
		if n.Role == "user" && len(n.ToolResults) > 0 {
			n.Text = ""
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

func buildCurrentUserMessage(content string, modelID string, tools []KiroTool, toolResults []ToolResult) KiroCurrentMessage {
	if strings.TrimSpace(content) == "" && len(toolResults) == 0 {
		content = "Continue"
	}

	userInput := &UserInputMessage{
		Content: content,
		ModelID: modelID,
		Origin:  "AI_EDITOR",
	}

	if len(tools) > 0 || len(toolResults) > 0 {
		userInput.UserInputMessageContext = &UserInputMessageContext{}
		if len(tools) > 0 {
			userInput.UserInputMessageContext.Tools = tools
		}
		if len(toolResults) > 0 {
			userInput.UserInputMessageContext.ToolResults = toolResults
		}
	}

	return KiroCurrentMessage{
		UserInputMessage: userInput,
	}
}

func buildHistoryMessage(msg normalizedClaudeMessage, modelID string, systemPrefix string) KiroHistoryMessage {
	switch msg.Role {
	case "assistant":
		content := msg.Text
		return KiroHistoryMessage{
			AssistantResponseMessage: &AssistantResponseMessage{
				Content:  content,
				ToolUses: msg.ToolUses,
			},
		}

	default:
		// Treat any non-assistant history as a user message.
		content := msg.Text
		if systemPrefix != "" {
			content = systemPrefix + "\n\n" + content
		}

		userInput := &UserInputMessage{
			Content: content,
			ModelID: modelID,
			Origin:  "AI_EDITOR",
		}

		if len(msg.ToolResults) > 0 {
			userInput.UserInputMessageContext = &UserInputMessageContext{
				ToolResults: msg.ToolResults,
			}
		}

		// 添加图片
		if len(msg.Images) > 0 {
			userInput.Images = msg.Images
		}

		return KiroHistoryMessage{
			UserInputMessage: userInput,
		}
	}
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

// convertToCurrentMessage converts a Claude message to Kiro current message format
func convertToCurrentMessage(msg map[string]interface{}, modelID string, systemPrompt string, claudeReq map[string]interface{}) KiroCurrentMessage {
	content := extractMessageContent(msg)

	// Prepend system prompt if present
	if systemPrompt != "" {
		content = systemPrompt + "\n\n" + content
	}
	if strings.TrimSpace(content) == "" {
		content = "Continue"
	}

	userInput := &UserInputMessage{
		Content: content,
		ModelID: modelID,
		Origin:  "AI_EDITOR",
	}

	// Add tools if present
	if tools, ok := claudeReq["tools"].([]interface{}); ok && len(tools) > 0 {
		userInput.UserInputMessageContext = &UserInputMessageContext{
			Tools: convertClaudeToolsToKiro(tools),
		}
	}

	// Check for tool results in the message
	if toolResults := extractToolResults(msg); len(toolResults) > 0 {
		if userInput.UserInputMessageContext == nil {
			userInput.UserInputMessageContext = &UserInputMessageContext{}
		}
		userInput.UserInputMessageContext.ToolResults = toolResults
	}

	return KiroCurrentMessage{
		UserInputMessage: userInput,
	}
}

// convertToHistoryMessage converts a Claude message to Kiro history message format
func convertToHistoryMessage(msg map[string]interface{}, modelID string) KiroHistoryMessage {
	role := shared.StringFromAny(msg["role"])

	if role == "assistant" {
		content := extractMessageContent(msg)
		toolUses := extractToolUses(msg)

		return KiroHistoryMessage{
			AssistantResponseMessage: &AssistantResponseMessage{
				Content:  content,
				ToolUses: toolUses,
			},
		}
	}

	// User message
	content := extractMessageContent(msg)
	if strings.TrimSpace(content) == "" {
		content = "Continue"
	}
	userInput := &UserInputMessage{
		Content: content,
		ModelID: modelID,
		Origin:  "AI_EDITOR",
	}

	// Check for tool results
	if toolResults := extractToolResults(msg); len(toolResults) > 0 {
		userInput.UserInputMessageContext = &UserInputMessageContext{
			ToolResults: toolResults,
		}
	}

	return KiroHistoryMessage{
		UserInputMessage: userInput,
	}
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
		// Keep parity with the reference Python gateway for Anthropic content blocks:
		// collect relevant content segments and join them with "\n".
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
				case "thinking":
					if thinking, ok := v["thinking"].(string); ok && thinking != "" {
						parts = append(parts, "<thinking>"+thinking+"</thinking>")
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
				// 过滤不支持的工具：web_search (静默过滤，不发送到上游)
				if name == "web_search" || name == "websearch" {
					continue
				}
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
			// 过滤不支持的工具：web_search (静默过滤，不发送到上游)
			if name == "web_search" || name == "websearch" {
				continue
			}
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
			}

			// Keep parity with the Python gateway:
			// tool_result content is normalized into a single text item (with "\n" between blocks).
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
// Note: it does not apply any description-length limits; use convertClaudeToolsToKiroWithToolDocs when forwarding to Kiro.
func convertClaudeToolsToKiro(tools []interface{}) []KiroTool {
	var kiroTools []KiroTool

	for _, tool := range tools {
		toolMap, ok := tool.(map[string]interface{})
		if !ok {
			continue
		}

		kiroTool := KiroTool{
			ToolSpecification: ToolSpecification{
				Name:        shared.StringFromAny(toolMap["name"]),
				Description: shared.StringFromAny(toolMap["description"]),
			},
		}

		if inputSchema, ok := toolMap["input_schema"].(map[string]interface{}); ok {
			kiroTool.ToolSpecification.InputSchema = InputSchema{
				JSON: inputSchema,
			}
		}

		kiroTools = append(kiroTools, kiroTool)
	}

	return kiroTools
}

func convertClaudeToolsToKiroWithToolDocs(tools []interface{}, maxDescriptionLength int) ([]KiroTool, string) {
	var kiroTools []KiroTool
	if len(tools) == 0 {
		return nil, ""
	}

	if maxDescriptionLength < 0 {
		maxDescriptionLength = 0
	}

	var toolDocumentationParts []string

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

		// 过滤不支持的工具：web_search (静默过滤，不发送到上游)
		if name == "web_search" || name == "websearch" {
			continue
		}

		if maxDescriptionLength > 0 && len(description) > maxDescriptionLength && strings.TrimSpace(name) != "" {
			toolDocumentationParts = append(toolDocumentationParts, "## Tool: "+name+"\n\n"+description)
			description = "[Full documentation in system prompt under '## Tool: " + name + "']"
		}

		kiroTool := KiroTool{
			ToolSpecification: ToolSpecification{
				Name:        name,
				Description: description,
			},
		}

		if inputSchema != nil {
			kiroTool.ToolSpecification.InputSchema = InputSchema{
				JSON: inputSchema,
			}
		}

		kiroTools = append(kiroTools, kiroTool)
	}

	if len(toolDocumentationParts) == 0 {
		return kiroTools, ""
	}

	toolDoc := "\n\n---\n" +
		"# Tool Documentation\n" +
		"The following tools have detailed documentation that couldn't fit in the tool definition.\n\n" +
		strings.Join(toolDocumentationParts, "\n\n---\n\n")
	return kiroTools, toolDoc
}

func appendToolDocumentationToSystemPrompt(systemPrompt string, toolDoc string) string {
	// Keep parity with the reference converter: preserve the leading `\n\n---\n` on tool docs.
	systemPrompt = strings.TrimRight(systemPrompt, "\r\n")
	if strings.TrimSpace(toolDoc) == "" {
		return systemPrompt
	}
	if systemPrompt == "" {
		return strings.TrimLeft(toolDoc, "\r\n")
	}
	return systemPrompt + toolDoc
}

// generateConversationID generates a unique conversation ID
func generateConversationID() string {
	return uuid.NewString()
}

// ensureMessageStarted ensures message_start event has been sent
func ensureMessageStarted(state *StreamState, outputs *[]string) {
	if state == nil || state.Started {
		return
	}
	state.Started = true
	*outputs = append(*outputs, buildMessageStart(state.MessageID, state.Model, state.InputTokens))

	// Rust parity: when thinking is not enabled, start the initial text block immediately
	// after message_start (even before the first text delta arrives).
	if !state.ThinkingEnabled && !state.ContentBlockOpen && !state.ToolUseBlockOpen && !state.ToolBlockOpen && !state.ThinkingBlockOpen {
		*outputs = append(*outputs, buildContentBlockStart(state.ContentIndex, "text"))
		state.ContentBlockOpen = true
	}
}

// ensureTextBlockOpen ensures a text content block is open
func ensureTextBlockOpen(state *StreamState, outputs *[]string) {
	if state == nil || state.ContentBlockOpen || state.ToolUseBlockOpen || state.ToolBlockOpen || state.ThinkingBlockOpen {
		return
	}
	*outputs = append(*outputs, buildContentBlockStart(state.ContentIndex, "text"))
	state.ContentBlockOpen = true
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
		// Rust parity: send an empty thinking_delta before closing.
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
				if beforeThinking != "" {
					emitText(beforeThinking)
				}

				// Consume the start tag and enter the thinking block.
				state.ThinkingBuffer = state.ThinkingBuffer[startPos+len(thinkingStartTag):]
				startThinkingBlock()
				continue
			}

			// No start tag found: flush safe prefix but keep enough bytes to detect a partial tag.
			target := len(state.ThinkingBuffer) - len(thinkingStartTag)
			if target <= 0 {
				break
			}
			safe := findCharBoundary(state.ThinkingBuffer, target)
			if safe > 0 {
				emitText(state.ThinkingBuffer[:safe])
				state.ThinkingBuffer = state.ThinkingBuffer[safe:]
			}
			break
		}

		if state.InThinkingBlock {
			endPos := findRealThinkingEndTag(state.ThinkingBuffer)
			if endPos >= 0 {
				thinking := state.ThinkingBuffer[:endPos]
				if thinking != "" {
					emitThinkingDelta(thinking)
				}

				// Consume only the end tag, leaving the trailing "\n\n" for text output.
				state.ThinkingBuffer = state.ThinkingBuffer[endPos+len(thinkingEndTag):]
				stopThinkingBlock()
				continue
			}

			// No end tag found: flush safe prefix but keep enough bytes to detect a partial end tag.
			target := len(state.ThinkingBuffer) - len(thinkingEndTag)
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

	// Replay/rollback: don't emit anything
	if strings.HasPrefix(state.RawTextSoFar, content) {
		return ""
	}

	// Fallback: treat as a delta chunk and append
	state.RawTextSoFar += content
	return content
}

const defaultMaxInputTokens = 200000

func estimateTokens(text string) int {
	if strings.TrimSpace(text) == "" {
		return 0
	}
	ascii := 0
	nonASCII := 0
	for _, r := range text {
		if r <= 0x7f {
			ascii++
		} else {
			nonASCII++
		}
	}
	// Heuristic token estimate:
	// - ASCII-heavy text ~ 4 chars/token
	// - Non-ASCII (e.g., CJK) ~ 2 chars/token
	tokens := (ascii + 3) / 4
	tokens += (nonASCII + 1) / 2
	return tokens
}

func applyTokenAccounting(state *StreamState) {
	if state == nil {
		return
	}

	// Kiro streams often don't provide output token counts; approximate from accumulated text.
	if state.OutputTokens == 0 && (strings.TrimSpace(state.TextSoFar) != "" || strings.TrimSpace(state.ThinkingSoFar) != "") {
		combined := state.TextSoFar + state.ThinkingSoFar
		state.OutputTokens = estimateTokens(combined)
	}

	// When context usage percentage is present, derive prompt tokens by subtraction.
	if state.ContextUsagePct > 0 {
		totalTokens := int((state.ContextUsagePct / 100.0) * float64(defaultMaxInputTokens))
		if totalTokens < 0 {
			totalTokens = 0
		}
		promptTokens := totalTokens - state.OutputTokens
		if promptTokens < 0 {
			promptTokens = 0
		}
		if promptTokens > 0 {
			state.InputTokens = promptTokens
		}
	}
}

// KiroStreamToClaudeSSE converts Kiro stream events to Claude SSE format
func KiroStreamToClaudeSSE(event *kirotypes.StreamEvent, state *StreamState) ([]string, error) {
	var outputs []string

	flushToolArgsDeltaIfValid := func() {
		if state == nil || !state.ToolBlockOpen {
			return
		}
		if strings.TrimSpace(state.ToolUseArgs) == "" {
			return
		}

		// Match the expected output fixture: emit a single input_json_delta only when the
		// accumulated argument string is valid JSON (so we don't stream broken fragments).
		var tmp any
		if err := json.Unmarshal([]byte(state.ToolUseArgs), &tmp); err != nil {
			return
		}
		outputs = append(outputs, buildToolUseInputDelta(state.CurrentToolBlock, state.ToolUseArgs))
	}

	finalizeToolUse := func() {
		if state == nil {
			return
		}
		if strings.TrimSpace(state.CurrentToolUseID) == "" && strings.TrimSpace(state.CurrentToolName) == "" && strings.TrimSpace(state.ToolUseArgs) == "" {
			return
		}

		var input any = map[string]any{}
		args := strings.TrimSpace(state.ToolUseArgs)
		if args != "" {
			var obj any
			if err := json.Unmarshal([]byte(args), &obj); err == nil {
				input = obj
			} else {
				// Keep parity with the Python gateway: if tool args aren't valid JSON, treat them as empty.
				input = map[string]any{}
			}
		}

		if state.CollectedToolUses == nil {
			state.CollectedToolUses = make([]map[string]any, 0, 1)
		}
		state.CollectedToolUses = append(state.CollectedToolUses, map[string]any{
			"id":       state.CurrentToolUseID,
			"name":     state.CurrentToolName,
			"input":    input,
			"args_raw": args,
		})

		state.CurrentToolUseID = ""
		state.CurrentToolName = ""
		state.ToolUseArgs = ""
	}

	startToolBlock := func(toolUseID, toolName string) {
		if state == nil {
			return
		}
		if state.ToolBlockOpen {
			return
		}
		if strings.TrimSpace(toolUseID) == "" {
			return
		}
		outputs = append(outputs, buildToolUseBlockStart(state.ContentIndex, toolUseID, toolName))
		state.CurrentToolBlock = state.ContentIndex
		state.ToolBlockOpen = true
		state.ToolBlocksStreamed = true
	}

	stopToolBlock := func() {
		if state == nil || !state.ToolBlockOpen {
			return
		}
		outputs = append(outputs, buildContentBlockStop(state.CurrentToolBlock))
		state.ToolBlockOpen = false
		state.CurrentToolBlock = -1
		state.ContentIndex++
	}

	switch event.Type {
	case kirotypes.StreamEventContent:
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

	case kirotypes.StreamEventToolStart:
		data, ok := event.Data.(map[string]interface{})
		if !ok {
			return nil, nil
		}

		ensureMessageStarted(state, &outputs)

		// Close text content block if open (Python gateway closes the text block before emitting tool_use blocks).
		if state.ContentBlockOpen {
			outputs = append(outputs, buildContentBlockStop(state.ContentIndex))
			state.ContentBlockOpen = false
			state.ContentIndex++
		}

		// Close thinking block if the upstream started a tool call mid-thinking.
		if state.ThinkingBlockOpen {
			outputs = append(outputs, buildThinkingDelta(state.ThinkingBlockIndex, ""))
			outputs = append(outputs, buildContentBlockStop(state.ThinkingBlockIndex))
			state.ThinkingBlockOpen = false
			state.InThinkingBlock = false
			state.ThinkingExtracted = true
			state.ContentIndex = state.ThinkingBlockIndex + 1
			state.ThinkingBlockIndex = -1
		}

		// If a previous tool call didn't emit a stop marker, finalize it before starting a new one.
		if state.ToolUseBlockOpen {
			finalizeToolUse()
			stopToolBlock()
			state.ToolUseBlockOpen = false
		}

		state.CurrentToolUseID = shared.StringFromAny(data["toolUseId"])
		state.CurrentToolName = shared.StringFromAny(data["name"])
		state.ToolUseArgs = ""
		state.ToolUseBlockOpen = true
		startToolBlock(state.CurrentToolUseID, state.CurrentToolName)

	case kirotypes.StreamEventToolInput:
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

			// If the stream doesn't send explicit tool_start events, a new toolUseId in an input
			// chunk should be treated as a new tool call. This mirrors the Python parser, which
			// finalizes any in-flight tool call when a new one starts.
			if state.ToolUseBlockOpen && incomingID != "" && strings.TrimSpace(state.CurrentToolUseID) != "" && incomingID != state.CurrentToolUseID {
				finalizeToolUse()
				stopToolBlock()
				state.ToolUseBlockOpen = false
			}

			data = m["input"]

			// Some streams may deliver input chunks without a preceding explicit tool_start.
			// When that happens, start tracking the tool call based on the chunk metadata.
			if !state.ToolUseBlockOpen && incomingID != "" {
				ensureMessageStarted(state, &outputs)

				// Close text content block if open.
				if state.ContentBlockOpen {
					outputs = append(outputs, buildContentBlockStop(state.ContentIndex))
					state.ContentBlockOpen = false
					state.ContentIndex++
				}

				state.CurrentToolUseID = incomingID
				state.CurrentToolName = incomingName
				state.ToolUseArgs = ""
				state.ToolUseBlockOpen = true
				startToolBlock(state.CurrentToolUseID, state.CurrentToolName)
			}
		}

		if !state.ToolUseBlockOpen {
			return nil, nil
		}

		// Accumulate tool input
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
			flushToolArgsDeltaIfValid()
			finalizeToolUse()
			state.ToolUseBlockOpen = false
			stopToolBlock()
		}

	case kirotypes.StreamEventToolStop:
		if state.ToolUseBlockOpen {
			flushToolArgsDeltaIfValid()
			finalizeToolUse()
			state.ToolUseBlockOpen = false
			stopToolBlock()
		}

	case kirotypes.StreamEventToolUses:
		ensureMessageStarted(state, &outputs)

		// Handle complete tool uses from assistantResponseMessage
		toolUses, ok := event.Data.([]interface{})
		if !ok {
			return nil, nil
		}

		// Replace collected tool uses with the authoritative complete list when present.
		state.CollectedToolUses = state.CollectedToolUses[:0]

		for _, tu := range toolUses {
			tuMap, ok := tu.(map[string]interface{})
			if !ok {
				continue
			}

			toolUseID := shared.StringFromAny(tuMap["toolUseId"])
			toolName := shared.StringFromAny(tuMap["name"])
			input := tuMap["input"]

			state.CollectedToolUses = append(state.CollectedToolUses, map[string]any{
				"id":       toolUseID,
				"name":     toolName,
				"input":    input,
				"args_raw": "",
			})
		}

	case kirotypes.StreamEventUsage:
		usage, ok := event.Data.(map[string]interface{})
		if !ok {
			return nil, nil
		}

		// Extract token counts
		if v := usage["inputTokens"]; v != nil {
			state.InputTokens = shared.IntFromAny(v)
		} else if v := usage["input_tokens"]; v != nil {
			state.InputTokens = shared.IntFromAny(v)
		}
		if v := usage["outputTokens"]; v != nil {
			state.OutputTokens = shared.IntFromAny(v)
		} else if v := usage["output_tokens"]; v != nil {
			state.OutputTokens = shared.IntFromAny(v)
		}

	case kirotypes.StreamEventContextUsage:
		if state == nil {
			return nil, nil
		}
		switch v := event.Data.(type) {
		case float64:
			state.ContextUsagePct = v
		case float32:
			state.ContextUsagePct = float64(v)
		case int:
			state.ContextUsagePct = float64(v)
		case int64:
			state.ContextUsagePct = float64(v)
		case json.Number:
			if f, err := v.Float64(); err == nil {
				state.ContextUsagePct = f
			}
		}

	case kirotypes.StreamEventStopReason:
		stopReason, ok := event.Data.(string)
		if ok {
			state.FinishReason = mapKiroStopReason(stopReason)
		}

	case kirotypes.StreamEventError:
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
		// If the upstream ended without a tool_stop marker, finalize the tool use so that
		// non-stream collection can still return the tool_use content.
		if state.ToolUseBlockOpen {
			// Best-effort parse of arguments accumulated in ToolUseArgs.
			var input any = map[string]any{}
			args := strings.TrimSpace(state.ToolUseArgs)
			if args != "" {
				var obj any
				if err := json.Unmarshal([]byte(args), &obj); err == nil {
					input = obj
				}
			}
			if strings.TrimSpace(state.CurrentToolUseID) != "" || strings.TrimSpace(state.CurrentToolName) != "" || args != "" {
				state.CollectedToolUses = append(state.CollectedToolUses, map[string]any{
					"id":    state.CurrentToolUseID,
					"name":  state.CurrentToolName,
					"input": input,
				})
			}
			state.CurrentToolUseID = ""
			state.CurrentToolName = ""
			state.ToolUseArgs = ""
		}
	}

	ensureMessageStarted(state, &outputs)

	// Flush any pending thinking buffer and close an open thinking block (Rust parity).
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
		} else {
			emitText(state.ThinkingBuffer)
			state.ThinkingBuffer = ""
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

	// Emit tool_use blocks at the end only when we didn't already stream tool_use blocks.
	if state != nil && !state.ToolBlocksStreamed && len(state.CollectedToolUses) > 0 {
		// Deduplicate by toolUseId, preferring non-empty args.
		type chosen struct {
			firstIdx int
			argsLen  int
			hasInput bool
			m        map[string]any
		}
		byID := map[string]chosen{}
		orderedIDs := make([]string, 0, len(state.CollectedToolUses))

		for idx, tu := range state.CollectedToolUses {
			if tu == nil {
				continue
			}
			id, _ := tu["id"].(string)
			id = strings.TrimSpace(id)
			if id == "" {
				continue
			}
			argsRaw, _ := tu["args_raw"].(string)
			argsRaw = strings.TrimSpace(argsRaw)
			input := tu["input"]
			hasInput := false
			if m, ok := input.(map[string]any); ok && len(m) > 0 {
				hasInput = true
			}
			cand := chosen{
				firstIdx: idx,
				argsLen:  len(argsRaw),
				hasInput: hasInput,
				m:        tu,
			}
			if existing, ok := byID[id]; ok {
				// Prefer non-empty inputs; otherwise longer args.
				better := false
				if cand.hasInput && !existing.hasInput {
					better = true
				} else if cand.hasInput == existing.hasInput && cand.argsLen > existing.argsLen {
					better = true
				}
				if better {
					cand.firstIdx = existing.firstIdx
					byID[id] = cand
				}
				continue
			}
			byID[id] = cand
			orderedIDs = append(orderedIDs, id)
		}

		for _, id := range orderedIDs {
			c := byID[id]
			tu := c.m
			toolName, _ := tu["name"].(string)
			toolName = strings.TrimSpace(toolName)
			argsRaw, _ := tu["args_raw"].(string)
			argsRaw = strings.TrimSpace(argsRaw)
			input := tu["input"]

			outputs = append(outputs, buildToolUseBlockStart(state.ContentIndex, id, toolName))

			// Python only emits input_json_delta when tool_input parses to a non-empty dict.
			if m, ok := input.(map[string]any); ok && len(m) > 0 {
				partial := argsRaw
				if partial == "" {
					// Fall back to compact JSON if we don't have the raw string.
					if b, err := json.Marshal(m); err == nil {
						partial = string(b)
					}
				}
				if partial != "" {
					outputs = append(outputs, buildToolUseInputDelta(state.ContentIndex, partial))
				}
			}

			outputs = append(outputs, buildContentBlockStop(state.ContentIndex))
			state.ContentIndex++
		}
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
				"input_tokens":  inputTokens,
				"output_tokens": 0,
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
		// 更新工具调用状态
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
