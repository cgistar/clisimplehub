package codexplugin

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"clisimplehub/internal/executor"
)

type codexModelCatalogEntry struct {
	ID      string
	Created int64
	OwnedBy string
}

var codexModelsCatalog = []codexModelCatalogEntry{
	{ID: "gpt-5.2", Created: 1765440000, OwnedBy: "openai"},
	{ID: "gpt-5.3-codex", Created: 1770307200, OwnedBy: "openai"},
	{ID: "gpt-5.3-codex-spark", Created: 1770912000, OwnedBy: "openai"},
	{ID: "gpt-5.4", Created: 1772668800, OwnedBy: "openai"},
	{ID: "gpt-5.4-mini", Created: 1773705600, OwnedBy: "openai"},
	{ID: "gpt-5.5", Created: 1776902400, OwnedBy: "openai"},
	{ID: "gpt-5.6-sol", Created: 1783616400, OwnedBy: "openai"},
	{ID: "gpt-5.6-terra", Created: 1783616400, OwnedBy: "openai"},
	{ID: "gpt-5.6-luna", Created: 1783616400, OwnedBy: "openai"},
	{ID: "codex-auto-review", Created: 1776902400, OwnedBy: "openai"},
}

func (p *CodexPlugin) handleCodexRoute(w http.ResponseWriter, r *http.Request) {
	p.mu.RLock()
	svc := p.service
	p.mu.RUnlock()
	if svc == nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{
			"error": "codex plugin not initialized",
		})
		return
	}

	switch normalizeCodexRoutePath(r.URL.Path) {
	case "/codex/v1/responses", "/codex/v1/responses/compact":
		if isCodexWebsocketRequest(r) {
			if normalizeCodexRoutePath(r.URL.Path) != "/codex/v1/responses" {
				writeJSON(w, http.StatusBadRequest, map[string]any{
					"error": "streaming not supported for compact responses websocket",
				})
				return
			}
			svc.HandleResponsesWebsocket(w, r, nil)
			return
		}
		svc.HandleResponses(w, r)
	case "/codex/v1/chat/completions":
		svc.HandleChatCompletions(w, r)
	case "/codex/v1/images/generations", "/codex/v1/images/edits":
		svc.HandleOpenAIImages(w, r)
	case "/codex/v1/models":
		handleCodexModels(w, r)
	default:
		writeJSON(w, http.StatusNotFound, map[string]any{
			"error": "codex route not found",
			"path":  r.URL.Path,
		})
	}
}

func normalizeCodexRoutePath(path string) string {
	path = strings.ToLower(strings.TrimSpace(path))
	if path == "" {
		return ""
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	if len(path) > 1 {
		path = strings.TrimRight(path, "/")
	}
	return path
}

func handleCodexModels(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{
			"error": "method not allowed",
		})
		return
	}

	models := make([]map[string]any, 0, len(codexModelsCatalog))
	for _, model := range codexModelsCatalog {
		models = append(models, map[string]any{
			"id":       model.ID,
			"object":   "model",
			"created":  model.Created,
			"owned_by": model.OwnedBy,
		})
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"object": "list",
		"data":   models,
	})
}

func (s *CodexService) HandleOpenAIImages(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
		return
	}
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "failed to read body", http.StatusBadRequest)
		return
	}
	_ = r.Body.Close()

	isStreaming := isOpenAIImagesStreamRequested(r, body)
	result := s.RoundTrip(r.Context(), &executor.UpstreamRequest{
		Method:              http.MethodPost,
		TargetPath:          r.URL.Path,
		RawQuery:            r.URL.RawQuery,
		Headers:             r.Header.Clone(),
		Body:                body,
		IsStreaming:         isStreaming,
		RequestModel:        extractModelFromBody(body),
		OriginalPath:        r.URL.Path,
		TargetInterfaceType: "codex",
	})
	if result == nil {
		http.Error(w, "codex image request failed", http.StatusBadGateway)
		return
	}
	writeUpstreamRoundTripHTTPResult(w, result)
}

func isOpenAIImagesStreamRequested(r *http.Request, body []byte) bool {
	if r != nil {
		if strings.Contains(strings.ToLower(r.Header.Get("Accept")), "text/event-stream") {
			return true
		}
		if v := strings.TrimSpace(r.URL.Query().Get("stream")); strings.EqualFold(v, "true") || v == "1" {
			return true
		}
	}
	var payload struct {
		Stream bool `json:"stream"`
	}
	_ = json.Unmarshal(body, &payload)
	return payload.Stream
}
