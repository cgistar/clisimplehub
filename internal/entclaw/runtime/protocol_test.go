package entclawruntime

import "testing"

func TestChatAdapterParsesToolCalls(t *testing.T) {
	adapter := adapterForFormat(FormatChatCompletions)
	raw := []byte(`{"choices":[{"message":{"content":"","tool_calls":[{"id":"call_1","type":"function","function":{"name":"skill_list","arguments":"{}"}}]}}]}`)

	calls, finalText, err := adapter.ParseToolCalls(raw)
	if err != nil {
		t.Fatalf("ParseToolCalls: %v", err)
	}
	if len(calls) != 1 {
		t.Fatalf("len(calls) = %d, want 1", len(calls))
	}
	if finalText != "" {
		t.Fatalf("finalText = %q, want empty", finalText)
	}
}

func TestResponsesAdapterForcesStreamFlag(t *testing.T) {
	adapter := adapterForFormat(FormatResponses)

	body, err := adapter.WithStreamFlag([]byte(`{"model":"gpt-5.4","stream":false}`), true)
	if err != nil {
		t.Fatalf("WithStreamFlag: %v", err)
	}
	if string(body) != `{"model":"gpt-5.4","stream":true}` {
		t.Fatalf("body = %s", body)
	}
}
