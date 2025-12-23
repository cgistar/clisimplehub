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

// Transformer implements: OpenAI Responses ("codex") <-> OpenAI Chat Completions ("chat").
// Use-case: client talks /v1/responses, upstream only supports /v1/chat/completions.
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
	out["model"] = modelName
	out["stream"] = stream
	out["messages"] = []any{}

	if v := root["max_output_tokens"]; v != nil {
		out["max_tokens"] = v
	}
	if v := root["parallel_tool_calls"]; v != nil {
		out["parallel_tool_calls"] = v
	}
	if v := root["reasoning"]; v != nil {
		out["reasoning"] = v
	}
	if v := root["reasoning_effort"]; v != nil {
		out["reasoning_effort"] = v
	}
	if v := root["tool_choice"]; v != nil {
		out["tool_choice"] = v
	}

	msgs := out["messages"].([]any)

	if instructions := shared.StringFromAny(root["instructions"]); strings.TrimSpace(instructions) != "" {
		msgs = append(msgs, map[string]any{"role": "system", "content": instructions})
	}

	// Convert `input` -> `messages`
	switch input := root["input"].(type) {
	case string:
		if strings.TrimSpace(input) != "" {
			msgs = append(msgs, map[string]any{"role": "user", "content": input})
		}
	case []any:
		for _, itemRaw := range input {
			item, _ := itemRaw.(map[string]any)
			if item == nil {
				continue
			}

			itemType := strings.TrimSpace(shared.StringFromAny(item["type"]))
			if itemType == "" && strings.TrimSpace(shared.StringFromAny(item["role"])) != "" {
				itemType = "message"
			}

			switch itemType {
			case "message", "":
				role := strings.TrimSpace(shared.StringFromAny(item["role"]))
				if role == "" {
					continue
				}

				contentVal := item["content"]
				if s, ok := contentVal.(string); ok {
					if strings.TrimSpace(s) == "" {
						continue
					}
					msgs = append(msgs, map[string]any{"role": role, "content": s})
					continue
				}

				contentArr, ok := contentVal.([]any)
				if !ok {
					continue
				}

				var textParts []string
				for _, cRaw := range contentArr {
					c, _ := cRaw.(map[string]any)
					if c == nil {
						continue
					}
					switch strings.TrimSpace(shared.StringFromAny(c["type"])) {
					case "input_text", "output_text", "":
						txt := shared.StringFromAny(c["text"])
						if strings.TrimSpace(txt) != "" {
							textParts = append(textParts, txt)
						}
					}
				}

				if len(textParts) > 0 {
					msgs = append(msgs, map[string]any{"role": role, "content": strings.Join(textParts, "\n")})
				}
			case "function_call":
				callID := strings.TrimSpace(shared.StringFromAny(item["call_id"]))
				name := strings.TrimSpace(shared.StringFromAny(item["name"]))
				args := strings.TrimSpace(shared.StringFromAny(item["arguments"]))
				if name == "" {
					continue
				}
				if args == "" {
					args = "{}"
				}
				msgs = append(msgs, map[string]any{
					"role": "assistant",
					"tool_calls": []any{
						map[string]any{
							"id":   callID,
							"type": "function",
							"function": map[string]any{
								"name":      name,
								"arguments": args,
							},
						},
					},
				})
			case "function_call_output":
				callID := strings.TrimSpace(shared.StringFromAny(item["call_id"]))
				output := shared.StringFromAny(item["output"])
				if callID == "" {
					continue
				}
				msgs = append(msgs, map[string]any{
					"role":         "tool",
					"tool_call_id": callID,
					"content":      output,
				})
			}
		}
	}

	out["messages"] = msgs

	if tools := convertResponsesToolsToChatTools(root["tools"]); len(tools) > 0 {
		out["tools"] = tools
	}

	return json.Marshal(out)
}

