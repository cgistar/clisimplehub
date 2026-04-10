package entclawruntime

import (
	"os"
	"path/filepath"
	"strings"
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

func TestBuildInitialLoopbackBodyResponsesInjectsSkillCatalog(t *testing.T) {
	t.Parallel()

	body, err := buildInitialLoopbackBody(&TaskRequest{
		Format:  FormatResponses,
		Model:   "gpt-5.4",
		RawBody: []byte(`{"model":"gpt-5.4","instructions":"be concise","input":"hello"}`),
	}, runtimeWithSkillCatalogFixture(t))
	if err != nil {
		t.Fatalf("buildInitialLoopbackBody: %v", err)
	}

	root := gjson.ParseBytes(body)
	instructions := root.Get("instructions").String()
	if !strings.Contains(instructions, "<available_skills>") {
		t.Fatalf("instructions = %q, want available_skills", instructions)
	}
	if !strings.Contains(instructions, "github-search") {
		t.Fatalf("instructions = %q, want skill name", instructions)
	}
	if !strings.Contains(instructions, "<location>skills/github-search/SKILL.md</location>") {
		t.Fatalf("instructions = %q, want skills-relative location", instructions)
	}
	if !strings.Contains(instructions, "be concise") {
		t.Fatalf("instructions should preserve user content: %q", instructions)
	}
}

func TestBuildInitialLoopbackBodyChatPrependsSystemSkillCatalog(t *testing.T) {
	t.Parallel()

	body, err := buildInitialLoopbackBody(&TaskRequest{
		Format:  FormatChatCompletions,
		Model:   "gpt-5.4",
		RawBody: []byte(`{"model":"gpt-5.4","messages":[{"role":"user","content":"hello"}]}`),
	}, runtimeWithSkillCatalogFixture(t))
	if err != nil {
		t.Fatalf("buildInitialLoopbackBody: %v", err)
	}

	root := gjson.ParseBytes(body)
	if root.Get("messages.0.role").String() != "system" {
		t.Fatalf("messages[0] = %s, want system prompt", root.Get("messages.0").Raw)
	}
	if !strings.Contains(root.Get("messages.0.content").String(), "<available_skills>") {
		t.Fatalf("messages[0] content = %q", root.Get("messages.0.content").String())
	}
}

func TestBuildInitialLoopbackBodyMessagesAppendsSystemSkillCatalog(t *testing.T) {
	t.Parallel()

	body, err := buildInitialLoopbackBody(&TaskRequest{
		Format:  FormatMessages,
		Model:   "gpt-5.4",
		RawBody: []byte(`{"model":"gpt-5.4","system":"be concise","messages":[{"role":"user","content":[{"type":"text","text":"hello"}]}]}`),
	}, runtimeWithSkillCatalogFixture(t))
	if err != nil {
		t.Fatalf("buildInitialLoopbackBody: %v", err)
	}

	root := gjson.ParseBytes(body)
	systemText := root.Get("system").String()
	if !strings.Contains(systemText, "<available_skills>") {
		t.Fatalf("system = %q, want skill catalog", systemText)
	}
	if !strings.Contains(systemText, "be concise") {
		t.Fatalf("system should preserve user content: %q", systemText)
	}
}

func TestBuiltinToolDefinitionsIncludeCanonicalNames(t *testing.T) {
	t.Parallel()

	body, err := buildInitialLoopbackBody(&TaskRequest{
		Format:  FormatResponses,
		Model:   "gpt-5.4",
		RawBody: []byte(`{"model":"gpt-5.4","input":"hello"}`),
	}, nil)
	if err != nil {
		t.Fatalf("buildInitialLoopbackBody: %v", err)
	}

	root := gjson.ParseBytes(body)
	if !root.Get(`tools.#(name="skill_run")`).Exists() {
		t.Fatalf("tools = %s, want skill_run", root.Get("tools").Raw)
	}
	if !root.Get(`tools.#(name="read")`).Exists() {
		t.Fatalf("tools = %s, want read", root.Get("tools").Raw)
	}
	if !root.Get(`tools.#(name="write")`).Exists() {
		t.Fatalf("tools = %s, want write", root.Get("tools").Raw)
	}
	if !root.Get(`tools.#(name="exec")`).Exists() {
		t.Fatalf("tools = %s, want exec", root.Get("tools").Raw)
	}
	if root.Get(`tools.#(name="fs_write")`).Exists() {
		t.Fatalf("tools = %s, should not expose legacy fs_write", root.Get("tools").Raw)
	}
}

func runtimeWithSkillCatalogFixture(t *testing.T) *ToolRuntime {
	t.Helper()

	dataDir := t.TempDir()
	store := NewSkillStore(dataDir)
	skillDir := filepath.Join(dataDir, "entclaw", "skills", "github-search")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(`---
name: github-search
description: Search GitHub repositories and similar projects.
---
`), 0o644); err != nil {
		t.Fatalf("WriteFile(SKILL.md): %v", err)
	}

	return NewToolRuntime(dataDir, NewSessionStore(t.TempDir()), store, NewMCPStore(dataDir), nil, nil)
}
