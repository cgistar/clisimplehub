package converters

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAnthropicToKiro_SystemReminderMergedIntoHistory(t *testing.T) {
	req := &AnthropicRequest{
		Model: "claude-sonnet-4-6",
		System: json.RawMessage(`[
			{"type":"text","text":"base system prompt"}
		]`),
		Messages: []AnthropicMessage{
			{
				Role: "user",
				Content: json.RawMessage(`[
					{"type":"text","text":"<system-reminder>\nreminder from user content\n</system-reminder>"},
					{"type":"text","text":"<local-command-caveat>ignore me</local-command-caveat>"},
					{"type":"text","text":"<command-name>/clear</command-name>"},
					{"type":"text","text":"真正用户问题"}
				]`),
			},
		},
	}

	payload, err := AnthropicToKiro(req, "conv-1", "")
	if err != nil {
		t.Fatalf("AnthropicToKiro failed: %v", err)
	}

	history := payload.ConversationState.History
	if len(history) < 2 || history[0].UserInputMessage == nil {
		t.Fatalf("expected system history pair, got history len=%d", len(history))
	}

	systemContent := history[0].UserInputMessage.Content
	if !strings.Contains(systemContent, "base system prompt") {
		t.Fatalf("expected system prompt in history context, got: %s", systemContent)
	}
	if !strings.Contains(systemContent, "reminder from user content") {
		t.Fatalf("expected system-reminder in history context, got: %s", systemContent)
	}
	if strings.Contains(systemContent, "<system-reminder>") {
		t.Fatalf("expected system-reminder tags stripped in history context, got: %s", systemContent)
	}

	current := payload.ConversationState.CurrentMessage.UserInputMessage.Content
	if !strings.Contains(current, "真正用户问题") {
		t.Fatalf("expected user content retained in current message, got: %s", current)
	}
	if strings.Contains(current, "system-reminder") {
		t.Fatalf("expected no system-reminder in current content, got: %s", current)
	}
	if strings.Contains(current, "local-command-caveat") || strings.Contains(current, "command-name") {
		t.Fatalf("expected local command noise stripped from current content, got: %s", current)
	}
}

func TestConvertAnthropicMessages_DropPureReminderUserMessage(t *testing.T) {
	messages := []AnthropicMessage{
		{
			Role: "user",
			Content: json.RawMessage(`[
				{"type":"text","text":"<system-reminder>\nfirst reminder\n</system-reminder>"}
			]`),
		},
		{
			Role: "user",
			Content: json.RawMessage(`[
				{"type":"text","text":"actual user prompt"}
			]`),
		},
	}

	unified, reminders := convertAnthropicMessages(messages)
	if len(reminders) != 1 || !strings.Contains(reminders[0], "first reminder") {
		t.Fatalf("unexpected reminders: %#v", reminders)
	}
	if len(unified) != 1 {
		t.Fatalf("expected pure reminder user message dropped, got len=%d", len(unified))
	}
	if unified[0].Role != "user" || unified[0].Content != "actual user prompt" {
		t.Fatalf("unexpected unified message: %#v", unified[0])
	}
}

func TestAnthropicToKiro_RealLogSystemReminderMovedOutOfCurrentMessage(t *testing.T) {
	logPath := filepath.Join("..", "..", "..", "test", "logs", "20260226_093333-b3fadd9d-claude-kiro.log")
	rawReq := extractOriginalRequestFromLog(t, logPath)

	var req AnthropicRequest
	if err := json.Unmarshal(rawReq, &req); err != nil {
		t.Fatalf("failed to unmarshal log request: %v", err)
	}

	payload, err := AnthropicToKiro(&req, "conv-real-log", "")
	if err != nil {
		t.Fatalf("AnthropicToKiro failed: %v", err)
	}

	current := payload.ConversationState.CurrentMessage.UserInputMessage.Content
	if strings.Contains(current, "<system-reminder>") {
		t.Fatalf("expected system-reminder removed from current message, got: %s", current)
	}
	if strings.Contains(current, "<local-command-caveat>") ||
		strings.Contains(current, "<command-name>") ||
		strings.Contains(current, "<command-message>") ||
		strings.Contains(current, "<command-args>") ||
		strings.Contains(current, "<local-command-stdout>") {
		t.Fatalf("expected local-command tags removed from current message, got: %s", current)
	}
	if !strings.Contains(current, "分析 webdav 的同步") {
		t.Fatalf("expected real user question retained, got: %s", current)
	}
}

func extractOriginalRequestFromLog(t *testing.T, path string) []byte {
	t.Helper()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("failed to read log file: %v", err)
	}

	text := string(data)
	marker := "--- OriginalRequest ---"
	idx := strings.Index(text, marker)
	if idx < 0 {
		t.Fatalf("marker %q not found in log", marker)
	}

	rest := text[idx+len(marker):]
	rest = strings.TrimLeft(rest, "\r\n")
	end := strings.Index(rest, "\n--- ")
	if end < 0 {
		t.Fatalf("failed to find end marker after OriginalRequest")
	}
	return []byte(strings.TrimSpace(rest[:end]))
}
