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
	for _, name := range []string{toolNameRead, toolNameWrite, "edit", toolNameExec} {
		if !root.Get(`tools.#(name="` + name + `")`).Exists() {
			t.Fatalf("tools = %s, want %s", root.Get("tools").Raw, name)
		}
	}
	for _, legacyName := range []string{"fs_read", "fs_write", "command_exec"} {
		if root.Get(`tools.#(name="` + legacyName + `")`).Exists() {
			t.Fatalf("tools = %s, should not expose legacy %s", root.Get("tools").Raw, legacyName)
		}
	}
}

func TestBuiltinToolDefinitionsExposeReadPaginationSchemaWithoutChangingWriteSchema(t *testing.T) {
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
	readTool := root.Get(`tools.#(name="` + toolNameRead + `")`)
	if !readTool.Exists() {
		t.Fatalf("tools = %s, want %s", root.Get("tools").Raw, toolNameRead)
	}
	if !readTool.Get(`parameters.required.#(=="path")`).Exists() {
		t.Fatalf("read required = %s, want path required", readTool.Get("parameters.required").Raw)
	}
	if readTool.Get(`parameters.properties.path.type`).String() != "string" {
		t.Fatalf("read path schema = %s, want string", readTool.Get("parameters.properties.path").Raw)
	}
	if readTool.Get(`parameters.properties.offset.type`).String() != "integer" {
		t.Fatalf("read offset schema = %s, want integer", readTool.Get("parameters.properties.offset").Raw)
	}
	if readTool.Get(`parameters.properties.limit.type`).String() != "integer" {
		t.Fatalf("read limit schema = %s, want integer", readTool.Get("parameters.properties.limit").Raw)
	}
	if !strings.Contains(readTool.Get("description").String(), "1-based line") {
		t.Fatalf("read description = %q, want 1-based line semantics", readTool.Get("description").String())
	}

	writeTool := root.Get(`tools.#(name="` + toolNameWrite + `")`)
	if !writeTool.Exists() {
		t.Fatalf("tools = %s, want %s", root.Get("tools").Raw, toolNameWrite)
	}
	if !writeTool.Get(`parameters.required.#(=="path")`).Exists() || !writeTool.Get(`parameters.required.#(=="content")`).Exists() {
		t.Fatalf("write required = %s, want path/content required", writeTool.Get("parameters.required").Raw)
	}
	if writeTool.Get(`parameters.required.#`).Int() != 2 {
		t.Fatalf("write required count = %d, want 2", writeTool.Get(`parameters.required.#`).Int())
	}
	if writeTool.Get(`parameters.properties.path.type`).String() != "string" {
		t.Fatalf("write path schema = %s, want string", writeTool.Get("parameters.properties.path").Raw)
	}
	if writeTool.Get(`parameters.properties.content.type`).String() != "string" {
		t.Fatalf("write content schema = %s, want string", writeTool.Get("parameters.properties.content").Raw)
	}
	if writeTool.Get(`parameters.properties.offset`).Exists() {
		t.Fatalf("write schema = %s, should not expose offset", writeTool.Get("parameters.properties").Raw)
	}
	if writeTool.Get(`parameters.properties.limit`).Exists() {
		t.Fatalf("write schema = %s, should not expose limit", writeTool.Get("parameters.properties").Raw)
	}
}

func TestBuiltinToolDefinitionsExposeEditSchema(t *testing.T) {
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
	editTool := root.Get(`tools.#(name="edit")`)
	if !editTool.Exists() {
		t.Fatalf("tools = %s, want edit", root.Get("tools").Raw)
	}
	if !editTool.Get(`parameters.required.#(=="path")`).Exists() || !editTool.Get(`parameters.required.#(=="edits")`).Exists() {
		t.Fatalf("edit required = %s, want path/edits required", editTool.Get("parameters.required").Raw)
	}
	item := editTool.Get(`parameters.properties.edits.items`)
	if item.Get(`type`).String() != "object" {
		t.Fatalf("edit item schema = %s, want object", item.Raw)
	}
	if item.Get(`properties.oldText.type`).String() != "string" {
		t.Fatalf("edit oldText schema = %s, want string", item.Get("properties.oldText").Raw)
	}
	if item.Get(`properties.newText.type`).String() != "string" {
		t.Fatalf("edit newText schema = %s, want string", item.Get("properties.newText").Raw)
	}
	if !item.Get(`required.#(=="oldText")`).Exists() || !item.Get(`required.#(=="newText")`).Exists() {
		t.Fatalf("edit item required = %s, want oldText/newText required", item.Get("required").Raw)
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
