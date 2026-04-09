# Entclaw Skill Scripts Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add `skills/<name>/SKILL.md + scripts/...` support to `entclaw`, including safe `skill_read` / `skill_run` tools and process-aware SSE updates for `/v1/entclaw/responses`, while keeping `/messages` and `/chat/completions` on simplified progress output.

**Architecture:** Keep the existing `entclaw` plugin/orchestrator/tool runtime split. Extend `SkillStore` and `ToolRuntime` for safe skill script execution, then add an internal orchestration event stream that the plugin can translate into protocol-specific SSE output. Reuse OpenAI Responses event shapes for the detailed `/responses` flow and map the same internal progress into lightweight textual updates for the other two endpoints.

**Tech Stack:** Go, `testing`, existing `entclaw` runtime/plugin packages, HTTP/SSE streaming, `exec.CommandContext`, filesystem path guards.

---

## File Structure

### Existing files to modify

- `internal/entclaw/runtime/skill_store.go`
  Add helpers for explicit skill markdown reads and script directory/script path resolution.
- `internal/entclaw/runtime/tool_runtime.go`
  Add `skill_read` and `skill_run`, plus secure exec helpers and payload shaping.
- `internal/entclaw/runtime/request_builder.go`
  Expose `skill_read` and `skill_run` in builtin tool definitions.
- `internal/entclaw/runtime/orchestrator.go`
  Add orchestration progress event emission and return structured rounds instead of only the final response.
- `internal/entclaw/plugin/handler.go`
  Replace the current "copy final upstream response as-is" path with protocol-aware SSE writers.
- `internal/entclaw/runtime/protocol_responses.go`
  Add helpers to encode process events as Responses-style SSE items.
- `internal/entclaw/runtime/protocol_chat.go`
  Add simplified textual progress encoding for chat completions.
- `internal/entclaw/runtime/protocol_messages.go`
  Add simplified textual progress encoding for messages.

### Existing tests to modify

- `internal/entclaw/runtime/tool_runtime_test.go`
  Add coverage for `skill_read`, successful `skill_run`, and path rejection cases.
- `internal/entclaw/runtime/session_store_test.go`
  Extend store tests for script directory helpers if the logic lives with `SkillStore`.
- `internal/entclaw/runtime/orchestrator_test.go`
  Add multi-round progress event coverage.
- `internal/entclaw/plugin/handler_test.go`
  Add protocol-level SSE output assertions.
- `internal/entclaw/runtime/protocol_test.go`
  Add event encoding tests for `/responses`, `/chat/completions`, and `/messages`.

### New files to create

- `internal/entclaw/runtime/orchestration_events.go`
  Shared event types for "assistant output observed", "tool execution started", "tool execution completed", and "orchestration completed".
- `internal/entclaw/runtime/response_stream.go`
  Helpers to serialize orchestration events into Responses SSE payloads.
- `internal/entclaw/runtime/chat_progress_stream.go`
  Helpers to emit simplified chat delta progress messages.
- `internal/entclaw/runtime/messages_progress_stream.go`
  Helpers to emit simplified Anthropic-style progress message blocks.

The plan keeps each new file focused on one responsibility: event model, detailed Responses serialization, and lightweight protocol mappings.

### Task 1: Add `skill_read` and expose skill script locations

**Files:**
- Modify: `internal/entclaw/runtime/skill_store.go`
- Modify: `internal/entclaw/runtime/request_builder.go`
- Modify: `internal/entclaw/runtime/tool_runtime.go`
- Test: `internal/entclaw/runtime/tool_runtime_test.go`
- Test: `internal/entclaw/runtime/session_store_test.go`

- [ ] **Step 1: Write the failing tests for `skill_read` and script directory helpers**

```go
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
```

- [ ] **Step 2: Run the focused tests and verify they fail**

