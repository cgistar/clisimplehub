package claude

import (
	"time"

	kiroresponse "clisimplehub/internal/transformer/kiro/response"
)

// KiroCredentials represents credentials loaded from the local Kiro credentials file.
type KiroCredentials struct {
	AccessToken  string    `json:"accessToken"`
	RefreshToken string    `json:"refreshToken"`
	ProfileArn   string    `json:"profileArn"`
	ExpiresAt    time.Time `json:"expiresAt"`
	Region       string    `json:"region,omitempty"`
	AuthMethod   string    `json:"authMethod,omitempty"`
	Provider     string    `json:"provider,omitempty"`
}

// KiroRequest represents the request payload for Kiro API
type KiroRequest struct {
	ConversationState ConversationState `json:"conversationState"`
	ProfileArn        string            `json:"profileArn,omitempty"`
}

// ConversationState represents the conversation state in Kiro API
type ConversationState struct {
	ConversationID  string               `json:"conversationId"`
	CurrentMessage  KiroCurrentMessage   `json:"currentMessage"`
	History         []KiroHistoryMessage `json:"history,omitempty"`
	ChatTriggerType string               `json:"chatTriggerType"`
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
	Description string      `json:"description,omitempty"`
	InputSchema InputSchema `json:"inputSchema,omitempty"`
}

// InputSchema represents the input schema for a tool
type InputSchema struct {
	JSON map[string]interface{} `json:"json,omitempty"`
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
}

// ToolResultContent represents the content of a tool result
type ToolResultContent struct {
	Text string `json:"text,omitempty"`
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
	ThinkingBlockIndex int
	ThinkingSoFar      string

	// 新增：状态管理器（从第三方代码迁移）
	SSEStateManager     *kiroresponse.SSEStateManager   // SSE 事件序列状态管理
	StopReasonManager   *kiroresponse.StopReasonManager // stop_reason 判断管理
	CompletedToolUseIds map[string]bool                 // 已完成的工具调用 ID 集合
}

// TokenUsage returns the best-known input/output token counts for this stream.
func (s *StreamState) TokenUsage() (inputTokens int, outputTokens int) {
	if s == nil {
		return 0, 0
	}
	return s.InputTokens, s.OutputTokens
}

// KiroModelMapping maps Claude model names to Kiro model IDs
var KiroModelMapping = map[string]string{
	// MODEL_MAPPING.
	"claude-opus-4-5":          "claude-opus-4.5",
	"claude-opus-4-5-20251101": "claude-opus-4.5",

	"claude-haiku-4.5":          "claude-haiku-4.5",
	"claude-haiku-4-5":          "claude-haiku-4.5",
	"claude-haiku-4-5-20251001": "claude-haiku-4.5",

	"claude-sonnet-4-5":          "CLAUDE_SONNET_4_5_20250929_V1_0",
	"claude-sonnet-4-5-20250929": "CLAUDE_SONNET_4_5_20250929_V1_0",

	"claude-sonnet-4":          "CLAUDE_SONNET_4_20250514_V1_0",
	"claude-sonnet-4-20250514": "CLAUDE_SONNET_4_20250514_V1_0",

	"claude-3-7-sonnet-20250219": "CLAUDE_3_7_SONNET_20250219_V1_0",

	// Convenience aliases
	"auto": "claude-sonnet-4.5",

	// Backward-compatible mappings (kept to avoid breaking existing configs).
	"claude-opus-4-5-20250514":   "CLAUDE_OPUS_4_5_20250514_V1_0",
	"claude-sonnet-4-5-20250514": "CLAUDE_SONNET_4_5_20250929_V1_0",
	"claude-3-5-sonnet-20241022": "anthropic.claude-3-5-sonnet-20241022-v2:0",
	"claude-3-5-haiku-20241022":  "anthropic.claude-3-5-haiku-20241022-v1:0",
	"claude-3-opus-20240229":     "anthropic.claude-3-opus-20240229-v1:0",
	"claude-3-sonnet-20240229":   "anthropic.claude-3-sonnet-20240229-v1:0",
	"claude-3-haiku-20240307":    "anthropic.claude-3-haiku-20240307-v1:0",
}

// GetKiroModelID returns the Kiro model ID for a given Claude model name
func GetKiroModelID(claudeModel string) string {
	if kiroModel, ok := KiroModelMapping[claudeModel]; ok {
		return kiroModel
	}
	// Default to the input model name if no mapping found
	return claudeModel
}
