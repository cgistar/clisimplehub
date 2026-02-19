package proxy

import (
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/google/uuid"
)

// SSE event names
const (
	SSEEventRequestLog           = "request_log"
	SSEEventTokenStats           = "token_stats"
	SSEEventFallbackSwitch       = "fallback_switch"
	SSEEventEndpointTempDisabled = "endpoint_temp_disabled"
	SSEEventDebugLog             = "debug_log"
)

// DebugLogPayload represents debug log payload for UI console.
type DebugLogPayload struct {
	RequestID string `json:"requestId,omitempty"`
	Level     int    `json:"level"`
	Message   string `json:"message"`
}

// EndpointTempDisabledPayload represents the payload for endpoint temporary disable events.
type EndpointTempDisabledPayload struct {
	InterfaceType string `json:"interfaceType"`
	EndpointID    int64  `json:"endpointId"`
	EndpointName  string `json:"endpointName"`
	DisabledUntil int64  `json:"disabledUntil"`
}

// FallbackSwitchPayload represents the payload for fallback switch events.
type FallbackSwitchPayload struct {
	FromEndpoint string `json:"fromEndpoint"`
	ToEndpoint   string `json:"toEndpoint"`
	Path         string `json:"path"`
	StatusCode   int    `json:"statusCode"`
	ErrorMessage string `json:"errorMessage"`
}

// SSEMessage represents a Server-Sent Event message.
type SSEMessage struct {
	Event string
	Data  interface{}
}

// SSEClient represents a connected SSE client.
type SSEClient struct {
	ID   string
	send chan *SSEMessage
}

// SSEHub manages SSE connections and message broadcasting.
type SSEHub struct {
	clients    map[*SSEClient]bool
	broadcast  chan *SSEMessage
	register   chan *SSEClient
	unregister chan *SSEClient
	mu         sync.RWMutex
	running    bool
	stopCh     chan struct{}
	stopOnce   sync.Once
}

// NewSSEHub creates a new SSE hub.
func NewSSEHub() *SSEHub {
	return &SSEHub{
		clients:    make(map[*SSEClient]bool),
		broadcast:  make(chan *SSEMessage, 256),
		register:   make(chan *SSEClient),
		unregister: make(chan *SSEClient),
		stopCh:     make(chan struct{}),
	}
}

// Run starts the hub's event loop.
func (h *SSEHub) Run() {
	h.mu.Lock()
	h.running = true
	h.mu.Unlock()

	for {
		select {
		case <-h.stopCh:
			h.mu.Lock()
			for client := range h.clients {
				close(client.send)
				delete(h.clients, client)
			}
			h.running = false
			h.mu.Unlock()
			return

		case client := <-h.register:
			h.mu.Lock()
			h.clients[client] = true
			h.mu.Unlock()

		case client := <-h.unregister:
			h.mu.Lock()
			if _, ok := h.clients[client]; ok {
				delete(h.clients, client)
				close(client.send)
			}
			h.mu.Unlock()

		case message := <-h.broadcast:
			h.mu.RLock()
			for client := range h.clients {
				select {
				case client.send <- message:
				default:
				}
			}
			h.mu.RUnlock()
		}
	}
}

// Stop stops the hub.
func (h *SSEHub) Stop() {
	h.mu.RLock()
	running := h.running
	h.mu.RUnlock()

	if !running {
		return
	}

	h.stopOnce.Do(func() { close(h.stopCh) })
}

// Broadcast sends a message to all connected clients.
func (h *SSEHub) Broadcast(msg *SSEMessage) {
	select {
	case h.broadcast <- msg:
	default:
	}
}

// BroadcastRequestLog broadcasts a request log to all clients.
func (h *SSEHub) BroadcastRequestLog(log *RequestLog) {
	h.Broadcast(&SSEMessage{Event: SSEEventRequestLog, Data: log})
}

// BroadcastTokenStats broadcasts token statistics to all clients.
func (h *SSEHub) BroadcastTokenStats(stats *TokenStats) {
	h.Broadcast(&SSEMessage{Event: SSEEventTokenStats, Data: stats})
}

// BroadcastFallbackSwitch broadcasts a fallback switch event.
func (h *SSEHub) BroadcastFallbackSwitch(payload *FallbackSwitchPayload) {
	h.Broadcast(&SSEMessage{Event: SSEEventFallbackSwitch, Data: payload})
}

// BroadcastEndpointTempDisabled broadcasts an endpoint temporary disable event.
func (h *SSEHub) BroadcastEndpointTempDisabled(payload *EndpointTempDisabledPayload) {
	h.Broadcast(&SSEMessage{Event: SSEEventEndpointTempDisabled, Data: payload})
}

// BroadcastDebugLog broadcasts a debug log message.
func (h *SSEHub) BroadcastDebugLog(payload *DebugLogPayload) {
	if payload == nil {
		return
	}
	h.Broadcast(&SSEMessage{Event: SSEEventDebugLog, Data: payload})
}

// ClientCount returns the number of connected clients.
func (h *SSEHub) ClientCount() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.clients)
}

// IsRunning returns whether the hub is running.
func (h *SSEHub) IsRunning() bool {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.running
}

// HandleSSE handles an SSE connection request.
func (h *SSEHub) HandleSSE(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}

	// Disable the server-wide WriteTimeout for this long-lived SSE stream.
	rc := http.NewResponseController(w)
	if err := rc.SetWriteDeadline(time.Time{}); err != nil && !errors.Is(err, http.ErrNotSupported) {
		http.Error(w, "failed to configure stream", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache, no-transform")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("X-Accel-Buffering", "no")

	client := &SSEClient{
		ID:   uuid.New().String(),
		send: make(chan *SSEMessage, 256),
	}

	select {
	case h.register <- client:
	case <-h.stopCh:
		http.Error(w, "stream unavailable", http.StatusServiceUnavailable)
		return
	case <-r.Context().Done():
		return
	}

	defer func() {
		select {
		case h.unregister <- client:
		case <-h.stopCh:
		}
	}()

	// Send retry directive so EventSource reconnects after 3s
	if _, err := fmt.Fprintf(w, "retry: 3000\n\n"); err != nil {
		return
	}
	flusher.Flush()

	keepalive := time.NewTicker(30 * time.Second)
	defer keepalive.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case msg, ok := <-client.send:
			if !ok {
				return
			}
			data, err := marshalJSONNoEscapeHTML(msg.Data)
			if err != nil {
				continue
			}
			if _, err := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", msg.Event, data); err != nil {
				return
			}
			flusher.Flush()
		case <-keepalive.C:
			if _, err := fmt.Fprintf(w, ": keepalive\n\n"); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}