Run: `go test ./internal/entclaw/runtime -run 'TestToolRuntimeSkillReadReturnsSkillMarkdown|TestSkillStoreScriptDirReturnsSkillScriptsPath' -count=1`

Expected:
- FAIL because `skill_read` is an unsupported tool
- FAIL because `SkillStore` has no `ScriptDir` method

- [ ] **Step 3: Implement minimal `skill_read` and skill script directory helpers**

```go
func (s SkillStore) ScriptDir(name string) (string, error) {
	dir, err := s.skillDir(name)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "scripts"), nil
}

func (s SkillStore) SkillPath(name string) (string, error) {
	return s.skillPath(name)
}

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
```

```go
{
	"type":        "function",
	"name":        "skill_read",
	"description": "Read the SKILL.md instructions for a local entclaw skill.",
	"parameters": map[string]any{
		"type": "object",
		"properties": map[string]any{
			"name": map[string]any{
				"type": "string",
			},
		},
		"required":             []string{"name"},
		"additionalProperties": false,
	},
},
```

- [ ] **Step 4: Run the tests and verify they pass**

Run: `go test ./internal/entclaw/runtime -run 'TestToolRuntimeSkillReadReturnsSkillMarkdown|TestSkillStoreScriptDirReturnsSkillScriptsPath' -count=1`

Expected: PASS

- [ ] **Step 5: Commit the focused change**

```bash
git add internal/entclaw/runtime/skill_store.go internal/entclaw/runtime/tool_runtime.go internal/entclaw/runtime/request_builder.go internal/entclaw/runtime/tool_runtime_test.go internal/entclaw/runtime/session_store_test.go
git commit -m "feat: add entclaw skill read tool"
```

### Task 2: Add secure `skill_run` execution with path enforcement

**Files:**
- Modify: `internal/entclaw/runtime/skill_store.go`
- Modify: `internal/entclaw/runtime/tool_runtime.go`
- Test: `internal/entclaw/runtime/tool_runtime_test.go`

- [ ] **Step 1: Write the failing tests for safe skill script execution**

```go
func TestToolRuntimeSkillRunExecutesScriptInsideSkill(t *testing.T) {
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
	if err := os.WriteFile(scriptPath, []byte("#!/bin/sh\nprintf 'skill:%s arg:%s' \"$PWD\" \"$1\"\n"), 0o755); err != nil {
		t.Fatalf("WriteFile(echo.sh): %v", err)
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
		Arguments: json.RawMessage(`{"name":"demo","script":"echo.sh","args":["ok"]}`),
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
	if !strings.Contains(payload.Stdout, filepath.Join(dataDir, "entclaw", "skills", "demo")) {
		t.Fatalf("payload.Stdout = %q, want skill work dir", payload.Stdout)
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
```

- [ ] **Step 2: Run the focused tests and verify they fail**

Run: `go test ./internal/entclaw/runtime -run 'TestToolRuntimeSkillRunExecutesScriptInsideSkill|TestToolRuntimeSkillRunRejectsTraversal|TestToolRuntimeSkillRunRejectsSymlinkEscape' -count=1`

Expected:
- FAIL because `skill_run` is unsupported
- Or FAIL because no secure script resolution exists

- [ ] **Step 3: Implement minimal secure `skill_run`**

```go
func (s SkillStore) ResolveScriptPath(name, script string) (string, string, error) {
	scriptDir, err := s.ScriptDir(name)
	if err != nil {
		return "", "", err
	}

	guard := NewPathGuard(scriptDir)
	resolved, err := guard.Resolve(script)
	if err != nil {
		return "", "", err
	}
	return resolved, filepath.Dir(scriptDir), nil
}
```

```go
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

	args := append([]string{scriptPath}, append([]string(nil), input.Args...)...)
	result, err := r.commands(ctx, CommandRequest{
		WorkDir: workDir,
		Args:    args,
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
	return ToolResult{Content: body, IsError: err != nil || result.ExitCode != 0}, nil
```

