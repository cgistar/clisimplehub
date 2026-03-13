package responses

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// Transformer implements: Chat Completions ("chat") -> Responses API ("codex").
// Use-case: client talks /v1/chat/completions, upstream only supports /v1/responses.
type Transformer struct{}

const defaultCodingInstructions = "You are a helpful coding assistant."

type modelSuffixResult struct {
	modelName string
	hasSuffix bool
	rawSuffix string
}

func (Transformer) TargetInterfaceType() string        { return "codex" }
func (Transformer) TargetPath(_ bool, _ string) string { return "/responses" }

func (Transformer) OutputContentType(isStreaming bool) string {
	if isStreaming {
		return "text/event-stream"
	}
	return "application/json"
}

// TransformRequest converts a Chat Completions request into a Responses API request.
func (Transformer) TransformRequest(modelName string, rawJSON []byte, stream bool) ([]byte, error) {
	if len(rawJSON) == 0 || !gjson.ValidBytes(rawJSON) {
		return nil, fmt.Errorf("invalid chat completions request json")
	}

	bodyModelSuffix := parseModelSuffix(gjson.GetBytes(rawJSON, "model").String())
	modelSuffix := parseModelSuffix(modelName)
	targetModel := strings.TrimSpace(modelSuffix.modelName)
	if targetModel == "" {
		targetModel = strings.TrimSpace(bodyModelSuffix.modelName)
	}
	if targetModel == "" {
		targetModel = strings.TrimSpace(modelName)
	}

	out := fmt.Sprintf(`{"instructions":%q}`, defaultCodingInstructions)
	out, _ = sjson.Set(out, "stream", stream)
	out, _ = sjson.Set(out, "model", targetModel)

	// Temperature / top_p passthrough for generic Responses API backends
	if v := gjson.GetBytes(rawJSON, "temperature"); v.Exists() {
		out, _ = sjson.Set(out, "temperature", v.Value())
	}
	if v := gjson.GetBytes(rawJSON, "top_p"); v.Exists() {
		out, _ = sjson.Set(out, "top_p", v.Value())
	}

	// Token limits
	if v := gjson.GetBytes(rawJSON, "max_tokens"); v.Exists() {
		out, _ = sjson.Set(out, "max_output_tokens", v.Value())
	}
	if v := gjson.GetBytes(rawJSON, "max_completion_tokens"); v.Exists() {
		out, _ = sjson.Set(out, "max_output_tokens", v.Value())
	}

	// Reasoning
	if v := gjson.GetBytes(rawJSON, "reasoning_effort"); v.Exists() {
		out, _ = sjson.Set(out, "reasoning.effort", v.Value())
	} else if effort, ok := parseEffortSuffix(modelSuffix.rawSuffix); ok {
		out, _ = sjson.Set(out, "reasoning.effort", effort)
	} else if effort, ok := parseEffortSuffix(bodyModelSuffix.rawSuffix); ok {
		out, _ = sjson.Set(out, "reasoning.effort", effort)
	} else {
		out, _ = sjson.Set(out, "reasoning.effort", "medium")
	}
	out, _ = sjson.Set(out, "reasoning.summary", "auto")
	if v := gjson.GetBytes(rawJSON, "parallel_tool_calls"); v.Exists() {
		out, _ = sjson.Set(out, "parallel_tool_calls", v.Value())
	} else {
		out, _ = sjson.Set(out, "parallel_tool_calls", true)
	}
	out, _ = sjson.Set(out, "include", []string{"reasoning.encrypted_content"})

	// Build tool name shortening map
	originalToolNameMap := buildShortNameMapFromChatTools(rawJSON)

	// Build input from messages
	out, _ = sjson.SetRaw(out, "input", `[]`)
	messages := gjson.GetBytes(rawJSON, "messages")
	if messages.IsArray() {
		for _, m := range messages.Array() {
			role := m.Get("role").String()

			switch role {
			case "tool":
				funcOutput := `{}`
				funcOutput, _ = sjson.Set(funcOutput, "type", "function_call_output")
				funcOutput, _ = sjson.Set(funcOutput, "call_id", m.Get("tool_call_id").String())
				funcOutput, _ = sjson.Set(funcOutput, "output", m.Get("content").String())
				out, _ = sjson.SetRaw(out, "input.-1", funcOutput)

			default:
				msg := `{}`
				msg, _ = sjson.Set(msg, "type", "message")
				if role == "system" {
					msg, _ = sjson.Set(msg, "role", "developer")
				} else {
					msg, _ = sjson.Set(msg, "role", role)
				}
				msg, _ = sjson.SetRaw(msg, "content", `[]`)

				c := m.Get("content")
				if c.Exists() && c.Type == gjson.String && c.String() != "" {
					partType := "input_text"
					if role == "assistant" {
						partType = "output_text"
					}
					part := `{}`
					part, _ = sjson.Set(part, "type", partType)
					part, _ = sjson.Set(part, "text", c.String())
					msg, _ = sjson.SetRaw(msg, "content.-1", part)
				} else if c.Exists() && c.IsArray() {
					for _, it := range c.Array() {
						t := it.Get("type").String()
						switch t {
						case "text":
							partType := "input_text"
							if role == "assistant" {
								partType = "output_text"
							}
							part := `{}`
							part, _ = sjson.Set(part, "type", partType)
							part, _ = sjson.Set(part, "text", it.Get("text").String())
							msg, _ = sjson.SetRaw(msg, "content.-1", part)
						case "image_url":
							if role == "user" {
								part := `{}`
								part, _ = sjson.Set(part, "type", "input_image")
								if u := it.Get("image_url.url"); u.Exists() {
									part, _ = sjson.Set(part, "image_url", u.String())
								}
								msg, _ = sjson.SetRaw(msg, "content.-1", part)
							}
						case "file":
							if role == "user" {
								fileData := it.Get("file.file_data").String()
								filename := it.Get("file.filename").String()
								if fileData != "" {
									part := `{}`
									part, _ = sjson.Set(part, "type", "input_file")
									part, _ = sjson.Set(part, "file_data", fileData)
									if filename != "" {
										part, _ = sjson.Set(part, "filename", filename)
									}
									msg, _ = sjson.SetRaw(msg, "content.-1", part)
								}
							}
						}
					}
				}

				// Only append message if it has content parts (skip empty assistant tool_calls-only turns)
				if gjson.Get(msg, "content.#").Int() > 0 {
					out, _ = sjson.SetRaw(out, "input.-1", msg)
				}

				// Assistant tool_calls -> separate function_call items
				if role == "assistant" {
					toolCalls := m.Get("tool_calls")
					if toolCalls.Exists() && toolCalls.IsArray() {
						for _, tc := range toolCalls.Array() {
							if tc.Get("type").String() == "function" {
								funcCall := `{}`
								funcCall, _ = sjson.Set(funcCall, "type", "function_call")
								funcCall, _ = sjson.Set(funcCall, "call_id", tc.Get("id").String())
								name := tc.Get("function.name").String()
								if short, ok := originalToolNameMap[name]; ok {
									name = short
								} else {
									name = shortenNameIfNeeded(name)
								}
								funcCall, _ = sjson.Set(funcCall, "name", name)
								funcCall, _ = sjson.Set(funcCall, "arguments", tc.Get("function.arguments").String())
								out, _ = sjson.SetRaw(out, "input.-1", funcCall)
							}
						}
					}
				}
			}
		}
	}

	// response_format -> text.format
	rf := gjson.GetBytes(rawJSON, "response_format")
	text := gjson.GetBytes(rawJSON, "text")
	if rf.Exists() {
		if !gjson.Get(out, "text").Exists() {
			out, _ = sjson.SetRaw(out, "text", `{}`)
		}
		switch rf.Get("type").String() {
		case "text":
			out, _ = sjson.Set(out, "text.format.type", "text")
		case "json_schema":
			js := rf.Get("json_schema")
			if js.Exists() {
				out, _ = sjson.Set(out, "text.format.type", "json_schema")
				if v := js.Get("name"); v.Exists() {
					out, _ = sjson.Set(out, "text.format.name", v.Value())
				}
				if v := js.Get("strict"); v.Exists() {
					out, _ = sjson.Set(out, "text.format.strict", v.Value())
				}
				if v := js.Get("schema"); v.Exists() {
					out, _ = sjson.SetRaw(out, "text.format.schema", v.Raw)
				}
			}
		}
		if text.Exists() {
			if v := text.Get("verbosity"); v.Exists() {
				out, _ = sjson.Set(out, "text.verbosity", v.Value())
			}
		}
	} else if text.Exists() {
		if v := text.Get("verbosity"); v.Exists() {
			if !gjson.Get(out, "text").Exists() {
				out, _ = sjson.SetRaw(out, "text", `{}`)
			}
			out, _ = sjson.Set(out, "text.verbosity", v.Value())
		}
	}

	// Tools
	tools := gjson.GetBytes(rawJSON, "tools")
	if tools.IsArray() && len(tools.Array()) > 0 {
		out, _ = sjson.SetRaw(out, "tools", `[]`)
		for _, t := range tools.Array() {
			toolType := t.Get("type").String()
			if toolType != "" && toolType != "function" && t.IsObject() {
				out, _ = sjson.SetRaw(out, "tools.-1", t.Raw)
				continue
			}
			if toolType == "function" {
				item := `{}`
				item, _ = sjson.Set(item, "type", "function")
				fn := t.Get("function")
				if fn.Exists() {
					if v := fn.Get("name"); v.Exists() {
						name := v.String()
						if short, ok := originalToolNameMap[name]; ok {
							name = short
						} else {
							name = shortenNameIfNeeded(name)
						}
						item, _ = sjson.Set(item, "name", name)
					}
					if v := fn.Get("description"); v.Exists() {
						item, _ = sjson.Set(item, "description", v.Value())
					}
					if v := fn.Get("parameters"); v.Exists() {
						item, _ = sjson.SetRaw(item, "parameters", v.Raw)
					}
					if v := fn.Get("strict"); v.Exists() {
						item, _ = sjson.Set(item, "strict", v.Value())
					}
				}
				out, _ = sjson.SetRaw(out, "tools.-1", item)
			}
		}
	}

	// tool_choice
	if tc := gjson.GetBytes(rawJSON, "tool_choice"); tc.Exists() {
		switch {
		case tc.Type == gjson.String:
			out, _ = sjson.Set(out, "tool_choice", tc.String())
		case tc.IsObject():
			tcType := tc.Get("type").String()
			if tcType == "function" {
				name := tc.Get("function.name").String()
				if name != "" {
					if short, ok := originalToolNameMap[name]; ok {
						name = short
					} else {
						name = shortenNameIfNeeded(name)
					}
				}
				choice := `{}`
				choice, _ = sjson.Set(choice, "type", "function")
				if name != "" {
					choice, _ = sjson.Set(choice, "name", name)
				}
				out, _ = sjson.SetRaw(out, "tool_choice", choice)
			} else if tcType != "" {
				out, _ = sjson.SetRaw(out, "tool_choice", tc.Raw)
			}
		}
	}

	out, _ = sjson.Set(out, "store", false)
	return []byte(out), nil
}

