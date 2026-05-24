package clashplugin

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"clisimplehub/internal/plugin"
	"gopkg.in/yaml.v3"
)

func registerRoutes(r plugin.RouteRegistrar, svc *ClashService) {
	h := &handler{svc: svc}

	r.HandleFunc("/clash/status", r.RequireAuth(h.handleStatus))
	r.HandleFunc("/clash/nodes", r.RequireAuth(h.handleNodes))
	r.HandleFunc("/clash/nodes/select", r.RequireAuth(h.handleSelectNode))
	r.HandleFunc("/clash/nodes/test", r.RequireAuth(h.handleTestNode))
	r.HandleFunc("/clash/subscriptions/refresh", r.RequireAuth(h.handleRefreshSubscriptions))
	r.HandleFunc("/clash/subscriptions/add", r.RequireAuth(h.handleAddSubscription))
	r.HandleFunc("/clash/subscriptions/remove", r.RequireAuth(h.handleRemoveSubscription))
	r.HandleFunc("/clash/start", r.RequireAuth(h.handleStart))
	r.HandleFunc("/clash/stop", r.RequireAuth(h.handleStop))
	r.HandleFunc("/clash/config", r.RequireAuth(h.handleConfig))
	r.HandleFunc("/sub", h.handleSubscriptionConvert)
	r.HandleFunc("/subip", h.handleSubscriptionIPs)
	r.HandleFunc("/rosip", h.handleRouterOSSubscriptionIPs)
	r.HandleFunc("/grouprule/mihomo", h.handleMihomoGroupRule)
}

type handler struct {
	svc *ClashService
}

func (h *handler) handleStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	writeJSON(w, h.svc.GetStatus())
}

func (h *handler) handleNodes(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	writeJSON(w, h.svc.GetNodes())
}

func (h *handler) handleSelectNode(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		NodeName string `json:"nodeName"`
	}
	if err := readJSON(r, &req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := h.svc.SelectNode(req.NodeName); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]string{"status": "ok"})
}

func (h *handler) handleTestNode(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		NodeName string `json:"nodeName"`
	}
	if err := readJSON(r, &req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), speedTestTotalTimeout)
	defer cancel()

	result := testSingleNode(ctx, h.svc, req.NodeName)
	writeJSON(w, result)
}

func (h *handler) handleRefreshSubscriptions(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	result, err := h.svc.RefreshSubscriptions(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, result)
}

func (h *handler) handleAddSubscription(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		Name string `json:"name"`
		URL  string `json:"url"`
	}
	if err := readJSON(r, &req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if strings.TrimSpace(req.URL) == "" {
		http.Error(w, "url is required", http.StatusBadRequest)
		return
	}
	if err := h.svc.AddSubscription(req.Name, req.URL); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]string{"status": "ok"})
}

func (h *handler) handleRemoveSubscription(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		ID string `json:"id"`
	}
	if err := readJSON(r, &req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := h.svc.RemoveSubscription(req.ID); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]string{"status": "ok"})
}

func (h *handler) handleStart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if err := h.svc.Start(); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]string{"status": "ok"})
}

func (h *handler) handleStop(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if err := h.svc.Stop(); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]string{"status": "ok"})
}

func (h *handler) handleConfig(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, h.svc.config.Get())
	case http.MethodPut:
		var cfg ClashConfig
		if err := readJSON(r, &cfg); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		before := h.svc.config.Get()
		if err := h.svc.config.Update(func(c *ClashConfig) {
			*c = cfg
		}); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		after := h.svc.config.Get()
		h.svc.reconcileRuntimeAfterConfigChangeBestEffort(before, after, "", "http save config")
		writeJSON(w, map[string]string{"status": "ok"})
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (h *handler) handleSubscriptionConvert(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	targetName := r.URL.Query().Get("target")
	if strings.TrimSpace(targetName) == "" {
		targetName = r.URL.Query().Get("flag")
	}
	target, err := normalizeSubscriptionTarget(targetName)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	urls := splitSubscriptionURLs(r.URL.Query()["url"])
	if len(urls) == 0 {
		http.Error(w, "url is required", http.StatusBadRequest)
		return
	}
	converter := newSubscriptionConverter(loadConverterSettings(h.svc.dataDir), nil, requestBaseURL(r))
	converter.options = parseConverterOptions(r)
	result, err := converter.Convert(r.Context(), urls, target)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	for key, value := range result.Headers {
		w.Header().Set(key, value)
	}
	w.Header().Set("Content-Type", result.ContentType)
	_, _ = w.Write(result.Body)
}

