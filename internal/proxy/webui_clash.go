package proxy

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"clisimplehub/internal/plugin"

	"github.com/go-chi/chi/v5"
)

const webUIClashBodyLimit = 8 << 20

var resolveWebUIClashProvider = func() plugin.ClashDesktopProvider {
	return plugin.GetClashDesktopProviderCached()
}

func webUIClashProvider() plugin.ClashDesktopProvider {
	provider := resolveWebUIClashProvider()
	if provider == nil || !provider.IsAvailable() {
		return nil
	}
	return provider
}

func (p *ProxyServer) registerWebUIClashRoutes(r chi.Router) {
	auth := p.requireWebUISession
	r.Get("/web/api/clash", auth(p.handleWebUIClash))
	r.Post("/web/api/clash/start", auth(p.handleWebUIClashStart))
	r.Post("/web/api/clash/stop", auth(p.handleWebUIClashStop))
	r.Post("/web/api/clash/reload", auth(p.handleWebUIClashReload))
	r.Put("/web/api/clash/config", auth(p.handleWebUIClashSaveConfig))
	r.Post("/web/api/clash/subscriptions", auth(p.handleWebUIClashAddSubscription))
	r.Put("/web/api/clash/subscriptions/{subscriptionId}", auth(p.handleWebUIClashUpdateSubscription))
	r.Delete("/web/api/clash/subscriptions/{subscriptionId}", auth(p.handleWebUIClashRemoveSubscription))
	r.Post("/web/api/clash/subscriptions/{subscriptionId}/toggle", auth(p.handleWebUIClashToggleSubscription))
	r.Post("/web/api/clash/subscriptions/{subscriptionId}/active", auth(p.handleWebUIClashSetActiveSubscription))
	r.Post("/web/api/clash/subscriptions/{subscriptionId}/refresh", auth(p.handleWebUIClashRefreshSubscription))
	r.Post("/web/api/clash/subscriptions/{subscriptionId}/nodes/parse", auth(p.handleWebUIClashParseNodes))
	r.Put("/web/api/clash/subscriptions/{subscriptionId}/nodes", auth(p.handleWebUIClashReplaceNodes))
	r.Post("/web/api/clash/chain", auth(p.handleWebUIClashSetChain))
	r.Get("/web/api/clash/nodes/config", auth(p.handleWebUIClashNodeConfig))
	r.Post("/web/api/clash/nodes/test", auth(p.handleWebUIClashTestNode))
	r.Post("/web/api/clash/nodes/tests/cancel", auth(p.handleWebUIClashCancelTests))
}

func writeWebUIClashUnavailable(w http.ResponseWriter) {
	writeJSON(w, http.StatusBadRequest, map[string]any{"error": "Clash/Mihomo 运行时不可用"})
}

func requireWebUIClashProvider(w http.ResponseWriter) (plugin.ClashDesktopProvider, bool) {
	provider := webUIClashProvider()
	if provider == nil {
		writeWebUIClashUnavailable(w)
		return nil, false
	}
	return provider, true
}

func decodeWebUIClashJSON(r *http.Request, target any) error {
	decoder := json.NewDecoder(io.LimitReader(r.Body, webUIClashBodyLimit+1))
	return decoder.Decode(target)
}

func writeWebUIClashRaw(w http.ResponseWriter, raw json.RawMessage) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(raw)
}

func (p *ProxyServer) handleWebUIClash(w http.ResponseWriter, _ *http.Request) {
	provider := webUIClashProvider()
	if provider == nil {
		writeJSON(w, http.StatusOK, map[string]any{"available": false, "message": "Clash/Mihomo 运行时不可用"})
		return
	}
	status, err := provider.GetStatus()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	config, err := provider.GetConfig(p.getConfigPath())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	nodes, err := provider.GetNodes()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	var statusValue, configValue, nodesValue any
	if json.Unmarshal(status, &statusValue) != nil || json.Unmarshal(config, &configValue) != nil || json.Unmarshal(nodes, &nodesValue) != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "Clash 数据格式无效"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"available": true, "status": statusValue, "config": configValue, "nodes": nodesValue})
}

