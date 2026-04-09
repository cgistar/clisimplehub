package entclawplugin

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	entclawruntime "clisimplehub/internal/entclaw/runtime"
	"clisimplehub/internal/plugin"
)

type stubLoopbackClient struct {
	responses []*http.Response
	callCount int
}

func (c *stubLoopbackClient) Do(_ context.Context, _ *http.Request, _ string, _ []byte) (*http.Response, error) {
	index := c.callCount
	c.callCount++
	if index >= len(c.responses) {
		return nil, io.EOF
	}

	response := *c.responses[index]
	response.Header = c.responses[index].Header.Clone()
	return &response, nil
}

func TestHandleInferenceStreamsFinalResponse(t *testing.T) {
	t.Parallel()

	dataDir := t.TempDir()
	sessions := entclawruntime.NewSessionStore(dataDir)
	client := &stubLoopbackClient{
		responses: []*http.Response{
			{
				StatusCode: http.StatusOK,
				Header: http.Header{
					"Content-Type": []string{"application/json"},
				},
				Body: io.NopCloser(bytes.NewBufferString(`{"content":[{"type":"text","text":"done"}]}`)),
			},
			{
				StatusCode: http.StatusCreated,
				Header: http.Header{
					"Content-Type":  []string{"text/event-stream"},
					"Cache-Control": []string{"no-cache"},
				},
				Body: io.NopCloser(bytes.NewBufferString("event: message\ndata: done\n\n")),
			},
		},
	}
	tools := entclawruntime.NewToolRuntime(
		dataDir,
		sessions,
		entclawruntime.NewSkillStore(dataDir),
		entclawruntime.NewMCPStore(dataDir),
		nil,
		nil,
	)
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

	req := httptest.NewRequest(http.MethodPost, "/v1/entclaw/messages", bytes.NewBufferString(`{"model":"claude-sonnet-4","messages":[]}`))
	req.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()

	plugin.handleInference(recorder, req)

	response := recorder.Result()
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d, want %d", response.StatusCode, http.StatusCreated)
	}
	if got := response.Header.Get("Content-Type"); got != "text/event-stream" {
		t.Fatalf("Content-Type = %q, want text/event-stream", got)
	}
	if got := response.Header.Get("Cache-Control"); got != "no-cache" {
		t.Fatalf("Cache-Control = %q, want no-cache", got)
	}
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("ReadAll(response.Body): %v", err)
	}
	if err := response.Body.Close(); err != nil {
		t.Fatalf("Close(response.Body): %v", err)
	}
	if string(body) != "event: message\ndata: done\n\n" {
		t.Fatalf("body = %q, want final stream body", body)
	}
}

