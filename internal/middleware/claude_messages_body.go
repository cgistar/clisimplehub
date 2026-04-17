package middleware

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// SuffixResult 保存 model 名称后缀解析结果。
// 格式: model-name(value) → ModelName="model-name", RawSuffix="value"
type SuffixResult struct {
	ModelName         string
	HasSuffix         bool
	RawSuffix         string
	OneMillionContext bool
}

// ParseModelSuffix 解析 model 名中的括号后缀。
func ParseModelSuffix(model string) SuffixResult {
	model = strings.TrimSpace(model)
	oneMillionContext := false
	if strings.HasSuffix(strings.ToLower(model), "[1m]") {
		model = strings.TrimSpace(model[:len(model)-4])
		oneMillionContext = true
	}
	lastOpen := strings.LastIndex(model, "(")
	if lastOpen == -1 || !strings.HasSuffix(model, ")") {
		return SuffixResult{ModelName: model, OneMillionContext: oneMillionContext}
	}
	return SuffixResult{
		ModelName:         strings.TrimSpace(model[:lastOpen]),
		HasSuffix:         true,
		RawSuffix:         strings.ToLower(strings.TrimSpace(model[lastOpen+1 : len(model)-1])),
		OneMillionContext: oneMillionContext,
	}
}

// normalizeModel 解析 model suffix 并将 base model 写回 body。
func normalizeModel(body []byte) ([]byte, SuffixResult) {
	model := gjson.GetBytes(body, "model").String()
	if model == "" {
		return body, SuffixResult{}
	}
	sr := ParseModelSuffix(model)
	if !sr.HasSuffix {
		if sr.OneMillionContext {
			body, _ = sjson.SetBytes(body, "model", sr.ModelName)
		}
		return body, sr
	}
	body, _ = sjson.SetBytes(body, "model", sr.ModelName)
	return body, sr
}

// applyClaudeThinkingConfig 将网关兼容 helper 字段映射为 Claude 原生字段。
func applyClaudeThinkingConfig(body []byte, suffix SuffixResult) []byte {
	thinkingTypeResult := gjson.GetBytes(body, "thinking_type")
	budgetResult := gjson.GetBytes(body, "thinking_budget_tokens")
	effortResult := gjson.GetBytes(body, "reasoning_effort")
	hasSuffixEffort := suffix.HasSuffix && suffix.RawSuffix != ""
	if !thinkingTypeResult.Exists() && !budgetResult.Exists() && !effortResult.Exists() && !hasSuffixEffort {
		return body
	}

	thinkingType := strings.ToLower(strings.TrimSpace(thinkingTypeResult.String()))
	switch thinkingType {
	case "enabled":
		body, _ = sjson.SetBytes(body, "thinking.type", "enabled")
		if budgetResult.Exists() && budgetResult.Int() > 0 {
			body, _ = sjson.SetBytes(body, "thinking.budget_tokens", budgetResult.Int())
		}
	case "adaptive", "auto":
		body, _ = sjson.SetBytes(body, "thinking.type", "adaptive")
		body, _ = sjson.DeleteBytes(body, "thinking.budget_tokens")
	case "disabled":
		body, _ = sjson.SetBytes(body, "thinking.type", "disabled")
		body, _ = sjson.DeleteBytes(body, "thinking.budget_tokens")
		body, _ = sjson.DeleteBytes(body, "output_config.effort")
	case "":
		if budgetResult.Exists() && budgetResult.Int() > 0 && strings.EqualFold(gjson.GetBytes(body, "thinking.type").String(), "enabled") {
			body, _ = sjson.SetBytes(body, "thinking.budget_tokens", budgetResult.Int())
		}
	}

	effort := strings.ToLower(strings.TrimSpace(effortResult.String()))
	if effort == "" {
		effort = suffix.RawSuffix
	}
	if mappedEffort, ok := normalizeClaudeReasoningEffort(effort); ok && thinkingType != "disabled" {
		if !gjson.GetBytes(body, "thinking.type").Exists() {
			body, _ = sjson.SetBytes(body, "thinking.type", "adaptive")
		}
		body, _ = sjson.SetBytes(body, "output_config.effort", mappedEffort)
	}

	body, _ = sjson.DeleteBytes(body, "thinking_type")
	body, _ = sjson.DeleteBytes(body, "thinking_budget_tokens")
	body, _ = sjson.DeleteBytes(body, "reasoning_effort")
	body = deleteEmptyObjectPath(body, "output_config")
	return body
}

