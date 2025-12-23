package executor

// GeminiExecutor 实现 Google Gemini API 的执行器
type GeminiExecutor struct {
	*BaseExecutor
}

// NewGeminiExecutor 创建 Gemini 执行器
func NewGeminiExecutor() *GeminiExecutor {
	return &GeminiExecutor{
		BaseExecutor: NewBaseExecutor("gemini"),
	}
}
