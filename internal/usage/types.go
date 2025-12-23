// Package usage 提供 API 使用量跟踪和 token 统计功能
package usage

// TokenStats 表示 token 使用量统计
type TokenStats struct {
	InputTokens  int64 `json:"input_tokens"`
	OutputTokens int64 `json:"output_tokens"`
	CachedCreate int64 `json:"cached_create"` // cache_creation_input_tokens
	CachedRead   int64 `json:"cached_read"`   // cache_read_input_tokens
	Reasoning    int64 `json:"reasoning"`     // reasoning_tokens / thinking_tokens
}

// Total 返回总 token 数（输入 + 输出）
func (t *TokenStats) Total() int64 {
	if t == nil {
		return 0
	}
	return t.InputTokens + t.OutputTokens
}

// TotalWithDetails 返回包含所有详细项的总 token 数
func (t *TokenStats) TotalWithDetails() int64 {
	if t == nil {
		return 0
	}
	return t.InputTokens + t.OutputTokens + t.CachedCreate + t.CachedRead + t.Reasoning
}

// IsEmpty 检查是否为空（无任何 token 数据）
func (t *TokenStats) IsEmpty() bool {
	if t == nil {
		return true
	}
	return t.InputTokens == 0 && t.OutputTokens == 0 &&
		t.CachedCreate == 0 && t.CachedRead == 0 && t.Reasoning == 0
}

// Merge 合并另一个 TokenStats（取最大值）
func (t *TokenStats) Merge(other *TokenStats) {
	if t == nil || other == nil {
		return
	}
	if other.InputTokens > t.InputTokens {
		t.InputTokens = other.InputTokens
	}
	if other.OutputTokens > t.OutputTokens {
		t.OutputTokens = other.OutputTokens
	}
	if other.CachedCreate > t.CachedCreate {
		t.CachedCreate = other.CachedCreate
	}
	if other.CachedRead > t.CachedRead {
		t.CachedRead = other.CachedRead
	}
	if other.Reasoning > t.Reasoning {
		t.Reasoning = other.Reasoning
	}
}