func (Transformer) TransformResponseStream(_ context.Context, modelName string, _ []byte, _ []byte, rawLine []byte, state *any) ([]string, error) {
	if state == nil {
		return nil, fmt.Errorf("nil transformer state")
	}
	if *state == nil {
		*state = &chatToResponsesState{
			toolCalls: make(map[int]*toolCallState),
		}
	}
	st := (*state).(*chatToResponsesState)

	line := bytes.TrimSpace(rawLine)
	if len(line) == 0 {
		return nil, nil
	}

	payload := line
	if p, ok := shared.SSEDataPayload(line); ok {
		payload = p
	}

	payload = bytes.TrimSpace(payload)
	if len(payload) == 0 {
		return nil, nil
	}

	if bytes.Equal(payload, []byte("[DONE]")) {
		return st.finish(modelName), nil
	}

	root, err := shared.DecodeJSONMap(payload)
	if err != nil {
		return nil, nil
	}

	if usage, ok := root["usage"].(map[string]any); ok {
		st.usagePrompt = int64(shared.IntFromAny(usage["prompt_tokens"]))
		st.usageCompletion = int64(shared.IntFromAny(usage["completion_tokens"]))
		st.usageCached = int64(shared.IntFromAny(getNestedInt(usage, "prompt_tokens_details", "cached_tokens")))
		st.usageReasoning = int64(shared.IntFromAny(getNestedInt(usage, "output_tokens_details", "reasoning_tokens")))
		if st.usageReasoning == 0 {
			st.usageReasoning = int64(shared.IntFromAny(getNestedInt(usage, "completion_tokens_details", "reasoning_tokens")))
		}
	}

	choices, _ := root["choices"].([]any)
	if len(choices) == 0 {
		return nil, nil
	}
	c0, _ := choices[0].(map[string]any)
	if c0 == nil {
		return nil, nil
	}
	delta, _ := c0["delta"].(map[string]any)
	if delta == nil {
		return nil, nil
	}

	if !st.started {
		st.started = true
		st.responseID = strings.TrimSpace(shared.StringFromAny(root["id"]))
		if st.responseID == "" {
			st.responseID = "resp_" + shared.RandomSuffix()
		}
		st.createdAt = int64(shared.IntFromAny(root["created"]))
		if st.createdAt == 0 {
			st.createdAt = time.Now().Unix()
		}
		st.model = modelName
		if strings.TrimSpace(st.model) == "" {
			st.model = shared.StringFromAny(root["model"])
		}
		st.msgItemID = "msg_" + st.responseID + "_0"
		st.nextOutputIndex = 0

		return []string{
			st.eventResponseCreated(),
			st.eventMessageItemAdded(),
			st.eventMessageContentPartAdded(),
		}, nil
	}

	var out []string

	// content delta -> response.output_text.delta
	if content := shared.StringFromAny(delta["content"]); content != "" {
		out = append(out, st.eventOutputTextDelta(content))
		st.textBuf.WriteString(content)
	}

	// tool_calls -> convert to response.function_call_arguments.delta
	if tcsAny, ok := delta["tool_calls"]; ok {
		if tcs, ok := tcsAny.([]any); ok {
			// Ensure message is closed before tool call output items (match common Responses ordering).
			if !st.msgDone {
				out = append(out, st.eventCloseMessageItem())
				st.msgDone = true
			}
			for _, tcRaw := range tcs {
				tc, _ := tcRaw.(map[string]any)
				if tc == nil {
					continue
				}
				tcIndex := shared.IntFromAny(tc["index"])
				call := st.toolCalls[tcIndex]
				if call == nil {
					call = &toolCallState{
						outputIndex: st.nextOutputIndex + 1,
					}
					st.nextOutputIndex = call.outputIndex
					st.toolCalls[tcIndex] = call
				}

				if id := shared.StringFromAny(tc["id"]); strings.TrimSpace(id) != "" {
					call.callID = id
				}
				function, _ := tc["function"].(map[string]any)
				if function != nil {
					if name := shared.StringFromAny(function["name"]); strings.TrimSpace(name) != "" {
						call.name = name
					}
					if args := shared.StringFromAny(function["arguments"]); args != "" {
						call.args.WriteString(args)
						call.hasArgs = true
					}
				}

				if !call.started && strings.TrimSpace(call.name) != "" {
					call.started = true
					call.itemID = fmt.Sprintf("fc_%s_%d", st.responseID, call.outputIndex)
					out = append(out, st.eventFunctionItemAdded(call))
				}
				if call.started && function != nil {
					if argsDelta := shared.StringFromAny(function["arguments"]); argsDelta != "" {
						out = append(out, st.eventFunctionArgsDelta(call, argsDelta))
					}
				}
			}
		}
	}

	finishReason := shared.StringFromAny(c0["finish_reason"])
	if strings.TrimSpace(finishReason) != "" {
		st.finishReason = finishReason
		out = append(out, st.finish(modelName)...)
	}

	return out, nil
}

