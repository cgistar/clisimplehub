package responses

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"clisimplehub/internal/transformer/shared"
)

type Transformer struct{}

func (Transformer) TargetInterfaceType() string { return "codex" }

func (Transformer) TargetPath(_ bool, _ string) string { return "/v1/responses" }

func (Transformer) OutputContentType(isStreaming bool) string {
	if isStreaming {
		return "text/event-stream"
	}
	return "application/json"
}

func (Transformer) TransformRequest(modelName string, rawJSON []byte, stream bool) ([]byte, error) {
	root, err := shared.DecodeJSONMap(rawJSON)
	if err != nil {
		return nil, err
	}

	out := make(map[string]any)
	if strings.TrimSpace(modelName) != "" {
		out["model"] = modelName
	} else {
		out["model"] = shared.StringFromAny(root["model"])
	}
	out["stream"] = stream

	if instructions := shared.BuildClaudeSystemText(root["system"]); strings.TrimSpace(instructions) != "" {
		out["instructions"] = instructions
	}

	input := make([]any, 0)
	if messages, ok := root["messages"].([]any); ok {
		for _, m := range messages {
			msg, _ := m.(map[string]any)
			role := strings.TrimSpace(shared.StringFromAny(msg["role"]))
			if role == "" {
				continue
			}

			content := msg["content"]
			if s, ok := content.(string); ok {
				if strings.TrimSpace(s) == "" {
					continue
				}
				partType := "input_text"
				if role == "assistant" {
					partType = "output_text"
				}
				input = append(input, map[string]any{
					"type": "message",
					"role": role,
					"content": []any{
						map[string]any{"type": partType, "text": s},
					},
				})
				continue
			}

			parts, ok := content.([]any)
			if !ok {
				continue
			}

			message := map[string]any{
				"type":    "message",
				"role":    role,
				"content": []any{},
			}

			appendToMessage := func(part any) {
				c := message["content"].([]any)
				message["content"] = append(c, part)
			}
			flushMessage := func() {
				c := message["content"].([]any)
				if len(c) == 0 {
					return
				}
				input = append(input, message)
				message = map[string]any{"type": "message", "role": role, "content": []any{}}
			}
			partTypeForRole := func(role string) string {
				if role == "assistant" {
					return "output_text"
				}
				return "input_text"
			}

			for _, p := range parts {
				part, _ := p.(map[string]any)
				if part == nil {
					continue
				}
				switch strings.TrimSpace(shared.StringFromAny(part["type"])) {
				case "text":
					text := shared.StringFromAny(part["text"])
					if strings.TrimSpace(text) == "" {
						continue
					}
					appendToMessage(map[string]any{"type": partTypeForRole(role), "text": text})
				case "tool_use":
					flushMessage()
					callID := shared.StringFromAny(part["id"])
					name := shared.StringFromAny(part["name"])
					argsJSON, _ := json.Marshal(part["input"])
					if len(argsJSON) == 0 {
						argsJSON = []byte("{}")
					}
					input = append(input, map[string]any{
						"type":      "function_call",
						"call_id":   callID,
						"name":      name,
						"arguments": string(argsJSON),
					})
				case "tool_result":
					flushMessage()
					callID := shared.StringFromAny(part["tool_use_id"])
					output := shared.StringFromAny(part["content"])
					input = append(input, map[string]any{
						"type":    "function_call_output",
						"call_id": callID,
						"output":  output,
					})
				}
			}
			flushMessage()
		}
	}
	out["input"] = input

	if tools := convertClaudeToolsToResponsesTools(root["tools"]); len(tools) > 0 {
		out["tools"] = tools
		out["tool_choice"] = "auto"
	}

	return json.Marshal(out)
}

