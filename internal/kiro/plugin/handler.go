package kiroplugin

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"clisimplehub/internal/executor"
	"clisimplehub/internal/logger"
)

// HandleMessages handles direct /kiro/v1/messages requests.
func (s *KiroService) HandleMessages(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	bodyBytes, err := io.ReadAll(r.Body)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]interface{}{
			"error": "Failed to read request body",
		})
		return
	}
	_ = r.Body.Close()

	model := extractModelFromBody(bodyBytes)

	var streamReq struct {
		Stream bool `json:"stream"`
	}
	_ = json.Unmarshal(bodyBytes, &streamReq)

	// 创建请求级别的调试日志记录器（每次检查配置，支持热更新）
	var debugLogger *logger.RequestDebugLogger
	requestID := executor.RequestIDFromContext(r.Context())
	if requestID == "" {
		requestID = fmt.Sprintf("kiro-%d", time.Now().UnixNano())
	}
	if logger.IsDebugFileModeEnabled() {
		debugLogger = logger.NewRequestDebugLogger(requestID)
		debugLogger.SetMetadata("Plugin", "Kiro")
		debugLogger.SetMetadata("Path", r.URL.Path)
		debugLogger.SetMetadata("Method", r.Method)
		debugLogger.SetMetadata("Streaming", fmt.Sprintf("%v", streamReq.Stream))
		defer func() {
			if debugLogger != nil {
				_ = debugLogger.Flush()
			}
		}()
	}

	ctx := r.Context()
	if debugLogger != nil {
		ctx = executor.WithDebugLogger(ctx, debugLogger)
	}

	result := s.Forward(ctx, bodyBytes, model, streamReq.Stream, w, r.URL.Path)
	if result == nil {
		http.Error(w, "Request failed", http.StatusBadGateway)
		return
	}
	if result.Streamed {
		return
	}
	if result.Error != nil && result.StatusCode == 0 {
		http.Error(w, result.Error.Error(), http.StatusBadGateway)
		return
	}
	if result.Headers != nil {
		for key, values := range result.Headers {
			for _, v := range values {
				w.Header().Add(key, v)
			}
		}
	}
	w.WriteHeader(result.StatusCode)
	_, _ = w.Write(result.Body)
}

// HandleModels handles /kiro/v1/models requests.
func (s *KiroService) HandleModels(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"object": "list",
		"data": []map[string]interface{}{
			{"id": "claude-sonnet-4-5-20250929", "object": "model", "owned_by": "anthropic", "display_name": "Claude Sonnet 4.5"},
			{"id": "claude-opus-4-5-20251101", "object": "model", "owned_by": "anthropic", "display_name": "Claude Opus 4.5"},
			{"id": "claude-haiku-4-5-20251001", "object": "model", "owned_by": "anthropic", "display_name": "Claude Haiku 4.5"},
		},
	})
}

func writeJSON(w http.ResponseWriter, statusCode int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	_ = json.NewEncoder(w).Encode(data)
}
