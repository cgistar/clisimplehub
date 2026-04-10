package entclawruntime

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestPathGuardRejectsTraversal(t *testing.T) {
	guard := NewPathGuard(t.TempDir())

	if _, err := guard.Resolve("../secrets.txt"); err == nil {
		t.Fatal("Resolve(../secrets.txt) succeeded, want error")
	}
}

func TestPathGuardRejectsSymlinkEscape(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "secret.txt"), []byte("secret"), 0o644); err != nil {
		t.Fatalf("WriteFile(secret): %v", err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "outside")); err != nil {
		t.Skipf("os.Symlink: %v", err)
	}

	guard := NewPathGuard(root)
	if _, err := guard.Resolve("outside/secret.txt"); err == nil {
		t.Fatal("Resolve(outside/secret.txt) succeeded, want symlink escape error")
	}
}

func TestToolRuntimeExecutesSkillList(t *testing.T) {
	dataDir := t.TempDir()
	skillStore := NewSkillStore(dataDir)
	if err := skillStore.Write(context.Background(), "alpha", "# Alpha\n"); err != nil {
		t.Fatalf("Write(alpha): %v", err)
	}
	if err := skillStore.Write(context.Background(), "beta", "# Beta\n"); err != nil {
		t.Fatalf("Write(beta): %v", err)
	}

	runtime := NewToolRuntime(
		dataDir,
		NewSessionStore(dataDir),
		skillStore,
		NewMCPStore(dataDir),
		func(context.Context, string, json.RawMessage, json.RawMessage) (json.RawMessage, error) {
			return nil, errors.New("unexpected mcp call")
		},
		func(context.Context, CommandRequest) (CommandResult, error) {
			return CommandResult{}, errors.New("unexpected command exec")
		},
	)

	result, err := runtime.Execute(context.Background(), "session-1", ToolCall{
		ID:        "call_1",
		Name:      "skill_list",
		Arguments: json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatalf("Execute(skill_list): %v", err)
	}
	if result.IsError {
		t.Fatalf("result.IsError = true, want false with content %s", string(result.Content))
	}

	var payload struct {
		Skills []string `json:"skills"`
	}
	if err := json.Unmarshal(result.Content, &payload); err != nil {
		t.Fatalf("json.Unmarshal(result.Content): %v", err)
	}
	if len(payload.Skills) != 2 || payload.Skills[0] != "alpha" || payload.Skills[1] != "beta" {
		t.Fatalf("payload.Skills = %#v, want [alpha beta]", payload.Skills)
	}
}

func TestToolRuntimeSkillReadReturnsSkillMarkdown(t *testing.T) {
	dataDir := t.TempDir()
	store := NewSkillStore(dataDir)
	want := "# Demo\n\nUse scripts/run.sh\n"
	if err := store.Write(context.Background(), "demo", want); err != nil {
		t.Fatalf("Write(demo): %v", err)
	}

	runtime := NewToolRuntime(
		dataDir,
		NewSessionStore(dataDir),
		store,
		NewMCPStore(dataDir),
		nil,
		nil,
	)

	result, err := runtime.Execute(context.Background(), "session-1", ToolCall{
		ID:        "call_1",
		Name:      "skill_read",
		Arguments: json.RawMessage(`{"name":"demo"}`),
	})
	if err != nil {
		t.Fatalf("Execute(skill_read): %v", err)
	}
	if result.IsError {
		t.Fatalf("result.IsError = true, want false with content %s", string(result.Content))
	}

	var payload struct {
		Name    string `json:"name"`
		Content string `json:"content"`
	}
	if err := json.Unmarshal(result.Content, &payload); err != nil {
		t.Fatalf("json.Unmarshal(result.Content): %v", err)
	}
	if payload.Name != "demo" {
		t.Fatalf("payload.Name = %q, want demo", payload.Name)
	}
	if payload.Content != want {
		t.Fatalf("payload.Content = %q, want %q", payload.Content, want)
	}
}

func TestToolRuntimeSkillRunUsesResolvedScriptPathAndSkillWorkDir(t *testing.T) {
	dataDir := t.TempDir()
	store := NewSkillStore(dataDir)
	if err := store.Write(context.Background(), "demo", "# Demo\n"); err != nil {
		t.Fatalf("Write(demo): %v", err)
	}

	scriptDir := filepath.Join(dataDir, "entclaw", "skills", "demo", "scripts")
	if err := os.MkdirAll(scriptDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(scriptDir): %v", err)
	}
	scriptPath := filepath.Join(scriptDir, "echo.sh")
	if err := os.WriteFile(scriptPath, []byte("placeholder"), 0o644); err != nil {
		t.Fatalf("WriteFile(echo.sh): %v", err)
	}

	var gotRequest CommandRequest

	runtime := NewToolRuntime(
		dataDir,
		NewSessionStore(dataDir),
		store,
		NewMCPStore(dataDir),
		nil,
		func(_ context.Context, request CommandRequest) (CommandResult, error) {
			gotRequest = request
			return CommandResult{
				Stdout:   "ok",
				ExitCode: 0,
			}, nil
		},
	)

	result, err := runtime.Execute(context.Background(), "session-1", ToolCall{
		ID:        "call_1",
		Name:      "skill_run",
		Arguments: json.RawMessage(`{"name":"demo","script":"echo.sh","args":["ok","again"]}`),
	})
	if err != nil {
		t.Fatalf("Execute(skill_run): %v", err)
	}
	if result.IsError {
		t.Fatalf("result.IsError = true, want false with content %s", string(result.Content))
	}

	var payload struct {
		Skill    string `json:"skill"`
		Script   string `json:"script"`
		Stdout   string `json:"stdout"`
		Stderr   string `json:"stderr"`
		ExitCode int    `json:"exitCode"`
	}
	if err := json.Unmarshal(result.Content, &payload); err != nil {
		t.Fatalf("json.Unmarshal(result.Content): %v", err)
	}
	if payload.Skill != "demo" || payload.Script != "echo.sh" {
		t.Fatalf("payload = %+v, want skill/script set", payload)
	}
	if payload.ExitCode != 0 {
		t.Fatalf("payload.ExitCode = %d, want 0", payload.ExitCode)
	}
	if payload.Stdout != "ok" {
		t.Fatalf("payload.Stdout = %q, want ok", payload.Stdout)
	}
	if payload.Stderr != "" {
		t.Fatalf("payload.Stderr = %q, want empty", payload.Stderr)
	}
	if gotRequest.WorkDir != filepath.Join(dataDir, "entclaw", "skills", "demo") {
		t.Fatalf("gotRequest.WorkDir = %q, want %q", gotRequest.WorkDir, filepath.Join(dataDir, "entclaw", "skills", "demo"))
	}
	if len(gotRequest.Args) != 3 {
		t.Fatalf("len(gotRequest.Args) = %d, want 3 with script path and forwarded args", len(gotRequest.Args))
	}
	if gotRequest.Args[0] != scriptPath {
		t.Fatalf("gotRequest.Args[0] = %q, want %q", gotRequest.Args[0], scriptPath)
	}
	if gotRequest.Args[1] != "ok" || gotRequest.Args[2] != "again" {
		t.Fatalf("gotRequest.Args = %#v, want script path plus forwarded args", gotRequest.Args)
	}
}

func TestToolRuntimeSkillRunAcceptsScriptsPrefixedPath(t *testing.T) {
	dataDir := t.TempDir()
	store := NewSkillStore(dataDir)
	if err := store.Write(context.Background(), "demo", "# Demo\n"); err != nil {
		t.Fatalf("Write(demo): %v", err)
	}

	scriptDir := filepath.Join(dataDir, "entclaw", "skills", "demo", "scripts")
	if err := os.MkdirAll(scriptDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(scriptDir): %v", err)
	}
	scriptPath := filepath.Join(scriptDir, "echo.sh")
	if err := os.WriteFile(scriptPath, []byte("placeholder"), 0o644); err != nil {
		t.Fatalf("WriteFile(echo.sh): %v", err)
	}

	var gotRequest CommandRequest
	runtime := NewToolRuntime(
		dataDir,
		NewSessionStore(dataDir),
		store,
		NewMCPStore(dataDir),
		nil,
		func(_ context.Context, request CommandRequest) (CommandResult, error) {
			gotRequest = request
			return CommandResult{
				Stdout:   "ok",
				ExitCode: 0,
			}, nil
		},
	)

	result, err := runtime.Execute(context.Background(), "session-1", ToolCall{
		ID:        "call_1",
		Name:      "skill_run",
		Arguments: json.RawMessage(`{"name":"demo","script":"scripts/echo.sh"}`),
	})
	if err != nil {
		t.Fatalf("Execute(skill_run): %v", err)
	}
	if result.IsError {
		t.Fatalf("result.IsError = true, want false with content %s", string(result.Content))
	}
	if len(gotRequest.Args) == 0 || gotRequest.Args[0] != scriptPath {
		t.Fatalf("gotRequest.Args = %#v, want resolved script path %q", gotRequest.Args, scriptPath)
	}
}

func TestToolRuntimeSkillRunRejectsMissingSkillBeforeExec(t *testing.T) {
	dataDir := t.TempDir()
	runtime := NewToolRuntime(
		dataDir,
		NewSessionStore(dataDir),
		NewSkillStore(dataDir),
		NewMCPStore(dataDir),
		nil,
		func(context.Context, CommandRequest) (CommandResult, error) {
			t.Fatal("command runner called for missing skill")
			return CommandResult{}, nil
		},
	)

	result, err := runtime.Execute(context.Background(), "session-1", ToolCall{
		ID:        "call_1",
		Name:      "skill_run",
		Arguments: json.RawMessage(`{"name":"missing","script":"echo.sh"}`),
	})
	if err != nil {
		t.Fatalf("Execute(skill_run): %v", err)
	}
	if !result.IsError {
		t.Fatalf("result.IsError = false, want true with content %s", string(result.Content))
	}

	var payload struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(result.Content, &payload); err != nil {
		t.Fatalf("json.Unmarshal(result.Content): %v", err)
	}
	if payload.Error != `skill "missing" not found` {
		t.Fatalf("payload.Error = %q, want %q", payload.Error, `skill "missing" not found`)
	}
}

func TestToolRuntimeSkillRunRejectsMissingScriptBeforeExec(t *testing.T) {
	dataDir := t.TempDir()
	store := NewSkillStore(dataDir)
	if err := store.Write(context.Background(), "demo", "# Demo\n"); err != nil {
		t.Fatalf("Write(demo): %v", err)
	}

	runtime := NewToolRuntime(
		dataDir,
		NewSessionStore(dataDir),
		store,
		NewMCPStore(dataDir),
		nil,
		func(context.Context, CommandRequest) (CommandResult, error) {
			t.Fatal("command runner called for missing script")
			return CommandResult{}, nil
		},
	)

	result, err := runtime.Execute(context.Background(), "session-1", ToolCall{
		ID:        "call_1",
		Name:      "skill_run",
		Arguments: json.RawMessage(`{"name":"demo","script":"missing.sh"}`),
	})
	if err != nil {
		t.Fatalf("Execute(skill_run): %v", err)
	}
	if !result.IsError {
		t.Fatalf("result.IsError = false, want true with content %s", string(result.Content))
	}

	var payload struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(result.Content, &payload); err != nil {
		t.Fatalf("json.Unmarshal(result.Content): %v", err)
	}
	if payload.Error != `script "missing.sh" not found for skill "demo"` {
		t.Fatalf("payload.Error = %q, want %q", payload.Error, `script "missing.sh" not found for skill "demo"`)
	}
}

func TestToolRuntimeSkillRunRejectsTraversal(t *testing.T) {
	dataDir := t.TempDir()
	runtime := NewToolRuntime(
		dataDir,
		NewSessionStore(dataDir),
		NewSkillStore(dataDir),
		NewMCPStore(dataDir),
		nil,
		nil,
	)

	result, err := runtime.Execute(context.Background(), "session-1", ToolCall{
		ID:        "call_1",
		Name:      "skill_run",
		Arguments: json.RawMessage(`{"name":"demo","script":"../escape.sh"}`),
	})
	if err != nil {
		t.Fatalf("Execute(skill_run): %v", err)
	}
	if !result.IsError {
		t.Fatalf("result.IsError = false, want true with content %s", string(result.Content))
	}
}

func TestToolRuntimeSkillRunRejectsSymlinkEscape(t *testing.T) {
	dataDir := t.TempDir()
	store := NewSkillStore(dataDir)
	if err := store.Write(context.Background(), "demo", "# Demo\n"); err != nil {
		t.Fatalf("Write(demo): %v", err)
	}

	outside := t.TempDir()
	target := filepath.Join(outside, "outside.sh")
	if err := os.WriteFile(target, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatalf("WriteFile(outside.sh): %v", err)
	}

	scriptDir := filepath.Join(dataDir, "entclaw", "skills", "demo", "scripts")
	if err := os.MkdirAll(scriptDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(scriptDir): %v", err)
	}
	if err := os.Symlink(target, filepath.Join(scriptDir, "linked.sh")); err != nil {
		t.Skipf("os.Symlink(linked.sh): %v", err)
	}

	runtime := NewToolRuntime(
		dataDir,
		NewSessionStore(dataDir),
		store,
		NewMCPStore(dataDir),
		nil,
		nil,
	)

	result, err := runtime.Execute(context.Background(), "session-1", ToolCall{
		ID:        "call_1",
		Name:      "skill_run",
		Arguments: json.RawMessage(`{"name":"demo","script":"linked.sh"}`),
	})
	if err != nil {
		t.Fatalf("Execute(skill_run): %v", err)
	}
	if !result.IsError {
		t.Fatalf("result.IsError = false, want true with content %s", string(result.Content))
	}
}

func TestToolRuntimeMemoryAppendEncodesSessionErrors(t *testing.T) {
	dataDir := t.TempDir()
	runtime := NewToolRuntime(
		dataDir,
		NewSessionStore(dataDir),
		NewSkillStore(dataDir),
		NewMCPStore(dataDir),
		nil,
		nil,
	)

	result, err := runtime.Execute(context.Background(), "", ToolCall{
		ID:        "call_1",
		Name:      "memory_append",
		Arguments: json.RawMessage(`{"round":{"call":{"id":"call_1","name":"memory_append","arguments":{}},"result":{"content":{"ok":true},"isError":false}}}`),
	})
	if err != nil {
		t.Fatalf("Execute(memory_append): %v", err)
	}
	if !result.IsError {
		t.Fatalf("result.IsError = false, want true with content %s", string(result.Content))
	}
}

func TestToolRuntimeFSWriteCreatesParentDirectoriesAndWritesContent(t *testing.T) {
	dataDir := t.TempDir()
	runtime := NewToolRuntime(
		dataDir,
		NewSessionStore(dataDir),
		NewSkillStore(dataDir),
		NewMCPStore(dataDir),
		nil,
		nil,
	)

	result, err := runtime.Execute(context.Background(), "session-1", ToolCall{
		ID:        "call_1",
		Name:      "fs_write",
		Arguments: json.RawMessage(`{"path":"skills/generated-demo/output.txt","content":"hello entclaw"}`),
	})
	if err != nil {
		t.Fatalf("Execute(fs_write): %v", err)
	}
	if result.IsError {
		t.Fatalf("result.IsError = true, want false with content %s", string(result.Content))
	}

	var payload struct {
		Path    string `json:"path"`
		Written bool   `json:"written"`
		Bytes   int    `json:"bytes"`
	}
	if err := json.Unmarshal(result.Content, &payload); err != nil {
		t.Fatalf("json.Unmarshal(result.Content): %v", err)
	}
	if payload.Path != "skills/generated-demo/output.txt" {
		t.Fatalf("payload.Path = %q, want %q", payload.Path, "skills/generated-demo/output.txt")
	}
	if !payload.Written {
		t.Fatalf("payload.Written = false, want true")
	}
	if payload.Bytes != len("hello entclaw") {
		t.Fatalf("payload.Bytes = %d, want %d", payload.Bytes, len("hello entclaw"))
	}

	body, err := os.ReadFile(filepath.Join(dataDir, "entclaw", "skills", "generated-demo", "output.txt"))
	if err != nil {
		t.Fatalf("os.ReadFile(written file): %v", err)
	}
	if string(body) != "hello entclaw" {
		t.Fatalf("string(body) = %q, want %q", string(body), "hello entclaw")
	}
}

func TestToolRuntimeWriteCanonicalNameCreatesParentDirectoriesAndWritesContent(t *testing.T) {
	dataDir := t.TempDir()
	runtime := NewToolRuntime(
		dataDir,
		NewSessionStore(dataDir),
		NewSkillStore(dataDir),
		NewMCPStore(dataDir),
		nil,
		nil,
	)

	result, err := runtime.Execute(context.Background(), "session-1", ToolCall{
		ID:        "call_1",
		Name:      "write",
		Arguments: json.RawMessage(`{"path":"skills/generated-demo/output.txt","content":"hello entclaw"}`),
	})
	if err != nil {
		t.Fatalf("Execute(write): %v", err)
	}
	if result.IsError {
		t.Fatalf("result.IsError = true, want false with content %s", string(result.Content))
	}

	var payload struct {
		Path    string `json:"path"`
		Written bool   `json:"written"`
		Bytes   int    `json:"bytes"`
	}
	if err := json.Unmarshal(result.Content, &payload); err != nil {
		t.Fatalf("json.Unmarshal(result.Content): %v", err)
	}
	if payload.Path != "skills/generated-demo/output.txt" {
		t.Fatalf("payload.Path = %q, want %q", payload.Path, "skills/generated-demo/output.txt")
	}
	if !payload.Written {
		t.Fatalf("payload.Written = false, want true")
	}
	if payload.Bytes != len("hello entclaw") {
		t.Fatalf("payload.Bytes = %d, want %d", payload.Bytes, len("hello entclaw"))
	}

	body, err := os.ReadFile(filepath.Join(dataDir, "entclaw", "skills", "generated-demo", "output.txt"))
	if err != nil {
		t.Fatalf("os.ReadFile(written file): %v", err)
	}
	if string(body) != "hello entclaw" {
		t.Fatalf("string(body) = %q, want %q", string(body), "hello entclaw")
	}
}

func TestToolRuntimeEditCanonicalNameAppliesSequentialExactReplacements(t *testing.T) {
	dataDir := t.TempDir()
	root := filepath.Join(dataDir, "entclaw")
	if err := os.MkdirAll(filepath.Join(root, "skills", "demo"), 0o755); err != nil {
		t.Fatalf("os.MkdirAll: %v", err)
	}
	const original = "hello world\nhello again\n"
	if err := os.WriteFile(filepath.Join(root, "skills", "demo", "note.txt"), []byte(original), 0o644); err != nil {
		t.Fatalf("os.WriteFile: %v", err)
	}

	runtime := NewToolRuntime(
		dataDir,
		NewSessionStore(dataDir),
		NewSkillStore(dataDir),
		NewMCPStore(dataDir),
		nil,
		nil,
	)

	result, err := runtime.Execute(context.Background(), "session-1", ToolCall{
		ID:        "call_1",
		Name:      "edit",
		Arguments: json.RawMessage(`{"path":"skills/demo/note.txt","edits":[{"oldText":"hello world","newText":"goodbye world"},{"oldText":"hello again","newText":"goodbye again"}]}`),
	})
	if err != nil {
		t.Fatalf("Execute(edit): %v", err)
	}
	if result.IsError {
		t.Fatalf("result.IsError = true, want false with content %s", string(result.Content))
	}

	var payload struct {
		Path    string `json:"path"`
		Written bool   `json:"written"`
		Bytes   int    `json:"bytes"`
	}
	if err := json.Unmarshal(result.Content, &payload); err != nil {
		t.Fatalf("json.Unmarshal(result.Content): %v", err)
	}
	if payload.Path != "skills/demo/note.txt" {
		t.Fatalf("payload.Path = %q, want %q", payload.Path, "skills/demo/note.txt")
	}
	if !payload.Written {
		t.Fatalf("payload.Written = false, want true")
	}
	want := "goodbye world\ngoodbye again\n"
	if payload.Bytes != len(want) {
		t.Fatalf("payload.Bytes = %d, want %d", payload.Bytes, len(want))
	}

	body, err := os.ReadFile(filepath.Join(root, "skills", "demo", "note.txt"))
	if err != nil {
		t.Fatalf("os.ReadFile(edited file): %v", err)
	}
	if string(body) != want {
		t.Fatalf("string(body) = %q, want %q", string(body), want)
	}
}

func TestToolRuntimeEditReturnsErrorWhenExactTextNotFound(t *testing.T) {
	dataDir := t.TempDir()
	root := filepath.Join(dataDir, "entclaw")
	if err := os.MkdirAll(filepath.Join(root, "skills", "demo"), 0o755); err != nil {
		t.Fatalf("os.MkdirAll: %v", err)
	}
	const original = "hello world\n"
	filePath := filepath.Join(root, "skills", "demo", "note.txt")
	if err := os.WriteFile(filePath, []byte(original), 0o644); err != nil {
		t.Fatalf("os.WriteFile: %v", err)
	}

	runtime := NewToolRuntime(
		dataDir,
		NewSessionStore(dataDir),
		NewSkillStore(dataDir),
		NewMCPStore(dataDir),
		nil,
		nil,
	)

	result, err := runtime.Execute(context.Background(), "session-1", ToolCall{
		ID:        "call_1",
		Name:      "edit",
		Arguments: json.RawMessage(`{"path":"skills/demo/note.txt","edits":[{"oldText":"missing text","newText":"goodbye world"}]}`),
	})
	if err != nil {
		t.Fatalf("Execute(edit): %v", err)
	}
	if !result.IsError {
		t.Fatalf("result.IsError = false, want true with content %s", string(result.Content))
	}

	var payload struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(result.Content, &payload); err != nil {
		t.Fatalf("json.Unmarshal(result.Content): %v", err)
	}
	if payload.Error != "could not find exact text in file" {
		t.Fatalf("payload.Error = %q, want %q", payload.Error, "could not find exact text in file")
	}

	body, err := os.ReadFile(filePath)
	if err != nil {
		t.Fatalf("os.ReadFile(note.txt): %v", err)
	}
	if string(body) != original {
		t.Fatalf("string(body) = %q, want %q", string(body), original)
	}
}

func TestToolRuntimeEditRejectsMissingOrBlankPath(t *testing.T) {
	dataDir := t.TempDir()
	runtime := NewToolRuntime(
		dataDir,
		NewSessionStore(dataDir),
		NewSkillStore(dataDir),
		NewMCPStore(dataDir),
		nil,
		nil,
	)

	for _, tc := range []struct {
		name      string
		arguments string
	}{
		{name: "missing", arguments: `{"edits":[{"oldText":"hello","newText":""}]}`},
		{name: "blank", arguments: `{"path":"   ","edits":[{"oldText":"hello","newText":""}]}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result, err := runtime.Execute(context.Background(), "session-1", ToolCall{
				ID:        "call_1",
				Name:      "edit",
				Arguments: json.RawMessage(tc.arguments),
			})
			if err != nil {
				t.Fatalf("Execute(edit): %v", err)
			}
			if !result.IsError {
				t.Fatalf("result.IsError = false, want true with content %s", string(result.Content))
			}

			var payload struct {
				Error string `json:"error"`
			}
			if err := json.Unmarshal(result.Content, &payload); err != nil {
				t.Fatalf("json.Unmarshal(result.Content): %v", err)
			}
			if payload.Error != "path is required" {
				t.Fatalf("payload.Error = %q, want %q", payload.Error, "path is required")
			}
		})
	}
}

func TestToolRuntimeEditRejectsMissingOrEmptyEdits(t *testing.T) {
	dataDir := t.TempDir()
	runtime := NewToolRuntime(
		dataDir,
		NewSessionStore(dataDir),
		NewSkillStore(dataDir),
		NewMCPStore(dataDir),
		nil,
		nil,
	)

	for _, tc := range []struct {
		name      string
		arguments string
	}{
		{name: "missing", arguments: `{"path":"skills/demo/note.txt"}`},
		{name: "empty", arguments: `{"path":"skills/demo/note.txt","edits":[]}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result, err := runtime.Execute(context.Background(), "session-1", ToolCall{
				ID:        "call_1",
				Name:      "edit",
				Arguments: json.RawMessage(tc.arguments),
			})
			if err != nil {
				t.Fatalf("Execute(edit): %v", err)
			}
			if !result.IsError {
				t.Fatalf("result.IsError = false, want true with content %s", string(result.Content))
			}

			var payload struct {
				Error string `json:"error"`
			}
			if err := json.Unmarshal(result.Content, &payload); err != nil {
				t.Fatalf("json.Unmarshal(result.Content): %v", err)
			}
			if payload.Error != "edits is required" {
				t.Fatalf("payload.Error = %q, want %q", payload.Error, "edits is required")
			}
		})
	}
}

