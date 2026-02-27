package claude

import (
	"fmt"

	"clisimplehub/internal/kiro/streaming"
)

// StreamStateV2 封装新的 streaming 流处理状态。
type StreamStateV2 struct {
	Decoder                *streaming.EventStreamDecoder
	Context                *streaming.StreamContext
	Buffered               *streaming.BufferedStreamContext
	BufferedMode           bool
	InitialEventsGenerated bool
	Finalized              bool
}

func newStreamStateV2(model string, inputTokens int, thinkingEnabled bool, bufferedMode bool) *StreamStateV2 {
	s := &StreamStateV2{
		Decoder:      streaming.NewEventStreamDecoder(),
		BufferedMode: bufferedMode,
	}
	if bufferedMode {
		s.Buffered = streaming.NewBufferedStreamContext(model, inputTokens, thinkingEnabled)
	} else {
		s.Context = streaming.NewStreamContext(model, inputTokens, thinkingEnabled)
	}
	return s
}

func (s *StreamStateV2) ensureInitialEvents() []*streaming.SseEvent {
	if s == nil || s.InitialEventsGenerated {
		return nil
	}
	s.InitialEventsGenerated = true
	if s.BufferedMode {
		return nil
	}
	if s.Context == nil {
		return nil
	}
	return s.Context.GenerateInitialEvents()
}

func (s *StreamStateV2) ProcessEvent(event *streaming.Event) []*streaming.SseEvent {
	if s == nil || event == nil {
		return nil
	}
	if s.BufferedMode {
		if s.Buffered == nil {
			return nil
		}
		s.Buffered.ProcessAndBuffer(event)
		s.InitialEventsGenerated = true
		return nil
	}
	if s.Context == nil {
		return nil
	}

	events := s.ensureInitialEvents()
	events = append(events, s.Context.ProcessEvent(event)...)
	return events
}

func (s *StreamStateV2) ProcessPayload(payload []byte) ([]*streaming.SseEvent, error) {
	if s == nil || s.Decoder == nil {
		return nil, fmt.Errorf("nil stream state")
	}
	if err := s.Decoder.Feed(payload); err != nil {
		return nil, err
	}

	frames, err := s.Decoder.DecodeAll()
	if err != nil {
		return nil, err
	}

	var events []*streaming.SseEvent
	for _, frame := range frames {
		ev, parseErr := streaming.EventFromFrame(frame)
		if parseErr != nil {
			return nil, parseErr
		}
		events = append(events, s.ProcessEvent(ev)...)
	}
	return events, nil
}

func (s *StreamStateV2) Finalize() []*streaming.SseEvent {
	if s == nil || s.Finalized {
		return nil
	}
	s.Finalized = true

	if s.BufferedMode {
		if s.Buffered == nil {
			return nil
		}
		return s.Buffered.FinishAndGetAllEvents()
	}

	if s.Context == nil {
		return nil
	}

	events := s.ensureInitialEvents()
	events = append(events, s.Context.GenerateFinalEvents()...)
	return events
}

// TokenUsage 与旧 StreamState 接口保持兼容。
func (s *StreamStateV2) TokenUsage() (int, int) {
	if s == nil {
		return 0, 0
	}
	if s.BufferedMode && s.Buffered != nil {
		return s.Buffered.TokenUsage()
	}
	if s.Context != nil {
		return s.Context.TokenUsage()
	}
	return 0, 0
}
