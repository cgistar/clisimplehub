package chat_completions

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"clisimplehub/internal/transformer/shared"

	"github.com/tidwall/gjson"
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

	out := map[string]any{
		"model":    modelName,
		"messages": []any{},
		"stream":   stream,
	}

	if v, ok := root["max_output_tokens"]; ok {
		out["max_tokens"] = v
	}
	if v, ok := root["parallel_tool_calls"]; ok {
		out["parallel_tool_calls"] = v
	}
	if v, ok := root["tool_choice"]; ok {
		out["tool_choice"] = v
	}
	if effort := resolveResponsesReasoningEffort(root); effort != "" {
		out["reasoning_effort"] = effort
	}

	msgs := out["messages"].([]any)
	if instructions := shared.StringFromAny(root["instructions"]); instructions != "" {
		msgs = append(msgs, map[string]any{
			"role":    "system",
			"content": instructions,
		})
	}

	switch input := root["input"].(type) {
	case string:
		if strings.TrimSpace(input) != "" {
			msgs = append(msgs, map[string]any{
				"role":    "user",
				"content": input,
			})
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
				if role == "developer" {
					role = "user"
				}

				message := map[string]any{"role": role}
				content, ok := convertResponsesMessageContentToChat(item["content"])
				if !ok {
					continue
				}
				message["content"] = content
				msgs = append(msgs, message)

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
				if callID == "" {
					continue
				}
				msgs = append(msgs, map[string]any{
					"role":         "tool",
					"tool_call_id": callID,
					"content":      shared.StringFromAny(item["output"]),
				})
			}
		}
	}

	out["messages"] = msgs
	if tools := convertResponsesToolsToChatTools(root["tools"]); len(tools) > 0 {
		out["tools"] = tools
	}

	return shared.MarshalNoEscapeHTML(out)
}

func (Transformer) TransformResponseStream(_ context.Context, modelName string, originalRequestRawJSON, requestRawJSON, rawLine []byte, state *any) ([]string, error) {
	if state == nil {
		return nil, fmt.Errorf("nil transformer state")
	}
	if *state == nil {
		*state = &chatToResponsesState{
			msgTextBuf:      make(map[int]*strings.Builder),
			msgItemAdded:    make(map[int]bool),
			msgContentAdded: make(map[int]bool),
			msgItemDone:     make(map[int]bool),
			funcArgsBuf:     make(map[int]*strings.Builder),
			funcNames:       make(map[int]string),
			funcCallIDs:     make(map[int]string),
			funcArgsDone:    make(map[int]bool),
			funcItemDone:    make(map[int]bool),
			reasonings:      make([]responsesReasoning, 0),
		}
	}
	st := (*state).(*chatToResponsesState)

	if rawLine == nil {
		return st.finish(modelName, originalRequestRawJSON, requestRawJSON), nil
	}

	line := bytes.TrimSpace(rawLine)
	if len(line) == 0 || bytes.HasPrefix(line, []byte("event:")) || bytes.HasPrefix(line, []byte(":")) {
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
		return st.finish(modelName, originalRequestRawJSON, requestRawJSON), nil
	}
	if !gjson.ValidBytes(payload) {
		return nil, nil
	}

	root := gjson.ParseBytes(payload)
	obj := root.Get("object").String()
	if obj != "" && obj != "chat.completion.chunk" {
		return nil, nil
	}
	if !root.Get("choices").Exists() || !root.Get("choices").IsArray() {
		return nil, nil
	}

	st.applyUsage(root.Get("usage"))

	var out []string
	if !st.started {
		out = append(out, st.start(root, modelName)...)
	}

	root.Get("choices").ForEach(func(_, choice gjson.Result) bool {
		idx := int(choice.Get("index").Int())
		delta := choice.Get("delta")
		if delta.Exists() {
			if content := delta.Get("content").String(); content != "" {
				if st.reasoningID != "" {
					out = append(out, st.stopReasoning()...)
				}
				out = append(out, st.ensureMessageOpened(idx)...)
				out = append(out, st.eventOutputTextDelta(idx, content))
				st.msgText(idx).WriteString(content)
			}

			if reasoning := delta.Get("reasoning_content").String(); reasoning != "" {
				out = append(out, st.ensureReasoningOpened(idx)...)
				st.reasoningBuf.WriteString(reasoning)
				out = append(out, st.eventReasoningDelta(reasoning))
			}

			if tcs := delta.Get("tool_calls"); tcs.Exists() && tcs.IsArray() {
				if st.reasoningID != "" {
					out = append(out, st.stopReasoning()...)
				}
				if st.msgItemAdded[idx] && !st.msgItemDone[idx] {
					out = append(out, st.closeMessage(idx)...)
				}
				tcs.ForEach(func(_, tc gjson.Result) bool {
					nameChunk := tc.Get("function.name").String()
					if nameChunk != "" {
						st.funcNames[idx] = nameChunk
					}
					newCallID := tc.Get("id").String()
					if st.funcCallIDs[idx] == "" && newCallID != "" {
						st.funcCallIDs[idx] = newCallID
						out = append(out, st.eventFunctionItemAdded(idx))
					}
					if st.funcArgsBuf[idx] == nil {
						st.funcArgsBuf[idx] = &strings.Builder{}
					}
					argsChunk := tc.Get("function.arguments").String()
					if argsChunk != "" {
						callID := st.funcCallIDs[idx]
						if callID == "" {
							callID = newCallID
						}
						if callID != "" {
							out = append(out, st.eventFunctionArgsDelta(idx, callID, argsChunk))
						}
						st.funcArgsBuf[idx].WriteString(argsChunk)
					}
					return true
				})
			}
		}

		if finishReason := strings.TrimSpace(choice.Get("finish_reason").String()); finishReason != "" {
			st.finishReason = finishReason
			out = append(out, st.finish(modelName, originalRequestRawJSON, requestRawJSON)...)
		}
		return true
	})

	return out, nil
}