func (Transformer) TransformResponseNonStream(_ context.Context, modelName string, _ []byte, _ []byte, rawJSON []byte, _ *any) ([]byte, error) {
	root, err := shared.DecodeJSONMap(rawJSON)
	if err != nil {
		return nil, err
	}

	id := strings.TrimSpace(shared.StringFromAny(root["id"]))
	if id == "" {
		id = "resp_" + shared.RandomSuffix()
	}
	model := strings.TrimSpace(modelName)
	if model == "" {
		model = strings.TrimSpace(shared.StringFromAny(root["model"]))
	}
	createdAt := int64(shared.IntFromAny(root["created"]))
	if createdAt == 0 {
		createdAt = time.Now().Unix()
	}

	var contentText string
	var toolCalls []map[string]any
	var finishReason string

	if choices, ok := root["choices"].([]any); ok && len(choices) > 0 {
		c0, _ := choices[0].(map[string]any)
		finishReason = shared.StringFromAny(c0["finish_reason"])
		if msg, ok := c0["message"].(map[string]any); ok {
			contentText = shared.StringFromAny(msg["content"])
			if tcAny, ok := msg["tool_calls"]; ok {
				if tcArr, ok := tcAny.([]any); ok {
					for _, tcRaw := range tcArr {
						tc, _ := tcRaw.(map[string]any)
						if tc == nil {
							continue
						}
						toolCalls = append(toolCalls, tc)
					}
				}
			}
		}
	}

	usage := map[string]any{}
	if u, ok := root["usage"].(map[string]any); ok {
		usage = u
	}

	output := make([]any, 0)

	// message output item
	msgItem := map[string]any{
		"id":     "msg_" + id + "_0",
		"type":   "message",
		"status": "completed",
		"role":   "assistant",
		"content": []any{
			map[string]any{
				"type":        "output_text",
				"annotations": []any{},
				"logprobs":    []any{},
				"text":        contentText,
			},
		},
	}
	output = append(output, msgItem)

	for i, tc := range toolCalls {
		callID := strings.TrimSpace(shared.StringFromAny(tc["id"]))
		fn, _ := tc["function"].(map[string]any)
		name := ""
		args := "{}"
		if fn != nil {
			name = strings.TrimSpace(shared.StringFromAny(fn["name"]))
			if a := strings.TrimSpace(shared.StringFromAny(fn["arguments"])); a != "" {
				args = a
			}
		}
		output = append(output, map[string]any{
			"id":        fmt.Sprintf("fc_%s_%d", id, i),
			"type":      "function_call",
			"status":    "completed",
			"call_id":   callID,
			"name":      name,
			"arguments": args,
		})
	}

	resp := map[string]any{
		"id":                 id,
		"object":             "response",
		"created_at":         createdAt,
		"status":             "completed",
		"error":              nil,
		"output":             output,
		"incomplete_details": nil,
		"model":              model,
		"usage": map[string]any{
			"input_tokens":            shared.IntFromAny(usage["prompt_tokens"]),
			"output_tokens":           shared.IntFromAny(usage["completion_tokens"]),
			"cache_read_input_tokens": shared.IntFromAny(getNestedInt(usage, "prompt_tokens_details", "cached_tokens")),
			"reasoning_tokens":        shared.IntFromAny(getNestedInt(usage, "completion_tokens_details", "reasoning_tokens")),
		},
	}

	_ = finishReason // kept for future mapping
	return json.Marshal(resp)
}

