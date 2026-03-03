package converters

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func TestBuildKiroPayload_ObservedRequestShape(t *testing.T) {
	messages := []UnifiedMessage{
		{Role: "user", Content: "history user message"},
		{Role: "assistant", Content: "history assistant message"},
		{Role: "user", Content: "current user message"},
	}

	result, err := BuildKiroPayload(messages, "system prompt", "claude-sonnet-4.5", nil, "conv-1", "", nil, nil)
	if err != nil {
		t.Fatalf("BuildKiroPayload returned error: %v", err)
	}
	if result == nil || result.Payload == nil {
		t.Fatalf("BuildKiroPayload returned nil payload")
	}

	payload := result.Payload
	if payload.ConversationState.AgentTaskType != "vibe" {
		t.Fatalf("expected agentTaskType to stay 'vibe', got %q", payload.ConversationState.AgentTaskType)
	}
	if payload.ConversationState.AgentContinuationID == "" {
		t.Fatalf("expected agentContinuationId to be present")
	}
	if len(payload.ConversationState.History) == 0 {
		t.Fatalf("expected history to be present")
	}
	if len(payload.ConversationState.History) < 4 {
		t.Fatalf("expected history to contain system pair + conversation history, got %d entries", len(payload.ConversationState.History))
	}

	systemHistoryUser := payload.ConversationState.History[0].UserInputMessage
	if systemHistoryUser == nil {
		t.Fatalf("expected first history item to be userInputMessage")
	}
	if systemHistoryUser.Origin != kiroCLIOrigin {
		t.Fatalf("expected history origin %q, got %q", kiroCLIOrigin, systemHistoryUser.Origin)
	}
	if strings.TrimSpace(systemHistoryUser.ModelID) != "" {
		t.Fatalf("expected system history message to omit modelId, got %q", systemHistoryUser.ModelID)
	}
	assertContextOnlyContent(t, systemHistoryUser.Content, "system prompt")
	assertEnvState(t, systemHistoryUser.UserInputMessageContext)

	systemHistoryAssistant := payload.ConversationState.History[1].AssistantResponseMessage
	if systemHistoryAssistant == nil {
		t.Fatalf("expected second history item to be assistantResponseMessage")
	}
	if systemHistoryAssistant.Content != systemAssistantAck {
		t.Fatalf("unexpected system assistant ack content: %q", systemHistoryAssistant.Content)
	}
	if strings.TrimSpace(systemHistoryAssistant.MessageID) != "" {
		t.Fatalf("expected system assistant ack to omit messageId, got %q", systemHistoryAssistant.MessageID)
	}

	conversationHistoryUser := payload.ConversationState.History[2].UserInputMessage
	if conversationHistoryUser == nil {
		t.Fatalf("expected third history item to be userInputMessage")
	}
	if conversationHistoryUser.Origin != kiroCLIOrigin {
		t.Fatalf("expected conversation history origin %q, got %q", kiroCLIOrigin, conversationHistoryUser.Origin)
	}
	assertPromptWrappedContent(t, conversationHistoryUser.Content, "history user message")
	assertEnvState(t, conversationHistoryUser.UserInputMessageContext)

	conversationHistoryAssistant := payload.ConversationState.History[3].AssistantResponseMessage
	if conversationHistoryAssistant == nil {
		t.Fatalf("expected fourth history item to be assistantResponseMessage")
	}
	if strings.TrimSpace(conversationHistoryAssistant.MessageID) == "" {
		t.Fatalf("expected conversation assistant history message to contain messageId")
	}

	current := payload.ConversationState.CurrentMessage.UserInputMessage
	if current == nil {
		t.Fatalf("expected current userInputMessage")
	}
	if current.Origin != kiroCLIOrigin {
		t.Fatalf("expected current origin %q, got %q", kiroCLIOrigin, current.Origin)
	}
	if strings.TrimSpace(current.ModelID) == "" {
		t.Fatalf("expected current message to contain modelId")
	}
	assertPromptWrappedContent(t, current.Content, "current user message")
	assertEnvState(t, current.UserInputMessageContext)

	for i, msg := range payload.ConversationState.History {
		if msg.UserInputMessage != nil && strings.Contains(msg.UserInputMessage.Content, "--- SYSTEM PROMPT BEGIN ---") {
			t.Fatalf("unexpected SYSTEM block found in history user content at index %d", i)
		}
	}
	if strings.Contains(current.Content, "--- SYSTEM PROMPT BEGIN ---") {
		t.Fatalf("unexpected SYSTEM block found in current user content")
	}
}