func (Transformer) TransformResponseStream(_ context.Context, modelName string, _ []byte, _ []byte, rawLine []byte, state *any) ([]string, error) {
	if state == nil {
		return nil, fmt.Errorf("nil transformer state")
	}
	if *state == nil {
		*state = &responsesToClaudeStreamState{}
	}
	s := (*state).(*responsesToClaudeStreamState)

	line := bytes.TrimSpace(rawLine)
	if len(line) == 0 {
		return nil, nil
	}

	payload := line
	if p, ok := shared.SSEDataPayload(line); ok {
		payload = p
	}

	if bytes.Equal(payload, []byte("[DONE]")) {
		if s.sentMessageStop {
			return nil, nil
		}
		s.sentMessageStop = true
		return []string{"event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"}, nil
	}

	root, err := shared.DecodeJSONMap(payload)
	if err != nil {
		return nil, nil
	}

	eventType := shared.StringFromAny(root["type"])
	switch eventType {
	case "response.created":
		resp, _ := root["response"].(map[string]any)
		if resp == nil {
			return nil, nil
		}
		messageID := shared.StringFromAny(resp["id"])
		model := modelName
		if model == "" {
			model = shared.StringFromAny(resp["model"])
		}
		msg := map[string]any{
			"type": "message_start",
			"message": map[string]any{
				"id":            messageID,
				"type":          "message",
				"role":          "assistant",
				"model":         model,
				"stop_reason":   nil,
				"stop_sequence": nil,
				"usage": map[string]any{
					"input_tokens":  0,
					"output_tokens": 0,
				},
				"content": []any{},
			},
		}
		return []string{shared.SSEEvent("message_start", msg)}, nil
	case "response.content_part.added":
		index := shared.IntFromAny(root["output_index"])
		ev := map[string]any{
			"type": "content_block_start",
			"index": index,
			"content_block": map[string]any{
				"type": "text",
				"text": "",
			},
		}
		return []string{shared.SSEEvent("content_block_start", ev)}, nil
	case "response.output_text.delta":
		index := shared.IntFromAny(root["output_index"])
		delta := shared.StringFromAny(root["delta"])
		if delta == "" {
			return nil, nil
		}
		ev := map[string]any{
			"type":  "content_block_delta",
			"index": index,
			"delta": map[string]any{
				"type": "text_delta",
				"text": delta,
			},
		}
		return []string{shared.SSEEvent("content_block_delta", ev)}, nil
	case "response.content_part.done":
		index := shared.IntFromAny(root["output_index"])
		return []string{shared.SSEEvent("content_block_stop", map[string]any{"type": "content_block_stop", "index": index})}, nil
	case "response.output_item.added":
		item, _ := root["item"].(map[string]any)
		if item == nil || shared.StringFromAny(item["type"]) != "function_call" {
			return nil, nil
		}
		s.hasToolCall = true
		index := shared.IntFromAny(root["output_index"])
		ev := map[string]any{
			"type":  "content_block_start",
			"index": index,
			"content_block": map[string]any{
				"type":  "tool_use",
				"id":    shared.StringFromAny(item["call_id"]),
				"name":  shared.StringFromAny(item["name"]),
				"input": map[string]any{},
			},
		}
		return []string{shared.SSEEvent("content_block_start", ev)}, nil
	case "response.function_call_arguments.delta":
		index := shared.IntFromAny(root["output_index"])
		delta := shared.StringFromAny(root["delta"])
		if delta == "" {
			return nil, nil
		}
		ev := map[string]any{
			"type":  "content_block_delta",
			"index": index,
			"delta": map[string]any{
				"type":         "input_json_delta",
				"partial_json": delta,
			},
		}
		return []string{shared.SSEEvent("content_block_delta", ev)}, nil
	case "response.output_item.done":
		item, _ := root["item"].(map[string]any)
		if item == nil || shared.StringFromAny(item["type"]) != "function_call" {
			return nil, nil
		}
		index := shared.IntFromAny(root["output_index"])
		return []string{shared.SSEEvent("content_block_stop", map[string]any{"type": "content_block_stop", "index": index})}, nil
	case "response.completed":
		resp, _ := root["response"].(map[string]any)
		if resp == nil {
			return nil, nil
		}
		usage, _ := resp["usage"].(map[string]any)
		stopReason := "end_turn"
		if s.hasToolCall {
			stopReason = "tool_use"
		}
		msgDelta := map[string]any{
			"type": "message_delta",
			"delta": map[string]any{
				"stop_reason":   stopReason,
				"stop_sequence": nil,
			},
			"usage": map[string]any{
				"input_tokens":  shared.IntFromAny(usage["input_tokens"]),
				"output_tokens": shared.IntFromAny(usage["output_tokens"]),
			},
		}
		s.sentMessageStop = true
		return []string{
			shared.SSEEvent("message_delta", msgDelta),
			"event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n",
		}, nil
	default:
		return nil, nil
	}
}

