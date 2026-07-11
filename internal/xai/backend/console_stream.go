package backend

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/tidwall/gjson"
)

// ConsoleStreamToChatCompletions 将 console.x.ai Responses SSE 转为 OpenAI chat.completions SSE。
// 写出 chat.completion.chunk 事件，并以 data: [DONE] 结束。
func ConsoleStreamToChatCompletions(r io.Reader, w io.Writer, model string) error {
	if r == nil || w == nil {
		return fmt.Errorf("nil reader/writer")
	}
	model = strings.TrimSpace(model)
	if model == "" {
		model = "grok-4.3-console"
	}
	id := "chatcmpl-" + strings.ReplaceAll(uuid.NewString(), "-", "")[:24]
	created := time.Now().Unix()
	flusher, _ := w.(interface{ Flush() })

	writeChunk := func(obj map[string]any) error {
		b, err := json.Marshal(obj)
		if err != nil {
			return err
		}
		if _, err := fmt.Fprintf(w, "data: %s\n\n", b); err != nil {
			return err
		}
		if flusher != nil {
			flusher.Flush()
		}
		return nil
	}

	// role 首包
	if err := writeChunk(map[string]any{
		"id":      id,
		"object":  "chat.completion.chunk",
		"created": created,
		"model":   model,
		"choices": []map[string]any{{
			"index": 0,
			"delta": map[string]any{"role": "assistant"},
		}},
	}); err != nil {
		return err
	}

	sc := bufio.NewScanner(r)
	// 放大 buffer 以容纳 encrypted reasoning
	buf := make([]byte, 0, 64*1024)
	sc.Buffer(buf, 4*1024*1024)

	var currentEvent string
	var fullText strings.Builder
	var usage map[string]any

	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "event:") {
			currentEvent = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
			continue
		}
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "" || data == "[DONE]" {
			continue
		}
		if currentEvent == "response.output_text.delta" {
			delta := gjson.Get(data, "delta").String()
			if delta == "" {
				continue
			}
			fullText.WriteString(delta)
			if err := writeChunk(map[string]any{
				"id":      id,
				"object":  "chat.completion.chunk",
				"created": created,
				"model":   model,
				"choices": []map[string]any{{
					"index": 0,
					"delta": map[string]any{"content": delta},
				}},
			}); err != nil {
				return err
			}
		} else if currentEvent == "response.completed" {
			if u := gjson.Get(data, "response.usage"); u.Exists() {
				usage = map[string]any{}
				_ = json.Unmarshal([]byte(u.Raw), &usage)
			}
		} else if currentEvent == "error" {
			msg := gjson.Get(data, "message").String()
			if msg == "" {
				msg = data
			}
			return fmt.Errorf("console api error: %s", msg)
		}
		currentEvent = ""
	}
	if err := sc.Err(); err != nil {
		return err
	}

	finish := map[string]any{
		"id":      id,
		"object":  "chat.completion.chunk",
		"created": created,
		"model":   model,
		"choices": []map[string]any{{
			"index":         0,
			"delta":         map[string]any{},
			"finish_reason": "stop",
		}},
	}
	if len(usage) > 0 {
		finish["usage"] = normalizeConsoleUsage(usage)
	}
	if err := writeChunk(finish); err != nil {
		return err
	}
	if _, err := io.WriteString(w, "data: [DONE]\n\n"); err != nil {
		return err
	}
	if flusher != nil {
		flusher.Flush()
	}
	_ = fullText
	return nil
}

// AggregateConsoleStream 聚合 console SSE 为非流式 chat.completion JSON。
func AggregateConsoleStream(r io.Reader, model string) ([]byte, error) {
	body, err := io.ReadAll(r)
	if err != nil {
		return nil, err
	}
	sc := bufio.NewScanner(bytes.NewReader(body))
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	var currentEvent string
	var text strings.Builder
	var usage map[string]any
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "event:") {
			currentEvent = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
			continue
		}
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "" || data == "[DONE]" {
			continue
		}
		if currentEvent == "response.output_text.delta" {
			if d := gjson.Get(data, "delta").String(); d != "" {
				text.WriteString(d)
			}
		} else if currentEvent == "response.completed" {
			if u := gjson.Get(data, "response.usage"); u.Exists() {
				usage = map[string]any{}
				_ = json.Unmarshal([]byte(u.Raw), &usage)
			}
		} else if currentEvent == "error" {
			msg := gjson.Get(data, "message").String()
			if msg == "" {
				msg = data
			}
			return nil, fmt.Errorf("console api error: %s", msg)
		}
		currentEvent = ""
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	model = strings.TrimSpace(model)
	if model == "" {
		model = "grok-4.3-console"
	}
	id := "chatcmpl-" + strings.ReplaceAll(uuid.NewString(), "-", "")[:24]
	out := map[string]any{
		"id":      id,
		"object":  "chat.completion",
		"created": time.Now().Unix(),
		"model":   model,
		"choices": []map[string]any{{
			"index": 0,
			"message": map[string]any{
				"role":    "assistant",
				"content": text.String(),
			},
			"finish_reason": "stop",
		}},
	}
	if len(usage) > 0 {
		out["usage"] = normalizeConsoleUsage(usage)
	}
	return json.Marshal(out)
}

