package gemini

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"clisimplehub/internal/transformer/shared"
)

type Transformer struct{}

func (Transformer) TargetInterfaceType() string { return "gemini" }

func (Transformer) TargetPath(isStreaming bool, upstreamModel string) string {
	model := strings.TrimSpace(upstreamModel)
	if model == "" {
		model = "gemini-1.5-pro"
	}
	if isStreaming {
		return "/v1beta/models/" + model + ":streamGenerateContent"
	}
	return "/v1beta/models/" + model + ":generateContent"
}

func (Transformer) OutputContentType(isStreaming bool) string {
	if isStreaming {
		return "text/event-stream"
	}
	return "application/json"
}

func (Transformer) TransformRequest(modelName string, rawJSON []byte, _ bool) ([]byte, error) {
	root, err := shared.DecodeJSONMap(rawJSON)
	if err != nil {
		return nil, err
	}

	out := map[string]any{
		"contents": []any{},
	}

	if system := shared.BuildClaudeSystemText(root["system"]); strings.TrimSpace(system) != "" {
		out["system_instruction"] = map[string]any{
			"role": "user",
			"parts": []any{
				map[string]any{"text": system},
			},
		}
	}

	contents := make([]any, 0)
	if messages, ok := root["messages"].([]any); ok {
		for _, m := range messages {
			msg, _ := m.(map[string]any)
			role := strings.TrimSpace(shared.StringFromAny(msg["role"]))
			if role == "" {
				continue
			}
			if role == "assistant" {
				role = "model"
			}

			content := msg["content"]
			contentJSON := map[string]any{
				"role":  role,
				"parts": []any{},
			}

			appendPart := func(part any) {
				ps := contentJSON["parts"].([]any)
				contentJSON["parts"] = append(ps, part)
			}

			if s, ok := content.(string); ok {
				if strings.TrimSpace(s) != "" {
					appendPart(map[string]any{"text": s})
				}
				if len(contentJSON["parts"].([]any)) > 0 {
					contents = append(contents, contentJSON)
				}
				continue
			}

			parts, ok := content.([]any)
			if !ok {
				continue
			}
			for _, p := range parts {
				part, _ := p.(map[string]any)
				if part == nil {
					continue
				}
				switch strings.TrimSpace(shared.StringFromAny(part["type"])) {
				case "text":
					text := shared.StringFromAny(part["text"])
					if strings.TrimSpace(text) != "" {
						appendPart(map[string]any{"text": text})
					}
				case "tool_use":
					name := shared.StringFromAny(part["name"])
					if strings.TrimSpace(name) == "" {
						continue
					}
					args, ok := part["input"].(map[string]any)
					if !ok {
						args = map[string]any{}
					}
					appendPart(map[string]any{
						"functionCall": map[string]any{
							"name": name,
							"args": args,
						},
					})
				case "tool_result":
					callID := shared.StringFromAny(part["tool_use_id"])
					if strings.TrimSpace(callID) == "" {
						continue
					}
					appendPart(map[string]any{
						"functionResponse": map[string]any{
							"name": callID,
							"response": map[string]any{
								"result": part["content"],
							},
						},
					})
				}
			}

			if len(contentJSON["parts"].([]any)) > 0 {
				contents = append(contents, contentJSON)
			}
		}
	}

	out["contents"] = contents

	if v, ok := root["temperature"]; ok {
		out = setNested(out, []string{"generationConfig", "temperature"}, v)
	}
	if v, ok := root["top_p"]; ok {
		out = setNested(out, []string{"generationConfig", "topP"}, v)
	}
	if v, ok := root["top_k"]; ok {
		out = setNested(out, []string{"generationConfig", "topK"}, v)
	}

	if tools := convertClaudeToolsToGeminiTools(root["tools"]); len(tools) > 0 {
		out["tools"] = tools
	}

	_ = modelName // model is in URL; keep signature for consistency
	return json.Marshal(out)
}

