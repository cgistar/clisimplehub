package converters

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
	if messages[len(messages)-1].Role != "user" {
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
