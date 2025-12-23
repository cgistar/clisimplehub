package executor

// CodexExecutor 实现 OpenAI Codex/Responses API 的执行器
type CodexExecutor struct {
	*BaseExecutor
}

// NewCodexExecutor 创建 Codex 执行器
func NewCodexExecutor() *CodexExecutor {
	return &CodexExecutor{
		BaseExecutor: NewBaseExecutor("codex"),
	}
}
