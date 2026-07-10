package main

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha1"
	"encoding/base64"
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
	"sync"
	"time"

	"clisimplehub/internal/appdb"
	codexShared "clisimplehub/internal/codex/shared"
	"clisimplehub/internal/config"
	"clisimplehub/internal/dbconfig"
	"clisimplehub/internal/plugin"
	"clisimplehub/internal/proxy"
	"clisimplehub/internal/statsdb"
	"clisimplehub/internal/storage"
	"clisimplehub/internal/transformer"

	"github.com/google/uuid"
	wailsRuntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

const (
	configReloadedEventName     = "config:reloaded"
	proxyStatusChangedEventName = "proxy:status-changed"
)

// Settings represents the application settings exposed to frontend
type Settings struct {
	Port                   int    `json:"port"`
	APIKey                 string `json:"apiKey"`
	ProxyURL               string `json:"proxyUrl,omitempty"`
	ClashPath              string `json:"clashPath,omitempty"`
	DBSource               string `json:"dbSource,omitempty"`
	Fallback               bool   `json:"fallback"`
	DebugMode              string `json:"debugMode,omitempty"`
	ListenAddr             string `json:"listenAddr,omitempty"`
	DisableImageGeneration string `json:"disableImageGeneration,omitempty"`
}

type DatabaseTestInput struct {
	DBSource string `json:"dbSource"`
}

type DatabaseTestResult struct {
	Message  string `json:"message,omitempty"`
	DBDriver string `json:"dbDriver,omitempty"`
	DBSource string `json:"dbSource,omitempty"`
}

type DatabaseApplyInput struct {
	DBSource string `json:"dbSource"`
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
	ProviderName  string                 `json:"providerName,omitempty"`
	Model         string                 `json:"model,omitempty"`
	Transformer   string                 `json:"transformer,omitempty"`
	ProxyURL      string                 `json:"proxyUrl,omitempty"`
	Models        []storage.ModelMapping `json:"models,omitempty"`
	Routes        []string               `json:"routes,omitempty"`
	Headers       map[string]string      `json:"headers,omitempty"`
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
	sseHub       *proxy.SSEHub
	configLoader *config.ConfigLoader
	usageStats   statsdb.UsageStatsStore
	logBridge    *goLogBridge

	reloadMu sync.Mutex

	proxyStatusMu  sync.RWMutex
	lastProxyError string

	kiroSignServer  *http.Server
	kiroSignMu      sync.Mutex
	kiroSignTimeout *time.Timer

	kiroSignIdcServer  *http.Server
	kiroSignIdcMu      sync.Mutex
	kiroSignIdcTimeout *time.Timer
}

// NewApp creates a new App application struct
func NewApp() *App {
	return &App{
		logBridge: newGoLogBridge(goLogEventName),
	}
}

// startup is called when the app starts. The context is saved
// so we can call the runtime methods
func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
	if a.logBridge != nil {
		a.logBridge.SetContext(ctx)
	}
}

func (a *App) GoLogWriter() io.Writer {
	if a == nil || a.logBridge == nil {
		return io.Discard
	}
	return a.logBridge
}

func normalizeProxyListenAddr(addr string) string {
	addr = strings.TrimSpace(addr)
	if addr == "" {
		return "0.0.0.0"
	}
	return addr
}

func (a *App) buildProxyStartError(listenAddr string, port int, cause error) error {
	return fmt.Errorf("代理服务无法监听 %s:%d: %w", normalizeProxyListenAddr(listenAddr), port, cause)
}

func (a *App) getLastProxyError() string {
	a.proxyStatusMu.RLock()
	defer a.proxyStatusMu.RUnlock()
	return a.lastProxyError
}

func (a *App) setLastProxyError(message string) {
	message = strings.TrimSpace(message)

	a.proxyStatusMu.Lock()
	if a.lastProxyError == message {
		a.proxyStatusMu.Unlock()
		return
	}
	a.lastProxyError = message
	a.proxyStatusMu.Unlock()

	a.emitProxyStatusChanged()
}

func (a *App) clearLastProxyError() {
	a.setLastProxyError("")
}

func (a *App) emitProxyStatusChanged() {
	if a == nil || a.ctx == nil {
		return
	}
	wailsRuntime.EventsEmit(a.ctx, proxyStatusChangedEventName, a.GetProxyStatus())
}

func (a *App) prepareProxyStart(port int, listenAddr string, checkPort bool) error {
	if !checkPort {
		a.clearLastProxyError()
		return nil
	}

	if err := config.IsListenAddrPortAvailable(normalizeProxyListenAddr(listenAddr), port); err != nil {
		wrapped := a.buildProxyStartError(listenAddr, port, err)
		a.setLastProxyError(wrapped.Error())
		return wrapped
	}

	a.clearLastProxyError()
	return nil
}

