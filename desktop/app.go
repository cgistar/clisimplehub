package main

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"clisimplehub/internal/config"
	"clisimplehub/internal/proxy"
	"clisimplehub/internal/statsdb"
	"clisimplehub/internal/storage"
	"clisimplehub/internal/transformer"

	"github.com/google/uuid"
)

// Settings represents the application settings exposed to frontend
type Settings struct {
	Port     int    `json:"port"`
	APIKey   string `json:"apiKey"`
	Fallback bool   `json:"fallback"`
}

// EndpointInfo represents endpoint information for frontend display
// Requirements: 6.1, 6.2, 6.3, 6.4
type EndpointInfo struct {
	ID            int64                  `json:"id"`
	Name          string                 `json:"name"`
	APIURL        string                 `json:"apiUrl"`
	APIKey        string                 `json:"apiKey,omitempty"`
	Active        bool                   `json:"active"`
	Enabled       bool                   `json:"enabled"`
	InterfaceType string                 `json:"interfaceType"`
	VendorID      int64                  `json:"vendorId"`
	VendorName    string                 `json:"vendorName,omitempty"`
	Model         string                 `json:"model,omitempty"`
	Transformer   string                 `json:"transformer,omitempty"`
	ProxyURL      string                 `json:"proxyUrl,omitempty"`
	Models        []storage.ModelMapping `json:"models,omitempty"`
	Remark        string                 `json:"remark,omitempty"`
	Priority      int                    `json:"priority"`
	// Daily stats
	TodayRequests int64 `json:"todayRequests"`
	TodayErrors   int64 `json:"todayErrors"`
	TodayInput    int64 `json:"todayInput"`
	TodayOutput   int64 `json:"todayOutput"`
}

// App struct represents the Wails application controller
// Requirements: 1.1, 6.1
type App struct {
	ctx          context.Context
	storage      storage.Storage
	proxyServer  *proxy.ProxyServer
	router       *proxy.DefaultRouter
	wsHub        *proxy.WSHub
	configLoader *config.ConfigLoader
	vendorStats  *statsdb.SQLiteVendorStatsStore
}

// NewApp creates a new App application struct
func NewApp() *App {
	return &App{}
}

// startup is called when the app starts. The context is saved
// so we can call the runtime methods
func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
}

// SetStorage sets the storage instance for the app
func (a *App) SetStorage(s storage.Storage) {
	a.storage = s
}

// SetProxyServer sets the proxy server instance for the app
func (a *App) SetProxyServer(p *proxy.ProxyServer) {
	a.proxyServer = p
}

// SetRouter sets the router instance for the app
func (a *App) SetRouter(r *proxy.DefaultRouter) {
	a.router = r
}

// SetWSHub sets the WebSocket hub instance for the app
func (a *App) SetWSHub(hub *proxy.WSHub) {
	a.wsHub = hub
}

// SetVendorStats sets the vendor stats store instance for the app
func (a *App) SetVendorStats(store *statsdb.SQLiteVendorStatsStore) {
	a.vendorStats = store
}

// SetConfigLoader sets the config loader instance for the app
func (a *App) SetConfigLoader(loader *config.ConfigLoader) {
	a.configLoader = loader
}

// =============================================================================
// Settings Management Methods
// Requirements: 1.1, 1.2, 1.3, 1.4
// =============================================================================

// GetSettings retrieves the current application settings
func (a *App) GetSettings() (*Settings, error) {
	if a.storage == nil {
		return nil, fmt.Errorf("storage not initialized")
	}

	settings := &Settings{
		Port:     5600, // Default port
		APIKey:   "",
		Fallback: false, // Default fallback disabled
	}

	// Get port from storage
	portStr, err := a.storage.GetConfig(ConfigKeyPort)
	if err == nil && portStr != "" {
		if port, parseErr := strconv.Atoi(portStr); parseErr == nil {
			settings.Port = port
		}
	}

	// Get proxy auth token from storage
	apiKey, err := a.storage.GetConfig(ConfigKeyAPIKey)
	if err == nil {
		settings.APIKey = apiKey
	}

	// Get fallback setting from storage
	fallbackStr, err := a.storage.GetConfig(ConfigKeyFallback)
	if err == nil && fallbackStr == "true" {
		settings.Fallback = true
	}

	return settings, nil
}

// SaveSettings saves the application settings
func (a *App) SaveSettings(settings *Settings) error {
	if a.storage == nil {
		return fmt.Errorf("storage not initialized")
	}

	// Validate port
	if err := config.ValidatePort(settings.Port); err != nil {
		return fmt.Errorf("invalid port: %w", err)
	}

	// Save port to storage
	if err := a.storage.SetConfig(ConfigKeyPort, strconv.Itoa(settings.Port)); err != nil {
		return fmt.Errorf("failed to save port: %w", err)
	}

	// Save proxy auth token to storage (empty => no auth)
	if err := a.storage.SetConfig(ConfigKeyAPIKey, settings.APIKey); err != nil {
		return fmt.Errorf("failed to save api key: %w", err)
	}

	// Save fallback setting to storage as bool
	if err := a.storage.SetConfigBool(ConfigKeyFallback, settings.Fallback); err != nil {
		return fmt.Errorf("failed to save fallback setting: %w", err)
	}

	// Update proxy server port if available
	if a.proxyServer != nil {
		a.proxyServer.SetPort(settings.Port)
		a.proxyServer.SetAuthKey(settings.APIKey)
		a.proxyServer.SetFallbackEnabled(settings.Fallback)
	}

	return nil
}

// GetPort returns the current proxy port
// Requirements: 1.1
func (a *App) GetPort() (int, error) {
	settings, err := a.GetSettings()
	if err != nil {
		return 0, err
	}
	return settings.Port, nil
}

// SetPort sets the proxy port
// Requirements: 1.2, 1.3, 1.4
func (a *App) SetPort(port int) error {
	// Validate port
	// Requirements: 1.2
	if err := config.ValidatePort(port); err != nil {
		return fmt.Errorf("invalid port: %w", err)
	}

	if a.storage == nil {
		return fmt.Errorf("storage not initialized")
	}

	// Save to storage
	// Requirements: 1.3
	if err := a.storage.SetConfig(ConfigKeyPort, strconv.Itoa(port)); err != nil {
		return fmt.Errorf("failed to save port: %w", err)
	}

	// Update proxy server
	if a.proxyServer != nil {
		a.proxyServer.SetPort(port)
	}

	return nil
}

// GetConfigPath returns the current config file path
func (a *App) GetConfigPath() string {
	if a.configLoader != nil {
		return a.configLoader.GetPath()
	}
	return ""
}

// =============================================================================
// Endpoint Management Methods
// Requirements: 6.1, 6.2, 6.3, 6.4
// =============================================================================

// GetEndpointsByType returns endpoints filtered by interface type
// Requirements: 6.1, 6.2, 6.3
func (a *App) GetEndpointsByType(interfaceType string) ([]*EndpointInfo, error) {
	if a.storage == nil {
		return nil, fmt.Errorf("storage not initialized")
	}

	// Get endpoints from storage
	// Requirements: 6.2
	endpoints, err := a.storage.GetEndpointsByType(interfaceType)
	if err != nil {
		return nil, fmt.Errorf("failed to get endpoints: %w", err)
	}

	// Get vendors to build vendor name map
	vendors, _ := a.storage.GetVendors()
	vendorMap := make(map[int64]string)
	for _, v := range vendors {
		vendorMap[v.ID] = v.Name
	}

	// Get today's stats for all endpoints
	var todayStats map[string]*statsdb.EndpointDailyStats
	if a.vendorStats != nil {
		todayStats, _ = a.vendorStats.GetTodayStatsByEndpoints(a.ctx)
	}

	// Get active endpoint from router to mark it
	var activeEndpointID int64
	runtimeEnabledByID := make(map[int64]bool)
	if a.router != nil {
		activeEp := a.router.GetActiveEndpoint(proxy.InterfaceType(interfaceType))
		if activeEp != nil {
			activeEndpointID = activeEp.ID
		}
		for _, ep := range a.router.GetEndpointsByType(proxy.InterfaceType(interfaceType)) {
			if ep == nil || ep.ID == 0 {
				continue
			}
			runtimeEnabledByID[ep.ID] = ep.Enabled
		}
	}

	// Convert to EndpointInfo and sort by sort_order
	// Requirements: 6.3
	result := make([]*EndpointInfo, 0, len(endpoints))
	for _, ep := range endpoints {
		enabled := ep.Enabled
		if runtimeEnabled, ok := runtimeEnabledByID[ep.ID]; ok {
			enabled = runtimeEnabled
		}
		info := &EndpointInfo{
			ID:            ep.ID,
			Name:          ep.Name,
			APIURL:        ep.APIURL,
			Active:        activeEndpointID != 0 && ep.ID == activeEndpointID,
			Enabled:       enabled,
			InterfaceType: ep.InterfaceType,
			VendorID:      ep.VendorID,
			VendorName:    vendorMap[ep.VendorID],
			Model:         ep.Model,
			Remark:        ep.Remark,
			Priority:      ep.Priority,
		}
		// Fill today's stats
		if todayStats != nil {
			epIDStr := fmt.Sprintf("%d", ep.ID)
			if stats, ok := todayStats[epIDStr]; ok {
				info.TodayRequests = stats.RequestCount
				info.TodayErrors = stats.ErrorCount
				info.TodayInput = stats.InputTokens
				info.TodayOutput = stats.OutputTokens
			}
		}
		result = append(result, info)
	}

	// Sort by priority (ascending), then by name
	// Requirements: 6.3
	sort.Slice(result, func(i, j int) bool {
		if result[i].Priority != result[j].Priority {
			return result[i].Priority < result[j].Priority
		}
		return result[i].Name < result[j].Name
	})

	return result, nil
}

// GetActiveEndpoint returns the currently active endpoint for the given interface type
// Requirements: 6.1
func (a *App) GetActiveEndpoint(interfaceType string) (*EndpointInfo, error) {
	if a.router == nil {
		return nil, fmt.Errorf("router not initialized")
	}

	ep := a.router.GetActiveEndpoint(proxy.InterfaceType(interfaceType))
	if ep == nil {
		return nil, nil
	}

	return &EndpointInfo{
		ID:            ep.ID,
		Name:          ep.Name,
		APIURL:        ep.APIURL,
		Active:        true,
		Enabled:       ep.Enabled,
		InterfaceType: ep.InterfaceType,
		VendorID:      ep.VendorID,
		Model:         ep.Model,
		Remark:        ep.Remark,
		Priority:      ep.Priority,
	}, nil
}

