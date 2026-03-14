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
	} else if v, ok := root["top_p"]; ok {
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
	out["model"] = modelName
	if effort, ok := resolveClaudeReasoningEffort(root); ok {
		out["reasoning_effort"] = effort
	}
	if user := shared.StringFromAny(root["user"]); strings.TrimSpace(user) != "" {
		out["user"] = user
	}

	openAIMessages := make([]any, 0)
	if system := buildClaudeSystemMessage(root["system"]); system != nil {
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
			var reasoningParts []string
			var toolResults []any

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
				case "thinking":
					if role == "assistant" {
						if thinking := extractClaudeThinkingText(part); strings.TrimSpace(thinking) != "" {
							reasoningParts = append(reasoningParts, thinking)
						}
					}
				case "redacted_thinking":
					continue
				case "tool_use":
					call := convertClaudeToolUseToOpenAIToolCall(role, part)
					if call != nil {
						toolCalls = append(toolCalls, call)
					}
				case "tool_result":
					toolMsg := convertClaudeToolResultToOpenAIToolMessage(part)
					if toolMsg != nil {
						toolResults = append(toolResults, toolMsg)
					}
				}
			}

			openAIMessages = append(openAIMessages, toolResults...)
			if role == "assistant" {
				if assistantMsg := buildOpenAIAssistantMessage(contentItems, strings.Join(reasoningParts, "\n\n"), toolCalls); assistantMsg != nil {
					openAIMessages = append(openAIMessages, assistantMsg)
				}
				continue
			}
			if len(contentItems) > 0 {
				openAIMessages = append(openAIMessages, map[string]any{
					"role":    role,
					"content": contentItems,
				})
			}
		}
	}

	out["messages"] = openAIMessages

	if tools := convertClaudeToolsToOpenAITools(root["tools"]); len(tools) > 0 {
		out["tools"] = tools
		if toolChoice, ok := root["tool_choice"]; ok {
			out["tool_choice"] = normalizeClaudeToolChoice(toolChoice)
		}
	}

	return json.Marshal(out)
}

