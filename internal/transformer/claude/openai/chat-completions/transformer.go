package chat_completions

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"clisimplehub/internal/transformer/shared"
)

type Transformer struct{}

func (Transformer) TargetInterfaceType() string { return "chat" }

func (Transformer) TargetPath(_ bool, _ string) string { return "/v1/chat/completions" }

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
	if v, ok := root["max_tokens"]; ok {
		out["max_tokens"] = v
	}
	if v, ok := root["temperature"]; ok {
		out["temperature"] = v
	}
	if v, ok := root["top_p"]; ok {
		out["top_p"] = v
	}
	if v, ok := root["stop_sequences"]; ok {
		if stops := shared.StringListFromAny(v); len(stops) == 1 {
			out["stop"] = stops[0]
		} else if len(stops) > 1 {
			out["stop"] = stops
		}
	}
	out["stream"] = stream
	if modelName != "" {
		out["model"] = modelName
	} else if v, _ := root["model"].(string); strings.TrimSpace(v) != "" {
		out["model"] = v
	} else {
		out["model"] = ""
	}

	openAIMessages := make([]any, 0)
	if system := shared.BuildClaudeSystemText(root["system"]); strings.TrimSpace(system) != "" {
		openAIMessages = append(openAIMessages, map[string]any{
			"role":    "system",
			"content": system,
		})
	}

	if messages, ok := root["messages"].([]any); ok {
		for _, m := range messages {
			msg, _ := m.(map[string]any)
			role, _ := msg["role"].(string)
			role = strings.TrimSpace(role)
			if role == "" {
				continue
			}

			content := msg["content"]
			if s, ok := content.(string); ok {
				if strings.TrimSpace(s) == "" {
					continue
				}
				openAIMessages = append(openAIMessages, map[string]any{
					"role":    role,
					"content": s,
				})
				continue
			}

			parts, ok := content.([]any)
			if !ok {
				continue
			}

			var contentItems []any
			var toolCalls []any

			flushContent := func() {
				if len(contentItems) == 0 {
					return
				}
				openAIMessages = append(openAIMessages, map[string]any{
					"role":    role,
					"content": contentItems,
				})
				contentItems = nil
			}

			flushToolCalls := func() {
				if role != "assistant" || len(toolCalls) == 0 {
					return
				}
				openAIMessages = append(openAIMessages, map[string]any{
					"role":       "assistant",
					"tool_calls": toolCalls,
				})
				toolCalls = nil
			}

			for _, p := range parts {
				part, _ := p.(map[string]any)
				switch strings.TrimSpace(shared.StringFromAny(part["type"])) {
				case "text":
					if item, ok := convertClaudeTextPart(part); ok {
						contentItems = append(contentItems, item)
					}
				case "image":
					if item, ok := convertClaudeImagePart(part); ok {
						contentItems = append(contentItems, item)
					}
				case "tool_use":
					flushContent()
					call := convertClaudeToolUseToOpenAIToolCall(part)
					if call != nil {
						toolCalls = append(toolCalls, call)
					}
				case "tool_result":
					flushContent()
					flushToolCalls()
					toolMsg := convertClaudeToolResultToOpenAIToolMessage(part)
					if toolMsg != nil {
						openAIMessages = append(openAIMessages, toolMsg)
					}
				}
			}

			flushContent()
			flushToolCalls()
		}
	}

	out["messages"] = openAIMessages

	if tools := convertClaudeToolsToOpenAITools(root["tools"]); len(tools) > 0 {
		out["tools"] = tools
		if toolChoice, ok := root["tool_choice"]; ok {
			out["tool_choice"] = toolChoice
		} else {
			out["tool_choice"] = "auto"
		}
	}

	return json.Marshal(out)
}