func (a *App) launchProxyServerAsync(port int, listenAddr string) {
	if a == nil || a.proxyServer == nil {
		return
	}

	time.AfterFunc(150*time.Millisecond, func() {
		a.emitProxyStatusChanged()
	})

	go func() {
		if err := a.proxyServer.Start(); err != nil {
			wrapped := a.buildProxyStartError(listenAddr, port, err)
			fmt.Printf("Proxy server error: %v\n", wrapped)
			a.setLastProxyError(wrapped.Error())
			return
		}

		a.clearLastProxyError()
		a.emitProxyStatusChanged()
	}()
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

// SetSSEHub sets the SSE hub instance for the app
func (a *App) SetSSEHub(hub *proxy.SSEHub) {
	a.sseHub = hub
}

// SetUsageStats sets the usage stats store instance for the app
func (a *App) SetUsageStats(store *statsdb.SQLiteUsageStatsStore) {
	a.usageStats = store
}

func (a *App) setUsageStatsStore(store statsdb.UsageStatsStore) {
	a.usageStats = store
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
		Port:                   5600, // Default port
		APIKey:                 "",
		ProxyURL:               "",
		ClashPath:              "",
		DBSource:               "",
		Fallback:               false, // Default fallback disabled
		DebugMode:              "",
		ListenAddr:             "0.0.0.0",
		DisableImageGeneration: "passthrough",
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

	proxyURL, err := a.storage.GetConfig(ConfigKeyProxyURL)
	if err == nil {
		settings.ProxyURL = strings.TrimSpace(proxyURL)
	}

	clashPath, err := a.storage.GetConfig(ConfigKeyClashPath)
	if err == nil {
		settings.ClashPath = strings.TrimSpace(clashPath)
	}

	dbSource, err := a.storage.GetConfig(ConfigKeyDBSource)
	if err == nil {
		settings.DBSource = strings.TrimSpace(dbSource)
	}

	// Get fallback setting from storage
	fallbackStr, err := a.storage.GetConfig(ConfigKeyFallback)
	if err == nil && fallbackStr == "true" {
		settings.Fallback = true
	}

	// Get debug mode from storage (e.g., "all")
	debugMode, err := a.storage.GetConfig(ConfigKeyDebugMode)
	if err == nil {
		settings.DebugMode = debugMode
	}

	// Get listen address from storage
	listenAddr, err := a.storage.GetConfig(ConfigKeyListenAddr)
	if err == nil && listenAddr != "" {
		settings.ListenAddr = listenAddr
	}

	// Get disable-image-generation 配置（默认 passthrough）
	if v, err := a.storage.GetConfig(ConfigKeyDisableImageGeneration); err == nil && strings.TrimSpace(v) != "" {
		settings.DisableImageGeneration = strings.TrimSpace(v)
	}

	return settings, nil
}

// SaveSettings saves the application settings
func (a *App) SaveSettings(settings *Settings) error {
	if a.storage == nil {
		return fmt.Errorf("storage not initialized")
	}
	if settings == nil {
		return fmt.Errorf("settings is nil")
	}

	oldPort := 0
	oldListenAddr := ""
	if a.proxyServer != nil {
		oldPort = a.proxyServer.GetPort()
		oldListenAddr = strings.TrimSpace(a.proxyServer.GetListenAddr())
	}
	normalizedAPIKey := strings.TrimSpace(settings.APIKey)
	normalizedProxyURL := strings.TrimSpace(settings.ProxyURL)
	normalizedClashPath := strings.TrimSpace(settings.ClashPath)
	normalizedListenAddr := strings.TrimSpace(settings.ListenAddr)
	if normalizedListenAddr == "" {
		normalizedListenAddr = "0.0.0.0"
	}

	// Validate port
	if err := config.ValidatePort(settings.Port); err != nil {
		return fmt.Errorf("invalid port: %w", err)
	}
	if err := config.ValidateListenAddr(normalizedListenAddr); err != nil {
		return fmt.Errorf("invalid listen address: %w", err)
	}
	if oldPort != 0 && oldPort != settings.Port {
		if err := a.prepareProxyStart(settings.Port, normalizedListenAddr, true); err != nil {
			return err
		}
	}

	// Save port to storage
	if err := a.storage.SetConfig(ConfigKeyPort, strconv.Itoa(settings.Port)); err != nil {
		return fmt.Errorf("failed to save port: %w", err)
	}

	// Save proxy auth token to storage (empty => no auth)
	if err := a.storage.SetConfig(ConfigKeyAPIKey, normalizedAPIKey); err != nil {
		return fmt.Errorf("failed to save api key: %w", err)
	}

	if err := a.storage.SetConfig(ConfigKeyProxyURL, normalizedProxyURL); err != nil {
		return fmt.Errorf("failed to save proxy url: %w", err)
	}

	if err := a.storage.SetConfig(ConfigKeyClashPath, normalizedClashPath); err != nil {
		return fmt.Errorf("failed to save clash path: %w", err)
	}

	// Save fallback setting to storage as bool
	if err := a.storage.SetConfigBool(ConfigKeyFallback, settings.Fallback); err != nil {
		return fmt.Errorf("failed to save fallback setting: %w", err)
	}

	// Save debug mode to storage (empty will remove key)
	if err := a.storage.SetConfig(ConfigKeyDebugMode, strings.TrimSpace(settings.DebugMode)); err != nil {
		return fmt.Errorf("failed to save debug mode: %w", err)
	}

	// Save disable-image-generation 配置
	imageGenMode := strings.TrimSpace(settings.DisableImageGeneration)
	if imageGenMode == "" {
		imageGenMode = "passthrough"
	}
	if err := a.storage.SetConfig(ConfigKeyDisableImageGeneration, imageGenMode); err != nil {
		return fmt.Errorf("failed to save disable-image-generation: %w", err)
	}

	// Save listen address to storage
	if err := a.storage.SetConfig(ConfigKeyListenAddr, normalizedListenAddr); err != nil {
		return fmt.Errorf("failed to save listen address: %w", err)
	}

	// Update proxy server port if available
	if a.proxyServer != nil {
		a.proxyServer.SetPort(settings.Port)
		a.proxyServer.SetAuthKey(normalizedAPIKey)
		a.proxyServer.SetFallbackEnabled(settings.Fallback)
		a.proxyServer.SetListenAddr(normalizedListenAddr)
		// 热更新调试日志配置
		a.proxyServer.UpdateDebugFileLogger()

		// 端口 / 监听地址变化需要重启代理服务才能生效
		if (oldPort != 0 && oldPort != settings.Port) || (oldListenAddr != "" && oldListenAddr != normalizedListenAddr) {
			a.restartProxyServerAsync()
		}
	}

	return nil
}

func (a *App) ApplyDatabaseConfig(input DatabaseApplyInput) (*DatabaseTestResult, error) {
	if a.storage == nil {
		return nil, fmt.Errorf("storage not initialized")
	}

	cfg, source, err := a.resolveDatabaseInput(input.DBSource)
	if err != nil {
		return nil, err
	}

	if err := a.testDatabaseConfig(cfg); err != nil {
		return nil, err
	}

	if a.proxyServer != nil {
		if err := appdb.ApplyRuntimeDatabase(cfg, a.proxyServer); err != nil {
			return nil, fmt.Errorf("database apply failed: %w", err)
		}
		a.setUsageStatsStore(a.proxyServer.GetUsageStatsStore())
	}

	if err := a.storage.SetConfig(ConfigKeyDBDriver, cfg.Driver); err != nil {
		return nil, fmt.Errorf("failed to save database driver: %w", err)
	}
	if err := a.storage.SetConfig(ConfigKeyDBSource, source); err != nil {
		return nil, fmt.Errorf("failed to save database source: %w", err)
	}

	return &DatabaseTestResult{
		Message:  "database config applied",
		DBDriver: cfg.Driver,
		DBSource: dbconfig.DisplaySource(cfg),
	}, nil
}

func (a *App) TestDatabaseConnection(input DatabaseTestInput) (*DatabaseTestResult, error) {
	cfg, _, err := a.resolveDatabaseInput(input.DBSource)
	if err != nil {
		return nil, err
	}

	if err := a.testDatabaseConfig(cfg); err != nil {
		return nil, err
	}

	return &DatabaseTestResult{
		Message:  "database connection ok",
		DBDriver: cfg.Driver,
		DBSource: dbconfig.DisplaySource(cfg),
	}, nil
}

func (a *App) resolveDatabaseInput(source string) (dbconfig.Config, string, error) {
	normalizedSource := strings.TrimSpace(source)
	cfg, err := dbconfig.ResolveSource(a.GetConfigPath(), normalizedSource)
	if err != nil {
		return dbconfig.Config{}, "", err
	}
	if cfg.Driver == dbconfig.DriverSQLite && normalizedSource == "" {
		normalizedSource = ""
	}
	return cfg, normalizedSource, nil
}

func (a *App) testDatabaseConfig(cfg dbconfig.Config) error {
	usageStore, err := statsdb.OpenUsageStatsStore(cfg)
	if err != nil {
		return fmt.Errorf("open usage stats store: %w", err)
	}
	defer usageStore.Close()

	accountStore, err := codexShared.OpenCodexAccountStoreWithConfig(cfg)
	if err != nil {
		return fmt.Errorf("open codex account store: %w", err)
	}
	defer accountStore.Close()
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

	oldPort := 0
	if a.proxyServer != nil {
		oldPort = a.proxyServer.GetPort()
	}
	if oldPort != 0 && oldPort != port {
		listenAddr := ""
		if a.proxyServer != nil {
			listenAddr = a.proxyServer.GetListenAddr()
		}
		if err := a.prepareProxyStart(port, listenAddr, true); err != nil {
			return err
		}
	}

	// Save to storage
	// Requirements: 1.3
	if err := a.storage.SetConfig(ConfigKeyPort, strconv.Itoa(port)); err != nil {
		return fmt.Errorf("failed to save port: %w", err)
	}

	// Update proxy server
	if a.proxyServer != nil {
		a.proxyServer.SetPort(port)
		if oldPort != 0 && oldPort != port {
			a.restartProxyServerAsync()
		}
	}

	return nil
}

// SaveListenAddr saves the listen address to config
func (a *App) SaveListenAddr(addr string) error {
	if a.storage == nil {
		return fmt.Errorf("storage not initialized")
	}

	// Validate listen address
	if err := config.ValidateListenAddr(addr); err != nil {
		return fmt.Errorf("invalid listen address: %w", err)
	}

	// Save to storage
	if err := a.storage.SetConfig(ConfigKeyListenAddr, addr); err != nil {
		return fmt.Errorf("failed to save listen address: %w", err)
	}

	// Update proxy server
	if a.proxyServer != nil {
		oldAddr := a.proxyServer.GetListenAddr()
		a.proxyServer.SetListenAddr(addr)
		// Listen address change requires restart
		if oldAddr != "" && oldAddr != addr {
			a.restartProxyServerAsync()
		}
	}

	return nil
}

func (a *App) restartProxyServerAsync() {
	if a.proxyServer == nil {
		return
	}

	go func() {
		port := a.proxyServer.GetPort()
		listenAddr := normalizeProxyListenAddr(a.proxyServer.GetListenAddr())
		fmt.Printf("Restarting proxy server on %s:%d...\n", listenAddr, port)

		// 先停止旧服务器，等待其完全关闭
		if err := a.proxyServer.Stop(); err != nil {
			fmt.Printf("Failed to stop proxy server: %v\n", err)
			a.setLastProxyError(err.Error())
			return
		}
		a.emitProxyStatusChanged()

		// 短暂延迟确保端口完全释放
		time.Sleep(100 * time.Millisecond)

		if err := a.prepareProxyStart(port, listenAddr, true); err != nil {
			fmt.Printf("Failed to start proxy server: %v\n", err)
			return
		}

		// 启动新服务器
		fmt.Printf("Starting proxy server on %s:%d...\n", listenAddr, port)
		a.launchProxyServerAsync(port, listenAddr)
	}()
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
	if a.usageStats != nil {
		todayStats, _ = a.usageStats.GetTodayStatsByEndpoints(a.ctx)
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
			APIKey:        ep.APIKey,
			Active:        activeEndpointID != 0 && ep.ID == activeEndpointID,
			Enabled:       enabled,
			InterfaceType: ep.InterfaceType,
			ProviderName:  ep.ProviderName,
			Model:         ep.Model,
			Transformer:   ep.Transformer,
			ProxyURL:      ep.ProxyURL,
			Models:        ep.Models,
			Routes:        ep.Routes,
			Headers:       ep.Headers,
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
		ProviderName:  ep.ProviderName,
		Model:         ep.Model,
		Headers:       ep.Headers,
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
			ProviderName:  ep.ProviderName,
			Model:         ep.Model,
			Transformer:   ep.Transformer,
			Headers:       ep.Headers,
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

// GetEndpointByID returns a single endpoint by ID
func (a *App) GetEndpointByID(endpointID int64) (*EndpointInfo, error) {
	if a.storage == nil {
		return nil, fmt.Errorf("storage not initialized")
	}

	ep, err := a.storage.GetEndpointByID(endpointID)
	if err != nil {
		return nil, fmt.Errorf("failed to get endpoint: %w", err)
	}

	info := &EndpointInfo{
		ID:            ep.ID,
		Name:          ep.Name,
		APIURL:        ep.APIURL,
		APIKey:        ep.APIKey,
		Active:        ep.Active,
		Enabled:       ep.Enabled,
		InterfaceType: ep.InterfaceType,
		ProviderName:  ep.ProviderName,
		Model:         ep.Model,
		Transformer:   ep.Transformer,
		ProxyURL:      ep.ProxyURL,
		Models:        ep.Models,
		Routes:        ep.Routes,
		Headers:       ep.Headers,
		Remark:        ep.Remark,
		Priority:      ep.Priority,
	}

	return info, nil
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
	ProviderName  string `json:"providerName"`
	EndpointName  string `json:"endpointName"`
	Path          string `json:"path"`
	Model         string `json:"model"`
	RunTime       int64  `json:"runTime"` // milliseconds
	Status        string `json:"status"`
	Timestamp     string `json:"timestamp"`
}

// TokenStatsInfo represents token statistics for frontend display
// Requirements: 8.1, 8.2
type TokenStatsInfo struct {
	EndpointName string `json:"endpointName"`
	ProviderName string `json:"providerName"`
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

	// Get recent logs (max 10)
	logs := stats.GetRecentLogs(10)

	result := make([]*RequestLogInfo, 0, len(logs))
	for _, log := range logs {
		info := &RequestLogInfo{
			ID:            log.ID,
			InterfaceType: log.InterfaceType,
			ProviderName:  log.ProviderName,
			EndpointName:  log.EndpointName,
			Path:          log.Path,
			Model:         log.Model,
			RunTime:       log.RunTime,
			Status:        log.Status,
			Timestamp:     log.Timestamp.Format(time.RFC3339),
		}
		result = append(result, info)
	}

	return result, nil
}

// RequestLogDetailInfo represents detailed request log for frontend display
type RequestLogDetailInfo struct {
	ID             string            `json:"id"`
	InterfaceType  string            `json:"interfaceType"`
	ProviderName   string            `json:"providerName"`
	EndpointName   string            `json:"endpointName"`
	Path           string            `json:"path"`
	Model          string            `json:"model"`
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
	logs := stats.GetRecentLogs(10)
	for _, log := range logs {
		if log.ID == logID {
			return &RequestLogDetailInfo{
				ID:             log.ID,
				InterfaceType:  log.InterfaceType,
				ProviderName:   log.ProviderName,
				EndpointName:   log.EndpointName,
				Path:           log.Path,
				Model:          log.Model,
				RunTime:        log.RunTime,
				Status:         log.Status,
				Timestamp:      log.Timestamp.Format(time.RFC3339),
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
			ProviderName: ts.ProviderName,
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
		ProviderName: ts.ProviderName,
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
	if a.proxyServer.IsRunning() {
		a.clearLastProxyError()
		a.emitProxyStatusChanged()
		return nil
	}

	port := a.proxyServer.GetPort()
	listenAddr := normalizeProxyListenAddr(a.proxyServer.GetListenAddr())
	if err := a.prepareProxyStart(port, listenAddr, true); err != nil {
		return err
	}

	// Start in a goroutine since Start() blocks
	a.launchProxyServerAsync(port, listenAddr)

	return nil
}

// StopProxy stops the proxy server
func (a *App) StopProxy() error {
	if a.proxyServer == nil {
		return fmt.Errorf("proxy server not initialized")
	}
	if err := a.proxyServer.Stop(); err != nil {
		return err
	}
	a.clearLastProxyError()
	a.emitProxyStatusChanged()
	return nil
}

// GetProxyStatus returns the current proxy status
func (a *App) GetProxyStatus() map[string]interface{} {
	status := map[string]interface{}{
		"running":    false,
		"port":       0,
		"listenAddr": "",
		"lastError":  a.getLastProxyError(),
	}

	if a.proxyServer != nil {
		status["port"] = a.proxyServer.GetPort()
		status["running"] = a.proxyServer.IsRunning()
		status["listenAddr"] = normalizeProxyListenAddr(a.proxyServer.GetListenAddr())
		if a.proxyServer.IsRunning() {
			status["lastError"] = ""
		}
	}

	return status
}

// ReloadConfig reloads configuration from the config file
func (a *App) ReloadConfig() error {
	if a.storage == nil {
		return fmt.Errorf("storage not initialized")
	}

	a.reloadMu.Lock()
	defer a.reloadMu.Unlock()

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

	// Reload plugins so their in-memory state reflects the latest config + synced plugin payloads.
	for _, pl := range plugin.All() {
		if err := pl.Reload(); err != nil {
			fmt.Printf("warning: plugin %s reload failed: %v\n", pl.Name(), err)
		}
	}

	// Notify frontend to refresh stores (endpoints/kiro/codex/clash) after external sync or manual reload.
	if a.ctx != nil {
		wailsRuntime.EventsEmit(a.ctx, configReloadedEventName, map[string]any{
			"at": time.Now().Format(time.RFC3339Nano),
		})
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

// EndpointInput represents endpoint input from frontend
type EndpointInput struct {
	ID             int64                  `json:"id"`
	Name           string                 `json:"name"`
	APIURL         string                 `json:"apiUrl"`
	APIKey         string                 `json:"apiKey"`
	Active         bool                   `json:"active"`
	Enabled        bool                   `json:"enabled"`
	InterfaceType  string                 `json:"interfaceType"`
	ProviderName   string                 `json:"providerName,omitempty"`
	Model          string                 `json:"model,omitempty"`
	Transformer    string                 `json:"transformer,omitempty"`
	TransformerSet bool                   `json:"transformerSet,omitempty"`
	ProxyURL       string                 `json:"proxyUrl,omitempty"`
	ProxyURLSet    bool                   `json:"proxyUrlSet,omitempty"`
	Models         []storage.ModelMapping `json:"models,omitempty"`
	ModelsSet      bool                   `json:"modelsSet,omitempty"`
	Routes         []string               `json:"routes,omitempty"`
	RoutesSet      bool                   `json:"routesSet,omitempty"`
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
		ProviderName:  endpoint.ProviderName,
		Model:         endpoint.Model,
		Transformer:   endpoint.Transformer,
		ProxyURL:      endpoint.ProxyURL,
		Models:        endpoint.Models,
		Routes:        endpoint.Routes,
		Remark:        endpoint.Remark,
		Priority:      priority,
	}
	if existing != nil {
		// Active 由 SetActiveEndpoint 管理；编辑端点时应保留当前 active 状态，避免表单保存意外取消活动端点。
		ep.Active = existing.Active

		// transformer 支持显式清空：前端会发送 transformerSet=true，
		// 只有当旧客户端未发送该字段时才走“空值保留”逻辑，避免误清空。
		if !endpoint.TransformerSet && ep.Transformer == "" {
			ep.Transformer = existing.Transformer
		}
		// proxyUrl 支持显式清空：前端会发送 proxyUrlSet=true，
		// 只有当旧客户端未发送该字段时才走“空值保留”逻辑，避免误清空。
		if !endpoint.ProxyURLSet && ep.ProxyURL == "" {
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
		if !endpoint.RoutesSet && ep.Routes == nil {
			ep.Routes = existing.Routes
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
		ProviderName:  ep.ProviderName,
		Model:         ep.Model,
		Routes:        ep.Routes,
		Headers:       ep.Headers,
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

	// Also cleanup usage_stats logs for this endpoint.
	if a.usageStats != nil {
		deleteCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if err := a.usageStats.DeleteStatsByEndpointID(deleteCtx, id); err != nil {
			return err
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

// GetSSEURL returns the SSE URL for real-time updates
func (a *App) GetSSEURL() string {
	port := 5600
	if a.proxyServer != nil {
		port = a.proxyServer.GetPort()
	}
	return fmt.Sprintf("http://localhost:%d/sse", port)
}

// =============================================================================
// SQLite Token Statistics Methods (New Design)
// =============================================================================

// ProviderStatsSummaryInfo represents aggregated stats for a provider (frontend)
type ProviderStatsSummaryInfo struct {
	ProviderName string                     `json:"providerName"`
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
	ProviderName string `json:"providerName"`
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

// GetTokenStatsByTimeRange returns token statistics grouped by provider for the given time range
func (a *App) GetTokenStatsByTimeRange(timeRange string) ([]*ProviderStatsSummaryInfo, error) {
	if a.usageStats == nil {
		return []*ProviderStatsSummaryInfo{}, nil
	}

	tr := statsdb.TimeRange(timeRange)
	stats, err := a.usageStats.GetStatsByTimeRange(a.ctx, tr)
	if err != nil {
		return nil, fmt.Errorf("failed to get stats: %w", err)
	}

	result := make([]*ProviderStatsSummaryInfo, 0, len(stats))
	for _, s := range stats {
		endpoints := make([]EndpointStatsSummaryInfo, 0, len(s.Endpoints))
		for _, ep := range s.Endpoints {
			endpoints = append(endpoints, EndpointStatsSummaryInfo{
				EndpointID:   ep.EndpointID,
				EndpointName: ep.EndpointName,
				ProviderName: ep.ProviderName,
				InputTokens:  ep.InputTokens,
				OutputTokens: ep.OutputTokens,
				CachedCreate: ep.CachedCreate,
				CachedRead:   ep.CachedRead,
				Reasoning:    ep.Reasoning,
				Total:        ep.Total,
			})
		}
		result = append(result, &ProviderStatsSummaryInfo{
			ProviderName: s.ProviderName,
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
	if a.usageStats == nil {
		return []*InterfaceTypeStatsSummaryInfo{}, nil
	}

	tr := statsdb.TimeRange(timeRange)
	stats, err := a.usageStats.GetStatsByInterfaceType(a.ctx, tr)
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
				ProviderName: ep.ProviderName,
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

	if a.usageStats == nil {
		fmt.Println("[ClearTokenStats] Error: usage stats store not initialized")
		return fmt.Errorf("usage stats store not initialized")
	}

	tr := statsdb.TimeRange(timeRange)
	fmt.Printf("[ClearTokenStats] Calling ClearStats with TimeRange: %s\n", tr)

	if err := a.usageStats.ClearStats(a.ctx, tr); err != nil {
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
		apiPath = "/responses"
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
			"stream": true,
			"store":  false,
			"reasoning": map[string]interface{}{
				"effort": "medium",
			},
		}
		if effort := strings.TrimSpace(reasoning); effort != "" {
			body["reasoning"] = map[string]interface{}{"effort": effort}
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
		req.Header.Set("connection", "close")
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
		req.Header.Set("connection", "close")
		req.Header.Set("content-type", "application/json")
		req.Header.Set("conversation_id", sessionID)
		req.Header.Set("openai-beta", "responses=experimental")
		req.Header.Set("originator", codexShared.DefaultCodexOriginator)
		req.Header.Set("session_id", sessionID)
		req.Header.Set("user-agent", codexShared.DefaultCodexUserAgent)
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

func buildModelsURL(apiURL, versionPath string, query url.Values) (string, error) {
	parsedURL, err := url.Parse(apiURL)
	if err != nil {
		return "", fmt.Errorf("invalid api url: %w", err)
	}

	versionPath = "/" + strings.Trim(strings.TrimSuffix(versionPath, "/"), "/")
	modelsPath := versionPath + "/models"
	currentPath := strings.TrimSuffix(parsedURL.Path, "/")

	switch {
	case currentPath == "":
		parsedURL.Path = modelsPath
	case strings.HasSuffix(currentPath, modelsPath):
		parsedURL.Path = currentPath
	case strings.HasSuffix(currentPath, versionPath):
		parsedURL.Path = currentPath + "/models"
	default:
		parsedURL.Path = currentPath + modelsPath
	}

	if len(query) > 0 {
		values := parsedURL.Query()
		for key, items := range query {
			if len(items) == 0 {
				continue
			}
			values.Del(key)
			for _, item := range items {
				values.Add(key, item)
			}
		}
		parsedURL.RawQuery = values.Encode()
	}

	return parsedURL.String(), nil
}

// fetchOpenAIModels fetches models from OpenAI-compatible API
func (a *App) fetchOpenAIModels(apiURL, apiKey string) ([]string, error) {
	url, err := buildModelsURL(apiURL, "/v1", nil)
	if err != nil {
		return nil, err
	}

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
	url, err := buildModelsURL(apiURL, "/v1beta", url.Values{
		"key": []string{apiKey},
	})
	if err != nil {
		return nil, err
	}

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

// ProcessCodexConfigResult represents the result of processing Codex config
type ProcessCodexConfigResult struct {
	ConfigToml string `json:"configToml"`
	AuthJson   string `json:"authJson"`
}

func isCodexChatGPTAuth(auth map[string]interface{}) bool {
	authMode, ok := auth["auth_mode"].(string)
	return ok && strings.EqualFold(strings.TrimSpace(authMode), "chatgpt")
}

func setCodexOpenAIAPIKey(auth map[string]interface{}, apiKey string) {
	if isCodexChatGPTAuth(auth) {
		auth["OPENAI_API_KEY"] = nil
		return
	}
	auth["OPENAI_API_KEY"] = apiKey
}

func tomlLineKey(line string) string {
	key, _, found := strings.Cut(strings.TrimSpace(line), "=")
	if !found {
		return ""
	}
	return strings.TrimSpace(key)
}

func updateCodexLocalProviderConfig(configToml, baseURL, experimentalBearerToken string) string {
	lines := strings.Split(configToml, "\n")
	newLines := make([]string, 0, len(lines)+3)
	inLocalProvider := false
	localProviderFound := false
	baseURLUpdated := false
	tokenUpdated := false

	appendMissingFields := func() {
		if !baseURLUpdated {
			newLines = append(newLines, fmt.Sprintf("base_url = '%s'", baseURL))
			baseURLUpdated = true
		}
		if experimentalBearerToken != "" && !tokenUpdated {
			newLines = append(newLines, fmt.Sprintf("experimental_bearer_token = %s", strconv.Quote(experimentalBearerToken)))
			tokenUpdated = true
		}
	}

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		isSection := strings.HasPrefix(trimmed, "[")

		if isSection && inLocalProvider && !strings.HasPrefix(trimmed, "[model_providers.shub]") {
			appendMissingFields()
			inLocalProvider = false
		}

		if strings.HasPrefix(trimmed, "[model_providers.shub]") {
			inLocalProvider = true
			localProviderFound = true
		}

		switch {
		case inLocalProvider && tomlLineKey(line) == "base_url":
			newLines = append(newLines, fmt.Sprintf("base_url = '%s'", baseURL))
			baseURLUpdated = true
		case inLocalProvider && experimentalBearerToken != "" && tomlLineKey(line) == "experimental_bearer_token":
			newLines = append(newLines, fmt.Sprintf("experimental_bearer_token = %s", strconv.Quote(experimentalBearerToken)))
			tokenUpdated = true
		default:
			newLines = append(newLines, line)
		}
	}

	if inLocalProvider {
		appendMissingFields()
	}

	if !localProviderFound {
		if len(newLines) > 0 && strings.TrimSpace(newLines[len(newLines)-1]) != "" {
			newLines = append(newLines, "")
		}
		newLines = append(newLines, "[model_providers.shub]")
		newLines = append(newLines, fmt.Sprintf("base_url = '%s'", baseURL))
		if experimentalBearerToken != "" {
			newLines = append(newLines, fmt.Sprintf("experimental_bearer_token = %s", strconv.Quote(experimentalBearerToken)))
		}
	}

	return strings.Join(newLines, "\n")
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

	// Process auth.json
	var auth map[string]interface{}
	if err := json.Unmarshal([]byte(authJson), &auth); err != nil {
		auth = make(map[string]interface{})
	}

	experimentalBearerToken := ""
	if isCodexChatGPTAuth(auth) {
		experimentalBearerToken = apiKey
	}
	newConfigToml := updateCodexLocalProviderConfig(configToml, proxyURL, experimentalBearerToken)

	setCodexOpenAIAPIKey(auth, apiKey)

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
			"CLAUDE_CODE_ATTRIBUTION_HEADER":           "0",
			"CLAUDE_CODE_DISABLE_EXPERIMENTAL_BETAS":   "1",
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
network_access = true
sandbox_mode = "workspace-write"
experimental_use_rmcp_client = true
model = "gpt-5.5"
model_reasoning_effort = "high"
personality = "pragmatic"
web_search = "live"
windows_wsl_setup_acknowledged = true
model_verbosity = "high"
plan_mode_reasoning_effort = "high"
supports_websockets = true
model_provider = "shub"

[features]
plan_tool = true
view_image_tool = true
streamable_shell = false
rmcp_client = true
skills = true
parallel = true
unified_exec = true
shell_snapshot = true
multi_agent = true
steer = true
goals = true

[model_providers.shub]
name = "shub"
base_url = "%s"
requires_openai_auth = true
wire_api = "responses"

[tui]
status_line = ["current-dir", "git-branch", "model-with-reasoning", "five-hour-limit", "weekly-limit", "context-used"]
status_line_use_colors = true`, proxyURL)
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

// convertModelMappings converts storage.ModelMapping to config.ModelMapping
func convertModelMappings(storageModels []storage.ModelMapping) []config.ModelMapping {
	if len(storageModels) == 0 {
		return nil
	}
	configModels := make([]config.ModelMapping, len(storageModels))
	for i, m := range storageModels {
		configModels[i] = config.ModelMapping{
			Name:  m.Name,
			Alias: m.Alias,
		}
	}
	return configModels
}

// FullConfig represents the complete application configuration for backup/restore
type FullConfig struct {
	AppConfig   map[string]interface{}   `json:"appConfig,omitempty"`
	Vendors     []*VendorInfo            `json:"vendors"`
	Endpoints   []*config.EndpointConfig `json:"endpoints"`
	ReplaceMode bool                     `json:"replaceMode,omitempty"` // If true, clear existing config before saving
}

// configBackup holds a snapshot of current configuration for rollback
type configBackup struct {
	settings  *Settings
	vendors   []*storage.Vendor
	endpoints []*storage.Endpoint
}

// backupCurrentConfig creates a snapshot of current configuration
func (a *App) backupCurrentConfig() (*configBackup, error) {
	settings, err := a.GetSettings()
	if err != nil {
		return nil, fmt.Errorf("failed to get settings: %w", err)
	}

	vendors, err := a.storage.GetVendors()
	if err != nil {
		return nil, fmt.Errorf("failed to get vendors: %w", err)
	}

	endpoints, err := a.storage.GetEndpoints()
	if err != nil {
		return nil, fmt.Errorf("failed to get endpoints: %w", err)
	}

	return &configBackup{
		settings:  settings,
		vendors:   vendors,
		endpoints: endpoints,
	}, nil
}

// restoreConfigBackup restores configuration from backup
func (a *App) restoreConfigBackup(backup *configBackup) error {
	if backup == nil {
		return fmt.Errorf("backup is nil")
	}

	// Clear current data
	currentEndpoints, err := a.storage.GetEndpoints()
	if err != nil {
		return fmt.Errorf("failed to get current endpoints: %w", err)
	}
	for _, ep := range currentEndpoints {
		if err := a.storage.DeleteEndpoint(ep.ID); err != nil {
			return fmt.Errorf("failed to delete endpoint: %w", err)
		}
	}

	currentVendors, err := a.storage.GetVendors()
	if err != nil {
		return fmt.Errorf("failed to get current vendors: %w", err)
	}
	for _, v := range currentVendors {
		if err := a.storage.DeleteVendor(v.ID); err != nil {
			return fmt.Errorf("failed to delete vendor: %w", err)
		}
	}

	// Restore settings
	if err := a.SaveSettings(backup.settings); err != nil {
		return fmt.Errorf("failed to restore settings: %w", err)
	}

	// Restore vendors
	for _, v := range backup.vendors {
		if err := a.storage.SaveVendor(v); err != nil {
			return fmt.Errorf("failed to restore vendor: %w", err)
		}
	}

	// Restore endpoints
	for _, ep := range backup.endpoints {
		if err := a.storage.SaveEndpoint(ep); err != nil {
			return fmt.Errorf("failed to restore endpoint: %w", err)
		}
	}

	return nil
}

// SaveFullConfig saves the complete configuration from backup
// P0 FIX: Added backup/restore mechanism to prevent data loss in ReplaceMode
func (a *App) SaveFullConfig(config *FullConfig) error {
	if a.storage == nil {
		return fmt.Errorf("storage not initialized")
	}
	if config == nil {
		return fmt.Errorf("config is nil")
	}

	// ReplaceMode: backup current config before clearing
	var backup *configBackup
	if config.ReplaceMode {
		var err error
		backup, err = a.backupCurrentConfig()
		if err != nil {
			return fmt.Errorf("failed to backup current config: %w", err)
		}
	}

	// Save app settings
	if config.AppConfig != nil {
		currentSettings, err := a.GetSettings()
		if err != nil {
			return fmt.Errorf("failed to get current settings: %w", err)
		}
		settings := &Settings{
			Port:       currentSettings.Port,
			APIKey:     currentSettings.APIKey,
			ProxyURL:   currentSettings.ProxyURL,
			ClashPath:  currentSettings.ClashPath,
			DBSource:   currentSettings.DBSource,
			Fallback:   currentSettings.Fallback,
			DebugMode:  currentSettings.DebugMode,
			ListenAddr: currentSettings.ListenAddr,
		}

		if port, ok := config.AppConfig["port"].(float64); ok {
			settings.Port = int(port)
		} else if port, ok := config.AppConfig["port"].(int); ok {
			settings.Port = port
		}

		if apiKey, ok := config.AppConfig["apiKey"].(string); ok {
			settings.APIKey = apiKey
		}

		if proxyURL, ok := config.AppConfig["proxyUrl"].(string); ok {
			settings.ProxyURL = proxyURL
		}
		if clashPath, ok := config.AppConfig["clashPath"].(string); ok {
			settings.ClashPath = clashPath
		}
		if debugMode, ok := config.AppConfig["debugMode"].(string); ok {
			settings.DebugMode = debugMode
		}
		if listenAddr, ok := config.AppConfig["listenAddr"].(string); ok {
			settings.ListenAddr = listenAddr
		}
		if dbSource, ok := config.AppConfig["dbSource"].(string); ok {
			settings.DBSource = dbSource
		}

		if fallback, ok := config.AppConfig["fallback"].(bool); ok {
			settings.Fallback = fallback
		}

		if err := a.SaveSettings(settings); err != nil {
			return fmt.Errorf("failed to save settings: %w", err)
		}
		if _, ok := config.AppConfig["dbSource"]; ok {
			if _, err := a.ApplyDatabaseConfig(DatabaseApplyInput{DBSource: settings.DBSource}); err != nil {
				return fmt.Errorf("failed to apply database config: %w", err)
			}
		}
	}

	// ReplaceMode: clear all existing endpoints and vendors first
	if config.ReplaceMode {
		// Delete all endpoints first (they depend on vendors)
		existingEndpoints, err := a.storage.GetEndpoints()
		if err != nil {
			// Restore backup on failure
			if restoreErr := a.restoreConfigBackup(backup); restoreErr != nil {
				return fmt.Errorf("failed to get endpoints and restore failed: %w, %v", err, restoreErr)
			}
			return fmt.Errorf("failed to get existing endpoints: %w", err)
		}
		for _, ep := range existingEndpoints {
			if err := a.storage.DeleteEndpoint(ep.ID); err != nil {
				// Restore backup on failure
				if restoreErr := a.restoreConfigBackup(backup); restoreErr != nil {
					return fmt.Errorf("failed to delete endpoint and restore failed: %w, %v", err, restoreErr)
				}
				return fmt.Errorf("failed to delete endpoint %s: %w", ep.Name, err)
			}
		}

		// Delete all vendors
		existingVendors, err := a.storage.GetVendors()
		if err != nil {
			// Restore backup on failure
			if restoreErr := a.restoreConfigBackup(backup); restoreErr != nil {
				return fmt.Errorf("failed to get vendors and restore failed: %w, %v", err, restoreErr)
			}
			return fmt.Errorf("failed to get existing vendors: %w", err)
		}
		for _, v := range existingVendors {
			if err := a.storage.DeleteVendor(v.ID); err != nil {
				// Restore backup on failure
				if restoreErr := a.restoreConfigBackup(backup); restoreErr != nil {
					return fmt.Errorf("failed to delete vendor and restore failed: %w, %v", err, restoreErr)
				}
				return fmt.Errorf("failed to delete vendor %s: %w", v.Name, err)
			}
		}
	}

	// Save vendors
	if config.Vendors != nil {
		// Get existing vendors to check for duplicates (empty if ReplaceMode)
		existingVendors, err := a.storage.GetVendors()
		if err != nil {
			return fmt.Errorf("failed to get existing vendors: %w", err)
		}

		existingVendorMap := make(map[string]*storage.Vendor)
		for _, v := range existingVendors {
			existingVendorMap[v.Name] = v
		}

		for _, v := range config.Vendors {
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

		// Get existing endpoints (empty if ReplaceMode)
		existingEndpoints, err := a.storage.GetEndpoints()
		if err != nil {
			return fmt.Errorf("failed to get existing endpoints: %w", err)
		}

		existingEndpointMap := make(map[string]*storage.Endpoint)
		for _, ep := range existingEndpoints {
			key := fmt.Sprintf("%s-%s", ep.ProviderName, ep.Name)
			existingEndpointMap[key] = ep
		}

		for _, ep := range config.Endpoints {
			key := fmt.Sprintf("%s-%s", ep.ProviderName, ep.Name)

			// Convert config.ModelMapping to storage.ModelMapping
			storageModels := make([]storage.ModelMapping, len(ep.Models))
			for i, m := range ep.Models {
				storageModels[i] = storage.ModelMapping{
					Name:  m.Name,
					Alias: m.Alias,
				}
			}

			if existing, exists := existingEndpointMap[key]; exists {
				// Update existing endpoint
				existing.Name = ep.Name
				existing.APIURL = ep.APIURL
				existing.APIKey = ep.APIKey
				existing.Active = ep.Active
				existing.Enabled = ep.Enabled
				existing.InterfaceType = ep.InterfaceType
				existing.ProviderName = ep.ProviderName
				existing.Model = ep.Model
				existing.Transformer = ep.Transformer
				existing.ProxyURL = ep.ProxyURL
				existing.Models = storageModels
				existing.Headers = ep.Headers
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
					ProviderName:  ep.ProviderName,
					Model:         ep.Model,
					Transformer:   ep.Transformer,
					ProxyURL:      ep.ProxyURL,
					Models:        storageModels,
					Headers:       ep.Headers,
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
// WebDAV Backup/Restore Methods
// =============================================================================

// BackupDataResponse 备份数据响应（包含文件名和数据）
type BackupDataResponse struct {
	Filename string             `json:"filename"`
	Data     *config.BackupData `json:"data"`
}

// CreateBackupData 创建完整的备份数据
// 包含 config.json、kiro.json、codex.json 和 codex sqlite 账号内容（不含统计）
func (a *App) CreateBackupData() (*BackupDataResponse, error) {
	if a.storage == nil {
		return nil, fmt.Errorf("storage not initialized")
	}

	// 1. 获取主配置 (settings, vendors, endpoints)
	settings, err := a.GetSettings()
	if err != nil {
		return nil, fmt.Errorf("failed to get settings: %w", err)
	}

	vendors, err := a.storage.GetVendors()
	if err != nil {
		return nil, fmt.Errorf("failed to get vendors: %w", err)
	}

	endpoints, err := a.storage.GetEndpoints()
	if err != nil {
		return nil, fmt.Errorf("failed to get endpoints: %w", err)
	}

	// 2. 组装 appConfig
	appConfig := map[string]interface{}{
		"port":       settings.Port,
		"apiKey":     settings.APIKey,
		"proxyUrl":   settings.ProxyURL,
		"clashPath":  settings.ClashPath,
		"dbSource":   settings.DBSource,
		"fallback":   settings.Fallback,
		"debugMode":  settings.DebugMode,
		"listenAddr": settings.ListenAddr,
	}

	// 3. 转换 vendors
	vendorConfigs := make([]config.VendorConfig, len(vendors))
	for i, v := range vendors {
		vendorConfigs[i] = config.VendorConfig{
			ID:      v.ID,
			Name:    v.Name,
			HomeURL: v.HomeURL,
			APIURL:  v.APIURL,
			Remark:  v.Remark,
		}
	}

	// 4. 转换 endpoints
	endpointConfigs := make([]config.EndpointConfig, len(endpoints))
	for i, e := range endpoints {
		models := make([]config.ModelMapping, len(e.Models))
		for j, m := range e.Models {
			models[j] = config.ModelMapping{
				Name:  m.Name,
				Alias: m.Alias,
			}
		}
		endpointConfigs[i] = config.EndpointConfig{
			ID:             e.ID,
			Name:           e.Name,
			APIURL:         e.APIURL,
			APIKey:         e.APIKey,
			Active:         e.Active,
			Enabled:        e.Enabled,
			InterfaceType:  e.InterfaceType,
			Transformer:    e.Transformer,
			ProviderName:   e.ProviderName,
			Model:          e.Model,
			Remark:         e.Remark,
			Priority:       e.Priority,
			ProxyURL:       e.ProxyURL,
			Routes:         e.Routes,
			Models:         models,
			Headers:        e.Headers,
			ClaudeMessages: e.ClaudeMessages,
		}
	}

	// 5. 尝试获取 kiro.json (多账号配置)
	var kiroMultiConfig interface{}
	kiroMultiConfigPath := a.getKiroMultiConfigPath()
	if dto, err := a.loadKiroMultiConfigDTO(kiroMultiConfigPath); err == nil {
		kiroMultiConfig = dto
	}

	// 6. 组装备份数据
	backupData := &config.BackupData{
		SchemaVersion:   3,
		CreatedAt:       time.Now().Format(time.RFC3339),
		AppConfig:       appConfig,
		Vendors:         vendorConfigs,
		Endpoints:       endpointConfigs,
		KiroMultiConfig: kiroMultiConfig,
	}

	// 7. 尝试获取 clash 同步数据
	if raw := a.clashSyncExportRaw(); raw != nil {
		backupData.ClashConfig = json.RawMessage(raw)
	}

	// 8. 尝试获取 codex 同步数据（全局配置 + sqlite 账号，不包含统计数据）
	if raw, err := a.codexSyncExportRaw(); err != nil {
		return nil, fmt.Errorf("failed to export codex sync payload: %w", err)
	} else if len(raw) > 0 {
		backupData.CodexConfig = json.RawMessage(raw)
	}

	// 9. 生成备份文件名
	computerName, _ := a.GetComputerName()
	timestamp := time.Now().Format("2006-01-02T15-04-05")
	filename := fmt.Sprintf("%s-%s.json", computerName, timestamp)

	return &BackupDataResponse{
		Filename: filename,
		Data:     backupData,
	}, nil
}

// loadKiroMultiConfigDTO 加载 kiro.json 并转换为 DTO
func (a *App) loadKiroMultiConfigDTO(path string) (*KiroMultiConfigDTO, error) {
	kp := kiroProvider()
	if kp == nil {
		return nil, fmt.Errorf("kiro plugin not available")
	}
	raw, err := kp.LoadMultiConfigDTO(path)
	if err != nil {
		return nil, err
	}
	var dto KiroMultiConfigDTO
	if err := json.Unmarshal(raw, &dto); err != nil {
		return nil, err
	}
	return &dto, nil
}

// RestoreBackupData 从备份数据恢复配置
// mode: "replace" 或 "merge"
func (a *App) RestoreBackupData(backupData *config.BackupData, mode string) error {
	if a.storage == nil {
		return fmt.Errorf("storage not initialized")
	}

	if backupData == nil {
		return fmt.Errorf("backup data is nil")
	}

	// 验证备份数据
	if err := a.validateBackupData(backupData); err != nil {
		return fmt.Errorf("invalid backup data: %w", err)
	}

	// 确定恢复模式
	replaceMode := strings.ToLower(strings.TrimSpace(mode)) == "replace"

	// 根据模式处理主配置
	var finalConfig *FullConfig
	if replaceMode {
		// 替换模式：直接使用远程配置
		finalConfig = a.convertBackupToFullConfig(backupData, true)
	} else {
		// 合并模式：合并本地和远程配置
		mergedConfig, err := a.mergeBackupWithLocal(backupData)
		if err != nil {
			return fmt.Errorf("failed to merge configs: %w", err)
		}
		finalConfig = mergedConfig
	}

	// 保存主配置
	if err := a.SaveFullConfig(finalConfig); err != nil {
		return fmt.Errorf("failed to save main config: %w", err)
	}

	// 恢复 kiro.json (多账号配置)
	if backupData.KiroMultiConfig != nil {
		if err := a.saveKiroMultiConfigInternal(backupData.KiroMultiConfig, replaceMode); err != nil {
			fmt.Printf("warning: failed to restore kiro.json: %v\n", err)
		}
	}

	// 恢复 clash 配置
	if backupData.ClashConfig != nil {
		a.restoreClashConfig(backupData.ClashConfig)
	}

	// 恢复 codex 配置与账号
	if backupData.CodexConfig != nil {
		if err := a.saveCodexSyncConfigInternal(backupData.CodexConfig, replaceMode); err != nil {
			fmt.Printf("warning: failed to restore codex config/accounts: %v\n", err)
		}
	}

	return nil
}

// validateBackupData 验证备份数据的完整性
func (a *App) validateBackupData(data *config.BackupData) error {
	if data == nil {
		return fmt.Errorf("backup data is nil")
	}
	if data.SchemaVersion < 1 {
		return fmt.Errorf("invalid schema version: %d", data.SchemaVersion)
	}
	if data.AppConfig == nil {
		return fmt.Errorf("appConfig is missing")
	}
	return nil
}

// convertBackupToFullConfig 将 BackupData 转换为 FullConfig
func (a *App) convertBackupToFullConfig(backup *config.BackupData, replaceMode bool) *FullConfig {
	vendors := make([]*VendorInfo, len(backup.Vendors))
	for i, v := range backup.Vendors {
		vendors[i] = &VendorInfo{
			ID:      v.ID,
			Name:    v.Name,
			HomeURL: v.HomeURL,
			APIURL:  v.APIURL,
			Remark:  v.Remark,
		}
	}

	endpoints := make([]*config.EndpointConfig, len(backup.Endpoints))
	for i := range backup.Endpoints {
		endpoints[i] = &backup.Endpoints[i]
	}

	return &FullConfig{
		AppConfig:   backup.AppConfig,
		Vendors:     vendors,
		Endpoints:   endpoints,
		ReplaceMode: replaceMode,
	}
}

// mergeBackupWithLocal 合并远程备份和本地配置
func (a *App) mergeBackupWithLocal(backupData *config.BackupData) (*FullConfig, error) {
	// 1. 获取本地配置
	localSettings, err := a.GetSettings()
	if err != nil {
		return nil, fmt.Errorf("failed to get local settings: %w", err)
	}

	localVendors, err := a.storage.GetVendors()
	if err != nil {
		return nil, fmt.Errorf("failed to get local vendors: %w", err)
	}

	localEndpoints, err := a.storage.GetEndpoints()
	if err != nil {
		return nil, fmt.Errorf("failed to get local endpoints: %w", err)
	}

	// 2. 合并 settings (远程覆盖本地)
	mergedAppConfig := map[string]interface{}{
		"port":       localSettings.Port,
		"apiKey":     localSettings.APIKey,
		"proxyUrl":   localSettings.ProxyURL,
		"clashPath":  localSettings.ClashPath,
		"dbSource":   localSettings.DBSource,
		"fallback":   localSettings.Fallback,
		"debugMode":  localSettings.DebugMode,
		"listenAddr": localSettings.ListenAddr,
	}
	for k, v := range backupData.AppConfig {
		mergedAppConfig[k] = v
	}

	// 3. 合并 vendors (按 name 去重，远程优先)
	vendorMap := make(map[string]*VendorInfo)
	for _, v := range localVendors {
		vendorMap[v.Name] = &VendorInfo{
			ID:      v.ID,
			Name:    v.Name,
			HomeURL: v.HomeURL,
			APIURL:  v.APIURL,
			Remark:  v.Remark,
		}
	}
	for _, v := range backupData.Vendors {
		vendorMap[v.Name] = &VendorInfo{
			ID:      v.ID,
			Name:    v.Name,
			HomeURL: v.HomeURL,
			APIURL:  v.APIURL,
			Remark:  v.Remark,
		}
	}
	mergedVendors := make([]*VendorInfo, 0, len(vendorMap))
	for _, v := range vendorMap {
		mergedVendors = append(mergedVendors, v)
	}

	// 4. 合并 endpoints (按 id 去重，远程优先)
	endpointMap := make(map[int64]*config.EndpointConfig)
	for _, e := range localEndpoints {
		if e.ID != 0 {
			endpointMap[e.ID] = &config.EndpointConfig{
				ID:             e.ID,
				Name:           e.Name,
				APIURL:         e.APIURL,
				APIKey:         e.APIKey,
				Active:         e.Active,
				Enabled:        e.Enabled,
				InterfaceType:  e.InterfaceType,
				ProviderName:   e.ProviderName,
				Model:          e.Model,
				Transformer:    e.Transformer,
				Remark:         e.Remark,
				Priority:       e.Priority,
				ProxyURL:       e.ProxyURL,
				Models:         convertModelMappings(e.Models),
				Headers:        e.Headers,
				ClaudeMessages: e.ClaudeMessages,
			}
		}
	}
	for i := range backupData.Endpoints {
		if backupData.Endpoints[i].ID != 0 {
			endpointMap[backupData.Endpoints[i].ID] = &backupData.Endpoints[i]
		}
	}
	mergedEndpoints := make([]*config.EndpointConfig, 0, len(endpointMap))
	for _, e := range endpointMap {
		mergedEndpoints = append(mergedEndpoints, e)
	}

	return &FullConfig{
		AppConfig:   mergedAppConfig,
		Vendors:     mergedVendors,
		Endpoints:   mergedEndpoints,
		ReplaceMode: false,
	}, nil
}

// saveKiroMultiConfigInternal 保存 kiro.json
func (a *App) saveKiroMultiConfigInternal(data interface{}, replaceMode bool) error {
	// 转换为 KiroMultiConfigDTO
	jsonData, err := json.Marshal(data)
	if err != nil {
		return err
	}

	var dto KiroMultiConfigDTO
	if err := json.Unmarshal(jsonData, &dto); err != nil {
		return err
	}

	dto.ReplaceMode = replaceMode

	// 复用现有的保存逻辑
	return a.SaveKiroMultiConfigFromBackup(&dto)
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
// Endpoint Ping/Speed Test Methods
// =============================================================================

// PingResult represents the result of pinging an endpoint
type PingResult struct {
	EndpointID int64  `json:"endpointId"`
	Success    bool   `json:"success"`
	Latency    int64  `json:"latency"` // milliseconds
	Error      string `json:"error,omitempty"`
}

// PingEndpoint tests the HTTP connection speed to an endpoint's API URL
func (a *App) PingEndpoint(endpointID int64) (*PingResult, error) {
	if a.storage == nil {
		return &PingResult{EndpointID: endpointID, Success: false, Error: "storage not initialized"}, nil
	}

	ep, err := a.storage.GetEndpointByID(endpointID)
	if err != nil || ep == nil {
		return &PingResult{EndpointID: endpointID, Success: false, Error: "endpoint not found"}, nil
	}

	return a.doPingURL(endpointID, ep.APIURL, ep.ProxyURL), nil
}

// PingEndpointByURL tests the HTTP connection speed to a specific URL
func (a *App) PingEndpointByURL(apiURL string) (*PingResult, error) {
	return a.doPingURL(0, apiURL, ""), nil
}

// PingAllEndpoints tests the HTTP connection speed to all endpoints of a given interface type
func (a *App) PingAllEndpoints(interfaceType string) ([]*PingResult, error) {
	if a.storage == nil {
		return nil, fmt.Errorf("storage not initialized")
	}

	endpoints, err := a.storage.GetEndpointsByType(interfaceType)
	if err != nil {
		return nil, fmt.Errorf("failed to get endpoints: %w", err)
	}

	results := make([]*PingResult, 0, len(endpoints))
	for _, ep := range endpoints {
		result := a.doPingURL(ep.ID, ep.APIURL, ep.ProxyURL)
		results = append(results, result)
	}

	return results, nil
}

// doPingURL performs the actual HTTP HEAD/GET request to measure latency
func (a *App) doPingURL(endpointID int64, apiURL, proxyURL string) *PingResult {
	result := &PingResult{EndpointID: endpointID, Success: false}

	// Normalize URL
	rawURL := strings.TrimSpace(apiURL)
	if rawURL == "" {
		result.Error = "empty API URL"
		return result
	}
	if !strings.HasPrefix(rawURL, "http://") && !strings.HasPrefix(rawURL, "https://") {
		rawURL = "https://" + rawURL
	}

	// Parse URL to get base (without path for ping)
	parsedURL, err := url.Parse(rawURL)
	if err != nil {
		result.Error = fmt.Sprintf("invalid URL: %v", err)
		return result
	}

	// Use just the scheme and host for ping
	pingURL := fmt.Sprintf("%s://%s", parsedURL.Scheme, parsedURL.Host)

	// Create HTTP client with timeout
	client := &http.Client{
		Timeout: 10 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse // Don't follow redirects
		},
	}

	// Configure proxy if specified
	if proxyURL != "" {
		proxyParsed, err := url.Parse(proxyURL)
		if err == nil {
			client.Transport = &http.Transport{
				Proxy: http.ProxyURL(proxyParsed),
			}
		}
	}

	// Create HEAD request (lighter than GET)
	req, err := http.NewRequest("HEAD", pingURL, nil)
	if err != nil {
		result.Error = fmt.Sprintf("failed to create request: %v", err)
		return result
	}

	// Measure latency
	startTime := time.Now()
	resp, err := client.Do(req)
	latency := time.Since(startTime).Milliseconds()

	if err != nil {
		// Try GET if HEAD fails (some servers don't support HEAD)
		req, _ = http.NewRequest("GET", pingURL, nil)
		startTime = time.Now()
		resp, err = client.Do(req)
		latency = time.Since(startTime).Milliseconds()

		if err != nil {
			result.Error = fmt.Sprintf("connection failed: %v", err)
			return result
		}
	}
	defer resp.Body.Close()

	result.Success = true
	result.Latency = latency
	return result
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

// convertEndpoints converts storage.Endpoint to proxy.Endpoint
func convertEndpoints(endpoints []*storage.Endpoint) []*proxy.Endpoint {
	result := make([]*proxy.Endpoint, len(endpoints))
	for i, e := range endpoints {
		var models []proxy.ModelMapping
		if len(e.Models) > 0 {
			models = make([]proxy.ModelMapping, 0, len(e.Models))
			for _, m := range e.Models {
				models = append(models, proxy.ModelMapping{Name: m.Name, Alias: m.Alias})
			}
		}
		result[i] = &proxy.Endpoint{
			ID:            e.ID,
			Name:          e.Name,
			APIURL:        e.APIURL,
			APIKey:        e.APIKey,
			Active:        e.Active,
			Enabled:       e.Enabled,
			InterfaceType: e.InterfaceType,
			Transformer:   e.Transformer,
			ProviderName:  e.ProviderName,
			Model:         e.Model,
			Remark:        e.Remark,
			Priority:      e.Priority,
			ProxyURL:      e.ProxyURL,
			Routes:        e.Routes,
			Models:        models,
			Headers:       e.Headers,
		}
	}
	return result
}

// WebDAVConfigInfo represents WebDAV configuration for frontend
type WebDAVConfigInfo struct {
	ServerURL string `json:"serverUrl"`
	Username  string `json:"username"`
	Password  string `json:"password"`
}

// GetWebDAVConfig retrieves WebDAV configuration
func (a *App) GetWebDAVConfig() (*WebDAVConfigInfo, error) {
	if a.storage == nil {
		return nil, fmt.Errorf("storage not initialized")
	}

	config := &WebDAVConfigInfo{}

	if serverUrl, err := a.storage.GetConfig("webdav.serverUrl"); err == nil {
		config.ServerURL = serverUrl
	}
	if username, err := a.storage.GetConfig("webdav.username"); err == nil {
		config.Username = username
	}
	if password, err := a.storage.GetConfig("webdav.password"); err == nil {
		config.Password = password
	}

	return config, nil
}

// SaveWebDAVConfig saves WebDAV configuration
func (a *App) SaveWebDAVConfig(config *WebDAVConfigInfo) error {
	if a.storage == nil {
		return fmt.Errorf("storage not initialized")
	}
	if config == nil {
		return fmt.Errorf("config is nil")
	}

	if err := a.storage.SetConfig("webdav.serverUrl", config.ServerURL); err != nil {
		return fmt.Errorf("failed to save server URL: %w", err)
	}
	if err := a.storage.SetConfig("webdav.username", config.Username); err != nil {
		return fmt.Errorf("failed to save username: %w", err)
	}
	if err := a.storage.SetConfig("webdav.password", config.Password); err != nil {
		return fmt.Errorf("failed to save password: %w", err)
	}

	return nil
}

// =============================================================================
// Server Sync Methods
// =============================================================================

func normalizeRemoteServerBaseURL(raw string) (*url.URL, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, fmt.Errorf("server URL is required")
	}
	u, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("invalid server URL: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, fmt.Errorf("server URL must start with http:// or https://")
	}
	if u.Host == "" {
		return nil, fmt.Errorf("server URL must include host")
	}
	u.RawQuery = ""
	u.Fragment = ""
	u.Path = strings.TrimRight(u.Path, "/")
	return u, nil
}

type syncRequestData struct {
	server      config.ServerConfig
	syncURL     string
	body        []byte
	verifyCodex bool
}

func encodeSyncPayload(raw []byte) string {
	if len(raw) == 0 {
		return ""
	}

	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	_, _ = gz.Write(raw)
	_ = gz.Close()
	return base64.StdEncoding.EncodeToString(buf.Bytes())
}

func shellSingleQuote(raw string) string {
	if raw == "" {
		return "''"
	}
	return "'" + strings.ReplaceAll(raw, "'", `'"'"'`) + "'"
}

func (a *App) buildSyncRequestData(index int) (*syncRequestData, error) {
	if a.storage == nil {
		return nil, fmt.Errorf("storage not initialized")
	}

	fileStore, ok := a.storage.(*storage.ConfigFileStore)
	if !ok {
		return nil, fmt.Errorf("storage type does not support servers")
	}

	servers, err := fileStore.GetServers()
	if err != nil {
		return nil, fmt.Errorf("failed to get servers: %w", err)
	}
	if index < 0 || index >= len(servers) {
		return nil, fmt.Errorf("invalid server index: %d", index)
	}

	server := servers[index]
	base, err := normalizeRemoteServerBaseURL(server.URL)
	if err != nil {
		return nil, err
	}
	syncURL := *base
	syncURL.Path += "/sync/config"

	if a.configLoader == nil {
		return nil, fmt.Errorf("config loader not initialized")
	}
	cfg, err := a.configLoader.Load()
	if err != nil {
		return nil, fmt.Errorf("failed to load config: %w", err)
	}

	type syncPayload struct {
		Vendors            []config.VendorConfig   `json:"vendors"`
		Endpoints          []config.EndpointConfig `json:"endpoints"`
		KiroConfigEncoded  string                  `json:"kiroConfigEncoded,omitempty"`
		ClashConfigEncoded string                  `json:"clashConfigEncoded,omitempty"`
		CodexConfigEncoded string                  `json:"codexConfigEncoded,omitempty"`
	}

	payload := syncPayload{
		Vendors:            cfg.Vendors,
		Endpoints:          cfg.Endpoints,
		KiroConfigEncoded:  encodeSyncPayload(a.kiroSyncExportRaw()),
		ClashConfigEncoded: encodeSyncPayload(a.clashSyncExportRaw()),
	}

	if raw, err := a.codexSyncExportRaw(); err != nil {
		return nil, fmt.Errorf("failed to export codex sync payload: %w", err)
	} else {
		payload.CodexConfigEncoded = encodeSyncPayload(raw)
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("failed to serialize config: %w", err)
	}

	return &syncRequestData{
		server:      server,
		syncURL:     syncURL.String(),
		body:        body,
		verifyCodex: payload.CodexConfigEncoded != "",
	}, nil
}

// GetServers returns the list of remote servers from config.
func (a *App) GetServers() ([]config.ServerConfig, error) {
	if a.storage == nil {
		return nil, fmt.Errorf("storage not initialized")
	}

	fileStore, ok := a.storage.(*storage.ConfigFileStore)
	if !ok {
		return nil, fmt.Errorf("storage type does not support servers")
	}
	return fileStore.GetServers()
}

// SaveServers saves the list of remote servers to config.
func (a *App) SaveServers(servers []config.ServerConfig) error {
	if a.storage == nil {
		return fmt.Errorf("storage not initialized")
	}

	fileStore, ok := a.storage.(*storage.ConfigFileStore)
	if !ok {
		return fmt.Errorf("storage type does not support servers")
	}
	return fileStore.SaveServers(servers)
}

// TestServerConnection tests connectivity to a remote server by hitting its /health endpoint.
func (a *App) TestServerConnection(serverURL, apiKey string) error {
	base, err := normalizeRemoteServerBaseURL(serverURL)
	if err != nil {
		return err
	}

	healthURL := *base
	healthURL.Path += "/health"

	client := &http.Client{Timeout: 10 * time.Second}
	req, err := http.NewRequest(http.MethodGet, healthURL.String(), nil)
	if err != nil {
		return fmt.Errorf("invalid URL: %w", err)
	}
	if apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("connection failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("server responded with status %d", resp.StatusCode)
	}
	return nil
}

// BuildSyncConfigCurl builds a complete curl command for syncing config to a selected server.
func (a *App) BuildSyncConfigCurl(index int) (string, error) {
	reqData, err := a.buildSyncRequestData(index)
	if err != nil {
		return "", err
	}

	const payloadDelimiter = "__CLISIMPLEHUB_SYNC_PAYLOAD__"

	var builder strings.Builder
	fmt.Fprintf(&builder, "curl --request POST --url %s \\\n", shellSingleQuote(reqData.syncURL))
	fmt.Fprintf(&builder, "  --header %s", shellSingleQuote("Content-Type: application/json"))
	if strings.TrimSpace(reqData.server.APIKey) != "" {
		fmt.Fprintf(&builder, " \\\n  --header %s", shellSingleQuote("Authorization: Bearer "+reqData.server.APIKey))
	}
	fmt.Fprintf(&builder, " \\\n  --data-binary @- <<'%s'\n", payloadDelimiter)
	builder.Write(reqData.body)
	if len(reqData.body) == 0 || reqData.body[len(reqData.body)-1] != '\n' {
		builder.WriteByte('\n')
	}
	builder.WriteString(payloadDelimiter)
	return builder.String(), nil
}

// SyncConfigToServer syncs the current config to a remote headless server.
func (a *App) SyncConfigToServer(index int) error {
	reqData, err := a.buildSyncRequestData(index)
	if err != nil {
		return err
	}

	client := &http.Client{Timeout: 120 * time.Second}
	req, err := http.NewRequest(http.MethodPost, reqData.syncURL, bytes.NewReader(reqData.body))
	if err != nil {
		return fmt.Errorf("invalid URL: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if reqData.server.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+reqData.server.APIKey)
	}

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("sync request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 32<<10))
		return fmt.Errorf("sync failed (status %d): %s", resp.StatusCode, string(respBody))
	}

	if reqData.verifyCodex {
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
		var syncResp struct {
			Codex struct {
				Synced  bool   `json:"synced"`
				Warning string `json:"warning,omitempty"`
			} `json:"codex"`
			Plugins map[string]struct {
				Synced  bool   `json:"synced"`
				Warning string `json:"warning,omitempty"`
			} `json:"plugins"`
		}
		if err := json.Unmarshal(respBody, &syncResp); err != nil {
			return fmt.Errorf("sync succeeded but failed to verify codex sync result: %w", err)
		}

		codexResult := syncResp.Codex
		if pluginResult, ok := syncResp.Plugins["codex-accounts"]; ok {
			codexResult = pluginResult
		}
		if !codexResult.Synced {
			if strings.TrimSpace(codexResult.Warning) != "" {
				return fmt.Errorf("remote codex sync failed: %s", codexResult.Warning)
			}
			return fmt.Errorf("remote codex sync failed")
		}
	}

	return nil
}

// ComputeSHA1 计算字符串的 SHA-1 哈希值（40字符十六进制）
func (a *App) ComputeSHA1(input string) string {
	hash := sha1.Sum([]byte(input))
	return fmt.Sprintf("%x", hash)
}
