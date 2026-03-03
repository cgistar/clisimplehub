package converters

import (
	"encoding/json"
	"fmt"
	"strings"
)

const (
	toolDescriptionMaxLength = 10000
	toolStatusSuccess        = "success"
	toolStatusError          = "error"

	// 追加到 Write 工具 description 末尾的内容
	writeToolDescriptionSuffix = "- IMPORTANT: If the content to write exceeds 150 lines, you MUST only write the first 50 lines using this tool, then use `Edit` tool to append the remaining content in chunks of no more than 50 lines each. If needed, leave a unique placeholder to help append content. Do NOT attempt to write all content at once."

	// 追加到 Edit 工具 description 末尾的内容
	editToolDescriptionSuffix = "- IMPORTANT: If the `new_string` content exceeds 50 lines, you MUST split it into multiple Edit calls, each replacing no more than 50 lines at a time. If used to append content, leave a unique placeholder to help append content. On the final chunk, do NOT include the placeholder."
)

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

	for key, value := range schema {
		if key == "properties" {
			if props, ok := value.(map[string]any); ok {
				result[key] = props
				continue
			}
		}
		result[key] = value
	}

	if _, hasRequired := result["required"]; !hasRequired {
		result["required"] = []any{}
	} else if arr, ok := result["required"].([]any); ok {
		filtered := make([]any, 0, len(arr))
		for _, v := range arr {
			if _, ok := v.(string); ok {
				filtered = append(filtered, v)
			}
		}
		result["required"] = filtered
	} else {
		result["required"] = []any{}
	}

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

		if len(desc) > toolDescriptionMaxLength {
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
			Source: KiroImageSource{Bytes: data},
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
		status := normalizeToolStatus(r.Status)
		if status == "" {
			status = toolStatusSuccess
		}
		kiroResults = append(kiroResults, KiroToolResult{
			Content:   []KiroToolResultContent{{Text: text}},
			Status:    status,
			ToolUseID: r.ToolUseID,
		})
	}
	return kiroResults
}

func normalizeToolStatus(status string) string {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case toolStatusError:
		return toolStatusError
	case toolStatusSuccess:
		return toolStatusSuccess
	default:
		return ""
	}
}

func deriveToolStatus(rawStatus any, rawIsError any) string {
	if status, ok := rawStatus.(string); ok {
		if normalized := normalizeToolStatus(status); normalized != "" {
			return normalized
		}
	}
	if isError, ok := rawIsError.(bool); ok && isError {
		return toolStatusError
	}
	return toolStatusSuccess
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
