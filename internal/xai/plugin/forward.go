package xaiplugin

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"clisimplehub/internal/executor"
	xaiBackend "clisimplehub/internal/xai/backend"
)

func (s *XaiService) HandleProxy(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "failed to read body"})
		return
	}
	_ = r.Body.Close()

	isStreaming := isStreamRequested(r, body)
	// compact 是严格的非流接口，不能把客户端 stream=true 静默降级。
	if strings.HasSuffix(strings.TrimRight(strings.ToLower(r.URL.Path), "/"), "/responses/compact") {
		if isStreaming {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": map[string]any{
				"message": "stream is not supported for responses compact",
				"type":    "invalid_request_error",
				"code":    "invalid_stream",
			}})
			return
		}
		isStreaming = false
	}
	// GET 无 body 场景（如 videos/{id}）
	if r.Method == http.MethodGet || r.Method == http.MethodHead {
		body = nil
	}

	requestID := executor.RequestIDFromContext(r.Context())
	if requestID == "" {
		requestID = fmt.Sprintf("xai-%d", time.Now().UnixNano())
	}

	// /xai/v1/* 已是 Responses wire 形态，统一走同一 prepare 链路。
	clientMode := xaiBackend.ClientModeCompat
	result := s.RoundTrip(r.Context(), &executor.UpstreamRequest{
		Method:              r.Method,
		TargetPath:          r.URL.Path,
		RawQuery:            r.URL.RawQuery,
		Headers:             r.Header.Clone(),
		Body:                body,
		IsStreaming:         isStreaming,
		RequestModel:        extractModelFromBody(body),
		OriginalPath:        r.URL.Path,
		TargetInterfaceType: "xai",
		TransformContext: &executor.TransformContext{
			Metadata: map[string]any{
				"client_mode": string(clientMode),
			},
		},
	})
	writeUpstreamResult(w, result)

	statusCode := http.StatusBadGateway
	if result != nil && result.StatusCode > 0 {
		statusCode = result.StatusCode
	}
	log.Printf("[INFO] [%s] %s %s | xai | %d | %.3fs",
		shortID(requestID), r.Method, r.URL.Path, statusCode, time.Since(start).Seconds())
}

func writeUpstreamResult(w http.ResponseWriter, result *executor.UpstreamRoundTripResult) {
	if result == nil {
		http.Error(w, `{"error":"xai request failed"}`, http.StatusBadGateway)
		return
	}
	for key, values := range result.Headers {
		if !allowedDownstreamHeader(key) {
			continue
		}
		for _, value := range values {
			w.Header().Add(key, value)
		}
	}
	statusCode := result.StatusCode
	if statusCode <= 0 {
		statusCode = http.StatusBadGateway
	}
	if result.Stream != nil {
		defer result.Stream.Close()
		w.WriteHeader(statusCode)
		buf := make([]byte, 32*1024)
		flusher, _ := w.(http.Flusher)
		for {
			n, readErr := result.Stream.Read(buf)
			if n > 0 {
				if _, writeErr := w.Write(buf[:n]); writeErr != nil {
					return
				}
				if flusher != nil {
					flusher.Flush()
				}
			}
			if readErr != nil {
				return
			}
		}
	}
	if len(result.Body) > 0 && w.Header().Get("Content-Type") == "" {
		w.Header().Set("Content-Type", "application/json")
	}
	w.WriteHeader(statusCode)
	if len(result.Body) > 0 {
		_, _ = w.Write(result.Body)
	}
}

func allowedDownstreamHeader(key string) bool {
	switch http.CanonicalHeaderKey(key) {
	case "Content-Type", "Cache-Control", "Retry-After":
		return true
	default:
		return false
	}
}

func isStreamRequested(r *http.Request, body []byte) bool {
	if r != nil {
		if strings.Contains(strings.ToLower(r.Header.Get("Accept")), "text/event-stream") {
			return true
		}
		if v := strings.TrimSpace(r.URL.Query().Get("stream")); strings.EqualFold(v, "true") || v == "1" {
			return true
		}
	}
	if len(body) == 0 {
		return false
	}
	var payload struct {
		Stream bool `json:"stream"`
	}
	_ = json.Unmarshal(body, &payload)
	return payload.Stream
}

func extractModelFromBody(body []byte) string {
	if len(body) == 0 {
		return ""
	}
	var payload struct {
		Model string `json:"model"`
	}
	_ = json.Unmarshal(body, &payload)
	return strings.TrimSpace(payload.Model)
}

func shortID(id string) string {
	id = strings.TrimSpace(id)
	if len(id) > 8 {
		return id[:8]
	}
	if id == "" {
		return "-"
	}
	return id
}
