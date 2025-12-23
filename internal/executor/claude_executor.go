package executor

// ClaudeExecutor 实现 Anthropic Claude API 的执行器
type ClaudeExecutor struct {
	*BaseExecutor
}

// NewClaudeExecutor 创建 Claude 执行器
func NewClaudeExecutor() *ClaudeExecutor {
	return &ClaudeExecutor{
		BaseExecutor: NewBaseExecutor("claude"),
	}
}
