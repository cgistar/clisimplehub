package messages

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"clisimplehub/internal/transformer/shared"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

type Transformer struct{}

func (Transformer) TargetInterfaceType() string { return "claude" }

func (Transformer) TargetPath(_ bool, _ string) string { return "/v1/messages" }

func (Transformer) OutputContentType(isStreaming bool) string {
	if isStreaming {
		return "text/event-stream"
	}
	return "application/json"
}

func (Transformer) TransformRequest(modelName string, rawJSON []byte, stream bool) ([]byte, error) {
	if len(rawJSON) == 0 || !gjson.ValidBytes(rawJSON) {
		return nil, fmt.Errorf("invalid chat completions request json")
	}

	root := gjson.ParseBytes(rawJSON)
	bodyModelSuffix := parseModelSuffix(root.Get("model").String())
	modelSuffix := parseModelSuffix(modelName)

	targetModel := strings.TrimSpace(modelSuffix.modelName)
	if targetModel == "" {
		targetModel = strings.TrimSpace(bodyModelSuffix.modelName)
	}
	if targetModel == "" {
		targetModel = strings.TrimSpace(root.Get("model").String())
	}

	out := map[string]any{
		"model":      targetModel,
		"max_tokens": 32000,
		"messages":   []any{},
		"stream":     stream,
	}

	if v := root.Get("max_tokens"); v.Exists() {
		out["max_tokens"] = v.Value()
	} else if v := root.Get("max_completion_tokens"); v.Exists() {
		out["max_tokens"] = v.Value()
	}
	if v := root.Get("temperature"); v.Exists() {
		out["temperature"] = v.Value()
	} else if v := root.Get("top_p"); v.Exists() {
		out["top_p"] = v.Value()
	}

	if stop := root.Get("stop"); stop.Exists() {
		if stop.IsArray() {
			stops := make([]string, 0, len(stop.Array()))
			for _, item := range stop.Array() {
				if s := strings.TrimSpace(item.String()); s != "" {
					stops = append(stops, s)
				}
			}
			if len(stops) > 0 {
				out["stop_sequences"] = stops
			}
		} else if s := strings.TrimSpace(stop.String()); s != "" {
			out["stop_sequences"] = []string{s}
		}
	}

	applyReasoningConfig(out, root, modelSuffix, bodyModelSuffix)
	toolNameMap := buildShortNameMapFromChatTools(rawJSON)

	var systemBlocks []any
	var anthropicMessages []any

	for _, message := range root.Get("messages").Array() {
		role := strings.TrimSpace(message.Get("role").String())
		if role == "" {
			continue
		}

		switch role {
		case "system":
			systemBlocks = append(systemBlocks, collectSystemBlocks(message.Get("content"))...)
		case "tool":
			toolUseID := strings.TrimSpace(message.Get("tool_call_id").String())
			if toolUseID == "" {
				continue
			}
			toolResult := map[string]any{
				"type":        "tool_result",
				"tool_use_id": toolUseID,
				"content":     convertOpenAIToolResultContent(message.Get("content")),
			}
			anthropicMessages = append(anthropicMessages, map[string]any{
				"role":    "user",
				"content": []any{toolResult},
			})
		case "user", "assistant":
			blocks := collectMessageBlocks(message.Get("content"))
			if role == "assistant" {
				for _, tc := range message.Get("tool_calls").Array() {
					if block := convertOpenAIToolCallToClaudeBlock(tc, toolNameMap); block != nil {
						blocks = append(blocks, block)
					}
				}
			}
			if len(blocks) == 0 {
				continue
			}
			anthropicMessages = append(anthropicMessages, map[string]any{
				"role":    role,
				"content": blocks,
			})
		}
	}

	if len(systemBlocks) > 0 {
		out["system"] = systemBlocks
	}
	out["messages"] = anthropicMessages

	if tools := convertOpenAIToolsToClaudeTools(root.Get("tools"), toolNameMap); len(tools) > 0 {
		out["tools"] = tools
	}
	if toolChoice := convertOpenAIToolChoice(root.Get("tool_choice"), toolNameMap); toolChoice != nil {
		out["tool_choice"] = toolChoice
	}

	return shared.MarshalNoEscapeHTML(out)
}

