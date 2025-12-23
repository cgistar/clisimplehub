package chat_completions

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestTransformRequest_ResponsesToChat(t *testing.T) {
	t.Parallel()

	raw := []byte(`{
		"model":"alias-model",
		"instructions":"sys",
		"input":[
			{"type":"message","role":"user","content":[{"type":"input_text","text":"hi"}]},
			{"type":"function_call","call_id":"call_1","name":"fn","arguments":"{\"a\":1}"},
			{"type":"function_call_output","call_id":"call_1","output":"ok"}
		],
		"tools":[{"type":"function","name":"fn","description":"d","parameters":{"type":"object","properties":{"a":{"type":"number"}}}}]
	}`)

	outBytes, err := (Transformer{}).TransformRequest("alias-model", raw, true)
	if err != nil {
		t.Fatalf("TransformRequest err=%v", err)
	}

	var out map[string]any
	if err := json.Unmarshal(outBytes, &out); err != nil {
		t.Fatalf("unmarshal out: %v", err)
	}
	if out["model"] != "alias-model" {
		t.Fatalf("model=%v", out["model"])
	}
	if out["stream"] != true {
		t.Fatalf("stream=%v", out["stream"])
	}
	msgs, ok := out["messages"].([]any)
	if !ok || len(msgs) < 3 {
		t.Fatalf("messages=%T len=%d", out["messages"], len(msgs))
	}
}

func TestTransformResponseStream_ChatToResponses(t *testing.T) {
	t.Parallel()

	var state any
	tr := Transformer{}

	lines := [][]byte{
		[]byte(`data: {"id":"chatcmpl_1","created":123,"model":"gpt-4o","choices":[{"index":0,"delta":{"role":"assistant","content":"hello"},"finish_reason":null}]}`),
		[]byte(`data: {"id":"chatcmpl_1","created":123,"model":"gpt-4o","choices":[{"index":0,"delta":{"content":" world"},"finish_reason":"stop"}]}`),
		[]byte(`data: [DONE]`),
	}

	var out []string
	for _, line := range lines {
		outs, err := tr.TransformResponseStream(context.Background(), "gpt-4o", nil, nil, line, &state)
		if err != nil {
			t.Fatalf("TransformResponseStream err=%v", err)
		}
		out = append(out, outs...)
	}

	joined := strings.Join(out, "\n")
	if !strings.Contains(joined, "event: response.created") {
		t.Fatalf("missing response.created: %s", joined)
	}
	if !strings.Contains(joined, "event: response.output_text.delta") {
		t.Fatalf("missing response.output_text.delta: %s", joined)
	}
	if !strings.Contains(joined, "event: response.completed") {
		t.Fatalf("missing response.completed: %s", joined)
	}
}