// SetActiveEndpoint sets the active endpoint for the given interface type
// Requirements: 6.1, 6.4
func (a *App) SetActiveEndpoint(interfaceType string, endpointID int64) error {
	if a.router == nil {
		return fmt.Errorf("router not initialized")
	}
	if a.storage == nil {
		return fmt.Errorf("storage not initialized")
	}

	// Get all endpoints for this type
	endpoints := a.router.GetEndpointsByType(proxy.InterfaceType(interfaceType))

	// Find the endpoint by ID
	var targetEndpoint *proxy.Endpoint
	for _, ep := range endpoints {
		if ep.ID == endpointID {
			targetEndpoint = ep
			break
		}
	}

	if targetEndpoint == nil {
		return fmt.Errorf("endpoint not found: %d", endpointID)
	}

	// Only enabled endpoints can be set as active
	// Requirements: 6.4
	if !targetEndpoint.Enabled {
		return fmt.Errorf("cannot set disabled endpoint as active: %d", endpointID)
	}

	// 持久化：清除同类型其他端点的 Active，设置目标端点为 Active
	storageEndpoints, err := a.storage.GetEndpointsByType(interfaceType)
	if err != nil {
		return fmt.Errorf("failed to get endpoints: %w", err)
	}
	for _, ep := range storageEndpoints {
		if ep.Active && ep.ID != endpointID {
			ep.Active = false
			_ = a.storage.UpdateEndpoint(ep)
		} else if ep.ID == endpointID && !ep.Active {
			ep.Active = true
			_ = a.storage.UpdateEndpoint(ep)
		}
	}

	// Set the active endpoint in router
	return a.router.SetActiveEndpoint(proxy.InterfaceType(interfaceType), targetEndpoint)
}

// ToggleEndpointEnabled toggles the enabled status of an endpoint
// Active endpoints cannot be disabled
func (a *App) ToggleEndpointEnabled(endpointID int64, enabled bool) error {
	if a.storage == nil {
		return fmt.Errorf("storage not initialized")
	}

	// Get the endpoint
	ep, err := a.storage.GetEndpointByID(endpointID)
	if err != nil {
		return fmt.Errorf("failed to get endpoint: %w", err)
	}
	if ep == nil {
		return fmt.Errorf("endpoint not found")
	}

	// Check if trying to disable an active endpoint
	if !enabled && a.router != nil {
		activeEp := a.router.GetActiveEndpoint(proxy.InterfaceType(ep.InterfaceType))
		if activeEp != nil && activeEp.ID == endpointID {
			return fmt.Errorf("cannot disable active endpoint")
		}
	}

	// Update enabled status
	ep.Enabled = enabled
	if err := a.storage.UpdateEndpoint(ep); err != nil {
		return fmt.Errorf("failed to update endpoint: %w", err)
	}

	// Reload endpoints into router
	if a.router != nil {
		endpoints, err := a.storage.GetEndpoints()
		if err == nil {
			a.router.LoadEndpoints(convertEndpoints(endpoints))
		}
	}

	return nil
}

// GetAllEndpoints returns all endpoints from storage
// Requirements: 6.1
func (a *App) GetAllEndpoints() ([]*EndpointInfo, error) {
	if a.storage == nil {
		return nil, fmt.Errorf("storage not initialized")
	}

	endpoints, err := a.storage.GetEndpoints()
	if err != nil {
		return nil, fmt.Errorf("failed to get endpoints: %w", err)
	}

	result := make([]*EndpointInfo, 0, len(endpoints))
	for _, ep := range endpoints {
		info := &EndpointInfo{
			ID:            ep.ID,
			Name:          ep.Name,
			APIURL:        ep.APIURL,
			Active:        ep.Active,
			Enabled:       ep.Enabled,
			InterfaceType: ep.InterfaceType,
			VendorID:      ep.VendorID,
			Model:         ep.Model,
			Remark:        ep.Remark,
			Priority:      ep.Priority,
		}
		result = append(result, info)
	}

	// Sort by priority (ascending), then by name
	sort.Slice(result, func(i, j int) bool {
		if result[i].Priority != result[j].Priority {
			return result[i].Priority < result[j].Priority
		}
		return result[i].Name < result[j].Name
	})

	return result, nil
}

// GetInterfaceTypes returns the list of supported interface types
// Requirements: 6.1
func (a *App) GetInterfaceTypes() []string {
	return []string{
		string(proxy.InterfaceTypeClaude),
		string(proxy.InterfaceTypeCodex),
		string(proxy.InterfaceTypeGemini),
		string(proxy.InterfaceTypeChat),
	}
}

// GetTransformers returns all supported transformer specs grouped by source interfaceType
func (a *App) GetTransformers() map[string][]string {
	return transformer.ListAll()
}

// =============================================================================
// Stats Retrieval Methods
// Requirements: 7.2, 8.1, 8.2
// =============================================================================

// RequestLogInfo represents a request log entry for frontend display
// Requirements: 7.2
type RequestLogInfo struct {
	ID            string `json:"id"`
	InterfaceType string `json:"interfaceType"`
	VendorName    string `json:"vendorName"`
	EndpointName  string `json:"endpointName"`
	Path          string `json:"path"`
	RunTime       int64  `json:"runTime"` // milliseconds
	Status        string `json:"status"`
	Timestamp     string `json:"timestamp"`
}

// TokenStatsInfo represents token statistics for frontend display
// Requirements: 8.1, 8.2
type TokenStatsInfo struct {
	EndpointName string `json:"endpointName"`
	VendorName   string `json:"vendorName"`
	InputTokens  int64  `json:"inputTokens"`
	CachedCreate int64  `json:"cachedCreate"`
	CachedRead   int64  `json:"cachedRead"`
	OutputTokens int64  `json:"outputTokens"`
	Reasoning    int64  `json:"reasoning"`
	Total        int64  `json:"total"`
}

// GetRecentLogs returns the most recent request logs
// Requirements: 7.2
func (a *App) GetRecentLogs() ([]*RequestLogInfo, error) {
	if a.proxyServer == nil {
		return []*RequestLogInfo{}, nil
	}

	stats := a.proxyServer.GetStats()
	if stats == nil {
		return []*RequestLogInfo{}, nil
	}

	// Get recent logs (max 5 as per Requirements 7.4)
	logs := stats.GetRecentLogs(5)

	result := make([]*RequestLogInfo, 0, len(logs))
	for _, log := range logs {
		info := &RequestLogInfo{
			ID:            log.ID,
			InterfaceType: log.InterfaceType,
			VendorName:    log.VendorName,
			EndpointName:  log.EndpointName,
			Path:          log.Path,
			RunTime:       log.RunTime,
			Status:        log.Status,
			Timestamp:     log.Timestamp.Format("2006-01-02 15:04:05"),
		}
		result = append(result, info)
	}

	return result, nil
}

// RequestLogDetailInfo represents detailed request log for frontend display
type RequestLogDetailInfo struct {
	ID             string            `json:"id"`
	InterfaceType  string            `json:"interfaceType"`
	VendorName     string            `json:"vendorName"`
	EndpointName   string            `json:"endpointName"`
	Path           string            `json:"path"`
	RunTime        int64             `json:"runTime"`
	Status         string            `json:"status"`
	Timestamp      string            `json:"timestamp"`
	Method         string            `json:"method"`
	StatusCode     int               `json:"statusCode"`
	TargetURL      string            `json:"targetUrl"`
	UpstreamAuth   string            `json:"upstreamAuth"`
	RequestHeaders map[string]string `json:"requestHeaders"`
	RequestStream  string            `json:"requestStream"`
	ResponseStream string            `json:"responseStream"`
}

// GetLogDetail returns detailed information for a specific request log
func (a *App) GetLogDetail(logID string) (*RequestLogDetailInfo, error) {
	if a.proxyServer == nil {
		return nil, fmt.Errorf("proxy server not initialized")
	}

	stats := a.proxyServer.GetStats()
	if stats == nil {
		return nil, fmt.Errorf("stats manager not initialized")
	}

	// Get recent logs and find the one with matching ID
	logs := stats.GetRecentLogs(5)
	for _, log := range logs {
		if log.ID == logID {
			return &RequestLogDetailInfo{
				ID:             log.ID,
				InterfaceType:  log.InterfaceType,
				VendorName:     log.VendorName,
				EndpointName:   log.EndpointName,
				Path:           log.Path,
				RunTime:        log.RunTime,
				Status:         log.Status,
				Timestamp:      log.Timestamp.Format("15:04:05"),
				Method:         log.Method,
				StatusCode:     log.StatusCode,
				TargetURL:      log.TargetURL,
				UpstreamAuth:   log.UpstreamAuth,
				RequestHeaders: log.RequestHeaders,
				RequestStream:  log.RequestStream,
				ResponseStream: log.ResponseStream,
			}, nil
		}
	}

	return nil, fmt.Errorf("log not found: %s", logID)
}

// GetTokenStats returns token usage statistics
// Requirements: 8.1, 8.2
func (a *App) GetTokenStats() ([]*TokenStatsInfo, error) {
	if a.proxyServer == nil {
		return []*TokenStatsInfo{}, nil
	}

	stats := a.proxyServer.GetStats()
	if stats == nil {
		return []*TokenStatsInfo{}, nil
	}

	tokenStats := stats.GetTokenStats()

	result := make([]*TokenStatsInfo, 0, len(tokenStats))
	for _, ts := range tokenStats {
		info := &TokenStatsInfo{
			EndpointName: ts.EndpointName,
			VendorName:   ts.VendorName,
			InputTokens:  ts.InputTokens,
			CachedCreate: ts.CachedCreate,
			CachedRead:   ts.CachedRead,
			OutputTokens: ts.OutputTokens,
			Reasoning:    ts.Reasoning,
			Total:        ts.Total,
		}
		result = append(result, info)
	}

	return result, nil
}

// GetTokenStatsForEndpoint returns token statistics for a specific endpoint
// Requirements: 8.1, 8.2
func (a *App) GetTokenStatsForEndpoint(endpointName string) (*TokenStatsInfo, error) {
	if a.proxyServer == nil {
		return nil, fmt.Errorf("proxy server not initialized")
	}

	stats := a.proxyServer.GetStats()
	if stats == nil {
		return nil, fmt.Errorf("stats manager not initialized")
	}

	ts := stats.GetTokenStatsForEndpoint(endpointName)
	if ts == nil {
		return nil, nil
	}

	return &TokenStatsInfo{
		EndpointName: ts.EndpointName,
		VendorName:   ts.VendorName,
		InputTokens:  ts.InputTokens,
		CachedCreate: ts.CachedCreate,
		CachedRead:   ts.CachedRead,
		OutputTokens: ts.OutputTokens,
		Reasoning:    ts.Reasoning,
		Total:        ts.Total,
	}, nil
}

