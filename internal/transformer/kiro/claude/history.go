package claude

import (
	"fmt"
	"strings"

	"clisimplehub/internal/transformer/shared"
)

const thinkingHint = "<antml:thinking_mode>interleaved</antml:thinking_mode><antml:max_thinking_length>16000</antml:max_thinking_length>"

// buildHistory builds Kiro conversation history from Claude/OpenAI messages,
// - history must strictly alternate user/assistant
// - toolResults are deduplicated/merged by toolUseId (error propagates)
// - toolResults are reordered to match the previous assistant toolUses order
// - consecutive user messages are merged using buffer mode (images keep last 2 image-containing messages)
// - thinking tags are injected directly into system message
func buildHistory(conversationMsgs []map[string]interface{}, modelID string, systemPrompt string, claudeReq map[string]interface{}) ([]KiroHistoryMessage, error) {
	var history []KiroHistoryMessage

	// 生成 thinking 前缀并注入到系统消息
	thinkingPrefix := buildKiroThinkingSystemPrefix(claudeReq)
	systemContent := strings.TrimSpace(systemPrompt)

	// 如果有 thinking 前缀且系统消息中没有 thinking 标签，则注入
	if strings.TrimSpace(thinkingPrefix) != "" && !systemHasKiroThinkingTags(systemContent) {
		if systemContent == "" {
			systemContent = thinkingPrefix
		} else {
			systemContent = thinkingPrefix + "\n" + systemContent
		}
	}

	// 系统消息作为 user + assistant 配对
	if systemContent != "" {
		history = append(history, buildHistoryUserMessage(systemContent, modelID, nil, nil))
		history = append(history, buildHistoryAssistantMessage(systemAssistantAck, nil))
	}

	if len(conversationMsgs) == 0 {
		return history, nil
	}

	// 确定历史范围：最后一条消息作为 currentMessage，不加入历史
	last := conversationMsgs[len(conversationMsgs)-1]
	lastRole := strings.ToLower(strings.TrimSpace(shared.StringFromAny(last["role"])))

	historyEndIndex := len(conversationMsgs) - 1
	if lastRole == "assistant" {
		historyEndIndex = len(conversationMsgs)
	}
	if historyEndIndex < 0 {
		historyEndIndex = 0
	}

	// 使用 buffer 模式处理消息，避免多次遍历
	raw, err := processHistoryMessages(conversationMsgs[:historyEndIndex], modelID)
	if err != nil {
		return nil, err
	}
	history = append(history, raw...)

	// Kiro 要求历史以 assistant 结尾（当 currentMessage 是 user 时）
	if len(history) > 0 && history[len(history)-1].UserInputMessage != nil {
		history = append(history, buildHistoryAssistantMessage(systemOrphanOK, nil))
	}

	if err := validateHistoryAlternation(history); err != nil {
		return nil, err
	}
	return history, nil
}

// processHistoryMessages 使用 buffer 模式处理消息，一次遍历完成合并
func processHistoryMessages(messages []map[string]interface{}, modelID string) ([]KiroHistoryMessage, error) {
	if len(messages) == 0 {
		return nil, nil
	}

	var history []KiroHistoryMessage
	var userBuffer []map[string]interface{}
	var lastToolUseOrder []string

	for _, msg := range messages {
		if msg == nil {
			continue
		}
		role := strings.ToLower(strings.TrimSpace(shared.StringFromAny(msg["role"])))

		if role == "user" || role == "tool" {
			// 收集 user 消息到 buffer
			userBuffer = append(userBuffer, msg)
		} else if role == "assistant" {
			// 没有待处理的 user/tool 时，跳过该 assistant，避免连续 assistant 导致历史非法
			if len(userBuffer) == 0 {
				continue
			}

			// 遇到 assistant，处理累积的 user 消息
			merged, err := mergeUserMessagesFromBuffer(userBuffer, modelID, lastToolUseOrder)
			if err != nil {
				return nil, err
			}
			history = append(history, merged)
			userBuffer = nil

			// 添加 assistant 消息
			content, _ := extractAssistantContent(msg)
			toolUses := extractToolUses(msg)
			if strings.TrimSpace(content) == "" && len(toolUses) > 0 {
				content = "There is a tool use."
			}
			history = append(history, buildHistoryAssistantMessage(content, toolUses))
			lastToolUseOrder = toolUseIDOrder(toolUses)
		}
	}

	// 处理结尾的孤立 user 消息
	if len(userBuffer) > 0 {
		merged, err := mergeUserMessagesFromBuffer(userBuffer, modelID, lastToolUseOrder)
		if err != nil {
			return nil, err
		}
		history = append(history, merged)
	}

	return history, nil
}

