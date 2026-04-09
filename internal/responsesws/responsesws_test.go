package responsesws

import (
	"net/http"
	"testing"

	"github.com/tidwall/gjson"
)

func TestNormalizeRequestCreate(t *testing.T) {
	raw := []byte(`{"type":"response.create","model":"test-model","stream":false,"input":[{"type":"message","id":"msg-1"}]}`)

	normalized, err := NormalizeRequest(raw, nil, nil, true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gjson.GetBytes(normalized, "type").Exists() {
		t.Fatalf("normalized create request must not include type field")
	}
	if !gjson.GetBytes(normalized, "stream").Bool() {
		t.Fatalf("normalized create request must force stream=true")
	}
	if got := gjson.GetBytes(normalized, "model").String(); got != "test-model" {
		t.Fatalf("model = %q, want test-model", got)
	}
}

func TestNormalizeRequestWithPreviousResponseIDIncremental(t *testing.T) {
	lastRequest := []byte(`{"model":"test-model","stream":true,"instructions":"be helpful","input":[{"type":"message","id":"msg-1"}]}`)
	lastResponseOutput := []byte(`[
		{"type":"function_call","id":"fc-1","call_id":"call-1"},
		{"type":"message","id":"assistant-1"}
	]`)
	raw := []byte(`{"type":"response.create","previous_response_id":"resp-1","input":[{"type":"function_call_output","call_id":"call-1","id":"tool-out-1"}]}`)

	normalized, err := NormalizeRequest(raw, lastRequest, lastResponseOutput, true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := gjson.GetBytes(normalized, "previous_response_id").String(); got != "resp-1" {
		t.Fatalf("previous_response_id = %q, want resp-1", got)
	}
	input := gjson.GetBytes(normalized, "input").Array()
	if len(input) != 1 {
		t.Fatalf("incremental input len = %d, want 1", len(input))
	}
	if got := input[0].Get("id").String(); got != "tool-out-1" {
		t.Fatalf("input[0].id = %q, want tool-out-1", got)
	}
	if got := gjson.GetBytes(normalized, "instructions").String(); got != "be helpful" {
		t.Fatalf("instructions = %q, want be helpful", got)
	}
}

func TestNormalizeRequestAppend(t *testing.T) {
	lastRequest := []byte(`{"model":"test-model","stream":true,"input":[{"type":"message","id":"msg-1"}]}`)
	lastResponseOutput := []byte(`[
		{"type":"message","id":"assistant-1"},
		{"type":"function_call_output","id":"tool-out-1"}
	]`)
	raw := []byte(`{"type":"response.append","input":[{"type":"message","id":"msg-2"},{"type":"message","id":"msg-3"}]}`)

	normalized, err := NormalizeRequest(raw, lastRequest, lastResponseOutput, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	input := gjson.GetBytes(normalized, "input").Array()
	if len(input) != 5 {
		t.Fatalf("merged input len = %d, want 5", len(input))
	}
	wantOrder := []string{"msg-1", "assistant-1", "tool-out-1", "msg-2", "msg-3"}
	for i, want := range wantOrder {
		if got := input[i].Get("id").String(); got != want {
			t.Fatalf("input[%d].id = %q, want %q", i, got, want)
		}
	}
}

func TestShouldHandlePrewarmLocally(t *testing.T) {
	normalized, err := NormalizeCreateRequest([]byte(`{"type":"response.create","model":"test-model","generate":false}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !ShouldHandlePrewarmLocally(normalized, nil) {
		t.Fatal("expected local prewarm handling for first generate=false request")
	}
	if ShouldHandlePrewarmLocally(normalized, []byte(`{"model":"test-model"}`)) {
		t.Fatal("did not expect local prewarm handling after session has history")
	}
}

func TestJSONPayloadsFromChunk(t *testing.T) {
	chunk := []byte("event: response.created\n\ndata: {\"type\":\"response.created\",\"response\":{\"id\":\"resp-1\"}}\n\ndata: [DONE]\n")

	payloads := JSONPayloadsFromChunk(chunk)
	if len(payloads) != 1 {
		t.Fatalf("payloads len = %d, want 1", len(payloads))
	}
	if got := gjson.GetBytes(payloads[0], "type").String(); got != "response.created" {
		t.Fatalf("payload type = %q, want response.created", got)
	}
}

func TestCompletedOutputFromPayload(t *testing.T) {
	payload := []byte(`{"type":"response.completed","response":{"id":"resp-1","output":[{"type":"message","id":"out-1"}]}}`)

	output := CompletedOutputFromPayload(payload)
	items := gjson.ParseBytes(output).Array()
	if len(items) != 1 {
		t.Fatalf("output len = %d, want 1", len(items))
	}
	if got := items[0].Get("id").String(); got != "out-1" {
		t.Fatalf("output[0].id = %q, want out-1", got)
	}
}

func TestBuildErrorPayload(t *testing.T) {
	payload := BuildErrorPayload(http.StatusBadRequest, []byte(`{"error":{"type":"invalid_request_error","message":"bad"}}`), http.Header{"Retry-After": []string{"10"}}, nil)
	if got := gjson.GetBytes(payload, "type").String(); got != EventTypeError {
		t.Fatalf("type = %q, want %q", got, EventTypeError)
	}
	if got := int(gjson.GetBytes(payload, "status").Int()); got != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", got, http.StatusBadRequest)
	}
	if got := gjson.GetBytes(payload, "headers.Retry-After").String(); got != "10" {
		t.Fatalf("Retry-After = %q, want 10", got)
	}
	if got := gjson.GetBytes(payload, "error.message").String(); got != "bad" {
		t.Fatalf("error.message = %q, want bad", got)
	}
}

func TestPayloadFromNonStreamingBodyWrapsResponse(t *testing.T) {
	payload := PayloadFromNonStreamingBody([]byte(`{"id":"resp-1","output":[{"type":"message","id":"out-1"}]}`))
	if got := gjson.GetBytes(payload, "type").String(); got != EventTypeCompleted {
		t.Fatalf("type = %q, want %q", got, EventTypeCompleted)
	}
	if got := gjson.GetBytes(payload, "response.id").String(); got != "resp-1" {
		t.Fatalf("response.id = %q, want resp-1", got)
	}
}