// =============================================================================
// Proxy Control Methods
// =============================================================================

// StartProxy starts the proxy server
func (a *App) StartProxy() error {
	if a.proxyServer == nil {
		return fmt.Errorf("proxy server not initialized")
	}

	// Start in a goroutine since Start() blocks
	go func() {
		if err := a.proxyServer.Start(); err != nil {
			// Log error - in production, this should be handled properly
			fmt.Printf("Proxy server error: %v\n", err)
		}
	}()

	return nil
}

// StopProxy stops the proxy server
func (a *App) StopProxy() error {
	if a.proxyServer == nil {
		return fmt.Errorf("proxy server not initialized")
	}
	return a.proxyServer.Stop()
}

// GetProxyStatus returns the current proxy status
func (a *App) GetProxyStatus() map[string]interface{} {
	status := map[string]interface{}{
		"running": false,
		"port":    0,
	}

	if a.proxyServer != nil {
		status["port"] = a.proxyServer.GetPort()
		// Note: We'd need to track running state in ProxyServer for accurate status
		status["running"] = true
	}

	return status
}

// ReloadConfig reloads configuration from the config file
func (a *App) ReloadConfig() error {
	if a.storage == nil {
		return fmt.Errorf("storage not initialized")
	}

	// Refresh router temp-disable TTL from config.json appConfig (default 5 minutes).
	if a.router != nil {
		tempDisableMinutes := 5
		if v, err := a.storage.GetConfig(ConfigKeyTempDisableMinutes); err == nil && v != "" {
			if minutes, err := strconv.Atoi(v); err == nil && minutes > 0 {
				tempDisableMinutes = minutes
			}
		}
		a.router.SetTempDisableTTL(time.Duration(tempDisableMinutes) * time.Minute)
	}

	if a.router != nil {
		endpoints, err := a.storage.GetEndpoints()
		if err != nil {
			return fmt.Errorf("failed to get endpoints: %w", err)
		}

		a.router.LoadEndpoints(convertEndpoints(endpoints))
	}

	// Also refresh runtime proxy settings from config.json.
	if a.proxyServer != nil {
		settings, err := a.GetSettings()
		if err != nil {
			return err
		}
		a.proxyServer.SetPort(settings.Port)
		a.proxyServer.SetAuthKey(settings.APIKey)
		a.proxyServer.SetFallbackEnabled(settings.Fallback)
	}

	return nil
}

// =============================================================================
// Vendor Management Methods
// =============================================================================

// VendorInfo represents vendor information for frontend display
type VendorInfo struct {
	ID      int64  `json:"id"`
	Name    string `json:"name"`
	HomeURL string `json:"homeUrl"`
	APIURL  string `json:"apiUrl"`
	Remark  string `json:"remark,omitempty"`
}

// GetVendors returns all vendors
func (a *App) GetVendors() ([]*VendorInfo, error) {
	if a.storage == nil {
		return nil, fmt.Errorf("storage not initialized")
	}
	vendors, err := a.storage.GetVendors()
	if err != nil {
		return nil, err
	}
	result := make([]*VendorInfo, 0, len(vendors))
	for _, v := range vendors {
		result = append(result, &VendorInfo{
			ID:      v.ID,
			Name:    v.Name,
			HomeURL: v.HomeURL,
			APIURL:  v.APIURL,
			Remark:  v.Remark,
		})
	}
	return result, nil
}

// SaveVendor creates or updates a vendor
func (a *App) SaveVendor(vendor *VendorInfo) (*VendorInfo, error) {
	if a.storage == nil {
		return nil, fmt.Errorf("storage not initialized")
	}
	v := &storage.Vendor{
		ID:      vendor.ID,
		Name:    vendor.Name,
		HomeURL: vendor.HomeURL,
		APIURL:  vendor.APIURL,
		Remark:  vendor.Remark,
	}
	if err := a.storage.SaveVendor(v); err != nil {
		return nil, err
	}
	vendor.ID = v.ID

	return vendor, nil
}

// DeleteVendor deletes a vendor by ID
func (a *App) DeleteVendor(id int64) error {
	if a.storage == nil {
		return fmt.Errorf("storage not initialized")
	}
	return a.storage.DeleteVendor(id)
}

// GetEndpointsByVendorID returns endpoints for a specific vendor
func (a *App) GetEndpointsByVendorID(vendorID int64) ([]*EndpointInfo, error) {
	if a.storage == nil {
		return nil, fmt.Errorf("storage not initialized")
	}
	endpoints, err := a.storage.GetEndpointsByVendorID(vendorID)
	if err != nil {
		return nil, err
	}
	result := make([]*EndpointInfo, 0, len(endpoints))
	for _, ep := range endpoints {
		result = append(result, &EndpointInfo{
			ID:            ep.ID,
			Name:          ep.Name,
			APIURL:        ep.APIURL,
			APIKey:        ep.APIKey,
			Active:        ep.Active,
			Enabled:       ep.Enabled,
			InterfaceType: ep.InterfaceType,
			VendorID:      ep.VendorID,
			Model:         ep.Model,
			Transformer:   ep.Transformer,
			ProxyURL:      ep.ProxyURL,
			Models:        ep.Models,
			Remark:        ep.Remark,
			Priority:      ep.Priority,
		})
	}
	return result, nil
}

// EndpointInput represents endpoint input from frontend
type EndpointInput struct {
	ID             int64                  `json:"id"`
	Name           string                 `json:"name"`
	APIURL         string                 `json:"apiUrl"`
	APIKey         string                 `json:"apiKey"`
	Active         bool                   `json:"active"`
	Enabled        bool                   `json:"enabled"`
	InterfaceType  string                 `json:"interfaceType"`
	VendorID       int64                  `json:"vendorId"`
	Model          string                 `json:"model,omitempty"`
	Transformer    string                 `json:"transformer,omitempty"`
	TransformerSet bool                   `json:"transformerSet,omitempty"`
	ProxyURL       string                 `json:"proxyUrl,omitempty"`
	Models         []storage.ModelMapping `json:"models,omitempty"`
	ModelsSet      bool                   `json:"modelsSet,omitempty"`
	Remark         string                 `json:"remark,omitempty"`
	Priority       int                    `json:"priority"`
}

// SaveEndpointData creates or updates an endpoint
func (a *App) SaveEndpointData(endpoint *EndpointInput) (*EndpointInfo, error) {
	if a.storage == nil {
		return nil, fmt.Errorf("storage not initialized")
	}

	// 更新已有端点时，保留前端表单未覆盖的字段（如 transformer/proxy/models/headers），避免意外清空。
	var existing *storage.Endpoint
	if endpoint.ID > 0 {
		if ep, err := a.storage.GetEndpointByID(endpoint.ID); err == nil {
			existing = ep
		}
	}

	// Default priority to 5 if not set
	priority := endpoint.Priority
	if priority == 0 {
		priority = 5
	}
	ep := &storage.Endpoint{
		ID:            endpoint.ID,
		Name:          endpoint.Name,
		APIURL:        endpoint.APIURL,
		APIKey:        endpoint.APIKey,
		Active:        endpoint.Active,
		Enabled:       endpoint.Enabled,
		InterfaceType: endpoint.InterfaceType,
		VendorID:      endpoint.VendorID,
		Model:         endpoint.Model,
		Transformer:   endpoint.Transformer,
		ProxyURL:      endpoint.ProxyURL,
		Models:        endpoint.Models,
		Remark:        endpoint.Remark,
		Priority:      priority,
	}
	if existing != nil {
		// transformer 支持显式清空：前端会发送 transformerSet=true，
		// 只有当旧客户端未发送该字段时才走“空值保留”逻辑，避免误清空。
		if !endpoint.TransformerSet && ep.Transformer == "" {
			ep.Transformer = existing.Transformer
		}
		if ep.ProxyURL == "" {
			ep.ProxyURL = existing.ProxyURL
		}
		if ep.Headers == nil {
			ep.Headers = existing.Headers
		}
		// models 支持显式清空：前端会发送 modelsSet=true，
		// 只有当旧客户端未发送该字段时才走“空值保留”逻辑，避免误清空。
		if !endpoint.ModelsSet && ep.Models == nil {
			ep.Models = existing.Models
		}
	}
	if err := a.storage.SaveEndpoint(ep); err != nil {
		return nil, err
	}

	// Reload endpoints into router
	if a.router != nil {
		endpoints, err := a.storage.GetEndpoints()
		if err == nil {
			a.router.LoadEndpoints(convertEndpoints(endpoints))
		}
	}

	return &EndpointInfo{
		ID:            ep.ID,
		Name:          ep.Name,
		APIURL:        ep.APIURL,
		Active:        ep.Active,
		Enabled:       ep.Enabled,
		InterfaceType: ep.InterfaceType,
		VendorID:      ep.VendorID,
		Model:         ep.Model,
		Remark:        ep.Remark,
		Priority:      ep.Priority,
	}, nil
}

// DeleteEndpoint deletes an endpoint by ID
func (a *App) DeleteEndpoint(id int64) error {
	if a.storage == nil {
		return fmt.Errorf("storage not initialized")
	}
	err := a.storage.DeleteEndpoint(id)
	if err != nil {
		return err
	}

	// Reload endpoints into router
	if a.router != nil {
		endpoints, err := a.storage.GetEndpoints()
		if err == nil {
			a.router.LoadEndpoints(convertEndpoints(endpoints))
		}
	}

	return nil
}

// =============================================================================
// Language Settings
// =============================================================================

// GetLanguage returns the current language setting.
// On first launch (no saved language), it detects the system language and uses it if supported.
func (a *App) GetLanguage() (string, error) {
	if a.storage == nil {
		return detectSystemLanguage(), nil
	}
	lang, err := a.storage.GetConfig("language")
	if err != nil || lang == "" {
		// First launch: detect system language and save it
		detectedLang := detectSystemLanguage()
		_ = a.storage.SetConfig("language", detectedLang)
		return detectedLang, nil
	}
	return lang, nil
}

// detectSystemLanguage detects the system language and returns a supported language code.
// Supported languages: "en", "zh-CN"
func detectSystemLanguage() string {
	// Try LANG environment variable first (common on Unix-like systems)
	lang := os.Getenv("LANG")
	if lang == "" {
		lang = os.Getenv("LC_ALL")
	}
	if lang == "" {
		lang = os.Getenv("LC_MESSAGES")
	}

	// Check for Chinese language
	langLower := strings.ToLower(lang)
	if strings.HasPrefix(langLower, "zh") {
		return "zh-CN"
	}

	// Default to English
	return "en"
}

