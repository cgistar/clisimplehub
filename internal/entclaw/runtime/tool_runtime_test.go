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