func (Transformer) TransformResponseNonStream(ctx context.Context, modelName string, originalRequestRawJSON, requestRawJSON, rawJSON []byte, _ *any) ([]byte, error) {
	line := bytes.TrimSpace(rawJSON)
	if gjson.ValidBytes(line) {
		root := gjson.ParseBytes(line)
		switch {
		case root.Get("type").String() == "response.completed" && root.Get("response").Exists():
			return normalizeExistingResponseObject(root.Get("response"), modelName, originalRequestRawJSON, requestRawJSON)
		case root.IsObject() && (root.Get("output").Exists() || root.Get("usage").Exists()) && root.Get("choices").Type == gjson.Null:
			return normalizeExistingResponseObject(root, modelName, originalRequestRawJSON, requestRawJSON)
		case root.Get("choices").Exists():
			return buildResponseFromChatCompletionObject(root, modelName, originalRequestRawJSON, requestRawJSON)
		}
	}

	return buildResponseFromTranscript(ctx, modelName, originalRequestRawJSON, requestRawJSON, rawJSON)
}

type responsesReasoning struct {
	ReasoningID string
	Text        string
	OutputIndex int
}

type chatToResponsesState struct {
	seq          int
	started      bool
	finished     bool
	responseID   string
	createdAt    int64
	model        string
	finishReason string

	msgTextBuf      map[int]*strings.Builder
	msgItemAdded    map[int]bool
	msgContentAdded map[int]bool
	msgItemDone     map[int]bool

	reasoningID    string
	reasoningIndex int
	reasoningBuf   strings.Builder
	reasonings     []responsesReasoning

	funcArgsBuf         map[int]*strings.Builder
	funcNames           map[int]string
	funcCallIDs         map[int]string
	funcArgsDone        map[int]bool
	funcItemDone        map[int]bool
	outputItemsOverride []any

	promptTokens     int64
	cachedTokens     int64
	completionTokens int64
	totalTokens      int64
	reasoningTokens  int64
	usageSeen        bool
}

func (s *chatToResponsesState) nextSeq() int {
	s.seq++
	return s.seq
}

func (s *chatToResponsesState) start(root gjson.Result, modelName string) []string {
	s.started = true
	s.responseID = root.Get("id").String()
	if s.responseID == "" {
		s.responseID = "resp_" + shared.RandomSuffix()
	}
	s.createdAt = root.Get("created").Int()
	if s.createdAt == 0 {
		s.createdAt = time.Now().Unix()
	}
	s.model = modelName
	if s.model == "" {
		s.model = root.Get("model").String()
	}

	response := map[string]any{
		"id":         s.responseID,
		"object":     "response",
		"created_at": s.createdAt,
		"status":     "in_progress",
		"background": false,
		"error":      nil,
		"output":     []any{},
	}

	return []string{
		shared.SSEEvent("response.created", map[string]any{
			"type":            "response.created",
			"sequence_number": s.nextSeq(),
			"response":        response,
		}),
		shared.SSEEvent("response.in_progress", map[string]any{
			"type":            "response.in_progress",
			"sequence_number": s.nextSeq(),
			"response": map[string]any{
				"id":         s.responseID,
				"object":     "response",
				"created_at": s.createdAt,
				"status":     "in_progress",
			},
		}),
	}
}

