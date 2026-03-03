package converters

import (
	"clisimplehub/internal/kiro/streaming"
)

// AMQSSEBridgeConfig defines SSE bridge behavior.
type AMQSSEBridgeConfig struct {
	Model           string
	InputTokens     int
	ThinkingEnabled bool
	Buffered        bool
}

// AMQSSEBridge converts AMQ structured stream events into Anthropic SSE strings.
type AMQSSEBridge struct {
	buffered  bool
	ctx       *streaming.StreamContext
	buf       *streaming.BufferedStreamContext
	started   bool
	finalized bool
}

func NewAMQSSEBridge(cfg AMQSSEBridgeConfig) *AMQSSEBridge {
	b := &AMQSSEBridge{buffered: cfg.Buffered}
	if cfg.Buffered {
		b.buf = streaming.NewBufferedStreamContext(cfg.Model, cfg.InputTokens, cfg.ThinkingEnabled)
	} else {
		b.ctx = streaming.NewStreamContext(cfg.Model, cfg.InputTokens, cfg.ThinkingEnabled)
	}
	return b
}

func sseEventsToStrings(events []*streaming.SseEvent) []string {
	if len(events) == 0 {
		return nil
	}
	out := make([]string, 0, len(events))
	for _, ev := range events {
		if ev == nil {
			continue
		}
		out = append(out, ev.ToSSEString())
	}
	return out
}

// ConsumeEvents ingests a structured AMQ event and emits SSE events.
func (b *AMQSSEBridge) ConsumeEvents(envelope *AMQStreamEnvelope) ([]*streaming.SseEvent, error) {
	if b == nil || envelope == nil || envelope.Event == nil {
		return nil, nil
	}

	if b.buffered {
		if b.buf == nil {
			return nil, nil
		}
		b.buf.ProcessAndBuffer(envelope.Event)
		b.started = true
		return nil, nil
	}

	if b.ctx == nil {
		return nil, nil
	}

	var events []*streaming.SseEvent
	if !b.started {
		events = append(events, b.ctx.GenerateInitialEvents()...)
		b.started = true
	}
	events = append(events, b.ctx.ProcessEvent(envelope.Event)...)
	return events, nil
}

// FinalizeEvents flushes pending state and returns final SSE events.
func (b *AMQSSEBridge) FinalizeEvents() ([]*streaming.SseEvent, error) {
	if b == nil || b.finalized {
		return nil, nil
	}
	b.finalized = true

	if b.buffered {
		if b.buf == nil {
			return nil, nil
		}
		return b.buf.FinishAndGetAllEvents(), nil
	}

	if b.ctx == nil {
		return nil, nil
	}
	if !b.started {
		b.started = true
		initial := b.ctx.GenerateInitialEvents()
		final := b.ctx.GenerateFinalEvents()
		all := append(initial, final...)
		return all, nil
	}
	return b.ctx.GenerateFinalEvents(), nil
}

// Consume ingests a structured AMQ event and emits SSE chunks.
func (b *AMQSSEBridge) Consume(envelope *AMQStreamEnvelope) ([]string, error) {
	events, err := b.ConsumeEvents(envelope)
	if err != nil {
		return nil, err
	}
	return sseEventsToStrings(events), nil
}

// Finalize flushes pending state and returns final SSE chunks.
func (b *AMQSSEBridge) Finalize() ([]string, error) {
	events, err := b.FinalizeEvents()
	if err != nil {
		return nil, err
	}
	return sseEventsToStrings(events), nil
}

// TokenUsage exposes current token usage stats.
func (b *AMQSSEBridge) TokenUsage() (int, int) {
	if b == nil {
		return 0, 0
	}
	if b.buffered {
		if b.buf == nil {
			return 0, 0
		}
		return b.buf.TokenUsage()
	}
	if b.ctx == nil {
		return 0, 0
	}
	return b.ctx.TokenUsage()
}