func (Transformer) TransformResponseStream(_ context.Context, modelName string, originalRequestRawJSON, _ []byte, rawLine []byte, state *any) ([]string, error) {
	if state == nil {
		return nil, fmt.Errorf("nil transformer state")
	}
	if *state == nil {
		*state = &streamState{
			openAIID:                 "chatcmpl_" + shared.RandomSuffix(),
			contentBlockToToolCallIx: make(map[int]int),
		}
	}

	s := (*state).(*streamState)
	line := bytes.TrimSpace(rawLine)
	if len(line) == 0 || bytes.HasPrefix(line, []byte("event:")) || bytes.HasPrefix(line, []byte(":")) {
		return nil, nil
	}

	payload := line
	if p, ok := shared.SSEDataPayload(line); ok {
		payload = bytes.TrimSpace(p)
	}
	if len(payload) == 0 {
		return nil, nil
	}
	if bytes.Equal(payload, []byte("[DONE]")) {
		if s.done {
			return nil, nil
		}
		s.done = true
		return []string{"data: [DONE]\n\n"}, nil
	}
	if !gjson.ValidBytes(payload) {
		return nil, nil
	}

	root := gjson.ParseBytes(payload)
	reverseMap := buildReverseMapFromOriginalChatCompletions(originalRequestRawJSON)

	switch root.Get("type").String() {
	case "message_start":
		message := root.Get("message")
		s.ensureStarted(modelName, message)
		s.applyUsage(message.Get("usage"))
		start, _ := openAISSE(map[string]any{
			"id":      s.openAIID,
			"object":  "chat.completion.chunk",
			"created": s.createdAt,
			"model":   s.model,
			"choices": []any{map[string]any{
				"index": 0,
				"delta": map[string]any{"role": "assistant"},
			}},
		})
		return []string{start}, nil

	case "content_block_start":
		s.ensureStarted(modelName, gjson.Result{})
		block := root.Get("content_block")
		if block.Get("type").String() != "tool_use" {
			return nil, nil
		}
		index := int(root.Get("index").Int())
		toolIx := s.nextToolCallIx
		s.nextToolCallIx++
		s.contentBlockToToolCallIx[index] = toolIx

		name := block.Get("name").String()
		if original, ok := reverseMap[name]; ok {
			name = original
		}

		ev, _ := openAISSE(map[string]any{
			"id":      s.openAIID,
			"object":  "chat.completion.chunk",
			"created": s.createdAt,
			"model":   s.model,
			"choices": []any{map[string]any{
				"index": 0,
				"delta": map[string]any{
					"tool_calls": []any{map[string]any{
						"index": toolIx,
						"id":    block.Get("id").String(),
						"type":  "function",
						"function": map[string]any{
							"name":      name,
							"arguments": "",
						},
					}},
				},
			}},
		})
		return []string{ev}, nil

	case "content_block_delta":
		s.ensureStarted(modelName, gjson.Result{})
		delta := root.Get("delta")
		switch delta.Get("type").String() {
		case "text_delta":
			text := delta.Get("text").String()
			if text == "" {
				return nil, nil
			}
			ev, _ := openAISSE(map[string]any{
				"id":      s.openAIID,
				"object":  "chat.completion.chunk",
				"created": s.createdAt,
				"model":   s.model,
				"choices": []any{map[string]any{
					"index": 0,
					"delta": map[string]any{"content": text},
				}},
			})
			return []string{ev}, nil
		case "thinking_delta":
			thinking := delta.Get("thinking").String()
			if thinking == "" {
				return nil, nil
			}
			ev, _ := openAISSE(map[string]any{
				"id":      s.openAIID,
				"object":  "chat.completion.chunk",
				"created": s.createdAt,
				"model":   s.model,
				"choices": []any{map[string]any{
					"index": 0,
					"delta": map[string]any{"reasoning_content": thinking},
				}},
			})
			return []string{ev}, nil
		case "input_json_delta":
			index := int(root.Get("index").Int())
			toolIx, ok := s.contentBlockToToolCallIx[index]
			if !ok {
				return nil, nil
			}
			partial := delta.Get("partial_json").String()
			if partial == "" {
				return nil, nil
			}
			ev, _ := openAISSE(map[string]any{
				"id":      s.openAIID,
				"object":  "chat.completion.chunk",
				"created": s.createdAt,
				"model":   s.model,
				"choices": []any{map[string]any{
					"index": 0,
					"delta": map[string]any{
						"tool_calls": []any{map[string]any{
							"index": toolIx,
							"function": map[string]any{
								"arguments": partial,
							},
						}},
					},
				}},
			})
			return []string{ev}, nil
		}
		return nil, nil

	case "message_delta":
		s.ensureStarted(modelName, gjson.Result{})
		s.applyUsage(root.Get("usage"))
		finish := mapClaudeStopReasonToOpenAIFinishReason(root.Get("delta.stop_reason").String())
		if finish == "" && s.nextToolCallIx > 0 {
			finish = "tool_calls"
		}
		payload := map[string]any{
			"id":      s.openAIID,
			"object":  "chat.completion.chunk",
			"created": s.createdAt,
			"model":   s.model,
			"choices": []any{map[string]any{
				"index": 0,
				"delta": map[string]any{},
			}},
			"usage": map[string]any{
				"prompt_tokens":     s.promptTokens,
				"completion_tokens": s.completionTokens,
				"total_tokens":      s.promptTokens + s.completionTokens,
			},
		}
		if s.cachedTokens > 0 {
			payload["usage"].(map[string]any)["prompt_tokens_details"] = map[string]any{"cached_tokens": s.cachedTokens}
		}
		if s.reasoningTokens > 0 {
			payload["usage"].(map[string]any)["completion_tokens_details"] = map[string]any{"reasoning_tokens": s.reasoningTokens}
		}
		if finish != "" {
			payload["choices"].([]any)[0].(map[string]any)["finish_reason"] = finish
		}
		ev, _ := openAISSE(payload)
		return []string{ev}, nil

	case "message_stop":
		if s.done {
			return nil, nil
		}
		s.done = true
		return []string{"data: [DONE]\n\n"}, nil

	case "error":
		errJSON, _ := anthropicErrorJSON(root)
		if errJSON == "" {
			return nil, nil
		}
		return []string{chatSSE(errJSON)}, nil
	}

	return nil, nil
}