// mergeUserMessagesFromBuffer 合并 buffer 中的多个 user 消息
func mergeUserMessagesFromBuffer(buffer []map[string]interface{}, modelID string, lastToolUseOrder []string) (KiroHistoryMessage, error) {
	if len(buffer) == 0 {
		return KiroHistoryMessage{}, fmt.Errorf("empty user buffer")
	}

	// 如果只有一条消息，直接处理
	if len(buffer) == 1 {
		msg := buffer[0]
		role := strings.ToLower(strings.TrimSpace(shared.StringFromAny(msg["role"])))

		if role == "tool" {
			var toolResults []ToolResult
			if r := toolMessageToToolResult(msg); r != nil {
				toolResults = []ToolResult{*r}
			}
			toolResults = mergeToolResultsByToolUseID(toolResults)
			if len(toolResults) > 0 && len(lastToolUseOrder) > 0 {
				toolResults = reorderToolResultsByToolUses(toolResults, lastToolUseOrder)
			}
			return buildHistoryUserMessage("", modelID, nil, toolResults), nil
		}

		text, images := extractMessageContentWithImages(msg)
		toolResults := mergeToolResultsByToolUseID(extractToolResults(msg))
		if len(toolResults) > 0 && len(lastToolUseOrder) > 0 {
			toolResults = reorderToolResultsByToolUses(toolResults, lastToolUseOrder)
		}
		return buildHistoryUserMessage(text, modelID, images, toolResults), nil
	}

	// 合并多条消息
	var contentParts []string
	var toolResults []ToolResult
	var imageSets [][]KiroImage
	hadThinkingHint := false

	for _, msg := range buffer {
		role := strings.ToLower(strings.TrimSpace(shared.StringFromAny(msg["role"])))

		if role == "tool" {
			if r := toolMessageToToolResult(msg); r != nil {
				toolResults = append(toolResults, *r)
			}
			continue
		}

		// user 消息
		text, images := extractMessageContentWithImages(msg)
		clean, had := stripThinkingHint(text, thinkingHint)
		hadThinkingHint = hadThinkingHint || had

		if strings.TrimSpace(clean) != "" {
			contentParts = append(contentParts, clean)
		}

		if len(images) > 0 {
			imageSets = append(imageSets, images)
			// 只保留最后 2 个包含图片的消息
			if len(imageSets) > 2 {
				imageSets = imageSets[len(imageSets)-2:]
			}
		}

		// 收集 tool_results
		results := extractToolResults(msg)
		if len(results) > 0 {
			toolResults = append(toolResults, results...)
		}
	}

	// 合并图片
	var keptImages []KiroImage
	for _, set := range imageSets {
		keptImages = append(keptImages, set...)
	}

	// 合并文本
	content := strings.Join(contentParts, "\n\n")
	if hadThinkingHint {
		content = appendThinkingHint(content, thinkingHint)
	}

	// 处理 tool_results：去重和重排序
	toolResults = mergeToolResultsByToolUseID(toolResults)
	if len(toolResults) > 0 && len(lastToolUseOrder) > 0 {
		toolResults = reorderToolResultsByToolUses(toolResults, lastToolUseOrder)
	}

	return buildHistoryUserMessage(content, modelID, keptImages, toolResults), nil
}