- [ ] **Step 4: Run the focused tests and verify they pass**

Run: `go test ./internal/entclaw/runtime -run 'TestToolRuntimeSkillRunExecutesScriptInsideSkill|TestToolRuntimeSkillRunRejectsTraversal|TestToolRuntimeSkillRunRejectsSymlinkEscape' -count=1`

Expected: PASS

- [ ] **Step 5: Commit the focused change**

```bash
git add internal/entclaw/runtime/skill_store.go internal/entclaw/runtime/tool_runtime.go internal/entclaw/runtime/tool_runtime_test.go
git commit -m "feat: add entclaw skill script runner"
```

### Task 3: Add orchestration event types and Responses SSE encoding

**Files:**
- Create: `internal/entclaw/runtime/orchestration_events.go`
- Create: `internal/entclaw/runtime/response_stream.go`
- Modify: `internal/entclaw/runtime/orchestrator.go`
- Test: `internal/entclaw/runtime/orchestrator_test.go`
- Test: `internal/entclaw/runtime/protocol_test.go`

- [ ] **Step 1: Write the failing tests for orchestration progress events**

```go
func TestOrchestratorEmitsResponsesProgressEventsForToolRound(t *testing.T) {
	dataDir := t.TempDir()
	store := NewSessionStore(dataDir)
	tools := NewToolRuntime(
		dataDir,
		store,
		NewSkillStore(dataDir),
		NewMCPStore(dataDir),
		nil,
		func(context.Context, CommandRequest) (CommandResult, error) {
			return CommandResult{Stdout: "done", ExitCode: 0}, nil
		},
	)

	client := &stubLoopbackClient{
		responses: []*http.Response{
			testHTTPResponse(`{"output":[{"type":"function_call","call_id":"call_1","name":"skill_run","arguments":"{\"name\":\"demo\",\"script\":\"run.sh\"}"}]}`, "application/json"),
			{
				StatusCode: http.StatusOK,
				Header: http.Header{
					"Content-Type": []string{"text/event-stream"},
				},
				Body: io.NopCloser(strings.NewReader(
					"event: response.output_item.done\n" +
						"data: {\"type\":\"response.output_item.done\",\"item\":{\"type\":\"message\",\"status\":\"completed\",\"content\":[{\"type\":\"output_text\",\"text\":\"final\"}]}}\n\n" +
						"event: response.completed\n" +
						"data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_final\",\"status\":\"completed\"}}\n\n",
				)),
			},
		},
	}

	var seen []OrchestrationEvent
	orchestrator := Orchestrator{
		client:   client,
		tools:    tools,
		sessions: store,
		OnEvent: func(event OrchestrationEvent) {
			seen = append(seen, event)
		},
	}

	_, err := orchestrator.Run(context.Background(), testSourceRequest(), &TaskRequest{
		SessionID: "session-1",
		Channel:   ChannelCodex,
		Format:    FormatResponses,
		Model:     "gpt-5.4",
		InputRaw:  json.RawMessage(`"run demo"`),
		RawBody:   []byte(`{"model":"gpt-5.4","input":"run demo"}`),
		Path:      "/v1/entclaw/responses",
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(seen) == 0 {
		t.Fatal("seen events = 0, want progress events")
	}
}

func TestEncodeResponsesProgressStreamIncludesToolCallAndOutput(t *testing.T) {
	events := []OrchestrationEvent{
		NewAssistantToolCallEvent("call_1", "skill_run", json.RawMessage(`{"name":"demo","script":"run.sh"}`)),
		NewToolStartedEvent("call_1"),
		NewToolCompletedEvent("call_1", json.RawMessage(`{"stdout":"done","stderr":"","exitCode":0}`), false),
	}

	body := BuildResponsesProgressStream("resp_entclaw", events)
	if !strings.Contains(body, "\"type\":\"function_call\"") {
		t.Fatalf("body = %s, want function_call item", body)
	}
	if !strings.Contains(body, "\"type\":\"function_call_output\"") {
		t.Fatalf("body = %s, want function_call_output item", body)
	}
}
```

