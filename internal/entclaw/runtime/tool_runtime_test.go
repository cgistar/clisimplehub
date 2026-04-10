package entclawruntime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/tidwall/gjson"
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

func TestBuiltinToolDefinitionsExposeApplyPatchSchema(t *testing.T) {
	t.Parallel()

	tools := builtinToolDefinitions()
	body, err := json.Marshal(map[string]any{"tools": tools})
	if err != nil {
		t.Fatalf("json.Marshal(tools): %v", err)
	}

	root := gjson.ParseBytes(body)
	tool := root.Get(`tools.#(name="apply_patch")`)
	if !tool.Exists() {
		t.Fatalf("tools = %s, want apply_patch", root.Get("tools").Raw)
	}
	if !tool.Get(`parameters.required.#(=="input")`).Exists() {
		t.Fatalf("apply_patch required = %s, want input required", tool.Get("parameters.required").Raw)
	}
	if tool.Get(`parameters.required.#`).Int() != 1 {
		t.Fatalf("apply_patch required count = %d, want 1", tool.Get(`parameters.required.#`).Int())
	}
	if tool.Get(`parameters.properties.input.type`).String() != "string" {
		t.Fatalf("apply_patch input schema = %s, want string", tool.Get("parameters.properties.input").Raw)
	}
	if tool.Get(`parameters.properties.path`).Exists() || tool.Get(`parameters.properties.content`).Exists() {
		t.Fatalf("apply_patch schema = %s, should expose only input", tool.Get("parameters.properties").Raw)
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

func TestToolRuntimeApplyPatchAddsFile(t *testing.T) {
	dataDir := t.TempDir()
	runtime := NewToolRuntime(dataDir, NewSessionStore(dataDir), NewSkillStore(dataDir), NewMCPStore(dataDir), nil, nil)

	patch := "*** Begin Patch\n*** Add File: hello.txt\n+hello\n*** End Patch\n"
	result, err := runtime.Execute(context.Background(), "session-1", ToolCall{
		ID:        "call_1",
		Name:      "apply_patch",
		Arguments: json.RawMessage(fmt.Sprintf(`{"input":%q}`, patch)),
	})
	if err != nil {
		t.Fatalf("Execute(apply_patch): %v", err)
	}
	if result.IsError {
		t.Fatalf("result.IsError = true, want false with content %s", string(result.Content))
	}

	var payload struct {
		Added    []string `json:"added"`
		Modified []string `json:"modified"`
		Deleted  []string `json:"deleted"`
	}
	if err := json.Unmarshal(result.Content, &payload); err != nil {
		t.Fatalf("json.Unmarshal(result.Content): %v", err)
	}
	if len(payload.Added) != 1 || payload.Added[0] != "hello.txt" {
		t.Fatalf("payload.Added = %#v, want [hello.txt]", payload.Added)
	}

	body, err := os.ReadFile(filepath.Join(dataDir, "entclaw", "hello.txt"))
	if err != nil {
		t.Fatalf("os.ReadFile(hello.txt): %v", err)
	}
	if string(body) != "hello\n" {
		t.Fatalf("string(body) = %q, want %q", string(body), "hello\n")
	}
}

func TestToolRuntimeApplyPatchUpdatesAndDeletesFiles(t *testing.T) {
	dataDir := t.TempDir()
	root := filepath.Join(dataDir, "entclaw")
	if err := os.MkdirAll(filepath.Join(root, "dir"), 0o755); err != nil {
		t.Fatalf("os.MkdirAll: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "dir", "note.txt"), []byte("old line\n"), 0o640); err != nil {
		t.Fatalf("os.WriteFile(note.txt): %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "dir", "delete.txt"), []byte("delete me\n"), 0o600); err != nil {
		t.Fatalf("os.WriteFile(delete.txt): %v", err)
	}

	runtime := NewToolRuntime(dataDir, NewSessionStore(dataDir), NewSkillStore(dataDir), NewMCPStore(dataDir), nil, nil)
	patch := "*** Begin Patch\n" +
		"*** Update File: dir/note.txt\n" +
		"@@\n" +
		"-old line\n" +
		"+new line\n" +
		"*** Delete File: dir/delete.txt\n" +
		"*** End Patch\n"
	result, err := runtime.Execute(context.Background(), "session-1", ToolCall{
		ID:        "call_1",
		Name:      "apply_patch",
		Arguments: json.RawMessage(fmt.Sprintf(`{"input":%q}`, patch)),
	})
	if err != nil {
		t.Fatalf("Execute(apply_patch): %v", err)
	}
	if result.IsError {
		t.Fatalf("result.IsError = true, want false with content %s", string(result.Content))
	}

	var payload struct {
		Added    []string `json:"added"`
		Modified []string `json:"modified"`
		Deleted  []string `json:"deleted"`
	}
	if err := json.Unmarshal(result.Content, &payload); err != nil {
		t.Fatalf("json.Unmarshal(result.Content): %v", err)
	}
	if len(payload.Modified) != 1 || payload.Modified[0] != "dir/note.txt" {
		t.Fatalf("payload.Modified = %#v, want [dir/note.txt]", payload.Modified)
	}
	if len(payload.Deleted) != 1 || payload.Deleted[0] != "dir/delete.txt" {
		t.Fatalf("payload.Deleted = %#v, want [dir/delete.txt]", payload.Deleted)
	}

	body, err := os.ReadFile(filepath.Join(root, "dir", "note.txt"))
	if err != nil {
		t.Fatalf("os.ReadFile(note.txt): %v", err)
	}
	if string(body) != "new line\n" {
		t.Fatalf("string(body) = %q, want %q", string(body), "new line\n")
	}
	if _, err := os.Stat(filepath.Join(root, "dir", "delete.txt")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("os.Stat(delete.txt) err = %v, want not exists", err)
	}
}

func TestToolRuntimeApplyPatchRejectsUnsupportedPartialUpdate(t *testing.T) {
	dataDir := t.TempDir()
	root := filepath.Join(dataDir, "entclaw")
	if err := os.MkdirAll(filepath.Join(root, "dir"), 0o755); err != nil {
		t.Fatalf("os.MkdirAll: %v", err)
	}
	filePath := filepath.Join(root, "dir", "note.txt")
	original := "keep top\nold line\nkeep bottom\n"
	if err := os.WriteFile(filePath, []byte(original), 0o644); err != nil {
		t.Fatalf("os.WriteFile(note.txt): %v", err)
	}

	runtime := NewToolRuntime(dataDir, NewSessionStore(dataDir), NewSkillStore(dataDir), NewMCPStore(dataDir), nil, nil)
	patch := "*** Begin Patch\n" +
		"*** Update File: dir/note.txt\n" +
		"@@\n" +
		" keep top\n" +
		"-old line\n" +
		"+new line\n" +
		" keep bottom\n" +
		"*** End Patch\n"
	result, err := runtime.Execute(context.Background(), "session-1", ToolCall{
		ID:        "call_1",
		Name:      "apply_patch",
		Arguments: json.RawMessage(fmt.Sprintf(`{"input":%q}`, patch)),
	})
	if err != nil {
		t.Fatalf("Execute(apply_patch): %v", err)
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
	if payload.Error != "apply_patch update hunks only support full-file replacement in v1" {
		t.Fatalf("payload.Error = %q, want %q", payload.Error, "apply_patch update hunks only support full-file replacement in v1")
	}

	body, err := os.ReadFile(filePath)
	if err != nil {
		t.Fatalf("os.ReadFile(note.txt): %v", err)
	}
	if string(body) != original {
		t.Fatalf("string(body) = %q, want %q", string(body), original)
	}
}

func TestToolRuntimeApplyPatchRejectsDuplicateNormalizedTargetPaths(t *testing.T) {
	dataDir := t.TempDir()
	runtime := NewToolRuntime(dataDir, NewSessionStore(dataDir), NewSkillStore(dataDir), NewMCPStore(dataDir), nil, nil)

	patch := "*** Begin Patch\n" +
		"*** Add File: nested/../same.txt\n" +
		"+first\n" +
		"*** Add File: same.txt\n" +
		"+second\n" +
		"*** End Patch\n"
	result, err := runtime.Execute(context.Background(), "session-1", ToolCall{
		ID:        "call_1",
		Name:      "apply_patch",
		Arguments: json.RawMessage(fmt.Sprintf(`{"input":%q}`, patch)),
	})
	if err != nil {
		t.Fatalf("Execute(apply_patch): %v", err)
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
	if payload.Error != "apply_patch multiple operations on the same path are unsupported in v1" {
		t.Fatalf("payload.Error = %q, want %q", payload.Error, "apply_patch multiple operations on the same path are unsupported in v1")
	}

	if _, err := os.Stat(filepath.Join(dataDir, "entclaw", "same.txt")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("os.Stat(same.txt) err = %v, want not exists", err)
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
		{name: "empty oldText", arguments: `{"path":"skills/demo/note.txt","edits":[{"oldText":"","newText":""}]}`, wantError: "edits[0].oldText is required"},
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

func TestToolRuntimeEditAllowsWhitespaceOnlyOldTextReplacement(t *testing.T) {
	dataDir := t.TempDir()
	root := filepath.Join(dataDir, "entclaw")
	if err := os.MkdirAll(filepath.Join(root, "skills", "demo"), 0o755); err != nil {
		t.Fatalf("os.MkdirAll: %v", err)
	}
	filePath := filepath.Join(root, "skills", "demo", "note.txt")
	if err := os.WriteFile(filePath, []byte("keep  spaces"), 0o644); err != nil {
		t.Fatalf("os.WriteFile: %v", err)
	}

	runtime := NewToolRuntime(dataDir, NewSessionStore(dataDir), NewSkillStore(dataDir), NewMCPStore(dataDir), nil, nil)
	result, err := runtime.Execute(context.Background(), "session-1", ToolCall{
		ID:        "call_1",
		Name:      "edit",
		Arguments: json.RawMessage(`{"path":"skills/demo/note.txt","edits":[{"oldText":"  ","newText":"--"}]}`),
	})
	if err != nil {
		t.Fatalf("Execute(edit): %v", err)
	}
	if result.IsError {
		t.Fatalf("result.IsError = true, want false with content %s", string(result.Content))
	}
	body, err := os.ReadFile(filePath)
	if err != nil {
		t.Fatalf("os.ReadFile(note.txt): %v", err)
	}
	if string(body) != "keep--spaces" {
		t.Fatalf("string(body) = %q, want %q", string(body), "keep--spaces")
	}
}

func TestToolRuntimeEditAllowsEmptyNewTextDeletion(t *testing.T) {
	dataDir := t.TempDir()
	root := filepath.Join(dataDir, "entclaw")
	if err := os.MkdirAll(filepath.Join(root, "skills", "demo"), 0o755); err != nil {
		t.Fatalf("os.MkdirAll: %v", err)
	}
	filePath := filepath.Join(root, "skills", "demo", "note.txt")
	if err := os.WriteFile(filePath, []byte("hello world"), 0o644); err != nil {
		t.Fatalf("os.WriteFile: %v", err)
	}

	runtime := NewToolRuntime(dataDir, NewSessionStore(dataDir), NewSkillStore(dataDir), NewMCPStore(dataDir), nil, nil)
	result, err := runtime.Execute(context.Background(), "session-1", ToolCall{
		ID:        "call_1",
		Name:      "edit",
		Arguments: json.RawMessage(`{"path":"skills/demo/note.txt","edits":[{"oldText":" world","newText":""}]}`),
	})
	if err != nil {
		t.Fatalf("Execute(edit): %v", err)
	}
	if result.IsError {
		t.Fatalf("result.IsError = true, want false with content %s", string(result.Content))
	}
	body, err := os.ReadFile(filePath)
	if err != nil {
		t.Fatalf("os.ReadFile(note.txt): %v", err)
	}
	if string(body) != "hello" {
		t.Fatalf("string(body) = %q, want %q", string(body), "hello")
	}
}

func TestToolRuntimeEditPreservesOriginalFileMode(t *testing.T) {
	dataDir := t.TempDir()
	root := filepath.Join(dataDir, "entclaw")
	if err := os.MkdirAll(filepath.Join(root, "skills", "demo"), 0o755); err != nil {
		t.Fatalf("os.MkdirAll: %v", err)
	}
	filePath := filepath.Join(root, "skills", "demo", "note.txt")
	if err := os.WriteFile(filePath, []byte("hello world"), 0o600); err != nil {
		t.Fatalf("os.WriteFile: %v", err)
	}

	runtime := NewToolRuntime(dataDir, NewSessionStore(dataDir), NewSkillStore(dataDir), NewMCPStore(dataDir), nil, nil)
	result, err := runtime.Execute(context.Background(), "session-1", ToolCall{
		ID:        "call_1",
		Name:      "edit",
		Arguments: json.RawMessage(`{"path":"skills/demo/note.txt","edits":[{"oldText":"world","newText":"entclaw"}]}`),
	})
	if err != nil {
		t.Fatalf("Execute(edit): %v", err)
	}
	if result.IsError {
		t.Fatalf("result.IsError = true, want false with content %s", string(result.Content))
	}
	info, err := os.Stat(filePath)
	if err != nil {
		t.Fatalf("os.Stat(note.txt): %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("info.Mode().Perm() = %#o, want %#o", info.Mode().Perm(), os.FileMode(0o600))
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

func TestToolRuntimeExecAcceptsCommandStringAndWorkdir(t *testing.T) {
	dataDir := t.TempDir()
	var gotRequest CommandRequest
	runtime := NewToolRuntime(
		dataDir,
		NewSessionStore(dataDir),
		NewSkillStore(dataDir),
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
		ID:   "call_1",
		Name: "exec",
		Arguments: json.RawMessage(`{
			"command":"pwd",
			"workdir":"skills",
			"env":{"DEMO":"1"},
			"yieldMs":25,
			"pty":true,
			"elevated":true
		}`),
	})
	if err != nil {
		t.Fatalf("Execute(exec): %v", err)
	}
	if result.IsError {
		t.Fatalf("result.IsError = true, want false with content %s", string(result.Content))
	}
	if gotRequest.WorkDir != filepath.Join(dataDir, "entclaw", "skills") {
		t.Fatalf("gotRequest.WorkDir = %q, want %q", gotRequest.WorkDir, filepath.Join(dataDir, "entclaw", "skills"))
	}
	if len(gotRequest.Args) == 0 {
		t.Fatalf("gotRequest.Args = %#v, want shell or command args", gotRequest.Args)
	}
	if gotRequest.Env["DEMO"] != "1" {
		t.Fatalf("gotRequest.Env = %#v, want DEMO=1", gotRequest.Env)
	}

	var payload struct {
		Stdout   string   `json:"stdout"`
		ExitCode int      `json:"exitCode"`
		Warnings []string `json:"warnings"`
	}
	if err := json.Unmarshal(result.Content, &payload); err != nil {
		t.Fatalf("json.Unmarshal(result.Content): %v", err)
	}
	if payload.Stdout != "ok" || payload.ExitCode != 0 {
		t.Fatalf("payload = %+v, want ok/0", payload)
	}
	if !containsString(payload.Warnings, "yieldMs is not supported in v1") {
		t.Fatalf("payload.Warnings = %#v, want yieldMs warning", payload.Warnings)
	}
	if !containsString(payload.Warnings, "pty is not supported in v1") {
		t.Fatalf("payload.Warnings = %#v, want pty warning", payload.Warnings)
	}
	if !containsString(payload.Warnings, "elevated is not supported in v1") {
		t.Fatalf("payload.Warnings = %#v, want elevated warning", payload.Warnings)
	}
}

func TestToolRuntimeProcessTracksBackgroundExecLifecycle(t *testing.T) {
	dataDir := t.TempDir()
	runtime := NewToolRuntime(
		dataDir,
		NewSessionStore(dataDir),
		NewSkillStore(dataDir),
		NewMCPStore(dataDir),
		nil,
		nil,
	)

	startResult, err := runtime.Execute(context.Background(), "session-1", ToolCall{
		ID:   "call_1",
		Name: "exec",
		Arguments: json.RawMessage(`{
			"command":"printf 'first\n'; sleep 0.1; printf 'second\n'",
			"background":true
		}`),
	})
	if err != nil {
		t.Fatalf("Execute(exec background): %v", err)
	}
	if startResult.IsError {
		t.Fatalf("startResult.IsError = true, want false with content %s", string(startResult.Content))
	}

	var started struct {
		SessionID  string `json:"sessionId"`
		Background bool   `json:"background"`
		Running    bool   `json:"running"`
	}
	if err := json.Unmarshal(startResult.Content, &started); err != nil {
		t.Fatalf("json.Unmarshal(startResult.Content): %v", err)
	}
	if started.SessionID == "" {
		t.Fatalf("started = %+v, want sessionId", started)
	}
	if !started.Background || !started.Running {
		t.Fatalf("started = %+v, want background/running true", started)
	}

	listResult, err := runtime.Execute(context.Background(), "session-1", ToolCall{
		ID:        "call_2",
		Name:      "process",
		Arguments: json.RawMessage(`{"action":"list"}`),
	})
	if err != nil {
		t.Fatalf("Execute(process list): %v", err)
	}
	if listResult.IsError {
		t.Fatalf("listResult.IsError = true, want false with content %s", string(listResult.Content))
	}

	var listPayload struct {
		Processes []struct {
			SessionID string `json:"sessionId"`
		} `json:"processes"`
	}
	if err := json.Unmarshal(listResult.Content, &listPayload); err != nil {
		t.Fatalf("json.Unmarshal(listResult.Content): %v", err)
	}
	if len(listPayload.Processes) == 0 || listPayload.Processes[0].SessionID == "" {
		t.Fatalf("listPayload = %+v, want listed session", listPayload)
	}

	pollResult, err := runtime.Execute(context.Background(), "session-1", ToolCall{
		ID:   "call_3",
		Name: "process",
		Arguments: json.RawMessage(fmt.Sprintf(`{
			"action":"poll",
			"sessionId":"%s",
			"timeout":1000
		}`, started.SessionID)),
	})
	if err != nil {
		t.Fatalf("Execute(process poll): %v", err)
	}
	if pollResult.IsError {
		t.Fatalf("pollResult.IsError = true, want false with content %s", string(pollResult.Content))
	}

	var pollPayload struct {
		SessionID string `json:"sessionId"`
		Running   bool   `json:"running"`
		ExitCode  int    `json:"exitCode"`
	}
	if err := json.Unmarshal(pollResult.Content, &pollPayload); err != nil {
		t.Fatalf("json.Unmarshal(pollResult.Content): %v", err)
	}
	if pollPayload.SessionID != started.SessionID {
		t.Fatalf("pollPayload.SessionID = %q, want %q", pollPayload.SessionID, started.SessionID)
	}
	if pollPayload.Running {
		t.Fatalf("pollPayload = %+v, want completed process", pollPayload)
	}
	if pollPayload.ExitCode != 0 {
		t.Fatalf("pollPayload.ExitCode = %d, want 0", pollPayload.ExitCode)
	}

	logResult, err := runtime.Execute(context.Background(), "session-1", ToolCall{
		ID:   "call_4",
		Name: "process",
		Arguments: json.RawMessage(fmt.Sprintf(`{
			"action":"log",
			"sessionId":"%s"
		}`, started.SessionID)),
	})
	if err != nil {
		t.Fatalf("Execute(process log): %v", err)
	}
	if logResult.IsError {
		t.Fatalf("logResult.IsError = true, want false with content %s", string(logResult.Content))
	}

	var logPayload struct {
		Content string `json:"content"`
	}
	if err := json.Unmarshal(logResult.Content, &logPayload); err != nil {
		t.Fatalf("json.Unmarshal(logResult.Content): %v", err)
	}
	if logPayload.Content != "first\nsecond\n" {
		t.Fatalf("logPayload.Content = %q, want %q", logPayload.Content, "first\nsecond\n")
	}
}

func TestToolRuntimeProcessKillStopsRunningSession(t *testing.T) {
	dataDir := t.TempDir()
	runtime := NewToolRuntime(
		dataDir,
		NewSessionStore(dataDir),
		NewSkillStore(dataDir),
		NewMCPStore(dataDir),
		nil,
		nil,
	)

	startResult, err := runtime.Execute(context.Background(), "session-1", ToolCall{
		ID:   "call_1",
		Name: "exec",
		Arguments: json.RawMessage(`{
			"command":"sleep 5",
			"background":true
		}`),
	})
	if err != nil {
		t.Fatalf("Execute(exec background): %v", err)
	}
	if startResult.IsError {
		t.Fatalf("startResult.IsError = true, want false with content %s", string(startResult.Content))
	}

	var started struct {
		SessionID string `json:"sessionId"`
	}
	if err := json.Unmarshal(startResult.Content, &started); err != nil {
		t.Fatalf("json.Unmarshal(startResult.Content): %v", err)
	}
	if started.SessionID == "" {
		t.Fatalf("started = %+v, want sessionId", started)
	}

	killResult, err := runtime.Execute(context.Background(), "session-1", ToolCall{
		ID:   "call_2",
		Name: "process",
		Arguments: json.RawMessage(fmt.Sprintf(`{
			"action":"kill",
			"sessionId":"%s"
		}`, started.SessionID)),
	})
	if err != nil {
		t.Fatalf("Execute(process kill): %v", err)
	}
	if killResult.IsError {
		t.Fatalf("killResult.IsError = true, want false with content %s", string(killResult.Content))
	}

	var killPayload struct {
		SessionID string `json:"sessionId"`
		Killed    bool   `json:"killed"`
	}
	if err := json.Unmarshal(killResult.Content, &killPayload); err != nil {
		t.Fatalf("json.Unmarshal(killResult.Content): %v", err)
	}
	if killPayload.SessionID != started.SessionID || !killPayload.Killed {
		t.Fatalf("killPayload = %+v, want killed session", killPayload)
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

func containsString(items []string, want string) bool {
	for _, item := range items {
		if item == want {
			return true
		}
	}
	return false
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
