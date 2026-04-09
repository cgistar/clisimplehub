package entclawruntime

import (
	"encoding/json"
	"strings"
)

func BuildChatProgressStream(events []OrchestrationEvent, finalBody []byte) string {
	var out strings.Builder
	for _, event := range events {
		text := simplifiedProgressText(event)
		if text == "" {
			continue
		}
		raw, err := json.Marshal(map[string]any{
			"choices": []any{
				map[string]any{
					"delta": map[string]any{
						"content": text,
					},
				},
			},
		})
		if err != nil {
			continue
		}
		out.WriteString("data: ")
		out.Write(raw)
		out.WriteString("\n\n")
	}
	out.Write(finalBody)
	return out.String()
}

func simplifiedProgressText(event OrchestrationEvent) string {
	switch {
	case event.Type == OrchestrationAssistantToolCall && event.Name == "skill_read":
		return "Reading skill instructions...\n"
	case event.Type == OrchestrationAssistantToolCall && event.Name == "skill_run":
		return "Running skill script...\n"
	case event.Type == OrchestrationToolCompleted:
		return "Tool finished.\n"
	default:
		return ""
	}
}