func (Transformer) TransformResponseStream(_ context.Context, modelName string, _ []byte, _ []byte, rawLine []byte, state *any) ([]string, error) {
	if state == nil {
		return nil, fmt.Errorf("nil transformer state")
	}
	if *state == nil {
		*state = &openAIToClaudeStreamState{
			nextBlockIndex: 0,
			toolBlocks:     make(map[int]*toolBlock),
		}
	}
	s := (*state).(*openAIToClaudeStreamState)

	line := bytes.TrimSpace(rawLine)
	if len(line) == 0 {
		return nil, nil
	}

	payload, ok := shared.SSEDataPayload(line)
	if !ok {
		return nil, nil
	}

	if bytes.Equal(payload, []byte("[DONE]")) {
		return s.finish(nil), nil
	}

	root, err := shared.DecodeJSONMap(payload)
	if err != nil {
		return nil, nil
	}

	var outputs []string

	if !s.started {
		s.started = true
		s.messageID = shared.StringFromAny(root["id"])
		if s.messageID == "" {
			s.messageID = "msg_" + shared.RandomSuffix()
		}
		if modelName != "" {
			s.model = modelName
		} else {
			s.model = shared.StringFromAny(root["model"])
		}
		s.createdAt = time.Now().Unix()
		outputs = append(outputs, s.eventMessageStart())
	}

	choices, _ := root["choices"].([]any)
	if len(choices) == 0 {
		return outputs, nil
	}
	firstChoice, _ := choices[0].(map[string]any)

	if finish := shared.StringFromAny(firstChoice["finish_reason"]); strings.TrimSpace(finish) != "" {
		s.finishReason = finish
	}

	delta, _ := firstChoice["delta"].(map[string]any)
	if delta == nil {
		return outputs, nil
	}

	if content := shared.StringFromAny(delta["content"]); content != "" {
		outputs = append(outputs, s.ensureTextBlockStarted()...)
		outputs = append(outputs, s.eventTextDelta(content))
		s.hasContent = true
	}

	if tcAny, ok := delta["tool_calls"]; ok {
		if tcArr, ok := tcAny.([]any); ok {
			for _, tcRaw := range tcArr {
				tc, _ := tcRaw.(map[string]any)
				if tc == nil {
					continue
				}
				index := shared.IntFromAny(tc["index"])
				tb := s.toolBlocks[index]
				if tb == nil {
					tb = &toolBlock{index: index}
					s.toolBlocks[index] = tb
				}

				if id := shared.StringFromAny(tc["id"]); id != "" {
					tb.id = id
				}
				function, _ := tc["function"].(map[string]any)
				if function != nil {
					if name := shared.StringFromAny(function["name"]); name != "" {
						tb.name = name
					}
					if args := shared.StringFromAny(function["arguments"]); args != "" {
						tb.args.WriteString(args)
					}
				}

				if !tb.started && tb.id != "" && tb.name != "" {
					tb.started = true
					tb.blockIndex = s.nextBlockIndex
					s.nextBlockIndex++
					outputs = append(outputs, s.eventToolUseStart(tb.blockIndex, tb.id, tb.name))
				}

				if tb.started {
					lastArgsDelta := ""
					if function != nil {
						lastArgsDelta = shared.StringFromAny(function["arguments"])
					}
					if lastArgsDelta != "" {
						outputs = append(outputs, s.eventToolArgsDelta(tb.blockIndex, lastArgsDelta))
					}
					s.hasContent = true
				}
			}
		}
	}

	if s.finishReason != "" && len(outputs) > 0 {
		// Let the caller send [DONE] later; we only finish when [DONE] arrives.
		return outputs, nil
	}
	return outputs, nil
}

func (Transformer) TransformResponseNonStream(_ context.Context, modelName string, _ []byte, _ []byte, rawJSON []byte, _ *any) ([]byte, error) {
	root, err := shared.DecodeJSONMap(rawJSON)
	if err != nil {
		return nil, err
	}

	id := shared.StringFromAny(root["id"])
	if id == "" {
		id = "msg_" + shared.RandomSuffix()
	}
	model := modelName
	if model == "" {
		model = shared.StringFromAny(root["model"])
	}

	var finishReason string
	var contentText string
	var toolUses []any
	var usage map[string]any

	if u, ok := root["usage"].(map[string]any); ok {
		usage = u
	}

	choices, _ := root["choices"].([]any)
	if len(choices) > 0 {
		c0, _ := choices[0].(map[string]any)
		finishReason = shared.StringFromAny(c0["finish_reason"])
		msg, _ := c0["message"].(map[string]any)
		if msg != nil {
			contentText = shared.StringFromAny(msg["content"])
			if tcAny, ok := msg["tool_calls"]; ok {
				if tcArr, ok := tcAny.([]any); ok {
					for _, tcRaw := range tcArr {
						tc, _ := tcRaw.(map[string]any)
						if tc == nil {
							continue
						}
						toolUses = append(toolUses, openAIToolCallToClaudeToolUse(tc))
					}
				}
			}
		}
	}

	out := map[string]any{
		"id":            id,
		"type":          "message",
		"role":          "assistant",
		"model":         model,
		"stop_reason":   mapOpenAIFinishReasonToClaudeStopReason(finishReason),
		"stop_sequence": nil,
		"content":       []any{},
		"usage": map[string]any{
			"input_tokens":  shared.IntFromAny(usage["prompt_tokens"]),
			"output_tokens": shared.IntFromAny(usage["completion_tokens"]),
		},
	}

	var contentBlocks []any
	if strings.TrimSpace(contentText) != "" {
		contentBlocks = append(contentBlocks, map[string]any{"type": "text", "text": contentText})
	}
	for _, u := range toolUses {
		if u != nil {
			contentBlocks = append(contentBlocks, u)
		}
	}
	out["content"] = contentBlocks

	return json.Marshal(out)
}