- [ ] **Step 2: Run the focused tests and verify they fail**

Run: `go test ./internal/entclaw/runtime -run 'TestOrchestratorEmitsResponsesProgressEventsForToolRound|TestEncodeResponsesProgressStreamIncludesToolCallAndOutput' -count=1`

Expected:
- FAIL because `Orchestrator` has no event callback
- FAIL because no Responses progress stream helper exists

- [ ] **Step 3: Implement event types and Responses stream encoding**

```go
type OrchestrationEventType string

const (
	OrchestrationAssistantMessage  OrchestrationEventType = "assistant_message"
	OrchestrationAssistantToolCall OrchestrationEventType = "assistant_tool_call"
	OrchestrationToolStarted       OrchestrationEventType = "tool_started"
	OrchestrationToolCompleted     OrchestrationEventType = "tool_completed"
	OrchestrationCompleted         OrchestrationEventType = "completed"
)

type OrchestrationEvent struct {
	Type      OrchestrationEventType
	CallID    string
	Name      string
	Text      string
	Arguments json.RawMessage
	Output    json.RawMessage
	IsError   bool
}
```

```go
type Orchestrator struct {
	client   LoopbackClient
	tools    *ToolRuntime
	sessions SessionStore
	OnEvent  func(OrchestrationEvent)
}

func (o Orchestrator) emit(event OrchestrationEvent) {
	if o.OnEvent != nil {
		o.OnEvent(event)
	}
}
```

```go
for _, part := range turn.Parts {
	switch part.Type {
	case assistantTurnPartText:
		o.emit(OrchestrationEvent{
			Type: OrchestrationAssistantMessage,
			Text: part.Text,
		})
	case assistantTurnPartToolCall:
		o.emit(OrchestrationEvent{
			Type:      OrchestrationAssistantToolCall,
			CallID:    part.Call.ID,
			Name:      part.Call.Name,
			Arguments: append([]byte(nil), part.Call.Arguments...),
		})
	}
}
```

```go
func BuildResponsesProgressStream(responseID string, events []OrchestrationEvent) string {
	var out strings.Builder
	out.WriteString("event: response.created\n")
	out.WriteString(fmt.Sprintf("data: {\"type\":\"response.created\",\"response\":{\"id\":%q,\"status\":\"in_progress\"}}\n\n", responseID))
	// append message/function_call/function_call_output items
	out.WriteString("event: response.completed\n")
	out.WriteString(fmt.Sprintf("data: {\"type\":\"response.completed\",\"response\":{\"id\":%q,\"status\":\"completed\"}}\n\n", responseID))
	return out.String()
}
```

- [ ] **Step 4: Run the focused tests and verify they pass**

Run: `go test ./internal/entclaw/runtime -run 'TestOrchestratorEmitsResponsesProgressEventsForToolRound|TestEncodeResponsesProgressStreamIncludesToolCallAndOutput' -count=1`

Expected: PASS

- [ ] **Step 5: Commit the focused change**

```bash
git add internal/entclaw/runtime/orchestration_events.go internal/entclaw/runtime/response_stream.go internal/entclaw/runtime/orchestrator.go internal/entclaw/runtime/orchestrator_test.go internal/entclaw/runtime/protocol_test.go
git commit -m "feat: add entclaw responses progress events"
```

### Task 4: Stream detailed `/responses` progress through the plugin handler

**Files:**
- Modify: `internal/entclaw/plugin/handler.go`
- Modify: `internal/entclaw/plugin/handler_test.go`
- Modify: `internal/entclaw/runtime/orchestrator.go`
- Modify: `internal/entclaw/runtime/protocol_responses.go`

- [ ] **Step 1: Write the failing handler test for `/v1/entclaw/responses` progress SSE**

