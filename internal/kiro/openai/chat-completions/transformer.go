package chat_completions

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"clisimplehub/internal/kiro/claude"
	"clisimplehub/internal/kiro/converters"
	"clisimplehub/internal/transformer/shared"
)

// Transformer converts OpenAI Chat Completions requests/responses to/from AWS Kiro (CodeWhisperer) EventStream.
//
// Source interface: `chat` (`/v1/chat/completions`)
// Target interface: `kiro` (`/generateAssistantResponse`)
type Transformer struct {
	core *claude.Transformer
}

func NewTransformer() *Transformer {
	return &Transformer{core: claude.NewTransformer()}
}

func (t *Transformer) TargetInterfaceType() string { return "kiro" }

func (t *Transformer) TargetPath(_ bool, _ string) string { return "/generateAssistantResponse" }

func (t *Transformer) OutputContentType(isStreaming bool) string {
	if isStreaming {
		return "text/event-stream"
	}
	return "application/json"
}

// KiroAuthSource hooks (used by executor kiro auth applier).
func (t *Transformer) GetAccessToken() (string, error) {
	if t == nil || t.core == nil {
		return "", fmt.Errorf("kiro transformer not initialized")
	}
	return t.core.GetAccessToken()
}

func (t *Transformer) MachineID() string {
	if t == nil || t.core == nil {
		return ""
	}
	return t.core.MachineID()
}

func (t *Transformer) KiroUserAgentBase() string {
	if t == nil || t.core == nil {
		return ""
	}
	return t.core.KiroUserAgentBase()
}

func (t *Transformer) KiroVersion() string {
	if t == nil || t.core == nil {
		return ""
	}
	return t.core.KiroVersion()
}

func (t *Transformer) GetAPIURL() string {
	if t == nil || t.core == nil {
		return ""
	}
	return t.core.GetAPIURL()
}

func (t *Transformer) KiroProxyURL() string {
	if t == nil || t.core == nil {
		return ""
	}
	return t.core.KiroProxyURL()
}

func (t *Transformer) ForceRefreshKiroToken() error {
	if t == nil || t.core == nil {
		return fmt.Errorf("kiro transformer not initialized")
	}
	return t.core.ForceRefreshKiroToken()
}

// TransformRequest transforms an OpenAI Chat Completions request into a Kiro request payload.
func (t *Transformer) TransformRequest(modelName string, rawJSON []byte, stream bool) ([]byte, error) {
	if t == nil || t.core == nil {
		return nil, fmt.Errorf("nil transformer")
	}

	var openAIReq converters.OpenAIRequest
	if err := json.Unmarshal(rawJSON, &openAIReq); err != nil {
		return nil, fmt.Errorf("failed to parse openai chat request: %w", err)
	}

	openAIReq.Model = claude.GetKiroModelID(modelName)

	conversationID := uuid.NewString()

	profileArn := ""
	if am := t.core.GetAuthManager(); am != nil {
		profileArn = am.GetProfileArn()
	}

	kiroPayload, err := converters.OpenAIToKiro(&openAIReq, conversationID, profileArn)
	if err != nil {
		return nil, err
	}

	return shared.MarshalNoEscapeHTML(kiroPayload)
}

type streamState struct {
	openAIID                 string
	createdAt                int64
	model                    string
	started                  bool
	done                     bool
	finishReason             string
	contentBlockToToolCallIx map[int]int
	nextToolCallIx           int
	inner                    any
}

func (s *streamState) TokenUsage() (int, int) {
	st, _ := s.inner.(*claude.StreamState)
	if st == nil {
		return 0, 0
	}
	return st.TokenUsage()
}

func (s *streamState) ensureStarted(modelName string) {
	if s.started {
		return
	}
	s.started = true
	s.createdAt = time.Now().Unix()
	if strings.TrimSpace(s.openAIID) == "" {
		s.openAIID = "chatcmpl_" + shared.RandomSuffix()
	}
	s.model = strings.TrimSpace(modelName)
	if s.contentBlockToToolCallIx == nil {
		s.contentBlockToToolCallIx = make(map[int]int)
	}
}

