package responses

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"clisimplehub/internal/transformer/shared"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

type Transformer struct{}

func (Transformer) TargetInterfaceType() string { return "codex" }

func (Transformer) TargetPath(_ bool, _ string) string { return "/responses" }

func (Transformer) OutputContentType(isStreaming bool) string {
	if isStreaming {
		return "text/event-stream"
	}
	return "application/json"
}

func (Transformer) TransformRequest(modelName string, rawJSON []byte, _ bool) ([]byte, error) {
	if len(rawJSON) == 0 || !gjson.ValidBytes(rawJSON) {
		return nil, fmt.Errorf("invalid claude request json")
	}

	root := gjson.ParseBytes(rawJSON)
	bodyModelSuffix := parseModelSuffix(root.Get("model").String())
	modelSuffix := parseModelSuffix(modelName)
	if modelSuffix.hasSuffix {
		modelName = modelSuffix.modelName
	}
	if strings.TrimSpace(modelName) == "" {
		modelName = strings.TrimSpace(bodyModelSuffix.modelName)
	}
	if strings.TrimSpace(modelName) == "" {
		modelName = strings.TrimSpace(root.Get("model").String())
	}

	template := `{"model":"","input":[]}`
	template, _ = sjson.Set(template, "model", modelName)

	if systemMessage := buildSystemDeveloperMessage(root.Get("system")); systemMessage != "" {
		template, _ = sjson.SetRaw(template, "input.-1", systemMessage)
	}

	toolMap := buildReverseMapFromClaudeOriginalToShort(rawJSON)
	messages := root.Get("messages")
	if messages.IsArray() {
		for _, messageResult := range messages.Array() {
			messageRole := strings.TrimSpace(messageResult.Get("role").String())
			if messageRole == "" {
				continue
			}

			newMessage := func() string {
				msg := `{"type":"message","role":"","content":[]}`
				msg, _ = sjson.Set(msg, "role", messageRole)
				return msg
			}

			message := newMessage()
			contentIndex := 0
			hasContent := false

			flushMessage := func() {
				if !hasContent {
					return
				}
				template, _ = sjson.SetRaw(template, "input.-1", message)
				message = newMessage()
				contentIndex = 0
				hasContent = false
			}

			appendTextContent := func(text string) {
				partType := "input_text"
				if messageRole == "assistant" {
					partType = "output_text"
				}
				message, _ = sjson.Set(message, fmt.Sprintf("content.%d.type", contentIndex), partType)
				message, _ = sjson.Set(message, fmt.Sprintf("content.%d.text", contentIndex), text)
				contentIndex++
				hasContent = true
			}

			appendImageContent := func(dataURL string) {
				message, _ = sjson.Set(message, fmt.Sprintf("content.%d.type", contentIndex), "input_image")
				message, _ = sjson.Set(message, fmt.Sprintf("content.%d.image_url", contentIndex), dataURL)
				contentIndex++
				hasContent = true
			}

			messageContents := messageResult.Get("content")
			switch {
			case messageContents.IsArray():
				for _, contentResult := range messageContents.Array() {
					switch strings.TrimSpace(contentResult.Get("type").String()) {
					case "text":
						appendTextContent(contentResult.Get("text").String())
					case "image":
						if dataURL := buildDataURL(contentResult.Get("source")); dataURL != "" {
							appendImageContent(dataURL)
						}
					case "tool_use":
						flushMessage()
						name := contentResult.Get("name").String()
						if short, ok := toolMap[name]; ok {
							name = short
						} else {
							name = shortenNameIfNeeded(name)
						}
						functionCall := `{"type":"function_call"}`
						functionCall, _ = sjson.Set(functionCall, "call_id", contentResult.Get("id").String())
						functionCall, _ = sjson.Set(functionCall, "name", name)
						argsRaw := strings.TrimSpace(contentResult.Get("input").Raw)
						if argsRaw == "" {
							argsRaw = "{}"
						}
						functionCall, _ = sjson.Set(functionCall, "arguments", argsRaw)
						template, _ = sjson.SetRaw(template, "input.-1", functionCall)
					case "tool_result":
						flushMessage()
						functionOutput := `{"type":"function_call_output"}`
						functionOutput, _ = sjson.Set(functionOutput, "call_id", contentResult.Get("tool_use_id").String())

						outputResult := contentResult.Get("content")
						if outputResult.IsArray() {
							output := `[]`
							outputIndex := 0
							for _, part := range outputResult.Array() {
								switch strings.TrimSpace(part.Get("type").String()) {
								case "image":
									if dataURL := buildDataURL(part.Get("source")); dataURL != "" {
										output, _ = sjson.Set(output, fmt.Sprintf("%d.type", outputIndex), "input_image")
										output, _ = sjson.Set(output, fmt.Sprintf("%d.image_url", outputIndex), dataURL)
										outputIndex++
									}
								case "text":
									output, _ = sjson.Set(output, fmt.Sprintf("%d.type", outputIndex), "input_text")
									output, _ = sjson.Set(output, fmt.Sprintf("%d.text", outputIndex), part.Get("text").String())
									outputIndex++
								}
							}
							if output != `[]` {
								functionOutput, _ = sjson.SetRaw(functionOutput, "output", output)
							} else {
								functionOutput, _ = sjson.Set(functionOutput, "output", outputResult.String())
							}
						} else {
							functionOutput, _ = sjson.Set(functionOutput, "output", outputResult.String())
						}

						template, _ = sjson.SetRaw(template, "input.-1", functionOutput)
					}
				}
				flushMessage()
			case messageContents.Type == gjson.String:
				appendTextContent(messageContents.String())
				flushMessage()
			}
		}
	}

	if tools := buildResponsesTools(root.Get("tools")); tools != "" {
		template, _ = sjson.SetRaw(template, "tools", tools)
		template, _ = sjson.Set(template, "tool_choice", "auto")
	}

	reasoningEffort := "high"
	if thinkingConfig := root.Get("thinking"); thinkingConfig.Exists() && thinkingConfig.IsObject() {
		switch thinkingConfig.Get("type").String() {
		case "enabled":
			if budgetTokens := thinkingConfig.Get("budget_tokens"); budgetTokens.Exists() {
				if effort, ok := convertBudgetToLevel(int(budgetTokens.Int())); ok && effort != "" {
					reasoningEffort = effort
				}
			}
		case "adaptive", "auto":
			if effort := strings.ToLower(strings.TrimSpace(root.Get("output_config.effort").String())); effort != "" {
				reasoningEffort = effort
			} else {
				reasoningEffort = "xhigh"
			}
		case "disabled":
			if effort, ok := convertBudgetToLevel(0); ok && effort != "" {
				reasoningEffort = effort
			}
		}
	}
	if modelSuffix.hasSuffix {
		if effort, ok := parseEffortSuffix(modelSuffix.rawSuffix); ok {
			reasoningEffort = effort
		}
	}
	if bodyModelSuffix.hasSuffix {
		if effort, ok := parseEffortSuffix(bodyModelSuffix.rawSuffix); ok {
			reasoningEffort = effort
		}
	}

	template, _ = sjson.Set(template, "parallel_tool_calls", true)
	template, _ = sjson.Set(template, "reasoning.effort", reasoningEffort)
	template, _ = sjson.Set(template, "reasoning.summary", "auto")
	template, _ = sjson.Set(template, "stream", true)
	template, _ = sjson.Set(template, "store", false)
	template, _ = sjson.Set(template, "include", []string{"reasoning.encrypted_content"})

	return []byte(template), nil
}