func (h *handler) handleSubscriptionIPs(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	urls := splitSubscriptionURLs(r.URL.Query()["url"])
	if len(urls) == 0 {
		http.Error(w, "url is required", http.StatusBadRequest)
		return
	}
	converter := newSubscriptionConverter(loadConverterSettings(h.svc.dataDir), nil, requestBaseURL(r))
	info, err := converter.subscriptionServerIPs(r.Context(), urls)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=UTF-8")
	_, _ = w.Write(renderSubscriptionIPReport(info))
}

func (h *handler) handleRouterOSSubscriptionIPs(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	urls := splitSubscriptionURLs(r.URL.Query()["url"])
	if len(urls) == 0 {
		http.Error(w, "url is required", http.StatusBadRequest)
		return
	}
	converter := newSubscriptionConverter(loadConverterSettings(h.svc.dataDir), nil, requestBaseURL(r))
	info, err := converter.subscriptionServerIPs(r.Context(), urls)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=UTF-8")
	_, _ = w.Write(renderRouterOSIPScript(info))
}

func parseConverterOptions(r *http.Request) converterOptions {
	q := r.URL.Query()
	return converterOptions{
		SocksPort: parseQueryInt(q.Get("socks5_port")),
		NoRPRX:    parseQueryBool(q.Get("no_rprx")),
		Landing:   parseQueryBool(q.Get("landing")),
		FixedNode: strings.TrimSpace(q.Get("fixed_node")),
		Tun:       parseQueryBool(q.Get("tun")),
		Version:   strings.TrimSpace(q.Get("ver")),
	}
}

func parseQueryInt(raw string) int {
	var value int
	_, _ = fmt.Sscanf(strings.TrimSpace(raw), "%d", &value)
	return value
}

func parseQueryBool(raw string) bool {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

func (h *handler) handleMihomoGroupRule(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	groupName := strings.TrimSpace(r.URL.Query().Get("group"))
	if groupName == "" {
		http.Error(w, "group is required", http.StatusBadRequest)
		return
	}
	converter := newSubscriptionConverter(loadConverterSettings(h.svc.dataDir), nil, requestBaseURL(r))
	rules := converter.groupRules(r.Context(), groupName)
	if r.URL.Query().Get("format") == "yaml" {
		w.Header().Set("Content-Type", "application/yaml; charset=UTF-8")
		data, err := yaml.Marshal(map[string]any{"payload": rules})
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		_, _ = w.Write(data)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=UTF-8")
	_, _ = w.Write([]byte(strings.Join(rules, "\n")))
}

func requestBaseURL(r *http.Request) string {
	scheme := "http"
	if forwardedProto := forwardedHeaderValue(r.Header, "X-Forwarded-Proto"); forwardedProto != "" {
		scheme = forwardedProto
	} else if r.TLS != nil {
		scheme = "https"
	}
	host := forwardedHeaderValue(r.Header, "X-Forwarded-Host")
	if host == "" {
		host = strings.TrimSpace(r.Host)
	}
	if host == "" {
		host = "localhost"
	}
	return scheme + "://" + host
}

func forwardedHeaderValue(headers http.Header, name string) string {
	value := strings.TrimSpace(headers.Get(name))
	if value == "" {
		return ""
	}
	if idx := strings.Index(value, ","); idx >= 0 {
		value = value[:idx]
	}
	return strings.TrimSpace(value)
}

func writeJSON(w http.ResponseWriter, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	data, err := json.Marshal(v)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	_, _ = w.Write(data)
}

func readJSON(r *http.Request, v interface{}) error {
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		return fmt.Errorf("read body: %w", err)
	}
	if len(body) == 0 {
		return fmt.Errorf("empty body")
	}
	return json.Unmarshal(body, v)
}