func (p *ProxyServer) handleWebUIClashStart(w http.ResponseWriter, _ *http.Request) {
	provider, ok := requireWebUIClashProvider(w)
	if !ok {
		return
	}
	if err := provider.Start(); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"message": "代理已启动"})
}

func (p *ProxyServer) handleWebUIClashStop(w http.ResponseWriter, _ *http.Request) {
	provider, ok := requireWebUIClashProvider(w)
	if !ok {
		return
	}
	if err := provider.Stop(); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"message": "代理已停止"})
}

func (p *ProxyServer) handleWebUIClashReload(w http.ResponseWriter, _ *http.Request) {
	provider, ok := requireWebUIClashProvider(w)
	if !ok {
		return
	}
	if err := provider.ReloadConfigFromDisk(); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"message": "代理配置已刷新"})
}

func (p *ProxyServer) handleWebUIClashSaveConfig(w http.ResponseWriter, r *http.Request) {
	provider, ok := requireWebUIClashProvider(w)
	if !ok {
		return
	}
	var raw json.RawMessage
	if err := decodeWebUIClashJSON(r, &raw); err != nil || len(raw) == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "请求体格式无效"})
		return
	}
	if err := provider.SaveConfig(p.getConfigPath(), raw); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"message": "代理配置已保存"})
}

func clashSubscriptionID(r *http.Request) string {
	return strings.TrimSpace(chi.URLParam(r, "subscriptionId"))
}

func (p *ProxyServer) handleWebUIClashAddSubscription(w http.ResponseWriter, r *http.Request) {
	provider, ok := requireWebUIClashProvider(w)
	if !ok {
		return
	}
	var req struct {
		Name string `json:"name"`
		URL  string `json:"url"`
	}
	if decodeWebUIClashJSON(r, &req) != nil || strings.TrimSpace(req.URL) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "订阅 URL 必填"})
		return
	}
	if err := provider.AddSubscription(req.Name, req.URL); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"message": "订阅已添加"})
}

func (p *ProxyServer) handleWebUIClashUpdateSubscription(w http.ResponseWriter, r *http.Request) {
	provider, ok := requireWebUIClashProvider(w)
	if !ok {
		return
	}
	id := clashSubscriptionID(r)
	var req struct {
		Name string `json:"name"`
		URL  string `json:"url"`
	}
	if id == "" || decodeWebUIClashJSON(r, &req) != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "请求体格式无效"})
		return
	}
	if err := provider.UpdateSubscription(id, req.Name, req.URL); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"message": "订阅已更新"})
}

func (p *ProxyServer) handleWebUIClashRemoveSubscription(w http.ResponseWriter, r *http.Request) {
	provider, ok := requireWebUIClashProvider(w)
	if !ok {
		return
	}
	id := clashSubscriptionID(r)
	if id == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "subscriptionId 必填"})
		return
	}
	if err := provider.RemoveSubscription(id); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"message": "订阅已删除"})
}

func (p *ProxyServer) handleWebUIClashToggleSubscription(w http.ResponseWriter, r *http.Request) {
	provider, ok := requireWebUIClashProvider(w)
	if !ok {
		return
	}
	id := clashSubscriptionID(r)
	if id == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "subscriptionId 必填"})
		return
	}
	if err := provider.ToggleSubscription(id); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"message": "订阅状态已更新"})
}

func (p *ProxyServer) handleWebUIClashSetActiveSubscription(w http.ResponseWriter, r *http.Request) {
	provider, ok := requireWebUIClashProvider(w)
	if !ok {
		return
	}
	id := clashSubscriptionID(r)
	if id == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "subscriptionId 必填"})
		return
	}
	if err := provider.SetActiveSubscription(id); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"message": "活跃订阅已更新"})
}

