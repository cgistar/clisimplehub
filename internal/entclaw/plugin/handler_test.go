package entclawplugin

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
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
