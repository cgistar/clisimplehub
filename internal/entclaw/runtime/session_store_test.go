package entclawruntime

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestSessionStoreLoadOrCreateWritesSessionFile(t *testing.T) {
	dataDir := t.TempDir()
	store := NewSessionStore(dataDir)
	seed := SessionSeed{
		Channel: ChannelClaude,
		Format:  FormatMessages,
		Model:   "gpt-5.4",
		Messages: []json.RawMessage{
			json.RawMessage(`{"role":"user","content":"hello"}`),
		},
		ToolHistory: []ToolRound{
			{
				Call: ToolCall{
					ID:        "call_1",
					Name:      "skill_list",
					Arguments: json.RawMessage(`{"limit":1}`),
				},
				Result: ToolResult{
					Content: json.RawMessage(`{"ok":true}`),
				},
			},
		},
	}

	session, err := store.LoadOrCreate(context.Background(), "session-1", seed)
	if err != nil {
		t.Fatalf("LoadOrCreate: %v", err)
	}
	if session.SessionID != "session-1" {
		t.Fatalf("session.SessionID = %q, want session-1", session.SessionID)
	}
	if session.Channel != seed.Channel {
		t.Fatalf("session.Channel = %q, want %q", session.Channel, seed.Channel)
	}
	if session.Format != seed.Format {
		t.Fatalf("session.Format = %q, want %q", session.Format, seed.Format)
	}
	if session.Model != seed.Model {
		t.Fatalf("session.Model = %q, want %q", session.Model, seed.Model)
	}
	if session.Status != "active" {
		t.Fatalf("session.Status = %q, want active", session.Status)
	}
	if !session.CreatedAt.Equal(session.UpdatedAt) {
		t.Fatalf("createdAt=%v updatedAt=%v, want equal on first create", session.CreatedAt, session.UpdatedAt)
	}

	sessionPath := filepath.Join(dataDir, "entclaw", "sessions", "session-1.json")
	if _, err := os.Stat(sessionPath); err != nil {
		t.Fatalf("session file stat: %v", err)
	}
	raw, err := os.ReadFile(sessionPath)
	if err != nil {
		t.Fatalf("ReadFile(sessionPath): %v", err)
	}

	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatalf("json.Unmarshal(session): %v", err)
	}
	if payload["sessionId"] != "session-1" {
		t.Fatalf("sessionId = %#v, want session-1", payload["sessionId"])
	}
	if payload["requestFormat"] != string(FormatMessages) {
		t.Fatalf("requestFormat = %#v, want %q", payload["requestFormat"], FormatMessages)
	}
	toolHistory, ok := payload["toolHistory"].([]any)
	if !ok || len(toolHistory) != 1 {
		t.Fatalf("toolHistory = %#v, want single entry", payload["toolHistory"])
	}
	toolRound, ok := toolHistory[0].(map[string]any)
	if !ok {
		t.Fatalf("toolRound = %#v, want object", toolHistory[0])
	}
	call, ok := toolRound["call"].(map[string]any)
	if !ok || call["id"] != "call_1" || call["name"] != "skill_list" {
		t.Fatalf("call = %#v, want lowercase JSON fields", toolRound["call"])
	}
	result, ok := toolRound["result"].(map[string]any)
	if !ok || result["isError"] != false {
		t.Fatalf("result = %#v, want lowercase JSON fields", toolRound["result"])
	}

	loaded, err := store.LoadOrCreate(context.Background(), "session-1", SessionSeed{
		Channel: ChannelChat,
		Format:  FormatChatCompletions,
		Model:   "other-model",
	})
	if err != nil {
		t.Fatalf("second LoadOrCreate: %v", err)
	}
	if loaded.SessionID != session.SessionID {
		t.Fatalf("loaded.SessionID = %q, want %q", loaded.SessionID, session.SessionID)
	}
	if loaded.Channel != session.Channel {
		t.Fatalf("loaded.Channel = %q, want %q", loaded.Channel, session.Channel)
	}
	if loaded.Format != session.Format {
		t.Fatalf("loaded.Format = %q, want %q", loaded.Format, session.Format)
	}
	if loaded.Model != session.Model {
		t.Fatalf("loaded.Model = %q, want %q", loaded.Model, session.Model)
	}
	if !loaded.CreatedAt.Equal(session.CreatedAt) {
		t.Fatalf("loaded.CreatedAt = %v, want %v", loaded.CreatedAt, session.CreatedAt)
	}
	if !loaded.UpdatedAt.Equal(session.UpdatedAt) {
		t.Fatalf("loaded.UpdatedAt = %v, want %v", loaded.UpdatedAt, session.UpdatedAt)
	}
}