func TestToolRuntimeEditRejectsEmptyOldTextAndMissingNewText(t *testing.T) {
	dataDir := t.TempDir()
	runtime := NewToolRuntime(
		dataDir,
		NewSessionStore(dataDir),
		NewSkillStore(dataDir),
		NewMCPStore(dataDir),
		nil,
		nil,
	)

	for _, tc := range []struct {
		name      string
		arguments string
		wantError string
	}{
		{name: "empty oldText", arguments: `{"path":"skills/demo/note.txt","edits":[{"oldText":"   ","newText":""}]}`, wantError: "edits[0].oldText is required"},
		{name: "missing newText", arguments: `{"path":"skills/demo/note.txt","edits":[{"oldText":"hello"}]}`, wantError: "edits[0].newText is required"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result, err := runtime.Execute(context.Background(), "session-1", ToolCall{
				ID:        "call_1",
				Name:      "edit",
				Arguments: json.RawMessage(tc.arguments),
			})
			if err != nil {
				t.Fatalf("Execute(edit): %v", err)
			}
			if !result.IsError {
				t.Fatalf("result.IsError = false, want true with content %s", string(result.Content))
			}

			var payload struct {
				Error string `json:"error"`
			}
			if err := json.Unmarshal(result.Content, &payload); err != nil {
				t.Fatalf("json.Unmarshal(result.Content): %v", err)
			}
			if payload.Error != tc.wantError {
				t.Fatalf("payload.Error = %q, want %q", payload.Error, tc.wantError)
			}
		})
	}
}