func (Transformer) TransformResponseStream(_ context.Context, _ string, _ []byte, _ []byte, rawLine []byte, state *any) ([]string, error) {
	if state == nil {
		return nil, fmt.Errorf("nil transformer state")
	}
	if *state == nil {
		*state = &geminiToClaudeStreamState{
			responseIndex: 0,
		}
	}
	s := (*state).(*geminiToClaudeStreamState)

	line := bytes.TrimSpace(rawLine)
	if len(line) == 0 {
		return nil, nil
	}

	payload := line
	if p, ok := shared.SSEDataPayload(line); ok {
		payload = p
	}

	if bytes.Equal(payload, []byte("[DONE]")) {
		if s.sentMessageStop || !s.hasContent {
			return nil, nil
		}
		s.sentMessageStop = true
		return []string{"event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n\n"}, nil
	}

	root, err := shared.DecodeJSONMap(payload)
	if err != nil {
		return nil, nil
	}

	var outputs []string

	if !s.started {
		s.started = true
		s.messageID = strings.TrimSpace(shared.StringFromAny(root["responseId"]))
		if s.messageID == "" {
			s.messageID = "msg_" + shared.RandomSuffix()
		}
		s.model = strings.TrimSpace(shared.StringFromAny(root["modelVersion"]))
		if s.model == "" {
			s.model = "gemini"
		}
		outputs = append(outputs, s.eventMessageStart())
	}

	cands, _ := root["candidates"].([]any)
	if len(cands) > 0 {
		c0, _ := cands[0].(map[string]any)
		if c0 != nil {
			content, _ := c0["content"].(map[string]any)
			if content != nil {
				parts, _ := content["parts"].([]any)
				for _, p := range parts {
					part, _ := p.(map[string]any)
					if part == nil {
						continue
					}
					if txt := shared.StringFromAny(part["text"]); strings.TrimSpace(txt) != "" {
						outputs = append(outputs, s.ensureTextBlockStarted()...)
						outputs = append(outputs, s.eventTextDelta(txt))
						s.hasContent = true
						continue
					}
					if fc, ok := part["functionCall"].(map[string]any); ok && fc != nil {
						s.usedTool = true
						name := shared.StringFromAny(fc["name"])
						args, _ := json.Marshal(fc["args"])
						if len(args) == 0 {
							args = []byte("{}")
						}
						outputs = append(outputs, s.ensureToolBlockStarted(name)...)
						outputs = append(outputs, s.eventToolArgsDelta(string(args)))
						s.hasContent = true
						continue
					}
				}
			}

			if finishReason := shared.StringFromAny(c0["finishReason"]); strings.TrimSpace(finishReason) != "" {
				outputs = append(outputs, s.finish(extractGeminiUsage(root))...)
			}
		}
	}

	return outputs, nil
}

func (Transformer) TransformResponseNonStream(_ context.Context, _ string, _ []byte, _ []byte, rawJSON []byte, _ *any) ([]byte, error) {
	root, err := shared.DecodeJSONMap(rawJSON)
	if err != nil {
		return nil, err
	}

	id := strings.TrimSpace(shared.StringFromAny(root["responseId"]))
	if id == "" {
		id = "msg_" + shared.RandomSuffix()
	}
	model := strings.TrimSpace(shared.StringFromAny(root["modelVersion"]))
	if model == "" {
		model = "gemini"
	}

	var contentBlocks []any
	usedTool := false

	cands, _ := root["candidates"].([]any)
	if len(cands) > 0 {
		c0, _ := cands[0].(map[string]any)
		if c0 != nil {
			content, _ := c0["content"].(map[string]any)
			if content != nil {
				parts, _ := content["parts"].([]any)
				for _, p := range parts {
					part, _ := p.(map[string]any)
					if part == nil {
						continue
					}
					if txt := shared.StringFromAny(part["text"]); strings.TrimSpace(txt) != "" {
						contentBlocks = append(contentBlocks, map[string]any{"type": "text", "text": txt})
					}
					if fc, ok := part["functionCall"].(map[string]any); ok && fc != nil {
						usedTool = true
						name := shared.StringFromAny(fc["name"])
						args, ok := fc["args"].(map[string]any)
						if !ok {
							args = map[string]any{}
						}
						contentBlocks = append(contentBlocks, map[string]any{
							"type":  "tool_use",
							"id":    "toolu_" + shared.RandomSuffix(),
							"name":  name,
							"input": args,
						})
					}
				}
			}
		}
	}

	usage := extractGeminiUsage(root)
	stopReason := "end_turn"
	if usedTool {
		stopReason = "tool_use"
	}

	out := map[string]any{
		"id":            id,
		"type":          "message",
		"role":          "assistant",
		"model":         model,
		"stop_reason":   stopReason,
		"stop_sequence": nil,
		"content":       contentBlocks,
		"usage":         usage,
	}
	return json.Marshal(out)
}

type geminiToClaudeStreamState struct {
	started bool

	messageID string
	model     string

	responseIndex int
	responseType  int // 0 none, 1 text, 3 tool

	hasContent     bool
	usedTool       bool
	sentMessageStop bool

	toolBlockName string
	toolBlockID   string
}

func (s *geminiToClaudeStreamState) eventMessageStart() string {
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
	return shared.SSEEvent("message_start", msg) + "\n"
}