func TestSkillStoreWriteAndRead(t *testing.T) {
	dataDir := t.TempDir()
	store := NewSkillStore(dataDir)
	want := "# Demo\n\nbody\n"

	if err := store.Write(context.Background(), "demo", want); err != nil {
		t.Fatalf("Write: %v", err)
	}

	skillPath := filepath.Join(dataDir, "entclaw", "skills", "demo", "SKILL.md")
	if _, err := os.Stat(skillPath); err != nil {
		t.Fatalf("skill file stat: %v", err)
	}

	content, err := store.Read(context.Background(), "demo")
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if content != want {
		t.Fatalf("content = %q, want %q", content, want)
	}
}

func TestSkillStoreScriptDirReturnsSkillScriptsPath(t *testing.T) {
	dataDir := t.TempDir()
	store := NewSkillStore(dataDir)

	dir, err := store.ScriptDir("demo")
	if err != nil {
		t.Fatalf("ScriptDir(demo): %v", err)
	}

	want := filepath.Join(dataDir, "entclaw", "skills", "demo", "scripts")
	if dir != want {
		t.Fatalf("dir = %q, want %q", dir, want)
	}
}

func TestSkillStorePublicHelpersRejectInvalidSkillNames(t *testing.T) {
	store := NewSkillStore(t.TempDir())

	tests := []struct {
		name   string
		call   func() (string, error)
		target string
	}{
		{
			name:   "SkillPath dot",
			target: ".",
			call: func() (string, error) {
				return store.SkillPath(".")
			},
		},
		{
			name:   "SkillPath dotdot",
			target: "..",
			call: func() (string, error) {
				return store.SkillPath("..")
			},
		},
		{
			name:   "ScriptDir dot",
			target: ".",
			call: func() (string, error) {
				return store.ScriptDir(".")
			},
		},
		{
			name:   "ScriptDir dotdot",
			target: "..",
			call: func() (string, error) {
				return store.ScriptDir("..")
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := tt.call(); err == nil {
				t.Fatalf("helper accepted invalid skill name %q", tt.target)
			}
		})
	}
}

func TestSessionStoreLoadOrCreateReadsExistingSessionFile(t *testing.T) {
	dataDir := t.TempDir()
	store := NewSessionStore(dataDir)

	sessionPath := filepath.Join(dataDir, "entclaw", "sessions", "session-1.json")
	if err := os.MkdirAll(filepath.Dir(sessionPath), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	body := []byte(`{
  "sessionId": "session-1",
  "channel": "claude",
  "requestFormat": "messages",
  "model": "gpt-5.4",
  "messages": [],
  "toolHistory": [],
  "status": "active",
  "createdAt": "2026-04-08T10:00:00Z",
  "updatedAt": "2026-04-08T10:00:00Z"
}
`)
	if err := os.WriteFile(sessionPath, body, 0o644); err != nil {
		t.Fatalf("WriteFile(sessionPath): %v", err)
	}

	session, err := store.LoadOrCreate(context.Background(), "session-1", SessionSeed{
		Channel: ChannelChat,
		Format:  FormatChatCompletions,
		Model:   "different-model",
	})
	if err != nil {
		t.Fatalf("LoadOrCreate(existing): %v", err)
	}
	if session.SessionID != "session-1" {
		t.Fatalf("session.SessionID = %q, want session-1", session.SessionID)
	}
	if session.Channel != ChannelClaude {
		t.Fatalf("session.Channel = %q, want %q", session.Channel, ChannelClaude)
	}
	if session.Format != FormatMessages {
		t.Fatalf("session.Format = %q, want %q", session.Format, FormatMessages)
	}
	if session.Model != "gpt-5.4" {
		t.Fatalf("session.Model = %q, want gpt-5.4", session.Model)
	}
}