func TestBuildKiroPayload_ThinkingPrefixIncludedInSystemHistoryContext(t *testing.T) {
	messages := []UnifiedMessage{
		{Role: "user", Content: "current user message"},
	}
	thinking := &AnthropicThinking{
		Type:         "enabled",
		BudgetTokens: 1234,
	}

	result, err := BuildKiroPayload(messages, "system prompt", "claude-sonnet-4.5", nil, "conv-2", "", thinking, nil)
	if err != nil {
		t.Fatalf("BuildKiroPayload returned error: %v", err)
	}
	history := result.Payload.ConversationState.History
	if len(history) < 1 || history[0].UserInputMessage == nil {
		t.Fatalf("expected first history message to be system context user")
	}
	content := history[0].UserInputMessage.Content
	want := "<thinking_mode>enabled</thinking_mode><max_thinking_length>1234</max_thinking_length>\nsystem prompt"
	if !strings.Contains(content, want) {
		t.Fatalf("expected system context to include thinking prefix, got content: %s", content)
	}
	if strings.Contains(result.Payload.ConversationState.CurrentMessage.UserInputMessage.Content, "system prompt") {
		t.Fatalf("expected current prompt wrapper to not embed system prompt")
	}
}

func TestBuildKiroPayload_CurrentToolResultTurnKeepsEmptyContent(t *testing.T) {
	messages := []UnifiedMessage{
		{Role: "user", Content: "history user message"},
		{
			Role: "assistant",
			ToolCalls: []ToolCall{
				{
					ID:   "tool-1",
					Type: "function",
					Function: struct {
						Name      string `json:"name"`
						Arguments any    `json:"arguments"`
					}{
						Name:      "grep",
						Arguments: `{"pattern":"mcp"}`,
					},
				},
			},
		},
		{
			Role:    "user",
			Content: "",
			ToolResults: []ToolResultRef{
				{ToolUseID: "tool-1", Content: "tool result payload"},
			},
		},
	}

	result, err := BuildKiroPayload(messages, "system prompt", "claude-sonnet-4.5", []UnifiedTool{
		{Name: "grep", Description: "search tool", InputSchema: map[string]any{"type": "object"}},
	}, "conv-3", "", nil, nil)
	if err != nil {
		t.Fatalf("BuildKiroPayload returned error: %v", err)
	}

	current := result.Payload.ConversationState.CurrentMessage.UserInputMessage
	if current.Content != "" {
		t.Fatalf("expected current content to remain empty for tool_result turn, got: %q", current.Content)
	}
	if current.Origin != kiroCLIOrigin {
		t.Fatalf("expected current origin %q, got %q", kiroCLIOrigin, current.Origin)
	}
	if current.UserInputMessageContext == nil || len(current.UserInputMessageContext.ToolResults) != 1 {
		t.Fatalf("expected current toolResults to be preserved")
	}
}

func TestBuildKiroHistory_PairsMessagesAndCompletesTrailingUser(t *testing.T) {
	env := &KiroEnvState{
		OperatingSystem:         "macos",
		CurrentWorkingDirectory: "/tmp",
	}
	messages := []UnifiedMessage{
		{Role: "assistant", Content: "orphan assistant"},
		{Role: "user", Content: "history user 1"},
		{Role: "assistant", Content: "history assistant 1"},
		{Role: "user", Content: "history user 2"},
	}

	history := BuildKiroHistory(messages, "", kiroCLIOrigin, env)
	if len(history) != 4 {
		t.Fatalf("expected 4 flattened history messages, got %d", len(history))
	}
	if history[0].UserInputMessage == nil || history[0].UserInputMessage.Content != "history user 1" {
		t.Fatalf("expected first history entry to be first user message")
	}
	if history[1].AssistantResponseMessage == nil || history[1].AssistantResponseMessage.Content != "history assistant 1" {
		t.Fatalf("expected second history entry to be first assistant message")
	}
	if strings.TrimSpace(history[1].AssistantResponseMessage.MessageID) == "" {
		t.Fatalf("expected first assistant history entry to contain messageId")
	}
	if history[2].UserInputMessage == nil || history[2].UserInputMessage.Content != "history user 2" {
		t.Fatalf("expected third history entry to be second user message")
	}
	if history[3].AssistantResponseMessage == nil || history[3].AssistantResponseMessage.Content != historyAssistantOK {
		t.Fatalf("expected trailing user to be completed by assistant %q", historyAssistantOK)
	}
	if strings.TrimSpace(history[3].AssistantResponseMessage.MessageID) != "" {
		t.Fatalf("expected synthetic assistant %q to omit messageId", historyAssistantOK)
	}
}

