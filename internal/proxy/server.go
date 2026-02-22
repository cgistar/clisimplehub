// Package proxy implements the HTTP proxy server for AI API requests.
package proxy

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"clisimplehub/internal/config"
	"clisimplehub/internal/plugin"
	"clisimplehub/internal/statsdb"
	"clisimplehub/internal/storage"
)

// ProxyServer represents the main proxy server implementation
type ProxyServer struct {
	port       int
	listenAddr string
	router     Router
	server     *http.Server
	stats      *StatsManager
	sseHub     *SSEHub
	mu         sync.RWMutex
	authKey    string
	store      storage.Storage
	usageStats statsdb.UsageStatsStore
	configPath string
	reloadFunc func() // 配置重载回调函数

	fallbackEnabled bool
	exec            *proxyExecutor
}

// NewProxyServer creates a new ProxyServer instance
func NewProxyServer(port int, router Router) *ProxyServer {
	return &ProxyServer{
		port:       port,
		listenAddr: "0.0.0.0",
		router:     router,
		stats:      NewStatsManager(),
	}
}

// NewProxyServerWithSSEHub creates a new ProxyServer with SSE hub integration.
func NewProxyServerWithSSEHub(port int, router Router, sseHub *SSEHub) *ProxyServer {
	stats := NewStatsManager()
	stats.SetSSEHub(sseHub)

	return &ProxyServer{
		port:       port,
		listenAddr: "0.0.0.0",
		router:     router,
		stats:      stats,
		sseHub:     sseHub,
	}
}

// SetSSEHub sets the SSE hub for real-time updates.
func (p *ProxyServer) SetSSEHub(hub *SSEHub) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.sseHub = hub
	if p.stats != nil {
		p.stats.SetSSEHub(hub)
	}
}

// SetStorage sets the storage for stats persistence and vendor lookup.
func (p *ProxyServer) SetStorage(store storage.Storage) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.store = store
	if p.stats != nil {
		p.stats.SetStorage(store)
	}
}

func (p *ProxyServer) SetUsageStatsStore(store statsdb.UsageStatsStore) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.usageStats = store
}

// GetSSEHub returns the SSE hub.
func (p *ProxyServer) GetSSEHub() *SSEHub {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.sseHub
}

// Start starts the proxy server
// Requirements: 1.1, 5.1, 7.1, 8.5
func (p *ProxyServer) Start() error {
	// 初始化文件调试日志
	p.InitDebugFileLogger()

	mux := http.NewServeMux()

	mux.HandleFunc("/", p.handleProxy)
	mux.HandleFunc("/health", p.handleHealth)
	mux.HandleFunc("/stats", p.handleStats)
	mux.HandleFunc("/transformers", p.handleTransformers)
	mux.HandleFunc("/reload", p.requireAuth(p.handleReload))
	mux.HandleFunc("/endpoint", p.requireAuth(p.handleEndpoint))
	mux.HandleFunc("/sync/config", p.requireAuthStrict(p.handleSyncConfig))

	// Register plugin routes
	registrar := &proxyRouteRegistrar{mux: mux, p: p}
	for _, pl := range plugin.All() {
		pl.RegisterRoutes(registrar)
	}

	if p.sseHub != nil {
		mux.HandleFunc("/sse", p.sseHub.HandleSSE)
	}

	// Get listen address (default to 0.0.0.0 if not set)
	listenAddr := p.GetListenAddr()
	if listenAddr == "" {
		listenAddr = "0.0.0.0"
	}

	p.server = &http.Server{
		Addr:         fmt.Sprintf("%s:%d", listenAddr, p.port),
		Handler:      mux,
		ReadTimeout:  600 * time.Second,
		WriteTimeout: 600 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	return p.server.ListenAndServe()
}

// Stop stops the proxy server gracefully
func (p *ProxyServer) Stop() error {
	if p.server == nil {
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	return p.server.Shutdown(ctx)
}

// GetPort returns the configured port
func (p *ProxyServer) GetPort() int {
	return p.port
}

// SetPort updates the server port (requires restart to take effect)
func (p *ProxyServer) SetPort(port int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.port = port
}

// SetListenAddr sets the listen address (requires restart to take effect)
func (p *ProxyServer) SetListenAddr(addr string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.listenAddr = addr
}

// GetListenAddr returns the listen address (thread-safe)
func (p *ProxyServer) GetListenAddr() string {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.listenAddr
}

func (p *ProxyServer) SetAuthKey(key string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.authKey = strings.TrimSpace(key)
}

func (p *ProxyServer) getAuthKey() string {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.authKey
}

func (p *ProxyServer) requireAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if key := p.getAuthKey(); key != "" && !isAuthorized(r, key) {
			writeJSON(w, http.StatusUnauthorized, map[string]interface{}{
				"error": "Invalid API key",
			})
			return
		}
		next(w, r)
	}
}

