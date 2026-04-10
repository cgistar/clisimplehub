package entclawruntime

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode"
)

type MaterializedMCPTool struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Parameters  map[string]any `json:"parameters"`
	ServerName  string         `json:"serverName"`
	ToolName    string         `json:"toolName"`
}

type mcpToolListResponse struct {
	Tools []struct {
		Name        string         `json:"name"`
		Description string         `json:"description"`
		InputSchema map[string]any `json:"inputSchema"`
	} `json:"tools"`
}

func (t MaterializedMCPTool) definition() map[string]any {
	parameters := t.Parameters
	if len(parameters) == 0 {
		parameters = map[string]any{
			"type":                 "object",
			"properties":           map[string]any{},
			"additionalProperties": true,
		}
	}
	return map[string]any{
		"type":        "function",
		"name":        t.Name,
		"description": t.Description,
		"parameters":  parameters,
	}
}

func (r *ToolRuntime) materializedMCPTools(ctx context.Context) []MaterializedMCPTool {
	if r == nil || r.mcpCall == nil {
		return nil
	}

	entries, err := listMCPConfigs(r.mcp)
	if err != nil || len(entries) == 0 {
		return nil
	}

	tools := make([]MaterializedMCPTool, 0)
	for _, entry := range entries {
		listArgs, marshalErr := json.Marshal(map[string]any{
			"method": "tools/list",
		})
		if marshalErr != nil {
			continue
		}

		result, callErr := r.mcpCall(ctx, entry.Name, entry.Config, listArgs)
		if callErr != nil {
			continue
		}

		var payload mcpToolListResponse
		if err := json.Unmarshal(rawJSONObjectOrEmpty(result), &payload); err != nil {
			continue
		}
		for _, tool := range payload.Tools {
			if strings.TrimSpace(tool.Name) == "" {
				continue
			}
			description := strings.TrimSpace(tool.Description)
			if description == "" {
				description = fmt.Sprintf("Call MCP tool %s on server %s.", tool.Name, entry.Name)
			}
			tools = append(tools, MaterializedMCPTool{
				Name:        materializedMCPToolName(entry.Name, tool.Name),
				Description: description,
				Parameters:  normalizeMaterializedMCPParameters(tool.InputSchema),
				ServerName:  entry.Name,
				ToolName:    tool.Name,
			})
		}
	}

	sort.Slice(tools, func(i, j int) bool {
		return tools[i].Name < tools[j].Name
	})
	return tools
}

func (r *ToolRuntime) executeMaterializedMCP(ctx context.Context, call ToolCall) (ToolResult, bool, error) {
	if r == nil || r.mcpCall == nil {
		return ToolResult{}, false, nil
	}

	for _, tool := range r.materializedMCPTools(ctx) {
		if normalizeToolName(call.Name) != tool.Name {
			continue
		}
		output, err := r.invokeMaterializedMCPTool(ctx, tool, call.Arguments)
		result, marshalErr := marshalToolPayload(map[string]any{
			"name":   tool.Name,
			"output": json.RawMessage(rawJSONObjectOrEmpty(output)),
		}, err)
		return result, true, marshalErr
	}

	return ToolResult{}, false, nil
}

func (r *ToolRuntime) invokeMaterializedMCPTool(ctx context.Context, tool MaterializedMCPTool, arguments json.RawMessage) (json.RawMessage, error) {
	config, err := r.mcp.Read(ctx, tool.ServerName)
	if err != nil {
		return nil, err
	}

	payload, err := json.Marshal(map[string]any{
		"method": "tools/call",
		"params": map[string]any{
			"name":      tool.ToolName,
			"arguments": decodeRawJSON(rawJSONObjectOrEmpty(arguments)),
		},
	})
	if err != nil {
		return nil, err
	}
	return r.mcpCall(ctx, tool.ServerName, config, payload)
}

func (r *ToolRuntime) findMaterializedMCPToolsByToolName(ctx context.Context, toolName string) []MaterializedMCPTool {
	name := strings.TrimSpace(toolName)
	if name == "" {
		return nil
	}

	matches := make([]MaterializedMCPTool, 0)
	for _, tool := range r.materializedMCPTools(ctx) {
		if strings.TrimSpace(tool.ToolName) == name {
			matches = append(matches, tool)
		}
	}
	return matches
}

type mcpConfigEntry struct {
	Name   string
	Config json.RawMessage
}

func listMCPConfigs(store MCPStore) ([]mcpConfigEntry, error) {
	if strings.TrimSpace(store.root) == "" {
		return nil, nil
	}
	entries, err := os.ReadDir(store.root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	configs := make([]mcpConfigEntry, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		name := strings.TrimSuffix(entry.Name(), ".json")
		if strings.TrimSpace(name) == "" {
			continue
		}
		body, err := os.ReadFile(filepath.Join(store.root, entry.Name()))
		if err != nil {
			return nil, err
		}
		configs = append(configs, mcpConfigEntry{
			Name:   name,
			Config: append(json.RawMessage(nil), body...),
		})
	}

	sort.Slice(configs, func(i, j int) bool {
		return configs[i].Name < configs[j].Name
	})
	return configs, nil
}

func materializedMCPToolName(serverName, toolName string) string {
	return sanitizeMCPNamePart(serverName) + "__" + sanitizeMCPNamePart(toolName)
}

func sanitizeMCPNamePart(raw string) string {
	var out strings.Builder
	lastUnderscore := false
	for _, r := range strings.TrimSpace(strings.ToLower(raw)) {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			out.WriteRune(r)
			lastUnderscore = false
		case r == '_' || r == '-' || r == ' ' || r == '.' || r == '/':
			if !lastUnderscore {
				out.WriteRune('_')
				lastUnderscore = true
			}
		}
	}
	name := strings.Trim(out.String(), "_")
	if name == "" {
		return "tool"
	}
	return name
}

func normalizeMaterializedMCPParameters(schema map[string]any) map[string]any {
	if len(schema) == 0 {
		return map[string]any{
			"type":                 "object",
			"properties":           map[string]any{},
			"additionalProperties": true,
		}
	}
	if _, ok := schema["type"]; !ok {
		schema["type"] = "object"
	}
	return schema
}