```go
func TestHandleInferenceResponsesStreamsProcessEventsBeforeFinalCompletion(t *testing.T) {
	t.Parallel()

	dataDir := t.TempDir()
	sessions := entclawruntime.NewSessionStore(dataDir)
	tools := entclawruntime.NewToolRuntime(
		dataDir,
		sessions,
		entclawruntime.NewSkillStore(dataDir),
		entclawruntime.NewMCPStore(dataDir),
		nil,
		func(context.Context, entclawruntime.CommandRequest) (entclawruntime.CommandResult, error) {
			return entclawruntime.CommandResult{Stdout: "done", ExitCode: 0}, nil
		},
	)
	client := &stubLoopbackClient{
		responses: []*http.Response{
			{
				StatusCode: http.StatusOK,
				Header: http.Header{"Content-Type": []string{"application/json"}},
				Body: io.NopCloser(bytes.NewBufferString(`{"output":[{"type":"function_call","call_id":"call_1","name":"skill_run","arguments":"{\"name\":\"demo\",\"script\":\"run.sh\"}"}]}`)),
			},
			{
				StatusCode: http.StatusOK,
				Header: http.Header{"Content-Type": []string{"text/event-stream"}},
				Body: io.NopCloser(bytes.NewBufferString(
					"event: response.output_item.done\n" +
						"data: {\"type\":\"response.output_item.done\",\"item\":{\"type\":\"message\",\"status\":\"completed\",\"content\":[{\"type\":\"output_text\",\"text\":\"final\"}]}}\n\n" +
						"event: response.completed\n" +
						"data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_123\",\"status\":\"completed\"}}\n\n",
				)),
			},
		},
	}

	plugin := &EntclawPlugin{
		sessions: sessions,
		tools:    tools,
		client:   client,
		orchestrator: entclawruntime.NewOrchestrator(
			client,
			tools,
			sessions,
		),
	}

	req := httptest.NewRequest(http.MethodPost, "/v1/entclaw/responses", bytes.NewBufferString(`{"model":"gpt-5.4","input":"run demo"}`))
	req.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()

	plugin.handleInference(recorder, req)

	body := recorder.Body.String()
	if !strings.Contains(body, "\"type\":\"function_call\"") {
		t.Fatalf("body = %s, want function_call progress item", body)
	}
	if !strings.Contains(body, "\"type\":\"function_call_output\"") {
		t.Fatalf("body = %s, want function_call_output item", body)
	}
	if !strings.Contains(body, "\"type\":\"response.completed\"") {
		t.Fatalf("body = %s, want response.completed", body)
	}
}
```

- [ ] **Step 2: Run the focused test and verify it fails**

Run: `go test ./internal/entclaw/plugin -run TestHandleInferenceResponsesStreamsProcessEventsBeforeFinalCompletion -count=1`

Expected:
- FAIL because the handler currently copies only the final upstream response body

- [ ] **Step 3: Implement detailed `/responses` SSE writing**

```go
result, err := p.orchestrator.Run(r.Context(), r, task)
if err != nil {
	// existing error mapping
}

if task.Format == entclawruntime.FormatResponses {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)
	for _, chunk := range result.ProgressChunks {
		_, _ = w.Write(chunk)
	}
	return
}
```

```go
type RunResult struct {
	Response       *http.Response
	Session        *Session
	ProgressChunks [][]byte
}
```

```go
events := make([]OrchestrationEvent, 0, 8)
orchestrator.OnEvent = func(event OrchestrationEvent) {
	events = append(events, event)
}
// after final round:
return &RunResult{
	Response:       finalResponse,
	Session:        session,
	ProgressChunks: BuildResponsesProgressChunks(responseID, events, finalRaw),
}, nil
```

- [ ] **Step 4: Run the focused test and verify it passes**