func (s *geminiToClaudeStreamState) ensureTextBlockStarted() []string {
	if s.responseType == 1 {
		return nil
	}
	var outputs []string
	if s.responseType == 3 {
		outputs = append(outputs, shared.SSEEvent("content_block_stop", map[string]any{"type": "content_block_stop", "index": s.responseIndex})+"\n")
		s.responseIndex++
	}
	s.responseType = 1
	outputs = append(outputs, shared.SSEEvent("content_block_start", map[string]any{
		"type": "content_block_start",
		"index": s.responseIndex,
		"content_block": map[string]any{
			"type": "text",
			"text": "",
		},
	})+"\n")
	return outputs
}

func (s *geminiToClaudeStreamState) eventTextDelta(text string) string {
	return shared.SSEEvent("content_block_delta", map[string]any{
		"type":  "content_block_delta",
		"index": s.responseIndex,
		"delta": map[string]any{
			"type": "text_delta",
			"text": text,
		},
	}) + "\n"
}

func (s *geminiToClaudeStreamState) ensureToolBlockStarted(name string) []string {
	if s.responseType == 3 && strings.TrimSpace(name) == "" {
		return nil
	}
	var outputs []string
	if s.responseType != 0 {
		outputs = append(outputs, shared.SSEEvent("content_block_stop", map[string]any{"type": "content_block_stop", "index": s.responseIndex})+"\n")
		s.responseIndex++
	}

	s.responseType = 3
	s.toolBlockName = name
	s.toolBlockID = "toolu_" + shared.RandomSuffix()

	outputs = append(outputs, shared.SSEEvent("content_block_start", map[string]any{
		"type": "content_block_start",
		"index": s.responseIndex,
		"content_block": map[string]any{
			"type":  "tool_use",
			"id":    s.toolBlockID,
			"name":  name,
			"input": map[string]any{},
		},
	})+"\n")
	return outputs
}

func (s *geminiToClaudeStreamState) eventToolArgsDelta(partialJSON string) string {
	return shared.SSEEvent("content_block_delta", map[string]any{
		"type":  "content_block_delta",
		"index": s.responseIndex,
		"delta": map[string]any{
			"type":         "input_json_delta",
			"partial_json": partialJSON,
		},
	}) + "\n"
}

func (s *geminiToClaudeStreamState) finish(usage map[string]any) []string {
	if s.sentMessageStop {
		return nil
	}
	s.sentMessageStop = true

	var outputs []string
	if s.responseType != 0 {
		outputs = append(outputs, shared.SSEEvent("content_block_stop", map[string]any{"type": "content_block_stop", "index": s.responseIndex})+"\n")
	}
	stopReason := "end_turn"
	if s.usedTool {
		stopReason = "tool_use"
	}
	outputs = append(outputs, shared.SSEEvent("message_delta", map[string]any{
		"type": "message_delta",
		"delta": map[string]any{
			"stop_reason":   stopReason,
			"stop_sequence": nil,
		},
		"usage": usage,
	})+"\n")
	outputs = append(outputs, "event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n\n")
	return outputs
}

func extractGeminiUsage(root map[string]any) map[string]any {
	usageMeta, _ := root["usageMetadata"].(map[string]any)
	if usageMeta == nil {
		return map[string]any{"input_tokens": 0, "output_tokens": 0}
	}
	return map[string]any{
		"input_tokens":  shared.IntFromAny(usageMeta["promptTokenCount"]),
		"output_tokens": shared.IntFromAny(usageMeta["candidatesTokenCount"]),
	}
}

func convertClaudeToolsToGeminiTools(v any) []any {
	toolsArr, ok := v.([]any)
	if !ok {
		return nil
	}
	decls := make([]any, 0, len(toolsArr))
	for _, t := range toolsArr {
		tool, _ := t.(map[string]any)
		if tool == nil {
			continue
		}
		name := shared.StringFromAny(tool["name"])
		if strings.TrimSpace(name) == "" {
			continue
		}
		d := map[string]any{
			"name": name,
		}
		if desc := shared.StringFromAny(tool["description"]); strings.TrimSpace(desc) != "" {
			d["description"] = desc
		}
		if schema, ok := tool["input_schema"].(map[string]any); ok && len(schema) > 0 {
			d["parametersJsonSchema"] = schema
		}
		decls = append(decls, d)
	}
	if len(decls) == 0 {
		return nil
	}
	return []any{
		map[string]any{
			"functionDeclarations": decls,
		},
	}
}

func setNested(root map[string]any, path []string, value any) map[string]any {
	if len(path) == 0 {
		return root
	}
	curr := root
	for i := 0; i < len(path)-1; i++ {
		key := path[i]
		next, ok := curr[key].(map[string]any)
		if !ok || next == nil {
			next = map[string]any{}
			curr[key] = next
		}
		curr = next
	}
	curr[path[len(path)-1]] = value
	return root
}