func (s *chatToResponsesState) msgText(idx int) *strings.Builder {
	if s.msgTextBuf[idx] == nil {
		s.msgTextBuf[idx] = &strings.Builder{}
	}
	return s.msgTextBuf[idx]
}

func (s *chatToResponsesState) ensureMessageOpened(idx int) []string {
	var out []string
	if !s.msgItemAdded[idx] {
		out = append(out, shared.SSEEvent("response.output_item.added", map[string]any{
			"type":            "response.output_item.added",
			"sequence_number": s.nextSeq(),
			"output_index":    idx,
			"item": map[string]any{
				"id":      s.messageItemID(idx),
				"type":    "message",
				"status":  "in_progress",
				"content": []any{},
				"role":    "assistant",
			},
		}))
		s.msgItemAdded[idx] = true
	}
	if !s.msgContentAdded[idx] {
		out = append(out, shared.SSEEvent("response.content_part.added", map[string]any{
			"type":            "response.content_part.added",
			"sequence_number": s.nextSeq(),
			"item_id":         s.messageItemID(idx),
			"output_index":    idx,
			"content_index":   0,
			"part": map[string]any{
				"type":        "output_text",
				"annotations": []any{},
				"logprobs":    []any{},
				"text":        "",
			},
		}))
		s.msgContentAdded[idx] = true
	}
	return out
}

func (s *chatToResponsesState) closeMessage(idx int) []string {
	if !s.msgItemAdded[idx] || s.msgItemDone[idx] {
		return nil
	}
	fullText := ""
	if b := s.msgTextBuf[idx]; b != nil {
		fullText = b.String()
	}
	s.msgItemDone[idx] = true
	return []string{
		shared.SSEEvent("response.output_text.done", map[string]any{
			"type":            "response.output_text.done",
			"sequence_number": s.nextSeq(),
			"item_id":         s.messageItemID(idx),
			"output_index":    idx,
			"content_index":   0,
			"text":            fullText,
			"logprobs":        []any{},
		}),
		shared.SSEEvent("response.content_part.done", map[string]any{
			"type":            "response.content_part.done",
			"sequence_number": s.nextSeq(),
			"item_id":         s.messageItemID(idx),
			"output_index":    idx,
			"content_index":   0,
			"part": map[string]any{
				"type":        "output_text",
				"annotations": []any{},
				"logprobs":    []any{},
				"text":        fullText,
			},
		}),
		shared.SSEEvent("response.output_item.done", map[string]any{
			"type":            "response.output_item.done",
			"sequence_number": s.nextSeq(),
			"output_index":    idx,
			"item": map[string]any{
				"id":     s.messageItemID(idx),
				"type":   "message",
				"status": "completed",
				"role":   "assistant",
				"content": []any{
					map[string]any{
						"type":        "output_text",
						"annotations": []any{},
						"logprobs":    []any{},
						"text":        fullText,
					},
				},
			},
		}),
	}
}

func (s *chatToResponsesState) ensureReasoningOpened(idx int) []string {
	if s.reasoningID != "" {
		return nil
	}
	s.reasoningIndex = idx
	s.reasoningID = fmt.Sprintf("rs_%s_%d", s.responseID, idx)
	return []string{
		shared.SSEEvent("response.output_item.added", map[string]any{
			"type":            "response.output_item.added",
			"sequence_number": s.nextSeq(),
			"output_index":    idx,
			"item": map[string]any{
				"id":      s.reasoningID,
				"type":    "reasoning",
				"status":  "in_progress",
				"summary": []any{},
			},
		}),
		shared.SSEEvent("response.reasoning_summary_part.added", map[string]any{
			"type":            "response.reasoning_summary_part.added",
			"sequence_number": s.nextSeq(),
			"item_id":         s.reasoningID,
			"output_index":    idx,
			"summary_index":   0,
			"part": map[string]any{
				"type": "summary_text",
				"text": "",
			},
		}),
	}
}

