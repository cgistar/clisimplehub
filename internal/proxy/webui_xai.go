package proxy

import (
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"

	"clisimplehub/internal/plugin"

	"github.com/go-chi/chi/v5"
)

func resolveWebUIXaiConfigPath(provider plugin.XaiDesktopProvider, configPath string) string {
	if provider == nil {
		return ""
	}
	defaultPath := provider.DefaultMultiConfigBasename()
	if strings.TrimSpace(configPath) == "" {
		return defaultPath
	}
	return filepath.Join(filepath.Dir(configPath), filepath.Base(defaultPath))
}

func (p *ProxyServer) handleWebUIXai(w http.ResponseWriter, r *http.Request) {
	provider := plugin.GetXaiDesktopProviderCached()
	if provider == nil {
		writeJSON(w, http.StatusOK, map[string]any{
			"available": false,
			"message":   "xai-accounts 插件不可用",
		})
		return
	}

	xaiPath := resolveWebUIXaiConfigPath(provider, p.getConfigPath())
	accountsRaw, err := provider.GetAccounts(xaiPath)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	globalConfigRaw, err := provider.GetXaiGlobalConfig(xaiPath)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}

	var accountsPayload map[string]any
	if err := json.Unmarshal(accountsRaw, &accountsPayload); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "failed to parse xai accounts payload"})
		return
	}
	var globalConfigPayload map[string]any
	if err := json.Unmarshal(globalConfigRaw, &globalConfigPayload); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "failed to parse xai config payload"})
		return
	}

	response := map[string]any{
		"available":    true,
		"configPath":   xaiPath,
		"globalConfig": globalConfigPayload,
	}
	for key, value := range accountsPayload {
		response[key] = value
	}
	writeJSON(w, http.StatusOK, response)
}

func (p *ProxyServer) handleWebUIActiveXaiAccount(w http.ResponseWriter, r *http.Request) {
	provider, accountID, xaiPath, ok := p.parseWebUIXaiAccountAction(w, r)
	if !ok {
		return
	}
	if err := provider.SetActiveAccount(xaiPath, accountID); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"message": "active xai account updated"})
}

func (p *ProxyServer) handleWebUIRefreshXaiToken(w http.ResponseWriter, r *http.Request) {
	provider, accountID, xaiPath, ok := p.parseWebUIXaiAccountAction(w, r)
	if !ok {
		return
	}
	result, err := provider.TestAccount(xaiPath, accountID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	var payload map[string]any
	if err := json.Unmarshal(result, &payload); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "failed to parse refresh token result"})
		return
	}
	payload["message"] = "refresh token updated"
	writeJSON(w, http.StatusOK, payload)
}

func (p *ProxyServer) handleWebUIProbeXaiStream(w http.ResponseWriter, r *http.Request) {
	provider, accountID, xaiPath, ok := p.parseWebUIXaiAccountAction(w, r)
	if !ok {
		return
	}
	result, err := provider.ProbeAccountStream(r.Context(), xaiPath, accountID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	var payload map[string]any
	if err := json.Unmarshal(result, &payload); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "failed to parse probe result"})
		return
	}
	if success, _ := payload["success"].(bool); !success {
		errMsg, _ := payload["error"].(string)
		if strings.TrimSpace(errMsg) == "" {
			errMsg, _ = payload["detail"].(string)
		}
		if strings.TrimSpace(errMsg) == "" {
			errMsg = "probe failed"
		}
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": errMsg, "result": payload})
		return
	}
	payload["message"] = "probe stream ok"
	writeJSON(w, http.StatusOK, payload)
}

func (p *ProxyServer) handleWebUIRefreshXaiQuota(w http.ResponseWriter, r *http.Request) {
	provider, accountID, xaiPath, ok := p.parseWebUIXaiAccountAction(w, r)
	if !ok {
		return
	}
	result, err := provider.RefreshAccountQuota(r.Context(), xaiPath, accountID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	var payload map[string]any
	if err := json.Unmarshal(result, &payload); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "failed to parse refresh quota result"})
		return
	}
	if success, _ := payload["success"].(bool); !success {
		errMsg, _ := payload["error"].(string)
		if strings.TrimSpace(errMsg) == "" {
			errMsg = "refresh quota failed"
		}
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": errMsg, "result": payload})
		return
	}
	payload["message"] = "quota refreshed"
	writeJSON(w, http.StatusOK, payload)
}

func (p *ProxyServer) handleWebUIXaiSSOToAuth(w http.ResponseWriter, r *http.Request) {
	provider, accountID, xaiPath, ok := p.parseWebUIXaiAccountAction(w, r)
	if !ok {
		return
	}
	result, err := provider.ConvertSSOToAuth(r.Context(), xaiPath, accountID)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	var payload map[string]any
	if err := json.Unmarshal(result, &payload); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "failed to parse sso2auth result"})
		return
	}
	if success, _ := payload["success"].(bool); !success {
		errMsg, _ := payload["error"].(string)
		if strings.TrimSpace(errMsg) == "" {
			errMsg = "sso2auth failed"
		}
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": errMsg, "result": payload})
		return
	}
	payload["message"] = "SSO2Auth completed"
	writeJSON(w, http.StatusOK, payload)
}

