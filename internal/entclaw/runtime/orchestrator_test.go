package entclawruntime

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tidwall/gjson"
)

type stubLoopbackClient struct {
	responses []*http.Response
	callCount int
	paths     []string
	bodies    [][]byte
}

func (c *stubLoopbackClient) Do(_ context.Context, _ *http.Request, path string, body []byte) (*http.Response, error) {
	c.callCount++
	c.paths = append(c.paths, path)
	c.bodies = append(c.bodies, append([]byte(nil), body...))

	index := c.callCount - 1
	if index >= len(c.responses) {
		return nil, io.EOF
	}

	response := *c.responses[index]
	response.Header = c.responses[index].Header.Clone()
	return &response, nil
}

func TestOrchestratorRunsIntermediateTurnsBeforeFinalStream(t *testing.T) {
	t.Parallel()

	dataDir := t.TempDir()
	store := NewSessionStore(dataDir)
	tools := NewToolRuntime(
		dataDir,
		store,
		NewSkillStore(dataDir),
		NewMCPStore(dataDir),
		nil,
		nil,
	)
	client := &stubLoopbackClient{
		responses: []*http.Response{
			testHTTPResponse(`{"choices":[{"message":{"content":"","tool_calls":[{"id":"call_1","type":"function","function":{"name":"skill_list","arguments":"{}"}}]}}]}`, "application/json"),
			testHTTPResponse(`{"choices":[{"message":{"content":"all done","tool_calls":[]}}]}`, "application/json"),
			testHTTPResponse("data: {\"choices\":[{\"delta\":{\"content\":\"all done\"}}]}\n\n", "text/event-stream"),
		},
	}
	orchestrator := Orchestrator{
		client:   client,
		tools:    tools,
		sessions: store,
	}

	source := testSourceRequest()
	task := &TaskRequest{
		SessionID: "session-1",
		Channel:   ChannelChat,
		Format:    FormatChatCompletions,
		Model:     "gpt-5.4",
		RawBody:   []byte(`{"model":"gpt-5.4","stream":true,"messages":[{"role":"user","content":"hello"}]}`),
		Path:      "/v1/entclaw/chat/completions",
	}

	result, err := orchestrator.Run(context.Background(), source, task)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Response == nil {
		t.Fatalf("RunResult.Response is nil")
	}
	if result.Session == nil {
		t.Fatalf("RunResult.Session is nil")
	}
	if client.callCount != 3 {
		t.Fatalf("callCount = %d, want 3", client.callCount)
	}
	for _, path := range client.paths {
		if path != "/v1/chat/completions" {
			t.Fatalf("loopback path = %q, want /v1/chat/completions", path)
		}
	}
	if bytes.Contains(client.bodies[0], []byte(`"stream":true`)) {
		t.Fatalf("first loopback body should be non-stream: %s", client.bodies[0])
	}
	if !bytes.Contains(client.bodies[1], []byte(`"tool_call_id":"call_1"`)) {
		t.Fatalf("second loopback body missing appended tool result: %s", client.bodies[1])
	}
	if !bytes.Contains(client.bodies[2], []byte(`"stream":true`)) {
		t.Fatalf("final loopback body should be stream=true: %s", client.bodies[2])
	}

	streamBody, err := io.ReadAll(result.Response.Body)
	if err != nil {
		t.Fatalf("ReadAll(stream): %v", err)
	}
	if err := result.Response.Body.Close(); err != nil {
		t.Fatalf("Close(stream): %v", err)
	}
	if !strings.Contains(string(streamBody), "all done") {
		t.Fatalf("stream body = %q, want final content", streamBody)
	}

	if len(result.Session.ToolHistory) != 1 {
		t.Fatalf("len(result.Session.ToolHistory) = %d, want 1", len(result.Session.ToolHistory))
	}
	if result.Session.ToolHistory[0].Call.Name != "skill_list" {
		t.Fatalf("tool name = %q, want skill_list", result.Session.ToolHistory[0].Call.Name)
	}

	loaded, err := store.LoadOrCreate(context.Background(), "session-1", SessionSeed{})
	if err != nil {
		t.Fatalf("LoadOrCreate(saved): %v", err)
	}
	if len(loaded.ToolHistory) != 1 {
		t.Fatalf("len(saved.ToolHistory) = %d, want 1", len(loaded.ToolHistory))
	}
}