func TestHandleInferenceResponsesStreamsLocalProgressEvents(t *testing.T) {
	t.Parallel()

	dataDir := t.TempDir()
	sessions := entclawruntime.NewSessionStore(dataDir)
	tools := entclawruntime.NewToolRuntime(
		dataDir,
		sessions,
		entclawruntime.NewSkillStore(dataDir),
		entclawruntime.NewMCPStore(dataDir),
		nil,
		nil,
	)
	client := &stubLoopbackClient{
		responses: []*http.Response{
			{
				StatusCode: http.StatusOK,
				Header: http.Header{
					"Content-Type": []string{"application/json"},
				},
				Body: io.NopCloser(bytes.NewBufferString(`{"output":[{"type":"function_call","call_id":"call_1","name":"skill_list","arguments":"{}"}]}`)),
			},
			{
				StatusCode: http.StatusOK,
				Header: http.Header{
					"Content-Type":  []string{"text/event-stream"},
					"Cache-Control": []string{"no-cache"},
				},
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

	response := recorder.Result()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", response.StatusCode, http.StatusOK)
	}
	if got := response.Header.Get("Content-Type"); got != "text/event-stream" {
		t.Fatalf("Content-Type = %q, want text/event-stream", got)
	}
	if got := response.Header.Get("Cache-Control"); got != "no-cache" {
		t.Fatalf("Cache-Control = %q, want no-cache", got)
	}
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("ReadAll(response.Body): %v", err)
	}
	if err := response.Body.Close(); err != nil {
		t.Fatalf("Close(response.Body): %v", err)
	}
	stream := string(body)
	if !strings.Contains(stream, `"type":"function_call"`) {
		t.Fatalf("body = %s, want function_call progress item", stream)
	}
	if !strings.Contains(stream, `"type":"function_call_output"`) {
		t.Fatalf("body = %s, want function_call_output progress item", stream)
	}
	if !strings.Contains(stream, `"type":"response.completed"`) {
		t.Fatalf("body = %s, want response.completed event", stream)
	}
}

func TestHandleInferenceResponsesPreservesUpstreamHeadersAndStatus(t *testing.T) {
	t.Parallel()

	dataDir := t.TempDir()
	sessions := entclawruntime.NewSessionStore(dataDir)
	tools := entclawruntime.NewToolRuntime(
		dataDir,
		sessions,
		entclawruntime.NewSkillStore(dataDir),
		entclawruntime.NewMCPStore(dataDir),
		nil,
		nil,
	)
	client := &stubLoopbackClient{
		responses: []*http.Response{
			{
				StatusCode: http.StatusAccepted,
				Header: http.Header{
					"Content-Type":  []string{"text/event-stream"},
					"Cache-Control": []string{"private, max-age=1"},
					"X-Request-Id":  []string{"req_123"},
				},
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

	req := httptest.NewRequest(http.MethodPost, "/v1/entclaw/responses", bytes.NewBufferString(`{"model":"gpt-5.4","input":"done"}`))
	req.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()

	plugin.handleInference(recorder, req)

	response := recorder.Result()
	if response.StatusCode != http.StatusAccepted {
		t.Fatalf("status = %d, want %d", response.StatusCode, http.StatusAccepted)
	}
	if got := response.Header.Get("X-Request-Id"); got != "req_123" {
		t.Fatalf("X-Request-Id = %q, want req_123", got)
	}
	if got := response.Header.Get("Cache-Control"); got != "private, max-age=1" {
		t.Fatalf("Cache-Control = %q, want private, max-age=1", got)
	}
}

func TestHandleInferenceResponsesRejectsUnexpectedJSONPayload(t *testing.T) {
	t.Parallel()

	dataDir := t.TempDir()
	sessions := entclawruntime.NewSessionStore(dataDir)
	tools := entclawruntime.NewToolRuntime(
		dataDir,
		sessions,
		entclawruntime.NewSkillStore(dataDir),
		entclawruntime.NewMCPStore(dataDir),
		nil,
		nil,
	)
	client := &stubLoopbackClient{
		responses: []*http.Response{
			{
				StatusCode: http.StatusOK,
				Header: http.Header{
					"Content-Type": []string{"application/json"},
				},
				Body: io.NopCloser(bytes.NewBufferString(`{"error":"schema drift"}`)),
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

	req := httptest.NewRequest(http.MethodPost, "/v1/entclaw/responses", bytes.NewBufferString(`{"model":"gpt-5.4","input":"done"}`))
	req.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()

	plugin.handleInference(recorder, req)

	response := recorder.Result()
	if response.StatusCode != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", response.StatusCode, http.StatusInternalServerError)
	}
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("ReadAll(response.Body): %v", err)
	}
	if err := response.Body.Close(); err != nil {
		t.Fatalf("Close(response.Body): %v", err)
	}
	if !bytes.Contains(body, []byte(`unexpected responses payload`)) {
		t.Fatalf("body = %s, want unexpected responses payload error", body)
	}
}

func TestHandleInferenceResponsesStreamsLocalFailureAsSSE(t *testing.T) {
	t.Parallel()

	dataDir := t.TempDir()
	sessions := entclawruntime.NewSessionStore(dataDir)
	tools := entclawruntime.NewToolRuntime(
		dataDir,
		sessions,
		entclawruntime.NewSkillStore(dataDir),
		entclawruntime.NewMCPStore(dataDir),
		nil,
		nil,
	)
	client := &stubLoopbackClient{
		responses: []*http.Response{
			{
				StatusCode: http.StatusOK,
				Header: http.Header{
					"Content-Type": []string{"application/json"},
				},
				Body: io.NopCloser(bytes.NewBufferString(`{"output":[{"type":"function_call","call_id":"call_1","name":"skill_read","arguments":"[]"}]}`)),
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

	req := httptest.NewRequest(http.MethodPost, "/v1/entclaw/responses", bytes.NewBufferString(`{"model":"gpt-5.4","input":"broken"}`))
	req.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()

	plugin.handleInference(recorder, req)

	response := recorder.Result()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", response.StatusCode, http.StatusOK)
	}
	if got := response.Header.Get("Content-Type"); got != "text/event-stream" {
		t.Fatalf("Content-Type = %q, want text/event-stream", got)
	}
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("ReadAll(response.Body): %v", err)
	}
	if err := response.Body.Close(); err != nil {
		t.Fatalf("Close(response.Body): %v", err)
	}
	if !bytes.Contains(body, []byte(`"type":"response.failed"`)) {
		t.Fatalf("body = %s, want response.failed event", body)
	}
	if !bytes.Contains(body, []byte(`"type":"function_call_output"`)) {
		t.Fatalf("body = %s, want function_call_output failure event", body)
	}
	if !bytes.Contains(body, []byte(`execute tool \"skill_read\"`)) {
		t.Fatalf("body = %s, want tool failure details", body)
	}
}

func TestHandleInferenceChatCompletionsStreamsProgressText(t *testing.T) {
	t.Parallel()

	dataDir := t.TempDir()
	sessions := entclawruntime.NewSessionStore(dataDir)
	skills := entclawruntime.NewSkillStore(dataDir)
	if err := skills.Write(context.Background(), "demo", "# Demo\n"); err != nil {
		t.Fatalf("Write(demo): %v", err)
	}
	tools := entclawruntime.NewToolRuntime(
		dataDir,
		sessions,
		skills,
		entclawruntime.NewMCPStore(dataDir),
		nil,
		nil,
	)
	client := &stubLoopbackClient{
		responses: []*http.Response{
			{
				StatusCode: http.StatusOK,
				Header: http.Header{
					"Content-Type": []string{"application/json"},
				},
				Body: io.NopCloser(bytes.NewBufferString(`{"choices":[{"message":{"content":"","tool_calls":[{"id":"call_1","type":"function","function":{"name":"skill_read","arguments":"{\"name\":\"demo\"}"}}]}}]}`)),
			},
			{
				StatusCode: http.StatusOK,
				Header: http.Header{
					"Content-Type": []string{"application/json"},
				},
				Body: io.NopCloser(bytes.NewBufferString(`{"choices":[{"message":{"content":"final","tool_calls":[]}}]}`)),
			},
			{
				StatusCode: http.StatusOK,
				Header: http.Header{
					"Content-Type": []string{"text/event-stream"},
				},
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
	req.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()

	plugin.handleInference(recorder, req)

	body := recorder.Body.String()
	if !strings.Contains(body, "Reading skill instructions") {
		t.Fatalf("body = %s, want simplified progress text", body)
	}
}

func TestHandleInferenceMessagesStreamsProgressText(t *testing.T) {
	t.Parallel()

	dataDir := t.TempDir()
	sessions := entclawruntime.NewSessionStore(dataDir)
	skills := entclawruntime.NewSkillStore(dataDir)
	if err := skills.Write(context.Background(), "demo", "# Demo\n"); err != nil {
		t.Fatalf("Write(demo): %v", err)
	}
	scriptDir, err := skills.ScriptDir("demo")
	if err != nil {
		t.Fatalf("ScriptDir(demo): %v", err)
	}
	if err := os.MkdirAll(scriptDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(scriptDir): %v", err)
	}
	if err := os.WriteFile(filepath.Join(scriptDir, "run.sh"), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatalf("WriteFile(run.sh): %v", err)
	}
	tools := entclawruntime.NewToolRuntime(
		dataDir,
		sessions,
		skills,
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
				Header: http.Header{
					"Content-Type": []string{"application/json"},
				},
				Body: io.NopCloser(bytes.NewBufferString(`{"content":[{"type":"tool_use","id":"call_1","name":"skill_run","input":{"name":"demo","script":"run.sh"}}]}`)),
			},
			{
				StatusCode: http.StatusOK,
				Header: http.Header{
					"Content-Type": []string{"application/json"},
				},
				Body: io.NopCloser(bytes.NewBufferString(`{"content":[{"type":"text","text":"final"}]}`)),
			},
			{
				StatusCode: http.StatusOK,
				Header: http.Header{
					"Content-Type": []string{"text/event-stream"},
				},
				Body: io.NopCloser(bytes.NewBufferString("event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"delta\":{\"type\":\"text_delta\",\"text\":\"final\"}}\n\n")),
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

	req := httptest.NewRequest(http.MethodPost, "/v1/entclaw/messages", bytes.NewBufferString(`{"model":"claude-sonnet-4","messages":[{"role":"user","content":"run demo"}]}`))
	req.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()

	plugin.handleInference(recorder, req)

	body := recorder.Body.String()
	if !strings.Contains(body, "Running skill script") {
		t.Fatalf("body = %s, want simplified progress text", body)
	}
}

func TestHandleSkillsRejectsUnsupportedMethod(t *testing.T) {
	t.Parallel()

	plugin := &EntclawPlugin{}
	req := httptest.NewRequest(http.MethodPatch, "/v1/entclaw/skills", nil)
	recorder := httptest.NewRecorder()

	plugin.handleSkills(recorder, req)

	response := recorder.Result()
	if response.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want %d", response.StatusCode, http.StatusMethodNotAllowed)
	}
	if got := response.Header.Get("Content-Type"); got != "application/json" {
		t.Fatalf("Content-Type = %q, want application/json", got)
	}
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("ReadAll(response.Body): %v", err)
	}
	if err := response.Body.Close(); err != nil {
		t.Fatalf("Close(response.Body): %v", err)
	}
	if !bytes.Contains(body, []byte(`"error":"method not allowed"`)) {
		t.Fatalf("body = %q, want method not allowed error", body)
	}
}

func TestHandleInferenceRejectsUnsupportedMethod(t *testing.T) {
	t.Parallel()

	plugin := &EntclawPlugin{}
	req := httptest.NewRequest(http.MethodGet, "/v1/entclaw/chat/completions", nil)
	recorder := httptest.NewRecorder()

	plugin.handleInference(recorder, req)

	response := recorder.Result()
	if response.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want %d", response.StatusCode, http.StatusMethodNotAllowed)
	}
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("ReadAll(response.Body): %v", err)
	}
	if err := response.Body.Close(); err != nil {
		t.Fatalf("Close(response.Body): %v", err)
	}
	if !bytes.Contains(body, []byte(`"type":"invalid_request_error"`)) {
		t.Fatalf("body = %q, want OpenAI-style error", body)
	}
}

func TestHandleInferenceMessagesErrorsUseAnthropicShape(t *testing.T) {
	t.Parallel()

	plugin := &EntclawPlugin{}
	req := httptest.NewRequest(http.MethodPost, "/v1/entclaw/messages", bytes.NewBufferString(`{`))
	recorder := httptest.NewRecorder()

	plugin.handleInference(recorder, req)

	response := recorder.Result()
	if response.StatusCode != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", response.StatusCode, http.StatusInternalServerError)
	}
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("ReadAll(response.Body): %v", err)
	}
	if err := response.Body.Close(); err != nil {
		t.Fatalf("Close(response.Body): %v", err)
	}
	if !bytes.Contains(body, []byte(`"type":"error"`)) {
		t.Fatalf("body = %q, want Anthropic error envelope", body)
	}
	if !bytes.Contains(body, []byte(`"invalid_request_error"`)) {
		t.Fatalf("body = %q, want invalid_request_error type", body)
	}
}

func TestHandleSkillsPutUsesPathName(t *testing.T) {
	t.Parallel()

	dataDir := t.TempDir()
	plugin := &EntclawPlugin{
		skills: entclawruntime.NewSkillStore(dataDir),
	}
	req := httptest.NewRequest(http.MethodPut, "/v1/entclaw/skills/path-name", bytes.NewBufferString(`{"name":"body-name","content":"# Demo\n"}`))
	recorder := httptest.NewRecorder()

	plugin.handleSkills(recorder, req)

	response := recorder.Result()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", response.StatusCode, http.StatusOK)
	}
	content, err := plugin.skills.Read(context.Background(), "path-name")
	if err != nil {
		t.Fatalf("Read(path-name): %v", err)
	}
	if content != "# Demo\n" {
		t.Fatalf("content = %q, want # Demo\\n", content)
	}
	if _, err := plugin.skills.Read(context.Background(), "body-name"); err == nil {
		t.Fatal("body-name skill unexpectedly written")
	}
}

func TestHandleSkillsDeleteRequiresPathName(t *testing.T) {
	t.Parallel()

	plugin := &EntclawPlugin{}
	req := httptest.NewRequest(http.MethodDelete, "/v1/entclaw/skills", nil)
	recorder := httptest.NewRecorder()

	plugin.handleSkills(recorder, req)

	response := recorder.Result()
	if response.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", response.StatusCode, http.StatusBadRequest)
	}
}

func TestHandleInferencePreservesUpstreamStatusCode(t *testing.T) {
	t.Parallel()

	plugin := &EntclawPlugin{
		orchestrator: entclawruntime.NewOrchestrator(&stubLoopbackClient{
			responses: []*http.Response{
				{
					StatusCode: http.StatusTooManyRequests,
					Header: http.Header{
						"Content-Type": []string{"application/json"},
					},
					Body: io.NopCloser(bytes.NewBufferString(`{"error":"rate limited"}`)),
				},
			},
		}, &entclawruntime.ToolRuntime{}, entclawruntime.NewSessionStore(t.TempDir())),
	}
	req := httptest.NewRequest(http.MethodPost, "/v1/entclaw/chat/completions", bytes.NewBufferString(`{"model":"gpt-5.4","messages":[]}`))
	recorder := httptest.NewRecorder()

	plugin.handleInference(recorder, req)

	response := recorder.Result()
	if response.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want %d", response.StatusCode, http.StatusTooManyRequests)
	}
}

func TestHandleSkillsRejectsWriteWhenStoreUninitialized(t *testing.T) {
	t.Parallel()

	plugin := &EntclawPlugin{}
	req := httptest.NewRequest(http.MethodPost, "/v1/entclaw/skills", bytes.NewBufferString(`{"name":"demo","content":"# Demo\n"}`))
	recorder := httptest.NewRecorder()

	plugin.handleSkills(recorder, req)

	response := recorder.Result()
	if response.StatusCode != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", response.StatusCode, http.StatusInternalServerError)
	}
}

func TestPluginInitRejectsEmptyConfigPath(t *testing.T) {
	t.Parallel()

	pluginInstance := &EntclawPlugin{}
	if err := pluginInstance.Init(plugin.InitConfig{}); err == nil {
		t.Fatal("Init error = nil, want empty ConfigPath rejection")
	}
}
