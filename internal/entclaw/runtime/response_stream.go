package entclawruntime

import (
	"encoding/json"
	"fmt"
	"strings"
)

func BuildResponsesProgressStream(responseID string, events []OrchestrationEvent) string {
	responseID = strings.TrimSpace(responseID)
	if responseID == "" {
		responseID = "resp_entclaw"
	}

	var out strings.Builder
	writeResponsesSSE(&out, "response.created", map[string]any{
		"type": "response.created",
		"response": map[string]any{
			"id":     responseID,
			"status": "in_progress",
		},
	})

	outputIndex := 0
	functionOutputIndexes := make(map[string]int)
	for _, event := range events {
		switch event.Type {
		case OrchestrationAssistantMessage:
			itemID := fmt.Sprintf("msg_%s_%d", responseID, outputIndex)
			writeResponsesSSE(&out, "response.output_item.added", map[string]any{
				"type":         "response.output_item.added",
				"output_index": outputIndex,
				"item": map[string]any{
					"id":      itemID,
					"type":    "message",
					"status":  "in_progress",
					"role":    "assistant",
					"content": []any{},
				},
			})
			writeResponsesSSE(&out, "response.output_item.done", map[string]any{
				"type":         "response.output_item.done",
				"output_index": outputIndex,
				"item": map[string]any{
					"id":     itemID,
					"type":   "message",
					"status": "completed",
					"role":   "assistant",
					"content": []any{
						map[string]any{
							"type": "output_text",
							"text": event.Text,
						},
					},
				},
			})
			outputIndex++
		case OrchestrationAssistantToolCall:
			itemID := responsesFunctionItemID(event.CallID)
			writeResponsesSSE(&out, "response.output_item.added", map[string]any{
				"type":         "response.output_item.added",
				"output_index": outputIndex,
				"item": map[string]any{
					"id":        itemID,
					"type":      "function_call",
					"status":    "in_progress",
					"call_id":   event.CallID,
					"name":      event.Name,
					"arguments": "",
				},
			})
			writeResponsesSSE(&out, "response.output_item.done", map[string]any{
				"type":         "response.output_item.done",
				"output_index": outputIndex,
				"item": map[string]any{
					"id":        itemID,
					"type":      "function_call",
					"status":    "completed",
					"call_id":   event.CallID,
					"name":      event.Name,
					"arguments": stringifyToolArguments(event.Arguments),
				},
			})
			outputIndex++
		case OrchestrationToolStarted:
			ensureResponsesFunctionOutputIndex(&out, event.CallID, functionOutputIndexes, &outputIndex)
		case OrchestrationToolCompleted:
			index := ensureResponsesFunctionOutputIndex(&out, event.CallID, functionOutputIndexes, &outputIndex)
			delete(functionOutputIndexes, event.CallID)
			writeResponsesSSE(&out, "response.output_item.done", map[string]any{
				"type":         "response.output_item.done",
				"output_index": index,
				"item": map[string]any{
					"id":       responsesFunctionOutputItemID(event.CallID),
					"type":     "function_call_output",
					"status":   "completed",
					"call_id":  event.CallID,
					"output":   stringifyToolResultContent(event.Output),
					"is_error": event.IsError,
				},
			})
		case OrchestrationFailed:
			if strings.TrimSpace(event.CallID) != "" {
				index := ensureResponsesFunctionOutputIndex(&out, event.CallID, functionOutputIndexes, &outputIndex)
				delete(functionOutputIndexes, event.CallID)
				writeResponsesSSE(&out, "response.output_item.done", map[string]any{
					"type":         "response.output_item.done",
					"output_index": index,
					"item": map[string]any{
						"id":       responsesFunctionOutputItemID(event.CallID),
						"type":     "function_call_output",
						"status":   "failed",
						"call_id":  event.CallID,
						"output":   stringifyToolResultContent(event.Output),
						"is_error": true,
					},
				})
			}

			failed := map[string]any{
				"type": "response.failed",
				"response": map[string]any{
					"id":     responseID,
					"status": "failed",
				},
			}
			if message := responsesFailureMessage(event.Output); message != "" {
				failed["error"] = map[string]any{"message": message}
			}
			writeResponsesSSE(&out, "response.failed", failed)
		case OrchestrationCompleted:
			writeResponsesSSE(&out, "response.completed", map[string]any{
				"type": "response.completed",
				"response": map[string]any{
					"id":     responseID,
					"status": "completed",
				},
			})
		}
	}

	return out.String()
}

func writeResponsesSSE(out *strings.Builder, event string, payload any) {
	if out == nil {
		return
	}

	raw, err := json.Marshal(payload)
	if err != nil {
		return
	}

	out.WriteString("event: ")
	out.WriteString(event)
	out.WriteString("\n")
	out.WriteString("data: ")
	_, _ = out.Write(raw)
	out.WriteString("\n\n")
}

func responsesFunctionItemID(callID string) string {
	return "fc_" + strings.TrimSpace(callID)
}

func responsesFunctionOutputItemID(callID string) string {
	return "fco_" + strings.TrimSpace(callID)
}

func ensureResponsesFunctionOutputIndex(out *strings.Builder, callID string, indexes map[string]int, next *int) int {
	if index, ok := indexes[callID]; ok {
		return index
	}

	index := *next
	indexes[callID] = index
	*next = *next + 1

	writeResponsesSSE(out, "response.output_item.added", map[string]any{
		"type":         "response.output_item.added",
		"output_index": index,
		"item": map[string]any{
			"id":      responsesFunctionOutputItemID(callID),
			"type":    "function_call_output",
			"status":  "in_progress",
			"call_id": callID,
			"output":  "",
		},
	})

	return index
}

func responsesFailureMessage(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}

	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err == nil {
		if message := responsesFailureMessageFromPayload(payload); message != "" {
			return message
		}
	}

	var message string
	if err := json.Unmarshal(raw, &message); err == nil {
		return strings.TrimSpace(message)
	}

	return strings.TrimSpace(string(raw))
}

func responsesFailureMessageFromPayload(payload map[string]any) string {
	if payload == nil {
		return ""
	}

	if message, ok := payload["message"].(string); ok {
		return strings.TrimSpace(message)
	}

	switch failure := payload["error"].(type) {
	case string:
		return strings.TrimSpace(failure)
	case map[string]any:
		if message, ok := failure["message"].(string); ok {
			return strings.TrimSpace(message)
		}
	}

	return ""
}