func TestToolRuntimeFSWriteRejectsTraversal(t *testing.T) {
	dataDir := t.TempDir()
	runtime := NewToolRuntime(
		dataDir,
		NewSessionStore(dataDir),
		NewSkillStore(dataDir),
		NewMCPStore(dataDir),
		nil,
		nil,
	)

	result, err := runtime.Execute(context.Background(), "session-1", ToolCall{
		ID:        "call_1",
		Name:      "fs_write",
		Arguments: json.RawMessage(`{"path":"../escape.txt","content":"nope"}`),
	})
	if err != nil {
		t.Fatalf("Execute(fs_write): %v", err)
	}
	if !result.IsError {
		t.Fatalf("result.IsError = false, want true with content %s", string(result.Content))
	}
}

func TestToolRuntimeCommandExecPreservesFailurePayload(t *testing.T) {
	dataDir := t.TempDir()
	runtime := NewToolRuntime(
		dataDir,
		NewSessionStore(dataDir),
		NewSkillStore(dataDir),
		NewMCPStore(dataDir),
		nil,
		func(context.Context, CommandRequest) (CommandResult, error) {
			return CommandResult{
				Stdout:   "partial stdout",
				Stderr:   "boom",
				ExitCode: 17,
			}, errors.New("command failed")
		},
	)

	result, err := runtime.Execute(context.Background(), "session-1", ToolCall{
		ID:        "call_1",
		Name:      "command_exec",
		Arguments: json.RawMessage(`{"command":"sh","args":["-c","exit 17"]}`),
	})
	if err != nil {
		t.Fatalf("Execute(command_exec): %v", err)
	}
	if !result.IsError {
		t.Fatalf("result.IsError = false, want true with content %s", string(result.Content))
	}

	var payload struct {
		Stdout   string `json:"stdout"`
		Stderr   string `json:"stderr"`
		ExitCode int    `json:"exitCode"`
		Error    string `json:"error"`
	}
	if err := json.Unmarshal(result.Content, &payload); err != nil {
		t.Fatalf("json.Unmarshal(result.Content): %v", err)
	}
	if payload.Stdout != "partial stdout" || payload.Stderr != "boom" || payload.ExitCode != 17 {
		t.Fatalf("payload = %+v, want stdout/stderr/exitCode preserved", payload)
	}
	if payload.Error != "command failed" {
		t.Fatalf("payload.Error = %q, want command failed", payload.Error)
	}
}

