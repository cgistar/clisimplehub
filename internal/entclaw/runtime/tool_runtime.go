package entclawruntime

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

type MCPCaller func(ctx context.Context, name string, config json.RawMessage, arguments json.RawMessage) (json.RawMessage, error)

type CommandRequest struct {
	WorkDir string
	Args    []string
}

type CommandResult struct {
	Stdout   string `json:"stdout"`
	Stderr   string `json:"stderr"`
	ExitCode int    `json:"exitCode"`
}

type CommandRunner func(ctx context.Context, request CommandRequest) (CommandResult, error)

type ToolRuntime struct {
	root     string
	sessions SessionStore
	skills   SkillStore
	mcp      MCPStore
	guard    PathGuard
	mcpCall  MCPCaller
	commands CommandRunner
}

func NewToolRuntime(dataRoot string, sessions SessionStore, skills SkillStore, mcp MCPStore, caller MCPCaller, commands CommandRunner) *ToolRuntime {
	if commands == nil {
		commands = defaultCommandRunner
	}

	entclawRoot := filepath.Join(dataRoot, "entclaw")
	return &ToolRuntime{
		root:     dataRoot,
		sessions: sessions,
		skills:   skills,
		mcp:      mcp,
		guard:    NewPathGuard(entclawRoot),
		mcpCall:  caller,
		commands: commands,
	}
}

func (r *ToolRuntime) Execute(ctx context.Context, sessionID string, call ToolCall) (ToolResult, error) {
	if r == nil {
		return ToolResult{}, fmt.Errorf("tool runtime is nil")
	}
	if err := ctx.Err(); err != nil {
		return ToolResult{}, err
	}

	switch normalizeToolName(call.Name) {
	case "skill_list":
		names, err := r.skills.List(ctx)
		if names == nil {
			names = []string{}
		}
		return marshalToolPayload(map[string]any{"skills": names}, err)
	case "skill_read":
		var input struct {
			Name string `json:"name"`
		}
		if err := json.Unmarshal(rawJSONObjectOrEmpty(call.Arguments), &input); err != nil {
			return ToolResult{}, err
		}
		content, err := r.skills.Read(ctx, input.Name)
		return marshalToolPayload(map[string]any{
			"name":    strings.TrimSpace(input.Name),
			"content": content,
		}, err)
	case "skill_write":
		var input struct {
			Name    string `json:"name"`
			Content string `json:"content"`
		}
		if err := json.Unmarshal(rawJSONObjectOrEmpty(call.Arguments), &input); err != nil {
			return ToolResult{}, err
		}
		return marshalToolPayload(map[string]any{
			"name":    strings.TrimSpace(input.Name),
			"written": true,
		}, r.skills.Write(ctx, input.Name, input.Content))
	case "skill_run":
		var input struct {
			Name   string   `json:"name"`
			Script string   `json:"script"`
			Args   []string `json:"args"`
		}
		if err := json.Unmarshal(rawJSONObjectOrEmpty(call.Arguments), &input); err != nil {
			return ToolResult{}, err
		}

		scriptPath, workDir, err := r.skills.ResolveScriptPath(input.Name, input.Script)
		if err != nil {
			return errorToolResult(err), nil
		}

		result, err := r.commands(ctx, CommandRequest{
			WorkDir: workDir,
			Args:    append([]string{scriptPath}, append([]string(nil), input.Args...)...),
		})

		payload := map[string]any{
			"skill":    strings.TrimSpace(input.Name),
			"script":   strings.TrimSpace(input.Script),
			"stdout":   result.Stdout,
			"stderr":   result.Stderr,
			"exitCode": result.ExitCode,
		}
		if err != nil {
			payload["error"] = err.Error()
		}

		body, marshalErr := json.Marshal(payload)
		if marshalErr != nil {
			return ToolResult{}, marshalErr
		}
		return ToolResult{
			Content: body,
			IsError: err != nil || result.ExitCode != 0,
		}, nil
	case "memory_append":
		var input struct {
			Round ToolRound `json:"round"`
		}
		if err := json.Unmarshal(rawJSONObjectOrEmpty(call.Arguments), &input); err != nil {
			return ToolResult{}, err
		}

		session, err := r.sessions.LoadOrCreate(ctx, sessionID, SessionSeed{})
		if err != nil {
			return errorToolResult(err), nil
		}
		session.ToolHistory = append(session.ToolHistory, cloneToolHistory([]ToolRound{input.Round})...)
		return marshalToolPayload(map[string]any{
			"appended": true,
			"count":    len(session.ToolHistory),
		}, r.sessions.Save(ctx, session))
	case "read":
		var input struct {
			Path string `json:"path"`
		}
		if err := json.Unmarshal(rawJSONObjectOrEmpty(call.Arguments), &input); err != nil {
			return ToolResult{}, err
		}

		path, err := r.guard.Resolve(input.Path)
		if err != nil {
			return errorToolResult(err), nil
		}
		body, err := os.ReadFile(path)
		return marshalToolPayload(map[string]any{
			"path":    input.Path,
			"content": string(body),
		}, err)
	case "write":
		var input struct {
			Path    string `json:"path"`
			Content string `json:"content"`
		}
		if err := json.Unmarshal(rawJSONObjectOrEmpty(call.Arguments), &input); err != nil {
			return ToolResult{}, err
		}

		if err := os.MkdirAll(r.guard.root, 0o755); err != nil {
			return errorToolResult(fmt.Errorf("create entclaw root: %w", err)), nil
		}
		path, err := r.guard.Resolve(input.Path)
		if err != nil {
			return errorToolResult(err), nil
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return errorToolResult(fmt.Errorf("create parent directory: %w", err)), nil
		}
		if err := os.WriteFile(path, []byte(input.Content), 0o644); err != nil {
			return errorToolResult(err), nil
		}
		return marshalToolPayload(map[string]any{
			"path":    input.Path,
			"written": true,
			"bytes":   len(input.Content),
		}, nil)
	case "mcp_call":
		var input struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
			Input     json.RawMessage `json:"input"`
		}
		if err := json.Unmarshal(rawJSONObjectOrEmpty(call.Arguments), &input); err != nil {
			return ToolResult{}, err
		}
		if len(input.Input) > 0 {
			return errorToolResult(fmt.Errorf("mcp_call input field is unsupported; use arguments")), nil
		}
		if r.mcpCall == nil {
			return errorToolResult(fmt.Errorf("mcp caller is not configured")), nil
		}

		config, err := r.mcp.Read(ctx, input.Name)
		if err != nil {
			return errorToolResult(err), nil
		}

		output, err := r.mcpCall(ctx, input.Name, config, rawJSONObjectOrEmpty(input.Arguments))
		return marshalToolPayload(map[string]any{
			"name":   strings.TrimSpace(input.Name),
			"output": json.RawMessage(rawJSONObjectOrEmpty(output)),
		}, err)
	case "exec":
		var input struct {
			Command string   `json:"command"`
			Args    []string `json:"args"`
		}
		if err := json.Unmarshal(rawJSONObjectOrEmpty(call.Arguments), &input); err != nil {
			return ToolResult{}, err
		}

		args := append([]string(nil), input.Args...)
		if strings.TrimSpace(input.Command) != "" {
			args = append([]string{input.Command}, args...)
		}
		workDir := filepath.Join(r.root, "entclaw")
		if err := os.MkdirAll(workDir, 0o755); err != nil {
			return errorToolResult(err), nil
		}

		result, err := r.commands(ctx, CommandRequest{
			WorkDir: workDir,
			Args:    args,
		})
		return marshalCommandResult(result, err)
	default:
		return errorToolResult(fmt.Errorf("unsupported tool %q", call.Name)), nil
	}
}