func TestOrchestratorFallsBackWhenSessionIDIsEmpty(t *testing.T) {
	t.Parallel()

	dataDir := t.TempDir()
	store := NewSessionStore(dataDir)
	clientOne := &stubLoopbackClient{
		responses: []*http.Response{
			testHTTPResponse(`{"choices":[{"message":{"content":"done","tool_calls":[]}}]}`, "application/json"),
			testHTTPResponse("data: done\n\n", "text/event-stream"),
		},
	}
	orchestrator := NewOrchestrator(
		clientOne,
		NewToolRuntime(dataDir, store, NewSkillStore(dataDir), NewMCPStore(dataDir), nil, nil),
		store,
	)

	first, err := orchestrator.Run(context.Background(), testSourceRequest(), &TaskRequest{
		Channel: ChannelChat,
		Format:  FormatChatCompletions,
		Model:   "gpt-5.4",
		RawBody: []byte(`{"model":"gpt-5.4","messages":[]}`),
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if first.Session == nil {
		t.Fatal("RunResult.Session is nil")
	}
	if first.Session.SessionID == "" {
		t.Fatal("first.Session.SessionID is empty")
	}

	clientTwo := &stubLoopbackClient{
		responses: []*http.Response{
			testHTTPResponse(`{"choices":[{"message":{"content":"done","tool_calls":[]}}]}`, "application/json"),
			testHTTPResponse("data: done\n\n", "text/event-stream"),
		},
	}
	orchestrator.client = clientTwo
	second, err := orchestrator.Run(context.Background(), testSourceRequest(), &TaskRequest{
		Channel: ChannelChat,
		Format:  FormatChatCompletions,
		Model:   "gpt-5.4",
		RawBody: []byte(`{"model":"gpt-5.4","messages":[]}`),
	})
	if err != nil {
		t.Fatalf("second Run: %v", err)
	}
	if second.Session == nil {
		t.Fatal("second.Session is nil")
	}
	if second.Session.SessionID == "" {
		t.Fatal("second.Session.SessionID is empty")
	}
	if second.Session.SessionID == first.Session.SessionID {
		t.Fatalf("anonymous session IDs should differ, both were %q", first.Session.SessionID)
	}
}

func TestOrchestratorReturnsErrorWhenFinalStreamFails(t *testing.T) {
	t.Parallel()

	dataDir := t.TempDir()
	store := NewSessionStore(dataDir)
	client := &stubLoopbackClient{
		responses: []*http.Response{
			testHTTPResponse(`{"choices":[{"message":{"content":"done","tool_calls":[]}}]}`, "application/json"),
			{
				StatusCode: http.StatusBadGateway,
				Status:     "502 Bad Gateway",
				Header: http.Header{
					"Content-Type": []string{"application/json"},
				},
				Body: io.NopCloser(strings.NewReader(`{"error":"upstream failed"}`)),
			},
		},
	}
	orchestrator := NewOrchestrator(
		client,
		NewToolRuntime(dataDir, store, NewSkillStore(dataDir), NewMCPStore(dataDir), nil, nil),
		store,
	)

	_, err := orchestrator.Run(context.Background(), testSourceRequest(), &TaskRequest{
		SessionID: "session-1",
		Channel:   ChannelChat,
		Format:    FormatChatCompletions,
		Model:     "gpt-5.4",
		RawBody:   []byte(`{"model":"gpt-5.4","messages":[]}`),
	})
	if err == nil {
		t.Fatal("Run error = nil, want final stream failure")
	}
}

func TestOrchestratorUsesStreamingProbeForResponsesToolCalls(t *testing.T) {
	t.Parallel()

	dataDir := t.TempDir()
	store := NewSessionStore(dataDir)
	tools := NewToolRuntime(
		dataDir,
		store,
		NewSkillStore(dataDir),
		NewMCPStore(dataDir),
		nil,
		nil,
	)
	client := &stubLoopbackClient{
		responses: []*http.Response{
			testHTTPResponse(strings.Join([]string{
				"event: response.output_item.done",
				`data: {"type":"response.output_item.done","item":{"type":"message","status":"completed","content":[{"type":"output_text","text":"checking skills"}]}}`,
				"",
				"event: response.output_item.done",
				`data: {"type":"response.output_item.done","item":{"type":"function_call","status":"completed","call_id":"call_1","name":"skill_list","arguments":"{}"}}`,
				"",
			}, "\n"), "text/event-stream"),
			testHTTPResponse(strings.Join([]string{
				"event: response.output_item.done",
				`data: {"type":"response.output_item.done","item":{"type":"message","status":"completed","content":[{"type":"output_text","text":"done"}]}}`,
				"",
				"event: response.output_text.done",
				`data: {"type":"response.output_text.done","text":"done"}`,
				"",
			}, "\n"), "text/event-stream"),
		},
	}
	orchestrator := Orchestrator{
		client:   client,
		tools:    tools,
		sessions: store,
	}

	result, err := orchestrator.Run(context.Background(), testSourceRequest(), &TaskRequest{
		SessionID: "session-1",
		Channel:   ChannelCodex,
		Format:    FormatResponses,
		Model:     "gpt-5.4",
		RawBody:   []byte(`{"model":"gpt-5.4","input":"hello"}`),
		Path:      "/v1/entclaw/responses",
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result == nil || result.Response == nil {
		t.Fatal("expected final response")
	}
	if client.callCount != 2 {
		t.Fatalf("callCount = %d, want 2", client.callCount)
	}
	if !bytes.Contains(client.bodies[0], []byte(`"stream":true`)) {
		t.Fatalf("first loopback body should be stream=true: %s", client.bodies[0])
	}
	if !bytes.Contains(client.bodies[1], []byte(`"stream":true`)) {
		t.Fatalf("second loopback body should be stream=true: %s", client.bodies[1])
	}
	if !bytes.Contains(client.bodies[1], []byte(`"function_call_output"`)) {
		t.Fatalf("second loopback body missing tool result: %s", client.bodies[1])
	}
	streamBody, err := io.ReadAll(result.Response.Body)
	if err != nil {
		t.Fatalf("ReadAll(stream): %v", err)
	}
	if err := result.Response.Body.Close(); err != nil {
		t.Fatalf("Close(stream): %v", err)
	}
	if !strings.Contains(string(streamBody), "done") {
		t.Fatalf("stream body = %q, want final content", streamBody)
	}
}

func TestOrchestratorBuildsResponsesLoopbackBodyFromEntclawTask(t *testing.T) {
	t.Parallel()

	dataDir := t.TempDir()
	store := NewSessionStore(dataDir)
	tools := NewToolRuntime(
		dataDir,
		store,
		NewSkillStore(dataDir),
		NewMCPStore(dataDir),
		nil,
		nil,
	)
	client := &stubLoopbackClient{
		responses: []*http.Response{
			testHTTPResponse(strings.Join([]string{
				"event: response.output_item.done",
				`data: {"type":"response.output_item.done","item":{"type":"message","status":"completed","content":[{"type":"output_text","text":"done"}]}}`,
				"",
			}, "\n"), "text/event-stream"),
		},
	}
	orchestrator := Orchestrator{
		client:   client,
		tools:    tools,
		sessions: store,
	}

	_, err := orchestrator.Run(context.Background(), testSourceRequest(), &TaskRequest{
		SessionID: "session-1",
		Channel:   ChannelCodex,
		Format:    FormatResponses,
		Model:     "gpt-5.4",
		InputRaw:  json.RawMessage(`"read demo skill"`),
		RawBody:   []byte(`{"model":"gpt-5.4","input":"read demo skill","tools":[{"type":"function","name":"client_tool"}],"tool_choice":"required"}`),
		Path:      "/v1/entclaw/responses",
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if client.callCount != 1 {
		t.Fatalf("callCount = %d, want 1", client.callCount)
	}
	if bytes.Contains(client.bodies[0], []byte(`client_tool`)) {
		t.Fatalf("loopback body should not include client tool definitions: %s", client.bodies[0])
	}
	if !bytes.Contains(client.bodies[0], []byte(`"name":"skill_list"`)) {
		t.Fatalf("loopback body missing built-in tools: %s", client.bodies[0])
	}
	root := gjson.ParseBytes(client.bodies[0])
	if root.Get("instructions").String() == "" {
		t.Fatalf("loopback body missing default instructions: %s", client.bodies[0])
	}
	if root.Get("tool_choice").String() != "required" {
		t.Fatalf("tool_choice = %s, want required", root.Get("tool_choice").Raw)
	}
	if root.Get("input.0.type").String() != "message" {
		t.Fatalf("input[0] type = %s", root.Get("input.0").Raw)
	}
	if root.Get("input.0.role").String() != "user" {
		t.Fatalf("input[0] role = %s", root.Get("input.0").Raw)
	}
	if root.Get("input.0.content").String() != "read demo skill" {
		t.Fatalf("input[0] content = %s", root.Get("input.0").Raw)
	}
}

func TestOrchestratorEmitsResponsesProgressEventsForToolRound(t *testing.T) {
	t.Parallel()

	dataDir := t.TempDir()
	store := NewSessionStore(dataDir)
	tools := NewToolRuntime(
		dataDir,
		store,
		NewSkillStore(dataDir),
		NewMCPStore(dataDir),
		nil,
		nil,
	)

	client := &stubLoopbackClient{
		responses: []*http.Response{
			testHTTPResponse(`{"output":[{"type":"message","content":[{"type":"output_text","text":"checking skills"}]},{"type":"function_call","call_id":"call_1","name":"skill_list","arguments":"{}"}]}`, "application/json"),
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

	if len(seen) != 6 {
		t.Fatalf("len(seen) = %d, want 6 (%+v)", len(seen), seen)
	}
	if seen[0].Type != OrchestrationAssistantMessage || seen[0].Text != "checking skills" {
		t.Fatalf("seen[0] = %+v, want assistant text event", seen[0])
	}
	if seen[1].Type != OrchestrationAssistantToolCall || seen[1].CallID != "call_1" || seen[1].Name != "skill_list" {
		t.Fatalf("seen[1] = %+v, want assistant tool call event", seen[1])
	}
	if seen[2].Type != OrchestrationToolStarted || seen[2].CallID != "call_1" {
		t.Fatalf("seen[2] = %+v, want tool started event", seen[2])
	}
	if seen[3].Type != OrchestrationToolCompleted || seen[3].CallID != "call_1" || seen[3].IsError {
		t.Fatalf("seen[3] = %+v, want successful tool completed event", seen[3])
	}
	if seen[4].Type != OrchestrationAssistantMessage || seen[4].Text != "final" {
		t.Fatalf("seen[4] = %+v, want final assistant text event", seen[4])
	}
	if seen[5].Type != OrchestrationCompleted {
		t.Fatalf("seen[5] = %+v, want completion event", seen[5])
	}
}

func TestOrchestratorAutoDiscoversSkillFromInjectedCatalog(t *testing.T) {
	t.Parallel()

	dataDir := t.TempDir()
	store := NewSessionStore(dataDir)
	skillStore := NewSkillStore(dataDir)
	skillDir := filepath.Join(dataDir, "entclaw", "skills", "github-search")
	if err := os.MkdirAll(filepath.Join(skillDir, "scripts"), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(`---
name: github-search
description: Search GitHub repositories and similar projects.
---

Run scripts/search.sh when repository search is required.
`), 0o644); err != nil {
		t.Fatalf("WriteFile(SKILL.md): %v", err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "scripts", "search.sh"), []byte("#!/bin/sh\necho ok\n"), 0o755); err != nil {
		t.Fatalf("WriteFile(search.sh): %v", err)
	}

	tools := NewToolRuntime(
		dataDir,
		store,
		skillStore,
		NewMCPStore(dataDir),
		nil,
		func(_ context.Context, request CommandRequest) (CommandResult, error) {
			return CommandResult{
				Stdout:   "search ok\n",
				Stderr:   "",
				ExitCode: 0,
			}, nil
		},
	)
	client := &stubLoopbackClient{
		responses: []*http.Response{
			testHTTPResponse(strings.Join([]string{
				"event: response.output_item.done",
				`data: {"type":"response.output_item.done","item":{"type":"function_call","status":"completed","call_id":"call_1","name":"skill_read","arguments":"{\"name\":\"github-search\"}"}}`,
				"",
			}, "\n"), "text/event-stream"),
			testHTTPResponse(strings.Join([]string{
				"event: response.output_item.done",
				`data: {"type":"response.output_item.done","item":{"type":"function_call","status":"completed","call_id":"call_2","name":"skill_run","arguments":"{\"name\":\"github-search\",\"script\":\"search.sh\"}"}}`,
				"",
			}, "\n"), "text/event-stream"),
			testHTTPResponse(strings.Join([]string{
				"event: response.output_text.done",
				`data: {"type":"response.output_text.done","text":"found similar repositories"}`,
				"",
			}, "\n"), "text/event-stream"),
		},
	}
	orchestrator := Orchestrator{
		client:   client,
		tools:    tools,
		sessions: store,
	}

	result, err := orchestrator.Run(context.Background(), testSourceRequest(), &TaskRequest{
		SessionID: "session-1",
		Channel:   ChannelCodex,
		Format:    FormatResponses,
		Model:     "gpt-5.4",
		RawBody:   []byte(`{"model":"gpt-5.4","input":"search github for openclaw alternatives"}`),
		Path:      "/v1/entclaw/responses",
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result == nil || result.Response == nil || result.Session == nil {
		t.Fatal("expected response and session")
	}
	if client.callCount != 3 {
		t.Fatalf("callCount = %d, want 3", client.callCount)
	}

	firstBody := gjson.ParseBytes(client.bodies[0])
	instructions := firstBody.Get("instructions").String()
	if !strings.Contains(instructions, "<available_skills>") {
		t.Fatalf("instructions = %q, want available_skills", instructions)
	}
	if !strings.Contains(instructions, "github-search") {
		t.Fatalf("instructions = %q, want github-search", instructions)
	}
	if !strings.Contains(instructions, "Search GitHub repositories and similar projects.") {
		t.Fatalf("instructions = %q, want skill description", instructions)
	}

	secondBody := gjson.ParseBytes(client.bodies[1])
	if secondBody.Get("input.2.type").String() != "function_call_output" {
		t.Fatalf("second body input = %s, want function_call_output replay", secondBody.Get("input").Raw)
	}
	if !strings.Contains(secondBody.Get("input.2.output").String(), "github-search") {
		t.Fatalf("second body output = %s, want skill_read result", secondBody.Get("input.2").Raw)
	}

	streamBody, err := io.ReadAll(result.Response.Body)
	if err != nil {
		t.Fatalf("ReadAll(stream): %v", err)
	}
	if err := result.Response.Body.Close(); err != nil {
		t.Fatalf("Close(stream): %v", err)
	}
	if !strings.Contains(string(streamBody), "found similar repositories") {
		t.Fatalf("stream body = %q, want final content", streamBody)
	}
	if len(result.Session.ToolHistory) != 2 {
		t.Fatalf("len(result.Session.ToolHistory) = %d, want 2", len(result.Session.ToolHistory))
	}
	if result.Session.ToolHistory[0].Call.Name != "skill_read" {
		t.Fatalf("first tool = %q, want skill_read", result.Session.ToolHistory[0].Call.Name)
	}
	if result.Session.ToolHistory[1].Call.Name != "skill_run" {
		t.Fatalf("second tool = %q, want skill_run", result.Session.ToolHistory[1].Call.Name)
	}
}

func TestOrchestratorEmitsFailureEventForHardToolExecutionError(t *testing.T) {
	t.Parallel()

	dataDir := t.TempDir()
	store := NewSessionStore(dataDir)
	tools := NewToolRuntime(
		dataDir,
		store,
		NewSkillStore(dataDir),
		NewMCPStore(dataDir),
		nil,
		nil,
	)
	client := &stubLoopbackClient{
		responses: []*http.Response{
			testHTTPResponse(`{"output":[{"type":"function_call","call_id":"call_1","name":"skill_read","arguments":"[]"}]}`, "application/json"),
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
		InputRaw:  json.RawMessage(`"broken tool args"`),
		RawBody:   []byte(`{"model":"gpt-5.4","input":"broken tool args"}`),
		Path:      "/v1/entclaw/responses",
	})
	if err == nil {
		t.Fatal("Run error = nil, want hard tool execution failure")
	}
	if client.callCount != 1 {
		t.Fatalf("callCount = %d, want 1", client.callCount)
	}
	if len(seen) != 3 {
		t.Fatalf("len(seen) = %d, want 3 (%+v)", len(seen), seen)
	}
	if seen[0].Type != OrchestrationAssistantToolCall || seen[0].CallID != "call_1" || seen[0].Name != "skill_read" {
		t.Fatalf("seen[0] = %+v, want assistant tool call event", seen[0])
	}
	if seen[1].Type != OrchestrationToolStarted || seen[1].CallID != "call_1" {
		t.Fatalf("seen[1] = %+v, want tool started event", seen[1])
	}
	if seen[2].Type != OrchestrationFailed || seen[2].CallID != "call_1" || !seen[2].IsError {
		t.Fatalf("seen[2] = %+v, want failed terminal event", seen[2])
	}
	if !bytes.Contains(seen[2].Output, []byte(`"error":"execute tool \"skill_read\"`)) {
		t.Fatalf("seen[2].Output = %s, want wrapped execution error", seen[2].Output)
	}
	for _, event := range seen {
		if event.Type == OrchestrationCompleted {
			t.Fatalf("unexpected completion event after hard failure: %+v", seen)
		}
	}
}

func TestOrchestratorEmitsFailureEventForResponsesProbeFailure(t *testing.T) {
	t.Parallel()

	dataDir := t.TempDir()
	store := NewSessionStore(dataDir)
	tools := NewToolRuntime(
		dataDir,
		store,
		NewSkillStore(dataDir),
		NewMCPStore(dataDir),
		nil,
		nil,
	)
	client := &stubLoopbackClient{
		responses: []*http.Response{
			testHTTPResponse(strings.Join([]string{
				"event: response.failed",
				`data: {"type":"response.failed","response":{"id":"resp_failed","status":"failed"},"error":{"message":"probe failed"}}`,
				"",
			}, "\n"), "text/event-stream"),
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

	result, err := orchestrator.Run(context.Background(), testSourceRequest(), &TaskRequest{
		SessionID: "session-1",
		Channel:   ChannelCodex,
		Format:    FormatResponses,
		Model:     "gpt-5.4",
		InputRaw:  json.RawMessage(`"probe failure"`),
		RawBody:   []byte(`{"model":"gpt-5.4","input":"probe failure"}`),
		Path:      "/v1/entclaw/responses",
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result == nil || result.Response == nil {
		t.Fatal("expected loopback response")
	}
	if len(seen) != 1 {
		t.Fatalf("len(seen) = %d, want 1 (%+v)", len(seen), seen)
	}
	if seen[0].Type != OrchestrationFailed || seen[0].CallID != "" || !seen[0].IsError {
		t.Fatalf("seen[0] = %+v, want failed terminal event", seen[0])
	}
	if !bytes.Contains(seen[0].Output, []byte(`probe failed`)) {
		t.Fatalf("seen[0].Output = %s, want probe failure details", seen[0].Output)
	}
	for _, event := range seen {
		if event.Type == OrchestrationCompleted {
			t.Fatalf("unexpected completion event after failed probe: %+v", seen)
		}
	}
}

func TestOrchestratorDoesNotExecuteToolCallsFromFailedResponsesProbe(t *testing.T) {
	t.Parallel()

	dataDir := t.TempDir()
	store := NewSessionStore(dataDir)
	ranTools := 0
	tools := NewToolRuntime(
		dataDir,
		store,
		NewSkillStore(dataDir),
		NewMCPStore(dataDir),
		nil,
		func(context.Context, CommandRequest) (CommandResult, error) {
			ranTools++
			return CommandResult{}, nil
		},
	)
	client := &stubLoopbackClient{
		responses: []*http.Response{
			testHTTPResponse(strings.Join([]string{
				"event: response.output_item.done",
				`data: {"type":"response.output_item.done","item":{"type":"function_call","status":"completed","call_id":"call_1","name":"skill_list","arguments":"{}"}}`,
				"",
				"event: response.failed",
				`data: {"type":"response.failed","response":{"id":"resp_failed","status":"failed"},"error":{"message":"probe failed after tool call"}}`,
				"",
			}, "\n"), "text/event-stream"),
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

	result, err := orchestrator.Run(context.Background(), testSourceRequest(), &TaskRequest{
		SessionID: "session-1",
		Channel:   ChannelCodex,
		Format:    FormatResponses,
		Model:     "gpt-5.4",
		InputRaw:  json.RawMessage(`"probe failure after tool call"`),
		RawBody:   []byte(`{"model":"gpt-5.4","input":"probe failure after tool call"}`),
		Path:      "/v1/entclaw/responses",
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result == nil || result.Response == nil {
		t.Fatal("expected loopback response")
	}
	if ranTools != 0 {
		t.Fatalf("ranTools = %d, want 0", ranTools)
	}
	if len(seen) != 2 {
		t.Fatalf("len(seen) = %d, want 2 (%+v)", len(seen), seen)
	}
	if seen[0].Type != OrchestrationAssistantToolCall || seen[0].CallID != "call_1" {
		t.Fatalf("seen[0] = %+v, want assistant tool call event", seen[0])
	}
	if seen[1].Type != OrchestrationFailed || seen[1].CallID != "" || !seen[1].IsError {
		t.Fatalf("seen[1] = %+v, want response-level failed event", seen[1])
	}
	if !bytes.Contains(seen[1].Output, []byte(`probe failed after tool call`)) {
		t.Fatalf("seen[1].Output = %s, want probe failure details", seen[1].Output)
	}
}

func TestOrchestratorEmitsFailureEventForFinalStreamStatusAfterProgress(t *testing.T) {
	t.Parallel()

	dataDir := t.TempDir()
	store := NewSessionStore(dataDir)
	tools := NewToolRuntime(
		dataDir,
		store,
		NewSkillStore(dataDir),
		NewMCPStore(dataDir),
		nil,
		nil,
	)
	client := &stubLoopbackClient{
		responses: []*http.Response{
			testHTTPResponse(`{"choices":[{"message":{"content":"almost there","tool_calls":[]}}]}`, "application/json"),
			{
				StatusCode: http.StatusBadGateway,
				Header: http.Header{
					"Content-Type": []string{"application/json"},
				},
				Body: io.NopCloser(strings.NewReader(`{"error":"upstream stream failed"}`)),
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
		Format:    FormatChatCompletions,
		Model:     "gpt-5.4",
		InputRaw:  json.RawMessage(`[]`),
		RawBody:   []byte(`{"model":"gpt-5.4","messages":[]}`),
		Path:      "/v1/entclaw/chat/completions",
	})
	if err == nil {
		t.Fatal("Run error = nil, want final stream failure")
	}
	if client.callCount != 2 {
		t.Fatalf("callCount = %d, want 2", client.callCount)
	}
	if len(seen) != 2 {
		t.Fatalf("len(seen) = %d, want 2 (%+v)", len(seen), seen)
	}
	if seen[0].Type != OrchestrationAssistantMessage || seen[0].Text != "almost there" {
		t.Fatalf("seen[0] = %+v, want assistant message event", seen[0])
	}
	if seen[1].Type != OrchestrationFailed || seen[1].CallID != "" || !seen[1].IsError {
		t.Fatalf("seen[1] = %+v, want terminal failed event", seen[1])
	}
	if !bytes.Contains(seen[1].Output, []byte(`final stream loopback returned`)) ||
		!bytes.Contains(seen[1].Output, []byte(`upstream stream failed`)) {
		t.Fatalf("seen[1].Output = %s, want final stream failure details", seen[1].Output)
	}
}

func testSourceRequest() *http.Request {
	req, err := http.NewRequest(http.MethodPost, "http://example.test/v1/entclaw/chat/completions", bytes.NewReader(nil))
	if err != nil {
		panic(err)
	}
	req.Header.Set("Authorization", "Bearer test-token")
	return req
}

func testHTTPResponse(body string, contentType string) *http.Response {
	return &http.Response{
		StatusCode: http.StatusOK,
		Header: http.Header{
			"Content-Type": []string{contentType},
		},
		Body: io.NopCloser(strings.NewReader(body)),
	}
}