func TestToolRuntimeExecCanonicalNamePreservesFailurePayload(t *testing.T) {
	dataDir := t.TempDir()
	runtime := NewToolRuntime(
		dataDir,
		NewSessionStore(dataDir),
		NewSkillStore(dataDir),
		NewMCPStore(dataDir),
		nil,
		func(context.Context, CommandRequest) (CommandResult, error) {
			return CommandResult{
				Stdout:   "partial stdout",
				Stderr:   "boom",
				ExitCode: 17,
			}, errors.New("command failed")
		},
	)

	result, err := runtime.Execute(context.Background(), "session-1", ToolCall{
		ID:        "call_1",
		Name:      "exec",
		Arguments: json.RawMessage(`{"command":"sh","args":["-c","exit 17"]}`),
	})
	if err != nil {
		t.Fatalf("Execute(exec): %v", err)
	}
	if !result.IsError {
		t.Fatalf("result.IsError = false, want true with content %s", string(result.Content))
	}

	var payload struct {
		Stdout   string `json:"stdout"`
		Stderr   string `json:"stderr"`
		ExitCode int    `json:"exitCode"`
		Error    string `json:"error"`
	}
	if err := json.Unmarshal(result.Content, &payload); err != nil {
		t.Fatalf("json.Unmarshal(result.Content): %v", err)
	}
	if payload.Stdout != "partial stdout" || payload.Stderr != "boom" || payload.ExitCode != 17 {
		t.Fatalf("payload = %+v, want stdout/stderr/exitCode preserved", payload)
	}
	if payload.Error != "command failed" {
		t.Fatalf("payload.Error = %q, want command failed", payload.Error)
	}
}