func parseSSEEvent(out string) (event string, data []byte, ok bool) {
	var eventLine, dataLine string
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "event:") {
			eventLine = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
		}
		if strings.HasPrefix(line, "data:") {
			dataLine = strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		}
	}
	if eventLine == "" || dataLine == "" {
		return "", nil, false
	}
	return eventLine, []byte(dataLine), true
}

func openAISSE(payload any) (string, error) {
	b, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	return "data: " + string(b) + "\n\n", nil
}

func mapClaudeStopReasonToOpenAIFinishReason(stopReason string) string {
	switch strings.ToLower(strings.TrimSpace(stopReason)) {
	case "end_turn", "stop":
		return "stop"
	case "max_tokens":
		return "length"
	case "tool_use":
		return "tool_calls"
	default:
		return "stop"
	}
}

// TransformResponseStream converts Kiro EventStream bytes into OpenAI Chat Completions SSE chunks.
func (t *Transformer) TransformResponseStream(
	ctx context.Context,
	modelName string,
	originalRequestRawJSON, requestRawJSON, rawLine []byte,
	state *any,
) ([]string, error) {
	if state == nil {
		return nil, fmt.Errorf("nil transformer state")
	}

	if *state == nil {
		*state = &streamState{}
	}
	s, ok := (*state).(*streamState)
	if !ok || s == nil {
		*state = &streamState{}
		s = (*state).(*streamState)
	}

	// EOF finalization hook: executor may call with an empty chunk.
	if len(rawLine) == 0 {
		if s.done {
			return nil, nil
		}
		s.ensureStarted(modelName)
		finish := s.finishReason
		if finish == "" {
			finish = "stop"
		}
		final, _ := openAISSE(map[string]any{
			"id":      s.openAIID,
			"object":  "chat.completion.chunk",
			"created": s.createdAt,
			"model":   s.model,
			"choices": []any{map[string]any{
				"index":         0,
				"delta":         map[string]any{},
				"finish_reason": finish,
			}},
		})
		s.done = true
		return []string{final, "data: [DONE]\n\n"}, nil
	}

	claudeOuts, err := t.core.TransformResponseStream(ctx, modelName, originalRequestRawJSON, requestRawJSON, rawLine, &s.inner)
	if err != nil {
		return nil, err
	}

	var outs []string
	for _, o := range claudeOuts {
		event, data, ok := parseSSEEvent(o)
		if !ok {
			continue
		}

		switch event {
		case "message_start":
			s.ensureStarted(modelName)
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
			outs = append(outs, start)

		case "content_block_start":
			s.ensureStarted(modelName)
			root, err := shared.DecodeJSONMap(data)
			if err != nil {
				continue
			}
			cb, _ := root["content_block"].(map[string]any)
			if cb == nil || !strings.EqualFold(shared.StringFromAny(cb["type"]), "tool_use") {
				continue
			}
			cbIndex := shared.IntFromAny(root["index"])
			toolUseID := shared.StringFromAny(cb["id"])
			toolName := shared.StringFromAny(cb["name"])

			toolIx := s.nextToolCallIx
			s.nextToolCallIx++
			s.contentBlockToToolCallIx[cbIndex] = toolIx

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
							"id":    toolUseID,
							"type":  "function",
							"function": map[string]any{
								"name":      toolName,
								"arguments": "",
							},
						}},
					},
				}},
			})
			outs = append(outs, ev)

		case "content_block_delta":
			s.ensureStarted(modelName)
			root, err := shared.DecodeJSONMap(data)
			if err != nil {
				continue
			}
			delta, _ := root["delta"].(map[string]any)
			if delta == nil {
				continue
			}

			switch strings.TrimSpace(shared.StringFromAny(delta["type"])) {
			case "text_delta":
				text := shared.StringFromAny(delta["text"])
				if text == "" {
					continue
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
				outs = append(outs, ev)

			case "input_json_delta":
				cbIndex := shared.IntFromAny(root["index"])
				toolIx, ok := s.contentBlockToToolCallIx[cbIndex]
				if !ok {
					continue
				}
				partial := shared.StringFromAny(delta["partial_json"])
				if partial == "" {
					continue
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
				outs = append(outs, ev)
			}

		case "message_delta":
			s.ensureStarted(modelName)
			root, err := shared.DecodeJSONMap(data)
			if err != nil {
				continue
			}
			delta, _ := root["delta"].(map[string]any)
			stopReason := ""
			if delta != nil {
				stopReason = shared.StringFromAny(delta["stop_reason"])
			}
			finish := mapClaudeStopReasonToOpenAIFinishReason(stopReason)
			if finish != "" {
				s.finishReason = finish
			}
			ev, _ := openAISSE(map[string]any{
				"id":      s.openAIID,
				"object":  "chat.completion.chunk",
				"created": s.createdAt,
				"model":   s.model,
				"choices": []any{map[string]any{
					"index":         0,
					"delta":         map[string]any{},
					"finish_reason": finish,
				}},
			})
			outs = append(outs, ev)

		case "message_stop":
			if s.done {
				continue
			}
			s.done = true
			outs = append(outs, "data: [DONE]\n\n")
		}
	}

	return outs, nil
}