func (Transformer) TransformResponseNonStream(_ context.Context, modelName string, _ []byte, _ []byte, rawJSON []byte, _ *any) ([]byte, error) {
	root, err := shared.DecodeJSONMap(rawJSON)
	if err != nil {
		return nil, err
	}
	if t := shared.StringFromAny(root["type"]); t == "response.completed" {
		if resp, ok := root["response"].(map[string]any); ok {
			return buildClaudeMessageFromResponseObject(modelName, resp)
		}
	}
	return buildClaudeMessageFromResponseObject(modelName, root)
}

type responsesToClaudeStreamState struct {
	hasToolCall     bool
	sentMessageStop bool
}

func buildClaudeMessageFromResponseObject(modelName string, response map[string]any) ([]byte, error) {
	if response == nil {
		return nil, fmt.Errorf("empty response")
	}

	id := shared.StringFromAny(response["id"])
	if id == "" {
		id = "msg_" + shared.RandomSuffix()
	}

	model := modelName
	if model == "" {
		model = shared.StringFromAny(response["model"])
	}

	usage, _ := response["usage"].(map[string]any)

	out := map[string]any{
		"id":            id,
		"type":          "message",
		"role":          "assistant",
		"model":         model,
		"stop_sequence": nil,
			"usage": map[string]any{
			"input_tokens":  shared.IntFromAny(usage["input_tokens"]),
			"output_tokens": shared.IntFromAny(usage["output_tokens"]),
		},
	}

	var contentBlocks []any
	hasToolCall := false

	if output, ok := response["output"].([]any); ok {
		for _, itemRaw := range output {
			item, _ := itemRaw.(map[string]any)
			if item == nil {
				continue
			}
			switch shared.StringFromAny(item["type"]) {
			case "message":
				if contents, ok := item["content"].([]any); ok {
					for _, cRaw := range contents {
						c, _ := cRaw.(map[string]any)
						if c == nil {
							continue
						}
						if shared.StringFromAny(c["type"]) != "output_text" {
							continue
						}
						txt := shared.StringFromAny(c["text"])
						if strings.TrimSpace(txt) != "" {
							contentBlocks = append(contentBlocks, map[string]any{"type": "text", "text": txt})
						}
					}
				}
			case "output_text":
				if txt := shared.StringFromAny(item["text"]); strings.TrimSpace(txt) != "" {
					contentBlocks = append(contentBlocks, map[string]any{"type": "text", "text": txt})
				}
			case "function_call":
				hasToolCall = true
				argsStr := shared.StringFromAny(item["arguments"])
				input := map[string]any{}
				if strings.TrimSpace(argsStr) != "" {
					_ = json.Unmarshal([]byte(argsStr), &input)
				}
				contentBlocks = append(contentBlocks, map[string]any{
					"type":  "tool_use",
					"id":    shared.StringFromAny(item["call_id"]),
					"name":  shared.StringFromAny(item["name"]),
					"input": input,
				})
			}
		}
	}

	out["content"] = contentBlocks
	out["stop_reason"] = "end_turn"
	if hasToolCall {
		out["stop_reason"] = "tool_use"
	}

	return json.Marshal(out)
}

func convertClaudeToolsToResponsesTools(v any) []any {
	toolsArr, ok := v.([]any)
	if !ok {
		return nil
	}
	out := make([]any, 0, len(toolsArr))
	for _, t := range toolsArr {
		tool, _ := t.(map[string]any)
		if tool == nil {
			continue
		}
		name := shared.StringFromAny(tool["name"])
		if strings.TrimSpace(name) == "" {
			continue
		}
		fn := map[string]any{
			"type": "function",
			"name": name,
		}
		if d := shared.StringFromAny(tool["description"]); strings.TrimSpace(d) != "" {
			fn["description"] = d
		}
		if schema, ok := tool["input_schema"].(map[string]any); ok && len(schema) > 0 {
			fn["parameters"] = schema
		} else {
			fn["parameters"] = map[string]any{"type": "object", "properties": map[string]any{}}
		}
		out = append(out, fn)
	}
	return out
}

 