func TestToolRuntimeCommandExecCreatesWorkingDirectory(t *testing.T) {
	dataDir := t.TempDir()
	runtime := NewToolRuntime(
		dataDir,
		NewSessionStore(dataDir),
		NewSkillStore(dataDir),
		NewMCPStore(dataDir),
		nil,
		func(_ context.Context, request CommandRequest) (CommandResult, error) {
			info, err := os.Stat(request.WorkDir)
			if err != nil {
				t.Fatalf("os.Stat(request.WorkDir): %v", err)
			}
			if !info.IsDir() {
				t.Fatalf("request.WorkDir = %q, want directory", request.WorkDir)
			}
			if request.WorkDir != filepath.Join(dataDir, "entclaw") {
				t.Fatalf("request.WorkDir = %q, want %q", request.WorkDir, filepath.Join(dataDir, "entclaw"))
			}
			return CommandResult{
				Stdout:   "ok",
				ExitCode: 0,
			}, nil
		},
	)

	result, err := runtime.Execute(context.Background(), "session-1", ToolCall{
		ID:        "call_1",
		Name:      "command_exec",
		Arguments: json.RawMessage(`{"command":"pwd"}`),
	})
	if err != nil {
		t.Fatalf("Execute(command_exec): %v", err)
	}
	if result.IsError {
		t.Fatalf("result.IsError = true, want false with content %s", string(result.Content))
	}
}

