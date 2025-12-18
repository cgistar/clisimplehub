// Package proxy implements the HTTP proxy server for AI API requests.
package proxy

import (
	"encoding/json"
	"net/http"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
)

// WSMessageType represents WebSocket message types
// Requirements: 7.1, 8.5
type WSMessageType string

const (
	// WSMessageTypeRequestLog indicates a request log message
	WSMessageTypeRequestLog WSMessageType = "request_log"
	// WSMessageTypeTokenStats indicates a token stats message
	WSMessageTypeTokenStats WSMessageType = "token_stats"
	// WSMessageTypeFallbackSwitch indicates an endpoint fallback switch event
	WSMessageTypeFallbackSwitch WSMessageType = "fallback_switch"
	// WSMessageTypeEndpointTempDisabled indicates an endpoint was temporarily disabled in runtime
	WSMessageTypeEndpointTempDisabled WSMessageType = "endpoint_temp_disabled"
)

// WSMessage represents a WebSocket message
// Requirements: 7.1, 8.5
type WSMessage struct {
	Type    WSMessageType `json:"type"`
	Payload interface{}   `json:"payload"`
}

// WSClient represents a WebSocket client connection
// Requirements: 7.1, 8.5
type WSClient struct {
	ID   string
	Send chan *WSMessage
	hub  *WSHub
	conn *websocket.Conn
}

// WebSocket configuration constants
const (
	// Time allowed to write a message to the peer
	writeWait = 10 * time.Second

	// Time allowed to read the next pong message from the peer
	pongWait = 60 * time.Second

	// Send pings to peer with this period (must be less than pongWait)
	pingPeriod = (pongWait * 9) / 10

	// Maximum message size allowed from peer
	maxMessageSize = 512
)

// WebSocket upgrader with default options
var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	// Allow all origins for development (should be restricted in production)
	CheckOrigin: func(r *http.Request) bool {
		return true
	},
}

// WSHub manages WebSocket connections and message broadcasting
// Requirements: 7.1, 8.5
type WSHub struct {
	clients    map[*WSClient]bool
	broadcast  chan *WSMessage
	register   chan *WSClient
	unregister chan *WSClient
	mu         sync.RWMutex
	running    bool
	stopCh     chan struct{}
}

// NewWSHub creates a new WebSocket hub
// Requirements: 7.1, 8.5
func NewWSHub() *WSHub {
	return &WSHub{
		clients:    make(map[*WSClient]bool),
		broadcast:  make(chan *WSMessage, 256),
		register:   make(chan *WSClient),
		unregister: make(chan *WSClient),
		stopCh:     make(chan struct{}),
	}
}

// Run starts the hub's main loop
// Requirements: 7.1, 8.5
func (h *WSHub) Run() {
	h.mu.Lock()
	h.running = true
	h.mu.Unlock()

	for {
		select {
		case <-h.stopCh:
			// Graceful shutdown
			h.mu.Lock()
			for client := range h.clients {
				close(client.Send)
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
				close(client.Send)
			}
			h.mu.Unlock()

		case message := <-h.broadcast:
			h.mu.RLock()
			for client := range h.clients {
				select {
				case client.Send <- message:
				default:
					// Client buffer full, skip this message for this client
				}
			}
			h.mu.RUnlock()
		}
	}
}

// Stop stops the hub's main loop
func (h *WSHub) Stop() {
	h.mu.RLock()
	running := h.running
	h.mu.RUnlock()

	if running {
		close(h.stopCh)
	}
}

// Register adds a client to the hub
func (h *WSHub) Register(client *WSClient) {
	h.register <- client
}

// Unregister removes a client from the hub
func (h *WSHub) Unregister(client *WSClient) {
	h.unregister <- client
}

// Broadcast sends a message to all connected clients
// Requirements: 7.1, 8.5
func (h *WSHub) Broadcast(message *WSMessage) {
	select {
	case h.broadcast <- message:
	default:
		// Broadcast channel full, skip
	}
}

// BroadcastRequestLog broadcasts a request log to all clients
// Requirements: 7.1
func (h *WSHub) BroadcastRequestLog(log *RequestLog) {
	h.Broadcast(&WSMessage{
		Type:    WSMessageTypeRequestLog,
		Payload: log,
	})
}

