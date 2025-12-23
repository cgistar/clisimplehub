package usage

import (
	"bytes"
	"encoding/json"
)

// Extractor 定义 token 提取器接口
type Extractor interface {
	// Extract 从响应体提取 token 使用量
	Extract(body []byte) *TokenStats
	// Name 返回提取器名称
	Name() string
}

// ExtractFromResponse 从响应体提取 token 使用量（自动检测格式）
func ExtractFromResponse(body []byte) *TokenStats {
	if len(body) == 0 {
		return nil
	}

	var payload map[string]any
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	if err := decoder.Decode(&payload); err != nil {
		return nil
	}

	var tokens TokenStats

	// 尝试从各种位置提取 usage
	applyUsageIfPresent := func(holder map[string]any) {
		if holder == nil {
			return
		}
		if usage, ok := holder["usage"].(map[string]any); ok {
			applyUsageMap(usage, &tokens)
		}
	}

	// 通用: 顶层 "usage"
	applyUsageIfPresent(payload)

	// OpenAI Responses API: {"response": {"usage": {...}}}
	if response, ok := payload["response"].(map[string]any); ok {
		applyUsageIfPresent(response)
	}

	// Anthropic/Claude: {"message": {"usage": {...}}}
	if message, ok := payload["message"].(map[string]any); ok {
		applyUsageIfPresent(message)
	}

	if !tokens.IsEmpty() {
		return &tokens
	}

	// Gemini 格式: "usageMetadata"
	if usageMeta, ok := payload["usageMetadata"].(map[string]any); ok {
		return extractGeminiUsage(usageMeta)
	}

	return nil
}

// applyUsageMap 从 usage map 提取 token 数据
func applyUsageMap(usage map[string]any, tokens *TokenStats) {
	if usage == nil || tokens == nil {
		return
	}

	setMax := func(dst *int64, v int64) {
		if v > *dst {
			*dst = v
		}
	}

	// Claude / Responses API 格式
	if v, ok := parseInt64(usage["input_tokens"]); ok {
		setMax(&tokens.InputTokens, v)
	}
	if v, ok := parseInt64(usage["output_tokens"]); ok {
		setMax(&tokens.OutputTokens, v)
	}
	if v, ok := parseInt64(usage["cache_creation_input_tokens"]); ok {
		setMax(&tokens.CachedCreate, v)
	}
	if v, ok := parseInt64(usage["cache_read_input_tokens"]); ok {
		setMax(&tokens.CachedRead, v)
	}
	if v, ok := parseInt64(usage["reasoning_tokens"]); ok {
		setMax(&tokens.Reasoning, v)
	}
	if v, ok := parseInt64(usage["thinking_tokens"]); ok && tokens.Reasoning == 0 {
		setMax(&tokens.Reasoning, v)
	}

	// OpenAI Chat Completions 格式
	if v, ok := parseInt64(usage["prompt_tokens"]); ok {
		setMax(&tokens.InputTokens, v)
	}
	if v, ok := parseInt64(usage["completion_tokens"]); ok {
		setMax(&tokens.OutputTokens, v)
	}

	// OpenAI 详细字段
	if promptDetails, ok := usage["prompt_tokens_details"].(map[string]any); ok {
		if v, ok := parseInt64(promptDetails["cached_tokens"]); ok {
			setMax(&tokens.CachedRead, v)
		}
	}
	if inputDetails, ok := usage["input_tokens_details"].(map[string]any); ok {
		if v, ok := parseInt64(inputDetails["cached_tokens"]); ok {
			setMax(&tokens.CachedRead, v)
		}
	}
	if completionDetails, ok := usage["completion_tokens_details"].(map[string]any); ok {
		if v, ok := parseInt64(completionDetails["reasoning_tokens"]); ok {
			setMax(&tokens.Reasoning, v)
		}
	}
	if outputDetails, ok := usage["output_tokens_details"].(map[string]any); ok {
		if v, ok := parseInt64(outputDetails["reasoning_tokens"]); ok {
			setMax(&tokens.Reasoning, v)
		}
	}
}

// extractGeminiUsage 从 Gemini usageMetadata 提取 token 数据
func extractGeminiUsage(usageMeta map[string]any) *TokenStats {
	var tokens TokenStats

	if v, ok := parseInt64(usageMeta["promptTokenCount"]); ok {
		tokens.InputTokens = v
	}
	if v, ok := parseInt64(usageMeta["candidatesTokenCount"]); ok {
		tokens.OutputTokens = v
	}
	// Fallback: 如果只有 totalTokenCount
	if v, ok := parseInt64(usageMeta["totalTokenCount"]); ok && tokens.InputTokens == 0 && tokens.OutputTokens == 0 {
		tokens.InputTokens = v
	}

	if tokens.IsEmpty() {
		return nil
	}
	return &tokens
}

// parseInt64 从 any 类型解析 int64
func parseInt64(v any) (int64, bool) {
	switch n := v.(type) {
	case float64:
		return int64(n), true
	case float32:
		return int64(n), true
	case int:
		return int64(n), true
	case int64:
		return n, true
	case int32:
		return int64(n), true
	case json.Number:
		i, err := n.Int64()
		if err == nil {
			return i, true
		}
		f, err := n.Float64()
		if err == nil {
			return int64(f), true
		}
	}
	return 0, false
}