func TestToolRuntimeReadCanonicalNameReturnsFileContent(t *testing.T) {
	dataDir := t.TempDir()
	root := filepath.Join(dataDir, "entclaw")
	if err := os.MkdirAll(filepath.Join(root, "skills", "demo"), 0o755); err != nil {
		t.Fatalf("os.MkdirAll: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "skills", "demo", "note.txt"), []byte("hello read"), 0o644); err != nil {
		t.Fatalf("os.WriteFile: %v", err)
	}

	runtime := NewToolRuntime(
		dataDir,
		NewSessionStore(dataDir),
		NewSkillStore(dataDir),
		NewMCPStore(dataDir),
		nil,
		nil,
	)

	result, err := runtime.Execute(context.Background(), "session-1", ToolCall{
		ID:        "call_1",
		Name:      "read",
		Arguments: json.RawMessage(`{"path":"skills/demo/note.txt"}`),
	})
	if err != nil {
		t.Fatalf("Execute(read): %v", err)
	}
	if result.IsError {
		t.Fatalf("result.IsError = true, want false with content %s", string(result.Content))
	}

	var payload struct {
		Path    string `json:"path"`
		Content string `json:"content"`
	}
	if err := json.Unmarshal(result.Content, &payload); err != nil {
		t.Fatalf("json.Unmarshal(result.Content): %v", err)
	}
	if payload.Path != "skills/demo/note.txt" {
		t.Fatalf("payload.Path = %q, want %q", payload.Path, "skills/demo/note.txt")
	}
	if payload.Content != "hello read" {
		t.Fatalf("payload.Content = %q, want %q", payload.Content, "hello read")
	}
}

func TestToolRuntimeReadCanonicalNameSupportsLinePagination(t *testing.T) {
	dataDir := t.TempDir()
	root := filepath.Join(dataDir, "entclaw")
	if err := os.MkdirAll(filepath.Join(root, "skills", "demo"), 0o755); err != nil {
		t.Fatalf("os.MkdirAll: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "skills", "demo", "note.txt"), []byte("line1\nline2\nline3\nline4"), 0o644); err != nil {
		t.Fatalf("os.WriteFile: %v", err)
	}

	runtime := NewToolRuntime(
		dataDir,
		NewSessionStore(dataDir),
		NewSkillStore(dataDir),
		NewMCPStore(dataDir),
		nil,
		nil,
	)

	result, err := runtime.Execute(context.Background(), "session-1", ToolCall{
		ID:        "call_1",
		Name:      "read",
		Arguments: json.RawMessage(`{"path":"skills/demo/note.txt","offset":2,"limit":2}`),
	})
	if err != nil {
		t.Fatalf("Execute(read): %v", err)
	}
	if result.IsError {
		t.Fatalf("result.IsError = true, want false with content %s", string(result.Content))
	}

	var payload struct {
		Path    string `json:"path"`
		Content string `json:"content"`
	}
	if err := json.Unmarshal(result.Content, &payload); err != nil {
		t.Fatalf("json.Unmarshal(result.Content): %v", err)
	}
	if payload.Path != "skills/demo/note.txt" {
		t.Fatalf("payload.Path = %q, want %q", payload.Path, "skills/demo/note.txt")
	}
	if payload.Content != "line2\nline3" {
		t.Fatalf("payload.Content = %q, want %q", payload.Content, "line2\nline3")
	}
}

func TestSliceReadContentBoundaryCases(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		body   string
		offset int
		limit  int
		want   string
	}{
		{
			name:   "offset less than or equal to zero starts at first line",
			body:   "line1\nline2\nline3",
			offset: 0,
			limit:  2,
			want:   "line1\nline2",
		},
		{
			name:   "non-positive limit returns remaining lines",
			body:   "line1\nline2\nline3",
			offset: 2,
			limit:  0,
			want:   "line2\nline3",
		},
		{
			name:   "offset beyond available lines returns empty string",
			body:   "line1\nline2",
			offset: 5,
			limit:  1,
			want:   "",
		},
		{
			name:   "trailing newline is preserved when reading remaining lines",
			body:   "line1\n",
			offset: 1,
			limit:  0,
			want:   "line1\n",
		},
		{
			name:   "empty content stays empty",
			body:   "",
			offset: 1,
			limit:  1,
			want:   "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := sliceReadContent(tt.body, tt.offset, tt.limit); got != tt.want {
				t.Fatalf("sliceReadContent(%q, %d, %d) = %q, want %q", tt.body, tt.offset, tt.limit, got, tt.want)
			}
		})
	}
}

