package executor

import (
	"bytes"

	"clisimplehub/internal/usage"
)

func (e *BaseExecutor) extractStreamTokens(line []byte) *TokenUsage {
	if !bytes.HasPrefix(line, []byte("data:")) {
		return nil
	}

	jsonData := bytes.TrimSpace(line[5:])
	if len(jsonData) == 0 || bytes.Equal(jsonData, []byte("[DONE]")) {
		return nil
	}

	stats := usage.ExtractFromResponse(jsonData)
	if stats == nil || stats.IsEmpty() {
		return nil
	}
	return &TokenUsage{
		InputTokens:  stats.InputTokens,
		OutputTokens: stats.OutputTokens,
		CachedCreate: stats.CachedCreate,
		CachedRead:   stats.CachedRead,
		Reasoning:    stats.Reasoning,
	}
}

// ExtractTokens 从响应体提取 token 使用量
func (e *BaseExecutor) ExtractTokens(body []byte) *TokenUsage {
	stats := usage.ExtractFromResponse(body)
	if stats == nil || stats.IsEmpty() {
		return nil
	}
	return &TokenUsage{
		InputTokens:  stats.InputTokens,
		OutputTokens: stats.OutputTokens,
		CachedCreate: stats.CachedCreate,
		CachedRead:   stats.CachedRead,
		Reasoning:    stats.Reasoning,
	}
}
