package converters

import "strings"

const (
	historyAssistantOK = "OK"
)

func shouldAssignAssistantMessageID(content string, toolUses []KiroToolUse) bool {
	if len(toolUses) > 0 {
		return true
	}
	trimmed := strings.TrimSpace(content)
	if trimmed == "" {
		return false
	}
	return trimmed != systemAssistantAck && trimmed != historyAssistantOK
}

func mergeUserHistoryMessage(base *UnifiedMessage, next UnifiedMessage) {
	switch {
	case strings.TrimSpace(base.Content) == "":
		base.Content = next.Content
	case strings.TrimSpace(next.Content) == "":
		// Keep base content unchanged.
	default:
		base.Content = base.Content + "\n" + next.Content
	}
	if len(next.Images) > 0 {
		base.Images = append(base.Images, next.Images...)
	}
	if len(next.ToolResults) > 0 {
		base.ToolResults = append(base.ToolResults, next.ToolResults...)
	}
}

func pairHistoryMessages(messages []UnifiedMessage) []UnifiedMessage {
	if len(messages) == 0 {
		return nil
	}

	pairs := make([]UnifiedMessage, 0, len(messages)+2)
	var pendingUser *UnifiedMessage

	for _, msg := range messages {
		if msg.Role == "assistant" {
			if pendingUser == nil {
				continue
			}
			pairs = append(pairs, *pendingUser)
			pairs = append(pairs, msg)
			pendingUser = nil
			continue
		}

		// Only "user" reaches here (roles are normalized before building history).
		if pendingUser == nil {
			copyMsg := msg
			pendingUser = &copyMsg
			continue
		}
		mergeUserHistoryMessage(pendingUser, msg)
	}

	if pendingUser != nil {
		pairs = append(pairs, *pendingUser)
		pairs = append(pairs, UnifiedMessage{Role: "assistant", Content: historyAssistantOK})
	}

	return pairs
}

func hasHistoryToolResults(msg KiroHistoryMessage) bool {
	if msg.UserInputMessage == nil || msg.UserInputMessage.UserInputMessageContext == nil {
		return false
	}
	return len(msg.UserInputMessage.UserInputMessageContext.ToolResults) > 0
}

func limitHistoryMessages(history []KiroHistoryMessage, maxMessages int, preservePrefixPairs int) []KiroHistoryMessage {
	if maxMessages <= 0 || len(history) == 0 {
		return nil
	}
	if len(history) <= maxMessages {
		return history
	}

	prefixKeep := preservePrefixPairs * 2
	if prefixKeep < 0 {
		prefixKeep = 0
	}
	if prefixKeep > len(history) {
		prefixKeep = len(history)
	}
	if prefixKeep > maxMessages {
		prefixKeep = maxMessages
	}

	remaining := maxMessages - prefixKeep
	if remaining <= 0 {
		return append([]KiroHistoryMessage(nil), history[:prefixKeep]...)
	}
	if remaining%2 != 0 {
		remaining--
	}
	if remaining <= 0 {
		return append([]KiroHistoryMessage(nil), history[:prefixKeep]...)
	}

	tailStart := len(history) - remaining
	if tailStart < prefixKeep {
		tailStart = prefixKeep
	}
	if tailStart%2 != 0 {
		tailStart++
	}
	if tailStart >= len(history) {
		return append([]KiroHistoryMessage(nil), history[:prefixKeep]...)
	}

	for tailStart < len(history) && hasHistoryToolResults(history[tailStart]) {
		tailStart += 2
	}
	if tailStart >= len(history) {
		return append([]KiroHistoryMessage(nil), history[:prefixKeep]...)
	}

	trimmed := make([]KiroHistoryMessage, 0, prefixKeep+(len(history)-tailStart))
	if prefixKeep > 0 {
		trimmed = append(trimmed, history[:prefixKeep]...)
	}
	trimmed = append(trimmed, history[tailStart:]...)

	if len(trimmed) > maxMessages {
		trimmed = trimmed[:maxMessages]
	}
	return trimmed
}

// BuildKiroHistory builds history array for Kiro API from unified messages.
func BuildKiroHistory(messages []UnifiedMessage, modelID string, origin string, envState *KiroEnvState) []KiroHistoryMessage {
	pairedMessages := pairHistoryMessages(messages)
	if len(pairedMessages) == 0 {
		return nil
	}

	var history []KiroHistoryMessage
	for _, msg := range pairedMessages {
		switch msg.Role {
		case "user":
			content := msg.Content
			userInput := &KiroUserInputMessage{
				Content: content,
				Origin:  origin,
			}
			if strings.TrimSpace(modelID) != "" {
				userInput.ModelID = modelID
			}
			images := msg.Images
			if len(images) > 0 {
				kiroImages := ConvertImagesToKiroFormat(images)
				if len(kiroImages) > 0 {
					userInput.Images = kiroImages
				}
			}
			ctx := &KiroUserInputMessageContext{EnvState: envState}
			if len(msg.ToolResults) > 0 {
				kiroResults := ConvertToolResultsToKiroFormat(msg.ToolResults)
				if len(kiroResults) > 0 {
					ctx.ToolResults = kiroResults
				}
			}
			userInput.UserInputMessageContext = ctx
			history = append(history, KiroHistoryMessage{UserInputMessage: userInput})

		case "assistant":
			content := msg.Content
			assistant := &KiroAssistantResponseMessage{Content: content}
			toolUses := ExtractToolUsesFromMessage(nil, msg.ToolCalls)
			if len(toolUses) > 0 {
				assistant.ToolUses = toolUses
			}
			if shouldAssignAssistantMessageID(content, assistant.ToolUses) {
				assistant.MessageID = generateUUID()
			}
			history = append(history, KiroHistoryMessage{AssistantResponseMessage: assistant})
		}
	}
	return history
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
func validateToolPairing(history []KiroHistoryMessage, toolResults []ToolResultRef) ([]ToolResultRef, map[string]bool) {
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

	unpairedToolUseIDs := make(map[string]bool)
	for id := range allToolUseIDs {
		if !historyToolResultIDs[id] {
			unpairedToolUseIDs[id] = true
		}
	}

	var filteredResults []ToolResultRef
	for _, result := range toolResults {
		if unpairedToolUseIDs[result.ToolUseID] {
			filteredResults = append(filteredResults, result)
			delete(unpairedToolUseIDs, result.ToolUseID)
		}
	}

	return filteredResults, unpairedToolUseIDs
}

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
