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
	port        int
	router      Router
	server      *http.Server
	stats       *StatsManager
	wsHub       *WSHub
	mu          sync.RWMutex
	authKey     string
	store       storage.Storage
	vendorStats statsdb.VendorStatsStore

	fallbackEnabled bool
	exec            *proxyExecutor
}

// NewProxyServer creates a new ProxyServer instance
func NewProxyServer(port int, router Router) *ProxyServer {
	return &ProxyServer{
		port:   port,
		router: router,
		stats:  NewStatsManager(),
	}
}

// NewProxyServerWithWSHub creates a new ProxyServer with WebSocket hub integration
// Requirements: 7.1, 8.5
func NewProxyServerWithWSHub(port int, router Router, wsHub *WSHub) *ProxyServer {
	stats := NewStatsManager()
	stats.SetWSHub(wsHub)

	return &ProxyServer{
		port:   port,
		router: router,
		stats:  stats,
		wsHub:  wsHub,
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

func (p *ProxyServer) SetVendorStatsStore(store statsdb.VendorStatsStore) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.vendorStats = store
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
	mux := http.NewServeMux()

	mux.HandleFunc("/", p.handleProxy)
	mux.HandleFunc("/health", p.handleHealth)
	mux.HandleFunc("/stats", p.handleStats)

	if p.wsHub != nil {
		mux.HandleFunc("/ws", p.wsHub.HandleWebSocket)
	}

	p.server = &http.Server{
		Addr:         fmt.Sprintf(":%d", p.port),
		Handler:      mux,
		ReadTimeout:  300 * time.Second,
		WriteTimeout: 300 * time.Second,
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
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)

	response := map[string]interface{}{
		"status": "healthy",
		"port":   p.port,
	}

	_ = json.NewEncoder(w).Encode(response)
}

// handleStats handles statistics requests
func (p *ProxyServer) handleStats(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	stats := map[string]interface{}{
		"recent_logs": p.stats.GetRecentLogs(5),
		"token_stats": p.stats.GetTokenStats(),
	}

	_ = json.NewEncoder(w).Encode(stats)
}