type chatToResponsesState struct {
	seq             int
	started         bool
	responseID      string
	createdAt       int64
	model           string
	nextOutputIndex int

	msgItemID string
	msgDone   bool
	textBuf   strings.Builder

	toolCalls map[int]*toolCallState

	finishReason string
	finished     bool

	usagePrompt     int64
	usageCompletion int64
	usageCached     int64
	usageReasoning  int64
}

type toolCallState struct {
	started     bool
	itemID      string
	outputIndex int
	callID      string
	name        string
	args        strings.Builder
	hasArgs     bool
	done        bool
}

func (s *chatToResponsesState) nextSeq() int {
	s.seq++
	return s.seq
}

func (s *chatToResponsesState) eventResponseCreated() string {
	payload := map[string]any{
		"type":            "response.created",
		"sequence_number": s.nextSeq(),
		"response": map[string]any{
			"id":         s.responseID,
			"object":     "response",
			"created_at": s.createdAt,
			"status":     "in_progress",
			"error":      nil,
			"output":     []any{},
		},
	}
	return shared.SSEEvent("response.created", payload)
}

func (s *chatToResponsesState) eventMessageItemAdded() string {
	payload := map[string]any{
		"type":            "response.output_item.added",
		"sequence_number": s.nextSeq(),
		"output_index":    0,
		"item": map[string]any{
			"id":      s.msgItemID,
			"type":    "message",
			"status":  "in_progress",
			"role":    "assistant",
			"content": []any{},
		},
	}
	return shared.SSEEvent("response.output_item.added", payload)
}

func (s *chatToResponsesState) eventMessageContentPartAdded() string {
	payload := map[string]any{
		"type":            "response.content_part.added",
		"sequence_number": s.nextSeq(),
		"item_id":         s.msgItemID,
		"output_index":    0,
		"content_index":   0,
		"part": map[string]any{
			"type":        "output_text",
			"annotations": []any{},
			"logprobs":    []any{},
			"text":        "",
		},
	}
	return shared.SSEEvent("response.content_part.added", payload)
}

func (s *chatToResponsesState) eventOutputTextDelta(delta string) string {
	payload := map[string]any{
		"type":            "response.output_text.delta",
		"sequence_number": s.nextSeq(),
		"item_id":         s.msgItemID,
		"output_index":    0,
		"content_index":   0,
		"delta":           delta,
		"logprobs":        []any{},
	}
	return shared.SSEEvent("response.output_text.delta", payload)
}

func (s *chatToResponsesState) eventCloseMessageItem() string {
	full := s.textBuf.String()
	doneText := map[string]any{
		"type":            "response.output_text.done",
		"sequence_number": s.nextSeq(),
		"item_id":         s.msgItemID,
		"output_index":    0,
		"content_index":   0,
		"text":            full,
		"logprobs":        []any{},
	}
	partDone := map[string]any{
		"type":            "response.content_part.done",
		"sequence_number": s.nextSeq(),
		"item_id":         s.msgItemID,
		"output_index":    0,
		"content_index":   0,
		"part": map[string]any{
			"type":        "output_text",
			"annotations": []any{},
			"logprobs":    []any{},
			"text":        full,
		},
	}
	itemDone := map[string]any{
		"type":            "response.output_item.done",
		"sequence_number": s.nextSeq(),
		"output_index":    0,
		"item": map[string]any{
			"id":     s.msgItemID,
			"type":   "message",
			"status": "completed",
			"role":   "assistant",
			"content": []any{
				map[string]any{
					"type":        "output_text",
					"annotations": []any{},
					"logprobs":    []any{},
					"text":        full,
				},
			},
		},
	}
	return doneJoin(
		shared.SSEEvent("response.output_text.done", doneText),
		shared.SSEEvent("response.content_part.done", partDone),
		shared.SSEEvent("response.output_item.done", itemDone),
	)
}

