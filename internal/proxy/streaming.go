// Package proxy implements the HTTP proxy server for AI API requests.
package proxy

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"io"
	"net/http"
	"strings"
)

// handleStreamingResponse processes streaming SSE responses
// Requirements: 9.4
func (p *ProxyServer) handleStreamingResponse(w http.ResponseWriter, resp *http.Response, endpoint *Endpoint) (int, []byte, error) {
	statusCode, body, _, err := p.handleStreamingResponseWithCapture(w, resp, endpoint)
	return statusCode, body, err
}

// handleStreamingResponseWithCapture processes streaming SSE responses and captures stream data
// Requirements: 9.4
func (p *ProxyServer) handleStreamingResponseWithCapture(w http.ResponseWriter, resp *http.Response, endpoint *Endpoint) (int, []byte, string, error) {
	statusCode, body, capture, _, err := p.handleStreamingResponseWithCaptureAndTokens(w, resp, endpoint)
	return statusCode, body, capture, err
}

func (p *ProxyServer) handleStreamingResponseWithCaptureAndTokens(w http.ResponseWriter, resp *http.Response, endpoint *Endpoint) (int, []byte, string, *TokenUsage, error) {
	// Copy response headers except Content-Length and Content-Encoding
	for key, values := range resp.Header {
		if key == "Content-Length" || key == "Content-Encoding" {
			continue
		}
		for _, value := range values {
			w.Header().Add(key, value)
		}
	}
	w.WriteHeader(resp.StatusCode)

	flusher, ok := w.(http.Flusher)
	if !ok {
		return resp.StatusCode, nil, "", nil, nil
	}

	// Handle gzip-encoded response body
	var reader io.Reader = resp.Body
	if resp.Header.Get("Content-Encoding") == "gzip" {
		gzipReader, err := gzip.NewReader(resp.Body)
		if err != nil {
			return resp.StatusCode, nil, "", nil, err
		}
		defer gzipReader.Close()
		reader = gzipReader
	}

	scanner := bufio.NewScanner(reader)
	buf := make([]byte, 0, 64*1024)
	scanner.Buffer(buf, 1024*1024)

	var tokenAcc TokenUsage
	var buffer bytes.Buffer
	var streamCapture strings.Builder // Capture stream data for detail view
	const maxCaptureSize = 50 * 1024  // Limit capture to 50KB
	streamDone := false

	for scanner.Scan() && !streamDone {
		line := scanner.Text()

		// Capture stream data (limited size)
		if streamCapture.Len() < maxCaptureSize {
			streamCapture.WriteString(line + "\n")
		}

		// Check for stream end
		if strings.Contains(line, "data: [DONE]") {
			streamDone = true
			buffer.WriteString(line + "\n")
			eventData := buffer.Bytes()
			w.Write(eventData)
			flusher.Flush()
			break
		}

		buffer.WriteString(line + "\n")

		// Empty line indicates end of SSE event
		if line == "" {
			eventData := buffer.Bytes()

			// Extract tokens from event
			p.extractTokenUsageFromSSEEvent(eventData, &tokenAcc)

			// Write event to client
			if _, writeErr := w.Write(eventData); writeErr != nil {
				// Client disconnected
				if strings.Contains(writeErr.Error(), "broken pipe") || strings.Contains(writeErr.Error(), "connection reset") {
					streamDone = true
					break
				}
			}
			flusher.Flush()
			buffer.Reset()
		}
	}

	var tokens *TokenUsage
	if tokenAcc.InputTokens != 0 || tokenAcc.OutputTokens != 0 || tokenAcc.CachedCreate != 0 || tokenAcc.CachedRead != 0 || tokenAcc.Reasoning != 0 {
		t := tokenAcc
		tokens = &t
		if p.stats != nil && endpoint != nil {
			p.stats.RecordTokens(endpoint.Name, tokens)
		}
	}

	return resp.StatusCode, nil, streamCapture.String(), tokens, nil
}

// extractTokenUsageFromSSEEvent extracts token usage from an SSE event (best-effort).
func (p *ProxyServer) extractTokenUsageFromSSEEvent(eventData []byte, tokens *TokenUsage) {
	scanner := bufio.NewScanner(bytes.NewReader(eventData))
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data:") {
			continue
		}

		jsonData := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if jsonData == "[DONE]" {
			continue
		}

		var event map[string]interface{}
		if err := json.Unmarshal([]byte(jsonData), &event); err != nil {
			continue
		}

		// OpenAI Responses streaming: {"response": {"usage": {...}}}
		if response, ok := event["response"].(map[string]interface{}); ok {
			if usage, ok := response["usage"].(map[string]interface{}); ok {
				applyUsageMap(anyMap(usage), tokens)
			}
		}

		// Handle Claude format
		eventType, _ := event["type"].(string)
		if eventType == "message_start" {
			if message, ok := event["message"].(map[string]interface{}); ok {
				if usage, ok := message["usage"].(map[string]interface{}); ok {
					applyUsageMap(anyMap(usage), tokens)
				}
			}
		} else if eventType == "message_delta" {
			if usage, ok := event["usage"].(map[string]interface{}); ok {
				applyUsageMap(anyMap(usage), tokens)
			}
		}

		// Some providers may include usage under a "message" object even without explicit event types.
		if message, ok := event["message"].(map[string]interface{}); ok {
			if usage, ok := message["usage"].(map[string]interface{}); ok {
				applyUsageMap(anyMap(usage), tokens)
			}
		}

		// Handle OpenAI / generic format
		if usage, ok := event["usage"].(map[string]interface{}); ok {
			applyUsageMap(anyMap(usage), tokens)
		}
	}
}

func anyMap(m map[string]interface{}) map[string]any {
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

// SSEEvent represents a Server-Sent Event
type SSEEvent struct {
	Event string `json:"event,omitempty"`
	Data  string `json:"data"`
}

// ParseSSEEvent parses an SSE event from raw bytes
func ParseSSEEvent(data []byte) (*SSEEvent, error) {
	event := &SSEEvent{}
	scanner := bufio.NewScanner(bytes.NewReader(data))

	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "event:") {
			event.Event = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
		} else if strings.HasPrefix(line, "data:") {
			event.Data = strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		}
	}

	return event, scanner.Err()
}

// FormatSSEEvent formats an SSE event for transmission
func FormatSSEEvent(event *SSEEvent) []byte {
	var buf bytes.Buffer
	if event.Event != "" {
		buf.WriteString("event: ")
		buf.WriteString(event.Event)
		buf.WriteString("\n")
	}
	buf.WriteString("data: ")
	buf.WriteString(event.Data)
	buf.WriteString("\n\n")
	return buf.Bytes()
}
