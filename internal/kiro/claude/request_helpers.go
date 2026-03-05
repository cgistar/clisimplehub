package claude

import (
	"encoding/json"
	"fmt"
	"strings"

	"clisimplehub/internal/transformer/shared"

	"github.com/google/uuid"
)

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

		// 兼容 OpenAI 风格 role=tool，统一转为 user tool_result。
		if role == "tool" {
			toolCallID := strings.TrimSpace(shared.StringFromAny(msg["tool_call_id"]))
			resultText := extractMessageContent(msg)
			if strings.TrimSpace(resultText) == "" {
				resultText = "(empty result)"
			}
			n := normalizedClaudeMessage{
				Role: "user",
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

		// 合并相邻同角色消息，避免下游角色序列不合法。
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

func extractMessageContent(msg map[string]interface{}) string {
	text, _ := extractMessageContentWithImages(msg)
	return text
}

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
					parts = append(parts, v)
				}
			}
		}

		return strings.Join(parts, "\n"), images
	}
	return "", nil
}

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

func convertImageURLToKiroImage(imageURL map[string]interface{}) *KiroImage {
	urlStr := shared.StringFromAny(imageURL["url"])
	if urlStr == "" {
		return nil
	}
	if !strings.HasPrefix(urlStr, "data:") {
		return nil
	}

	parts := strings.SplitN(urlStr, ",", 2)
	if len(parts) != 2 {
		return nil
	}

	metaPart := parts[0]
	base64Data := parts[1]
	if !strings.HasPrefix(metaPart, "data:") {
		return nil
	}
	metaPart = strings.TrimPrefix(metaPart, "data:")
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
		Source: KiroImageSource{Bytes: base64Data},
	}
}

func extractToolUses(msg map[string]interface{}) []ToolUse {
	var toolUses []ToolUse
	seen := map[string]struct{}{}

	if content, ok := msg["content"].([]interface{}); ok {
		for _, part := range content {
			partMap, ok := part.(map[string]interface{})
			if !ok {
				continue
			}
			if shared.StringFromAny(partMap["type"]) != "tool_use" {
				continue
			}
			toolUse := ToolUse{
				Name:      shared.StringFromAny(partMap["name"]),
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
			argsStr := shared.StringFromAny(fn["arguments"])
			toolUse := ToolUse{
				Name:      shared.StringFromAny(fn["name"]),
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
		if shared.StringFromAny(partMap["type"]) != "tool_result" {
			continue
		}

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
			Content: []ToolResultContent{{
				Text: extractToolResultText(partMap["content"]),
			}},
		}
		results = append(results, result)
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
	if strings.Count(uuidStr, "-") != 4 {
		return ""
	}
	return uuidStr
}

func generateConversationID(claudeReq map[string]interface{}) string {
	if metadata, ok := claudeReq["metadata"].(map[string]interface{}); ok {
		if userID, ok := metadata["user_id"].(string); ok {
			if sessionID := extractSessionID(userID); sessionID != "" {
				return sessionID
			}
		}
	}
	return uuid.NewString()
}