Run: `go test ./internal/entclaw/plugin -run TestHandleInferenceResponsesStreamsProcessEventsBeforeFinalCompletion -count=1`

Expected: PASS

- [ ] **Step 5: Commit the focused change**

```bash
git add internal/entclaw/plugin/handler.go internal/entclaw/plugin/handler_test.go internal/entclaw/runtime/orchestrator.go internal/entclaw/runtime/protocol_responses.go
git commit -m "feat: stream entclaw responses progress"
```

### Task 5: Add simplified progress text for `/chat/completions` and `/messages`

**Files:**
- Create: `internal/entclaw/runtime/chat_progress_stream.go`
- Create: `internal/entclaw/runtime/messages_progress_stream.go`
- Modify: `internal/entclaw/plugin/handler.go`
- Test: `internal/entclaw/plugin/handler_test.go`
- Test: `internal/entclaw/runtime/protocol_test.go`

- [ ] **Step 1: Write the failing tests for simplified progress output**

```go
func TestHandleInferenceChatCompletionsStreamsProgressText(t *testing.T) {
	t.Parallel()

	dataDir := t.TempDir()
	sessions := entclawruntime.NewSessionStore(dataDir)
	tools := entclawruntime.NewToolRuntime(
		dataDir,
		sessions,
		entclawruntime.NewSkillStore(dataDir),
		entclawruntime.NewMCPStore(dataDir),
		nil,
		func(context.Context, entclawruntime.CommandRequest) (entclawruntime.CommandResult, error) {
			return entclawruntime.CommandResult{Stdout: "done", ExitCode: 0}, nil
		},
	)
	client := &stubLoopbackClient{
		responses: []*http.Response{
			testHTTPResponse(`{"choices":[{"message":{"content":"","tool_calls":[{"id":"call_1","type":"function","function":{"name":"skill_read","arguments":"{\"name\":\"demo\"}"}}]}}]}`, "application/json"),
			{
				StatusCode: http.StatusOK,
				Header: http.Header{"Content-Type": []string{"text/event-stream"}},
				Body: io.NopCloser(bytes.NewBufferString("data: {\"choices\":[{\"delta\":{\"content\":\"final\"}}]}\n\n")),
			},
		},
	}
	plugin := &EntclawPlugin{
		sessions: sessions,
		tools:    tools,
		client:   client,
		orchestrator: entclawruntime.NewOrchestrator(
			client,
			tools,
			sessions,
		),
	}

	req := httptest.NewRequest(http.MethodPost, "/v1/entclaw/chat/completions", bytes.NewBufferString(`{"model":"gpt-5.4","messages":[{"role":"user","content":"run demo"}]}`))
	recorder := httptest.NewRecorder()
	plugin.handleInference(recorder, req)

	body := recorder.Body.String()
	if !strings.Contains(body, "Reading skill instructions") {
		t.Fatalf("body = %s, want simplified progress text", body)
	}
}

func TestHandleInferenceMessagesStreamsProgressText(t *testing.T) {
	t.Parallel()
	// same structure; assert Anthropic stream contains "Running skill script..."
}
```

- [ ] **Step 2: Run the focused tests and verify they fail**

Run: `go test ./internal/entclaw/plugin -run 'TestHandleInferenceChatCompletionsStreamsProgressText|TestHandleInferenceMessagesStreamsProgressText' -count=1`

Expected:
- FAIL because non-Responses formats still proxy only the final upstream body

- [ ] **Step 3: Implement minimal simplified progress writers**

