package kiroplugin

import (
	"encoding/json"
	"io"
	"net/http"
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

	result := s.Forward(r.Context(), bodyBytes, model, streamReq.Stream, w)
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