func (s *chatToResponsesState) stopReasoning() []string {
	if s.reasoningID == "" {
		return nil
	}
	text := s.reasoningBuf.String()
	out := []string{
		shared.SSEEvent("response.reasoning_summary_text.done", map[string]any{
			"type":            "response.reasoning_summary_text.done",
			"sequence_number": s.nextSeq(),
			"item_id":         s.reasoningID,
			"output_index":    s.reasoningIndex,
			"summary_index":   0,
			"text":            text,
		}),
		shared.SSEEvent("response.reasoning_summary_part.done", map[string]any{
			"type":            "response.reasoning_summary_part.done",
			"sequence_number": s.nextSeq(),
			"item_id":         s.reasoningID,
			"output_index":    s.reasoningIndex,
			"summary_index":   0,
			"part": map[string]any{
				"type": "summary_text",
				"text": text,
			},
		}),
		shared.SSEEvent("response.output_item.done", map[string]any{
			"type":            "response.output_item.done",
			"sequence_number": s.nextSeq(),
			"output_index":    s.reasoningIndex,
			"item": map[string]any{
				"id":                s.reasoningID,
				"type":              "reasoning",
				"encrypted_content": "",
				"summary": []any{
					map[string]any{
						"type": "summary_text",
						"text": text,
					},
				},
			},
		}),
	}
	s.reasonings = append(s.reasonings, responsesReasoning{
		ReasoningID: s.reasoningID,
		Text:        text,
		OutputIndex: s.reasoningIndex,
	})
	s.reasoningID = ""
	s.reasoningBuf.Reset()
	s.reasoningIndex = 0
	return out
}

func (s *chatToResponsesState) eventOutputTextDelta(idx int, delta string) string {
	return shared.SSEEvent("response.output_text.delta", map[string]any{
		"type":            "response.output_text.delta",
		"sequence_number": s.nextSeq(),
		"item_id":         s.messageItemID(idx),
		"output_index":    idx,
		"content_index":   0,
		"delta":           delta,
		"logprobs":        []any{},
	})
}

func (s *chatToResponsesState) eventReasoningDelta(delta string) string {
	return shared.SSEEvent("response.reasoning_summary_text.delta", map[string]any{
		"type":            "response.reasoning_summary_text.delta",
		"sequence_number": s.nextSeq(),
		"item_id":         s.reasoningID,
		"output_index":    s.reasoningIndex,
		"summary_index":   0,
		"delta":           delta,
	})
}

func (s *chatToResponsesState) eventFunctionItemAdded(idx int) string {
	callID := s.funcCallIDs[idx]
	return shared.SSEEvent("response.output_item.added", map[string]any{
		"type":            "response.output_item.added",
		"sequence_number": s.nextSeq(),
		"output_index":    idx,
		"item": map[string]any{
			"id":        functionItemID(callID),
			"type":      "function_call",
			"status":    "in_progress",
			"arguments": "",
			"call_id":   callID,
			"name":      s.funcNames[idx],
		},
	})
}

func (s *chatToResponsesState) eventFunctionArgsDelta(idx int, callID string, delta string) string {
	return shared.SSEEvent("response.function_call_arguments.delta", map[string]any{
		"type":            "response.function_call_arguments.delta",
		"sequence_number": s.nextSeq(),
		"item_id":         functionItemID(callID),
		"output_index":    idx,
		"delta":           delta,
	})
}

func (s *chatToResponsesState) finish(modelName string, originalRequestRawJSON, requestRawJSON []byte) []string {
	if s.finished || !s.started {
		return nil
	}
	s.finished = true

	var out []string
	for _, idx := range sortedKeysBool(s.msgItemAdded) {
		out = append(out, s.closeMessage(idx)...)
	}
	if s.reasoningID != "" {
		out = append(out, s.stopReasoning()...)
	}
	for _, idx := range sortedKeysString(s.funcCallIDs) {
		callID := s.funcCallIDs[idx]
		if callID == "" || s.funcItemDone[idx] {
			continue
		}
		args := "{}"
		if buf := s.funcArgsBuf[idx]; buf != nil && buf.Len() > 0 {
			args = buf.String()
		}
		if !s.funcArgsDone[idx] {
			out = append(out, shared.SSEEvent("response.function_call_arguments.done", map[string]any{
				"type":            "response.function_call_arguments.done",
				"sequence_number": s.nextSeq(),
				"item_id":         functionItemID(callID),
				"output_index":    idx,
				"arguments":       args,
			}))
			s.funcArgsDone[idx] = true
		}
		out = append(out, shared.SSEEvent("response.output_item.done", map[string]any{
			"type":            "response.output_item.done",
			"sequence_number": s.nextSeq(),
			"output_index":    idx,
			"item": map[string]any{
				"id":        functionItemID(callID),
				"type":      "function_call",
				"status":    "completed",
				"arguments": args,
				"call_id":   callID,
				"name":      s.funcNames[idx],
			},
		}))
		s.funcItemDone[idx] = true
	}

	response := s.buildResponseObject(modelName, originalRequestRawJSON, requestRawJSON)
	out = append(out, shared.SSEEvent("response.completed", map[string]any{
		"type":            "response.completed",
		"sequence_number": s.nextSeq(),
		"response":        response,
	}))
	return out
}