func (s *chatToResponsesState) eventFunctionItemAdded(call *toolCallState) string {
	payload := map[string]any{
		"type":            "response.output_item.added",
		"sequence_number": s.nextSeq(),
		"output_index":    call.outputIndex,
		"item": map[string]any{
			"id":        call.itemID,
			"type":      "function_call",
			"status":    "in_progress",
			"call_id":   call.callID,
			"name":      call.name,
			"arguments": "",
		},
	}
	return shared.SSEEvent("response.output_item.added", payload)
}

func (s *chatToResponsesState) eventFunctionArgsDelta(call *toolCallState, delta string) string {
	payload := map[string]any{
		"type":            "response.function_call_arguments.delta",
		"sequence_number": s.nextSeq(),
		"item_id":         call.itemID,
		"output_index":    call.outputIndex,
		"delta":           delta,
	}
	return shared.SSEEvent("response.function_call_arguments.delta", payload)
}

func (s *chatToResponsesState) finish(modelName string) []string {
	if s.finished {
		return nil
	}
	s.finished = true

	var out []string

	if !s.msgDone {
		out = append(out, s.eventCloseMessageItem())
		s.msgDone = true
	}

	for _, call := range s.toolCalls {
		if call == nil || call.done || !call.started {
			continue
		}
		call.done = true
		args := call.args.String()
		if strings.TrimSpace(args) == "" && call.hasArgs {
			args = "{}"
		}
		itemDone := map[string]any{
			"type":            "response.output_item.done",
			"sequence_number": s.nextSeq(),
			"output_index":    call.outputIndex,
			"item": map[string]any{
				"id":        call.itemID,
				"type":      "function_call",
				"status":    "completed",
				"call_id":   call.callID,
				"name":      call.name,
				"arguments": args,
			},
		}
		out = append(out, shared.SSEEvent("response.output_item.done", itemDone))
	}

	model := strings.TrimSpace(modelName)
	if model == "" {
		model = s.model
	}
	completed := map[string]any{
		"type":            "response.completed",
		"sequence_number": s.nextSeq(),
		"response": map[string]any{
			"id":         s.responseID,
			"object":     "response",
			"created_at": s.createdAt,
			"status":     "completed",
			"error":      nil,
			"model":      model,
			"output":     []any{},
			"usage": map[string]any{
				"input_tokens":            s.usagePrompt,
				"output_tokens":           s.usageCompletion,
				"cache_read_input_tokens": s.usageCached,
				"reasoning_tokens":        s.usageReasoning,
			},
		},
	}
	out = append(out, shared.SSEEvent("response.completed", completed))
	return out
}

func convertResponsesToolsToChatTools(v any) []any {
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
		name := strings.TrimSpace(shared.StringFromAny(tool["name"]))
		if name == "" {
			continue
		}
		fn := map[string]any{
			"name": name,
		}
		if d := strings.TrimSpace(shared.StringFromAny(tool["description"])); d != "" {
			fn["description"] = d
		}
		if schema, ok := tool["parameters"].(map[string]any); ok && len(schema) > 0 {
			fn["parameters"] = schema
		} else {
			fn["parameters"] = map[string]any{"type": "object", "properties": map[string]any{}}
		}
		out = append(out, map[string]any{"type": "function", "function": fn})
	}
	return out
}

func doneJoin(parts ...string) string {
	var b strings.Builder
	for _, p := range parts {
		if p == "" {
			continue
		}
		b.WriteString(p)
	}
	return b.String()
}

func getNestedInt(root map[string]any, keys ...string) int {
	curr := root
	for i := 0; i < len(keys)-1; i++ {
		next, _ := curr[keys[i]].(map[string]any)
		if next == nil {
			return 0
		}
		curr = next
	}
	return shared.IntFromAny(curr[keys[len(keys)-1]])
}
