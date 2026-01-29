// Package proxy implements the HTTP proxy server for AI API requests.
package proxy

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

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
	wsHub      *WSHub
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

// NewProxyServerWithWSHub creates a new ProxyServer with WebSocket hub integration
// Requirements: 7.1, 8.5
func NewProxyServerWithWSHub(port int, router Router, wsHub *WSHub) *ProxyServer {
	stats := NewStatsManager()
	stats.SetWSHub(wsHub)

	return &ProxyServer{
		port:       port,
		listenAddr: "0.0.0.0",
		router:     router,
		stats:      stats,
		wsHub:      wsHub,
	}
}

// SetWSHub sets the WebSocket hub for real-time updates
// Requirements: 7.1, 8.5
func (p *ProxyServer) SetWSHub(hub *WSHub) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.wsHub = hub
	if p.stats != nil {
		p.stats.SetWSHub(hub)
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

// GetWSHub returns the WebSocket hub
// Requirements: 7.1, 8.5
func (p *ProxyServer) GetWSHub() *WSHub {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.wsHub
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
	mux.HandleFunc("/kiro/config", p.requireAuth(p.handleKiroConfig))
	mux.HandleFunc("/kiro/getUsage", p.requireAuth(p.handleKiroGetUsage))
	mux.HandleFunc("/reload", p.requireAuth(p.handleReload))
	mux.HandleFunc("/endpoint", p.requireAuth(p.handleEndpoint))

	if p.wsHub != nil {
		mux.HandleFunc("/ws", p.wsHub.HandleWebSocket)
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

// requireAuth 是一个中间件，用于验证 API Key
// 如果配置了 apiKey，则要求请求头中包含 Authorization: Bearer <apiKey>
func (p *ProxyServer) requireAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		apiKey := p.getAuthKey()

		// 如果没有配置 apiKey，直接放行
		if apiKey == "" {
			next(w, r)
			return
		}

		// 检查 Authorization 头
		authHeader := r.Header.Get("Authorization")
		if authHeader == "" {
			writeJSON(w, http.StatusUnauthorized, map[string]interface{}{
				"error": "Missing Authorization header",
			})
			return
		}

		// 验证 Bearer token
		const prefix = "Bearer "
		if !strings.HasPrefix(authHeader, prefix) {
			writeJSON(w, http.StatusUnauthorized, map[string]interface{}{
				"error": "Invalid Authorization header format, expected: Bearer <token>",
			})
			return
		}

		token := strings.TrimSpace(authHeader[len(prefix):])
		if token != apiKey {
			writeJSON(w, http.StatusUnauthorized, map[string]interface{}{
				"error": "Invalid API key",
			})
			return
		}

		// 验证通过，继续处理请求
		next(w, r)
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
		"recent_logs": p.stats.GetRecentLogs(5),
		"token_stats": p.stats.GetTokenStats(),
	}
	writeJSON(w, http.StatusOK, stats)
}

// SetConfigPath sets the config path for kiro-auth-token.json location
func (p *ProxyServer) SetConfigPath(path string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.configPath = path
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