// BroadcastTokenStats broadcasts token statistics to all clients
// Requirements: 8.5
func (h *WSHub) BroadcastTokenStats(stats *TokenStats) {
	h.Broadcast(&WSMessage{
		Type:    WSMessageTypeTokenStats,
		Payload: stats,
	})
}

// EndpointTempDisabledPayload represents the payload for endpoint temporary disable events
type EndpointTempDisabledPayload struct {
	InterfaceType string `json:"interfaceType"`
	EndpointID    int64  `json:"endpointId"`
	EndpointName  string `json:"endpointName"`
	DisabledUntil int64  `json:"disabledUntil"` // unix milliseconds
}

// FallbackSwitchPayload represents the payload for fallback switch events
type FallbackSwitchPayload struct {
	FromVendor   string `json:"fromVendor"`
	FromEndpoint string `json:"fromEndpoint"`
	ToVendor     string `json:"toVendor"`
	ToEndpoint   string `json:"toEndpoint"`
	Path         string `json:"path"`
	StatusCode   int    `json:"statusCode"`
	ErrorMessage string `json:"errorMessage"`
}

// BroadcastFallbackSwitch broadcasts a fallback switch event to all clients
func (h *WSHub) BroadcastFallbackSwitch(payload *FallbackSwitchPayload) {
	h.Broadcast(&WSMessage{
		Type:    WSMessageTypeFallbackSwitch,
		Payload: payload,
	})
}

// BroadcastEndpointTempDisabled broadcasts an endpoint temporary disable event to all clients
func (h *WSHub) BroadcastEndpointTempDisabled(payload *EndpointTempDisabledPayload) {
	h.Broadcast(&WSMessage{
		Type:    WSMessageTypeEndpointTempDisabled,
		Payload: payload,
	})
}

// ClientCount returns the number of connected clients
func (h *WSHub) ClientCount() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.clients)
}

// IsRunning returns whether the hub is running
func (h *WSHub) IsRunning() bool {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.running
}

// HandleWebSocket handles WebSocket connection upgrade and client management
// Requirements: 7.1, 8.5
func (h *WSHub) HandleWebSocket(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}

	client := &WSClient{
		ID:   uuid.New().String(),
		Send: make(chan *WSMessage, 256),
		hub:  h,
		conn: conn,
	}

	h.Register(client)

	// Start goroutines for reading and writing
	go client.writePump()
	go client.readPump()
}

// readPump pumps messages from the WebSocket connection to the hub
func (c *WSClient) readPump() {
	defer func() {
		c.hub.Unregister(c)
		c.conn.Close()
	}()

	c.conn.SetReadLimit(maxMessageSize)
	c.conn.SetReadDeadline(time.Now().Add(pongWait))
	c.conn.SetPongHandler(func(string) error {
		c.conn.SetReadDeadline(time.Now().Add(pongWait))
		return nil
	})

	for {
		_, _, err := c.conn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
				// Log error if needed
			}
			break
		}
		// We don't process incoming messages from clients in this implementation
		// The WebSocket is primarily for server-to-client broadcasting
	}
}

// writePump pumps messages from the hub to the WebSocket connection
func (c *WSClient) writePump() {
	ticker := time.NewTicker(pingPeriod)
	defer func() {
		ticker.Stop()
		c.conn.Close()
	}()

	for {
		select {
		case message, ok := <-c.Send:
			c.conn.SetWriteDeadline(time.Now().Add(writeWait))
			if !ok {
				// The hub closed the channel
				c.conn.WriteMessage(websocket.CloseMessage, []byte{})
				return
			}

			// Marshal the message to JSON
			data, err := json.Marshal(message)
			if err != nil {
				continue
			}

			w, err := c.conn.NextWriter(websocket.TextMessage)
			if err != nil {
				return
			}
			w.Write(data)

			// Add queued messages to the current WebSocket message
			n := len(c.Send)
			for i := 0; i < n; i++ {
				msg := <-c.Send
				data, err := json.Marshal(msg)
				if err != nil {
					continue
				}
				w.Write([]byte{'\n'})
				w.Write(data)
			}

			if err := w.Close(); err != nil {
				return
			}

		case <-ticker.C:
			c.conn.SetWriteDeadline(time.Now().Add(writeWait))
			if err := c.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}
