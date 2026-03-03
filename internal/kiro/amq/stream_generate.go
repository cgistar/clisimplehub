package converters

import (
	"context"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"clisimplehub/internal/kiro/streaming"
)

// AMQStreamEnvelope wraps one parsed AMQ stream event.
type AMQStreamEnvelope struct {
	Event      *streaming.Event
	RequestID  string
	ReceivedAt time.Time
}

// AMQStreamMetadata stores stream-consumption metadata.
type AMQStreamMetadata struct {
	RequestID         string
	RequestStartAt    time.Time
	StreamEndAt       time.Time
	TimeToFirstChunk  *time.Duration
	TimeBetweenChunks []time.Duration
	ResponseSize      int
	ToolUses          [][2]string
}

// AMQGenerateStream provides incremental event consumption over AWS EventStream.
type AMQGenerateStream struct {
	resp      *http.Response
	decoder   *streaming.EventStreamDecoder
	pending   []*AMQStreamEnvelope
	readBuf   []byte
	metadata  AMQStreamMetadata
	done      bool
	closeOnce sync.Once
	mu        sync.Mutex
	seenTools map[string]string
}

func newAMQGenerateStream(resp *http.Response) *AMQGenerateStream {
	requestID := strings.TrimSpace(resp.Header.Get("x-amzn-RequestId"))
	return &AMQGenerateStream{
		resp:    resp,
		decoder: streaming.NewEventStreamDecoder(),
		readBuf: make([]byte, 32*1024),
		metadata: AMQStreamMetadata{
			RequestID:      requestID,
			RequestStartAt: time.Now(),
		},
		seenTools: make(map[string]string),
	}
}

func (s *AMQGenerateStream) updateEventMetrics(event *streaming.Event) {
	if event == nil {
		return
	}
	switch event.Type {
	case streaming.EventAssistantResponse:
		s.metadata.ResponseSize += len(event.Content)
	case streaming.EventToolUse:
		s.metadata.ResponseSize += len(event.ToolInput)
		if event.ToolUseID != "" && event.ToolName != "" {
			if _, ok := s.seenTools[event.ToolUseID]; !ok {
				s.seenTools[event.ToolUseID] = event.ToolName
				s.metadata.ToolUses = append(s.metadata.ToolUses, [2]string{event.ToolUseID, event.ToolName})
			}
		}
	}
}

func (s *AMQGenerateStream) pushEventsFromFrames(frames []*streaming.Frame) error {
	now := time.Now()
	for _, frame := range frames {
		ev, err := streaming.EventFromFrame(frame)
		if err != nil {
			return err
		}
		s.updateEventMetrics(ev)
		s.pending = append(s.pending, &AMQStreamEnvelope{
			Event:      ev,
			RequestID:  s.metadata.RequestID,
			ReceivedAt: now,
		})
	}
	return nil
}

func (s *AMQGenerateStream) finalize() {
	if s.metadata.StreamEndAt.IsZero() {
		s.metadata.StreamEndAt = time.Now()
	}
	s.done = true
}

// StatusCode returns upstream status code.
func (s *AMQGenerateStream) StatusCode() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.resp == nil {
		return 0
	}
	return s.resp.StatusCode
}

// Header returns a copy of upstream response headers.
func (s *AMQGenerateStream) Header() http.Header {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.resp == nil || s.resp.Header == nil {
		return nil
	}
	return s.resp.Header.Clone()
}

// Recv returns the next parsed stream event.
func (s *AMQGenerateStream) Recv(ctx context.Context) (*AMQStreamEnvelope, error) {
	s.mu.Lock()

	if len(s.pending) > 0 {
		ev := s.pending[0]
		s.pending = s.pending[1:]
		s.mu.Unlock()
		return ev, nil
	}
	if s.done {
		s.mu.Unlock()
		return nil, io.EOF
	}
	if s.resp == nil || s.resp.Body == nil {
		s.finalize()
		s.mu.Unlock()
		return nil, io.EOF
	}

	body := s.resp.Body
	readBuf := s.readBuf
	s.mu.Unlock()

	for {
		if ctx != nil {
			if err := ctx.Err(); err != nil {
				s.mu.Lock()
				s.finalize()
				s.mu.Unlock()
				return nil, err
			}
		}

		waitStart := time.Now()
		n, err := body.Read(readBuf)
		delta := time.Since(waitStart)

		s.mu.Lock()
		if n > 0 {
			if s.metadata.TimeToFirstChunk == nil {
				t := delta
				s.metadata.TimeToFirstChunk = &t
			}
			s.metadata.TimeBetweenChunks = append(s.metadata.TimeBetweenChunks, delta)

			if feedErr := s.decoder.Feed(readBuf[:n]); feedErr != nil {
				s.finalize()
				s.mu.Unlock()
				return nil, feedErr
			}

			frames, decodeErr := s.decoder.DecodeAll()
			if decodeErr != nil {
				s.finalize()
				s.mu.Unlock()
				return nil, decodeErr
			}
			if pushErr := s.pushEventsFromFrames(frames); pushErr != nil {
				s.finalize()
				s.mu.Unlock()
				return nil, pushErr
			}
			if len(s.pending) > 0 {
				ev := s.pending[0]
				s.pending = s.pending[1:]
				s.mu.Unlock()
				return ev, nil
			}
		}

		if err == io.EOF {
			s.finalize()
			s.mu.Unlock()
			return nil, io.EOF
		}
		if err != nil {
			s.finalize()
			s.mu.Unlock()
			return nil, err
		}
		s.mu.Unlock()
	}
}

// Metadata returns a snapshot of consumed stream metadata.
func (s *AMQGenerateStream) Metadata() AMQStreamMetadata {
	s.mu.Lock()
	defer s.mu.Unlock()
	copyDurations := make([]time.Duration, len(s.metadata.TimeBetweenChunks))
	copy(copyDurations, s.metadata.TimeBetweenChunks)
	copyTools := make([][2]string, len(s.metadata.ToolUses))
	copy(copyTools, s.metadata.ToolUses)
	m := s.metadata
	m.TimeBetweenChunks = copyDurations
	m.ToolUses = copyTools
	return m
}

// Close closes the underlying response body.
func (s *AMQGenerateStream) Close() error {
	var closeErr error
	s.closeOnce.Do(func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		if s.resp == nil || s.resp.Body == nil {
			s.finalize()
			return
		}
		closeErr = s.resp.Body.Close()
		s.finalize()
	})
	return closeErr
}
