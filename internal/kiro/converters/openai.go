package converters

import (
	"encoding/json"
)

// OpenAIToKiro converts an OpenAI Chat Completions request to Kiro API payload.
func OpenAIToKiro(req *OpenAIRequest, conversationID, profileArn string) (*KiroPayload, error) {
	systemPrompt, unifiedMessages := convertOpenAIMessagesToUnified(req.Messages)
	unifiedTools := convertOpenAIToolsToUnified(req.Tools)
	modelID := NormalizeModelID(req.Model)

	result, err := BuildKiroPayload(
		unifiedMessages,
		systemPrompt,
		modelID,
		unifiedTools,
		conversationID,
		profileArn,
		nil, // OpenAI doesn't support thinking
		nil, // OpenAI doesn't support outputConfig
	)
	if err != nil {
		return nil, err
	}
	return result.Payload, nil
}

// convertOpenAIMessagesToUnified converts OpenAI messages to unified format.
// Returns (systemPrompt, unifiedMessages).
func convertOpenAIMessagesToUnified(messages []OpenAIMessage) (string, []UnifiedMessage) {
	var systemParts []string
	var nonSystem []OpenAIMessage

	for _, msg := range messages {
		if msg.Role == "system" {
			text := extractTextFromRawMessage(msg.Content)
			systemParts = append(systemParts, text)
		} else {
			nonSystem = append(nonSystem, msg)
		}
	}

	systemPrompt := ""
	if len(systemParts) > 0 {
		combined := ""
		for _, p := range systemParts {
			combined += p + "\n"
		}
		systemPrompt = trimString(combined)
	}

	// Process tool messages: convert to user messages with tool_results
	var processed []UnifiedMessage
	var pendingToolResults []ToolResultRef
	var pendingToolImages []ImageRef

	for _, msg := range nonSystem {
		if msg.Role == "tool" {
			text := extractTextFromRawMessage(msg.Content)
			if text == "" {
				text = "(empty result)"
			}
			pendingToolResults = append(pendingToolResults, ToolResultRef{
				Type:      "tool_result",
				ToolUseID: msg.ToolCallID,
				Content:   text,
			})
			toolImages := ExtractImagesFromContent(msg.Content)
			pendingToolImages = append(pendingToolImages, toolImages...)
		} else {
			// Flush accumulated tool results
			if len(pendingToolResults) > 0 {
				u := UnifiedMessage{
					Role:        "user",
					Content:     "",
					ToolResults: copyToolResults(pendingToolResults),
				}
				if len(pendingToolImages) > 0 {
					u.Images = copyImages(pendingToolImages)
				}
				processed = append(processed, u)
				pendingToolResults = pendingToolResults[:0]
				pendingToolImages = pendingToolImages[:0]
			}

			// Convert regular message
			var toolCalls []ToolCall
			var toolResults []ToolResultRef
			var images []ImageRef

			switch msg.Role {
			case "assistant":
				toolCalls = extractToolCallsFromOpenAI(msg)
			case "user":
				toolResults = extractToolResultsFromOpenAIContent(msg.Content)
				images = ExtractImagesFromContent(msg.Content)
			}

			u := UnifiedMessage{
				Role:    msg.Role,
				Content: extractTextFromRawMessage(msg.Content),
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
			processed = append(processed, u)
		}
	}

	// Flush remaining tool results
	if len(pendingToolResults) > 0 {
		u := UnifiedMessage{
			Role:        "user",
			Content:     "",
			ToolResults: copyToolResults(pendingToolResults),
		}
		if len(pendingToolImages) > 0 {
			u.Images = copyImages(pendingToolImages)
		}
		processed = append(processed, u)
	}

	return systemPrompt, processed
}

// extractToolCallsFromOpenAI extracts tool calls from OpenAI assistant message.
func extractToolCallsFromOpenAI(msg OpenAIMessage) []ToolCall {
	if len(msg.ToolCalls) == 0 {
		return nil
	}

	var rawCalls []map[string]any
	if err := json.Unmarshal(msg.ToolCalls, &rawCalls); err != nil {
		return nil
	}

	var toolCalls []ToolCall
	for _, rc := range rawCalls {
		id, _ := rc["id"].(string)
		fn, _ := rc["function"].(map[string]any)
		if fn == nil {
			continue
		}
		name, _ := fn["name"].(string)
		arguments, _ := fn["arguments"].(string)
		if arguments == "" {
			arguments = "{}"
		}

		tc := ToolCall{
			ID:   id,
			Type: "function",
		}
		tc.Function.Name = name
		tc.Function.Arguments = arguments
		toolCalls = append(toolCalls, tc)
	}
	return toolCalls
}

// extractToolResultsFromOpenAIContent extracts tool_result blocks from OpenAI user content.
func extractToolResultsFromOpenAIContent(content json.RawMessage) []ToolResultRef {
	if len(content) == 0 {
		return nil
	}

	var items []map[string]any
	if err := json.Unmarshal(content, &items); err != nil {
		return nil
	}

	var results []ToolResultRef
	for _, item := range items {
		t, _ := item["type"].(string)
		if t != "tool_result" {
			continue
		}
		toolUseID, _ := item["tool_use_id"].(string)
		contentVal := item["content"]
		text := ExtractTextContent(contentVal)
		if text == "" {
			text = "(empty result)"
		}
		results = append(results, ToolResultRef{
			Type:      "tool_result",
			ToolUseID: toolUseID,
			Content:   text,
		})
	}
	return results
}

// convertOpenAIToolsToUnified converts OpenAI tools to unified format.
func convertOpenAIToolsToUnified(tools []OpenAITool) []UnifiedTool {
	if len(tools) == 0 {
		return nil
	}

	var unified []UnifiedTool
	for _, tool := range tools {
		if tool.Type != "function" {
			continue
		}
		// Standard OpenAI format
		if tool.Function != nil {
			unified = append(unified, UnifiedTool{
				Name:        tool.Function.Name,
				Description: tool.Function.Description,
				InputSchema: tool.Function.Parameters,
			})
		} else if tool.Name != "" {
			// Flat format (Cursor-style)
			unified = append(unified, UnifiedTool{
				Name:        tool.Name,
				Description: tool.Description,
				InputSchema: tool.InputSchema,
			})
		}
	}
	if len(unified) == 0 {
		return nil
	}
	return unified
}

func trimString(s string) string {
	for len(s) > 0 && (s[len(s)-1] == ' ' || s[len(s)-1] == '\n' || s[len(s)-1] == '\r' || s[len(s)-1] == '\t') {
		s = s[:len(s)-1]
	}
	for len(s) > 0 && (s[0] == ' ' || s[0] == '\n' || s[0] == '\r' || s[0] == '\t') {
		s = s[1:]
	}
	return s
}

func copyToolResults(results []ToolResultRef) []ToolResultRef {
	c := make([]ToolResultRef, len(results))
	copy(c, results)
	return c
}

func copyImages(images []ImageRef) []ImageRef {
	c := make([]ImageRef, len(images))
	copy(c, images)
	return c
}
