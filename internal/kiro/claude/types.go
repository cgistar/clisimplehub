package claude

import (
	kiroShared "clisimplehub/internal/kiro/shared"
	"strconv"
	"strings"
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

// inferKiroModelID derives a Kiro model ID from an unmapped Claude model name.
// Rule: claude-{family}-{major}-{minor}[-date] → claude-{family}-{major}.{minor}
// e.g. "claude-sonnet-4-7" → "claude-sonnet-4.7"
//
//	"claude-sonnet-4-7-20260101" → "claude-sonnet-4.7"
func inferKiroModelID(claudeModel string) string {
	parts := strings.Split(claudeModel, "-")
	// strip trailing 8-digit date suffix
	if len(parts) > 0 {
		last := parts[len(parts)-1]
		if len(last) == 8 {
			if _, err := strconv.Atoi(last); err == nil {
				parts = parts[:len(parts)-1]
			}
		}
	}
	// expect at least: claude, {family}, {major}, {minor}
	if len(parts) < 4 {
		return claudeModel
	}
	// parts[0]="claude", parts[1..n-2]=family segments, parts[n-1]=minor, parts[n-2]=major
	minor := parts[len(parts)-1]
	if _, err := strconv.Atoi(minor); err != nil {
		return claudeModel
	}
	major := parts[len(parts)-2]
	if _, err := strconv.Atoi(major); err != nil {
		return claudeModel
	}
	prefix := strings.Join(parts[:len(parts)-2], "-")
	return prefix + "-" + major + "." + minor
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
	return inferKiroModelID(claudeModel)
}
