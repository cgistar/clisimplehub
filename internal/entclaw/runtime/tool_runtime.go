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
	goruntime "runtime"
	"strings"
	"time"
)

type MCPCaller func(ctx context.Context, name string, config json.RawMessage, arguments json.RawMessage) (json.RawMessage, error)

type CommandRequest struct {
	WorkDir string
	Args    []string
	Env     map[string]string
	Timeout time.Duration
}

type CommandResult struct {
	Stdout   string `json:"stdout"`
	Stderr   string `json:"stderr"`
	ExitCode int    `json:"exitCode"`
}

type CommandRunner func(ctx context.Context, request CommandRequest) (CommandResult, error)

type readRequest struct {
	Path   string `json:"path"`
	Offset int    `json:"offset"`
	Limit  int    `json:"limit"`
}

type execRequestInput struct {
	Command    *string           `json:"command"`
	Args       []string          `json:"args"`
	WorkDir    string            `json:"workdir"`
	Env        map[string]string `json:"env"`
	YieldMs    *int              `json:"yieldMs"`
	Background bool              `json:"background"`
	Timeout    int               `json:"timeout"`
	Pty        *bool             `json:"pty"`
	Elevated   json.RawMessage   `json:"elevated"`
	Host       json.RawMessage   `json:"host"`
	Security   json.RawMessage   `json:"security"`
	Ask        json.RawMessage   `json:"ask"`
	Node       json.RawMessage   `json:"node"`
}

type ToolRuntime struct {
	root     string
	sessions SessionStore
	skills   SkillStore
	mcp      MCPStore
	guard    PathGuard
	mcpCall  MCPCaller
	commands CommandRunner
	process  *ProcessStore
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
		process:  NewProcessStore(),
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
	case toolNameRead:
		var input readRequest
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
			"content": sliceReadContent(string(body), input.Offset, input.Limit),
		}, err)
	case toolNameWrite:
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
	case "edit":
		input, err := decodeEditRequest(call.Arguments)
		if err != nil {
			return errorToolResult(err), nil
		}

		path, err := r.guard.Resolve(input.Path)
		if err != nil {
			return errorToolResult(err), nil
		}
		info, err := os.Stat(path)
		if err != nil {
			return errorToolResult(err), nil
		}
		body, err := os.ReadFile(path)
		if err != nil {
			return errorToolResult(err), nil
		}
		updated, err := applyEdits(string(body), input.Edits)
		if err != nil {
			return errorToolResult(err), nil
		}
		if err := os.WriteFile(path, []byte(updated), info.Mode().Perm()); err != nil {
			return errorToolResult(err), nil
		}
		return marshalToolPayload(map[string]any{
			"path":    input.Path,
			"written": true,
			"bytes":   len(updated),
		}, nil)
	case "apply_patch":
		return r.executeApplyPatch(call.Arguments)
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
	case "process":
		return r.executeProcess(call.Arguments)
	case toolNameExec:
		var input execRequestInput
		if err := json.Unmarshal(rawJSONObjectOrEmpty(call.Arguments), &input); err != nil {
			return ToolResult{}, err
		}
		if input.Command == nil || strings.TrimSpace(*input.Command) == "" {
			return errorToolResult(fmt.Errorf("command is required")), nil
		}
		if err := os.MkdirAll(r.guard.root, 0o755); err != nil {
			return errorToolResult(fmt.Errorf("create entclaw root: %w", err)), nil
		}
		workDir, err := r.guard.Resolve(input.WorkDir)
		if err != nil {
			return errorToolResult(err), nil
		}
		if err := os.MkdirAll(workDir, 0o755); err != nil {
			return errorToolResult(fmt.Errorf("create exec working directory: %w", err)), nil
		}

		command := strings.TrimSpace(*input.Command)
		args := buildExecArgs(command, input.Args)
		request := CommandRequest{
			WorkDir: workDir,
			Args:    args,
			Env:     cloneStringMap(input.Env),
			Timeout: durationFromMillis(input.Timeout),
		}
		warnings := collectExecWarnings(input)
		if input.Background {
			started, err := r.process.Start(command, request)
			if err != nil {
				return errorToolResult(err), nil
			}
			payload := map[string]any{
				"sessionId":  started.SessionID,
				"background": true,
				"running":    started.Running,
				"command":    started.Command,
			}
			if len(warnings) > 0 {
				payload["warnings"] = warnings
			}
			return marshalToolPayload(payload, nil)
		}

		result, err := r.commands(ctx, request)
		return marshalCommandResult(result, err, warnings)
	default:
		return errorToolResult(fmt.Errorf("unsupported tool %q", call.Name)), nil
	}
}