func (s *chatToResponsesState) buildResponseObject(modelName string, originalRequestRawJSON, requestRawJSON []byte) map[string]any {
	outputItems := s.buildOutputItems()
	if s.outputItemsOverride != nil {
		outputItems = s.outputItemsOverride
	}
	response := map[string]any{
		"id":                 s.responseID,
		"object":             "response",
		"created_at":         s.createdAt,
		"status":             "completed",
		"background":         false,
		"error":              nil,
		"incomplete_details": nil,
		"output":             outputItems,
	}

	if model := resolveResponseModel(modelName, originalRequestRawJSON, requestRawJSON, s.model); model != "" {
		response["model"] = model
	}
	populateResponseRequestFields(response, originalRequestRawJSON, requestRawJSON)
	if _, ok := response["model"]; !ok {
		if model := resolveResponseModel(modelName, originalRequestRawJSON, requestRawJSON, s.model); model != "" {
			response["model"] = model
		}
	}
	if s.usageSeen {
		response["usage"] = s.buildUsage()
	}
	return response
}

func (s *chatToResponsesState) buildOutputItems() []any {
	outputs := make([]any, 0)
	for _, reasoning := range s.reasonings {
		item := map[string]any{
			"id":                reasoning.ReasoningID,
			"type":              "reasoning",
			"encrypted_content": "",
			"summary":           []any{},
		}
		if reasoning.Text != "" {
			item["summary"] = []any{
				map[string]any{
					"type": "summary_text",
					"text": reasoning.Text,
				},
			}
		}
		outputs = append(outputs, item)
	}

	for _, idx := range sortedKeysBool(s.msgItemAdded) {
		text := ""
		if buf := s.msgTextBuf[idx]; buf != nil {
			text = buf.String()
		}
		outputs = append(outputs, map[string]any{
			"id":     s.messageItemID(idx),
			"type":   "message",
			"status": "completed",
			"role":   "assistant",
			"content": []any{
				map[string]any{
					"type":        "output_text",
					"annotations": []any{},
					"logprobs":    []any{},
					"text":        text,
				},
			},
		})
	}

	for _, idx := range sortedKeysString(s.funcCallIDs) {
		callID := s.funcCallIDs[idx]
		if callID == "" {
			continue
		}
		args := ""
		if buf := s.funcArgsBuf[idx]; buf != nil {
			args = buf.String()
		}
		outputs = append(outputs, map[string]any{
			"id":        functionItemID(callID),
			"type":      "function_call",
			"status":    "completed",
			"arguments": args,
			"call_id":   callID,
			"name":      s.funcNames[idx],
		})
	}
	return outputs
}

func (s *chatToResponsesState) buildUsage() map[string]any {
	total := s.totalTokens
	if total == 0 {
		total = s.promptTokens + s.completionTokens
	}
	usage := map[string]any{
		"input_tokens":  s.promptTokens,
		"output_tokens": s.completionTokens,
		"total_tokens":  total,
	}
	if s.cachedTokens > 0 {
		usage["input_tokens_details"] = map[string]any{
			"cached_tokens": s.cachedTokens,
		}
	}
	if s.reasoningTokens > 0 {
		usage["output_tokens_details"] = map[string]any{
			"reasoning_tokens": s.reasoningTokens,
		}
	}
	return usage
}

func (s *chatToResponsesState) applyUsage(usage gjson.Result) {
	if !usage.Exists() || !usage.IsObject() {
		return
	}
	if v := usage.Get("prompt_tokens"); v.Exists() {
		s.promptTokens = v.Int()
		s.usageSeen = true
	}
	if v := usage.Get("prompt_tokens_details.cached_tokens"); v.Exists() {
		s.cachedTokens = v.Int()
		s.usageSeen = true
	}
	if v := usage.Get("completion_tokens"); v.Exists() {
		s.completionTokens = v.Int()
		s.usageSeen = true
	} else if v := usage.Get("output_tokens"); v.Exists() {
		s.completionTokens = v.Int()
		s.usageSeen = true
	}
	if v := usage.Get("output_tokens_details.reasoning_tokens"); v.Exists() {
		s.reasoningTokens = v.Int()
		s.usageSeen = true
	} else if v := usage.Get("completion_tokens_details.reasoning_tokens"); v.Exists() {
		s.reasoningTokens = v.Int()
		s.usageSeen = true
	}
	if v := usage.Get("total_tokens"); v.Exists() {
		s.totalTokens = v.Int()
		s.usageSeen = true
	}
}