func (Transformer) TransformResponseStream(_ context.Context, modelName string, originalRequestRawJSON []byte, _ []byte, rawLine []byte, state *any) ([]string, error) {
	if state == nil {
		return nil, fmt.Errorf("nil transformer state")
	}
	if *state == nil {
		*state = &openAIToClaudeStreamState{
			nextBlockIndex:     0,
			toolBlocks:         make(map[int]*toolBlock),
			textBlockIndex:     -1,
			thinkingBlockIndex: -1,
		}
	}
	s := (*state).(*openAIToClaudeStreamState)
	if s.toolNameMap == nil {
		s.toolNameMap = buildClaudeToolNameMap(originalRequestRawJSON)
	}
	if rawLine == nil {
		return s.finish(), nil
	}

	line := bytes.TrimSpace(rawLine)
	if len(line) == 0 {
		return nil, nil
	}

	payload, ok := shared.SSEDataPayload(line)
	if !ok {
		return nil, nil
	}

	if bytes.Equal(payload, []byte("[DONE]")) {
		return s.finish(), nil
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

	delta, _ := firstChoice["delta"].(map[string]any)
	deltaHasActivity := false
	outputs, deltaHasActivity = s.applyDelta(outputs, delta)
	outputs = s.applyFinishReason(outputs, shared.StringFromAny(firstChoice["finish_reason"]), root["usage"], deltaHasActivity)
	s.applyUsage(root["usage"], deltaHasActivity)
	if s.shouldEmitMessageDelta() {
		outputs = append(outputs, s.emitMessageDeltaAndStop()...)
	}
	return outputs, nil
}

func (Transformer) TransformResponseNonStream(_ context.Context, modelName string, originalRequestRawJSON []byte, _ []byte, rawJSON []byte, _ *any) ([]byte, error) {
	root, err := shared.DecodeJSONMap(rawJSON)
	if err != nil {
		return nil, err
	}
	toolNameMap := buildClaudeToolNameMap(originalRequestRawJSON)

	id := shared.StringFromAny(root["id"])
	if id == "" {
		id = "msg_" + shared.RandomSuffix()
	}
	model := modelName
	if model == "" {
		model = shared.StringFromAny(root["model"])
	}

	var finishReason string
	var usage map[string]any
	var contentBlocks []any
	var hasToolCall bool

	if u, ok := root["usage"].(map[string]any); ok {
		usage = u
	}

	choices, _ := root["choices"].([]any)
	if len(choices) > 0 {
		c0, _ := choices[0].(map[string]any)
		finishReason = shared.StringFromAny(c0["finish_reason"])
		msg, _ := c0["message"].(map[string]any)
		if msg != nil {
			contentBlocks = append(contentBlocks, buildClaudeContentBlocksFromOpenAIMessage(msg, toolNameMap)...)
			if tcAny, ok := msg["tool_calls"]; ok {
				if tcArr, ok := tcAny.([]any); ok {
					for _, tcRaw := range tcArr {
						tc, _ := tcRaw.(map[string]any)
						if tc == nil {
							continue
						}
						hasToolCall = true
						if toolUse := openAIToolCallToClaudeToolUse(tc, toolNameMap); toolUse != nil {
							contentBlocks = append(contentBlocks, toolUse)
						}
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
			"input_tokens":  maxZero(shared.IntFromAny(usage["prompt_tokens"]) - shared.IntFromAny(getNestedMapInt(usage, "prompt_tokens_details", "cached_tokens"))),
			"output_tokens": shared.IntFromAny(usage["completion_tokens"]),
		},
	}
	if cached := shared.IntFromAny(getNestedMapInt(usage, "prompt_tokens_details", "cached_tokens")); cached > 0 {
		out["usage"].(map[string]any)["cache_read_input_tokens"] = cached
	}
	if strings.TrimSpace(finishReason) == "" {
		if hasToolCall {
			out["stop_reason"] = "tool_use"
		} else {
			out["stop_reason"] = "end_turn"
		}
	}
	out["content"] = contentBlocks

	return json.Marshal(out)
}

type openAIToClaudeStreamState struct {
	started        bool
	messageID      string
	model          string
	createdAt      int64
	nextBlockIndex int

	textBlockStarted     bool
	textBlockIndex       int
	thinkingBlockStarted bool
	thinkingBlockIndex   int

	toolBlocks map[int]*toolBlock

	finishReason         string
	finishReady          bool
	sawToolCall          bool
	usageInput           int
	usageOutput          int
	usageCached          int
	usageSeen            bool
	messageDeltaSent     bool
	contentBlocksStopped bool
	finished             bool
	toolNameMap          map[string]string
}

type toolBlock struct {
	index      int
	started    bool
	blockIndex int
	id         string
	name       string
	args       strings.Builder
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
		"type":  "content_block_start",
		"index": s.textBlockIndex,
		"content_block": map[string]any{
			"type": "text",
			"text": "",
		},
	}
	return []string{shared.SSEEvent("content_block_start", ev)}
}

func (s *openAIToClaudeStreamState) ensureThinkingBlockStarted() []string {
	if s.thinkingBlockStarted {
		return nil
	}
	s.thinkingBlockStarted = true
	if s.thinkingBlockIndex < 0 {
		s.thinkingBlockIndex = s.nextBlockIndex
		s.nextBlockIndex++
	}
	ev := map[string]any{
		"type":  "content_block_start",
		"index": s.thinkingBlockIndex,
		"content_block": map[string]any{
			"type":     "thinking",
			"thinking": "",
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

func (s *openAIToClaudeStreamState) eventThinkingDelta(text string) string {
	ev := map[string]any{
		"type":  "content_block_delta",
		"index": s.thinkingBlockIndex,
		"delta": map[string]any{
			"type":     "thinking_delta",
			"thinking": text,
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
			"partial_json": fixJSON(partial),
		},
	}
	return shared.SSEEvent("content_block_delta", ev)
}

func (s *openAIToClaudeStreamState) captureUsage(v any) {
	usage, _ := v.(map[string]any)
	if usage == nil {
		return
	}
	s.usageSeen = true
	s.usageInput = maxZero(shared.IntFromAny(usage["prompt_tokens"]) - shared.IntFromAny(getNestedMapInt(usage, "prompt_tokens_details", "cached_tokens")))
	s.usageOutput = shared.IntFromAny(usage["completion_tokens"])
	s.usageCached = shared.IntFromAny(getNestedMapInt(usage, "prompt_tokens_details", "cached_tokens"))
}

func (s *openAIToClaudeStreamState) applyDelta(outputs []string, delta map[string]any) ([]string, bool) {
	if delta == nil {
		return outputs, false
	}

	deltaHasActivity := false
	for _, reasoningText := range collectOpenAIReasoningTexts(delta["reasoning_content"]) {
		if strings.TrimSpace(reasoningText) == "" {
			continue
		}
		deltaHasActivity = true
		outputs = append(outputs, s.stopTextBlock()...)
		outputs = append(outputs, s.ensureThinkingBlockStarted()...)
		outputs = append(outputs, s.eventThinkingDelta(reasoningText))
	}

	if content := shared.StringFromAny(delta["content"]); content != "" {
		deltaHasActivity = true
		outputs = append(outputs, s.stopThinkingBlock()...)
		outputs = append(outputs, s.ensureTextBlockStarted()...)
		outputs = append(outputs, s.eventTextDelta(content))
	}

	if tcAny, ok := delta["tool_calls"]; ok {
		if tcArr, ok := tcAny.([]any); ok {
			for _, tcRaw := range tcArr {
				tc, _ := tcRaw.(map[string]any)
				if tc == nil {
					continue
				}
				s.sawToolCall = true
				index := shared.IntFromAny(tc["index"])
				tb := s.toolBlocks[index]
				if tb == nil {
					tb = &toolBlock{index: index}
					s.toolBlocks[index] = tb
				}
				deltaHasActivity = true

				if id := shared.StringFromAny(tc["id"]); id != "" {
					tb.id = sanitizeClaudeToolID(id)
				}
				function, _ := tc["function"].(map[string]any)
				if function != nil {
					if name := shared.StringFromAny(function["name"]); name != "" {
						tb.name = mapToolNameFromClaudeRequest(name, s.toolNameMap)
					}
					if args := shared.StringFromAny(function["arguments"]); args != "" {
						tb.args.WriteString(args)
					}
				}

				if !tb.started && tb.id != "" && tb.name != "" {
					outputs = append(outputs, s.stopThinkingBlock()...)
					outputs = append(outputs, s.stopTextBlock()...)
					tb.started = true
					tb.blockIndex = s.nextBlockIndex
					s.nextBlockIndex++
					outputs = append(outputs, s.eventToolUseStart(tb.blockIndex, tb.id, tb.name))
				}
			}
		}
	}

	return outputs, deltaHasActivity
}

func (s *openAIToClaudeStreamState) applyFinishReason(outputs []string, finish string, usageAny any, deltaHasActivity bool) []string {
	if finish = strings.TrimSpace(finish); finish != "" {
		s.finishReason = finish
	}
	if s.finishReady || strings.TrimSpace(s.finishReason) == "" {
		return outputs
	}
	if !shouldFinalizeClaudeStreamChunk(usageAny, deltaHasActivity) {
		return outputs
	}
	s.finishReady = true
	return append(outputs, s.stopOpenBlocks()...)
}

func (s *openAIToClaudeStreamState) applyUsage(v any, deltaHasActivity bool) {
	if !shouldCaptureClaudeStreamUsage(v, deltaHasActivity, s.finishReason) {
		return
	}
	s.captureUsage(v)
}

func (s *openAIToClaudeStreamState) stopTextBlock() []string {
	if !s.textBlockStarted {
		return nil
	}
	out := []string{shared.SSEEvent("content_block_stop", map[string]any{
		"type":  "content_block_stop",
		"index": s.textBlockIndex,
	})}
	s.textBlockStarted = false
	s.textBlockIndex = -1
	return out
}

func (s *openAIToClaudeStreamState) stopThinkingBlock() []string {
	if !s.thinkingBlockStarted {
		return nil
	}
	out := []string{shared.SSEEvent("content_block_stop", map[string]any{
		"type":  "content_block_stop",
		"index": s.thinkingBlockIndex,
	})}
	s.thinkingBlockStarted = false
	s.thinkingBlockIndex = -1
	return out
}

func (s *openAIToClaudeStreamState) stopOpenBlocks() []string {
	if s.contentBlocksStopped {
		return nil
	}
	var outputs []string
	outputs = append(outputs, s.stopThinkingBlock()...)
	outputs = append(outputs, s.stopTextBlock()...)
	for _, tb := range s.toolBlocks {
		if tb == nil || !tb.started {
			continue
		}
		if tb.args.Len() > 0 {
			outputs = append(outputs, s.eventToolArgsDelta(tb.blockIndex, tb.args.String()))
		}
		outputs = append(outputs, shared.SSEEvent("content_block_stop", map[string]any{
			"type":  "content_block_stop",
			"index": tb.blockIndex,
		}))
	}
	s.contentBlocksStopped = true
	return outputs
}

func (s *openAIToClaudeStreamState) shouldEmitMessageDelta() bool {
	return s.finishReady && s.usageSeen && !s.messageDeltaSent
}

func (s *openAIToClaudeStreamState) emitMessageDeltaAndStop() []string {
	if s.messageDeltaSent {
		return nil
	}
	stopReason := s.finishReason
	if s.sawToolCall {
		stopReason = "tool_calls"
	}
	usage := map[string]any{
		"input_tokens":  s.usageInput,
		"output_tokens": s.usageOutput,
	}
	if s.usageCached > 0 {
		usage["cache_read_input_tokens"] = s.usageCached
	}
	s.messageDeltaSent = true
	return []string{
		shared.SSEEvent("message_delta", map[string]any{
			"type": "message_delta",
			"delta": map[string]any{
				"stop_reason":   mapOpenAIFinishReasonToClaudeStopReason(stopReason),
				"stop_sequence": nil,
			},
			"usage": usage,
		}),
		"event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n",
	}
}

func (s *openAIToClaudeStreamState) finish() []string {
	if s.finished {
		return nil
	}
	s.finished = true

	var outputs []string
	outputs = append(outputs, s.stopOpenBlocks()...)
	if s.finishReason != "" && !s.finishReady {
		s.finishReady = true
	}
	if s.finishReady && !s.messageDeltaSent {
		outputs = append(outputs, s.emitMessageDeltaAndStop()...)
	}
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

func openAIToolCallToClaudeToolUse(tc map[string]any, toolNameMap map[string]string) any {
	if tc == nil {
		return nil
	}
	callID := shared.StringFromAny(tc["id"])
	function, _ := tc["function"].(map[string]any)
	if function == nil {
		return nil
	}
	name := mapToolNameFromClaudeRequest(shared.StringFromAny(function["name"]), toolNameMap)
	argsStr := fixJSON(shared.StringFromAny(function["arguments"]))

	input := map[string]any{}
	if strings.TrimSpace(argsStr) != "" && json.Valid([]byte(argsStr)) {
		_ = json.Unmarshal([]byte(argsStr), &input)
	}
	return map[string]any{
		"type":  "tool_use",
		"id":    sanitizeClaudeToolID(callID),
		"name":  name,
		"input": input,
	}
}

func resolveClaudeReasoningEffort(root map[string]any) (string, bool) {
	thinkingConfig, _ := root["thinking"].(map[string]any)
	if thinkingConfig == nil {
		return "", false
	}

	switch strings.ToLower(strings.TrimSpace(shared.StringFromAny(thinkingConfig["type"]))) {
	case "enabled":
		if _, ok := thinkingConfig["budget_tokens"]; ok {
			return convertBudgetToLevel(shared.IntFromAny(thinkingConfig["budget_tokens"]))
		}
		return convertBudgetToLevel(-1)
	case "adaptive", "auto":
		if outputConfig, _ := root["output_config"].(map[string]any); outputConfig != nil {
			if effort := strings.ToLower(strings.TrimSpace(shared.StringFromAny(outputConfig["effort"]))); effort != "" {
				return effort, true
			}
		}
		return "xhigh", true
	case "disabled":
		return convertBudgetToLevel(0)
	default:
		return "", false
	}
}

func buildClaudeSystemMessage(v any) any {
	switch system := v.(type) {
	case string:
		if strings.TrimSpace(system) == "" {
			return nil
		}
		return []any{map[string]any{"type": "text", "text": system}}
	case []any:
		var content []any
		for _, item := range system {
			part, _ := item.(map[string]any)
			if part == nil {
				continue
			}
			if contentItem, ok := convertClaudeContentPart(part); ok {
				content = append(content, contentItem)
			}
		}
		if len(content) == 0 {
			return nil
		}
		return content
	default:
		return nil
	}
}

func buildOpenAIAssistantMessage(contentItems []any, reasoningContent string, toolCalls []any) any {
	if len(contentItems) == 0 && strings.TrimSpace(reasoningContent) == "" && len(toolCalls) == 0 {
		return nil
	}

	msg := map[string]any{
		"role": "assistant",
	}
	if len(contentItems) > 0 {
		msg["content"] = contentItems
	} else {
		msg["content"] = ""
	}
	if strings.TrimSpace(reasoningContent) != "" {
		msg["reasoning_content"] = reasoningContent
	}
	if len(toolCalls) > 0 {
		msg["tool_calls"] = toolCalls
	}
	return msg
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
		}
		out = append(out, map[string]any{"type": "function", "function": fn})
	}
	return out
}

func convertClaudeToolUseToOpenAIToolCall(role string, part map[string]any) any {
	if part == nil || role != "assistant" {
		return nil
	}
	callID := shared.StringFromAny(part["id"])
	name := shared.StringFromAny(part["name"])
	if strings.TrimSpace(name) == "" {
		return nil
	}
	var argsJSON []byte
	if _, ok := part["input"]; ok {
		argsJSON, _ = json.Marshal(part["input"])
	}
	if len(argsJSON) == 0 || string(argsJSON) == "null" {
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
	return map[string]any{
		"role":         "tool",
		"tool_call_id": callID,
		"content":      convertClaudeToolResultContent(part["content"]),
	}
}

func convertClaudeToolResultContent(v any) any {
	switch content := v.(type) {
	case nil:
		return ""
	case string:
		return content
	case []any:
		var textParts []string
		var contentItems []any
		hasImage := false

		for _, item := range content {
			switch current := item.(type) {
			case string:
				textParts = append(textParts, current)
				contentItems = append(contentItems, map[string]any{"type": "text", "text": current})
			case map[string]any:
				switch strings.TrimSpace(shared.StringFromAny(current["type"])) {
				case "text":
					text := shared.StringFromAny(current["text"])
					textParts = append(textParts, text)
					contentItems = append(contentItems, map[string]any{"type": "text", "text": text})
				case "image":
					if imageItem, ok := convertClaudeImagePart(current); ok {
						contentItems = append(contentItems, imageItem)
						hasImage = true
					} else {
						textParts = append(textParts, marshalToolResultFallback(current))
					}
				default:
					if text := shared.StringFromAny(current["text"]); strings.TrimSpace(text) != "" {
						textParts = append(textParts, text)
					} else {
						textParts = append(textParts, marshalToolResultFallback(current))
					}
				}
			default:
				textParts = append(textParts, marshalToolResultFallback(current))
			}
		}

		if hasImage && len(contentItems) > 0 {
			return contentItems
		}
		joined := strings.Join(textParts, "\n\n")
		if strings.TrimSpace(joined) != "" {
			return joined
		}
		return marshalToolResultFallback(content)
	case map[string]any:
		switch strings.TrimSpace(shared.StringFromAny(content["type"])) {
		case "image":
			if imageItem, ok := convertClaudeImagePart(content); ok {
				return []any{imageItem}
			}
		case "text":
			if text := shared.StringFromAny(content["text"]); strings.TrimSpace(text) != "" {
				return text
			}
		}
		if text := shared.StringFromAny(content["text"]); strings.TrimSpace(text) != "" {
			return text
		}
		return marshalToolResultFallback(content)
	default:
		return marshalToolResultFallback(content)
	}
}

func normalizeClaudeToolChoice(v any) any {
	switch choice := v.(type) {
	case string:
		if strings.TrimSpace(choice) == "" {
			return "auto"
		}
		return choice
	case map[string]any:
		switch strings.ToLower(strings.TrimSpace(shared.StringFromAny(choice["type"]))) {
		case "auto":
			return "auto"
		case "any":
			return "required"
		case "tool":
			name := strings.TrimSpace(shared.StringFromAny(choice["name"]))
			if name != "" {
				return map[string]any{
					"type": "function",
					"function": map[string]any{
						"name": name,
					},
				}
			}
		}
	}
	return "auto"
}

func convertClaudeContentPart(part map[string]any) (any, bool) {
	switch strings.TrimSpace(shared.StringFromAny(part["type"])) {
	case "text":
		return convertClaudeTextPart(part)
	case "image":
		return convertClaudeImagePart(part)
	default:
		return nil, false
	}
}

func extractClaudeThinkingText(part map[string]any) string {
	if part == nil {
		return ""
	}
	if thinking := shared.StringFromAny(part["thinking"]); strings.TrimSpace(thinking) != "" {
		return thinking
	}
	return shared.StringFromAny(part["text"])
}

func marshalToolResultFallback(v any) string {
	b, err := shared.MarshalNoEscapeHTML(v)
	if err != nil {
		b, _ = json.Marshal(v)
	}
	return string(b)
}

func convertBudgetToLevel(budget int) (string, bool) {
	switch {
	case budget < -1:
		return "", false
	case budget == -1:
		return "auto", true
	case budget == 0:
		return "none", true
	case budget <= 512:
		return "minimal", true
	case budget <= 1024:
		return "low", true
	case budget <= 8192:
		return "medium", true
	case budget <= 24576:
		return "high", true
	default:
		return "xhigh", true
	}
}

func buildClaudeContentBlocksFromOpenAIMessage(msg map[string]any, toolNameMap map[string]string) []any {
	if msg == nil {
		return nil
	}

	var out []any
	for _, reasoningText := range collectOpenAIReasoningTexts(msg["reasoning_content"]) {
		if strings.TrimSpace(reasoningText) != "" {
			out = append(out, map[string]any{"type": "thinking", "thinking": reasoningText})
		}
	}

	switch content := msg["content"].(type) {
	case string:
		if strings.TrimSpace(content) != "" {
			out = append(out, map[string]any{"type": "text", "text": content})
		}
	case []any:
		var textBuilder strings.Builder
		var thinkingBuilder strings.Builder

		flushText := func() {
			if textBuilder.Len() == 0 {
				return
			}
			out = append(out, map[string]any{"type": "text", "text": textBuilder.String()})
			textBuilder.Reset()
		}
		flushThinking := func() {
			if thinkingBuilder.Len() == 0 {
				return
			}
			out = append(out, map[string]any{"type": "thinking", "thinking": thinkingBuilder.String()})
			thinkingBuilder.Reset()
		}

		for _, itemRaw := range content {
			item, _ := itemRaw.(map[string]any)
			if item == nil {
				continue
			}
			switch strings.TrimSpace(shared.StringFromAny(item["type"])) {
			case "text":
				flushThinking()
				textBuilder.WriteString(shared.StringFromAny(item["text"]))
			case "reasoning":
				flushText()
				thinkingBuilder.WriteString(shared.StringFromAny(item["text"]))
			case "tool_calls":
				flushThinking()
				flushText()
				if tcAny, ok := item["tool_calls"].([]any); ok {
					for _, tcRaw := range tcAny {
						tc, _ := tcRaw.(map[string]any)
						if tc != nil {
							out = append(out, openAIToolCallToClaudeToolUse(tc, toolNameMap))
						}
					}
				}
			default:
				flushThinking()
				flushText()
			}
		}
		flushThinking()
		flushText()
	}

	return compactAnySlice(out)
}

func compactAnySlice(items []any) []any {
	if len(items) == 0 {
		return nil
	}
	out := make([]any, 0, len(items))
	for _, item := range items {
		if item != nil {
			out = append(out, item)
		}
	}
	return out
}

func getNestedMapInt(root map[string]any, outer, inner string) any {
	if root == nil {
		return nil
	}
	child, _ := root[outer].(map[string]any)
	if child == nil {
		return nil
	}
	return child[inner]
}

func shouldFinalizeClaudeStreamChunk(usageAny any, deltaHasActivity bool) bool {
	if !deltaHasActivity {
		return true
	}
	return hasNonZeroClaudeStreamUsage(usageAny)
}

func shouldCaptureClaudeStreamUsage(usageAny any, deltaHasActivity bool, finishReason string) bool {
	if strings.TrimSpace(finishReason) == "" {
		return false
	}
	if !deltaHasActivity {
		return true
	}
	return hasNonZeroClaudeStreamUsage(usageAny)
}

func hasNonZeroClaudeStreamUsage(v any) bool {
	usage, _ := v.(map[string]any)
	if usage == nil {
		return false
	}
	if shared.IntFromAny(usage["prompt_tokens"]) > 0 {
		return true
	}
	if shared.IntFromAny(usage["completion_tokens"]) > 0 {
		return true
	}
	if shared.IntFromAny(getNestedMapInt(usage, "prompt_tokens_details", "cached_tokens")) > 0 {
		return true
	}
	return false
}

func maxZero(v int) int {
	if v < 0 {
		return 0
	}
	return v
}

func collectOpenAIReasoningTexts(v any) []string {
	var texts []string
	switch current := v.(type) {
	case nil:
		return nil
	case string:
		if current != "" {
			return []string{current}
		}
	case []any:
		for _, item := range current {
			texts = append(texts, collectOpenAIReasoningTexts(item)...)
		}
	case map[string]any:
		if text := shared.StringFromAny(current["text"]); text != "" {
			texts = append(texts, text)
		}
	}
	return texts
}

func buildClaudeToolNameMap(originalRequestRawJSON []byte) map[string]string {
	if len(originalRequestRawJSON) == 0 {
		return nil
	}
	root, err := shared.DecodeJSONMap(originalRequestRawJSON)
	if err != nil {
		return nil
	}
	tools, _ := root["tools"].([]any)
	if len(tools) == 0 {
		return nil
	}
	out := make(map[string]string, len(tools))
	for _, toolRaw := range tools {
		tool, _ := toolRaw.(map[string]any)
		if tool == nil {
			continue
		}
		name := strings.TrimSpace(shared.StringFromAny(tool["name"]))
		if name == "" {
			continue
		}
		if key := canonicalToolName(name); key != "" {
			if _, exists := out[key]; !exists {
				out[key] = name
			}
		}
	}
	return out
}

func mapToolNameFromClaudeRequest(name string, toolNameMap map[string]string) string {
	if name = strings.TrimSpace(name); name == "" {
		return ""
	}
	if original, ok := toolNameMap[canonicalToolName(name)]; ok && strings.TrimSpace(original) != "" {
		return original
	}
	return name
}

func fixJSON(input string) string {
	var out bytes.Buffer

	inDouble := false
	inSingle := false
	escaped := false

	writeConverted := func(r rune) {
		if r == '"' {
			out.WriteByte('\\')
			out.WriteByte('"')
			return
		}
		out.WriteRune(r)
	}

	runes := []rune(input)
	for i := 0; i < len(runes); i++ {
		r := runes[i]

		if inDouble {
			out.WriteRune(r)
			if escaped {
				escaped = false
				continue
			}
			if r == '\\' {
				escaped = true
				continue
			}
			if r == '"' {
				inDouble = false
			}
			continue
		}

		if inSingle {
			if escaped {
				escaped = false
				switch r {
				case 'n', 'r', 't', 'b', 'f', '/', '"':
					out.WriteByte('\\')
					out.WriteRune(r)
				case '\\':
					out.WriteByte('\\')
					out.WriteByte('\\')
				case '\'':
					out.WriteRune('\'')
				case 'u':
					out.WriteByte('\\')
					out.WriteByte('u')
					for k := 0; k < 4 && i+1 < len(runes); k++ {
						peek := runes[i+1]
						if (peek >= '0' && peek <= '9') || (peek >= 'a' && peek <= 'f') || (peek >= 'A' && peek <= 'F') {
							out.WriteRune(peek)
							i++
						} else {
							break
						}
					}
				default:
					out.WriteByte('\\')
					out.WriteRune(r)
				}
				continue
			}

			if r == '\\' {
				escaped = true
				continue
			}
			if r == '\'' {
				out.WriteByte('"')
				inSingle = false
				continue
			}
			writeConverted(r)
			continue
		}

		if r == '"' {
			inDouble = true
			out.WriteRune(r)
			continue
		}
		if r == '\'' {
			inSingle = true
			out.WriteByte('"')
			continue
		}
		out.WriteRune(r)
	}

	if inSingle {
		out.WriteByte('"')
	}

	return out.String()
}

func canonicalToolName(name string) string {
	canonical := strings.TrimSpace(name)
	canonical = strings.TrimLeft(canonical, "_")
	return strings.ToLower(canonical)
}

func sanitizeClaudeToolID(id string) string {
	var b strings.Builder
	for _, r := range id {
		switch {
		case r >= 'a' && r <= 'z':
			b.WriteRune(r)
		case r >= 'A' && r <= 'Z':
			b.WriteRune(r)
		case r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '_' || r == '-':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	if b.Len() == 0 {
		return "toolu_" + shared.RandomSuffix()
	}
	return b.String()
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