func TestBuildKiroHistory_MergesConsecutiveUserMessagesBeforePairing(t *testing.T) {
	env := &KiroEnvState{
		OperatingSystem:         "macos",
		CurrentWorkingDirectory: "/tmp",
	}
	messages := []UnifiedMessage{
		{Role: "user", Content: "first user"},
		{Role: "user", Content: "second user"},
		{Role: "assistant", Content: "assistant reply"},
	}

	history := BuildKiroHistory(messages, "", kiroCLIOrigin, env)
	if len(history) != 2 {
		t.Fatalf("expected merged pair to produce 2 entries, got %d", len(history))
	}
	user := history[0].UserInputMessage
	if user == nil {
		t.Fatalf("expected first entry to be user message")
	}
	if user.Content != "first user\nsecond user" {
		t.Fatalf("unexpected merged user content: %q", user.Content)
	}
}

func TestBuildKiroPayload_HistoryHardLimit250(t *testing.T) {
	var messages []UnifiedMessage
	for i := 0; i < 130; i++ {
		messages = append(messages,
			UnifiedMessage{Role: "user", Content: fmt.Sprintf("history user %d", i)},
			UnifiedMessage{Role: "assistant", Content: fmt.Sprintf("history assistant %d", i)},
		)
	}
	messages = append(messages, UnifiedMessage{Role: "user", Content: "current user message"})

	result, err := BuildKiroPayload(messages, "system prompt", "claude-sonnet-4.5", nil, "conv-limit", "", nil, nil)
	if err != nil {
		t.Fatalf("BuildKiroPayload returned error: %v", err)
	}
	history := result.Payload.ConversationState.History
	if len(history) != maxHistoryMessages {
		t.Fatalf("expected history len %d, got %d", maxHistoryMessages, len(history))
	}

	if history[0].UserInputMessage == nil {
		t.Fatalf("expected first history message to be userInputMessage")
	}
	if !strings.Contains(history[0].UserInputMessage.Content, "system prompt") {
		t.Fatalf("expected system prompt preserved in first history message")
	}
	if history[1].AssistantResponseMessage == nil || history[1].AssistantResponseMessage.Content != systemAssistantAck {
		t.Fatalf("expected system assistant ack preserved in second history message")
	}
}

func TestEnsurePromptContentWrapped_PreservesExistingTimestampWrapper(t *testing.T) {
	alreadyWrapped := strings.Join([]string{
		"--- CONTEXT ENTRY BEGIN ---",
		"Current time: Friday, 2026-02-27T11:44:38.463+08:00",
		"--- CONTEXT ENTRY END ---",
		"",
		"--- USER MESSAGE BEGIN ---",
		"hello",
		"--- USER MESSAGE END ---",
	}, "\n")

	got := ensurePromptContentWrapped(alreadyWrapped)
	if got != alreadyWrapped {
		t.Fatalf("expected wrapped content to be preserved")
	}
}

func TestResolveCurrentWorkingDirectory_TruncateTo256(t *testing.T) {
	longPath := "/" + strings.Repeat("a", 400)
	if got := truncateUTF8Safe(longPath, maxCurrentWorkingDirLen); len(got) > maxCurrentWorkingDirLen {
		t.Fatalf("expected truncated cwd length <= %d, got %d", maxCurrentWorkingDirLen, len(got))
	}
}

func TestConvertToolsToKiroFormat_DescriptionLimitAndWriteSuffix(t *testing.T) {
	tools := []UnifiedTool{
		{
			Name:        "Write",
			Description: "write description",
			InputSchema: map[string]any{"type": "object"},
		},
		{
			Name:        "LongTool",
			Description: strings.Repeat("a", 12050),
			InputSchema: map[string]any{"type": "object"},
		},
	}

	converted := ConvertToolsToKiroFormat(tools)
	if len(converted) != 2 {
		t.Fatalf("expected 2 converted tools, got %d", len(converted))
	}

	writeDesc := converted[0].ToolSpecification.Description
	if !strings.Contains(writeDesc, "If the content to write exceeds 150 lines") {
		t.Fatalf("expected Write suffix to be retained, got: %s", writeDesc)
	}

	longDesc := converted[1].ToolSpecification.Description
	if got := len([]rune(longDesc)); got != toolDescriptionMaxLength {
		t.Fatalf("expected long description length %d, got %d", toolDescriptionMaxLength, got)
	}
}

