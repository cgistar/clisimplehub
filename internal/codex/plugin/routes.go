package codexplugin

import (
	"net/http"
	"strings"
)

type codexModelCatalogEntry struct {
	ID      string
	Created int64
	OwnedBy string
}

var codexModelsCatalog = []codexModelCatalogEntry{
	{ID: "gpt-5", Created: 1754524800, OwnedBy: "openai"},
	{ID: "gpt-5-codex", Created: 1757894400, OwnedBy: "openai"},
	{ID: "gpt-5-codex-mini", Created: 1762473600, OwnedBy: "openai"},
	{ID: "gpt-5.1", Created: 1762905600, OwnedBy: "openai"},
	{ID: "gpt-5.1-codex", Created: 1762905600, OwnedBy: "openai"},
	{ID: "gpt-5.1-codex-mini", Created: 1762905600, OwnedBy: "openai"},
	{ID: "gpt-5.1-codex-max", Created: 1763424000, OwnedBy: "openai"},
	{ID: "gpt-5.2", Created: 1765440000, OwnedBy: "openai"},
	{ID: "gpt-5.2-codex", Created: 1765440000, OwnedBy: "openai"},
	{ID: "gpt-5.3-codex", Created: 1770307200, OwnedBy: "openai"},
	{ID: "gpt-5.3-codex-spark", Created: 1770912000, OwnedBy: "openai"},
	{ID: "gpt-5.4", Created: 1772668800, OwnedBy: "openai"},
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
		svc.HandleResponses(w, r)
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