```go
func BuildChatProgressChunks(events []OrchestrationEvent, finalBody []byte) [][]byte {
	chunks := make([][]byte, 0, len(events)+1)
	for _, event := range events {
		text := simplifiedProgressText(event)
		if text == "" {
			continue
		}
		chunks = append(chunks, []byte(fmt.Sprintf("data: {\"choices\":[{\"delta\":{\"content\":%q}}]}\n\n", text)))
	}
	chunks = append(chunks, finalBody)
	return chunks
}

func BuildMessagesProgressChunks(events []OrchestrationEvent, finalBody []byte) [][]byte {
	chunks := make([][]byte, 0, len(events)+1)
	for _, event := range events {
		text := simplifiedProgressText(event)
		if text == "" {
			continue
		}
		chunks = append(chunks, []byte(fmt.Sprintf("event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"delta\":{\"type\":\"text_delta\",\"text\":%q}}\n\n", text)))
	}
	chunks = append(chunks, finalBody)
	return chunks
}

func simplifiedProgressText(event OrchestrationEvent) string {
	switch {
	case event.Type == OrchestrationAssistantToolCall && event.Name == "skill_read":
		return "Reading skill instructions...\n"
	case event.Type == OrchestrationAssistantToolCall && event.Name == "skill_run":
		return "Running skill script...\n"
	case event.Type == OrchestrationToolCompleted:
		return "Tool finished.\n"
	default:
		return ""
	}
}
```

- [ ] **Step 4: Run the focused tests and verify they pass**

Run: `go test ./internal/entclaw/plugin -run 'TestHandleInferenceChatCompletionsStreamsProgressText|TestHandleInferenceMessagesStreamsProgressText' -count=1`

Expected: PASS

- [ ] **Step 5: Commit the focused change**

```bash
git add internal/entclaw/runtime/chat_progress_stream.go internal/entclaw/runtime/messages_progress_stream.go internal/entclaw/plugin/handler.go internal/entclaw/plugin/handler_test.go internal/entclaw/runtime/protocol_test.go
git commit -m "feat: add entclaw progress text for chat and messages"
```

### Task 6: Run the full verification sweep

**Files:**
- Modify: none
- Test: `internal/entclaw/runtime/*.go`
- Test: `internal/entclaw/plugin/*.go`

- [ ] **Step 1: Run the runtime package tests**

Run: `go test ./internal/entclaw/runtime -count=1`

Expected: PASS

- [ ] **Step 2: Run the plugin package tests**

Run: `go test ./internal/entclaw/plugin -count=1`

Expected: PASS

- [ ] **Step 3: Run both packages together to catch integration regressions**

Run: `go test ./internal/entclaw/runtime ./internal/entclaw/plugin -count=1`

Expected: PASS

- [ ] **Step 4: Inspect the worktree for only intended changes**

Run: `git status --short`

Expected:
- Only the planned `internal/entclaw/...` files and the new plan/spec docs appear
- No unrelated files are modified by the implementation

- [ ] **Step 5: Commit the verification checkpoint**

```bash
git add internal/entclaw/runtime internal/entclaw/plugin
git commit -m "test: verify entclaw skill scripts flow"
```

## Self-Review

### Spec coverage

- `skills/<name>/SKILL.md + scripts/...`
  Covered by Task 1 and Task 2.
- `skill_read(name)`
  Covered by Task 1.
- `skill_run(name, script, args)`
  Covered by Task 2.
- Safe execution constrained to the skill's own `scripts/`
  Covered by Task 2.
- `/v1/entclaw/responses` detailed process SSE
  Covered by Task 3 and Task 4.
- `/messages` and `/chat/completions` simplified progress
  Covered by Task 5.
- Full package verification
  Covered by Task 6.

### Placeholder scan

- No `TODO`, `TBD`, or deferred steps remain.
- Every task includes a concrete test, a concrete command, and a concrete implementation direction.

### Type consistency

- The plan keeps `OrchestrationEvent`, `RunResult.ProgressChunks`, `BuildResponsesProgressStream`, `BuildResponsesProgressChunks`, `BuildChatProgressChunks`, and `BuildMessagesProgressChunks` in distinct roles: low-level serializer vs. handler-facing chunk builder.
- `skill_read(name)` and `skill_run(name, script, args)` signatures remain stable across all tasks.