func (Transformer) TransformResponseStream(_ context.Context, modelName string, originalRequestRawJSON, _ []byte, rawLine []byte, state *any) ([]string, error) {
	if state == nil {
		return nil, fmt.Errorf("nil transformer state")
	}
	if *state == nil {
		*state = &responsesToClaudeStreamState{}
	}
	streamState := (*state).(*responsesToClaudeStreamState)
	return transformResponsesLineToClaudeSSE(originalRequestRawJSON, modelName, rawLine, streamState)
}

func (Transformer) TransformResponseNonStream(ctx context.Context, modelName string, originalRequestRawJSON, requestRawJSON, rawJSON []byte, _ *any) ([]byte, error) {
	line := bytes.TrimSpace(rawJSON)
	if gjson.ValidBytes(line) {
		root := gjson.ParseBytes(line)
		switch {
		case root.Get("type").String() == "response.completed":
			return buildClaudeMessageFromResponseObject(modelName, originalRequestRawJSON, root.Get("response"))
		case root.IsObject():
			return buildClaudeMessageFromResponseObject(modelName, originalRequestRawJSON, root)
		}
	}

	return buildClaudeMessageFromResponsesTranscript(ctx, modelName, originalRequestRawJSON, requestRawJSON, rawJSON)
}

type responsesToClaudeStreamState struct {
	HasToolCall               bool
	BlockIndex                int
	HasReceivedArgumentsDelta bool
	SentMessageStop           bool
}

