package claude

import (
	kiroresponse "clisimplehub/internal/transformer/kiro/response"
	kiroShared "clisimplehub/internal/transformer/kiro/shared"
	"sync"
)

// KiroRequest represents the request payload for Kiro API
type KiroRequest struct {
	ConversationState ConversationState `json:"conversationState"`
	ProfileArn        string            `json:"profileArn,omitempty"`
}

// ConversationState represents the conversation state in Kiro API
type ConversationState struct {
	ConversationID      string               `json:"conversationId"`
	AgentContinuationID string               `json:"agentContinuationId,omitempty"`
	AgentTaskType       string               `json:"agentTaskType,omitempty"`
	CurrentMessage      KiroCurrentMessage   `json:"currentMessage"`
	History             []KiroHistoryMessage `json:"history,omitempty"`
	ChatTriggerType     string               `json:"chatTriggerType"`
}

// KiroCurrentMessage represents the current message in Kiro API
type KiroCurrentMessage struct {
	UserInputMessage *UserInputMessage `json:"userInputMessage,omitempty"`
}

// KiroHistoryMessage represents a history message in Kiro API
type KiroHistoryMessage struct {
	UserInputMessage         *UserInputMessage         `json:"userInputMessage,omitempty"`
	AssistantResponseMessage *AssistantResponseMessage `json:"assistantResponseMessage,omitempty"`
}

// UserInputMessage represents a user input message
type UserInputMessage struct {
	Content                 string                   `json:"content"`
	ModelID                 string                   `json:"modelId,omitempty"`
	Origin                  string                   `json:"origin,omitempty"`
	Images                  []KiroImage              `json:"images,omitempty"`
	UserInputMessageContext *UserInputMessageContext `json:"userInputMessageContext,omitempty"`
}

// KiroImage 表示 Kiro API 的图片结构
type KiroImage struct {
	Format string          `json:"format"` // "jpeg", "png", "gif", "webp"
	Source KiroImageSource `json:"source"`
}

// KiroImageSource 表示图片来源
type KiroImageSource struct {
	Bytes string `json:"bytes"` // base64 编码的图片数据
}

// NewKiroImageFromBase64 从 base64 数据创建 KiroImage
func NewKiroImageFromBase64(format, base64Data string) KiroImage {
	return KiroImage{
		Format: format,
		Source: KiroImageSource{
			Bytes: base64Data,
		},
	}
}

// AssistantResponseMessage represents an assistant response message
type AssistantResponseMessage struct {
	Content  string    `json:"content"`
	ToolUses []ToolUse `json:"toolUses,omitempty"`
}

// UserInputMessageContext contains additional context for user input
type UserInputMessageContext struct {
	Tools       []KiroTool   `json:"tools,omitempty"`
	ToolResults []ToolResult `json:"toolResults,omitempty"`
}

// KiroTool represents a tool definition in Kiro format
type KiroTool struct {
	ToolSpecification ToolSpecification `json:"toolSpecification"`
}

// ToolSpecification represents the specification of a tool
type ToolSpecification struct {
	Name        string      `json:"name"`
	Description string      `json:"description"`
	InputSchema InputSchema `json:"inputSchema"`
}

// InputSchema represents the input schema for a tool
type InputSchema struct {
	JSON map[string]interface{} `json:"json"`
}

// ToolUse represents a tool use in assistant response
type ToolUse struct {
	Name      string                 `json:"name"`
	ToolUseID string                 `json:"toolUseId"`
	Input     map[string]interface{} `json:"input"`
}

// ToolResult represents a tool result in user input
type ToolResult struct {
	Content   []ToolResultContent `json:"content"`
	Status    string              `json:"status"`
	ToolUseID string              `json:"toolUseId"`
	IsError   bool                `json:"isError,omitempty"`
}

// ToolResultContent represents the content of a tool result
type ToolResultContent struct {
	Text string `json:"text"`
}

// StreamState maintains state during streaming response transformation
type StreamState struct {
	Started            bool
	Finished           bool
	MessageID          string
	Model              string
	Parser             *kiroresponse.EventStreamParser
	ContentIndex       int
	RawTextSoFar       string // Track cumulative upstream text for delta calculation
	TextSoFar          string // Visible text (excluding thinking)
	ToolUseIndex       int
	InputTokens        int
	OutputTokens       int
	ContextUsagePct    float64
	ContentBlockOpen   bool
	ToolUseBlockOpen   bool
	CurrentToolUseID   string
	CurrentToolName    string
	CurrentToolBlock   int
	ToolBlockOpen      bool
	ToolBlocksStreamed bool
	ToolUseArgs        string
	FinishReason       string
	CollectedToolUses  []map[string]any
	ThinkingEnabled    bool
	ThinkingBuffer     string
	InThinkingBlock    bool
	ThinkingExtracted  bool
	ThinkingBlockOpen  bool
	ThinkingBlockIndex          int
	ThinkingSoFar               string
	StripThinkingLeadingNewline bool

	// 新增：状态管理器（从第三方代码迁移）
	SSEStateManager     *kiroresponse.SSEStateManager   // SSE 事件序列状态管理
	StopReasonManager   *kiroresponse.StopReasonManager // stop_reason 判断管理
	CompletedToolUseIds map[string]bool                 // 已完成的工具调用 ID 集合

	// Token 来源追踪（用于调试和日志）
	InputTokensSource  string // "context_usage" | "estimate" | "api"
	OutputTokensSource string // "estimate" | "api"

	// 缓冲流式模式相关字段
	BufferedStreamingEnabled  bool     // 是否启用缓冲流式模式
	EstimatedInputTokens      int      // 初始估算的 input_tokens（用于回退）
	BufferedOutputs           []string // 缓冲的 SSE 事件
	BufferedMessageStartIndex int      // message_start 在缓冲区的位置（-1 表示未找到）
}

// TokenUsage returns the best-known input/output token counts for this stream.
func (s *StreamState) TokenUsage() (inputTokens int, outputTokens int) {
	if s == nil {
		return 0, 0
	}
	return s.InputTokens, s.OutputTokens
}

var (
	cachedModelMapping map[string]string
	cachedModelMu      sync.RWMutex

	cachedBufferedStream bool
	cachedBufferedMu     sync.RWMutex
)

// SetCachedModelMapping updates the cached model mapping (called during transformer init/reload).
func SetCachedModelMapping(m map[string]string) {
	cachedModelMu.Lock()
	defer cachedModelMu.Unlock()
	if len(m) == 0 {
		cachedModelMapping = nil
		return
	}
	clone := make(map[string]string, len(m))
	for k, v := range m {
		clone[k] = v
	}
	cachedModelMapping = clone
}

// SetCachedBufferedStream updates the cached buffered-stream flag (called during transformer init/reload).
func SetCachedBufferedStream(v bool) {
	cachedBufferedMu.Lock()
	defer cachedBufferedMu.Unlock()
	cachedBufferedStream = v
}

// GetCachedBufferedStream returns the cached buffered-stream flag.
func GetCachedBufferedStream() bool {
	cachedBufferedMu.RLock()
	defer cachedBufferedMu.RUnlock()
	return cachedBufferedStream
}

// GetKiroModelID returns the Kiro model ID for a given Claude model name
func GetKiroModelID(claudeModel string) string {
	cachedModelMu.RLock()
	mapping := cachedModelMapping
	cachedModelMu.RUnlock()
	if mapping == nil {
		mapping = kiroShared.DefaultKiroModelMapping()
	}
	if kiroModel, ok := mapping[claudeModel]; ok {
		return kiroModel
	}
	return "claude-sonnet-4.5"
}