func (Transformer) TransformResponseNonStream(ctx context.Context, modelName string, originalRequestRawJSON, requestRawJSON, rawJSON []byte, _ *any) ([]byte, error) {
	line := bytes.TrimSpace(rawJSON)
	if gjson.ValidBytes(line) {
		root := gjson.ParseBytes(line)
		if errJSON, ok := anthropicErrorJSON(root); ok {
			return []byte(errJSON), nil
		}
		if root.Get("type").String() == "message" {
			return buildResponseFromClaudeMessage(modelName, originalRequestRawJSON, root)
		}
	}

	return buildResponseFromTranscript(ctx, modelName, originalRequestRawJSON, requestRawJSON, rawJSON)
}

type streamState struct {
	openAIID                 string
	createdAt                int64
	model                    string
	started                  bool
	done                     bool
	contentBlockToToolCallIx map[int]int
	nextToolCallIx           int
	promptTokens             int
	completionTokens         int
	cachedTokens             int
	reasoningTokens          int
}

func (s *streamState) ensureStarted(modelName string, message gjson.Result) {
	if s.started {
		return
	}
	s.started = true
	s.createdAt = time.Now().Unix()
	s.model = strings.TrimSpace(modelName)
	if s.model == "" {
		s.model = strings.TrimSpace(message.Get("model").String())
	}
}

func (s *streamState) applyUsage(usage gjson.Result) {
	if !usage.Exists() {
		return
	}
	if v := int(usage.Get("input_tokens").Int()); v > s.promptTokens {
		s.promptTokens = v
	}
	if v := int(usage.Get("output_tokens").Int()); v > s.completionTokens {
		s.completionTokens = v
	}
	if v := int(usage.Get("cache_read_input_tokens").Int()); v > s.cachedTokens {
		s.cachedTokens = v
	}
	if v := int(usage.Get("reasoning_tokens").Int()); v > s.reasoningTokens {
		s.reasoningTokens = v
	}
}