func (s *chatToResponsesState) messageItemID(idx int) string {
	return fmt.Sprintf("msg_%s_%d", s.responseID, idx)
}

func convertResponsesMessageContentToChat(content any) (any, bool) {
	switch value := content.(type) {
	case string:
		if strings.TrimSpace(value) == "" {
			return nil, false
		}
		return value, true
	case []any:
		items := make([]any, 0, len(value))
		for _, raw := range value {
			part, _ := raw.(map[string]any)
			if part == nil {
				continue
			}
			partType := strings.TrimSpace(shared.StringFromAny(part["type"]))
			if partType == "" {
				partType = "input_text"
			}
			switch partType {
			case "input_text", "output_text":
				text := shared.StringFromAny(part["text"])
				if strings.TrimSpace(text) == "" {
					continue
				}
				items = append(items, map[string]any{
					"type": "text",
					"text": text,
				})
			case "input_image":
				imageURL := shared.StringFromAny(part["image_url"])
				if imageURL == "" {
					if imageMap, ok := part["image_url"].(map[string]any); ok {
						imageURL = shared.StringFromAny(imageMap["url"])
					}
				}
				if strings.TrimSpace(imageURL) == "" {
					continue
				}
				items = append(items, map[string]any{
					"type": "image_url",
					"image_url": map[string]any{
						"url": imageURL,
					},
				})
			}
		}
		if len(items) == 0 {
			return nil, false
		}
		return items, true
	default:
		return nil, false
	}
}

func convertResponsesToolsToChatTools(v any) []any {
	toolsArr, ok := v.([]any)
	if !ok {
		return nil
	}
	out := make([]any, 0, len(toolsArr))
	for _, raw := range toolsArr {
		tool, _ := raw.(map[string]any)
		if tool == nil {
			continue
		}
		toolType := strings.TrimSpace(shared.StringFromAny(tool["type"]))
		if toolType != "" && toolType != "function" {
			continue
		}
		name := shared.StringFromAny(tool["name"])
		if strings.TrimSpace(name) == "" {
			continue
		}
		function := map[string]any{
			"name":       name,
			"parameters": map[string]any{},
		}
		if description := shared.StringFromAny(tool["description"]); description != "" {
			function["description"] = description
		}
		if parameters, ok := tool["parameters"].(map[string]any); ok {
			function["parameters"] = parameters
		}
		out = append(out, map[string]any{
			"type":     "function",
			"function": function,
		})
	}
	return out
}

func resolveResponsesReasoningEffort(root map[string]any) string {
	if reasoning, ok := root["reasoning"].(map[string]any); ok {
		if effort := strings.ToLower(strings.TrimSpace(shared.StringFromAny(reasoning["effort"]))); effort != "" {
			return effort
		}
	}
	if effort := strings.ToLower(strings.TrimSpace(shared.StringFromAny(root["reasoning_effort"]))); effort != "" {
		return effort
	}
	return ""
}

