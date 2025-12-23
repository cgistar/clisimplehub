package executor

// OpenAICompatExecutor 实现 OpenAI Chat Completions 兼容 API 的执行器
type OpenAICompatExecutor struct {
	*BaseExecutor
}

// NewOpenAICompatExecutor 创建 OpenAI 兼容执行器
func NewOpenAICompatExecutor(provider string) *OpenAICompatExecutor {
	if provider == "" {
		provider = "chat"
	}
	return &OpenAICompatExecutor{
		BaseExecutor: NewBaseExecutor(provider),
	}
}