// SetLanguage sets the language setting
func (a *App) SetLanguage(lang string) error {
	if a.storage == nil {
		return fmt.Errorf("storage not initialized")
	}
	return a.storage.SetConfig("language", lang)
}

// GetWebSocketURL returns the WebSocket URL for real-time updates
func (a *App) GetWebSocketURL() string {
	port := 5600
	if a.proxyServer != nil {
		port = a.proxyServer.GetPort()
	}
	return fmt.Sprintf("ws://localhost:%d/ws", port)
}

// =============================================================================
// SQLite Token Statistics Methods (New Design)
// =============================================================================

// VendorStatsSummaryInfo represents aggregated stats for a vendor (frontend)
type VendorStatsSummaryInfo struct {
	VendorID     string                     `json:"vendorId"`
	VendorName   string                     `json:"vendorName"`
	InputTokens  int64                      `json:"inputTokens"`
	OutputTokens int64                      `json:"outputTokens"`
	CachedCreate int64                      `json:"cachedCreate"`
	CachedRead   int64                      `json:"cachedRead"`
	Reasoning    int64                      `json:"reasoning"`
	Total        int64                      `json:"total"`
	Endpoints    []EndpointStatsSummaryInfo `json:"endpoints"`
}

// EndpointStatsSummaryInfo represents aggregated stats for an endpoint (frontend)
type EndpointStatsSummaryInfo struct {
	EndpointID   string `json:"endpointId"`
	EndpointName string `json:"endpointName"`
	VendorName   string `json:"vendorName"`
	Date         string `json:"date,omitempty"`
	InputTokens  int64  `json:"inputTokens"`
	OutputTokens int64  `json:"outputTokens"`
	CachedCreate int64  `json:"cachedCreate"`
	CachedRead   int64  `json:"cachedRead"`
	Reasoning    int64  `json:"reasoning"`
	Total        int64  `json:"total"`
	RequestCount int64  `json:"requestCount"`
}

// InterfaceTypeStatsSummaryInfo represents aggregated stats grouped by interface type (frontend)
type InterfaceTypeStatsSummaryInfo struct {
	InterfaceType string                     `json:"interfaceType"`
	InputTokens   int64                      `json:"inputTokens"`
	OutputTokens  int64                      `json:"outputTokens"`
	CachedCreate  int64                      `json:"cachedCreate"`
	CachedRead    int64                      `json:"cachedRead"`
	Reasoning     int64                      `json:"reasoning"`
	Total         int64                      `json:"total"`
	RequestCount  int64                      `json:"requestCount"`
	Endpoints     []EndpointStatsSummaryInfo `json:"endpoints"`
}

// GetTokenStatsByTimeRange returns token statistics grouped by vendor for the given time range
func (a *App) GetTokenStatsByTimeRange(timeRange string) ([]*VendorStatsSummaryInfo, error) {
	if a.vendorStats == nil {
		return []*VendorStatsSummaryInfo{}, nil
	}

	tr := statsdb.TimeRange(timeRange)
	stats, err := a.vendorStats.GetStatsByTimeRange(a.ctx, tr)
	if err != nil {
		return nil, fmt.Errorf("failed to get stats: %w", err)
	}

	result := make([]*VendorStatsSummaryInfo, 0, len(stats))
	for _, s := range stats {
		endpoints := make([]EndpointStatsSummaryInfo, 0, len(s.Endpoints))
		for _, ep := range s.Endpoints {
			endpoints = append(endpoints, EndpointStatsSummaryInfo{
				EndpointID:   ep.EndpointID,
				EndpointName: ep.EndpointName,
				InputTokens:  ep.InputTokens,
				OutputTokens: ep.OutputTokens,
				CachedCreate: ep.CachedCreate,
				CachedRead:   ep.CachedRead,
				Reasoning:    ep.Reasoning,
				Total:        ep.Total,
			})
		}
		result = append(result, &VendorStatsSummaryInfo{
			VendorID:     s.VendorID,
			VendorName:   s.VendorName,
			InputTokens:  s.InputTokens,
			OutputTokens: s.OutputTokens,
			CachedCreate: s.CachedCreate,
			CachedRead:   s.CachedRead,
			Reasoning:    s.Reasoning,
			Total:        s.Total,
			Endpoints:    endpoints,
		})
	}

	return result, nil
}

// GetStatsByInterfaceType returns token statistics grouped by interface type for the given time range
func (a *App) GetStatsByInterfaceType(timeRange string) ([]*InterfaceTypeStatsSummaryInfo, error) {
	if a.vendorStats == nil {
		return []*InterfaceTypeStatsSummaryInfo{}, nil
	}

	tr := statsdb.TimeRange(timeRange)
	stats, err := a.vendorStats.GetStatsByInterfaceType(a.ctx, tr)
	if err != nil {
		return nil, fmt.Errorf("failed to get stats by interface type: %w", err)
	}

	result := make([]*InterfaceTypeStatsSummaryInfo, 0, len(stats))
	for _, s := range stats {
		endpoints := make([]EndpointStatsSummaryInfo, 0, len(s.Endpoints))
		for _, ep := range s.Endpoints {
			endpoints = append(endpoints, EndpointStatsSummaryInfo{
				EndpointID:   ep.EndpointID,
				EndpointName: ep.EndpointName,
				VendorName:   ep.VendorName,
				Date:         ep.Date,
				InputTokens:  ep.InputTokens,
				OutputTokens: ep.OutputTokens,
				CachedCreate: ep.CachedCreate,
				CachedRead:   ep.CachedRead,
				Reasoning:    ep.Reasoning,
				Total:        ep.Total,
				RequestCount: ep.RequestCount,
			})
		}
		result = append(result, &InterfaceTypeStatsSummaryInfo{
			InterfaceType: s.InterfaceType,
			InputTokens:   s.InputTokens,
			OutputTokens:  s.OutputTokens,
			CachedCreate:  s.CachedCreate,
			CachedRead:    s.CachedRead,
			Reasoning:     s.Reasoning,
			Total:         s.Total,
			RequestCount:  s.RequestCount,
			Endpoints:     endpoints,
		})
	}

	return result, nil
}

// ClearTokenStats clears token statistics for the given time range
func (a *App) ClearTokenStats(timeRange string) error {
	fmt.Printf("[ClearTokenStats] Called with timeRange: %s\n", timeRange)

	if a.vendorStats == nil {
		fmt.Println("[ClearTokenStats] Error: vendor stats store not initialized")
		return fmt.Errorf("vendor stats store not initialized")
	}

	tr := statsdb.TimeRange(timeRange)
	fmt.Printf("[ClearTokenStats] Calling ClearStats with TimeRange: %s\n", tr)

	if err := a.vendorStats.ClearStats(a.ctx, tr); err != nil {
		fmt.Printf("[ClearTokenStats] Error: %v\n", err)
		return fmt.Errorf("failed to clear stats: %w", err)
	}

	fmt.Println("[ClearTokenStats] Success")
	return nil
}

// =============================================================================
// Endpoint Testing Methods
// =============================================================================

// TestEndpointResult represents the result of an endpoint test
type TestEndpointResult struct {
	Success        bool              `json:"success"`
	StatusCode     int               `json:"statusCode,omitempty"`
	Message        string            `json:"message"`
	TargetURL      string            `json:"targetUrl,omitempty"`
	RequestHeaders map[string]string `json:"requestHeaders,omitempty"`
	ErrorMessage   string            `json:"errorMessage,omitempty"`
	ResponseText   string            `json:"responseText,omitempty"`
}

// TestEndpointParams represents parameters for testing an endpoint
type TestEndpointParams struct {
	APIURL        string `json:"apiUrl"`
	APIKey        string `json:"apiKey"`
	InterfaceType string `json:"interfaceType"`
	Model         string `json:"model"`
	Reasoning     string `json:"reasoning,omitempty"`
}

// TestEndpointWithParams tests an endpoint using provided parameters (from form)
// This allows testing with current form values before saving
func (a *App) TestEndpointWithParams(params TestEndpointParams) string {
	return a.doTestEndpoint(params.APIURL, params.APIKey, params.InterfaceType, params.Model, params.Reasoning)
}

// TestEndpoint tests an endpoint by ID (uses saved values from database)
// Only supports claude and codex interface types
func (a *App) TestEndpoint(endpointID int64) string {
	if a.storage == nil {
		return toJSON(TestEndpointResult{Success: false, Message: "Storage not initialized"})
	}

	ep, err := a.storage.GetEndpointByID(endpointID)
	if err != nil || ep == nil {
		return toJSON(TestEndpointResult{Success: false, Message: fmt.Sprintf("Endpoint not found: %d", endpointID)})
	}

	return a.doTestEndpoint(ep.APIURL, ep.APIKey, ep.InterfaceType, ep.Model, "")
}

