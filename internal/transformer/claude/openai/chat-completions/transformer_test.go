package chat_completions

import (
	"context"
	"encoding/json"
	"testing"
	"strings"
)

func TestTransformRequest_Basic(t *testing.T) {
	t.Parallel()

	raw := []byte(`{
		"model":"alias-model",
		"max_tokens":123,
		"system":[{"type":"text","text":"sys"}],
		"messages":[
			{"role":"user","content":[{"type":"text","text":"hi"}]},
			{"role":"assistant","content":[{"type":"tool_use","id":"toolu_1","name":"fn","input":{"a":1}}]},
			{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_1","content":"ok"}]}
		],
		"tools":[{"name":"fn","description":"d","input_schema":{"type":"object","properties":{"a":{"type":"number"}}}}]
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

func TestTransformResponseStream_OpenAIToClaude(t *testing.T) {
	t.Parallel()

	var state any
	tr := Transformer{}

	lines := [][]byte{
		[]byte(`data: {"id":"chatcmpl_1","model":"gpt-4o","choices":[{"index":0,"delta":{"role":"assistant","content":"hello"},"finish_reason":null}]}`),
		[]byte(`data: {"id":"chatcmpl_1","model":"gpt-4o","choices":[{"index":0,"delta":{"content":" world"},"finish_reason":"stop"}]}`),
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

	foundStart := false
	foundDelta := false
	foundStop := false
	for _, s := range out {
		if s == "" {
			continue
		}
		if containsAll(s, "event: message_start", "\"type\":\"message_start\"") {
			foundStart = true
		}
		if containsAll(s, "event: content_block_delta", "\"type\":\"content_block_delta\"") {
			foundDelta = true
		}
		if containsAll(s, "event: message_stop", "\"type\":\"message_stop\"") {
			foundStop = true
		}
	}
	if !foundStart || !foundDelta || !foundStop {
		t.Fatalf("foundStart=%v foundDelta=%v foundStop=%v out=%v", foundStart, foundDelta, foundStop, out)
	}
}

func containsAll(s string, subs ...string) bool {
	for _, sub := range subs {
		if !strings.Contains(s, sub) {
			return false
		}
	}
	return true
}