func buildResponseFromClaudeMessage(modelName string, originalRequestRawJSON []byte, message gjson.Result) ([]byte, error) {
	reverseMap := buildReverseMapFromOriginalChatCompletions(originalRequestRawJSON)
	template := `{"id":"","object":"chat.completion","created":0,"model":"","choices":[{"index":0,"message":{"role":"assistant","content":null,"reasoning_content":null,"tool_calls":null},"finish_reason":null}]}`

	if id := strings.TrimSpace(message.Get("id").String()); id != "" {
		template, _ = sjson.Set(template, "id", id)
	} else {
		template, _ = sjson.Set(template, "id", "chatcmpl_"+shared.RandomSuffix())
	}
	template, _ = sjson.Set(template, "created", time.Now().Unix())
	if m := strings.TrimSpace(message.Get("model").String()); m != "" {
		template, _ = sjson.Set(template, "model", m)
	} else {
		template, _ = sjson.Set(template, "model", modelName)
	}

	if usage := message.Get("usage"); usage.Exists() {
		promptTokens := usage.Get("input_tokens").Int()
		completionTokens := usage.Get("output_tokens").Int()
		template, _ = sjson.Set(template, "usage.prompt_tokens", promptTokens)
		template, _ = sjson.Set(template, "usage.completion_tokens", completionTokens)
		template, _ = sjson.Set(template, "usage.total_tokens", promptTokens+completionTokens)
		if cached := usage.Get("cache_read_input_tokens").Int(); cached > 0 {
			template, _ = sjson.Set(template, "usage.prompt_tokens_details.cached_tokens", cached)
		}
		if reasoning := usage.Get("reasoning_tokens").Int(); reasoning > 0 {
			template, _ = sjson.Set(template, "usage.completion_tokens_details.reasoning_tokens", reasoning)
		}
	}

	var contentBuf strings.Builder
	var reasoningBuf strings.Builder
	var toolCalls []string

	for _, block := range message.Get("content").Array() {
		switch block.Get("type").String() {
		case "text":
			contentBuf.WriteString(block.Get("text").String())
		case "thinking":
			reasoningBuf.WriteString(block.Get("thinking").String())
		case "tool_use":
			name := block.Get("name").String()
			if original, ok := reverseMap[name]; ok {
				name = original
			}
			inputRaw := strings.TrimSpace(block.Get("input").Raw)
			if inputRaw == "" {
				inputRaw = "{}"
			}
			call := `{"id":"","type":"function","function":{"name":"","arguments":"{}"}}`
			call, _ = sjson.Set(call, "id", block.Get("id").String())
			call, _ = sjson.Set(call, "function.name", name)
			call, _ = sjson.Set(call, "function.arguments", inputRaw)
			toolCalls = append(toolCalls, call)
		}
	}

	if contentBuf.Len() > 0 {
		template, _ = sjson.Set(template, "choices.0.message.content", contentBuf.String())
	}
	if reasoningBuf.Len() > 0 {
		template, _ = sjson.Set(template, "choices.0.message.reasoning_content", reasoningBuf.String())
	}
	if len(toolCalls) > 0 {
		template, _ = sjson.SetRaw(template, "choices.0.message.tool_calls", `[]`)
		for _, tc := range toolCalls {
			template, _ = sjson.SetRaw(template, "choices.0.message.tool_calls.-1", tc)
		}
	}

	finishReason := mapClaudeStopReasonToOpenAIFinishReason(message.Get("stop_reason").String())
	if finishReason == "" && len(toolCalls) > 0 {
		finishReason = "tool_calls"
	}
	if finishReason != "" {
		template, _ = sjson.Set(template, "choices.0.finish_reason", finishReason)
	}

	return []byte(template), nil
}

func buildResponseFromTranscript(ctx context.Context, modelName string, originalRequestRawJSON, requestRawJSON, rawJSON []byte) ([]byte, error) {
	var state any
	var chunks []string
	scanner := bufio.NewScanner(bytes.NewReader(rawJSON))
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	tr := Transformer{}

	for scanner.Scan() {
		line := append([]byte(nil), scanner.Bytes()...)
		outs, err := tr.TransformResponseStream(ctx, modelName, originalRequestRawJSON, requestRawJSON, line, &state)
		if err != nil {
			return nil, err
		}
		chunks = append(chunks, outs...)
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}

	return mergeChunksToNonStream(chunks)
}