// TransformResponseNonStream converts a collected Kiro EventStream response into an OpenAI Chat Completions JSON response.
func (t *Transformer) TransformResponseNonStream(
	ctx context.Context,
	modelName string,
	originalRequestRawJSON, requestRawJSON, rawJSON []byte,
	state *any,
) ([]byte, error) {
	var inner any
	_, err := t.core.TransformResponseStream(ctx, modelName, originalRequestRawJSON, requestRawJSON, rawJSON, &inner)
	if err != nil {
		return nil, err
	}

	st, _ := inner.(*claude.StreamState)
	if st == nil {
		return nil, fmt.Errorf("failed to collect kiro stream: missing stream state")
	}
	if !st.Finished {
		_ = claude.FinishStream(st)
	}

	content := strings.TrimSpace(st.TextSoFar)
	var toolCalls []any
	for _, tc := range st.CollectedToolUses {
		if tc == nil {
			continue
		}
		callID := strings.TrimSpace(shared.StringFromAny(tc["id"]))
		name := strings.TrimSpace(shared.StringFromAny(tc["name"]))
		args := strings.TrimSpace(shared.StringFromAny(tc["args_raw"]))
		if args == "" {
			if b, err := json.Marshal(tc["input"]); err == nil && len(b) > 0 {
				args = string(b)
			} else {
				args = "{}"
			}
		}
		toolCalls = append(toolCalls, map[string]any{
			"id":   callID,
			"type": "function",
			"function": map[string]any{
				"name":      name,
				"arguments": args,
			},
		})
	}

	finish := strings.TrimSpace(st.FinishReason)
	if finish == "" {
		finish = "end_turn"
	}

	message := map[string]any{
		"role":    "assistant",
		"content": content,
	}
	if len(toolCalls) > 0 {
		message["tool_calls"] = toolCalls
	}

	out := map[string]any{
		"id":      "chatcmpl_" + shared.RandomSuffix(),
		"object":  "chat.completion",
		"created": time.Now().Unix(),
		"model":   strings.TrimSpace(modelName),
		"choices": []any{map[string]any{
			"index": 0,
			"message": message,
			"finish_reason": mapClaudeStopReasonToOpenAIFinishReason(finish),
		}},
		"usage": map[string]any{
			"prompt_tokens":     st.InputTokens,
			"completion_tokens": st.OutputTokens,
			"total_tokens":      st.InputTokens + st.OutputTokens,
		},
	}

	return shared.MarshalNoEscapeHTML(out)
}
