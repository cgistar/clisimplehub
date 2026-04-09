package entclawruntime

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
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
