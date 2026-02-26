package converters

import "encoding/json"

// ============================================================================
// Anthropic API Input Types
// ============================================================================

type AnthropicRequest struct {
	Model        string              `json:"model"`
	Messages     []AnthropicMessage  `json:"messages"`
	MaxTokens    int                 `json:"max_tokens"`
	System       json.RawMessage     `json:"system,omitempty"`
	Stream       bool                `json:"stream,omitempty"`
	Tools        []AnthropicTool     `json:"tools,omitempty"`
	Temperature  *float64            `json:"temperature,omitempty"`
	TopP         *float64            `json:"top_p,omitempty"`
	TopK         *int                `json:"top_k,omitempty"`
	Thinking     *AnthropicThinking  `json:"thinking,omitempty"`
	OutputConfig *AnthropicOutputCfg `json:"output_config,omitempty"`
	Metadata     *AnthropicMetadata  `json:"metadata,omitempty"`
}

type AnthropicThinking struct {
	Type         string `json:"type"`          // "enabled" or "adaptive"
	BudgetTokens int    `json:"budget_tokens"` // for "enabled" type
}

type AnthropicOutputCfg struct {
	Effort string `json:"effort"` // "low", "medium", "high"
}

type AnthropicMetadata struct {
	UserID string `json:"user_id,omitempty"`
}

type AnthropicMessage struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
}

type AnthropicTool struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	InputSchema map[string]any `json:"input_schema"`
}

// ============================================================================
// OpenAI API Input Types
// ============================================================================

type OpenAIRequest struct {
	Model    string          `json:"model"`
	Messages []OpenAIMessage `json:"messages"`
	Stream   bool            `json:"stream,omitempty"`
	Tools    []OpenAITool    `json:"tools,omitempty"`
}

type OpenAIMessage struct {
	Role       string          `json:"role"`
	Content    json.RawMessage `json:"content,omitempty"`
	ToolCalls  json.RawMessage `json:"tool_calls,omitempty"`
	ToolCallID string          `json:"tool_call_id,omitempty"`
	Name       string          `json:"name,omitempty"`
}

type OpenAITool struct {
	Type        string          `json:"type"`
	Function    *OpenAIFunction `json:"function,omitempty"`
	Name        string          `json:"name,omitempty"`
	Description string          `json:"description,omitempty"`
	InputSchema map[string]any  `json:"input_schema,omitempty"`
}

type OpenAIFunction struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	Parameters  map[string]any `json:"parameters,omitempty"`
}

// ============================================================================
// Unified Intermediate Types
// ============================================================================

type UnifiedMessage struct {
	Role        string
	Content     string
	ToolCalls   []ToolCall
	ToolResults []ToolResultRef
	Images      []ImageRef
}

type ToolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments any    `json:"arguments"`
	} `json:"function"`
}

type ToolResultRef struct {
	Type      string `json:"type"`
	ToolUseID string `json:"tool_use_id"`
	Content   string `json:"content"`
	IsError   bool   `json:"is_error,omitempty"` // 标记是否为错误结果
}

type ImageRef struct {
	MediaType string `json:"media_type"`
	Data      string `json:"data"`
}

type UnifiedTool struct {
	Name        string
	Description string
	InputSchema map[string]any
}

// ============================================================================
// Kiro API Output Types
// ============================================================================

type KiroPayload struct {
	ConversationState KiroConversationState `json:"conversationState"`
	ProfileArn        string                `json:"profileArn,omitempty"`
}

type KiroConversationState struct {
	AgentContinuationID string                `json:"agentContinuationId,omitempty"`
	AgentTaskType       string                `json:"agentTaskType,omitempty"`
	ChatTriggerType     string                `json:"chatTriggerType"`
	ConversationID      string                `json:"conversationId"`
	CurrentMessage      KiroCurrentMessage    `json:"currentMessage"`
	History             []KiroHistoryMessage  `json:"history,omitempty"`
}

type KiroCurrentMessage struct {
	UserInputMessage *KiroUserInputMessage `json:"userInputMessage"`
}

type KiroHistoryMessage struct {
	UserInputMessage         *KiroUserInputMessage         `json:"userInputMessage,omitempty"`
	AssistantResponseMessage *KiroAssistantResponseMessage  `json:"assistantResponseMessage,omitempty"`
}

type KiroUserInputMessage struct {
	Content                 string                       `json:"content"`
	ModelID                 string                       `json:"modelId"`
	Origin                  string                       `json:"origin"`
	Images                  []KiroImage                  `json:"images,omitempty"`
	UserInputMessageContext *KiroUserInputMessageContext  `json:"userInputMessageContext,omitempty"`
}

type KiroUserInputMessageContext struct {
	Tools       []KiroToolSpec   `json:"tools,omitempty"`
	ToolResults []KiroToolResult `json:"toolResults,omitempty"`
}

type KiroAssistantResponseMessage struct {
	Content  string         `json:"content"`
	ToolUses []KiroToolUse  `json:"toolUses,omitempty"`
}

type KiroImage struct {
	Format string          `json:"format"`
	Source KiroImageSource `json:"source"`
}

type KiroImageSource struct {
	Bytes string `json:"bytes"`
}

type KiroToolSpec struct {
	ToolSpecification KiroToolSpecification `json:"toolSpecification"`
}

type KiroToolSpecification struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema KiroInputSchema `json:"inputSchema"`
}

type KiroInputSchema struct {
	JSON map[string]any `json:"json"`
}

type KiroToolUse struct {
	Name      string         `json:"name"`
	ToolUseID string         `json:"toolUseId"`
	Input     map[string]any `json:"input"`
}

type KiroToolResult struct {
	Content   []KiroToolResultContent `json:"content"`
	Status    string                  `json:"status"`
	ToolUseID string                  `json:"toolUseId"`
	IsError   bool                    `json:"isError,omitempty"` // 标记是否为错误结果
}

type KiroToolResultContent struct {
	Text string `json:"text"`
}

// ============================================================================
// Build Result
// ============================================================================

type KiroPayloadResult struct {
	Payload           *KiroPayload
	ToolDocumentation string
}
