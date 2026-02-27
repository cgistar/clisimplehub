package claude

import (
	"strings"

	"clisimplehub/internal/transformer/shared"
)

// KiroToClaudeMessage converts a complete Kiro response to Claude message format.
func KiroToClaudeMessage(kiroResp map[string]interface{}, modelName string) map[string]interface{} {
	content := []interface{}{}

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

	stopReason := strings.TrimSpace(shared.StringFromAny(kiroResp["stopReason"]))
	if stopReason == "" {
		stopReason = strings.TrimSpace(shared.StringFromAny(kiroResp["stop_reason"]))
	}
	if stopReason == "" {
		stopReason = "end_turn"
	}

	// Check if there are tool uses in content.
	hasToolUses := false
	for _, c := range content {
		if cMap, ok := c.(map[string]interface{}); ok && cMap["type"] == "tool_use" {
			hasToolUses = true
			break
		}
	}
	if hasToolUses && stopReason == "end_turn" {
		stopReason = "tool_use"
	}

	inputTokens := 0
	outputTokens := 0
	if usage, ok := kiroResp["usage"].(map[string]interface{}); ok {
		if v := shared.IntFromAny(usage["input_tokens"]); v > 0 {
			inputTokens = v
		}
		if v := shared.IntFromAny(usage["output_tokens"]); v > 0 {
			outputTokens = v
		}
	}

	return map[string]interface{}{
		"id":            "msg_kiro_" + shared.RandomSuffix(),
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
