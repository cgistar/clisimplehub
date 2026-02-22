package main

import (
	"bytes"
	"context"
	"os"
	"strings"
	"sync"

	"github.com/wailsapp/wails/v2/pkg/runtime"
)

const (
	wailsConsoleLevelDebug = 0
	wailsConsoleLevelInfo  = 1
	wailsConsoleLevelWarn  = 2
	wailsConsoleLevelError = 3
)

const (
	goLogEventName   = "app-log"
	maxPendingGoLogs = 300
)

type goLogEvent struct {
	Level     int    `json:"level"`
	Message   string `json:"message"`
	Source    string `json:"source"`
	Timestamp string `json:"timestamp"`
}

type goLogBridge struct {
	mu       sync.Mutex
	ctx      context.Context
	buf      bytes.Buffer
	pending  []goLogEvent
	event    string
	maxQueue int
}

func newGoLogBridge(eventName string) *goLogBridge {
	name := strings.TrimSpace(eventName)
	if name == "" {
		name = goLogEventName
	}
	return &goLogBridge{
		event:    name,
		maxQueue: maxPendingGoLogs,
	}
}

func (b *goLogBridge) SetContext(ctx context.Context) {
	if b == nil {
		return
	}

	b.mu.Lock()
	b.ctx = ctx
	pending := make([]goLogEvent, len(b.pending))
	copy(pending, b.pending)
	b.pending = nil
	b.mu.Unlock()

	for i := range pending {
		b.emit(pending[i])
	}
}

func (b *goLogBridge) Write(p []byte) (int, error) {
	if b == nil || len(p) == 0 {
		return len(p), nil
	}

	var ready []goLogEvent

	b.mu.Lock()
	_, _ = b.buf.Write(p)

	for {
		data := b.buf.Bytes()
		idx := bytes.IndexByte(data, '\n')
		if idx < 0 {
			break
		}

		line := string(data[:idx])
		b.buf.Next(idx + 1)

		message := strings.TrimRight(line, "\r")
		if strings.TrimSpace(message) == "" {
			continue
		}

		entry := goLogEvent{
			Level:     inferGoLogLevel(message),
			Message:   message,
			Source:    "go",
			Timestamp: "",
		}

		if b.ctx == nil {
			b.pending = append(b.pending, entry)
			if len(b.pending) > b.maxQueue {
				b.pending = b.pending[len(b.pending)-b.maxQueue:]
			}
			continue
		}

		ready = append(ready, entry)
	}

	b.mu.Unlock()

	for i := range ready {
		b.emit(ready[i])
	}

	return len(p), nil
}

func (b *goLogBridge) emit(entry goLogEvent) {
	if b == nil {
		return
	}

	b.mu.Lock()
	ctx := b.ctx
	eventName := b.event
	b.mu.Unlock()

	if ctx == nil {
		return
	}

	defer func() {
		if recovered := recover(); recovered != nil {
			_, _ = os.Stderr.WriteString("[go-log-bridge] recovered panic while emitting app-log event\n")
		}
	}()

	runtime.EventsEmit(ctx, eventName, entry)
}

func inferGoLogLevel(message string) int {
	lower := strings.ToLower(strings.TrimSpace(message))
	if lower == "" {
		return wailsConsoleLevelInfo
	}
	if strings.Contains(lower, "fatal") || strings.Contains(lower, "panic") || strings.Contains(lower, "error") {
		return wailsConsoleLevelError
	}
	if strings.Contains(lower, "warn") || strings.Contains(lower, "warning") {
		return wailsConsoleLevelWarn
	}
	if strings.Contains(lower, "debug") {
		return wailsConsoleLevelDebug
	}
	return wailsConsoleLevelInfo
}