type collectedSSEBlock struct {
	blockType string
	id        string
	name      string
	text      strings.Builder
	args      strings.Builder
	thinking  strings.Builder
}

type collectedSSEEvent struct {
	event string
	data  map[string]any
}

func buildSystemDeveloperMessage(systemResult gjson.Result) string {
	if !systemResult.Exists() {
		return ""
	}

	message := `{"type":"message","role":"developer","content":[]}`
	contentIndex := 0

	appendText := func(text string) {
		if text == "" || strings.HasPrefix(text, "x-anthropic-billing-header: ") {
			return
		}
		message, _ = sjson.Set(message, fmt.Sprintf("content.%d.type", contentIndex), "input_text")
		message, _ = sjson.Set(message, fmt.Sprintf("content.%d.text", contentIndex), text)
		contentIndex++
	}

	switch {
	case systemResult.Type == gjson.String:
		appendText(systemResult.String())
	case systemResult.IsArray():
		for _, systemItem := range systemResult.Array() {
			if systemItem.Get("type").String() == "text" {
				appendText(systemItem.Get("text").String())
			}
		}
	}

	if contentIndex == 0 {
		return ""
	}
	return message
}

func buildResponsesTools(toolsResult gjson.Result) string {
	if !toolsResult.IsArray() {
		return ""
	}

	tools := `[]`
	var names []string
	for _, tool := range toolsResult.Array() {
		if name := tool.Get("name").String(); name != "" {
			names = append(names, name)
		}
	}
	shortMap := buildShortNameMap(names)

	for _, tool := range toolsResult.Array() {
		if tool.Get("type").String() == "web_search_20250305" {
			tools, _ = sjson.SetRaw(tools, "-1", `{"type":"web_search"}`)
			continue
		}

		name := tool.Get("name").String()
		if short, ok := shortMap[name]; ok {
			name = short
		} else {
			name = shortenNameIfNeeded(name)
		}

		out := `{"type":"function","name":"","strict":false}`
		out, _ = sjson.Set(out, "name", name)
		if description := tool.Get("description").String(); description != "" {
			out, _ = sjson.Set(out, "description", description)
		}
		out, _ = sjson.SetRaw(out, "parameters", normalizeToolParameters(tool.Get("input_schema").Raw))
		tools, _ = sjson.SetRaw(tools, "-1", out)
	}

	return tools
}

func buildDataURL(source gjson.Result) string {
	if !source.Exists() {
		return ""
	}
	data := source.Get("data").String()
	if data == "" {
		data = source.Get("base64").String()
	}
	if data == "" {
		return ""
	}
	mediaType := source.Get("media_type").String()
	if mediaType == "" {
		mediaType = source.Get("mime_type").String()
	}
	if mediaType == "" {
		mediaType = "application/octet-stream"
	}
	return fmt.Sprintf("data:%s;base64,%s", mediaType, data)
}