func buildResponseFromChatCompletionObject(root gjson.Result, modelName string, originalRequestRawJSON, requestRawJSON []byte) ([]byte, error) {
	state := &chatToResponsesState{
		started:         true,
		responseID:      root.Get("id").String(),
		createdAt:       root.Get("created").Int(),
		model:           root.Get("model").String(),
		msgTextBuf:      make(map[int]*strings.Builder),
		msgItemAdded:    make(map[int]bool),
		msgContentAdded: make(map[int]bool),
		msgItemDone:     make(map[int]bool),
		funcArgsBuf:     make(map[int]*strings.Builder),
		funcNames:       make(map[int]string),
		funcCallIDs:     make(map[int]string),
		funcArgsDone:    make(map[int]bool),
		funcItemDone:    make(map[int]bool),
		reasonings:      make([]responsesReasoning, 0),
		finished:        true,
	}
	if state.responseID == "" {
		state.responseID = "resp_" + shared.RandomSuffix()
	}
	if state.createdAt == 0 {
		state.createdAt = time.Now().Unix()
	}
	if state.model == "" {
		state.model = modelName
	}

	state.applyUsage(root.Get("usage"))

	outputItems := make([]any, 0)
	includeReasoning := false
	if rc := root.Get("choices.0.message.reasoning_content").String(); rc != "" {
		includeReasoning = true
		reasoningID := fmt.Sprintf("rs_%s", trimResponsePrefix(state.responseID))
		state.reasonings = append(state.reasonings, responsesReasoning{
			ReasoningID: reasoningID,
			Text:        rc,
			OutputIndex: 0,
		})
		outputItems = append(outputItems, map[string]any{
			"id":                reasoningID,
			"type":              "reasoning",
			"encrypted_content": "",
			"summary": []any{
				map[string]any{
					"type": "summary_text",
					"text": rc,
				},
			},
		})
	} else if requestHasReasoning(originalRequestRawJSON) {
		includeReasoning = true
		reasoningID := fmt.Sprintf("rs_%s", trimResponsePrefix(state.responseID))
		state.reasonings = append(state.reasonings, responsesReasoning{
			ReasoningID: reasoningID,
			Text:        "",
			OutputIndex: 0,
		})
		outputItems = append(outputItems, map[string]any{
			"id":                reasoningID,
			"type":              "reasoning",
			"encrypted_content": "",
			"summary":           []any{},
		})
	}
	_ = includeReasoning

	if choices := root.Get("choices"); choices.Exists() && choices.IsArray() {
		choices.ForEach(func(_, choice gjson.Result) bool {
			idx := int(choice.Get("index").Int())
			msg := choice.Get("message")
			if msg.Exists() {
				if text := msg.Get("content").String(); text != "" {
					state.msgItemAdded[idx] = true
					state.msgContentAdded[idx] = true
					state.msgItemDone[idx] = true
					state.msgText(idx).WriteString(text)
					outputItems = append(outputItems, map[string]any{
						"id":     state.messageItemID(idx),
						"type":   "message",
						"status": "completed",
						"role":   "assistant",
						"content": []any{
							map[string]any{
								"type":        "output_text",
								"annotations": []any{},
								"logprobs":    []any{},
								"text":        text,
							},
						},
					})
				}
				if tcs := msg.Get("tool_calls"); tcs.Exists() && tcs.IsArray() {
					tcs.ForEach(func(_, tc gjson.Result) bool {
						callID := tc.Get("id").String()
						if callID == "" {
							return true
						}
						outputItems = append(outputItems, map[string]any{
							"id":        functionItemID(callID),
							"type":      "function_call",
							"status":    "completed",
							"arguments": tc.Get("function.arguments").String(),
							"call_id":   callID,
							"name":      tc.Get("function.name").String(),
						})
						return true
					})
				}
			}
			if finishReason := strings.TrimSpace(choice.Get("finish_reason").String()); finishReason != "" {
				state.finishReason = finishReason
			}
			return true
		})
	}
	state.outputItemsOverride = outputItems

	return shared.MarshalNoEscapeHTML(state.buildResponseObject(modelName, originalRequestRawJSON, requestRawJSON))
}

