package entclawruntime

import (
	"encoding/json"
	"strings"
)

func BuildMessagesProgressStream(events []OrchestrationEvent, finalBody []byte) string {
	var out strings.Builder
	for _, event := range events {
		text := simplifiedProgressText(event)
		if text == "" {
			continue
		}
		raw, err := json.Marshal(map[string]any{
			"type": "content_block_delta",
			"delta": map[string]any{
				"type": "text_delta",
				"text": text,
			},
		})
		if err != nil {
			continue
		}
		out.WriteString("event: content_block_delta\n")
		out.WriteString("data: ")
		out.Write(raw)
		out.WriteString("\n\n")
	}
	out.Write(finalBody)
	return out.String()
}