func anthropicErrorJSON(root gjson.Result) (string, bool) {
	var message string
	var typ string

	switch {
	case root.Get("type").String() == "error":
		message = root.Get("error.message").String()
		typ = root.Get("error.type").String()
	case root.Get("error").Exists():
		message = root.Get("error.message").String()
		typ = root.Get("error.type").String()
	default:
		return "", false
	}

	if message == "" && typ == "" {
		return "", false
	}

	errJSON := `{"error":{"message":"","type":""}}`
	errJSON, _ = sjson.Set(errJSON, "error.message", message)
	errJSON, _ = sjson.Set(errJSON, "error.type", typ)
	return errJSON, true
}

func openAISSE(payload any) (string, error) {
	b, err := shared.MarshalNoEscapeHTML(payload)
	if err != nil {
		return "", err
	}
	return "data: " + string(b) + "\n\n", nil
}

func mapClaudeStopReasonToOpenAIFinishReason(stopReason string) string {
	switch strings.ToLower(strings.TrimSpace(stopReason)) {
	case "end_turn", "stop_sequence", "stop":
		return "stop"
	case "tool_use":
		return "tool_calls"
	case "max_tokens":
		return "length"
	default:
		return ""
	}
}

func mergeChunksToNonStream(chunks []string) ([]byte, error) {
	template := `{"id":"","object":"chat.completion","created":0,"model":"","choices":[{"index":0,"message":{"role":"assistant","content":null,"reasoning_content":null,"tool_calls":null},"finish_reason":null}]}`

	var contentBuf strings.Builder
	var reasoningBuf strings.Builder
	var toolCalls []string
	var finishReason string

	for _, chunk := range chunks {
		data := strings.TrimSpace(chunk)
		if strings.HasPrefix(data, "data: ") {
			data = strings.TrimSpace(strings.TrimPrefix(data, "data: "))
		}
		if data == "" || data == "[DONE]" || !gjson.Valid(data) {
			continue
		}

		root := gjson.Parse(data)
		if root.Get("error").Exists() {
			return []byte(data), nil
		}
		if id := root.Get("id").String(); id != "" {
			template, _ = sjson.Set(template, "id", id)
		}
		if model := root.Get("model").String(); model != "" {
			template, _ = sjson.Set(template, "model", model)
		}
		if created := root.Get("created").Int(); created > 0 {
			template, _ = sjson.Set(template, "created", created)
		}

		if usage := root.Get("usage"); usage.Exists() {
			if v := usage.Get("prompt_tokens"); v.Exists() {
				template, _ = sjson.Set(template, "usage.prompt_tokens", v.Int())
			}
			if v := usage.Get("completion_tokens"); v.Exists() {
				template, _ = sjson.Set(template, "usage.completion_tokens", v.Int())
			}
			if v := usage.Get("total_tokens"); v.Exists() {
				template, _ = sjson.Set(template, "usage.total_tokens", v.Int())
			}
			if v := usage.Get("prompt_tokens_details.cached_tokens"); v.Exists() {
				template, _ = sjson.Set(template, "usage.prompt_tokens_details.cached_tokens", v.Int())
			}
			if v := usage.Get("completion_tokens_details.reasoning_tokens"); v.Exists() {
				template, _ = sjson.Set(template, "usage.completion_tokens_details.reasoning_tokens", v.Int())
			}
		}

		delta := root.Get("choices.0.delta")
		if delta.Exists() {
			if v := delta.Get("content"); v.Exists() && v.Type == gjson.String {
				contentBuf.WriteString(v.String())
			}
			if v := delta.Get("reasoning_content"); v.Exists() && v.Type == gjson.String {
				reasoningBuf.WriteString(v.String())
			}
			if tcs := delta.Get("tool_calls"); tcs.Exists() && tcs.IsArray() {
				for _, tc := range tcs.Array() {
					idx := int(tc.Get("index").Int())
					for len(toolCalls) <= idx {
						toolCalls = append(toolCalls, `{"id":"","type":"function","function":{"name":"","arguments":""}}`)
					}
					entry := toolCalls[idx]
					if tc.Get("id").Exists() {
						entry, _ = sjson.Set(entry, "id", tc.Get("id").String())
					}
					if tc.Get("function.name").Exists() {
						entry, _ = sjson.Set(entry, "function.name", tc.Get("function.name").String())
					}
					if args := tc.Get("function.arguments"); args.Exists() && args.String() != "" {
						existing := gjson.Get(entry, "function.arguments").String()
						entry, _ = sjson.Set(entry, "function.arguments", existing+args.String())
					}
					toolCalls[idx] = entry
				}
			}
		}

		if fr := root.Get("choices.0.finish_reason"); fr.Exists() && fr.Type == gjson.String {
			finishReason = fr.String()
		}
	}

	if contentBuf.Len() > 0 {
		template, _ = sjson.Set(template, "choices.0.message.content", contentBuf.String())
	}
	if reasoningBuf.Len() > 0 {
		template, _ = sjson.Set(template, "choices.0.message.reasoning_content", reasoningBuf.String())
	}
	if len(toolCalls) > 0 {
		template, _ = sjson.SetRaw(template, "choices.0.message.tool_calls", `[]`)
		for _, tc := range toolCalls {
			template, _ = sjson.SetRaw(template, "choices.0.message.tool_calls.-1", tc)
		}
	}
	if finishReason != "" {
		template, _ = sjson.Set(template, "choices.0.finish_reason", finishReason)
	}

	return []byte(template), nil
}