func (p *ProxyServer) handleWebUIXaiSSOImport(w http.ResponseWriter, r *http.Request) {
	provider := plugin.GetXaiDesktopProviderCached()
	if provider == nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "xai-accounts 插件不可用"})
		return
	}
	var req struct {
		SSO string `json:"sso"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "请求体格式无效"})
		return
	}
	if strings.TrimSpace(req.SSO) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "sso 必填"})
		return
	}
	xaiPath := resolveWebUIXaiConfigPath(provider, p.getConfigPath())
	result, err := provider.ImportSSOAccount(r.Context(), xaiPath, req.SSO)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	var payload map[string]any
	if err := json.Unmarshal(result, &payload); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "failed to parse sso import result"})
		return
	}
	payload["message"] = "xai sso account imported"
	writeJSON(w, http.StatusOK, payload)
}

func (p *ProxyServer) handleWebUISaveXaiConfig(w http.ResponseWriter, r *http.Request) {
	provider := plugin.GetXaiDesktopProviderCached()
	if provider == nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "xai-accounts 插件不可用"})
		return
	}
	var req map[string]any
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "请求体格式无效"})
		return
	}
	dto, err := json.Marshal(req)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "配置数据格式无效"})
		return
	}
	xaiPath := resolveWebUIXaiConfigPath(provider, p.getConfigPath())
	if err := provider.SaveXaiGlobalConfig(xaiPath, dto); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"message": "xai config saved"})
}

func (p *ProxyServer) handleWebUISetXaiAutoRefreshToken(w http.ResponseWriter, r *http.Request) {
	provider := plugin.GetXaiDesktopProviderCached()
	if provider == nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "xai-accounts 插件不可用"})
		return
	}
	var req struct {
		Enabled *bool `json:"enabled"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Enabled == nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "enabled 必须是布尔值"})
		return
	}
	xaiPath := resolveWebUIXaiConfigPath(provider, p.getConfigPath())
	if err := provider.SetAutoRefreshToken(xaiPath, *req.Enabled); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"message": "xai automatic token refresh updated",
		"enabled": *req.Enabled,
	})
}

func (p *ProxyServer) handleWebUIAddXaiAccount(w http.ResponseWriter, r *http.Request) {
	provider := plugin.GetXaiDesktopProviderCached()
	if provider == nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "xai-accounts 插件不可用"})
		return
	}
	var req map[string]any
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "请求体格式无效"})
		return
	}
	refreshToken := strings.TrimSpace(fmt.Sprint(req["refreshToken"]))
	if refreshToken == "<nil>" {
		refreshToken = ""
	}
	accessToken := strings.TrimSpace(fmt.Sprint(req["accessToken"]))
	if accessToken == "<nil>" {
		accessToken = ""
	}
	apiKey := strings.TrimSpace(fmt.Sprint(req["apiKey"]))
	if apiKey == "<nil>" {
		apiKey = ""
	}
	sso := strings.TrimSpace(fmt.Sprint(req["sso"]))
	if sso == "<nil>" {
		sso = ""
	}
	if refreshToken == "" && accessToken == "" && apiKey == "" && sso == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "refreshToken、accessToken、apiKey 或 sso 至少需要一个"})
		return
	}
	dto, err := json.Marshal(req)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "账号数据格式无效"})
		return
	}
	xaiPath := resolveWebUIXaiConfigPath(provider, p.getConfigPath())
	raw, err := provider.AddAccount(xaiPath, dto)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "failed to parse xai account payload"})
		return
	}
	payload["message"] = "xai account added"
	writeJSON(w, http.StatusOK, payload)
}

func (p *ProxyServer) handleWebUIUpdateXaiAccount(w http.ResponseWriter, r *http.Request) {
	provider := plugin.GetXaiDesktopProviderCached()
	if provider == nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "xai-accounts 插件不可用"})
		return
	}
	var req map[string]any
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "请求体格式无效"})
		return
	}
	accountID := strings.TrimSpace(fmt.Sprint(req["id"]))
	if accountID == "" || accountID == "<nil>" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "id 必填"})
		return
	}
	dto, err := json.Marshal(req)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "账号数据格式无效"})
		return
	}
	xaiPath := resolveWebUIXaiConfigPath(provider, p.getConfigPath())
	if err := provider.UpdateAccount(xaiPath, dto); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"message": "xai account updated"})
}

func (p *ProxyServer) handleWebUIDeleteXaiAccount(w http.ResponseWriter, r *http.Request) {
	provider := plugin.GetXaiDesktopProviderCached()
	if provider == nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "xai-accounts 插件不可用"})
		return
	}
	accountID := strings.TrimSpace(chi.URLParam(r, "accountId"))
	if accountID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "accountId 必填"})
		return
	}
	xaiPath := resolveWebUIXaiConfigPath(provider, p.getConfigPath())
	if err := provider.DeleteAccount(xaiPath, accountID); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"message": "xai account deleted"})
}

func (p *ProxyServer) parseWebUIXaiAccountAction(w http.ResponseWriter, r *http.Request) (plugin.XaiDesktopProvider, string, string, bool) {
	provider := plugin.GetXaiDesktopProviderCached()
	if provider == nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "xai-accounts 插件不可用"})
		return nil, "", "", false
	}
	var req struct {
		AccountID string `json:"accountId"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "请求体格式无效"})
		return nil, "", "", false
	}
	accountID := strings.TrimSpace(req.AccountID)
	if accountID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "accountId 必填"})
		return nil, "", "", false
	}
	xaiPath := resolveWebUIXaiConfigPath(provider, p.getConfigPath())
	return provider, accountID, xaiPath, true
}