func (p *ProxyServer) handleWebUIClashRefreshSubscription(w http.ResponseWriter, r *http.Request) {
	provider, ok := requireWebUIClashProvider(w)
	if !ok {
		return
	}
	id := clashSubscriptionID(r)
	if id == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "subscriptionId 必填"})
		return
	}
	raw, err := provider.RefreshSingleSubscription(r.Context(), id)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	writeWebUIClashRaw(w, raw)
}

func (p *ProxyServer) handleWebUIClashSetChain(w http.ResponseWriter, r *http.Request) {
	provider, ok := requireWebUIClashProvider(w)
	if !ok {
		return
	}
	var req struct {
		SubscriptionID string `json:"subscriptionId"`
	}
	if decodeWebUIClashJSON(r, &req) != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "请求体格式无效"})
		return
	}
	if err := provider.SetDialerProxySubscription(strings.TrimSpace(req.SubscriptionID)); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"message": "链式代理已更新"})
}

func (p *ProxyServer) handleWebUIClashParseNodes(w http.ResponseWriter, r *http.Request) {
	provider, ok := requireWebUIClashProvider(w)
	if !ok {
		return
	}
	id := clashSubscriptionID(r)
	var req struct {
		Content string `json:"content"`
	}
	if id == "" || decodeWebUIClashJSON(r, &req) != nil || strings.TrimSpace(req.Content) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "节点内容必填"})
		return
	}
	raw, err := provider.ParseNodesForSubscription(id, req.Content)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	writeWebUIClashRaw(w, raw)
}

func (p *ProxyServer) handleWebUIClashReplaceNodes(w http.ResponseWriter, r *http.Request) {
	provider, ok := requireWebUIClashProvider(w)
	if !ok {
		return
	}
	id := clashSubscriptionID(r)
	var req struct {
		Nodes        json.RawMessage `json:"nodes"`
		SelectedNode string          `json:"selectedNode"`
	}
	if id == "" || decodeWebUIClashJSON(r, &req) != nil || len(req.Nodes) == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "请求体格式无效"})
		return
	}
	var probe []map[string]any
	if json.Unmarshal(req.Nodes, &probe) != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "nodes 必须是数组"})
		return
	}
	if err := provider.ReplaceSubscriptionNodes(id, req.Nodes, req.SelectedNode); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"message": "节点已保存"})
}

func (p *ProxyServer) handleWebUIClashNodeConfig(w http.ResponseWriter, r *http.Request) {
	provider, ok := requireWebUIClashProvider(w)
	if !ok {
		return
	}
	name := strings.TrimSpace(r.URL.Query().Get("nodeName"))
	if name == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "nodeName 必填"})
		return
	}
	config, err := provider.GetNodeConfig(name)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"config": config})
}

func (p *ProxyServer) handleWebUIClashTestNode(w http.ResponseWriter, r *http.Request) {
	provider, ok := requireWebUIClashProvider(w)
	if !ok {
		return
	}
	var req struct {
		NodeName string `json:"nodeName"`
		Mode     string `json:"mode"`
	}
	if decodeWebUIClashJSON(r, &req) != nil || strings.TrimSpace(req.NodeName) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "nodeName 必填"})
		return
	}
	ctx := r.Context()
	var raw json.RawMessage
	var err error
	switch strings.ToLower(strings.TrimSpace(req.Mode)) {
	case "http":
		raw, err = provider.TestNode(ctx, req.NodeName)
	case "tcp":
		raw, err = provider.TestNodeTCP(ctx, req.NodeName)
	default:
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "mode 必须为 http 或 tcp"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	writeWebUIClashRaw(w, raw)
}

func (p *ProxyServer) handleWebUIClashCancelTests(w http.ResponseWriter, _ *http.Request) {
	provider, ok := requireWebUIClashProvider(w)
	if !ok {
		return
	}
	if err := provider.CancelSpeedTests(); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"message": "测速已取消"})
}