func chatSSE(jsonStr string) string {
	return "data: " + jsonStr + "\n\n"
}

func applyReasoningConfig(out map[string]any, root gjson.Result, modelSuffix, bodyModelSuffix modelSuffixResult) {
	effort := strings.ToLower(strings.TrimSpace(root.Get("reasoning_effort").String()))
	if effort == "" {
		if parsed, ok := parseEffortSuffix(modelSuffix.rawSuffix); ok {
			effort = parsed
		} else if parsed, ok := parseEffortSuffix(bodyModelSuffix.rawSuffix); ok {
			effort = parsed
		}
	}
	if effort == "" {
		return
	}

	switch effort {
	case "none":
		out["thinking"] = map[string]any{"type": "disabled"}
	case "auto":
		out["thinking"] = map[string]any{"type": "adaptive"}
	default:
		out["thinking"] = map[string]any{"type": "adaptive"}
		out["output_config"] = map[string]any{"effort": effort}
	}
}

func collectSystemBlocks(content gjson.Result) []any {
	var blocks []any
	switch {
	case content.Type == gjson.String:
		if text := strings.TrimSpace(content.String()); text != "" {
			blocks = append(blocks, map[string]any{"type": "text", "text": text})
		}
	case content.IsArray():
		for _, part := range content.Array() {
			switch {
			case part.Type == gjson.String:
				if text := strings.TrimSpace(part.String()); text != "" {
					blocks = append(blocks, map[string]any{"type": "text", "text": text})
				}
			case part.Get("type").String() == "text":
				if text := strings.TrimSpace(part.Get("text").String()); text != "" {
					blocks = append(blocks, map[string]any{"type": "text", "text": text})
				}
			}
		}
	}
	return blocks
}

func collectMessageBlocks(content gjson.Result) []any {
	var blocks []any
	switch {
	case content.Type == gjson.String:
		if text := strings.TrimSpace(content.String()); text != "" {
			blocks = append(blocks, map[string]any{"type": "text", "text": text})
		}
	case content.IsArray():
		for _, part := range content.Array() {
			if part.Type == gjson.String {
				if text := strings.TrimSpace(part.String()); text != "" {
					blocks = append(blocks, map[string]any{"type": "text", "text": text})
				}
				continue
			}
			if block := convertOpenAIContentPartToClaudeBlock(part); block != nil {
				blocks = append(blocks, block)
			}
		}
	}
	return blocks
}

