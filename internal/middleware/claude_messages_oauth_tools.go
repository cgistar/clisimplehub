package middleware

import (
	"bytes"
	"fmt"
	"strings"

	"clisimplehub/internal/storage"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

var claudeMessagesOAuthToolRenameMap = map[string]string{
	"bash":         "Bash",
	"read":         "Read",
	"write":        "Write",
	"edit":         "Edit",
	"glob":         "Glob",
	"grep":         "Grep",
	"task":         "Task",
	"webfetch":     "WebFetch",
	"todowrite":    "TodoWrite",
	"question":     "Question",
	"skill":        "Skill",
	"ls":           "LS",
	"todoread":     "TodoRead",
	"notebookedit": "NotebookEdit",
}

var claudeMessagesOAuthToolRenameReverseMap = func() map[string]string {
	out := make(map[string]string, len(claudeMessagesOAuthToolRenameMap))
	for k, v := range claudeMessagesOAuthToolRenameMap {
		out[v] = k
	}
	return out
}()

var claudeMessagesOAuthToolsToRemove = map[string]bool{}

func ResolveClaudeMessagesAuthModeForEndpoint(endpoint *storage.Endpoint) string {
	return resolveClaudeMessagesAuthMode(endpoint, resolveClaudeMessagesConfig(endpoint))
}

func ReverseClaudeMessagesOAuthToolNamesForExecutor(body []byte) []byte {
	return reverseRemapClaudeMessagesOAuthToolNames(body)
}

func ReverseClaudeMessagesOAuthToolNamesFromStreamLineForExecutor(line []byte) []byte {
	return reverseRemapClaudeMessagesOAuthToolNamesFromStreamLine(line)
}

func shouldRemapClaudeMessagesOAuthTools(endpoint *storage.Endpoint, cfg resolvedClaudeMessagesConfig) bool {
	return resolveClaudeMessagesAuthMode(endpoint, cfg) == "oauth"
}

func remapClaudeMessagesOAuthToolNames(body []byte) ([]byte, bool) {
	renamed := false

	tools := gjson.GetBytes(body, "tools")
	if tools.Exists() && tools.IsArray() {
		var toolsJSON strings.Builder
		toolsJSON.WriteByte('[')
		toolCount := 0
		tools.ForEach(func(_, tool gjson.Result) bool {
			if tool.Get("type").Exists() && tool.Get("type").String() != "" {
				if toolCount > 0 {
					toolsJSON.WriteByte(',')
				}
				toolsJSON.WriteString(tool.Raw)
				toolCount++
				return true
			}

			name := tool.Get("name").String()
			if claudeMessagesOAuthToolsToRemove[name] {
				return true
			}

			toolJSON := tool.Raw
			if newName, ok := claudeMessagesOAuthToolRenameMap[name]; ok && newName != name {
				if updated, err := sjson.Set(toolJSON, "name", newName); err == nil {
					toolJSON = updated
					renamed = true
				}
			}

			if toolCount > 0 {
				toolsJSON.WriteByte(',')
			}
			toolsJSON.WriteString(toolJSON)
			toolCount++
			return true
		})
		toolsJSON.WriteByte(']')
		body, _ = sjson.SetRawBytes(body, "tools", []byte(toolsJSON.String()))
	}

	if gjson.GetBytes(body, "tool_choice.type").String() == "tool" {
		name := gjson.GetBytes(body, "tool_choice.name").String()
		if claudeMessagesOAuthToolsToRemove[name] {
			body, _ = sjson.DeleteBytes(body, "tool_choice")
		} else if newName, ok := claudeMessagesOAuthToolRenameMap[name]; ok && newName != name {
			body, _ = sjson.SetBytes(body, "tool_choice.name", newName)
			renamed = true
		}
	}

	messages := gjson.GetBytes(body, "messages")
	if messages.Exists() && messages.IsArray() {
		messages.ForEach(func(msgIndex, msg gjson.Result) bool {
			content := msg.Get("content")
			if !content.Exists() || !content.IsArray() {
				return true
			}
			content.ForEach(func(contentIndex, part gjson.Result) bool {
				switch part.Get("type").String() {
				case "tool_use":
					name := part.Get("name").String()
					if newName, ok := claudeMessagesOAuthToolRenameMap[name]; ok && newName != name {
						path := fmt.Sprintf("messages.%d.content.%d.name", msgIndex.Int(), contentIndex.Int())
						body, _ = sjson.SetBytes(body, path, newName)
						renamed = true
					}
				case "tool_reference":
					toolName := part.Get("tool_name").String()
					if newName, ok := claudeMessagesOAuthToolRenameMap[toolName]; ok && newName != toolName {
						path := fmt.Sprintf("messages.%d.content.%d.tool_name", msgIndex.Int(), contentIndex.Int())
						body, _ = sjson.SetBytes(body, path, newName)
						renamed = true
					}
				case "tool_result":
					nested := part.Get("content")
					if nested.Exists() && nested.IsArray() {
						nested.ForEach(func(nestedIndex, nestedPart gjson.Result) bool {
							if nestedPart.Get("type").String() != "tool_reference" {
								return true
							}
							toolName := nestedPart.Get("tool_name").String()
							if newName, ok := claudeMessagesOAuthToolRenameMap[toolName]; ok && newName != toolName {
								path := fmt.Sprintf("messages.%d.content.%d.content.%d.tool_name", msgIndex.Int(), contentIndex.Int(), nestedIndex.Int())
								body, _ = sjson.SetBytes(body, path, newName)
								renamed = true
							}
							return true
						})
					}
				}
				return true
			})
			return true
		})
	}

	return body, renamed
}

func reverseRemapClaudeMessagesOAuthToolNames(body []byte) []byte {
	content := gjson.GetBytes(body, "content")
	if !content.Exists() || !content.IsArray() {
		return body
	}
	content.ForEach(func(index, part gjson.Result) bool {
		switch part.Get("type").String() {
		case "tool_use":
			name := part.Get("name").String()
			if orig, ok := claudeMessagesOAuthToolRenameReverseMap[name]; ok {
				path := fmt.Sprintf("content.%d.name", index.Int())
				body, _ = sjson.SetBytes(body, path, orig)
			}
		case "tool_reference":
			toolName := part.Get("tool_name").String()
			if orig, ok := claudeMessagesOAuthToolRenameReverseMap[toolName]; ok {
				path := fmt.Sprintf("content.%d.tool_name", index.Int())
				body, _ = sjson.SetBytes(body, path, orig)
			}
		}
		return true
	})
	return body
}

func reverseRemapClaudeMessagesOAuthToolNamesFromStreamLine(line []byte) []byte {
	payload := extractJSONPayloadFromSSELine(line)
	if len(payload) == 0 || !gjson.ValidBytes(payload) {
		return line
	}
	contentBlock := gjson.GetBytes(payload, "content_block")
	if !contentBlock.Exists() {
		return line
	}

	var (
		updated []byte
		err     error
	)
	switch contentBlock.Get("type").String() {
	case "tool_use":
		name := contentBlock.Get("name").String()
		orig, ok := claudeMessagesOAuthToolRenameReverseMap[name]
		if !ok {
			return line
		}
		updated, err = sjson.SetBytes(payload, "content_block.name", orig)
	case "tool_reference":
		toolName := contentBlock.Get("tool_name").String()
		orig, ok := claudeMessagesOAuthToolRenameReverseMap[toolName]
		if !ok {
			return line
		}
		updated, err = sjson.SetBytes(payload, "content_block.tool_name", orig)
	default:
		return line
	}
	if err != nil {
		return line
	}
	if bytes.HasPrefix(bytes.TrimSpace(line), []byte("data:")) {
		return append([]byte("data: "), updated...)
	}
	return updated
}

func extractJSONPayloadFromSSELine(line []byte) []byte {
	trimmed := bytes.TrimSpace(line)
	if !bytes.HasPrefix(trimmed, []byte("data:")) {
		return trimmed
	}
	return bytes.TrimSpace(trimmed[5:])
}