func defaultCommandRunner(ctx context.Context, request CommandRequest) (CommandResult, error) {
	if len(request.Args) == 0 {
		return CommandResult{ExitCode: -1}, fmt.Errorf("command args are required")
	}

	cmd := exec.CommandContext(ctx, request.Args[0], request.Args[1:]...)
	cmd.Dir = request.WorkDir

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	result := CommandResult{
		Stdout:   stdout.String(),
		Stderr:   stderr.String(),
		ExitCode: 0,
	}
	if err == nil {
		return result, nil
	}

	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		result.ExitCode = exitErr.ExitCode()
		return result, err
	}
	result.ExitCode = -1
	return result, err
}

func marshalToolPayload(payload any, runErr error) (ToolResult, error) {
	if runErr != nil {
		return errorToolResult(runErr), nil
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return ToolResult{}, err
	}
	return ToolResult{
		Content: body,
		IsError: false,
	}, nil
}

func errorToolResult(err error) ToolResult {
	body, marshalErr := json.Marshal(map[string]any{
		"error": err.Error(),
	})
	if marshalErr != nil {
		body = json.RawMessage(`{"error":"tool execution failed"}`)
	}
	return ToolResult{
		Content: body,
		IsError: true,
	}
}

func marshalCommandResult(result CommandResult, runErr error) (ToolResult, error) {
	payload := map[string]any{
		"stdout":   result.Stdout,
		"stderr":   result.Stderr,
		"exitCode": result.ExitCode,
	}
	if runErr != nil {
		payload["error"] = runErr.Error()
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return ToolResult{}, err
	}
	return ToolResult{
		Content: body,
		IsError: runErr != nil || result.ExitCode != 0,
	}, nil
}
