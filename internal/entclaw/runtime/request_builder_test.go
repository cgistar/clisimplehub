package entclawruntime

import (
	"testing"

	"github.com/tidwall/gjson"
)

func TestBuildInitialLoopbackBodyResponsesNormalizesInputAndPreservesInstructions(t *testing.T) {
	body, err := buildInitialLoopbackBody(&TaskRequest{
		Format:  FormatResponses,
		Model:   "gpt-5.4",
		RawBody: []byte(`{"model":"gpt-5.4","instructions":"be concise","input":"hello","temperature":0.2,"tools":[{"type":"function","name":"client_tool"}]}`),
	}, nil)
	if err != nil {
		t.Fatalf("buildInitialLoopbackBody: %v", err)
	}

	root := gjson.ParseBytes(body)
	if root.Get("instructions").String() != "be concise" {
		t.Fatalf("instructions = %s, want be concise", root.Get("instructions").Raw)
	}
	if root.Get("temperature").Num != 0.2 {
		t.Fatalf("temperature = %s, want 0.2", root.Get("temperature").Raw)
	}
	if root.Get("input.0.type").String() != "message" {
		t.Fatalf("input[0] type = %s", root.Get("input.0").Raw)
	}
	if root.Get("input.0.role").String() != "user" {
		t.Fatalf("input[0] role = %s", root.Get("input.0").Raw)
	}
	if root.Get("input.0.content").String() != "hello" {
		t.Fatalf("input[0] content = %s", root.Get("input.0").Raw)
	}
	if root.Get("tools.#").Int() == 0 {
		t.Fatalf("tools should be injected: %s", root.Get("tools").Raw)
	}
	if root.Get("tools.0.name").String() != "skill_list" {
		t.Fatalf("tools[0] = %s, want built-in tool set", root.Get("tools.0").Raw)
	}
	if root.Get(`tools.#(name="client_tool")`).Exists() {
		t.Fatalf("client tools should not be forwarded: %s", root.Get("tools").Raw)
	}
}

func TestBuildInitialLoopbackBodyResponsesAddsDefaultInstructionsWhenMissing(t *testing.T) {
	body, err := buildInitialLoopbackBody(&TaskRequest{
		Format:  FormatResponses,
		Model:   "gpt-5.4",
		RawBody: []byte(`{"model":"gpt-5.4","input":"hello"}`),
	}, nil)
	if err != nil {
		t.Fatalf("buildInitialLoopbackBody: %v", err)
	}

	root := gjson.ParseBytes(body)
	if root.Get("instructions").String() == "" {
		t.Fatalf("instructions should be populated for strict /responses upstreams: %s", root.Raw)
	}
	if root.Get("input.0.type").String() != "message" {
		t.Fatalf("input[0] type = %s", root.Get("input.0").Raw)
	}
}