func normalizeClaudeReasoningEffort(effort string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(effort)) {
	case "low", "medium", "high", "max":
		return strings.ToLower(strings.TrimSpace(effort)), true
	case "xhigh":
		return "max", true
	case "auto":
		return "", false
	case "none", "minimal":
		return "", false
	default:
		return "", false
	}
}

// disableThinkingIfToolChoiceForced 当 tool_choice.type 为 "any" 或 "tool" 时
// 禁用 thinking（Anthropic API 硬约束）。
func disableThinkingIfToolChoiceForced(body []byte) []byte {
	tcType := gjson.GetBytes(body, "tool_choice.type").String()
	if tcType != "any" && tcType != "tool" {
		return body
	}
	body, _ = sjson.DeleteBytes(body, "thinking")
	body, _ = sjson.DeleteBytes(body, "output_config.effort")
	return deleteEmptyObjectPath(body, "output_config")
}

// normalizeClaudeTemperatureForThinking 当 thinking 开启时强制 temperature=1。
func normalizeClaudeTemperatureForThinking(body []byte) []byte {
	thinkingType := strings.ToLower(strings.TrimSpace(gjson.GetBytes(body, "thinking.type").String()))
	switch thinkingType {
	case "enabled", "adaptive", "auto":
	default:
		return body
	}

	temp := gjson.GetBytes(body, "temperature")
	if !temp.Exists() {
		return body
	}
	if temp.Float() == 1 {
		return body
	}

	body, _ = sjson.SetBytes(body, "temperature", 1)
	return body
}

// extractAndRemoveBetas 从 body 中提取 "betas" 字段并删除，返回 betas 列表。
func extractAndRemoveBetas(body []byte) ([]string, []byte) {
	betasResult := gjson.GetBytes(body, "betas")
	if !betasResult.Exists() {
		return nil, body
	}
	var betas []string
	if betasResult.IsArray() {
		for _, item := range betasResult.Array() {
			if s := strings.TrimSpace(item.String()); s != "" {
				betas = append(betas, s)
			}
		}
	} else if s := strings.TrimSpace(betasResult.String()); s != "" {
		betas = append(betas, s)
	}
	body, _ = sjson.DeleteBytes(body, "betas")
	return betas, body
}

// fixClaudeMessages 修复 Claude Messages API 对 role 与工具配对的硬约束。
func fixClaudeMessages(body []byte) []byte {
	var root map[string]any
	if err := json.Unmarshal(body, &root); err != nil {
		return body
	}
	rawMessages, ok := root["messages"].([]any)
	if !ok {
		return body
	}

	firstUser := -1
	for i, raw := range rawMessages {
		msg, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		if strings.EqualFold(stringFromMap(msg, "role"), "user") {
			firstUser = i
			break
		}
	}
	if firstUser < 0 {
		root["messages"] = []any{}
		return marshalClaudeMessagesBody(body, root)
	}

	var normalized []map[string]any
	for _, raw := range rawMessages[firstUser:] {
		msg, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		role := strings.ToLower(strings.TrimSpace(stringFromMap(msg, "role")))
		if role != "user" && role != "assistant" {
			continue
		}
		copied := cloneStringAnyMap(msg)
		copied["role"] = role
		copied["content"] = normalizeClaudeContentBlocks(copied["content"])
		normalized = append(normalized, copied)
	}
	if len(normalized) == 0 {
		root["messages"] = []any{}
		return marshalClaudeMessagesBody(body, root)
	}

	if normalized[0]["role"] == "user" {
		normalized[0]["content"] = sanitizeLeadingUserContent(normalized[0]["content"])
	}

	merged := mergeAdjacentClaudeMessages(normalized)
	insertMissingToolResults(merged)

	out := make([]any, 0, len(merged))
	for _, msg := range merged {
		out = append(out, msg)
	}
	root["messages"] = out
	return marshalClaudeMessagesBody(body, root)
}

func mergeAdjacentClaudeMessages(messages []map[string]any) []map[string]any {
	merged := make([]map[string]any, 0, len(messages))
	for _, msg := range messages {
		if len(merged) == 0 || stringFromMap(merged[len(merged)-1], "role") != stringFromMap(msg, "role") {
			merged = append(merged, msg)
			continue
		}
		prev := merged[len(merged)-1]
		prevContent := contentSlice(prev["content"])
		nextContent := contentSlice(msg["content"])
		if len(prevContent) > 0 && len(nextContent) > 0 {
			prevContent = append(prevContent, map[string]any{"type": "text", "text": "\n"})
		}
		prev["content"] = append(prevContent, nextContent...)
	}
	return merged
}