func normalizeConsoleUsage(u map[string]any) map[string]any {
	// Responses usage → chat usage 字段名
	in := intFromAny(u["input_tokens"])
	if in == 0 {
		in = intFromAny(u["prompt_tokens"])
	}
	out := intFromAny(u["output_tokens"])
	if out == 0 {
		out = intFromAny(u["completion_tokens"])
	}
	total := intFromAny(u["total_tokens"])
	if total == 0 {
		total = in + out
	}
	return map[string]any{
		"prompt_tokens":     in,
		"completion_tokens": out,
		"total_tokens":      total,
	}
}

func intFromAny(v any) int {
	switch t := v.(type) {
	case float64:
		return int(t)
	case int:
		return t
	case int64:
		return int(t)
	case json.Number:
		i, _ := t.Int64()
		return int(i)
	default:
		return 0
	}
}

// ConsoleStreamToResponses 将上游 console SSE 过滤/透传为 OpenAI Responses SSE。
// 保留文本 delta 与 completed；忽略 encrypted reasoning 内容体细节。
func ConsoleStreamToResponses(r io.Reader, w io.Writer, model string) error {
	if r == nil || w == nil {
		return fmt.Errorf("nil reader/writer")
	}
	model = strings.TrimSpace(model)
	if model == "" {
		model = "grok-4.3-console"
	}
	flusher, _ := w.(interface{ Flush() })
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	var currentEvent string
	var text strings.Builder
	respID := "resp_" + strings.ReplaceAll(uuid.NewString(), "-", "")[:20]
	created := time.Now().Unix()

	writeSSE := func(event string, obj any) error {
		b, err := json.Marshal(obj)
		if err != nil {
			return err
		}
		if event != "" {
			if _, err := fmt.Fprintf(w, "event: %s\n", event); err != nil {
				return err
			}
		}
		if _, err := fmt.Fprintf(w, "data: %s\n\n", b); err != nil {
			return err
		}
		if flusher != nil {
			flusher.Flush()
		}
		return nil
	}

	_ = writeSSE("response.created", map[string]any{
		"type": "response.created",
		"response": map[string]any{
			"id":      respID,
			"object":  "response",
			"created": created,
			"model":   model,
			"status":  "in_progress",
		},
	})

	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "event:") {
			currentEvent = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
			continue
		}
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "" || data == "[DONE]" {
			continue
		}
		switch currentEvent {
		case "response.output_text.delta":
			delta := gjson.Get(data, "delta").String()
			if delta == "" {
				continue
			}
			text.WriteString(delta)
			if err := writeSSE("response.output_text.delta", map[string]any{
				"type":  "response.output_text.delta",
				"delta": delta,
			}); err != nil {
				return err
			}
		case "response.completed":
			usageRaw := gjson.Get(data, "response.usage")
			var usage any
			if usageRaw.Exists() {
				_ = json.Unmarshal([]byte(usageRaw.Raw), &usage)
			}
			if err := writeSSE("response.completed", map[string]any{
				"type": "response.completed",
				"response": map[string]any{
					"id":     respID,
					"object": "response",
					"model":  model,
					"status": "completed",
					"output": []map[string]any{{
						"type": "message",
						"role": "assistant",
						"content": []map[string]any{{
							"type": "output_text",
							"text": text.String(),
						}},
					}},
					"usage": usage,
				},
			}); err != nil {
				return err
			}
		case "error":
			msg := gjson.Get(data, "message").String()
			if msg == "" {
				msg = data
			}
			return fmt.Errorf("console api error: %s", msg)
		}
		currentEvent = ""
	}
	return sc.Err()
}

// AggregateConsoleStreamToResponses 聚合为非流式 Responses JSON。
func AggregateConsoleStreamToResponses(r io.Reader, model string) ([]byte, error) {
	chatRaw, err := AggregateConsoleStream(r, model)
	if err != nil {
		return nil, err
	}
	var chat map[string]any
	if err := json.Unmarshal(chatRaw, &chat); err != nil {
		return nil, err
	}
	text := ""
	if choices, ok := chat["choices"].([]any); ok && len(choices) > 0 {
		if c0, ok := choices[0].(map[string]any); ok {
			if msg, ok := c0["message"].(map[string]any); ok {
				text, _ = msg["content"].(string)
			}
		}
	}
	model = strings.TrimSpace(model)
	if model == "" {
		model = "grok-4.3-console"
	}
	out := map[string]any{
		"id":     "resp_" + strings.ReplaceAll(uuid.NewString(), "-", "")[:20],
		"object": "response",
		"model":  model,
		"status": "completed",
		"output": []map[string]any{{
			"type": "message",
			"role": "assistant",
			"content": []map[string]any{{
				"type": "output_text",
				"text": text,
			}},
		}},
	}
	if u, ok := chat["usage"]; ok {
		out["usage"] = u
	}
	return json.Marshal(out)
}