func convertOpenAIToolsToClaudeTools(tools gjson.Result, toolNameMap map[string]string) []any {
	if !tools.IsArray() {
		return nil
	}
	var out []any
	for _, tool := range tools.Array() {
		if tool.Get("type").String() != "function" {
			continue
		}
		name := tool.Get("function.name").String()
		if short, ok := toolNameMap[name]; ok {
			name = short
		} else {
			name = shortenNameIfNeeded(name)
		}
		if strings.TrimSpace(name) == "" {
			continue
		}
		item := map[string]any{
			"name":         name,
			"input_schema": map[string]any{"type": "object", "properties": map[string]any{}},
		}
		if desc := strings.TrimSpace(tool.Get("function.description").String()); desc != "" {
			item["description"] = desc
		}
		switch {
		case tool.Get("function.parameters").Exists():
			var schema map[string]any
			_ = json.Unmarshal([]byte(tool.Get("function.parameters").Raw), &schema)
			if schema != nil {
				item["input_schema"] = schema
			}
		case tool.Get("function.parametersJsonSchema").Exists():
			var schema map[string]any
			_ = json.Unmarshal([]byte(tool.Get("function.parametersJsonSchema").Raw), &schema)
			if schema != nil {
				item["input_schema"] = schema
			}
		}
		out = append(out, item)
	}
	return out
}

func convertOpenAIToolChoice(toolChoice gjson.Result, toolNameMap map[string]string) any {
	if !toolChoice.Exists() {
		return nil
	}
	switch toolChoice.Type {
	case gjson.String:
		switch toolChoice.String() {
		case "auto":
			return map[string]any{"type": "auto"}
		case "required":
			return map[string]any{"type": "any"}
		default:
			return nil
		}
	case gjson.JSON:
		if toolChoice.Get("type").String() != "function" {
			return nil
		}
		name := toolChoice.Get("function.name").String()
		if short, ok := toolNameMap[name]; ok {
			name = short
		} else {
			name = shortenNameIfNeeded(name)
		}
		if name == "" {
			return nil
		}
		return map[string]any{"type": "tool", "name": name}
	default:
		return nil
	}
}

func convertOpenAIContentPartToClaudeBlock(part gjson.Result) any {
	switch part.Get("type").String() {
	case "text":
		if text := part.Get("text").String(); strings.TrimSpace(text) != "" {
			return map[string]any{"type": "text", "text": text}
		}
	case "image_url":
		return convertOpenAIImageURLToClaudeBlock(part.Get("image_url.url").String())
	case "file":
		fileData := part.Get("file.file_data").String()
		if strings.HasPrefix(fileData, "data:") {
			semi := strings.Index(fileData, ";")
			comma := strings.Index(fileData, ",")
			if semi != -1 && comma != -1 && comma > semi {
				mediaType := strings.TrimPrefix(fileData[:semi], "data:")
				data := fileData[comma+1:]
				return map[string]any{
					"type": "document",
					"source": map[string]any{
						"type":       "base64",
						"media_type": mediaType,
						"data":       data,
					},
				}
			}
		}
	}
	return nil
}

func convertOpenAIImageURLToClaudeBlock(imageURL string) any {
	if imageURL == "" {
		return nil
	}
	if strings.HasPrefix(imageURL, "data:") {
		parts := strings.SplitN(imageURL, ",", 2)
		if len(parts) != 2 {
			return nil
		}
		mediaType := strings.TrimPrefix(strings.SplitN(parts[0], ";", 2)[0], "data:")
		if mediaType == "" {
			mediaType = "application/octet-stream"
		}
		return map[string]any{
			"type": "image",
			"source": map[string]any{
				"type":       "base64",
				"media_type": mediaType,
				"data":       parts[1],
			},
		}
	}
	return map[string]any{
		"type": "image",
		"source": map[string]any{
			"type": "url",
			"url":  imageURL,
		},
	}
}

func convertOpenAIToolCallToClaudeBlock(toolCall gjson.Result, toolNameMap map[string]string) any {
	if toolCall.Get("type").String() != "function" {
		return nil
	}
	name := toolCall.Get("function.name").String()
	if short, ok := toolNameMap[name]; ok {
		name = short
	} else {
		name = shortenNameIfNeeded(name)
	}
	if strings.TrimSpace(name) == "" {
		return nil
	}

	callID := strings.TrimSpace(toolCall.Get("id").String())
	if callID == "" {
		callID = "toolu_" + shared.RandomSuffix()
	}

	input := map[string]any{}
	args := strings.TrimSpace(toolCall.Get("function.arguments").String())
	if args != "" && gjson.Valid(args) {
		_ = json.Unmarshal([]byte(args), &input)
	}

	return map[string]any{
		"type":  "tool_use",
		"id":    callID,
		"name":  name,
		"input": input,
	}
}