func stripThinkingHint(text, hint string) (clean string, had bool) {
	if strings.TrimSpace(hint) == "" || strings.TrimSpace(text) == "" {
		return text, false
	}
	if !strings.Contains(text, hint) {
		return text, false
	}
	return strings.ReplaceAll(text, hint, ""), true
}

func appendThinkingHint(text, hint string) string {
	if strings.TrimSpace(hint) == "" {
		return text
	}
	text = strings.TrimRight(text, "\r\n")
	if text == "" {
		return hint
	}
	return text + "\n\n" + hint
}

func normalizeHistoryToolResults(history []KiroHistoryMessage) []KiroHistoryMessage {
	var lastToolUseOrder []string
	for i := range history {
		if arm := history[i].AssistantResponseMessage; arm != nil {
			lastToolUseOrder = toolUseIDOrder(arm.ToolUses)
			continue
		}
		u := history[i].UserInputMessage
		if u == nil || u.UserInputMessageContext == nil || len(u.UserInputMessageContext.ToolResults) == 0 {
			continue
		}
		u.UserInputMessageContext.ToolResults = mergeToolResultsByToolUseID(u.UserInputMessageContext.ToolResults)
		if len(lastToolUseOrder) > 0 {
			u.UserInputMessageContext.ToolResults = reorderToolResultsByToolUses(u.UserInputMessageContext.ToolResults, lastToolUseOrder)
		}
	}
	return history
}

func validateHistoryAlternation(history []KiroHistoryMessage) error {
	prevRole := ""
	for i := 0; i < len(history); i++ {
		role, ok := historyMessageRole(history, i)
		if !ok {
			return fmt.Errorf("invalid history entry at index %d: missing message body", i)
		}
		if prevRole != "" && role == prevRole {
			return fmt.Errorf("invalid history alternation at index %d: consecutive %s", i, role)
		}
		prevRole = role
	}
	return nil
}

func historyMessageRole(history []KiroHistoryMessage, index int) (string, bool) {
	if index < 0 || index >= len(history) {
		return "", false
	}
	msg := history[index]

	hasUser := msg.UserInputMessage != nil
	hasAssistant := msg.AssistantResponseMessage != nil

	if hasUser == hasAssistant {
		return "", false
	}
	if hasUser {
		return "user", true
	}
	return "assistant", true
}

func toolUseIDOrder(toolUses []ToolUse) []string {
	if len(toolUses) == 0 {
		return nil
	}
	out := make([]string, 0, len(toolUses))
	for _, tu := range toolUses {
		id := strings.TrimSpace(tu.ToolUseID)
		if id == "" {
			continue
		}
		out = append(out, id)
	}
	return out
}

func lastToolUseOrderFromHistory(history []KiroHistoryMessage) []string {
	for i := len(history) - 1; i >= 0; i-- {
		if history[i].AssistantResponseMessage == nil {
			continue
		}
		if order := toolUseIDOrder(history[i].AssistantResponseMessage.ToolUses); len(order) > 0 {
			return order
		}
	}
	return nil
}

func mergeToolResultsByToolUseID(toolResults []ToolResult) []ToolResult {
	if len(toolResults) <= 1 {
		return toolResults
	}

	var out []ToolResult
	indexByID := make(map[string]int, len(toolResults))

	for _, r := range toolResults {
		id := strings.TrimSpace(r.ToolUseID)
		if id == "" {
			out = append(out, r)
			continue
		}
		if ix, ok := indexByID[id]; ok {
			mergeToolResultInto(&out[ix], r)
			continue
		}
		indexByID[id] = len(out)
		out = append(out, r)
	}

	return out
}

func mergeToolResultInto(dst *ToolResult, src ToolResult) {
	if dst == nil {
		return
	}

	dst.Content = mergeToolResultContents(dst.Content, src.Content)

	srcError := src.IsError || strings.EqualFold(strings.TrimSpace(src.Status), "error")
	dst.IsError = dst.IsError || srcError || strings.EqualFold(strings.TrimSpace(dst.Status), "error")
	if dst.IsError {
		dst.Status = "error"
	} else {
		dst.Status = "success"
	}
}