func buildResponseFromTranscript(ctx context.Context, modelName string, originalRequestRawJSON, requestRawJSON, rawJSON []byte) ([]byte, error) {
	scanner := bufio.NewScanner(bytes.NewReader(rawJSON))
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	var state any
	parsedAny := false
	for scanner.Scan() {
		line := scanner.Bytes()
		outs, err := (Transformer{}).TransformResponseStream(ctx, modelName, originalRequestRawJSON, requestRawJSON, line, &state)
		if err != nil {
			return nil, err
		}
		if len(outs) > 0 {
			parsedAny = true
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	if state == nil {
		return nil, fmt.Errorf("failed to parse chat completions transcript")
	}
	st := state.(*chatToResponsesState)
	if !parsedAny && !st.started {
		return nil, fmt.Errorf("failed to parse chat completions transcript")
	}
	_, _ = (Transformer{}).TransformResponseStream(ctx, modelName, originalRequestRawJSON, requestRawJSON, nil, &state)
	st = state.(*chatToResponsesState)
	return shared.MarshalNoEscapeHTML(st.buildResponseObject(modelName, originalRequestRawJSON, requestRawJSON))
}

func normalizeExistingResponseObject(response gjson.Result, modelName string, originalRequestRawJSON, requestRawJSON []byte) ([]byte, error) {
	if !response.Exists() || !response.IsObject() {
		return nil, fmt.Errorf("empty response object")
	}
	root, err := shared.DecodeJSONMap([]byte(response.Raw))
	if err != nil {
		return nil, err
	}
	if _, ok := root["id"]; !ok {
		root["id"] = "resp_" + shared.RandomSuffix()
	}
	if _, ok := root["created_at"]; !ok {
		root["created_at"] = time.Now().Unix()
	}
	if _, ok := root["object"]; !ok {
		root["object"] = "response"
	}
	if _, ok := root["status"]; !ok {
		root["status"] = "completed"
	}
	if _, ok := root["background"]; !ok {
		root["background"] = false
	}
	if _, ok := root["error"]; !ok {
		root["error"] = nil
	}
	if _, ok := root["incomplete_details"]; !ok {
		root["incomplete_details"] = nil
	}
	if _, ok := root["output"]; !ok {
		root["output"] = []any{}
	}
	if model := resolveResponseModel(modelName, originalRequestRawJSON, requestRawJSON, response.Get("model").String()); model != "" {
		root["model"] = model
	}
	populateResponseRequestFields(root, originalRequestRawJSON, requestRawJSON)
	return shared.MarshalNoEscapeHTML(root)
}

func populateResponseRequestFields(response map[string]any, originalRequestRawJSON, requestRawJSON []byte) {
	orig := gjson.ParseBytes(originalRequestRawJSON)
	req := gjson.ParseBytes(requestRawJSON)

	if v := firstResult(orig.Get("instructions"), gjson.Result{}); v.Exists() {
		response["instructions"] = v.String()
	}
	if v := orig.Get("max_output_tokens"); v.Exists() {
		response["max_output_tokens"] = v.Value()
	} else if v := req.Get("max_tokens"); v.Exists() {
		response["max_output_tokens"] = v.Value()
	}
	copyRequestField(response, "max_tool_calls", orig, req)
	if model := resolveResponseModel("", originalRequestRawJSON, requestRawJSON, ""); model != "" {
		response["model"] = model
	}
	copyRequestField(response, "parallel_tool_calls", orig, req)
	copyRequestField(response, "previous_response_id", orig, req)
	copyRequestField(response, "prompt_cache_key", orig, req)
	if v := orig.Get("reasoning"); v.Exists() {
		response["reasoning"] = v.Value()
	} else if v := orig.Get("reasoning_effort"); v.Exists() {
		response["reasoning"] = map[string]any{"effort": v.Value()}
	}
	copyRequestField(response, "safety_identifier", orig, req)
	copyRequestField(response, "service_tier", orig, req)
	copyRequestField(response, "store", orig, req)
	copyRequestField(response, "temperature", orig, req)
	copyRequestField(response, "text", orig, req)
	copyRequestField(response, "tool_choice", orig, req)
	copyRequestField(response, "tools", orig, req)
	copyRequestField(response, "top_logprobs", orig, req)
	copyRequestField(response, "top_p", orig, req)
	copyRequestField(response, "truncation", orig, req)
	copyRequestField(response, "user", orig, req)
	copyRequestField(response, "metadata", orig, req)
}

func copyRequestField(response map[string]any, field string, orig, req gjson.Result) {
	if v := orig.Get(field); v.Exists() {
		response[field] = v.Value()
		return
	}
	if v := req.Get(field); v.Exists() {
		response[field] = v.Value()
	}
}

func resolveResponseModel(modelName string, originalRequestRawJSON, requestRawJSON []byte, fallback string) string {
	orig := gjson.ParseBytes(originalRequestRawJSON)
	if v := strings.TrimSpace(orig.Get("model").String()); v != "" {
		return v
	}
	req := gjson.ParseBytes(requestRawJSON)
	if v := strings.TrimSpace(req.Get("model").String()); v != "" {
		return v
	}
	if v := strings.TrimSpace(modelName); v != "" {
		return v
	}
	return strings.TrimSpace(fallback)
}

func requestHasReasoning(originalRequestRawJSON []byte) bool {
	root := gjson.ParseBytes(originalRequestRawJSON)
	return root.Get("reasoning").Exists() || root.Get("reasoning_effort").Exists()
}

func trimResponsePrefix(id string) string {
	if strings.HasPrefix(id, "resp_") {
		return strings.TrimPrefix(id, "resp_")
	}
	return id
}

func functionItemID(callID string) string {
	return "fc_" + callID
}

func sortedKeysBool(m map[int]bool) []int {
	out := make([]int, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Ints(out)
	return out
}

func sortedKeysString(m map[int]string) []int {
	out := make([]int, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Ints(out)
	return out
}

func firstResult(a, b gjson.Result) gjson.Result {
	if a.Exists() {
		return a
	}
	return b
}
