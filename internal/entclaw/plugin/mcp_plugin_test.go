package entclawplugin

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	entclawruntime "clisimplehub/internal/entclaw/runtime"
	"clisimplehub/internal/plugin"
)

func TestInitWiresStdioMCPCaller(t *testing.T) {
	dataDir := t.TempDir()
	configPath := filepath.Join(dataDir, "config.json")
	if err := os.WriteFile(configPath, []byte(`{}`), 0o644); err != nil {
		t.Fatalf("os.WriteFile(config.json): %v", err)
	}

	serverPath := filepath.Join(dataDir, "stub-mcp.sh")
	if err := os.WriteFile(serverPath, []byte(`#!/bin/sh
while IFS= read -r line; do
  case "$line" in
    *'"method":"initialize"'*)
      printf '%s\n' '{"jsonrpc":"2.0","id":1,"result":{"protocolVersion":"2025-03-26","capabilities":{"tools":{}},"serverInfo":{"name":"stub","version":"1.0.0"}}}'
      ;;
    *'"method":"notifications/initialized"'*)
      ;;
    *'"method":"tools/list"'*)
      printf '%s\n' '{"jsonrpc":"2.0","id":2,"result":{"tools":[{"name":"search_repositories"}]}}'
      ;;
  esac
done
`), 0o755); err != nil {
		t.Fatalf("os.WriteFile(stub-mcp.sh): %v", err)
	}

	mcpDir := filepath.Join(dataDir, "entclaw", "mcp")
	if err := os.MkdirAll(mcpDir, 0o755); err != nil {
		t.Fatalf("os.MkdirAll(mcpDir): %v", err)
	}
	if err := os.WriteFile(filepath.Join(mcpDir, "github.json"), mustPluginJSON(t, map[string]any{
		"command": serverPath,
	}), 0o644); err != nil {
		t.Fatalf("os.WriteFile(github.json): %v", err)
	}

	var p EntclawPlugin
	if err := p.Init(plugin.InitConfig{ConfigPath: configPath}); err != nil {
		t.Fatalf("Init(...): %v", err)
	}

	result, err := p.tools.Execute(context.Background(), "session-1", entclawruntime.ToolCall{
		ID:   "call_1",
		Name: "mcp_call",
		Arguments: mustPluginJSON(t, map[string]any{
			"name": "github",
			"arguments": map[string]any{
				"method": "tools/list",
			},
		}),
	})
	if err != nil {
		t.Fatalf("Execute(mcp_call): %v", err)
	}
	if result.IsError {
		t.Fatalf("result.IsError = true, want false with content %s", result.Content)
	}
	if string(result.Content) == "" {
		t.Fatal("result.Content is empty")
	}
}

func mustPluginJSON(t *testing.T, value any) json.RawMessage {
	t.Helper()
	body, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("json.Marshal(%T): %v", value, err)
	}
	return body
}
