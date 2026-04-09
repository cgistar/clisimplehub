package entclawruntime

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStdioMCPCallerInvokesToolCall(t *testing.T) {
	dataDir := t.TempDir()
	serverPath := writeStubMCPServer(t, dataDir)
	caller := NewStdioMCPCaller(dataDir)

	config := mustMarshalJSON(t, map[string]any{
		"command": serverPath,
		"cwd":     ".",
	})
	args := mustMarshalJSON(t, map[string]any{
		"method": "tools/call",
		"params": map[string]any{
			"name": "search_repositories",
			"arguments": map[string]any{
				"query": "openclaw",
			},
		},
	})

	result, err := caller(context.Background(), "github", config, args)
	if err != nil {
		t.Fatalf("caller(...): %v", err)
	}

	var payload struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		IsError bool `json:"isError"`
	}
	if err := json.Unmarshal(result, &payload); err != nil {
		t.Fatalf("json.Unmarshal(result): %v", err)
	}
	if payload.IsError {
		t.Fatalf("payload.IsError = true, want false with result %s", result)
	}
	if len(payload.Content) != 1 || payload.Content[0].Text != "tool:search_repositories:openclaw" {
		t.Fatalf("payload.Content = %+v, want tool call payload", payload.Content)
	}
}

func TestStdioMCPCallerSupportsResourcesAndPrompts(t *testing.T) {
	dataDir := t.TempDir()
	serverPath := writeStubMCPServer(t, dataDir)
	caller := NewStdioMCPCaller(dataDir)
	config := mustMarshalJSON(t, map[string]any{
		"command": serverPath,
	})

	cases := []struct {
		name       string
		args       map[string]any
		wantSubstr string
	}{
		{
			name: "resources/list",
			args: map[string]any{
				"method": "resources/list",
			},
			wantSubstr: `"uri":"resource://repo/openclaw"`,
		},
		{
			name: "resources/read",
			args: map[string]any{
				"method": "resources/read",
				"params": map[string]any{
					"uri": "resource://repo/openclaw",
				},
			},
			wantSubstr: `"text":"resource:resource://repo/openclaw"`,
		},
		{
			name: "prompts/list",
			args: map[string]any{
				"method": "prompts/list",
			},
			wantSubstr: `"name":"repo_search"`,
		},
		{
			name: "prompts/get",
			args: map[string]any{
				"method": "prompts/get",
				"params": map[string]any{
					"name": "repo_search",
				},
			},
			wantSubstr: `"text":"prompt:repo_search"`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			result, err := caller(context.Background(), "github", config, mustMarshalJSON(t, tc.args))
			if err != nil {
				t.Fatalf("caller(...): %v", err)
			}
			if !containsJSONSubstring(string(result), tc.wantSubstr) {
				t.Fatalf("result = %s, want substring %s", result, tc.wantSubstr)
			}
		})
	}
}

func TestStdioMCPCallerRejectsDisabledConfig(t *testing.T) {
	dataDir := t.TempDir()
	caller := NewStdioMCPCaller(dataDir)

	_, err := caller(context.Background(), "github", mustMarshalJSON(t, map[string]any{
		"command":  "/bin/echo",
		"disabled": true,
	}), mustMarshalJSON(t, map[string]any{
		"method": "tools/list",
	}))
	if err == nil {
		t.Fatal("caller error = nil, want disabled config error")
	}
}

func writeStubMCPServer(t *testing.T, dir string) string {
	t.Helper()

	path := filepath.Join(dir, "stub-mcp.sh")
	content := `#!/bin/sh
while IFS= read -r line; do
  case "$line" in
    *'"method":"initialize"'*)
      printf '%s\n' '{"jsonrpc":"2.0","id":1,"result":{"protocolVersion":"2025-03-26","capabilities":{"tools":{},"resources":{},"prompts":{}},"serverInfo":{"name":"stub","version":"1.0.0"}}}'
      ;;
    *'"method":"notifications/initialized"'*)
      ;;
    *'"method":"tools/call"'*)
      printf '%s\n' '{"jsonrpc":"2.0","id":2,"result":{"content":[{"type":"text","text":"tool:search_repositories:openclaw"}],"isError":false}}'
      ;;
    *'"method":"resources/list"'*)
      printf '%s\n' '{"jsonrpc":"2.0","id":2,"result":{"resources":[{"uri":"resource://repo/openclaw","name":"openclaw"}]}}'
      ;;
    *'"method":"resources/read"'*)
      printf '%s\n' '{"jsonrpc":"2.0","id":2,"result":{"contents":[{"uri":"resource://repo/openclaw","mimeType":"text/plain","text":"resource:resource://repo/openclaw"}]}}'
      ;;
    *'"method":"prompts/list"'*)
      printf '%s\n' '{"jsonrpc":"2.0","id":2,"result":{"prompts":[{"name":"repo_search","description":"Search repos"}]}}'
      ;;
    *'"method":"prompts/get"'*)
      printf '%s\n' '{"jsonrpc":"2.0","id":2,"result":{"messages":[{"role":"user","content":{"type":"text","text":"prompt:repo_search"}}]}}'
      ;;
  esac
done
`
	if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
		t.Fatalf("os.WriteFile(stub-mcp.sh): %v", err)
	}
	return path
}

func mustMarshalJSON(t *testing.T, value any) json.RawMessage {
	t.Helper()
	body, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("json.Marshal(%T): %v", value, err)
	}
	return body
}

func containsJSONSubstring(raw string, want string) bool {
	return json.Valid([]byte(raw)) && strings.Contains(raw, want)
}
