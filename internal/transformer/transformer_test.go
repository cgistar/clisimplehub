package transformer_test

import (
	"testing"

	"clisimplehub/internal/transformer"
)

func TestGetFromClaude(t *testing.T) {
	t.Parallel()

	cases := []struct {
		spec      string
		target    string
		targetPath string
	}{
		{spec: "openai/chat-completions", target: "chat", targetPath: "/v1/chat/completions"},
		{spec: "openai/responses", target: "codex", targetPath: "/v1/responses"},
		{spec: "gemini", target: "gemini", targetPath: "/v1beta/models/gemini-1.5-pro:generateContent"},
	}

	for _, tc := range cases {
		tr, err := transformer.Get("claude", tc.spec)
		if err != nil {
			t.Fatalf("Get(%q) err=%v", tc.spec, err)
		}
		if got := tr.TargetInterfaceType(); got != tc.target {
			t.Fatalf("TargetInterfaceType(%q)=%q want %q", tc.spec, got, tc.target)
		}

		path := tr.TargetPath(false, "")
		if path != tc.targetPath {
			t.Fatalf("TargetPath(%q)=%q want %q", tc.spec, path, tc.targetPath)
		}
	}
}

func TestGetFromCodex(t *testing.T) {
	t.Parallel()

	tr, err := transformer.Get("codex", "openai/chat-completions")
	if err != nil {
		t.Fatalf("Get err=%v", err)
	}
	if got := tr.TargetInterfaceType(); got != "chat" {
		t.Fatalf("TargetInterfaceType=%q want %q", got, "chat")
	}
	if got := tr.TargetPath(false, ""); got != "/v1/chat/completions" {
		t.Fatalf("TargetPath=%q want %q", got, "/v1/chat/completions")
	}
}

func TestList(t *testing.T) {
	t.Parallel()

	claude, err := transformer.List("claude")
	if err != nil {
		t.Fatalf("List(claude) err=%v", err)
	}
	if len(claude) < 3 {
		t.Fatalf("List(claude) len=%d want >=3", len(claude))
	}

	codex, err := transformer.List("codex")
	if err != nil {
		t.Fatalf("List(codex) err=%v", err)
	}
	if len(codex) != 1 || codex[0] != "openai/chat-completions" {
		t.Fatalf("List(codex)=%v", codex)
	}
}