// responsesToChatState holds streaming conversion state.
type responsesToChatState struct {
	ResponseID                string
	CreatedAt                 int64
	Model                     string
	FunctionCallIndex         int
	HasReceivedArgumentsDelta bool
	HasToolCallAnnounced      bool
	Finished                  bool
	ReverseMap                map[string]string
}

var dataTag = []byte("data:")

// TransformResponseStream converts a single SSE line from Responses API to Chat Completions SSE chunk(s).
func (Transformer) TransformResponseStream(_ context.Context, modelName string, originalRequestRawJSON, _ []byte, rawLine []byte, state *any) ([]string, error) {
	if state == nil {
		return nil, fmt.Errorf("nil transformer state")
	}
	if *state == nil {
		*state = &responsesToChatState{
			Model:             modelName,
			FunctionCallIndex: -1,
		}
	}
	st := (*state).(*responsesToChatState)
	if st.ReverseMap == nil {
		st.ReverseMap = buildReverseMapFromOriginalChatCompletions(originalRequestRawJSON)
	}

	line := bytes.TrimSpace(rawLine)
	if len(line) == 0 || bytes.HasPrefix(line, []byte("event:")) || bytes.HasPrefix(line, []byte(":")) {
		return nil, nil
	}

	payload := line
	if bytes.HasPrefix(line, dataTag) {
		payload = bytes.TrimSpace(line[5:])
	}
	if len(payload) == 0 || bytes.Equal(payload, []byte("[DONE]")) {
		return nil, nil
	}
	if !gjson.ValidBytes(payload) {
		return nil, nil
	}

	root := gjson.ParseBytes(payload)
	dataType := root.Get("type").String()

	// Build base chunk without null fields — each branch adds only relevant delta keys
	chunk := `{"id":"","object":"chat.completion.chunk","created":0,"model":"","choices":[{"index":0,"delta":{},"finish_reason":null}]}`

	if dataType == "response.created" {
		st.ResponseID = root.Get("response.id").String()
		st.CreatedAt = root.Get("response.created_at").Int()
		st.Model = root.Get("response.model").String()
		return nil, nil
	}

	// Set common fields
	cachedModel := st.Model
	if m := root.Get("model"); m.Exists() {
		chunk, _ = sjson.Set(chunk, "model", m.String())
	} else if cachedModel != "" {
		chunk, _ = sjson.Set(chunk, "model", cachedModel)
	} else if modelName != "" {
		chunk, _ = sjson.Set(chunk, "model", modelName)
	}
	chunk, _ = sjson.Set(chunk, "created", st.CreatedAt)
	chunk, _ = sjson.Set(chunk, "id", st.ResponseID)

	// Usage (only in response.completed)
	if usage := root.Get("response.usage"); usage.Exists() {
		if v := usage.Get("output_tokens"); v.Exists() {
			chunk, _ = sjson.Set(chunk, "usage.completion_tokens", v.Int())
		}
		if v := usage.Get("total_tokens"); v.Exists() {
			chunk, _ = sjson.Set(chunk, "usage.total_tokens", v.Int())
		}
		if v := usage.Get("input_tokens"); v.Exists() {
			chunk, _ = sjson.Set(chunk, "usage.prompt_tokens", v.Int())
		}
		if v := usage.Get("input_tokens_details.cached_tokens"); v.Exists() {
			chunk, _ = sjson.Set(chunk, "usage.prompt_tokens_details.cached_tokens", v.Int())
		}
		if v := usage.Get("output_tokens_details.reasoning_tokens"); v.Exists() {
			chunk, _ = sjson.Set(chunk, "usage.completion_tokens_details.reasoning_tokens", v.Int())
		}
	}

	reverseMap := st.ReverseMap

	switch dataType {
	case "response.output_text.delta":
		if delta := root.Get("delta"); delta.Exists() {
			chunk, _ = sjson.Set(chunk, "choices.0.delta.role", "assistant")
			chunk, _ = sjson.Set(chunk, "choices.0.delta.content", delta.String())
		}

	case "response.reasoning_summary_text.delta":
		if delta := root.Get("delta"); delta.Exists() {
			chunk, _ = sjson.Set(chunk, "choices.0.delta.role", "assistant")
			chunk, _ = sjson.Set(chunk, "choices.0.delta.reasoning_content", delta.String())
		}

	case "response.reasoning_summary_text.done":
		chunk, _ = sjson.Set(chunk, "choices.0.delta.role", "assistant")
		chunk, _ = sjson.Set(chunk, "choices.0.delta.reasoning_content", "\n\n")

	case "response.output_item.added":
		item := root.Get("item")
		if !item.Exists() || item.Get("type").String() != "function_call" {
			return nil, nil
		}
		st.FunctionCallIndex++
		st.HasReceivedArgumentsDelta = false
		st.HasToolCallAnnounced = true

		fcItem := `{"index":0,"id":"","type":"function","function":{"name":"","arguments":""}}`
		fcItem, _ = sjson.Set(fcItem, "index", st.FunctionCallIndex)
		fcItem, _ = sjson.Set(fcItem, "id", item.Get("call_id").String())
		name := item.Get("name").String()
		if orig, ok := reverseMap[name]; ok {
			name = orig
		}
		fcItem, _ = sjson.Set(fcItem, "function.name", name)

		chunk, _ = sjson.Set(chunk, "choices.0.delta.role", "assistant")
		chunk, _ = sjson.SetRaw(chunk, "choices.0.delta.tool_calls", `[]`)
		chunk, _ = sjson.SetRaw(chunk, "choices.0.delta.tool_calls.-1", fcItem)

	case "response.function_call_arguments.delta":
		st.HasReceivedArgumentsDelta = true
		fcItem := `{"index":0,"function":{"arguments":""}}`
		fcItem, _ = sjson.Set(fcItem, "index", st.FunctionCallIndex)
		fcItem, _ = sjson.Set(fcItem, "function.arguments", root.Get("delta").String())
		chunk, _ = sjson.SetRaw(chunk, "choices.0.delta.tool_calls", `[]`)
		chunk, _ = sjson.SetRaw(chunk, "choices.0.delta.tool_calls.-1", fcItem)

	case "response.function_call_arguments.done":
		if st.HasReceivedArgumentsDelta {
			return nil, nil
		}
		fcItem := `{"index":0,"function":{"arguments":""}}`
		fcItem, _ = sjson.Set(fcItem, "index", st.FunctionCallIndex)
		fcItem, _ = sjson.Set(fcItem, "function.arguments", root.Get("arguments").String())
		chunk, _ = sjson.SetRaw(chunk, "choices.0.delta.tool_calls", `[]`)
		chunk, _ = sjson.SetRaw(chunk, "choices.0.delta.tool_calls.-1", fcItem)

	case "response.output_item.done":
		item := root.Get("item")
		if !item.Exists() || item.Get("type").String() != "function_call" {
			return nil, nil
		}
		if st.HasToolCallAnnounced {
			st.HasToolCallAnnounced = false
			return nil, nil
		}
		// Fallback: model skipped output_item.added
		st.FunctionCallIndex++
		fcItem := `{"index":0,"id":"","type":"function","function":{"name":"","arguments":""}}`
		fcItem, _ = sjson.Set(fcItem, "index", st.FunctionCallIndex)
		fcItem, _ = sjson.Set(fcItem, "id", item.Get("call_id").String())
		name := item.Get("name").String()
		if orig, ok := reverseMap[name]; ok {
			name = orig
		}
		fcItem, _ = sjson.Set(fcItem, "function.name", name)
		fcItem, _ = sjson.Set(fcItem, "function.arguments", item.Get("arguments").String())
		chunk, _ = sjson.Set(chunk, "choices.0.delta.role", "assistant")
		chunk, _ = sjson.SetRaw(chunk, "choices.0.delta.tool_calls", `[]`)
		chunk, _ = sjson.SetRaw(chunk, "choices.0.delta.tool_calls.-1", fcItem)

	case "response.completed":
		finishReason := "stop"
		if st.FunctionCallIndex >= 0 {
			finishReason = "tool_calls"
		}
		chunk, _ = sjson.Set(chunk, "choices.0.finish_reason", finishReason)
		st.Finished = true

	default:
		return nil, nil
	}

	return []string{chatSSE(chunk)}, nil
}