// doTestEndpoint performs the actual endpoint test
func (a *App) doTestEndpoint(apiURL, apiKey, interfaceType, model, reasoning string) string {
	// Only support claude and codex types
	if interfaceType != "claude" && interfaceType != "codex" {
		return toJSON(TestEndpointResult{Success: false, Message: fmt.Sprintf("Test not supported for interface type: %s", interfaceType)})
	}

	// Build test request based on interface type
	var requestBody []byte
	var apiPath string
	testMessage := "Say 'OK' only"
	testMaxTokens := 10

	switch interfaceType {
	case "claude":
		apiPath = "/v1/messages"
		if model == "" {
			model = "claude-sonnet-4-20250514"
		}
		requestBody, _ = json.Marshal(map[string]interface{}{
			"model":      model,
			"max_tokens": testMaxTokens,
			"messages": []map[string]interface{}{
				{
					"role": "user",
					"content": []map[string]string{
						{"type": "text", "text": testMessage},
					},
				},
			},
			"stream": true,
		})
	case "codex":
		apiPath = "/v1/responses"
		if model == "" {
			model = "codex-mini-latest"
		}
		body := map[string]interface{}{
			"model":        model,
			"instructions": "You are Codex, based on GPT-5.",
			"input": []map[string]interface{}{
				{
					"type": "message",
					"role": "user",
					"content": []map[string]interface{}{
						{"type": "input_text", "text": testMessage},
					},
				},
			},
			"stream":  true,
			"store":   false,
			"include": []string{"reasoning.encrypted_content"},
		}
		if strings.TrimSpace(reasoning) != "" {
			body["reasoning"] = map[string]interface{}{
				"effort": strings.TrimSpace(reasoning),
			}
		}
		requestBody, _ = json.Marshal(body)
	}

	targetURL, err := buildTestTargetURL(apiURL, apiPath)
	if err != nil {
		return toJSON(TestEndpointResult{Success: false, Message: fmt.Sprintf("Invalid API URL: %v", err)})
	}

	parsedTargetURL, err := url.Parse(targetURL)
	if err != nil {
		return toJSON(TestEndpointResult{Success: false, Message: fmt.Sprintf("Invalid target URL: %v", err)})
	}
	if interfaceType == "claude" {
		q := parsedTargetURL.Query()
		q.Set("beta", "true")
		parsedTargetURL.RawQuery = q.Encode()
		targetURL = parsedTargetURL.String()
	}

	// Create HTTP request
	req, err := http.NewRequest("POST", targetURL, bytes.NewReader(requestBody))
	if err != nil {
		return toJSON(TestEndpointResult{Success: false, TargetURL: targetURL, Message: fmt.Sprintf("Failed to create request: %v", err)})
	}

	// Set headers based on interface type
	switch interfaceType {
	case "claude":
		req.Host = parsedTargetURL.Host
		req.Header.Set("accept", "application/json")
		req.Header.Set("accept-encoding", "gzip, deflate")
		req.Header.Set("accept-language", "*")
		req.Header.Set("anthropic-beta", "claude-code-20250219,interleaved-thinking-2025-05-14,fine-grained-tool-streaming-2025-05-14")
		req.Header.Set("anthropic-dangerous-direct-browser-access", "true")
		req.Header.Set("anthropic-version", "2023-06-01")
		req.Header.Set("connection", "keep-alive")
		req.Header.Set("content-type", "application/json")
		req.Header.Set("sec-fetch-mode", "cors")
		req.Header.Set("user-agent", "claude-cli/2.0.0 (external, cli)")
		req.Header.Set("x-app", "cli")
		req.Header.Set("x-stainless-arch", "arm64")
		req.Header.Set("x-stainless-helper-method", "stream")
		req.Header.Set("x-stainless-lang", "js")
		req.Header.Set("x-stainless-os", "MacOS")
		req.Header.Set("x-stainless-package-version", "0.60.0")
		req.Header.Set("x-stainless-retry-count", "0")
		req.Header.Set("x-stainless-runtime", "node")
		req.Header.Set("x-stainless-runtime-version", "v23.11.0")
		req.Header.Set("x-stainless-timeout", "600")
		req.Header.Set("x-api-key", apiKey)
	case "codex":
		req.Host = parsedTargetURL.Host
		sessionID := uuid.NewString()
		req.Header.Set("accept", "text/event-stream")
		req.Header.Set("accept-encoding", "gzip")
		req.Header.Set("authorization", "Bearer "+apiKey)
		req.Header.Set("connection", "keep-alive")
		req.Header.Set("content-type", "application/json")
		req.Header.Set("conversation_id", sessionID)
		req.Header.Set("openai-beta", "responses=experimental")
		req.Header.Set("originator", "codex_cli_rs")
		req.Header.Set("session_id", sessionID)
		req.Header.Set("user-agent", "codex_cli_rs/0.42.0 (Mac OS 26.0.0; arm64) Apple_Terminal/464")
	}
	requestHeaders := sanitizeRequestHeadersForTestLog(req)

	// Send request with timeout
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return toJSON(TestEndpointResult{Success: false, TargetURL: targetURL, RequestHeaders: requestHeaders, Message: fmt.Sprintf("Request failed: %v", err)})
	}
	defer resp.Body.Close()

	// Read response
	respBody, err := readResponseBodyLimited(resp, 256*1024)
	if err != nil {
		return toJSON(TestEndpointResult{Success: false, TargetURL: targetURL, RequestHeaders: requestHeaders, Message: fmt.Sprintf("Failed to read response: %v", err)})
	}
	respText := string(respBody)

	// Check status code
	if resp.StatusCode != http.StatusOK {
		return toJSON(TestEndpointResult{
			Success:        false,
			StatusCode:     resp.StatusCode,
			TargetURL:      targetURL,
			RequestHeaders: requestHeaders,
			ErrorMessage:   fmt.Sprintf("HTTP %d: %s", resp.StatusCode, resp.Status),
			ResponseText:   respText,
			Message:        fmt.Sprintf("HTTP %d: %s", resp.StatusCode, respText),
		})
	}

	// Parse response to extract content
	var responseData map[string]interface{}
	if err := json.Unmarshal(respBody, &responseData); err != nil {
		return toJSON(TestEndpointResult{Success: true, TargetURL: targetURL, RequestHeaders: requestHeaders, StatusCode: resp.StatusCode, Message: respText, ResponseText: respText})
	}

	// Extract message based on interface type
	var message string
	switch interfaceType {
	case "claude":
		if content, ok := responseData["content"].([]interface{}); ok && len(content) > 0 {
			if textBlock, ok := content[0].(map[string]interface{}); ok {
				if text, ok := textBlock["text"].(string); ok {
					message = text
				}
			}
		}
	case "codex":
		if output, ok := responseData["output"].([]interface{}); ok && len(output) > 0 {
			if item, ok := output[0].(map[string]interface{}); ok {
				if content, ok := item["content"].([]interface{}); ok && len(content) > 0 {
					if textItem, ok := content[0].(map[string]interface{}); ok {
						if text, ok := textItem["text"].(string); ok {
							message = text
						}
					}
				}
			}
		}
	}

	if message == "" {
		message = "Connection successful"
	}

	return toJSON(TestEndpointResult{Success: true, StatusCode: resp.StatusCode, TargetURL: targetURL, RequestHeaders: requestHeaders, Message: message, ResponseText: respText})
}

func toJSON(v interface{}) string {
	data, _ := json.Marshal(v)
	return string(data)
}

func buildTestTargetURL(apiURL, apiPath string) (string, error) {
	raw := strings.TrimSpace(apiURL)
	raw = strings.TrimSuffix(raw, "/")
	if raw == "" {
		return "", fmt.Errorf("empty api url")
	}
	if !strings.HasPrefix(raw, "http://") && !strings.HasPrefix(raw, "https://") {
		raw = "https://" + raw
	}

	u, err := url.Parse(raw)
	if err != nil {
		return "", err
	}

	pathToAppend := strings.TrimSpace(apiPath)
	if pathToAppend == "" {
		pathToAppend = "/"
	}
	if !strings.HasPrefix(pathToAppend, "/") {
		pathToAppend = "/" + pathToAppend
	}
	pathToAppend = strings.TrimSuffix(pathToAppend, "/")

	basePath := strings.TrimSuffix(u.Path, "/")
	if basePath != "" && strings.HasSuffix(basePath, pathToAppend) {
		return u.String(), nil
	}

	if strings.HasSuffix(basePath, "/v1") && strings.HasPrefix(pathToAppend, "/v1/") {
		pathToAppend = strings.TrimPrefix(pathToAppend, "/v1")
		if pathToAppend == "" {
			pathToAppend = "/"
		}
	}

	u.Path = strings.TrimSuffix(u.Path, "/") + pathToAppend
	return u.String(), nil
}

func readResponseBodyLimited(resp *http.Response, limit int64) ([]byte, error) {
	if resp == nil || resp.Body == nil {
		return nil, fmt.Errorf("nil response")
	}

	var reader io.Reader = resp.Body
	if strings.EqualFold(resp.Header.Get("Content-Encoding"), "gzip") {
		gz, err := gzip.NewReader(resp.Body)
		if err != nil {
			return nil, err
		}
		defer gz.Close()
		reader = gz
	}

	return io.ReadAll(io.LimitReader(reader, limit))
}

func sanitizeRequestHeadersForTestLog(req *http.Request) map[string]string {
	if req == nil {
		return map[string]string{}
	}

	out := make(map[string]string, len(req.Header)+1)
	if host := strings.TrimSpace(req.Host); host != "" {
		out["host"] = host
	}
	for key, values := range req.Header {
		if len(values) == 0 {
			continue
		}
		out[key] = sanitizeTestHeaderValue(key, values[0])
	}
	return out
}

func sanitizeTestHeaderValue(key string, value string) string {
	if value == "" {
		return ""
	}
	if strings.EqualFold(key, "authorization") || strings.EqualFold(key, "proxy-authorization") {
		return proxy.MaskAuthorizationValue(value)
	}
	if strings.EqualFold(key, "x-api-key") {
		return maskSecret(value)
	}
	if strings.EqualFold(key, "cookie") {
		return "[redacted]"
	}
	return value
}

func maskSecret(secret string) string {
	s := strings.TrimSpace(secret)
	if s == "" {
		return ""
	}
	if len(s) <= 8 {
		return "****"
	}
	prefixLen := 8
	suffixLen := 4
	if len(s) <= prefixLen+suffixLen {
		return "****"
	}
	return s[:prefixLen] + "..." + s[len(s)-suffixLen:]
}

// FetchModelsResult represents the result of fetching models
type FetchModelsResult struct {
	Success bool     `json:"success"`
	Message string   `json:"message"`
	Models  []string `json:"models"`
}

// FetchModels fetches available models from the API provider
func (a *App) FetchModels(apiURL, apiKey, interfaceType string) string {
	if interfaceType == "" {
		interfaceType = "claude"
	}

	// Normalize API URL
	apiURL = strings.TrimSuffix(apiURL, "/")
	if !strings.HasPrefix(apiURL, "http://") && !strings.HasPrefix(apiURL, "https://") {
		apiURL = "https://" + apiURL
	}

	var models []string
	var err error

	switch interfaceType {
	case "claude", "codex", "chat":
		models, err = a.fetchOpenAIModels(apiURL, apiKey)
	case "gemini":
		models, err = a.fetchGeminiModels(apiURL, apiKey)
	default:
		return toJSON(FetchModelsResult{Success: false, Message: fmt.Sprintf("Unsupported interface type: %s", interfaceType), Models: []string{}})
	}

	if err != nil {
		return toJSON(FetchModelsResult{Success: false, Message: err.Error(), Models: []string{}})
	}

	return toJSON(FetchModelsResult{Success: true, Message: fmt.Sprintf("Found %d models", len(models)), Models: models})
}

// fetchOpenAIModels fetches models from OpenAI-compatible API
func (a *App) fetchOpenAIModels(apiURL, apiKey string) ([]string, error) {
	url := fmt.Sprintf("%s/v1/models", apiURL)

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %v", err)
	}

	req.Header.Set("Authorization", "Bearer "+apiKey)

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("no_models_found")
	}

	var result struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to parse response: %v", err)
	}

	seen := make(map[string]bool)
	models := make([]string, 0, len(result.Data))
	for _, m := range result.Data {
		id := strings.TrimSpace(m.ID)
		if id != "" && !seen[id] {
			seen[id] = true
			models = append(models, id)
		}
	}

	return models, nil
}