func TestToolRuntimeFSReadLegacyAliasReturnsFileContent(t *testing.T) {
	dataDir := t.TempDir()
	root := filepath.Join(dataDir, "entclaw")
	if err := os.MkdirAll(filepath.Join(root, "skills", "demo"), 0o755); err != nil {
		t.Fatalf("os.MkdirAll: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "skills", "demo", "note.txt"), []byte("hello read"), 0o644); err != nil {
		t.Fatalf("os.WriteFile: %v", err)
	}

	runtime := NewToolRuntime(
		dataDir,
		NewSessionStore(dataDir),
		NewSkillStore(dataDir),
		NewMCPStore(dataDir),
		nil,
		nil,
	)

	result, err := runtime.Execute(context.Background(), "session-1", ToolCall{
		ID:        "call_1",
		Name:      "fs_read",
		Arguments: json.RawMessage(`{"path":"skills/demo/note.txt"}`),
	})
	if err != nil {
		t.Fatalf("Execute(fs_read): %v", err)
	}
	if result.IsError {
		t.Fatalf("result.IsError = true, want false with content %s", string(result.Content))
	}

	var payload struct {
		Path    string `json:"path"`
		Content string `json:"content"`
	}
	if err := json.Unmarshal(result.Content, &payload); err != nil {
		t.Fatalf("json.Unmarshal(result.Content): %v", err)
	}
	if payload.Path != "skills/demo/note.txt" {
		t.Fatalf("payload.Path = %q, want %q", payload.Path, "skills/demo/note.txt")
	}
	if payload.Content != "hello read" {
		t.Fatalf("payload.Content = %q, want %q", payload.Content, "hello read")
	}
}

func TestDefaultCommandRunnerMarksStartupFailureWithNonZeroExitCode(t *testing.T) {
	result, err := defaultCommandRunner(context.Background(), CommandRequest{
		WorkDir: t.TempDir(),
		Args:    []string{"__definitely_missing_command__"},
	})
	if err == nil {
		t.Fatal("defaultCommandRunner error = nil, want startup failure")
	}
	if result.ExitCode == 0 {
		t.Fatalf("result.ExitCode = %d, want non-zero for startup failure", result.ExitCode)
	}
}

func TestToolRuntimeMCPCallRejectsLegacyInputField(t *testing.T) {
	dataDir := t.TempDir()
	runtime := NewToolRuntime(
		dataDir,
		NewSessionStore(dataDir),
		NewSkillStore(dataDir),
		NewMCPStore(dataDir),
		func(context.Context, string, json.RawMessage, json.RawMessage) (json.RawMessage, error) {
			t.Fatal("unexpected mcp call")
			return nil, nil
		},
		nil,
	)

	result, err := runtime.Execute(context.Background(), "session-1", ToolCall{
		ID:   "call_1",
		Name: "mcp_call",
		Arguments: json.RawMessage(`{
			"name":"demo",
			"input":{"legacy":true}
		}`),
	})
	if err != nil {
		t.Fatalf("Execute(mcp_call): %v", err)
	}
	if !result.IsError {
		t.Fatalf("result.IsError = false, want true with content %s", string(result.Content))
	}
}