func transformResponsesLineToClaudeSSE(originalRequestRawJSON []byte, modelName string, rawLine []byte, state *responsesToClaudeStreamState) ([]string, error) {
	if state == nil {
		return nil, fmt.Errorf("nil stream state")
	}

	line := bytes.TrimSpace(rawLine)
	if len(line) == 0 || bytes.HasPrefix(line, []byte("event:")) || bytes.HasPrefix(line, []byte(":")) {
		return nil, nil
	}

	payload := line
	if p, ok := shared.SSEDataPayload(line); ok {
		payload = p
	}

	if bytes.Equal(payload, []byte("[DONE]")) {
		if state.SentMessageStop {
			return nil, nil
		}
		state.SentMessageStop = true
		return []string{shared.SSEEvent("message_stop", map[string]any{"type": "message_stop"})}, nil
	}
	if !gjson.ValidBytes(payload) {
		return nil, nil
	}

	root := gjson.ParseBytes(payload)
	typeStr := root.Get("type").String()
	outputs := make([]string, 0, 2)

	switch typeStr {
	case "response.created":
		resp := root.Get("response")
		model := strings.TrimSpace(modelName)
		if model == "" {
			model = strings.TrimSpace(resp.Get("model").String())
		}
		outputs = append(outputs, shared.SSEEvent("message_start", map[string]any{
			"type": "message_start",
			"message": map[string]any{
				"id":            resp.Get("id").String(),
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
		}))
	case "response.reasoning_summary_part.added":
		outputs = append(outputs, shared.SSEEvent("content_block_start", map[string]any{
			"type":  "content_block_start",
			"index": state.BlockIndex,
			"content_block": map[string]any{
				"type":     "thinking",
				"thinking": "",
			},
		}))
	case "response.reasoning_summary_text.delta":
		outputs = append(outputs, shared.SSEEvent("content_block_delta", map[string]any{
			"type":  "content_block_delta",
			"index": state.BlockIndex,
			"delta": map[string]any{
				"type":     "thinking_delta",
				"thinking": root.Get("delta").String(),
			},
		}))
	case "response.reasoning_summary_part.done":
		outputs = append(outputs, shared.SSEEvent("content_block_stop", map[string]any{
			"type":  "content_block_stop",
			"index": state.BlockIndex,
		}))
		state.BlockIndex++
	case "response.content_part.added":
		outputs = append(outputs, shared.SSEEvent("content_block_start", map[string]any{
			"type":  "content_block_start",
			"index": state.BlockIndex,
			"content_block": map[string]any{
				"type": "text",
				"text": "",
			},
		}))
	case "response.output_text.delta":
		outputs = append(outputs, shared.SSEEvent("content_block_delta", map[string]any{
			"type":  "content_block_delta",
			"index": state.BlockIndex,
			"delta": map[string]any{
				"type": "text_delta",
				"text": root.Get("delta").String(),
			},
		}))
	case "response.content_part.done":
		outputs = append(outputs, shared.SSEEvent("content_block_stop", map[string]any{
			"type":  "content_block_stop",
			"index": state.BlockIndex,
		}))
		state.BlockIndex++
	case "response.output_item.added":
		item := root.Get("item")
		if item.Get("type").String() == "function_call" {
			state.HasToolCall = true
			state.HasReceivedArgumentsDelta = false
			name := item.Get("name").String()
			if original, ok := buildReverseMapFromClaudeOriginalShortToOriginal(originalRequestRawJSON)[name]; ok {
				name = original
			}
			outputs = append(outputs, shared.SSEEvent("content_block_start", map[string]any{
				"type":  "content_block_start",
				"index": state.BlockIndex,
				"content_block": map[string]any{
					"type":  "tool_use",
					"id":    sanitizeClaudeToolID(item.Get("call_id").String()),
					"name":  name,
					"input": map[string]any{},
				},
			}))
			outputs = append(outputs, shared.SSEEvent("content_block_delta", map[string]any{
				"type":  "content_block_delta",
				"index": state.BlockIndex,
				"delta": map[string]any{
					"type":         "input_json_delta",
					"partial_json": "",
				},
			}))
		}
	case "response.function_call_arguments.delta":
		state.HasReceivedArgumentsDelta = true
		outputs = append(outputs, shared.SSEEvent("content_block_delta", map[string]any{
			"type":  "content_block_delta",
			"index": state.BlockIndex,
			"delta": map[string]any{
				"type":         "input_json_delta",
				"partial_json": root.Get("delta").String(),
			},
		}))
	case "response.function_call_arguments.done":
		if !state.HasReceivedArgumentsDelta {
			if args := root.Get("arguments").String(); args != "" {
				outputs = append(outputs, shared.SSEEvent("content_block_delta", map[string]any{
					"type":  "content_block_delta",
					"index": state.BlockIndex,
					"delta": map[string]any{
						"type":         "input_json_delta",
						"partial_json": args,
					},
				}))
			}
		}
	case "response.output_item.done":
		if root.Get("item.type").String() == "function_call" {
			outputs = append(outputs, shared.SSEEvent("content_block_stop", map[string]any{
				"type":  "content_block_stop",
				"index": state.BlockIndex,
			}))
			state.BlockIndex++
		}
	case "response.completed":
		stopReason := root.Get("response.stop_reason").String()
		switch {
		case state.HasToolCall:
			stopReason = "tool_use"
		case stopReason == "max_tokens" || stopReason == "stop":
		default:
			stopReason = "end_turn"
		}

		inputTokens, outputTokens, cachedRead, reasoning := extractResponsesUsage(root.Get("response.usage"))
		usage := map[string]any{
			"input_tokens":  inputTokens,
			"output_tokens": outputTokens,
		}
		if cachedRead > 0 {
			usage["cache_read_input_tokens"] = cachedRead
		}
		if reasoning > 0 {
			usage["reasoning_tokens"] = reasoning
		}

		outputs = append(outputs, shared.SSEEvent("message_delta", map[string]any{
			"type": "message_delta",
			"delta": map[string]any{
				"stop_reason":   stopReason,
				"stop_sequence": nil,
			},
			"usage": usage,
		}))
		outputs = append(outputs, shared.SSEEvent("message_stop", map[string]any{"type": "message_stop"}))
		state.SentMessageStop = true
	}

	return outputs, nil
}

func buildClaudeMessageFromResponsesTranscript(_ context.Context, modelName string, originalRequestRawJSON, _ []byte, rawJSON []byte) ([]byte, error) {
	scanner := bufio.NewScanner(bytes.NewReader(rawJSON))
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	state := &responsesToClaudeStreamState{}
	events := make([]collectedSSEEvent, 0, 16)
	for scanner.Scan() {
		outs, err := transformResponsesLineToClaudeSSE(originalRequestRawJSON, modelName, scanner.Bytes(), state)
		if err != nil {
			return nil, err
		}
		for _, out := range outs {
			if event, ok := parseGeneratedSSEEvent(out); ok {
				events = append(events, event)
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	if len(events) == 0 {
		return nil, fmt.Errorf("failed to parse responses transcript")
	}
	return shared.MarshalNoEscapeHTML(buildClaudeMessageFromSSEEvents(events, modelName))
}

func parseGeneratedSSEEvent(raw string) (collectedSSEEvent, bool) {
	var eventName string
	var dataLine string
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(line, "event:"):
			eventName = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
		case strings.HasPrefix(line, "data:"):
			dataLine = strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		}
	}
	if eventName == "" || dataLine == "" || dataLine == "[DONE]" {
		return collectedSSEEvent{}, false
	}
	data, err := shared.DecodeJSONMap([]byte(dataLine))
	if err != nil {
		return collectedSSEEvent{}, false
	}
	return collectedSSEEvent{event: eventName, data: data}, true
}

func buildClaudeMessageFromSSEEvents(events []collectedSSEEvent, modelName string) map[string]any {
	messageID := "msg_" + shared.RandomSuffix()
	stopReason := "end_turn"
	var stopSequence any
	inputTokens := 0
	outputTokens := 0
	cachedRead := 0
	reasoning := 0

	blocks := make(map[int]*collectedSSEBlock)
	blockOrder := make([]int, 0)

	for _, event := range events {
		data := event.data
		switch event.event {
		case "message_start":
			message, _ := data["message"].(map[string]any)
			if message != nil {
				if id := strings.TrimSpace(shared.StringFromAny(message["id"])); id != "" {
					messageID = id
				}
				if model := strings.TrimSpace(shared.StringFromAny(message["model"])); model != "" {
					modelName = model
				}
				applyUsageMapFromClaudeEvent(message["usage"], &inputTokens, &outputTokens, &cachedRead, &reasoning)
			}
		case "content_block_start":
			index := shared.IntFromAny(data["index"])
			if _, exists := blocks[index]; !exists {
				blockOrder = append(blockOrder, index)
			}
			contentBlock, _ := data["content_block"].(map[string]any)
			blocks[index] = &collectedSSEBlock{
				blockType: strings.TrimSpace(shared.StringFromAny(contentBlock["type"])),
				id:        strings.TrimSpace(shared.StringFromAny(contentBlock["id"])),
				name:      strings.TrimSpace(shared.StringFromAny(contentBlock["name"])),
			}
		case "content_block_delta":
			index := shared.IntFromAny(data["index"])
			block := blocks[index]
			if block == nil {
				continue
			}
			delta, _ := data["delta"].(map[string]any)
			switch strings.TrimSpace(shared.StringFromAny(delta["type"])) {
			case "text_delta":
				block.text.WriteString(shared.StringFromAny(delta["text"]))
			case "input_json_delta":
				block.args.WriteString(shared.StringFromAny(delta["partial_json"]))
			case "thinking_delta":
				block.thinking.WriteString(shared.StringFromAny(delta["thinking"]))
			}
		case "message_delta":
			delta, _ := data["delta"].(map[string]any)
			if delta != nil {
				if reason := strings.TrimSpace(shared.StringFromAny(delta["stop_reason"])); reason != "" {
					stopReason = reason
				}
				if seq, ok := delta["stop_sequence"]; ok {
					stopSequence = seq
				}
			}
			applyUsageMapFromClaudeEvent(data["usage"], &inputTokens, &outputTokens, &cachedRead, &reasoning)
		}
	}

	sort.Ints(blockOrder)
	content := make([]any, 0, len(blockOrder))
	hasToolUse := false

	for _, index := range blockOrder {
		block := blocks[index]
		if block == nil {
			continue
		}
		switch block.blockType {
		case "text":
			text := block.text.String()
			if strings.TrimSpace(text) != "" {
				content = append(content, map[string]any{
					"type": "text",
					"text": text,
				})
			}
		case "thinking":
			thinking := block.thinking.String()
			if strings.TrimSpace(thinking) != "" {
				content = append(content, map[string]any{
					"type":     "thinking",
					"thinking": thinking,
				})
			}
		case "tool_use":
			hasToolUse = true
			input := map[string]any{}
			rawArgs := strings.TrimSpace(block.args.String())
			if rawArgs != "" && gjson.Valid(rawArgs) {
				parsed := gjson.Parse(rawArgs)
				if parsed.IsObject() {
					_ = json.Unmarshal([]byte(parsed.Raw), &input)
				}
			}
			content = append(content, map[string]any{
				"type":  "tool_use",
				"id":    block.id,
				"name":  block.name,
				"input": input,
			})
		}
	}

	if hasToolUse && stopReason == "end_turn" {
		stopReason = "tool_use"
	}

	usage := map[string]any{
		"input_tokens":  inputTokens,
		"output_tokens": outputTokens,
	}
	if cachedRead > 0 {
		usage["cache_read_input_tokens"] = cachedRead
	}
	if reasoning > 0 {
		usage["reasoning_tokens"] = reasoning
	}

	return map[string]any{
		"id":            messageID,
		"type":          "message",
		"role":          "assistant",
		"content":       content,
		"model":         modelName,
		"stop_reason":   stopReason,
		"stop_sequence": stopSequence,
		"usage":         usage,
	}
}

func applyUsageMapFromClaudeEvent(rawUsage any, inputTokens, outputTokens, cachedRead, reasoning *int) {
	usage, _ := rawUsage.(map[string]any)
	if usage == nil {
		return
	}
	if v := shared.IntFromAny(usage["input_tokens"]); v > *inputTokens {
		*inputTokens = v
	}
	if v := shared.IntFromAny(usage["output_tokens"]); v > *outputTokens {
		*outputTokens = v
	}
	if v := shared.IntFromAny(usage["cache_read_input_tokens"]); v > *cachedRead {
		*cachedRead = v
	}
	if v := shared.IntFromAny(usage["reasoning_tokens"]); v > *reasoning {
		*reasoning = v
	}
}

func buildClaudeMessageFromResponseObject(modelName string, originalRequestRawJSON []byte, response gjson.Result) ([]byte, error) {
	if !response.Exists() || !response.IsObject() {
		return nil, fmt.Errorf("empty response")
	}

	messageID := strings.TrimSpace(response.Get("id").String())
	if messageID == "" {
		messageID = "msg_" + shared.RandomSuffix()
	}
	if strings.TrimSpace(modelName) == "" {
		modelName = strings.TrimSpace(response.Get("model").String())
	}

	inputTokens, outputTokens, cachedRead, reasoning := extractResponsesUsage(response.Get("usage"))
	usage := map[string]any{
		"input_tokens":  inputTokens,
		"output_tokens": outputTokens,
	}
	if cachedRead > 0 {
		usage["cache_read_input_tokens"] = cachedRead
	}
	if reasoning > 0 {
		usage["reasoning_tokens"] = reasoning
	}

	content := make([]any, 0)
	hasToolCall := false
	reverseToolNames := buildReverseMapFromClaudeOriginalShortToOriginal(originalRequestRawJSON)

	output := response.Get("output")
	if output.IsArray() {
		for _, item := range output.Array() {
			switch item.Get("type").String() {
			case "reasoning":
				if thinking := extractThinkingTextFromReasoningItem(item); strings.TrimSpace(thinking) != "" {
					content = append(content, map[string]any{
						"type":     "thinking",
						"thinking": thinking,
					})
				}
			case "message":
				itemContent := item.Get("content")
				if itemContent.IsArray() {
					for _, part := range itemContent.Array() {
						if part.Get("type").String() != "output_text" {
							continue
						}
						text := part.Get("text").String()
						if text != "" {
							content = append(content, map[string]any{
								"type": "text",
								"text": text,
							})
						}
					}
				} else if text := itemContent.String(); text != "" {
					content = append(content, map[string]any{
						"type": "text",
						"text": text,
					})
				}
			case "output_text":
				if text := item.Get("text").String(); text != "" {
					content = append(content, map[string]any{
						"type": "text",
						"text": text,
					})
				}
			case "function_call":
				hasToolCall = true
				name := item.Get("name").String()
				if original, ok := reverseToolNames[name]; ok {
					name = original
				}

				input := map[string]any{}
				args := item.Get("arguments").String()
				if args != "" && gjson.Valid(args) {
					parsed := gjson.Parse(args)
					if parsed.IsObject() {
						_ = json.Unmarshal([]byte(parsed.Raw), &input)
					}
				}

				content = append(content, map[string]any{
					"type":  "tool_use",
					"id":    sanitizeClaudeToolID(item.Get("call_id").String()),
					"name":  name,
					"input": input,
				})
			}
		}
	}

	stopReason := strings.TrimSpace(response.Get("stop_reason").String())
	switch {
	case stopReason != "":
	case hasToolCall:
		stopReason = "tool_use"
	default:
		stopReason = "end_turn"
	}

	var stopSequence any
	if stopSeq := response.Get("stop_sequence"); stopSeq.Exists() && stopSeq.Type != gjson.Null {
		stopSequence = stopSeq.Value()
	}

	return shared.MarshalNoEscapeHTML(map[string]any{
		"id":            messageID,
		"type":          "message",
		"role":          "assistant",
		"model":         modelName,
		"content":       content,
		"stop_reason":   stopReason,
		"stop_sequence": stopSequence,
		"usage":         usage,
	})
}

func extractThinkingTextFromReasoningItem(item gjson.Result) string {
	var thinking strings.Builder

	appendParts := func(parts gjson.Result) bool {
		if !parts.Exists() {
			return false
		}
		switch {
		case parts.IsArray():
			parts.ForEach(func(_, part gjson.Result) bool {
				if text := part.Get("text"); text.Exists() {
					thinking.WriteString(text.String())
				} else {
					thinking.WriteString(part.String())
				}
				return true
			})
			return thinking.Len() > 0
		default:
			thinking.WriteString(parts.String())
			return thinking.Len() > 0
		}
	}

	if appendParts(item.Get("summary")) {
		return thinking.String()
	}
	appendParts(item.Get("content"))
	return thinking.String()
}

func extractResponsesUsage(usage gjson.Result) (int, int, int, int) {
	if !usage.Exists() || usage.Type == gjson.Null {
		return 0, 0, 0, 0
	}

	inputTokens := int(usage.Get("input_tokens").Int())
	outputTokens := int(usage.Get("output_tokens").Int())
	cachedTokens := int(usage.Get("input_tokens_details.cached_tokens").Int())
	reasoningTokens := int(usage.Get("output_tokens_details.reasoning_tokens").Int())
	if reasoningTokens == 0 {
		reasoningTokens = int(usage.Get("completion_tokens_details.reasoning_tokens").Int())
	}
	if reasoningTokens == 0 {
		reasoningTokens = int(usage.Get("reasoning_tokens").Int())
	}

	if cachedTokens > 0 {
		if inputTokens >= cachedTokens {
			inputTokens -= cachedTokens
		} else {
			inputTokens = 0
		}
	}

	return inputTokens, outputTokens, cachedTokens, reasoningTokens
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
		return modelSuffixResult{
			modelName: model,
			hasSuffix: false,
		}
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
		index := strings.LastIndex(name, "__")
		if index > 0 {
			candidate := "mcp__" + name[index+2:]
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
	result := map[string]string{}

	baseCandidate := func(name string) string {
		if len(name) <= limit {
			return name
		}
		if strings.HasPrefix(name, "mcp__") {
			index := strings.LastIndex(name, "__")
			if index > 0 {
				candidate := "mcp__" + name[index+2:]
				if len(candidate) > limit {
					candidate = candidate[:limit]
				}
				return candidate
			}
		}
		return name[:limit]
	}

	makeUnique := func(candidate string) string {
		if _, exists := used[candidate]; !exists {
			return candidate
		}
		base := candidate
		for i := 1; ; i++ {
			suffix := "_" + strconv.Itoa(i)
			allowed := limit - len(suffix)
			if allowed < 0 {
				allowed = 0
			}
			current := base
			if len(current) > allowed {
				current = current[:allowed]
			}
			current += suffix
			if _, exists := used[current]; !exists {
				return current
			}
		}
	}

	for _, name := range names {
		unique := makeUnique(baseCandidate(name))
		used[unique] = struct{}{}
		result[name] = unique
	}

	return result
}

func buildReverseMapFromClaudeOriginalToShort(original []byte) map[string]string {
	tools := gjson.GetBytes(original, "tools")
	result := map[string]string{}
	if !tools.IsArray() {
		return result
	}

	names := make([]string, 0)
	for _, tool := range tools.Array() {
		if name := tool.Get("name").String(); name != "" {
			names = append(names, name)
		}
	}

	if len(names) == 0 {
		return result
	}
	return buildShortNameMap(names)
}

func buildReverseMapFromClaudeOriginalShortToOriginal(original []byte) map[string]string {
	reverse := map[string]string{}
	for originalName, shortName := range buildReverseMapFromClaudeOriginalToShort(original) {
		reverse[shortName] = originalName
	}
	return reverse
}

func normalizeToolParameters(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == "null" || !gjson.Valid(raw) {
		return `{"type":"object","properties":{}}`
	}

	schema := raw
	result := gjson.Parse(raw)
	schemaType := result.Get("type").String()
	if schemaType == "" {
		schema, _ = sjson.Set(schema, "type", "object")
		schemaType = "object"
	}
	if schemaType == "object" && !result.Get("properties").Exists() {
		schema, _ = sjson.SetRaw(schema, "properties", `{}`)
	}
	if strings.TrimSpace(gjson.Get(schema, "$schema").String()) != "" {
		schema, _ = sjson.Delete(schema, "$schema")
	}
	return schema
}