// requireAuthStrict requires an API key to be configured and matching.
// Use for endpoints that mutate local state (e.g. /sync/config).
func (p *ProxyServer) requireAuthStrict(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if strings.TrimSpace(p.getAuthKey()) == "" {
			writeJSON(w, http.StatusForbidden, map[string]interface{}{
				"error": "API authentication must be enabled for this endpoint (apiKey not configured)",
			})
			return
		}
		p.requireAuth(next)(w, r)
	}
}

// SetFallbackEnabled sets whether fallback is enabled
func (p *ProxyServer) SetFallbackEnabled(enabled bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.fallbackEnabled = enabled
}

// IsFallbackEnabled returns whether fallback is enabled
func (p *ProxyServer) IsFallbackEnabled() bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.fallbackEnabled
}

// GetCurrentEndpoint returns the current active endpoint for the given interface type
func (p *ProxyServer) GetCurrentEndpoint(interfaceType string) *Endpoint {
	return p.router.GetActiveEndpoint(InterfaceType(interfaceType))
}

// SetCurrentEndpoint sets the current active endpoint for the given interface type
func (p *ProxyServer) SetCurrentEndpoint(interfaceType, endpointName string) error {
	eps := p.router.GetEndpointsByType(InterfaceType(interfaceType))
	for _, ep := range eps {
		if ep.Name == endpointName {
			return p.router.SetActiveEndpoint(InterfaceType(interfaceType), ep)
		}
	}
	return ErrEndpointNotFound
}

// GetStats returns the statistics manager
func (p *ProxyServer) GetStats() *StatsManager {
	return p.stats
}

// handleHealth handles health check requests
func (p *ProxyServer) handleHealth(w http.ResponseWriter, r *http.Request) {
	response := map[string]interface{}{
		"status": "healthy",
		"port":   p.port,
	}
	writeJSON(w, http.StatusOK, response)
}

// handleStats handles statistics requests
func (p *ProxyServer) handleStats(w http.ResponseWriter, r *http.Request) {
	stats := map[string]interface{}{
		"recent_logs": p.stats.GetRecentLogs(10),
		"token_stats": p.stats.GetTokenStats(),
	}
	writeJSON(w, http.StatusOK, stats)
}

// SetConfigPath sets the config path for locating kiro.json
func (p *ProxyServer) SetConfigPath(path string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.configPath = path
}

func (p *ProxyServer) getConfigPath() string {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.configPath
}

// proxyRouteRegistrar adapts ProxyServer into a plugin.RouteRegistrar.
type proxyRouteRegistrar struct {
	mux *http.ServeMux
	p   *ProxyServer
}

func (r *proxyRouteRegistrar) HandleFunc(pattern string, handler http.HandlerFunc) {
	r.mux.HandleFunc(pattern, handler)
}

func (r *proxyRouteRegistrar) RequireAuth(handler http.HandlerFunc) http.HandlerFunc {
	return r.p.requireAuth(handler)
}

