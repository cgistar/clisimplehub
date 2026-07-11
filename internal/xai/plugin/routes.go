package xaiplugin

import (
	"net/http"
	"strings"

	xaiBackend "clisimplehub/internal/xai/backend"
)

func (p *XaiPlugin) handleXaiRoute(w http.ResponseWriter, r *http.Request) {
	p.mu.RLock()
	svc := p.service
	path := normalizeXaiRoutePath(r.URL.Path)
	p.mu.RUnlock()
	if svc == nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "xai plugin not initialized"})
		return
	}

	// 管理接口（账号列表/配置）
	switch path {
	case "/xai", "/xai/accounts":
		if r.Method == http.MethodGet {
			raw, err := p.GetAccounts(p.xaiJsonPath)
			if err != nil {
				writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
				return
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(raw)
			return
		}
	case "/xai/config":
		if r.Method == http.MethodGet {
			raw, err := p.GetXaiGlobalConfig(p.xaiJsonPath)
			if err != nil {
				writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
				return
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(raw)
			return
		}
	}

	// API 转发
	switch {
	case path == "/xai/v1/responses":
		if isWebsocketRequest(r) {
			svc.HandleResponsesWebsocket(w, r)
			return
		}
		if r.Method != http.MethodPost {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
			return
		}
		svc.HandleProxy(w, r)
		return

	case path == "/xai/v1/responses/compact":
		if r.Method != http.MethodPost {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
			return
		}
		svc.HandleProxy(w, r)
		return

	case path == "/xai/v1/chat/completions":
		if r.Method != http.MethodPost {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
			return
		}
		svc.HandleChatCompletions(w, r)
		return

	case path == "/xai/v1/images/generations", path == "/xai/v1/images/edits":
		if r.Method != http.MethodPost {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
			return
		}
		svc.HandleProxy(w, r)
		return

	case path == "/xai/v1/videos/generations",
		path == "/xai/v1/videos/edits",
		path == "/xai/v1/videos/extensions":
		if r.Method != http.MethodPost {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
			return
		}
		svc.HandleProxy(w, r)
		return

	case strings.HasPrefix(path, "/xai/v1/videos/"):
		// GET /xai/v1/videos/{request_id}
		if r.Method != http.MethodGet {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
			return
		}
		svc.HandleProxy(w, r)
		return

	case path == "/xai/v1/models":
		if r.Method != http.MethodGet {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
			return
		}
		// 本地模型表，不转发上游
		writeJSON(w, http.StatusOK, xaiBackend.LocalModelsResponse())
		return
	}

	// 未显式注册的 /xai 路径一律 404（避免未知 path 被误转发上游）
	writeJSON(w, http.StatusNotFound, map[string]any{
		"error": "xai route not found",
		"path":  r.URL.Path,
	})
}

func normalizeXaiRoutePath(path string) string {
	path = strings.TrimSpace(path)
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

func isWebsocketRequest(r *http.Request) bool {
	if r == nil {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(r.Header.Get("Upgrade")), "websocket")
}