// TransformResponseNonStream converts a Responses API JSON response to Chat Completions JSON.
func (Transformer) TransformResponseNonStream(ctx context.Context, modelName string, originalRequestRawJSON, requestRawJSON, rawJSON []byte, _ *any) ([]byte, error) {
	line := bytes.TrimSpace(rawJSON)
	if gjson.ValidBytes(line) {
		root := gjson.ParseBytes(line)
		switch {
		case root.Get("type").String() == "response.completed":
			return buildResponseFromObject(modelName, originalRequestRawJSON, root.Get("response"))
		case root.IsObject() && (root.Get("output").Exists() || root.Get("usage").Exists()):
			return buildResponseFromObject(modelName, originalRequestRawJSON, root)
		}
	}

	return buildResponseFromTranscript(ctx, modelName, originalRequestRawJSON, rawJSON)
}

// --- Non-stream helpers ---

func buildResponseFromObject(modelName string, originalRequestRawJSON []byte, response gjson.Result) ([]byte, error) {
	if !response.Exists() || !response.IsObject() {
		return nil, fmt.Errorf("empty response")
	}

	template := `{"id":"","object":"chat.completion","created":0,"model":"","choices":[{"index":0,"message":{"role":"assistant","content":null,"reasoning_content":null,"tool_calls":null},"finish_reason":null}]}`

	if m := response.Get("model"); m.Exists() {
		template, _ = sjson.Set(template, "model", m.String())
	} else if modelName != "" {
		template, _ = sjson.Set(template, "model", modelName)
	}

	if v := response.Get("created_at"); v.Exists() {
		template, _ = sjson.Set(template, "created", v.Int())
	} else {
		template, _ = sjson.Set(template, "created", time.Now().Unix())
	}

	if v := response.Get("id"); v.Exists() {
		template, _ = sjson.Set(template, "id", v.String())
	}

	// Usage
	if usage := response.Get("usage"); usage.Exists() {
		if v := usage.Get("output_tokens"); v.Exists() {
			template, _ = sjson.Set(template, "usage.completion_tokens", v.Int())
		}
		if v := usage.Get("total_tokens"); v.Exists() {
			template, _ = sjson.Set(template, "usage.total_tokens", v.Int())
		}
		if v := usage.Get("input_tokens"); v.Exists() {
			template, _ = sjson.Set(template, "usage.prompt_tokens", v.Int())
		}
		if v := usage.Get("input_tokens_details.cached_tokens"); v.Exists() {
			template, _ = sjson.Set(template, "usage.prompt_tokens_details.cached_tokens", v.Int())
		}
		if v := usage.Get("output_tokens_details.reasoning_tokens"); v.Exists() {
			template, _ = sjson.Set(template, "usage.completion_tokens_details.reasoning_tokens", v.Int())
		}
	}

	// Process output array
	reverseMap := buildReverseMapFromOriginalChatCompletions(originalRequestRawJSON)
	output := response.Get("output")
	var toolCalls []string
	if output.IsArray() {
		var contentBuf strings.Builder
		var reasoningBuf strings.Builder

		for _, item := range output.Array() {
			switch item.Get("type").String() {
			case "reasoning":
				if summary := item.Get("summary"); summary.IsArray() {
					for _, si := range summary.Array() {
						if si.Get("type").String() == "summary_text" {
							reasoningBuf.WriteString(si.Get("text").String())
						}
					}
				}
			case "message":
				if content := item.Get("content"); content.IsArray() {
					for _, ci := range content.Array() {
						if ci.Get("type").String() == "output_text" {
							contentBuf.WriteString(ci.Get("text").String())
						}
					}
				}
			case "function_call":
				fc := `{"id":"","type":"function","function":{"name":"","arguments":""}}`
				if v := item.Get("call_id"); v.Exists() {
					fc, _ = sjson.Set(fc, "id", v.String())
				}
				if v := item.Get("name"); v.Exists() {
					name := v.String()
					if orig, ok := reverseMap[name]; ok {
						name = orig
					}
					fc, _ = sjson.Set(fc, "function.name", name)
				}
				if v := item.Get("arguments"); v.Exists() {
					fc, _ = sjson.Set(fc, "function.arguments", v.String())
				}
				toolCalls = append(toolCalls, fc)
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
	}

	// Finish reason
	if status := response.Get("status"); status.Exists() && status.String() == "completed" {
		if len(toolCalls) > 0 {
			template, _ = sjson.Set(template, "choices.0.finish_reason", "tool_calls")
		} else {
			template, _ = sjson.Set(template, "choices.0.finish_reason", "stop")
		}
	}

	return []byte(template), nil
}

func buildResponseFromTranscript(ctx context.Context, modelName string, originalRequestRawJSON, rawJSON []byte) ([]byte, error) {
	scanner := bufio.NewScanner(bytes.NewReader(rawJSON))
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	var state any
	var allChunks []string

	for scanner.Scan() {
		outs, err := (Transformer{}).TransformResponseStream(ctx, modelName, originalRequestRawJSON, nil, scanner.Bytes(), &state)
		if err != nil {
			return nil, err
		}
		allChunks = append(allChunks, outs...)
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	if len(allChunks) == 0 {
		return nil, fmt.Errorf("failed to parse responses transcript")
	}

	// Merge all streaming chunks into one non-stream response
	return mergeChunksToNonStream(allChunks)
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

func mergeChunksToNonStream(chunks []string) ([]byte, error) {
	template := `{"id":"","object":"chat.completion","created":0,"model":"","choices":[{"index":0,"message":{"role":"assistant","content":null,"reasoning_content":null,"tool_calls":null},"finish_reason":null}]}`

	var contentBuf strings.Builder
	var reasoningBuf strings.Builder
	var toolCalls []string
	var finishReason string

	for _, chunk := range chunks {
		// Strip "data: " prefix and trailing newlines
		data := strings.TrimSpace(chunk)
		if strings.HasPrefix(data, "data: ") {
			data = strings.TrimPrefix(data, "data: ")
		}
		data = strings.TrimSpace(data)
		if data == "" || !gjson.Valid(data) {
			continue
		}

		root := gjson.Parse(data)

		if id := root.Get("id"); id.Exists() && id.String() != "" {
			template, _ = sjson.Set(template, "id", id.String())
		}
		if m := root.Get("model"); m.Exists() && m.String() != "" {
			template, _ = sjson.Set(template, "model", m.String())
		}
		if c := root.Get("created"); c.Exists() && c.Int() > 0 {
			template, _ = sjson.Set(template, "created", c.Int())
		}

		// Usage
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
		if !delta.Exists() {
			continue
		}

		if v := delta.Get("content"); v.Exists() && v.Type == gjson.String {
			contentBuf.WriteString(v.String())
		}
		if v := delta.Get("reasoning_content"); v.Exists() && v.Type == gjson.String {
			reasoningBuf.WriteString(v.String())
		}
		if tcs := delta.Get("tool_calls"); tcs.Exists() && tcs.IsArray() {
			for _, tc := range tcs.Array() {
				// If it has id+name, it's a new tool call announcement
				if tc.Get("id").Exists() && tc.Get("function.name").Exists() {
					idx := int(tc.Get("index").Int())
					for len(toolCalls) <= idx {
						toolCalls = append(toolCalls, `{"id":"","type":"function","function":{"name":"","arguments":""}}`)
					}
					entry := toolCalls[idx]
					entry, _ = sjson.Set(entry, "id", tc.Get("id").String())
					entry, _ = sjson.Set(entry, "function.name", tc.Get("function.name").String())
					toolCalls[idx] = entry
				}
				// Accumulate arguments
				if args := tc.Get("function.arguments"); args.Exists() && args.String() != "" {
					idx := int(tc.Get("index").Int())
					for len(toolCalls) <= idx {
						toolCalls = append(toolCalls, `{"id":"","type":"function","function":{"name":"","arguments":""}}`)
					}
					entry := toolCalls[idx]
					existing := gjson.Get(entry, "function.arguments").String()
					entry, _ = sjson.Set(entry, "function.arguments", existing+args.String())
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

// --- SSE helper ---

func chatSSE(jsonStr string) string {
	return "data: " + jsonStr + "\n\n"
}

// --- Tool name shortening ---

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
	m := map[string]string{}

	baseCandidate := func(n string) string {
		if len(n) <= limit {
			return n
		}
		if strings.HasPrefix(n, "mcp__") {
			idx := strings.LastIndex(n, "__")
			if idx > 0 {
				cand := "mcp__" + n[idx+2:]
				if len(cand) > limit {
					cand = cand[:limit]
				}
				return cand
			}
		}
		return n[:limit]
	}

	makeUnique := func(cand string) string {
		if _, ok := used[cand]; !ok {
			return cand
		}
		base := cand
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

	for _, n := range names {
		uniq := makeUnique(baseCandidate(n))
		used[uniq] = struct{}{}
		m[n] = uniq
	}
	return m
}

func buildShortNameMapFromChatTools(rawJSON []byte) map[string]string {
	tools := gjson.GetBytes(rawJSON, "tools")
	if !tools.IsArray() || len(tools.Array()) == 0 {
		return map[string]string{}
	}
	var names []string
	for _, t := range tools.Array() {
		if t.Get("type").String() == "function" {
			if v := t.Get("function.name"); v.Exists() {
				names = append(names, v.String())
			}
		}
	}
	if len(names) == 0 {
		return map[string]string{}
	}
	return buildShortNameMap(names)
}

func buildReverseMapFromOriginalChatCompletions(original []byte) map[string]string {
	m := buildShortNameMapFromChatTools(original)
	rev := make(map[string]string, len(m))
	for orig, short := range m {
		rev[short] = orig
	}
	return rev
}
