package proxy

import (
	"encoding/json"
	"net/http"
	"strings"

	"clisimplehub/internal/plugin"
)

func (p *ProxyServer) handleWebUICodexOAuthStart(w http.ResponseWriter, r *http.Request) {
	provider := plugin.GetCodexDesktopProviderCached()
	if provider == nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "codex-accounts 插件不可用"})
		return
	}

	raw, err := provider.StartOAuthURLLogin(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "生成登录链接失败"})
		return
	}
	writeJSON(w, http.StatusOK, payload)
}

func (p *ProxyServer) handleWebUICodexOAuthStatus(w http.ResponseWriter, r *http.Request) {
	state := strings.TrimSpace(r.URL.Query().Get("state"))
	if state == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "缺少 state"})
		return
	}
	provider := plugin.GetCodexDesktopProviderCached()
	if provider == nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "codex-accounts 插件不可用"})
		return
	}
	raw, err := provider.OAuthURLLoginStatus(state)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "登录状态解析失败"})
		return
	}
	writeJSON(w, http.StatusOK, payload)
}

func (p *ProxyServer) handleWebUICodexOAuthCallback(w http.ResponseWriter, r *http.Request) {
	var req struct {
		RedirectURL      string `json:"redirectUrl"`
		RedirectURLSnake string `json:"redirect_url"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "请求体格式无效"})
		return
	}
	callbackURL := strings.TrimSpace(req.RedirectURL)
	if callbackURL == "" {
		callbackURL = strings.TrimSpace(req.RedirectURLSnake)
	}
	if callbackURL == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "请粘贴回调 URL"})
		return
	}

	provider := plugin.GetCodexDesktopProviderCached()
	if provider == nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "codex-accounts 插件不可用"})
		return
	}

	raw, err := provider.CompleteOAuthURLLogin(r.Context(), callbackURL)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "登录结果解析失败"})
		return
	}
	writeJSON(w, http.StatusOK, payload)
}

func (p *ProxyServer) handleWebUICodexOAuthCancel(w http.ResponseWriter, r *http.Request) {
	var req struct {
		State string `json:"state"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "请求体格式无效"})
		return
	}

	provider := plugin.GetCodexDesktopProviderCached()
	if provider == nil {
		writeJSON(w, http.StatusOK, map[string]any{"message": "oauth login cancelled"})
		return
	}
	if err := provider.CancelOAuthURLLogin(req.State); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"message": "oauth login cancelled"})
}