func TestConvertToolResultsToKiroFormat_StatusAndNoIsErrorField(t *testing.T) {
	results := []ToolResultRef{
		{Type: "tool_result", ToolUseID: "tool-1", Content: "ok", Status: "success"},
		{Type: "tool_result", ToolUseID: "tool-2", Content: "failed", Status: "error"},
		{Type: "tool_result", ToolUseID: "tool-3", Content: "unknown status", Status: "Success"},
		{Type: "tool_result", ToolUseID: "tool-4", Content: "default status"},
	}

	converted := ConvertToolResultsToKiroFormat(results)
	if len(converted) != 4 {
		t.Fatalf("expected 4 converted tool results, got %d", len(converted))
	}

	if converted[0].Status != toolStatusSuccess {
		t.Fatalf("expected first status %q, got %q", toolStatusSuccess, converted[0].Status)
	}
	if converted[1].Status != toolStatusError {
		t.Fatalf("expected second status %q, got %q", toolStatusError, converted[1].Status)
	}
	if converted[2].Status != toolStatusSuccess {
		t.Fatalf("expected third status normalized to %q, got %q", toolStatusSuccess, converted[2].Status)
	}
	if converted[3].Status != toolStatusSuccess {
		t.Fatalf("expected fourth default status %q, got %q", toolStatusSuccess, converted[3].Status)
	}

	encoded, err := json.Marshal(converted[0])
	if err != nil {
		t.Fatalf("failed to marshal converted tool result: %v", err)
	}
	if strings.Contains(string(encoded), "isError") {
		t.Fatalf("expected serialized tool result to omit isError, got: %s", string(encoded))
	}
}

func TestExtractToolResultsFromAnthropicContent_StatusCompatibility(t *testing.T) {
	content := json.RawMessage(`[
		{"type":"tool_result","tool_use_id":"tool-1","content":"legacy error","is_error":true},
		{"type":"tool_result","tool_use_id":"tool-2","content":"explicit success","status":"success","is_error":true},
		{"type":"tool_result","tool_use_id":"tool-3","content":"explicit error","status":"error"}
	]`)

	results := extractToolResultsFromAnthropicContent(content)
	if len(results) != 3 {
		t.Fatalf("expected 3 results, got %d", len(results))
	}

	if results[0].Status != toolStatusError {
		t.Fatalf("expected legacy is_error to map status %q, got %q", toolStatusError, results[0].Status)
	}
	if results[1].Status != toolStatusSuccess {
		t.Fatalf("expected explicit status to win over is_error, got %q", results[1].Status)
	}
	if results[2].Status != toolStatusError {
		t.Fatalf("expected explicit status %q, got %q", toolStatusError, results[2].Status)
	}
}

func assertContextOnlyContent(t *testing.T, content, expectedContext string) {
	t.Helper()
	requiredParts := []string{
		"--- CONTEXT ENTRY BEGIN ---",
		"--- CONTEXT ENTRY END ---",
		expectedContext,
	}
	for _, part := range requiredParts {
		if !strings.Contains(content, part) {
			t.Fatalf("expected context-only content to include %q, got: %s", part, content)
		}
	}
	if strings.Contains(content, "--- USER MESSAGE BEGIN ---") {
		t.Fatalf("context-only content should not include USER block, got: %s", content)
	}
}

func assertPromptWrappedContent(t *testing.T, content, userMessage string) {
	t.Helper()
	requiredParts := []string{
		"--- CONTEXT ENTRY BEGIN ---",
		"Current time: ",
		"--- CONTEXT ENTRY END ---",
		"--- USER MESSAGE BEGIN ---",
		"--- USER MESSAGE END ---",
		userMessage,
	}
	for _, part := range requiredParts {
		if !strings.Contains(content, part) {
			t.Fatalf("expected prompt-wrapped content to include %q, got: %s", part, content)
		}
	}
	if strings.Contains(content, "--- SYSTEM PROMPT BEGIN ---") {
		t.Fatalf("prompt-wrapped content should not include SYSTEM block, got: %s", content)
	}
}

func assertEnvState(t *testing.T, ctx *KiroUserInputMessageContext) {
	t.Helper()
	if ctx == nil {
		t.Fatalf("expected userInputMessageContext")
	}
	if ctx.EnvState == nil {
		t.Fatalf("expected envState")
	}
	if strings.TrimSpace(ctx.EnvState.OperatingSystem) == "" {
		t.Fatalf("expected envState.operatingSystem")
	}
	if strings.TrimSpace(ctx.EnvState.CurrentWorkingDirectory) == "" {
		t.Fatalf("expected envState.currentWorkingDirectory")
	}
}