type openAIToClaudeStreamState struct {
	started      bool
	messageID    string
	model        string
	createdAt    int64
	nextBlockIndex int

	textBlockStarted bool
	textBlockIndex   int

	toolBlocks map[int]*toolBlock

	finishReason string
	hasContent   bool
	finished     bool
}

type toolBlock struct {
	index     int
	started   bool
	blockIndex int
	id        string
	name      string
	args      strings.Builder
}

func (s *openAIToClaudeStreamState) eventMessageStart() string {
	msg := map[string]any{
		"type": "message_start",
		"message": map[string]any{
			"id":            s.messageID,
			"type":          "message",
			"role":          "assistant",
			"model":         s.model,
			"stop_reason":   nil,
			"stop_sequence": nil,
			"usage": map[string]any{
				"input_tokens":  0,
				"output_tokens": 0,
			},
			"content": []any{},
		},
	}
	return shared.SSEEvent("message_start", msg)
}

func (s *openAIToClaudeStreamState) ensureTextBlockStarted() []string {
	if s.textBlockStarted {
		return nil
	}
	s.textBlockStarted = true
	s.textBlockIndex = s.nextBlockIndex
	s.nextBlockIndex++

	ev := map[string]any{
		"type": "content_block_start",
		"index": s.textBlockIndex,
		"content_block": map[string]any{
			"type": "text",
			"text": "",
		},
	}
	return []string{shared.SSEEvent("content_block_start", ev)}
}

func (s *openAIToClaudeStreamState) eventTextDelta(text string) string {
	ev := map[string]any{
		"type":  "content_block_delta",
		"index": s.textBlockIndex,
		"delta": map[string]any{
			"type": "text_delta",
			"text": text,
		},
	}
	return shared.SSEEvent("content_block_delta", ev)
}

func (s *openAIToClaudeStreamState) eventToolUseStart(blockIndex int, id, name string) string {
	ev := map[string]any{
		"type":  "content_block_start",
		"index": blockIndex,
		"content_block": map[string]any{
			"type":  "tool_use",
			"id":    id,
			"name":  name,
			"input": map[string]any{},
		},
	}
	return shared.SSEEvent("content_block_start", ev)
}

func (s *openAIToClaudeStreamState) eventToolArgsDelta(blockIndex int, partial string) string {
	ev := map[string]any{
		"type":  "content_block_delta",
		"index": blockIndex,
		"delta": map[string]any{
			"type":         "input_json_delta",
			"partial_json": partial,
		},
	}
	return shared.SSEEvent("content_block_delta", ev)
}