func defaultCommandRunner(ctx context.Context, request CommandRequest) (CommandResult, error) {
	if len(request.Args) == 0 {
		return CommandResult{ExitCode: -1}, fmt.Errorf("command args are required")
	}

	if request.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, request.Timeout)
		defer cancel()
	}

	cmd := exec.CommandContext(ctx, request.Args[0], request.Args[1:]...)
	cmd.Dir = request.WorkDir
	if len(request.Env) > 0 {
		cmd.Env = mergeCommandEnv(request.Env)
	}

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

func buildExecArgs(command string, extraArgs []string) []string {
	args := append([]string(nil), extraArgs...)
	if len(args) > 0 {
		return append([]string{command}, args...)
	}
	if goruntime.GOOS == "windows" {
		return []string{"cmd.exe", "/C", command}
	}
	return []string{"sh", "-lc", command}
}

func collectExecWarnings(input execRequestInput) []string {
	warnings := make([]string, 0, 7)
	if input.YieldMs != nil {
		warnings = append(warnings, "yieldMs is not supported in v1")
	}
	if input.Pty != nil && *input.Pty {
		warnings = append(warnings, "pty is not supported in v1")
	}
	for _, unsupported := range []struct {
		Name  string
		Value json.RawMessage
	}{
		{Name: "elevated", Value: input.Elevated},
		{Name: "host", Value: input.Host},
		{Name: "security", Value: input.Security},
		{Name: "ask", Value: input.Ask},
		{Name: "node", Value: input.Node},
	} {
		if len(unsupported.Value) == 0 {
			continue
		}
		warnings = append(warnings, unsupported.Name+" is not supported in v1")
	}
	return warnings
}

func cloneStringMap(input map[string]string) map[string]string {
	if len(input) == 0 {
		return nil
	}
	cloned := make(map[string]string, len(input))
	for key, value := range input {
		cloned[key] = value
	}
	return cloned
}

func durationFromMillis(value int) time.Duration {
	if value <= 0 {
		return 0
	}
	return time.Duration(value) * time.Millisecond
}

func mergeCommandEnv(overrides map[string]string) []string {
	merged := make([]string, 0, len(os.Environ())+len(overrides))
	index := make(map[string]int, len(overrides))
	for _, entry := range os.Environ() {
		name, _, ok := strings.Cut(entry, "=")
		if !ok {
			merged = append(merged, entry)
			continue
		}
		index[name] = len(merged)
		merged = append(merged, entry)
	}
	for name, value := range overrides {
		entry := name + "=" + value
		if idx, ok := index[name]; ok {
			merged[idx] = entry
			continue
		}
		merged = append(merged, entry)
	}
	return merged
}

func sliceReadContent(body string, offset, limit int) string {
	lines := strings.Split(body, "\n")
	start := 0
	if offset > 1 {
		start = offset - 1
	}
	if start > len(lines) {
		start = len(lines)
	}

	end := len(lines)
	if limit > 0 && start+limit < end {
		end = start + limit
	}

	return strings.Join(lines[start:end], "\n")
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

func marshalCommandResult(result CommandResult, runErr error, warnings []string) (ToolResult, error) {
	payload := map[string]any{
		"stdout":   result.Stdout,
		"stderr":   result.Stderr,
		"exitCode": result.ExitCode,
	}
	if len(warnings) > 0 {
		payload["warnings"] = warnings
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
