package claude

import (
	"encoding/json"
	"strings"
	"unicode/utf8"

	"clisimplehub/internal/transformer/shared"
)

// EstimateClaudeInputTokens estimates the input token count for an Anthropic `/v1/messages`-style request.
// It is a best-effort heuristic used for local endpoints (e.g., `/v1/messages/count_tokens`) and early stream usage.
func EstimateClaudeInputTokens(rawJSON []byte) int {
	req, err := shared.DecodeJSONMap(rawJSON)
	if err != nil || req == nil {
		return 0
	}

	systemPrompt := extractSystemPrompt(req)
	var msgs []normalizedClaudeMessage
	if rawMessages, ok := req["messages"].([]interface{}); ok && len(rawMessages) > 0 {
		msgs = normalizeAndMergeClaudeMessages(rawMessages, &systemPrompt)
	}

	var b strings.Builder
	if strings.TrimSpace(systemPrompt) != "" {
		b.WriteString(systemPrompt)
		b.WriteString("\n")
	}

	for _, msg := range msgs {
		if strings.TrimSpace(msg.Role) != "" {
			b.WriteString(msg.Role)
			b.WriteString(": ")
		}
		if strings.TrimSpace(msg.Text) != "" {
			b.WriteString(msg.Text)
			b.WriteString("\n")
		}
		for _, tr := range msg.ToolResults {
			for _, c := range tr.Content {
				if strings.TrimSpace(c.Text) == "" {
					continue
				}
				b.WriteString(c.Text)
				b.WriteString("\n")
			}
		}
		for _, tu := range msg.ToolUses {
			if strings.TrimSpace(tu.Name) != "" {
				b.WriteString(tu.Name)
				b.WriteString(" ")
			}
			if tu.Input != nil {
				if raw, err := json.Marshal(tu.Input); err == nil && len(raw) > 0 {
					b.Write(raw)
					b.WriteString("\n")
				}
			}
		}
	}

	// Include tool definitions, since they are part of the prompt budget in many gateways.
	if tools, ok := req["tools"].([]interface{}); ok && len(tools) > 0 {
		for _, tool := range tools {
			toolMap, ok := tool.(map[string]interface{})
			if !ok {
				continue
			}

			name := strings.TrimSpace(shared.StringFromAny(toolMap["name"]))
			description := strings.TrimSpace(shared.StringFromAny(toolMap["description"]))
			inputSchema, _ := toolMap["input_schema"].(map[string]interface{})

			// Also accept OpenAI-style `{type:"function", function:{...}}`.
			if name == "" && strings.EqualFold(shared.StringFromAny(toolMap["type"]), "function") {
				if fn, ok := toolMap["function"].(map[string]interface{}); ok {
					name = strings.TrimSpace(shared.StringFromAny(fn["name"]))
					if description == "" {
						description = strings.TrimSpace(shared.StringFromAny(fn["description"]))
					}
					if inputSchema == nil {
						if params, ok := fn["parameters"].(map[string]interface{}); ok {
							inputSchema = params
						}
					}
				}
			}

			if name != "" {
				b.WriteString("tool: ")
				b.WriteString(name)
				b.WriteString("\n")
			}
			if description != "" {
				b.WriteString(description)
				b.WriteString("\n")
			}
			if inputSchema != nil {
				if raw, err := json.Marshal(inputSchema); err == nil && len(raw) > 0 {
					b.Write(raw)
					b.WriteString("\n")
				}
			}
		}
	}

	return estimateTokens(b.String())
}

func isNonWesternChar(r rune) bool {
	switch {
	case r >= 0x4E00 && r <= 0x9FFF: // CJK Unified Ideographs
		return true
	case r >= 0x3400 && r <= 0x4DBF: // CJK Extension A
		return true
	case r >= 0x3040 && r <= 0x309F: // Hiragana
		return true
	case r >= 0x30A0 && r <= 0x30FF: // Katakana
		return true
	case r >= 0xAC00 && r <= 0xD7AF: // Hangul
		return true
	default:
		return false
	}
}

func estimateTokens(text string) int {
	if text == "" {
		return 0
	}

	nonWestern := 0
	totalRunes := utf8.RuneCountInString(text)
	for _, r := range text {
		if isNonWesternChar(r) {
			nonWestern++
		}
	}

	western := totalRunes - nonWestern
	nonWesternTokens := (nonWestern + 1) / 2
	westernTokens := (western + 3) / 4

	estimated := nonWesternTokens + westernTokens
	if estimated <= 0 {
		return 1
	}
	return estimated
}