func (s *openAIToClaudeStreamState) finish(finalUsage map[string]any) []string {
	if s.finished {
		return nil
	}
	s.finished = true

	var outputs []string

	if s.textBlockStarted {
		outputs = append(outputs, shared.SSEEvent("content_block_stop", map[string]any{
			"type":  "content_block_stop",
			"index": s.textBlockIndex,
		}))
		s.textBlockStarted = false
	}

	for _, tb := range s.toolBlocks {
		if tb == nil || !tb.started {
			continue
		}
		outputs = append(outputs, shared.SSEEvent("content_block_stop", map[string]any{
			"type":  "content_block_stop",
			"index": tb.blockIndex,
		}))
	}

	stopReason := mapOpenAIFinishReasonToClaudeStopReason(s.finishReason)
	usage := map[string]any{"input_tokens": 0, "output_tokens": 0}
	if finalUsage != nil {
		usage = map[string]any{
			"input_tokens":  shared.IntFromAny(finalUsage["prompt_tokens"]),
			"output_tokens": shared.IntFromAny(finalUsage["completion_tokens"]),
		}
	}

	outputs = append(outputs, shared.SSEEvent("message_delta", map[string]any{
		"type": "message_delta",
		"delta": map[string]any{
			"stop_reason":   stopReason,
			"stop_sequence": nil,
		},
		"usage": usage,
	}))
	outputs = append(outputs, "event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n")

	return outputs
}

func mapOpenAIFinishReasonToClaudeStopReason(finish string) any {
	switch strings.TrimSpace(finish) {
	case "tool_calls":
		return "tool_use"
	case "length":
		return "max_tokens"
	case "stop":
		return "end_turn"
	case "":
		return nil
	default:
		return "end_turn"
	}
}

func openAIToolCallToClaudeToolUse(tc map[string]any) any {
	if tc == nil {
		return nil
	}
	callID := shared.StringFromAny(tc["id"])
	function, _ := tc["function"].(map[string]any)
	if function == nil {
		return nil
	}
	name := shared.StringFromAny(function["name"])
	argsStr := shared.StringFromAny(function["arguments"])

	input := map[string]any{}
	if strings.TrimSpace(argsStr) != "" {
		_ = json.Unmarshal([]byte(argsStr), &input)
	}
	return map[string]any{
		"type":  "tool_use",
		"id":    callID,
		"name":  name,
		"input": input,
	}
}

func convertClaudeToolsToOpenAITools(v any) []any {
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
		out = append(out, map[string]any{"type": "function", "function": fn})
	}
	return out
}

func convertClaudeToolUseToOpenAIToolCall(part map[string]any) any {
	if part == nil {
		return nil
	}
	callID := shared.StringFromAny(part["id"])
	name := shared.StringFromAny(part["name"])
	if strings.TrimSpace(name) == "" {
		return nil
	}
	argsJSON, _ := json.Marshal(part["input"])
	if len(argsJSON) == 0 {
		argsJSON = []byte("{}")
	}
	return map[string]any{
		"id":   callID,
		"type": "function",
		"function": map[string]any{
			"name":      name,
			"arguments": string(argsJSON),
		},
	}
}

func convertClaudeToolResultToOpenAIToolMessage(part map[string]any) any {
	if part == nil {
		return nil
	}
	callID := shared.StringFromAny(part["tool_use_id"])
	if strings.TrimSpace(callID) == "" {
		return nil
	}
	content := shared.StringFromAny(part["content"])
	return map[string]any{
		"role":         "tool",
		"tool_call_id": callID,
		"content":      content,
	}
}

func convertClaudeTextPart(part map[string]any) (any, bool) {
	if part == nil {
		return nil, false
	}
	text := shared.StringFromAny(part["text"])
	if strings.TrimSpace(text) == "" {
		return nil, false
	}
	return map[string]any{"type": "text", "text": text}, true
}

func convertClaudeImagePart(part map[string]any) (any, bool) {
	if part == nil {
		return nil, false
	}

	imageURL := ""
	if source, ok := part["source"].(map[string]any); ok {
		switch strings.TrimSpace(shared.StringFromAny(source["type"])) {
		case "base64":
			mediaType := shared.StringFromAny(source["media_type"])
			if strings.TrimSpace(mediaType) == "" {
				mediaType = "application/octet-stream"
			}
			data := shared.StringFromAny(source["data"])
			if strings.TrimSpace(data) != "" {
				imageURL = "data:" + mediaType + ";base64," + data
			}
		case "url":
			imageURL = shared.StringFromAny(source["url"])
		}
	}
	if imageURL == "" {
		imageURL = shared.StringFromAny(part["url"])
	}
	if strings.TrimSpace(imageURL) == "" {
		return nil, false
	}
	return map[string]any{"type": "image_url", "image_url": map[string]any{"url": imageURL}}, true
}