func mergeToolResultContents(a, b []ToolResultContent) []ToolResultContent {
	if len(a) == 0 {
		return append([]ToolResultContent(nil), b...)
	}
	if len(b) == 0 {
		return a
	}

	seen := make(map[string]struct{}, len(a)+len(b))
	for _, item := range a {
		seen[item.Text] = struct{}{}
	}

	for _, item := range b {
		if _, ok := seen[item.Text]; ok {
			continue
		}
		seen[item.Text] = struct{}{}
		a = append(a, item)
	}
	return a
}

func reorderToolResultsByToolUses(toolResults []ToolResult, toolUseOrder []string) []ToolResult {
	if len(toolResults) <= 1 || len(toolUseOrder) == 0 {
		return toolResults
	}

	resultsByID := make(map[string]ToolResult, len(toolResults))
	for _, r := range toolResults {
		id := strings.TrimSpace(r.ToolUseID)
		if id == "" {
			continue
		}
		resultsByID[id] = r
	}

	used := make(map[string]struct{}, len(toolUseOrder))
	ordered := make([]ToolResult, 0, len(toolResults))

	for _, id := range toolUseOrder {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		r, ok := resultsByID[id]
		if !ok {
			continue
		}
		ordered = append(ordered, r)
		used[id] = struct{}{}
	}

	// Append remaining results in their original order.
	for _, r := range toolResults {
		id := strings.TrimSpace(r.ToolUseID)
		if id != "" {
			if _, ok := used[id]; ok {
				continue
			}
		}
		ordered = append(ordered, r)
	}

	return ordered
}

func cloneUserInputMessage(in *UserInputMessage) *UserInputMessage {
	if in == nil {
		return nil
	}
	out := &UserInputMessage{
		Content: in.Content,
		ModelID: in.ModelID,
		Origin:  in.Origin,
	}
	if len(in.Images) > 0 {
		out.Images = append([]KiroImage(nil), in.Images...)
	}
	if in.UserInputMessageContext != nil {
		ctx := &UserInputMessageContext{}
		if len(in.UserInputMessageContext.Tools) > 0 {
			ctx.Tools = append([]KiroTool(nil), in.UserInputMessageContext.Tools...)
		}
		if len(in.UserInputMessageContext.ToolResults) > 0 {
			ctx.ToolResults = append([]ToolResult(nil), in.UserInputMessageContext.ToolResults...)
		}
		out.UserInputMessageContext = ctx
	}
	return out
}

func buildHistoryUserMessage(content string, modelID string, images []KiroImage, toolResults []ToolResult) KiroHistoryMessage {
	userInput := &UserInputMessage{
		Content: content,
		ModelID: modelID,
		Origin:  "AI_EDITOR",
	}

	if len(images) > 0 {
		userInput.Images = images
	}
	if len(toolResults) > 0 {
		userInput.UserInputMessageContext = &UserInputMessageContext{
			ToolResults: toolResults,
		}
	}

	return KiroHistoryMessage{
		UserInputMessage: userInput,
	}
}

func buildHistoryAssistantMessage(content string, toolUses []ToolUse) KiroHistoryMessage {
	resp := &AssistantResponseMessage{
		Content: content,
	}
	if len(toolUses) > 0 {
		resp.ToolUses = toolUses
	}
	return KiroHistoryMessage{
		AssistantResponseMessage: resp,
	}
}

func toolMessageToToolResult(msg map[string]interface{}) *ToolResult {
	toolCallID := strings.TrimSpace(shared.StringFromAny(msg["tool_call_id"]))
	if toolCallID == "" {
		return nil
	}
	resultText := extractMessageContent(msg)
	if strings.TrimSpace(resultText) == "" {
		resultText = "(empty result)"
	}
	return &ToolResult{
		ToolUseID: toolCallID,
		Status:    "success",
		Content:   []ToolResultContent{{Text: resultText}},
		IsError:   false,
	}
}
