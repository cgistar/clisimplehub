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
	// ── console 独立体系：SSO + basic，不影响 /xai/v1/* ──
	if strings.HasPrefix(path, "/xai/console") {
		switch {
		case path == "/xai/console/v1/models":
			if r.Method != http.MethodGet {
				writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
				return
			}
			writeJSON(w, http.StatusOK, xaiBackend.ConsoleModelsResponse())
			return
		case path == "/xai/console/v1/chat/completions":
			if r.Method != http.MethodPost {
				writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
				return
			}
			svc.HandleConsoleChatCompletions(w, r)
			return
		case path == "/xai/console/v1/responses":
			if r.Method != http.MethodPost {
				writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
				return
			}
			svc.HandleConsoleResponses(w, r)
			return
		case path == "/xai/console/v1/messages":
			if r.Method != http.MethodPost {
				writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
				return
			}
			svc.HandleConsoleMessages(w, r)
			return
		case path == "/xai/console/v1/images/generations":
			if r.Method != http.MethodPost {
				writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
				return
			}
			svc.HandleConsoleImagesGenerations(w, r)
			return
		case path == "/xai/console/v1/images/edits":
			if r.Method != http.MethodPost {
				writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
				return
			}
			svc.HandleConsoleImagesEdits(w, r)
			return
		case path == "/xai/console/v1/videos", path == "/xai/console/v1/videos/generations":
			if r.Method != http.MethodPost {
				writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
				return
			}
			svc.HandleConsoleVideosCreate(w, r)
			return
		case strings.HasPrefix(path, "/xai/console/v1/videos/"):
			// GET /videos/{id}/content — 最终文件
			// GET /videos/{id}         — 任务状态
			if strings.HasSuffix(path, "/content") {
				if r.Method != http.MethodGet {
					writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
					return
				}
				svc.HandleConsoleVideosContent(w, r)
				return
			}
			if r.Method != http.MethodGet {
				writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
				return
			}
			svc.HandleConsoleVideosGet(w, r)
			return
		case path == "/xai/console/v1/files/video", path == "/xai/console/v1/files/image",
			strings.HasPrefix(path, "/xai/console/v1/files/"):
			// grok2api 的 /v1/files/* 本地缓存接口：本路径明确不支持
			writeJSON(w, http.StatusNotFound, map[string]any{
				"error": map[string]any{
					"type":    "not_supported",
					"message": "local file cache endpoints are not supported under /xai/console; use GET /xai/console/v1/videos/{id}/content for video bytes",
					"path":    r.URL.Path,
				},
			})
			return
		default:
			writeJSON(w, http.StatusNotFound, map[string]any{
				"error": "xai console route not found",
				"path":  r.URL.Path,
			})
			return
		}
	}

	switch {
	case path == "/xai/v1/responses":
		if isWebsocketRequest(r) {
			svc.HandleResponsesWebsocket(w, r)
			return
		}
		if r.Method != http.MethodPost {
			writeAPIError(w, http.StatusMethodNotAllowed, "method not allowed", "invalid_request_error", "method_not_allowed")
			return
		}
		svc.HandleProxy(w, r)
		return

	case path == "/xai/v1/responses/compact":
		if r.Method != http.MethodPost {
			writeAPIError(w, http.StatusMethodNotAllowed, "method not allowed", "invalid_request_error", "method_not_allowed")
			return
		}
		svc.HandleProxy(w, r)
		return

	case path == "/xai/v1/chat/completions":
		if r.Method != http.MethodPost {
			writeAPIError(w, http.StatusMethodNotAllowed, "method not allowed", "invalid_request_error", "method_not_allowed")
			return
		}
		svc.HandleChatCompletions(w, r)
		return

	case path == "/xai/v1/images/generations", path == "/xai/v1/images/edits":
		if r.Method != http.MethodPost {
			writeAPIError(w, http.StatusMethodNotAllowed, "method not allowed", "invalid_request_error", "method_not_allowed")
			return
		}
		svc.HandleImages(w, r, path == "/xai/v1/images/edits")
		return

	case path == "/xai/v1/videos",
		path == "/xai/v1/videos/generations",
		path == "/xai/v1/videos/edits",
		path == "/xai/v1/videos/extensions":
		if r.Method != http.MethodPost {
			writeAPIError(w, http.StatusMethodNotAllowed, "method not allowed", "invalid_request_error", "method_not_allowed")
			return
		}
		svc.HandleVideos(w, r)
		return

	case strings.HasPrefix(path, "/xai/v1/videos/"):
		// GET /xai/v1/videos/{request_id}
		if r.Method != http.MethodGet {
			writeAPIError(w, http.StatusMethodNotAllowed, "method not allowed", "invalid_request_error", "method_not_allowed")
			return
		}
		svc.HandleVideos(w, r)
		return

	case path == "/xai/v1/models":
		if r.Method != http.MethodGet {
			writeAPIError(w, http.StatusMethodNotAllowed, "method not allowed", "invalid_request_error", "method_not_allowed")
			return
		}
		// 本地模型表，不转发上游
		writeJSON(w, http.StatusOK, xaiBackend.LocalModelsResponse())
		return
	}

	// 未显式注册的 /xai 路径一律 404（避免未知 path 被误转发上游）
	writeAPIError(w, http.StatusNotFound, "xai route not found", "invalid_request_error", "route_not_found")
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