// ConsoleStreamToAnthropic 将 console SSE 转为 Anthropic Messages SSE。
func ConsoleStreamToAnthropic(r io.Reader, w io.Writer, model string) error {
	if r == nil || w == nil {
		return fmt.Errorf("nil reader/writer")
	}
	model = strings.TrimSpace(model)
	if model == "" {
		model = "grok-4.3-console"
	}
	msgID := "msg_" + strings.ReplaceAll(uuid.NewString(), "-", "")[:20]
	flusher, _ := w.(interface{ Flush() })
	writeEvent := func(event string, obj any) error {
		b, err := json.Marshal(obj)
		if err != nil {
			return err
		}
		if _, err := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, b); err != nil {
			return err
		}
		if flusher != nil {
			flusher.Flush()
		}
		return nil
	}

	if err := writeEvent("message_start", map[string]any{
		"type": "message_start",
		"message": map[string]any{
			"id":            msgID,
			"type":          "message",
			"role":          "assistant",
			"model":         model,
			"content":       []any{},
			"stop_reason":   nil,
			"stop_sequence": nil,
			"usage":         map[string]any{"input_tokens": 0, "output_tokens": 0},
		},
	}); err != nil {
		return err
	}
	if err := writeEvent("content_block_start", map[string]any{
		"type":  "content_block_start",
		"index": 0,
		"content_block": map[string]any{
			"type": "text",
			"text": "",
		},
	}); err != nil {
		return err
	}

	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	var currentEvent string
	var outTokens int
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "event:") {
			currentEvent = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
			continue
		}
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "" || data == "[DONE]" {
			continue
		}
		if currentEvent == "response.output_text.delta" {
			delta := gjson.Get(data, "delta").String()
			if delta == "" {
				continue
			}
			outTokens += len([]rune(delta)) / 4
			if err := writeEvent("content_block_delta", map[string]any{
				"type":  "content_block_delta",
				"index": 0,
				"delta": map[string]any{"type": "text_delta", "text": delta},
			}); err != nil {
				return err
			}
		} else if currentEvent == "error" {
			msg := gjson.Get(data, "message").String()
			if msg == "" {
				msg = data
			}
			return fmt.Errorf("console api error: %s", msg)
		}
		currentEvent = ""
	}
	if err := sc.Err(); err != nil {
		return err
	}
	if err := writeEvent("content_block_stop", map[string]any{"type": "content_block_stop", "index": 0}); err != nil {
		return err
	}
	if err := writeEvent("message_delta", map[string]any{
		"type":  "message_delta",
		"delta": map[string]any{"stop_reason": "end_turn", "stop_sequence": nil},
		"usage": map[string]any{"output_tokens": outTokens},
	}); err != nil {
		return err
	}
	if err := writeEvent("message_stop", map[string]any{"type": "message_stop"}); err != nil {
		return err
	}
	return nil
}

// AggregateConsoleStreamToAnthropic 聚合为 Anthropic message JSON。
func AggregateConsoleStreamToAnthropic(r io.Reader, model string) ([]byte, error) {
	chatRaw, err := AggregateConsoleStream(r, model)
	if err != nil {
		return nil, err
	}
	var chat map[string]any
	if err := json.Unmarshal(chatRaw, &chat); err != nil {
		return nil, err
	}
	text := ""
	if choices, ok := chat["choices"].([]any); ok && len(choices) > 0 {
		if c0, ok := choices[0].(map[string]any); ok {
			if msg, ok := c0["message"].(map[string]any); ok {
				text, _ = msg["content"].(string)
			}
		}
	}
	model = strings.TrimSpace(model)
	if model == "" {
		model = "grok-4.3-console"
	}
	out := map[string]any{
		"id":    "msg_" + strings.ReplaceAll(uuid.NewString(), "-", "")[:20],
		"type":  "message",
		"role":  "assistant",
		"model": model,
		"content": []map[string]any{{
			"type": "text",
			"text": text,
		}},
		"stop_reason":   "end_turn",
		"stop_sequence": nil,
		"usage": map[string]any{
			"input_tokens":  0,
			"output_tokens": len([]rune(text)) / 4,
		},
	}
	return json.Marshal(out)
}
