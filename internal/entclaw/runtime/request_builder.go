package entclawruntime

import (
	"encoding/json"
	"fmt"
	"strings"
)

const defaultResponsesInstructions = "You are a helpful coding assistant."

func buildInitialLoopbackBody(task *TaskRequest, tools *ToolRuntime) ([]byte, error) {
	if task == nil {
		return nil, fmt.Errorf("task is nil")
	}

	if task.Format != FormatResponses {
		return append([]byte(nil), task.RawBody...), nil
	}

	if len(task.RawBody) == 0 {
		input := any("")
		if len(task.InputRaw) > 0 {
			if err := json.Unmarshal(task.InputRaw, &input); err != nil {
				return nil, fmt.Errorf("decode responses input: %w", err)
			}
		}
		rawInput, err := normalizeResponsesInput(input)
		if err != nil {
			return nil, err
		}
		payload := map[string]any{
			"model":        task.Model,
			"instructions": defaultResponsesInstructions,
			"input":        rawInput,
			"tools":        builtinToolDefinitions(),
		}
		return json.Marshal(payload)
	}

	return mutateJSON(task.RawBody, func(payload map[string]any) error {
		rawInput, err := normalizeResponsesInput(payload["input"])
		if err != nil {
			return err
		}
		payload["input"] = rawInput
		if strings.TrimSpace(stringFromAny(payload["instructions"])) == "" {
			payload["instructions"] = defaultResponsesInstructions
		}
		if strings.TrimSpace(stringFromAny(payload["model"])) == "" && strings.TrimSpace(task.Model) != "" {
			payload["model"] = task.Model
		}
		payload["tools"] = builtinToolDefinitions()
		return nil
	})
}

func stringFromAny(value any) string {
	if value == nil {
		return ""
	}
	if text, ok := value.(string); ok {
		return text
	}
	return ""
}

func builtinToolDefinitions() []map[string]any {
	return []map[string]any{
		{
			"type":        "function",
			"name":        "skill_list",
			"description": "List local entclaw skills stored under the data directory.",
			"parameters": map[string]any{
				"type":                 "object",
				"properties":           map[string]any{},
				"additionalProperties": false,
			},
		},
		{
			"type":        "function",
			"name":        "skill_write",
			"description": "Create or update a local entclaw skill file.",
			"parameters": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"name": map[string]any{
						"type": "string",
					},
					"content": map[string]any{
						"type": "string",
					},
				},
				"required":             []string{"name", "content"},
				"additionalProperties": false,
			},
		},
		{
			"type":        "function",
			"name":        "memory_append",
			"description": "Append a tool round into the current entclaw session memory.",
			"parameters": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"round": map[string]any{
						"type": "object",
					},
				},
				"required":             []string{"round"},
				"additionalProperties": false,
			},
		},
		{
			"type":        "function",
			"name":        "fs_read",
			"description": "Read a file under the entclaw data root, including skills and mcp config files.",
			"parameters": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"path": map[string]any{
						"type": "string",
					},
				},
				"required":             []string{"path"},
				"additionalProperties": false,
			},
		},
		{
			"type":        "function",
			"name":        "mcp_call",
			"description": "Call a configured entclaw MCP server by name with JSON arguments.",
			"parameters": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"name": map[string]any{
						"type": "string",
					},
					"arguments": map[string]any{
						"type": "object",
					},
				},
				"required":             []string{"name"},
				"additionalProperties": false,
			},
		},
		{
			"type":        "function",
			"name":        "command_exec",
			"description": "Execute a command inside the entclaw working directory.",
			"parameters": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"command": map[string]any{
						"type": "string",
					},
					"args": map[string]any{
						"type": "array",
						"items": map[string]any{
							"type": "string",
						},
					},
				},
				"required":             []string{"command"},
				"additionalProperties": false,
			},
		},
	}
}