func convertOpenAIToolResultContent(content gjson.Result) any {
	if !content.Exists() {
		return ""
	}
	if content.Type == gjson.String {
		return content.String()
	}
	if content.IsArray() {
		var blocks []any
		for _, part := range content.Array() {
			if part.Type == gjson.String {
				blocks = append(blocks, map[string]any{"type": "text", "text": part.String()})
				continue
			}
			if block := convertOpenAIContentPartToClaudeBlock(part); block != nil {
				blocks = append(blocks, block)
			}
		}
		if len(blocks) > 0 || len(content.Array()) == 0 {
			return blocks
		}
		return content.Value()
	}
	if content.IsObject() {
		if block := convertOpenAIContentPartToClaudeBlock(content); block != nil {
			return []any{block}
		}
		return content.Value()
	}
	return content.Value()
}

type modelSuffixResult struct {
	modelName string
	hasSuffix bool
	rawSuffix string
}

func parseModelSuffix(model string) modelSuffixResult {
	model = strings.TrimSpace(model)
	if model == "" {
		return modelSuffixResult{}
	}
	lastOpen := strings.LastIndex(model, "(")
	if lastOpen == -1 || !strings.HasSuffix(model, ")") {
		return modelSuffixResult{modelName: model}
	}
	return modelSuffixResult{
		modelName: strings.TrimSpace(model[:lastOpen]),
		hasSuffix: true,
		rawSuffix: strings.ToLower(strings.TrimSpace(model[lastOpen+1 : len(model)-1])),
	}
}

func parseEffortSuffix(raw string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "minimal", "low", "medium", "high", "xhigh", "max", "none", "auto":
		return strings.ToLower(strings.TrimSpace(raw)), true
	default:
		return "", false
	}
}

func shortenNameIfNeeded(name string) string {
	const limit = 64
	if len(name) <= limit {
		return name
	}
	if strings.HasPrefix(name, "mcp__") {
		idx := strings.LastIndex(name, "__")
		if idx > 0 {
			candidate := "mcp__" + name[idx+2:]
			if len(candidate) > limit {
				return candidate[:limit]
			}
			return candidate
		}
	}
	return name[:limit]
}

func buildShortNameMap(names []string) map[string]string {
	const limit = 64
	used := map[string]struct{}{}
	out := map[string]string{}

	baseCandidate := func(name string) string {
		if len(name) <= limit {
			return name
		}
		if strings.HasPrefix(name, "mcp__") {
			idx := strings.LastIndex(name, "__")
			if idx > 0 {
				candidate := "mcp__" + name[idx+2:]
				if len(candidate) > limit {
					candidate = candidate[:limit]
				}
				return candidate
			}
		}
		return name[:limit]
	}

	makeUnique := func(candidate string) string {
		if _, ok := used[candidate]; !ok {
			return candidate
		}
		base := candidate
		for i := 1; ; i++ {
			suffix := "_" + strconv.Itoa(i)
			allowed := limit - len(suffix)
			if allowed < 0 {
				allowed = 0
			}
			tmp := base
			if len(tmp) > allowed {
				tmp = tmp[:allowed]
			}
			tmp += suffix
			if _, ok := used[tmp]; !ok {
				return tmp
			}
		}
	}

	for _, name := range names {
		unique := makeUnique(baseCandidate(name))
		used[unique] = struct{}{}
		out[name] = unique
	}
	return out
}

func buildShortNameMapFromChatTools(rawJSON []byte) map[string]string {
	tools := gjson.GetBytes(rawJSON, "tools")
	if !tools.IsArray() || len(tools.Array()) == 0 {
		return map[string]string{}
	}
	names := make([]string, 0, len(tools.Array()))
	for _, tool := range tools.Array() {
		if tool.Get("type").String() == "function" {
			if name := tool.Get("function.name").String(); name != "" {
				names = append(names, name)
			}
		}
	}
	if len(names) == 0 {
		return map[string]string{}
	}
	return buildShortNameMap(names)
}

func buildReverseMapFromOriginalChatCompletions(original []byte) map[string]string {
	shortMap := buildShortNameMapFromChatTools(original)
	reverse := make(map[string]string, len(shortMap))
	for originalName, shortName := range shortMap {
		reverse[shortName] = originalName
	}
	return reverse
}
