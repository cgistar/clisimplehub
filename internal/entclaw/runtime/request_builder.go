package entclawruntime

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

const defaultResponsesInstructions = "You are a helpful coding assistant."

func buildInitialLoopbackBody(task *TaskRequest, tools *ToolRuntime) ([]byte, error) {
	if task == nil {
		return nil, fmt.Errorf("task is nil")
	}

	skillPrompt := buildSkillDiscoveryInstructions(tools)

	if task.Format != FormatResponses {
		if len(task.RawBody) == 0 {
			return append([]byte(nil), task.RawBody...), nil
		}
		return mutateJSON(task.RawBody, func(payload map[string]any) error {
			switch task.Format {
			case FormatChatCompletions:
				payload["messages"] = prependSystemMessage(payload["messages"], skillPrompt)
			case FormatMessages:
				payload["system"] = mergePromptText(skillPrompt, stringFromAny(payload["system"]))
			}
			payload["tools"] = builtinToolDefinitions()
			return nil
		})
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
			"instructions": mergeResponseInstructions(skillPrompt, ""),
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
		payload["instructions"] = mergeResponseInstructions(skillPrompt, stringFromAny(payload["instructions"]))
		if strings.TrimSpace(stringFromAny(payload["model"])) == "" && strings.TrimSpace(task.Model) != "" {
			payload["model"] = task.Model
		}
		payload["tools"] = builtinToolDefinitions()
		return nil
	})
}

func buildSkillDiscoveryInstructions(tools *ToolRuntime) string {
	if tools == nil {
		return ""
	}

	entries, err := tools.skills.Catalog(context.Background())
	if err != nil || len(entries) == 0 {
		return ""
	}

	var out strings.Builder
	out.WriteString("Before replying, scan <available_skills> descriptions.\n")
	out.WriteString("If exactly one skill clearly matches the task, call skill_read(name) first.\n")
	out.WriteString("After reading SKILL.md, follow its guidance and decide whether to call " + toolNameRead + ", " + toolNameWrite + ", mcp_call, " + toolNameExec + ", or skill_run.\n")
	out.WriteString("Do not call skill_run unless the selected SKILL.md indicates a script should be executed.\n\n")
	out.WriteString("<available_skills>\n")
	for _, entry := range entries {
		out.WriteString("  <skill>\n")
		out.WriteString("    <name>" + escapePromptText(entry.Name) + "</name>\n")
		out.WriteString("    <description>" + escapePromptText(entry.Description) + "</description>\n")
		out.WriteString("    <location>" + escapePromptText(entry.Location) + "</location>\n")
		out.WriteString("  </skill>\n")
	}
	out.WriteString("</available_skills>")
	return out.String()
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

func mergePromptText(parts ...string) string {
	seen := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		seen = append(seen, part)
	}
	return strings.Join(seen, "\n\n")
}

func mergeResponseInstructions(skillPrompt string, existing string) string {
	existing = strings.TrimSpace(existing)
	if existing != "" {
		return mergePromptText(existing, skillPrompt)
	}
	return mergePromptText(defaultResponsesInstructions, skillPrompt)
}

func prependSystemMessage(raw any, content string) []any {
	messages, _ := raw.([]any)
	content = strings.TrimSpace(content)
	if content == "" {
		return messages
	}

	out := make([]any, 0, len(messages)+1)
	out = append(out, map[string]any{
		"role":    "system",
		"content": content,
	})
	out = append(out, messages...)
	return out
}

func escapePromptText(text string) string {
	replacer := strings.NewReplacer(
		"&", "&amp;",
		"<", "&lt;",
		">", "&gt;",
		`"`, "&quot;",
		"'", "&apos;",
	)
	return replacer.Replace(text)
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
			"name":        "skill_read",
			"description": "Read the SKILL.md instructions for a local entclaw skill.",
			"parameters": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"name": map[string]any{
						"type": "string",
					},
				},
				"required":             []string{"name"},
				"additionalProperties": false,
			},
		},
		{
			"type":        "function",
			"name":        "skill_run",
			"description": "Run a script from a selected entclaw skill after reading its SKILL.md instructions.",
			"parameters": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"name": map[string]any{
						"type": "string",
					},
					"script": map[string]any{
						"type": "string",
					},
					"args": map[string]any{
						"type": "array",
						"items": map[string]any{
							"type": "string",
						},
					},
				},
				"required":             []string{"name", "script"},
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
			"name":        toolNameRead,
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
			"name":        toolNameWrite,
			"description": "Write a file under the entclaw data root, including skills and mcp config files.",
			"parameters": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"path": map[string]any{
						"type": "string",
					},
					"content": map[string]any{
						"type": "string",
					},
				},
				"required":             []string{"path", "content"},
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
			"name":        toolNameExec,
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