func (r *proxyRouteRegistrar) RequireAuthStrict(handler http.HandlerFunc) http.HandlerFunc {
	return r.p.requireAuthStrict(handler)
}

// SetReloadFunc sets the config reload callback function
func (p *ProxyServer) SetReloadFunc(fn func()) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.reloadFunc = fn
}

// handleReload handles config reload requests
func (p *ProxyServer) handleReload(w http.ResponseWriter, r *http.Request) {
	// 支持 GET 和 POST 方法
	if r.Method != http.MethodGet && r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	p.mu.RLock()
	reloadFunc := p.reloadFunc
	p.mu.RUnlock()

	if reloadFunc == nil {
		writeJSON(w, http.StatusInternalServerError, map[string]interface{}{
			"error": "reload function not configured",
		})
		return
	}

	// 执行重载
	reloadFunc()

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"message": "config reloaded successfully",
	})
}

// handleEndpoint handles endpoint creation/update requests
func (p *ProxyServer) handleEndpoint(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if p.store == nil {
		writeJSON(w, http.StatusInternalServerError, map[string]interface{}{
			"error": "storage not initialized",
		})
		return
	}

	// 解析请求体
	var req struct {
		Name          string `json:"name"`
		APIURL        string `json:"apiUrl"`
		APIKey        string `json:"apiKey"`
		Active        *bool  `json:"active,omitempty"`
		Enabled       *bool  `json:"enabled,omitempty"`
		InterfaceType string `json:"interfaceType"`
		ProviderName  string `json:"providerName,omitempty"`
		Priority      int    `json:"priority,omitempty"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]interface{}{
			"error": "invalid request body: " + err.Error(),
		})
		return
	}

	// 验证必填字段
	if strings.TrimSpace(req.Name) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]interface{}{
			"error": "name is required",
		})
		return
	}
	if strings.TrimSpace(req.APIURL) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]interface{}{
			"error": "apiUrl is required",
		})
		return
	}
	if strings.TrimSpace(req.APIKey) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]interface{}{
			"error": "apiKey is required",
		})
		return
	}
	if strings.TrimSpace(req.InterfaceType) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]interface{}{
			"error": "interfaceType is required",
		})
		return
	}

	// 设置默认值
	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}

	active := false
	if req.Active != nil {
		active = *req.Active
	}

	priority := req.Priority
	if priority == 0 {
		priority = 5
	}

	// 查找是否存在相同的 endpoint（根据 apiUrl, apiKey, interfaceType）
	endpoints, err := p.store.GetEndpoints()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]interface{}{
			"error": "failed to get endpoints: " + err.Error(),
		})
		return
	}

	var existingEndpoint *storage.Endpoint
	for _, ep := range endpoints {
		if ep.APIURL == req.APIURL &&
			ep.APIKey == req.APIKey &&
			ep.InterfaceType == req.InterfaceType {
			existingEndpoint = ep
			break
		}
	}

	// 处理 providerName：如果提供了 providerName，检查 vendor 是否存在
	if strings.TrimSpace(req.ProviderName) != "" {
		vendors, err := p.store.GetVendors()
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]interface{}{
				"error": "failed to get vendors: " + err.Error(),
			})
			return
		}

		vendorExists := false
		for _, v := range vendors {
			if v.Name == req.ProviderName {
				vendorExists = true
				break
			}
		}

		// 如果 vendor 不存在，创建新的 vendor
		if !vendorExists {
			// 从 apiUrl 中提取 host 作为 homeUrl
			homeURL := req.APIURL
			if idx := strings.Index(req.APIURL, "://"); idx > 0 {
				remaining := req.APIURL[idx+3:]
				if slashIdx := strings.Index(remaining, "/"); slashIdx > 0 {
					homeURL = req.APIURL[:idx+3+slashIdx]
				}
			}

			newVendor := &storage.Vendor{
				Name:    req.ProviderName,
				HomeURL: homeURL,
				APIURL:  req.APIURL,
			}
			if err := p.store.SaveVendor(newVendor); err != nil {
				writeJSON(w, http.StatusInternalServerError, map[string]interface{}{
					"error": "failed to create vendor: " + err.Error(),
				})
				return
			}
		}
	}

	// 处理 active 状态
	if active {
		// 如果设置为 active，需要将同一 interfaceType 的其他 endpoint 设为 inactive
		for _, ep := range endpoints {
			if ep.InterfaceType == req.InterfaceType && ep.Active {
				if existingEndpoint == nil || ep.ID != existingEndpoint.ID {
					ep.Active = false
					if err := p.store.UpdateEndpoint(ep); err != nil {
						writeJSON(w, http.StatusInternalServerError, map[string]interface{}{
							"error": "failed to update endpoint active status: " + err.Error(),
						})
						return
					}
				}
			}
		}
	} else {
		// 如果 active 未设置或为 false，检查是否只有一个 endpoint
		sameTypeCount := 0
		for _, ep := range endpoints {
			if ep.InterfaceType == req.InterfaceType {
				if existingEndpoint == nil || ep.ID != existingEndpoint.ID {
					sameTypeCount++
				}
			}
		}
		// 如果只有一个 endpoint（即将创建或更新后只有一个），自动设为 active
		if sameTypeCount == 0 {
			active = true
		}
	}

	// 创建或更新 endpoint
	if existingEndpoint != nil {
		// 更新现有 endpoint
		existingEndpoint.Name = req.Name
		existingEndpoint.APIURL = req.APIURL
		existingEndpoint.APIKey = req.APIKey
		existingEndpoint.Active = active
		existingEndpoint.Enabled = enabled
		existingEndpoint.InterfaceType = req.InterfaceType
		existingEndpoint.ProviderName = req.ProviderName
		existingEndpoint.Priority = priority

		if err := p.store.UpdateEndpoint(existingEndpoint); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]interface{}{
				"error": "failed to update endpoint: " + err.Error(),
			})
			return
		}

		writeJSON(w, http.StatusOK, map[string]interface{}{
			"message":  "endpoint updated successfully",
			"endpoint": existingEndpoint,
		})
	} else {
		// 创建新 endpoint
		newEndpoint := &storage.Endpoint{
			Name:          req.Name,
			APIURL:        req.APIURL,
			APIKey:        req.APIKey,
			Active:        active,
			Enabled:       enabled,
			InterfaceType: req.InterfaceType,
			ProviderName:  req.ProviderName,
			Priority:      priority,
		}

		if err := p.store.SaveEndpoint(newEndpoint); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]interface{}{
				"error": "failed to create endpoint: " + err.Error(),
			})
			return
		}

		writeJSON(w, http.StatusOK, map[string]interface{}{
			"message":  "endpoint created successfully",
			"endpoint": newEndpoint,
		})
	}

	// 触发配置重载
	p.mu.RLock()
	reloadFunc := p.reloadFunc
	p.mu.RUnlock()

	if reloadFunc != nil {
		reloadFunc()
	}
}

// handleSyncConfig receives a full config JSON and replaces the local config.
func (p *ProxyServer) handleSyncConfig(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]interface{}{
			"error": "method not allowed",
		})
		return
	}

	if p.store == nil {
		writeJSON(w, http.StatusInternalServerError, map[string]interface{}{
			"error": "storage not initialized",
		})
		return
	}

	const maxSyncConfigBytes = 128 << 20 // 128 MiB (supports large multi-account sync payloads)
	r.Body = http.MaxBytesReader(w, r.Body, maxSyncConfigBytes)

	// 扩展 payload：vendors + endpoints + plugin configs (via json.RawMessage)
	type syncConfigRequest struct {
		config.AppConfig
		KiroConfigEncoded  string          `json:"kiroConfigEncoded,omitempty"`
		KiroConfig         json.RawMessage `json:"kiroConfig,omitempty"`
		XRayConfigEncoded  string          `json:"xrayConfigEncoded,omitempty"`
		XRayConfig         json.RawMessage `json:"xrayConfig,omitempty"`
		CodexConfigEncoded string          `json:"codexConfigEncoded,omitempty"`
		CodexConfig        json.RawMessage `json:"codexConfig,omitempty"`
	}

	var req syncConfigRequest
	dec := json.NewDecoder(r.Body)
	if err := dec.Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]interface{}{
			"error": "invalid request body: " + err.Error(),
		})
		return
	}
	if err := dec.Decode(&struct{}{}); err != io.EOF {
		writeJSON(w, http.StatusBadRequest, map[string]interface{}{
			"error": "invalid request body: multiple JSON values",
		})
		return
	}

	incoming := req.AppConfig

	// Validate endpoints
	var validationErrs []string
	for i := range incoming.Endpoints {
		if errs := config.ValidateEndpoint(&incoming.Endpoints[i]); len(errs) > 0 {
			for _, e := range errs {
				validationErrs = append(validationErrs, fmt.Sprintf("endpoints[%d]: %s", i, e.Error()))
				if len(validationErrs) >= 20 {
					break
				}
			}
		}
		if len(validationErrs) >= 20 {
			break
		}
	}
	if len(validationErrs) > 0 {
		writeJSON(w, http.StatusBadRequest, map[string]interface{}{
			"error":   "config validation failed",
			"details": validationErrs,
		})
		return
	}

	fileStore, ok := p.store.(*storage.ConfigFileStore)
	if !ok {
		writeJSON(w, http.StatusInternalServerError, map[string]interface{}{
			"error": "storage type does not support full config replace",
		})
		return
	}

	// Sync plugin configs via name-based dispatch.
	type pluginPayload struct {
		encoded string
		plain   json.RawMessage
	}
	type pluginImportPlan struct {
		name     string
		importer plugin.ConfigSyncImporter
		data     json.RawMessage
		snapshot json.RawMessage
	}

	pluginData := map[string]pluginPayload{
		"kiro":           {req.KiroConfigEncoded, req.KiroConfig},
		"xray":           {req.XRayConfigEncoded, req.XRayConfig},
		"codex-accounts": {req.CodexConfigEncoded, req.CodexConfig},
	}

	configPath := strings.TrimSpace(p.getConfigPath())
	if configPath == "" {
		writeJSON(w, http.StatusInternalServerError, map[string]interface{}{
			"error": "config path not configured",
		})
		return
	}

	// Preflight plugin payload decode + plugin state snapshot for rollback.
	pluginPlans := make([]pluginImportPlan, 0, len(pluginData))
	pluginResults := map[string]interface{}{}
	for _, pl := range plugin.All() {
		importer, ok := pl.(plugin.ConfigSyncImporter)
		if !ok {
			continue
		}
		pd, exists := pluginData[pl.Name()]
		if !exists {
			continue
		}

		data, hasData, err := decodeSyncPluginPayload(pl, pd.encoded, pd.plain)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]interface{}{
				"error": fmt.Sprintf("invalid %s sync payload: %v", pl.Name(), err),
			})
			return
		}
		if !hasData {
			continue
		}

		exporter, ok := pl.(plugin.ConfigSyncExporter)
		if !ok {
			writeJSON(w, http.StatusInternalServerError, map[string]interface{}{
				"error": fmt.Sprintf("plugin %s does not support sync rollback snapshot", pl.Name()),
			})
			return
		}
		_, snapshot, err := exporter.SyncExport(configPath)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]interface{}{
				"error": fmt.Sprintf("failed to snapshot plugin %s before sync: %v", pl.Name(), err),
			})
			return
		}

		pluginPlans = append(pluginPlans, pluginImportPlan{
			name:     pl.Name(),
			importer: importer,
			data:     data,
			snapshot: snapshot,
		})
	}

	oldInfo, err := os.Stat(configPath)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]interface{}{
			"error": "failed to stat current config before sync: " + err.Error(),
		})
		return
	}
	oldMode := oldInfo.Mode().Perm()
	oldConfigRaw, err := os.ReadFile(configPath)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]interface{}{
			"error": "failed to backup current config before sync: " + err.Error(),
		})
		return
	}

	rollback := func(syncErr error) {
		rollbackErrs := make([]string, 0, len(pluginPlans)+1)

		if writeErr := os.WriteFile(configPath, oldConfigRaw, oldMode); writeErr != nil {
			rollbackErrs = append(rollbackErrs, "restore config.json failed: "+writeErr.Error())
		}

		for i := len(pluginPlans) - 1; i >= 0; i-- {
			plan := pluginPlans[i]
			if len(plan.snapshot) == 0 {
				continue
			}
			if err := plan.importer.SyncImport(configPath, plan.snapshot); err != nil {
				rollbackErrs = append(rollbackErrs, fmt.Sprintf("restore plugin %s failed: %v", plan.name, err))
			}
		}

		p.mu.RLock()
		reloadFunc := p.reloadFunc
		p.mu.RUnlock()
		if reloadFunc != nil {
			reloadFunc()
		}

		resp := map[string]interface{}{
			"error":   "sync failed and rollback attempted: " + syncErr.Error(),
			"plugins": pluginResults,
		}
		if kr, ok := pluginResults["kiro"]; ok {
			resp["kiro"] = kr
		}
		if cr, ok := pluginResults["codex-accounts"]; ok {
			resp["codex"] = cr
		}
		if len(rollbackErrs) > 0 {
			resp["rollbackWarnings"] = rollbackErrs
		}
		writeJSON(w, http.StatusInternalServerError, resp)
	}

	if err := fileStore.ReplaceFullConfig(&incoming); err != nil {
		rollback(fmt.Errorf("failed to replace config: %w", err))
		return
	}

	for _, plan := range pluginPlans {
		if err := plan.importer.SyncImport(configPath, plan.data); err != nil {
			pluginResults[plan.name] = map[string]interface{}{"synced": false, "warning": err.Error()}
			rollback(fmt.Errorf("failed to import plugin %s: %w", plan.name, err))
			return
		}
		pluginResults[plan.name] = map[string]interface{}{"synced": true}
	}

	// Trigger hot reload after successful full sync.
	p.mu.RLock()
	reloadFunc := p.reloadFunc
	p.mu.RUnlock()

	if reloadFunc != nil {
		reloadFunc()
	}

	resp := map[string]interface{}{
		"message": "config synced and reloaded successfully",
	}
	// Backward compatibility: expose kiro result at top level
	if kr, ok := pluginResults["kiro"]; ok {
		resp["kiro"] = kr
	}
	if cr, ok := pluginResults["codex-accounts"]; ok {
		resp["codex"] = cr
	}
	if len(pluginResults) > 0 {
		resp["plugins"] = pluginResults
	}
	writeJSON(w, http.StatusOK, resp)
}

func decodeSyncPluginPayload(pl plugin.Plugin, encoded string, plain json.RawMessage) (json.RawMessage, bool, error) {
	if strings.TrimSpace(encoded) != "" {
		decoder, ok := pl.(plugin.ConfigSyncDecoder)
		if !ok {
			return nil, false, fmt.Errorf("encoded payload provided but decoder is unavailable")
		}
		decoded, err := decoder.SyncDecode(encoded)
		if err != nil {
			return nil, false, err
		}
		if len(decoded) == 0 {
			return nil, false, nil
		}
		return decoded, true, nil
	}
	if len(plain) > 0 {
		return plain, true, nil
	}
	return nil, false, nil
}