func insertMissingToolResults(messages []map[string]any) {
	for i := 1; i < len(messages); i++ {
		if stringFromMap(messages[i], "role") != "user" || stringFromMap(messages[i-1], "role") != "assistant" {
			continue
		}
		toolUseIDs := collectToolUseIDs(messages[i-1]["content"])
		if len(toolUseIDs) == 0 {
			continue
		}
		existing := collectToolResultIDs(messages[i]["content"])
		var missing []any
		for _, id := range toolUseIDs {
			if !existing[id] {
				missing = append(missing, map[string]any{
					"type":        "tool_result",
					"tool_use_id": id,
					"content":     "(error)",
				})
			}
		}
		if len(missing) == 0 {
			continue
		}
		messages[i]["content"] = append(missing, contentSlice(messages[i]["content"])...)
	}
}

func sanitizeLeadingUserContent(content any) []any {
	blocks := contentSlice(content)
	for i, block := range blocks {
		obj, ok := block.(map[string]any)
		if !ok || stringFromMap(obj, "type") != "tool_result" {
			continue
		}
		blocks[i] = map[string]any{
			"type": "text",
			"text": toolResultBlockToText(obj),
		}
	}
	return blocks
}

func normalizeClaudeContentBlocks(content any) []any {
	switch v := content.(type) {
	case string:
		if strings.TrimSpace(v) == "" {
			return nil
		}
		return []any{map[string]any{"type": "text", "text": v}}
	case []any:
		out := make([]any, 0, len(v))
		for _, item := range v {
			if text, ok := item.(string); ok {
				out = append(out, map[string]any{"type": "text", "text": text})
				continue
			}
			out = append(out, item)
		}
		return out
	default:
		return nil
	}
}

func collectToolUseIDs(content any) []string {
	var ids []string
	for _, block := range contentSlice(content) {
		obj, ok := block.(map[string]any)
		if !ok || stringFromMap(obj, "type") != "tool_use" {
			continue
		}
		if id := strings.TrimSpace(stringFromMap(obj, "id")); id != "" {
			ids = append(ids, id)
		}
	}
	return ids
}

func collectToolResultIDs(content any) map[string]bool {
	ids := make(map[string]bool)
	for _, block := range contentSlice(content) {
		obj, ok := block.(map[string]any)
		if !ok || stringFromMap(obj, "type") != "tool_result" {
			continue
		}
		if id := strings.TrimSpace(stringFromMap(obj, "tool_use_id")); id != "" {
			ids[id] = true
		}
	}
	return ids
}

func toolResultBlockToText(block map[string]any) string {
	id := strings.TrimSpace(stringFromMap(block, "tool_use_id"))
	content := block["content"]
	switch v := content.(type) {
	case string:
		if id == "" {
			return v
		}
		return fmt.Sprintf("Tool result for %s: %s", id, v)
	default:
		data, err := json.Marshal(v)
		if err != nil {
			if id == "" {
				return "Tool result"
			}
			return "Tool result for " + id
		}
		if id == "" {
			return string(data)
		}
		return fmt.Sprintf("Tool result for %s: %s", id, string(data))
	}
}

func contentSlice(content any) []any {
	if blocks, ok := content.([]any); ok {
		return blocks
	}
	return nil
}

func stringFromMap(m map[string]any, key string) string {
	v, _ := m[key].(string)
	return strings.TrimSpace(v)
}

func cloneStringAnyMap(src map[string]any) map[string]any {
	dst := make(map[string]any, len(src))
	for k, v := range src {
		dst[k] = v
	}
	return dst
}

func marshalClaudeMessagesBody(original []byte, root map[string]any) []byte {
	data, err := json.Marshal(root)
	if err != nil {
		return original
	}
	return data
}

func deleteEmptyObjectPath(body []byte, path string) []byte {
	obj := gjson.GetBytes(body, path)
	if obj.Exists() && obj.IsObject() && len(obj.Map()) == 0 {
		body, _ = sjson.DeleteBytes(body, path)
	}
	return body
}
