// Package proxy implements the HTTP proxy server for AI API requests.
package proxy

import (
	"bytes"
	"encoding/json"
)

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

func applyUsageMap(usage map[string]any, tokens *TokenUsage) {
	if usage == nil || tokens == nil {
		return
	}

	setMax := func(dst *int64, v int64) {
		if v > *dst {
			*dst = v
		}
	}

	// Claude / Responses API style
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

	// OpenAI Chat Completions style
	if v, ok := parseInt64(usage["prompt_tokens"]); ok {
		setMax(&tokens.InputTokens, v)
	}
	if v, ok := parseInt64(usage["completion_tokens"]); ok {
		setMax(&tokens.OutputTokens, v)
	}

	if promptDetails, ok := usage["prompt_tokens_details"].(map[string]any); ok {
		// OpenAI: prompt_tokens_details.cached_tokens
		if v, ok := parseInt64(promptDetails["cached_tokens"]); ok {
			setMax(&tokens.CachedRead, v)
		}
	}
	if inputDetails, ok := usage["input_tokens_details"].(map[string]any); ok {
		// OpenAI Responses: input_tokens_details.cached_tokens
		if v, ok := parseInt64(inputDetails["cached_tokens"]); ok {
			setMax(&tokens.CachedRead, v)
		}
	}
	if completionDetails, ok := usage["completion_tokens_details"].(map[string]any); ok {
		// OpenAI: completion_tokens_details.reasoning_tokens
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

func ExtractTokenUsageFromResponseBody(respBody []byte) *TokenUsage {
	if len(respBody) == 0 {
		return nil
	}

	var payload map[string]any
	decoder := json.NewDecoder(bytes.NewReader(respBody))
	decoder.UseNumber()
	if err := decoder.Decode(&payload); err != nil {
		return nil
	}

	var tokens TokenUsage

	applyUsageIfPresent := func(holder map[string]any) {
		if holder == nil {
			return
		}
		if usage, ok := holder["usage"].(map[string]any); ok {
			applyUsageMap(usage, &tokens)
		}
	}

	// Common: top-level "usage"
	applyUsageIfPresent(payload)
	// OpenAI Responses API: {"response": {"usage": {...}}}
	if response, ok := payload["response"].(map[string]any); ok {
		applyUsageIfPresent(response)
	}
	// Anthropic/Claude: {"message": {"usage": {...}}}
	if message, ok := payload["message"].(map[string]any); ok {
		applyUsageIfPresent(message)
	}

	if tokens.InputTokens != 0 || tokens.OutputTokens != 0 || tokens.CachedCreate != 0 || tokens.CachedRead != 0 || tokens.Reasoning != 0 {
		return &tokens
	}

	// Gemini style: "usageMetadata"
	if usageMeta, ok := payload["usageMetadata"].(map[string]any); ok {
		var metaTokens TokenUsage
		if v, ok := parseInt64(usageMeta["promptTokenCount"]); ok {
			metaTokens.InputTokens = v
		}
		if v, ok := parseInt64(usageMeta["candidatesTokenCount"]); ok {
			metaTokens.OutputTokens = v
		}
		if v, ok := parseInt64(usageMeta["totalTokenCount"]); ok && metaTokens.InputTokens == 0 && metaTokens.OutputTokens == 0 {
			// Best-effort fallback: if only total exists, treat it as input+output unknown split.
			metaTokens.InputTokens = v
		}
		if metaTokens.InputTokens != 0 || metaTokens.OutputTokens != 0 {
			return &metaTokens
		}
	}

	return nil
}