// fetchGeminiModels fetches models from Gemini API
func (a *App) fetchGeminiModels(apiURL, apiKey string) ([]string, error) {
	url := fmt.Sprintf("%s/v1beta/models?key=%s", apiURL, apiKey)

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %v", err)
	}

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(body))
	}

	var result struct {
		Models []struct {
			Name string `json:"name"`
		} `json:"models"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to parse response: %v", err)
	}

	models := make([]string, 0, len(result.Models))
	for _, m := range result.Models {
		name := m.Name
		if after, ok := strings.CutPrefix(name, "models/"); ok {
			name = after
		}
		models = append(models, name)
	}

	return models, nil
}

// =============================================================================
// CLI Config Editor Methods (Claude Code & Codex)
// =============================================================================

// CLIConfigDirs represents the CLI config directories
type CLIConfigDirs struct {
	ClaudeConfigDir string `json:"claudeConfigDir"`
	CodexConfigDir  string `json:"codexConfigDir"`
}

// CLIConfigFile represents a config file content
type CLIConfigFile struct {
	Name              string `json:"name"`
	Content           string `json:"content"`
	Exists            bool   `json:"exists"`
	IsProxyConfigured bool   `json:"isProxyConfigured"`
}

// CLIConfigResult represents the result of reading CLI configs
type CLIConfigResult struct {
	Success bool            `json:"success"`
	Message string          `json:"message,omitempty"`
	Files   []CLIConfigFile `json:"files,omitempty"`
}

// GetCLIConfigDirs returns the CLI config directories
func (a *App) GetCLIConfigDirs() (*CLIConfigDirs, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("failed to get home directory: %w", err)
	}

	dirs := &CLIConfigDirs{
		ClaudeConfigDir: filepath.Join(homeDir, ".claude"),
		CodexConfigDir:  filepath.Join(homeDir, ".codex"),
	}

	// Try to get saved values from storage
	if a.storage != nil {
		if saved, err := a.storage.GetConfig("claudeConfigDir"); err == nil && saved != "" {
			dirs.ClaudeConfigDir = saved
		}
		if saved, err := a.storage.GetConfig("codexConfigDir"); err == nil && saved != "" {
			dirs.CodexConfigDir = saved
		}
	}

	return dirs, nil
}

// SaveCLIConfigDirs saves the CLI config directories
func (a *App) SaveCLIConfigDirs(dirs *CLIConfigDirs) error {
	if a.storage == nil {
		return fmt.Errorf("storage not initialized")
	}

	if err := a.storage.SetConfig("claudeConfigDir", dirs.ClaudeConfigDir); err != nil {
		return fmt.Errorf("failed to save claude config dir: %w", err)
	}
	if err := a.storage.SetConfig("codexConfigDir", dirs.CodexConfigDir); err != nil {
		return fmt.Errorf("failed to save codex config dir: %w", err)
	}

	return nil
}

// GetClaudeConfig reads Claude Code config files
func (a *App) GetClaudeConfig() (*CLIConfigResult, error) {
	dirs, err := a.GetCLIConfigDirs()
	if err != nil {
		return &CLIConfigResult{Success: false, Message: err.Error()}, nil
	}

	settings, _ := a.GetSettings()
	proxyURL := fmt.Sprintf("http://127.0.0.1:%d", settings.Port)

	homeDir, _ := os.UserHomeDir()
	files := []CLIConfigFile{}

	// Read settings.json from config dir
	settingsPath := filepath.Join(dirs.ClaudeConfigDir, "settings.json")
	settingsContent, settingsExists := readFileContent(settingsPath)
	if !settingsExists {
		// Create default settings.json
		settingsContent = a.getDefaultClaudeSettings()
	}

	// Check if proxy is configured by looking for ANTHROPIC_BASE_URL
	isProxyConfigured := strings.Contains(settingsContent, proxyURL)

	files = append(files, CLIConfigFile{Name: "settings.json", Content: settingsContent, Exists: settingsExists, IsProxyConfigured: isProxyConfigured})

	// Ensure ~/.claude.json has hasCompletedOnboarding: true
	claudeJsonPath := filepath.Join(homeDir, ".claude.json")
	claudeJsonData := map[string]interface{}{}
	if data, err := os.ReadFile(claudeJsonPath); err == nil {
		json.Unmarshal(data, &claudeJsonData)
	}
	claudeJsonData["hasCompletedOnboarding"] = true
	if newData, err := json.MarshalIndent(claudeJsonData, "", "  "); err == nil {
		os.WriteFile(claudeJsonPath, newData, 0644)
	}

	return &CLIConfigResult{Success: true, Files: files}, nil
}

// GetCodexConfig reads Codex config files
func (a *App) GetCodexConfig() (*CLIConfigResult, error) {
	dirs, err := a.GetCLIConfigDirs()
	if err != nil {
		return &CLIConfigResult{Success: false, Message: err.Error()}, nil
	}

	settings, _ := a.GetSettings()
	proxyURL := fmt.Sprintf("http://127.0.0.1:%d/v1", settings.Port)

	files := []CLIConfigFile{}

	// Read config.toml
	configPath := filepath.Join(dirs.CodexConfigDir, "config.toml")
	configContent, configExists := readFileContent(configPath)
	if !configExists {
		configContent = a.getDefaultCodexConfig()
	}

	// Check if proxy is configured by looking for base_url
	isConfigProxyConfigured := strings.Contains(configContent, proxyURL)

	files = append(files, CLIConfigFile{Name: "config.toml", Content: configContent, Exists: configExists, IsProxyConfigured: isConfigProxyConfigured})

	// Read auth.json
	authPath := filepath.Join(dirs.CodexConfigDir, "auth.json")
	authContent, authExists := readFileContent(authPath)
	if !authExists {
		authContent = a.getDefaultCodexAuth()
	}
	files = append(files, CLIConfigFile{Name: "auth.json", Content: authContent, Exists: authExists, IsProxyConfigured: true})

	return &CLIConfigResult{Success: true, Files: files}, nil
}

// SaveClaudeConfig saves Claude Code config file
func (a *App) SaveClaudeConfig(content string) error {
	// Validate JSON
	var js json.RawMessage
	if err := json.Unmarshal([]byte(content), &js); err != nil {
		return fmt.Errorf("invalid JSON format: %w", err)
	}

	dirs, err := a.GetCLIConfigDirs()
	if err != nil {
		return err
	}

	// Ensure directory exists
	if err := os.MkdirAll(dirs.ClaudeConfigDir, 0755); err != nil {
		return fmt.Errorf("failed to create config directory: %w", err)
	}

	// 隐性需求：保存时确保 {claudeConfigDir}/config.json 存在且包含 primaryApiKey
	if err := ensureClaudePrimaryAPIKeyConfig(dirs.ClaudeConfigDir); err != nil {
		return err
	}

	settingsPath := filepath.Join(dirs.ClaudeConfigDir, "settings.json")
	if err := os.WriteFile(settingsPath, []byte(content), 0644); err != nil {
		return fmt.Errorf("failed to write settings.json: %w", err)
	}

	return nil
}

// SaveCodexConfig saves Codex config files
func (a *App) SaveCodexConfig(configToml, authJson string) error {
	dirs, err := a.GetCLIConfigDirs()
	if err != nil {
		return err
	}

	// Ensure directory exists
	if err := os.MkdirAll(dirs.CodexConfigDir, 0755); err != nil {
		return fmt.Errorf("failed to create config directory: %w", err)
	}

	// Save config.toml (basic TOML validation - just check it's not empty)
	if strings.TrimSpace(configToml) == "" {
		return fmt.Errorf("config.toml cannot be empty")
	}
	configPath := filepath.Join(dirs.CodexConfigDir, "config.toml")
	if err := os.WriteFile(configPath, []byte(configToml), 0644); err != nil {
		return fmt.Errorf("failed to write config.toml: %w", err)
	}

	// Validate and save auth.json
	var js json.RawMessage
	if err := json.Unmarshal([]byte(authJson), &js); err != nil {
		return fmt.Errorf("invalid JSON format in auth.json: %w", err)
	}
	authPath := filepath.Join(dirs.CodexConfigDir, "auth.json")
	if err := os.WriteFile(authPath, []byte(authJson), 0644); err != nil {
		return fmt.Errorf("failed to write auth.json: %w", err)
	}

	return nil
}

// ProcessClaudeConfig processes Claude config with proxy settings
func (a *App) ProcessClaudeConfig(content string) (string, error) {
	settings, err := a.GetSettings()
	if err != nil {
		return "", err
	}

	// Parse JSON
	var config map[string]interface{}
	if err := json.Unmarshal([]byte(content), &config); err != nil {
		return "", fmt.Errorf("invalid JSON: %w", err)
	}

	// Ensure env section exists
	env, ok := config["env"].(map[string]interface{})
	if !ok {
		env = make(map[string]interface{})
		config["env"] = env
	}

	// Set proxy URL and API key
	proxyURL := fmt.Sprintf("http://127.0.0.1:%d", settings.Port)
	env["ANTHROPIC_BASE_URL"] = proxyURL

	apiKey := settings.APIKey
	if apiKey == "" {
		apiKey = "-"
	}
	env["ANTHROPIC_AUTH_TOKEN"] = apiKey
	env["CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC"] = "1"

	// Marshal back to JSON with indentation
	result, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return "", err
	}

	return string(result), nil
}

// ProcessCodexConfigResult represents the result of processing Codex config
type ProcessCodexConfigResult struct {
	ConfigToml string `json:"configToml"`
	AuthJson   string `json:"authJson"`
}

// ProcessCodexConfig processes Codex config with proxy settings
func (a *App) ProcessCodexConfig(configToml, authJson string) (*ProcessCodexConfigResult, error) {
	settings, err := a.GetSettings()
	if err != nil {
		return nil, err
	}

	proxyURL := fmt.Sprintf("http://127.0.0.1:%d/v1", settings.Port)
	apiKey := settings.APIKey
	if apiKey == "" {
		apiKey = "-"
	}

	// Process config.toml - replace base_url
	// Simple string replacement for TOML
	lines := strings.Split(configToml, "\n")
	var newLines []string
	inLocalProvider := false

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)

		// Track if we're in [model_providers.local] section
		if strings.HasPrefix(trimmed, "[model_providers.local]") {
			inLocalProvider = true
		} else if strings.HasPrefix(trimmed, "[") && !strings.HasPrefix(trimmed, "[model_providers.local]") {
			inLocalProvider = false
		}

		// Replace base_url in local provider section
		if inLocalProvider && strings.HasPrefix(trimmed, "base_url") {
			newLines = append(newLines, fmt.Sprintf("base_url = '%s'", proxyURL))
		} else {
			newLines = append(newLines, line)
		}
	}
	newConfigToml := strings.Join(newLines, "\n")

	// Process auth.json
	var auth map[string]interface{}
	if err := json.Unmarshal([]byte(authJson), &auth); err != nil {
		// If invalid, create new
		auth = make(map[string]interface{})
	}
	auth["OPENAI_API_KEY"] = apiKey

	newAuthJson, err := json.MarshalIndent(auth, "", "  ")
	if err != nil {
		return nil, err
	}

	return &ProcessCodexConfigResult{
		ConfigToml: newConfigToml,
		AuthJson:   string(newAuthJson),
	}, nil
}

// ProcessClaudeConfigWithIP processes Claude config with proxy settings using specified IP
func (a *App) ProcessClaudeConfigWithIP(content string, ip string) (string, error) {
	settings, err := a.GetSettings()
	if err != nil {
		return "", err
	}

	// Parse JSON
	var config map[string]interface{}
	if err := json.Unmarshal([]byte(content), &config); err != nil {
		return "", fmt.Errorf("invalid JSON: %w", err)
	}

	// Ensure env section exists
	env, ok := config["env"].(map[string]interface{})
	if !ok {
		env = make(map[string]interface{})
		config["env"] = env
	}

	// Set proxy URL with user-selected IP and API key
	proxyURL := fmt.Sprintf("http://%s:%d", ip, settings.Port)
	env["ANTHROPIC_BASE_URL"] = proxyURL

	apiKey := settings.APIKey
	if apiKey == "" {
		apiKey = "-"
	}
	env["ANTHROPIC_AUTH_TOKEN"] = apiKey
	env["CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC"] = "1"

	// Marshal back to JSON with indentation
	result, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return "", err
	}

	return string(result), nil
}

// ProcessCodexConfigWithIP processes Codex config with proxy settings using specified IP
func (a *App) ProcessCodexConfigWithIP(configToml, authJson, ip string) (*ProcessCodexConfigResult, error) {
	settings, err := a.GetSettings()
	if err != nil {
		return nil, err
	}

	proxyURL := fmt.Sprintf("http://%s:%d/v1", ip, settings.Port)
	apiKey := settings.APIKey
	if apiKey == "" {
		apiKey = "-"
	}

	// Process config.toml - replace base_url
	lines := strings.Split(configToml, "\n")
	var newLines []string
	inLocalProvider := false

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)

		// Track if we're in [model_providers.local] section
		if strings.HasPrefix(trimmed, "[model_providers.local]") {
			inLocalProvider = true
		} else if strings.HasPrefix(trimmed, "[") && !strings.HasPrefix(trimmed, "[model_providers.local]") {
			inLocalProvider = false
		}

		// Replace base_url in local provider section
		if inLocalProvider && strings.HasPrefix(trimmed, "base_url") {
			newLines = append(newLines, fmt.Sprintf("base_url = '%s'", proxyURL))
		} else {
			newLines = append(newLines, line)
		}
	}
	newConfigToml := strings.Join(newLines, "\n")

	// Process auth.json
	var auth map[string]interface{}
	if err := json.Unmarshal([]byte(authJson), &auth); err != nil {
		auth = make(map[string]interface{})
	}
	auth["OPENAI_API_KEY"] = apiKey

	newAuthJson, err := json.MarshalIndent(auth, "", "  ")
	if err != nil {
		return nil, err
	}

	return &ProcessCodexConfigResult{
		ConfigToml: newConfigToml,
		AuthJson:   string(newAuthJson),
	}, nil
}

// Helper functions

func readFileContent(path string) (string, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", false
	}
	return string(data), true
}

func ensureClaudePrimaryAPIKeyConfig(claudeConfigDir string) error {
	if strings.TrimSpace(claudeConfigDir) == "" {
		return fmt.Errorf("claude config dir is empty")
	}

	if err := os.MkdirAll(claudeConfigDir, 0755); err != nil {
		return fmt.Errorf("failed to create claude config directory: %w", err)
	}

	configPath := filepath.Join(claudeConfigDir, "config.json")
	data, err := os.ReadFile(configPath)
	if err != nil {
		if !os.IsNotExist(err) {
			return fmt.Errorf("failed to read config.json: %w", err)
		}

		defaultConfig := map[string]string{"primaryApiKey": "key"}
		newData, marshalErr := json.MarshalIndent(defaultConfig, "", "  ")
		if marshalErr != nil {
			return fmt.Errorf("failed to marshal default config.json: %w", marshalErr)
		}
		newData = append(newData, '\n')
		if writeErr := os.WriteFile(configPath, newData, 0644); writeErr != nil {
			return fmt.Errorf("failed to write config.json: %w", writeErr)
		}
		return nil
	}

	var config map[string]interface{}
	if err := json.Unmarshal(data, &config); err != nil {
		return nil
	}
	if _, ok := config["primaryApiKey"]; ok {
		return nil
	}

	config["primaryApiKey"] = "key"
	newData, marshalErr := json.MarshalIndent(config, "", "  ")
	if marshalErr != nil {
		return fmt.Errorf("failed to marshal updated config.json: %w", marshalErr)
	}
	newData = append(newData, '\n')
	if err := os.WriteFile(configPath, newData, 0644); err != nil {
		return fmt.Errorf("failed to write updated config.json: %w", err)
	}

	return nil
}

func (a *App) getDefaultClaudeSettings() string {
	settings, _ := a.GetSettings()
	apiKey := settings.APIKey
	if apiKey == "" {
		apiKey = "-"
	}
	proxyURL := fmt.Sprintf("http://127.0.0.1:%d", settings.Port)

	config := map[string]interface{}{
		"env": map[string]string{
			"ANTHROPIC_AUTH_TOKEN":                     apiKey,
			"ANTHROPIC_BASE_URL":                       proxyURL,
			"CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC": "1",
		},
		"permissions": map[string]interface{}{
			"allow": []string{"Bash(ls :*)"},
			"deny":  []string{},
		},
		"alwaysThinkingEnabled": true,
	}

	data, _ := json.MarshalIndent(config, "", "  ")
	return string(data)
}

func (a *App) getDefaultCodexConfig() string {
	settings, _ := a.GetSettings()
	proxyURL := fmt.Sprintf("http://127.0.0.1:%d/v1", settings.Port)

	return fmt.Sprintf(`disable_response_storage = true
model = "gpt-5.2"
model_provider = 'local'
model_reasoning_effort = "high"

[model_providers.local]
name = 'local'
base_url = '%s'
requires_openai_auth = true
wire_api = 'responses'`, proxyURL)
}

func (a *App) getDefaultCodexAuth() string {
	settings, _ := a.GetSettings()
	apiKey := settings.APIKey
	if apiKey == "" {
		apiKey = "-"
	}

	auth := map[string]string{
		"OPENAI_API_KEY": apiKey,
	}

	data, _ := json.MarshalIndent(auth, "", "  ")
	return string(data)
}

// =============================================================================
// WebDAV Sync Methods
// =============================================================================

// FullConfig represents the complete application configuration for backup/restore
type FullConfig struct {
	AppConfig map[string]interface{} `json:"appConfig,omitempty"`
	Vendors   []*VendorInfo          `json:"vendors"`
	Endpoints []*EndpointInfo        `json:"endpoints"`
}

// GetFullConfig returns the complete configuration for backup
func (a *App) GetFullConfig() (*FullConfig, error) {
	if a.storage == nil {
		return nil, fmt.Errorf("storage not initialized")
	}

	// Get app settings
	settings, err := a.GetSettings()
	if err != nil {
		return nil, fmt.Errorf("failed to get settings: %w", err)
	}

	// Get vendors
	vendors, err := a.GetVendors()
	if err != nil {
		return nil, fmt.Errorf("failed to get vendors: %w", err)
	}

	// Get all endpoints
	allEndpoints, err := a.storage.GetEndpoints()
	if err != nil {
		return nil, fmt.Errorf("failed to get endpoints: %w", err)
	}

	// Convert endpoints to frontend format
	endpoints := make([]*EndpointInfo, 0, len(allEndpoints))
	for _, ep := range allEndpoints {
		// Find vendor name
		var vendorName string
		for _, v := range vendors {
			if v.ID == ep.VendorID {
				vendorName = v.Name
				break
			}
		}

		endpoints = append(endpoints, &EndpointInfo{
			ID:            ep.ID,
			Name:          ep.Name,
			APIURL:        ep.APIURL,
			APIKey:        ep.APIKey,
			Active:        ep.Active,
			Enabled:       ep.Enabled,
			InterfaceType: ep.InterfaceType,
			VendorID:      ep.VendorID,
			VendorName:    vendorName,
			Model:         ep.Model,
			Remark:        ep.Remark,
			Priority:      ep.Priority,
		})
	}

	// Create appConfig map from settings
	appConfig := map[string]interface{}{
		"port":     settings.Port,
		"apiKey":   settings.APIKey,
		"fallback": settings.Fallback,
	}

	return &FullConfig{
		AppConfig: appConfig,
		Vendors:   vendors,
		Endpoints: endpoints,
	}, nil
}

// SaveFullConfig saves the complete configuration from backup
func (a *App) SaveFullConfig(config *FullConfig) error {
	if a.storage == nil {
		return fmt.Errorf("storage not initialized")
	}
	if config == nil {
		return fmt.Errorf("config is nil")
	}

	// Save app settings
	if config.AppConfig != nil {
		settings := &Settings{
			Port:     5600, // Default
			APIKey:   "",
			Fallback: false,
		}

		if port, ok := config.AppConfig["port"].(float64); ok {
			settings.Port = int(port)
		} else if port, ok := config.AppConfig["port"].(int); ok {
			settings.Port = port
		}

		if apiKey, ok := config.AppConfig["apiKey"].(string); ok {
			settings.APIKey = apiKey
		}

		if fallback, ok := config.AppConfig["fallback"].(bool); ok {
			settings.Fallback = fallback
		}

		if err := a.SaveSettings(settings); err != nil {
			return fmt.Errorf("failed to save settings: %w", err)
		}
	}

	// Clear existing vendors and endpoints
	// Note: We need to be careful here to avoid data loss
	// Instead, we'll overwrite by ID or add new ones

	// Save vendors
	if config.Vendors != nil {
		// First, get existing vendors to avoid duplicates
		existingVendors, err := a.storage.GetVendors()
		if err != nil {
			return fmt.Errorf("failed to get existing vendors: %w", err)
		}

		// Create a map of existing vendors by name for lookup
		existingVendorMap := make(map[string]*storage.Vendor)
		for _, v := range existingVendors {
			existingVendorMap[v.Name] = v
		}

		for _, v := range config.Vendors {
			// Check if vendor already exists
			if existing, exists := existingVendorMap[v.Name]; exists {
				// Update existing vendor
				existing.Name = v.Name
				existing.HomeURL = v.HomeURL
				existing.APIURL = v.APIURL
				existing.Remark = v.Remark
				if err := a.storage.SaveVendor(existing); err != nil {
					return fmt.Errorf("failed to update vendor %s: %w", v.Name, err)
				}
			} else {
				// Create new vendor
				newVendor := &storage.Vendor{
					Name:    v.Name,
					HomeURL: v.HomeURL,
					APIURL:  v.APIURL,
					Remark:  v.Remark,
				}
				if err := a.storage.SaveVendor(newVendor); err != nil {
					return fmt.Errorf("failed to save vendor %s: %w", v.Name, err)
				}
			}
		}
	}

	// Save endpoints
	if config.Endpoints != nil {
		// Get updated vendors to map names to IDs
		vendors, err := a.storage.GetVendors()
		if err != nil {
			return fmt.Errorf("failed to get vendors: %w", err)
		}

		vendorNameToID := make(map[string]int64)
		for _, v := range vendors {
			vendorNameToID[v.Name] = v.ID
		}

		// Get existing endpoints to avoid duplicates
		existingEndpoints, err := a.storage.GetEndpoints()
		if err != nil {
			return fmt.Errorf("failed to get existing endpoints: %w", err)
		}

		// Create a map for existing endpoints by name+vendor
		existingEndpointMap := make(map[string]*storage.Endpoint)
		for _, ep := range existingEndpoints {
			var vendorName string
			for _, v := range vendors {
				if v.ID == ep.VendorID {
					vendorName = v.Name
					break
				}
			}
			key := fmt.Sprintf("%s-%s", vendorName, ep.Name)
			existingEndpointMap[key] = ep
		}

		for _, ep := range config.Endpoints {
			vendorID, ok := vendorNameToID[ep.VendorName]
			if !ok {
				return fmt.Errorf("vendor not found: %s", ep.VendorName)
			}

			key := fmt.Sprintf("%s-%s", ep.VendorName, ep.Name)

			// Check if endpoint already exists
			if existing, exists := existingEndpointMap[key]; exists {
				// Update existing endpoint
				existing.Name = ep.Name
				existing.APIURL = ep.APIURL
				existing.APIKey = ep.APIKey
				existing.Active = ep.Active
				existing.Enabled = ep.Enabled
				existing.InterfaceType = ep.InterfaceType
				existing.VendorID = vendorID
				existing.Model = ep.Model
				existing.Remark = ep.Remark
				existing.Priority = ep.Priority
				if err := a.storage.UpdateEndpoint(existing); err != nil {
					return fmt.Errorf("failed to update endpoint %s: %w", ep.Name, err)
				}
			} else {
				// Create new endpoint
				newEndpoint := &storage.Endpoint{
					Name:          ep.Name,
					APIURL:        ep.APIURL,
					APIKey:        ep.APIKey,
					Active:        ep.Active,
					Enabled:       ep.Enabled,
					InterfaceType: ep.InterfaceType,
					VendorID:      vendorID,
					Model:         ep.Model,
					Remark:        ep.Remark,
					Priority:      ep.Priority,
				}
				if err := a.storage.SaveEndpoint(newEndpoint); err != nil {
					return fmt.Errorf("failed to save endpoint %s: %w", ep.Name, err)
				}
			}
		}
	}

	// Reload the router configuration
	if err := a.ReloadConfig(); err != nil {
		return fmt.Errorf("failed to reload config: %w", err)
	}

	return nil
}

// GetComputerName returns the computer name for backup identification
func (a *App) GetComputerName() (string, error) {
	hostname, err := os.Hostname()
	if err != nil {
		return "Unknown-Computer", nil
	}
	return hostname, nil
}

// =============================================================================
// WebDAV Proxy Methods
// =============================================================================

// webdavProxy is the singleton WebDAV proxy instance
var webdavProxy = proxy.NewWebDAVProxy()

// WebDAVConfigInput represents WebDAV configuration from frontend
type WebDAVConfigInput struct {
	ServerURL string `json:"serverUrl"`
	Username  string `json:"username"`
	Password  string `json:"password"`
}

// WebDAVRequestInput represents a WebDAV request from frontend
type WebDAVRequestInput struct {
	Config   WebDAVConfigInput `json:"config"`
	Method   string            `json:"method"`
	Path     string            `json:"path"`
	Body     string            `json:"body,omitempty"`
	Headers  map[string]string `json:"headers,omitempty"`
	DestPath string            `json:"destPath,omitempty"` // For MOVE/COPY operations
	Depth    string            `json:"depth,omitempty"`    // For PROPFIND operations
}

// WebDAVProxyRequest proxies a generic WebDAV request
func (a *App) WebDAVProxyRequest(input *WebDAVRequestInput) (*proxy.WebDAVResponse, error) {
	if input == nil {
		return nil, fmt.Errorf("input is required")
	}

	config := &proxy.WebDAVConfig{
		ServerURL: input.Config.ServerURL,
		Username:  input.Config.Username,
		Password:  input.Config.Password,
	}

	var body io.Reader
	if input.Body != "" {
		body = strings.NewReader(input.Body)
	}

	return webdavProxy.ProxyRequest(config, input.Method, input.Path, body, input.Headers)
}

// WebDAVList lists files and directories at the given path
func (a *App) WebDAVList(input *WebDAVRequestInput) (*proxy.WebDAVResponse, error) {
	if input == nil {
		return nil, fmt.Errorf("input is required")
	}

	config := &proxy.WebDAVConfig{
		ServerURL: input.Config.ServerURL,
		Username:  input.Config.Username,
		Password:  input.Config.Password,
	}

	return webdavProxy.List(config, input.Path, input.Depth)
}

// WebDAVGet retrieves a file from the WebDAV server
func (a *App) WebDAVGet(input *WebDAVRequestInput) (*proxy.WebDAVResponse, error) {
	if input == nil {
		return nil, fmt.Errorf("input is required")
	}

	config := &proxy.WebDAVConfig{
		ServerURL: input.Config.ServerURL,
		Username:  input.Config.Username,
		Password:  input.Config.Password,
	}

	return webdavProxy.Get(config, input.Path)
}

// WebDAVPut uploads a file to the WebDAV server
func (a *App) WebDAVPut(input *WebDAVRequestInput) (*proxy.WebDAVResponse, error) {
	if input == nil {
		return nil, fmt.Errorf("input is required")
	}

	config := &proxy.WebDAVConfig{
		ServerURL: input.Config.ServerURL,
		Username:  input.Config.Username,
		Password:  input.Config.Password,
	}

	return webdavProxy.Put(config, input.Path, input.Body)
}

// WebDAVDelete removes a file or directory from the WebDAV server
func (a *App) WebDAVDelete(input *WebDAVRequestInput) (*proxy.WebDAVResponse, error) {
	if input == nil {
		return nil, fmt.Errorf("input is required")
	}

	config := &proxy.WebDAVConfig{
		ServerURL: input.Config.ServerURL,
		Username:  input.Config.Username,
		Password:  input.Config.Password,
	}

	return webdavProxy.Delete(config, input.Path)
}

// WebDAVMkcol creates a new directory on the WebDAV server
func (a *App) WebDAVMkcol(input *WebDAVRequestInput) (*proxy.WebDAVResponse, error) {
	if input == nil {
		return nil, fmt.Errorf("input is required")
	}

	config := &proxy.WebDAVConfig{
		ServerURL: input.Config.ServerURL,
		Username:  input.Config.Username,
		Password:  input.Config.Password,
	}

	return webdavProxy.Mkcol(config, input.Path)
}

// WebDAVMove moves/renames a file or directory on the WebDAV server
func (a *App) WebDAVMove(input *WebDAVRequestInput) (*proxy.WebDAVResponse, error) {
	if input == nil {
		return nil, fmt.Errorf("input is required")
	}

	config := &proxy.WebDAVConfig{
		ServerURL: input.Config.ServerURL,
		Username:  input.Config.Username,
		Password:  input.Config.Password,
	}

	return webdavProxy.Move(config, input.Path, input.DestPath)
}

// WebDAVCopy copies a file or directory on the WebDAV server
func (a *App) WebDAVCopy(input *WebDAVRequestInput) (*proxy.WebDAVResponse, error) {
	if input == nil {
		return nil, fmt.Errorf("input is required")
	}

	config := &proxy.WebDAVConfig{
		ServerURL: input.Config.ServerURL,
		Username:  input.Config.Username,
		Password:  input.Config.Password,
	}

	return webdavProxy.Copy(config, input.Path, input.DestPath)
}

// =============================================================================
// Network Utility Methods
// =============================================================================

// LocalIPInfo represents a local IP address with its interface name
type LocalIPInfo struct {
	IP        string `json:"ip"`
	Interface string `json:"interface"`
	IsIPv4    bool   `json:"isIPv4"`
}

// GetLocalIPs returns all local IP addresses of the machine
func (a *App) GetLocalIPs() ([]*LocalIPInfo, error) {
	var result []*LocalIPInfo

	// Always include localhost first
	result = append(result, &LocalIPInfo{
		IP:        "127.0.0.1",
		Interface: "localhost",
		IsIPv4:    true,
	})

	interfaces, err := net.Interfaces()
	if err != nil {
		return result, nil // Return at least localhost
	}

	for _, iface := range interfaces {
		// Skip loopback and down interfaces
		if iface.Flags&net.FlagLoopback != 0 || iface.Flags&net.FlagUp == 0 {
			continue
		}

		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}

		for _, addr := range addrs {
			var ip net.IP
			switch v := addr.(type) {
			case *net.IPNet:
				ip = v.IP
			case *net.IPAddr:
				ip = v.IP
			}

			if ip == nil || ip.IsLoopback() {
				continue
			}

			// Check if IPv4
			if ipv4 := ip.To4(); ipv4 != nil {
				result = append(result, &LocalIPInfo{
					IP:        ipv4.String(),
					Interface: iface.Name,
					IsIPv4:    true,
				})
			}
		}
	}

	return result, nil
}
