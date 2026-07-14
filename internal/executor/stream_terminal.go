package executor

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
)

var completedStreamEvents = map[string]struct{}{
	"response.completed": {},
	"response.done":      {},
	"message_stop":       {},
	"[DONE]":             {},
}

// ObserveStreamLine records protocol-level terminal markers from the source SSE
// stream. Status handling should use this structured state instead of scanning
// captured debug text after the request finishes.
func (r *ForwardResult) ObserveStreamLine(line []byte) {
	if r == nil || len(line) == 0 || r.StreamCompleted {
		return
	}

	trimmed := bytes.TrimSpace(line)
	if len(trimmed) == 0 {
		return
	}

	if event, ok := CompletedStreamLineEvent(trimmed); ok {
		r.markStreamCompleted(event)
	}
}

func (r *ForwardResult) markStreamCompleted(event string) {
	event = strings.TrimSpace(event)
	if event == "" {
		return
	}
	if _, ok := completedStreamEvents[event]; !ok {
		return
	}
	r.StreamCompleted = true
	r.StreamTerminalEvent = event
}

func CompletedStreamLineEvent(line []byte) (string, bool) {
	trimmed := bytes.TrimSpace(line)
	if len(trimmed) == 0 {
		return "", false
	}

	if event, ok := sseField(trimmed, "event"); ok {
		_, completed := completedStreamEvents[event]
		return event, completed
	}

	data, ok := sseField(trimmed, "data")
	if !ok {
		return "", false
	}
	if data == "[DONE]" {
		return "[DONE]", true
	}

	var payload struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal([]byte(data), &payload); err != nil {
		return "", false
	}
	_, completed := completedStreamEvents[payload.Type]
	return payload.Type, completed
}

func sseField(line []byte, field string) (string, bool) {
	prefix := []byte(field + ":")
	if !bytes.HasPrefix(line, prefix) {
		return "", false
	}
	return strings.TrimSpace(string(line[len(prefix):])), true
}

func IsClientCanceledError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.Canceled) {
		return true
	}
	if errors.Is(err, io.ErrClosedPipe) {
		return true
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "broken pipe") || strings.Contains(msg, "connection reset by peer")
}

func normalizeCompletedStreamError(result *ForwardResult) {
	if result == nil || result.Error == nil {
		return
	}
	if result.Streamed && result.StreamCompleted && IsClientCanceledError(result.Error) {
		result.Error = nil
	}
}
