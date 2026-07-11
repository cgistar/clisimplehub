package responses

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"clisimplehub/internal/transformer/shared"
	xaiBackend "clisimplehub/internal/xai/backend"

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

	template := `{"model":"","instructions":"","input":[]}`
	template, _ = sjson.Set(template, "model", modelName)

	if systemMessage := buildSystemDeveloperMessage(root.Get("system")); systemMessage != "" {
		template, _ = sjson.SetRaw(template, "input.-1", systemMessage)
	}
	// Claude max_tokens → Responses max_output_tokens
	if maxTokens := root.Get("max_tokens"); maxTokens.Exists() && maxTokens.Type == gjson.Number {
		if n := maxTokens.Int(); n > 0 {
			template, _ = sjson.Set(template, "max_output_tokens", n)
		}
	}

	toolMap := buildReverseMapFromClaudeOriginalToShort(rawJSON)
	webSearchNames := buildClaudeWebSearchToolNameSet(root.Get("tools"))
	messages := root.Get("messages")
	if messages.IsArray() {
		for _, messageResult := range messages.Array() {
			messageRole := strings.TrimSpace(messageResult.Get("role").String())
			if messageRole == "" {
				continue
			}
			// system role message → user reminder
			if messageRole == "system" {
				if reminder := claudeMessageSystemReminderText(messageResult.Get("content")); reminder != "" {
					msg := `{"type":"message","role":"user","content":[{"type":"input_text","text":""}]}`
					msg, _ = sjson.Set(msg, "content.0.text", reminder)
					template, _ = sjson.SetRaw(template, "input.-1", msg)
				}
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

			// Claude thinking → Responses reasoning（仅当 signature 为合法 Grok encrypted 时）
			appendThinkingAsReasoning := func(part gjson.Result) {
				if messageRole != "assistant" {
					return
				}
				sig := strings.TrimSpace(part.Get("signature").String())
				if sig == "" || !xaiBackend.IsValidGrokEncryptedContent(sig) {
					return
				}
				flushMessage()
				item := `{"type":"reasoning","summary":[],"content":null}`
				item, _ = sjson.Set(item, "encrypted_content", sig)
				template, _ = sjson.SetRaw(template, "input.-1", item)
			}

			messageContents := messageResult.Get("content")
			switch {
			case messageContents.IsArray():
				for _, contentResult := range messageContents.Array() {
					switch strings.TrimSpace(contentResult.Get("type").String()) {
					case "text":
						appendTextContent(contentResult.Get("text").String())
					case "thinking":
						appendThinkingAsReasoning(contentResult)
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
						functionCall, _ = sjson.Set(functionCall, "call_id", shortenCodexCallIDIfNeeded(contentResult.Get("id").String()))
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
						functionOutput, _ = sjson.Set(functionOutput, "call_id", shortenCodexCallIDIfNeeded(contentResult.Get("tool_use_id").String()))

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
		template, _ = sjson.SetRaw(template, "tool_choice", convertClaudeToolChoiceToCodex(root.Get("tool_choice"), toolMap, webSearchNames))
	}

	// 禁用 thinking 时不传 effort=none（上游 400）
	reasoningEffort := "high"
	disableReasoning := false
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
			// Claude Code 常在 thinking.disabled 时仍带 output_config.effort；
			// xAI 不接受 none，关闭 reasoning 块。
			disableReasoning = true
		}
	}
	if modelSuffix.hasSuffix {
		if effort, ok := parseEffortSuffix(modelSuffix.rawSuffix); ok {
			if effort == "none" {
				disableReasoning = true
			} else {
				reasoningEffort = effort
				disableReasoning = false
			}
		}
	}
	if bodyModelSuffix.hasSuffix {
		if effort, ok := parseEffortSuffix(bodyModelSuffix.rawSuffix); ok {
			if effort == "none" {
				disableReasoning = true
			} else {
				reasoningEffort = effort
				disableReasoning = false
			}
		}
	}
	// 规范化 xAI effort
	if !disableReasoning {
		if norm, drop := xaiBackend.NormalizeXAIReasoningEffort(reasoningEffort); drop {
			disableReasoning = true
		} else {
			reasoningEffort = norm
		}
	}

	parallelToolCalls := true
	if disable := root.Get("tool_choice.disable_parallel_tool_use"); disable.Exists() {
		parallelToolCalls = !disable.Bool()
	}
	template, _ = sjson.Set(template, "parallel_tool_calls", parallelToolCalls)
	if !disableReasoning {
		template, _ = sjson.Set(template, "reasoning.effort", reasoningEffort)
		template, _ = sjson.Set(template, "reasoning.summary", "auto")
	}
	if tier := normalizeCodexServiceTier(root.Get("service_tier")); tier != "" {
		template, _ = sjson.Set(template, "service_tier", tier)
	}
	template, _ = sjson.Set(template, "stream", true)
	template, _ = sjson.Set(template, "store", false)
	// xAI / Codex 多轮 reasoning 必需
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
		switch root.Get("type").String() {
		case "response.completed", "response.incomplete":
			// 事件外壳下的 response 对象
			return buildClaudeMessageFromResponseObject(modelName, originalRequestRawJSON, root.Get("response"))
		default:
			// 裸 response 对象（无 type 包装）
			if root.IsObject() && (root.Get("output").Exists() || root.Get("id").Exists()) {
				return buildClaudeMessageFromResponseObject(modelName, originalRequestRawJSON, root)
			}
		}
	}

	return buildClaudeMessageFromResponsesTranscript(ctx, modelName, originalRequestRawJSON, requestRawJSON, rawJSON)
}

type responsesToClaudeStreamState struct {
	HasEmittedToolUse         bool
	BlockIndex                int
	HasReceivedArgumentsDelta bool
	SentMessageStop           bool
	// 分块开关
	TextBlockOpen           bool
	ThinkingBlockOpen       bool
	ThinkingStopPending     bool
	ThinkingSummarySeen     bool
	FunctionCallBlockOpen   bool
	FunctionCallBlockCallID string
	FunctionCallBlockIndex  int
	HasTextDelta            bool
	ThinkingSignature       string

	PendingFunctionCalls       map[string]*pendingFunctionCall
	LastPendingFunctionCallKey string

	WebSearchToolUseIDs    map[string]struct{}
	WebSearchToolResultIDs map[string]struct{}
	LastWebSearchToolUseID string
}

type collectedSSEBlock struct {
	blockType  string
	id         string
	name       string
	toolUseID  string
	text       strings.Builder
	args       strings.Builder
	thinking   strings.Builder
	signature  string
	rawContent any // web_search_tool_result.content 等
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
		if text == "" || isClaudeCodeAttributionSystemText(text) {
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
		toolType := tool.Get("type").String()
		if isClaudeWebSearchToolType(toolType) {
			tools, _ = sjson.SetRaw(tools, "-1", convertClaudeWebSearchToolToCodex(tool))
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
		// 剥离 Claude 专用字段
		out, _ = sjson.Delete(out, "input_schema")
		out, _ = sjson.Delete(out, "cache_control")
		out, _ = sjson.Delete(out, "defer_loading")
		out, _ = sjson.Delete(out, "parameters.$schema")
		out, _ = sjson.Set(out, "strict", false)
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

// stream transform 见 stream_transform.go

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
			block := &collectedSSEBlock{
				blockType: strings.TrimSpace(shared.StringFromAny(contentBlock["type"])),
				id:        strings.TrimSpace(shared.StringFromAny(contentBlock["id"])),
				name:      strings.TrimSpace(shared.StringFromAny(contentBlock["name"])),
				toolUseID: strings.TrimSpace(shared.StringFromAny(contentBlock["tool_use_id"])),
			}
			if contentBlock["content"] != nil {
				block.rawContent = contentBlock["content"]
			}
			// server_tool_use 可能在 start 时已带 input
			if input, ok := contentBlock["input"].(map[string]any); ok && len(input) > 0 {
				if raw, err := json.Marshal(input); err == nil {
					block.args.Write(raw)
				}
			}
			blocks[index] = block
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
			case "signature_delta":
				if sig := strings.TrimSpace(shared.StringFromAny(delta["signature"])); sig != "" {
					block.signature = sig
				}
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
			// signature-only thinking 也要保留
			if strings.TrimSpace(thinking) != "" || block.signature != "" {
				item := map[string]any{
					"type":     "thinking",
					"thinking": thinking,
				}
				if block.signature != "" {
					item["signature"] = block.signature
				}
				content = append(content, item)
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
		case "server_tool_use":
			input := map[string]any{}
			rawArgs := strings.TrimSpace(block.args.String())
			if rawArgs != "" && gjson.Valid(rawArgs) {
				parsed := gjson.Parse(rawArgs)
				if parsed.IsObject() {
					_ = json.Unmarshal([]byte(parsed.Raw), &input)
				}
			}
			content = append(content, map[string]any{
				"type":  "server_tool_use",
				"id":    block.id,
				"name":  block.name,
				"input": input,
			})
		case "web_search_tool_result":
			resultContent := block.rawContent
			if resultContent == nil {
				resultContent = []any{}
			}
			content = append(content, map[string]any{
				"type":        "web_search_tool_result",
				"tool_use_id": block.toolUseID,
				"content":     resultContent,
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
	webSearchSeen := make(map[string]struct{})

	output := response.Get("output")
	if output.IsArray() {
		for _, item := range output.Array() {
			switch item.Get("type").String() {
			case "reasoning":
				thinking := extractThinkingTextFromReasoningItem(item)
				signature := item.Get("encrypted_content").String()
				if strings.TrimSpace(thinking) != "" || signature != "" {
					block := map[string]any{
						"type":     "thinking",
						"thinking": thinking,
					}
					if signature != "" {
						block["signature"] = signature
					}
					content = append(content, block)
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
			case "web_search_call":
				content = appendWebSearchNonStreamContent(content, item, webSearchSeen)
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
					"id":    shortenCodexCallIDIfNeeded(sanitizeClaudeToolID(item.Get("call_id").String())),
					"name":  name,
					"input": input,
				})
			}
		}
	}

	stopReason := mapCodexStopReasonToClaude(codexStopReason(response), hasToolCall)

	var stopSequence any
	if stopSeq := response.Get("stop_sequence"); stopSeq.Exists() && stopSeq.Type != gjson.Null && stopSeq.String() != "" {
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

// call_id 上限 64。
func shortenCodexCallIDIfNeeded(id string) string {
	const limit = 64
	if len(id) <= limit {
		return id
	}
	sum := sha256.Sum256([]byte(id))
	suffix := "_" + hex.EncodeToString(sum[:8])
	prefixLen := limit - len(suffix)
	if prefixLen <= 0 {
		return suffix[len(suffix)-limit:]
	}
	return id[:prefixLen] + suffix
}

func isClaudeWebSearchToolType(toolType string) bool {
	return toolType == "web_search_20250305" || toolType == "web_search_20260209"
}

func buildClaudeWebSearchToolNameSet(tools gjson.Result) map[string]struct{} {
	names := map[string]struct{}{}
	if !tools.IsArray() {
		return names
	}
	for _, tool := range tools.Array() {
		if !isClaudeWebSearchToolType(tool.Get("type").String()) {
			continue
		}
		if name := tool.Get("name").String(); name != "" {
			names[name] = struct{}{}
		}
	}
	return names
}

func convertClaudeWebSearchToolToCodex(tool gjson.Result) string {
	out := `{"type":"web_search"}`
	if allowed := tool.Get("allowed_domains"); allowed.Exists() && allowed.IsArray() {
		out, _ = sjson.SetRaw(out, "filters.allowed_domains", allowed.Raw)
	}
	if loc := tool.Get("user_location"); loc.Exists() && loc.IsObject() {
		out, _ = sjson.SetRaw(out, "user_location", loc.Raw)
	}
	return out
}

func convertClaudeToolChoiceToCodex(toolChoice gjson.Result, toolNameMap map[string]string, webSearchNames map[string]struct{}) string {
	if !toolChoice.Exists() || toolChoice.Type == gjson.Null {
		return `"auto"`
	}
	choiceType := toolChoice.Get("type").String()
	if choiceType == "" && toolChoice.Type == gjson.String {
		choiceType = toolChoice.String()
	}
	switch choiceType {
	case "auto", "":
		return `"auto"`
	case "any":
		return `"required"`
	case "none":
		return `"none"`
	case "tool":
		name := toolChoice.Get("name").String()
		if _, ok := webSearchNames[name]; ok {
			return `{"type":"web_search"}`
		}
		if short, ok := toolNameMap[name]; ok {
			name = short
		} else {
			name = shortenNameIfNeeded(name)
		}
		if name == "" {
			return `"auto"`
		}
		choice := `{"type":"function","name":""}`
		choice, _ = sjson.Set(choice, "name", name)
		return choice
	default:
		return `"auto"`
	}
}

func normalizeCodexServiceTier(result gjson.Result) string {
	if !result.Exists() || result.Type != gjson.String {
		return ""
	}
	switch strings.ToLower(strings.TrimSpace(result.String())) {
	case "fast", "priority":
		return "priority"
	default:
		return ""
	}
}

// 过滤 billing attribution，并用 <system-reminder> 包装。
func claudeMessageSystemReminderText(content gjson.Result) string {
	parts := claudeSystemTextParts(content)
	if len(parts) == 0 {
		return ""
	}
	text := strings.Join(parts, "\n")
	if strings.TrimSpace(text) == "" {
		return ""
	}
	return "<system-reminder>\n" + text + "\n</system-reminder>"
}

func claudeSystemTextParts(content gjson.Result) []string {
	if !content.Exists() {
		return nil
	}
	if content.Type == gjson.String {
		text := content.String()
		if text == "" || isClaudeCodeAttributionSystemText(text) {
			return nil
		}
		return []string{text}
	}
	if !content.IsArray() {
		return nil
	}
	parts := make([]string, 0)
	for _, item := range content.Array() {
		if item.Get("type").String() != "text" {
			continue
		}
		text := item.Get("text").String()
		if text == "" || isClaudeCodeAttributionSystemText(text) {
			continue
		}
		parts = append(parts, text)
	}
	return parts
}
