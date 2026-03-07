package kiroplugin

import (
	"net/http"
	"strings"
)

func (p *KiroPlugin) handleKiroRoute(w http.ResponseWriter, r *http.Request) {
	p.mu.RLock()
	svc := p.service
	p.mu.RUnlock()
	if svc == nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{
			"error": "kiro plugin not initialized",
		})
		return
	}

	switch normalizeKiroRoutePath(r.URL.Path) {
	case "/kiro/v1/messages":
		svc.HandleMessages(w, r)
	case "/kiro/v1/models":
		svc.HandleModels(w, r)
	case "/kiro/config":
		svc.HandleKiroConfig(w, r)
	case "/kiro/getusage":
		svc.HandleKiroGetUsage(w, r)
	default:
		writeJSON(w, http.StatusNotFound, map[string]any{
			"error": "kiro route not found",
			"path":  r.URL.Path,
		})
	}
}

func normalizeKiroRoutePath(path string) string {
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
