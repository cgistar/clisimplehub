// Package main provides the headless server mode for Cli Simple Hub.
package main

import (
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"clisimplehub/internal/config"
	"clisimplehub/internal/dbconfig"
	"clisimplehub/internal/plugin"
	"clisimplehub/internal/proxy"
	"clisimplehub/internal/sqlitequeue"
	"clisimplehub/internal/statsdb"
	"clisimplehub/internal/storage"
)

// Default configuration values
const (
	DefaultPort = 5600
)

// Config keys for config.json appConfig
const (
	ConfigKeyPort      = "port"
	ConfigKeyAPIKey    = "apiKey"
	ConfigKeyFallback  = "fallback"
	ConfigKeyDebugMode = "debugMode"
	// Temporary disable TTL for failed endpoints (minutes)
	ConfigKeyTempDisableMinutes = "tempDisableMinutes"
	// Listen address for proxy server
	ConfigKeyListenAddr = "listenAddr"
)

func main() {
	log.SetPrefix("[clisimplehub] ")
	log.SetFlags(log.Ldate | log.Ltime | log.Lshortfile)

	log.Println("Starting Cli Simple Hub in headless mode...")

	// Get configuration from environment variables or defaults
	port := getEnvInt("PORT", DefaultPort)
	configPath := resolveConfigPath()

	log.Printf("Configuration: port=%d, configPath=%s", port, configPath)

	// Initialize config loader (config.json is the single source of truth)
	configLoader := config.NewConfigLoader(configPath)

	// Initialize storage using config.json only
	store, err := storage.NewConfigFileStore(configLoader)
	if err != nil {
		log.Fatalf("Failed to initialize storage: %v", err)
	}
	defer store.Close()
	log.Println("Storage initialized successfully")

	// Initialize usage stats store (best-effort; never blocks proxy startup).
	var usageStatsStore statsdb.UsageStatsStore
	dbCfg, err := dbconfig.Resolve(configLoader.GetPath(), store.GetConfig)
	if err != nil {
		log.Printf("Warning: Failed to resolve usage stats db config: %v", err)
	} else if db, err := statsdb.OpenUsageStatsStore(dbCfg); err != nil {
		log.Printf("Warning: Failed to initialize usage stats db (%s): %v", dbconfig.DisplaySource(dbCfg), err)
	} else {
		usageStatsStore = db
		defer usageStatsStore.Close()
		log.Printf("Usage stats db (%s): %s", dbCfg.Driver, dbconfig.DisplaySource(dbCfg))
	}

	// Load port from config.json if not set via environment
	if os.Getenv("PORT") == "" {
		if savedPort, err := store.GetConfig(ConfigKeyPort); err == nil && savedPort != "" {
			if p, err := strconv.Atoi(savedPort); err == nil {
				port = p
				log.Printf("Using port from config.json app_config: %d", port)
			}
		}
	}

	// Load listen address configuration
	// Default to 0.0.0.0 for security (localhost only)
	listenAddr := "0.0.0.0"
	if envListenAddr := strings.TrimSpace(os.Getenv("LISTEN_ADDR")); envListenAddr != "" {
		if err := config.ValidateListenAddr(envListenAddr); err != nil {
			log.Printf("Warning: Invalid listen address from LISTEN_ADDR '%s': %v, using default 0.0.0.0", envListenAddr, err)
		} else {
			listenAddr = envListenAddr
			log.Printf("Using listen address from LISTEN_ADDR: %s", listenAddr)
		}
	} else if savedAddr, err := store.GetConfig(ConfigKeyListenAddr); err == nil && savedAddr != "" {
		if err := config.ValidateListenAddr(savedAddr); err != nil {
			log.Printf("Warning: Invalid listen address '%s': %v, using default 0.0.0.0", savedAddr, err)
		} else {
			listenAddr = savedAddr
			log.Printf("Using listen address from config: %s", listenAddr)
		}
	}

	// 检查最终监听地址上的端口是否可用
	if err := config.IsListenAddrPortAvailable(listenAddr, port); err != nil {
		log.Fatalf("启动失败: %v\n请检查端口是否被其他程序占用，或尝试使用其他端口", err)
	}

	// Load endpoints from config.json
	endpoints, err := store.GetEndpoints()
	if err != nil {
		log.Fatalf("Failed to load endpoints: %v", err)
	}
	log.Printf("Loaded %d endpoints from config.json", len(endpoints))

	// Initialize router and load endpoints
	router := proxy.NewRouter()
	tempDisableMinutes := 5
	if v, err := store.GetConfig(ConfigKeyTempDisableMinutes); err == nil && v != "" {
		if minutes, err := strconv.Atoi(v); err == nil && minutes > 0 {
			tempDisableMinutes = minutes
		}
	}
	router.SetTempDisableTTL(time.Duration(tempDisableMinutes) * time.Minute)
	proxyEndpoints := convertEndpoints(endpoints)
	router.LoadEndpoints(proxyEndpoints)

	// Initialize SSE hub for real-time updates
	sseHub := proxy.NewSSEHub()
	go sseHub.Run()
	defer sseHub.Stop()

	// Initialize proxy server
	proxyServer := proxy.NewProxyServerWithSSEHub(port, router, sseHub)
	proxyServer.SetListenAddr(listenAddr)
	proxyServer.SetStorage(store)
	proxyServer.SetUsageStatsStore(usageStatsStore)
	proxyServer.SetConfigPath(configPath)

	// Initialize all plugins
	configGetter := func(key string) (string, error) {
		if strings.TrimSpace(key) == "configPath" {
			return configPath, nil
		}
		return store.GetConfig(key)
	}
	plugin.SetConfigGetter(configGetter)
	reloadOnce := func() {
		reloadConfig(store, router, proxyServer)
	}
	for _, pl := range plugin.All() {
		if err := pl.Init(plugin.InitConfig{
			ConfigPath:    configPath,
			ConfigGetter:  configGetter,
			Storage:       store,
			TriggerReload: reloadOnce,
		}); err != nil {
			log.Printf("Warning: plugin %s init failed: %v", pl.Name(), err)
		}
	}

	// Load API Key from environment variable (highest priority) or config.json
	apiKey := strings.TrimSpace(os.Getenv(config.EnvAPIKey))
	if apiKey == "" {
		// Fallback to config.json
		if key, err := store.GetConfig(ConfigKeyAPIKey); err == nil {
			apiKey = strings.TrimSpace(key)
		}
	}
	if apiKey != "" {
		proxyServer.SetAuthKey(apiKey)
		log.Println("API authentication enabled")
	} else {
		log.Println("API authentication disabled (no API key configured)")
	}

	// Load fallback setting from config
	if fallbackStr, err := store.GetConfig(ConfigKeyFallback); err == nil && strings.TrimSpace(fallbackStr) == "true" {
		proxyServer.SetFallbackEnabled(true)
		log.Println("Fallback mode enabled")
	}

	// Set reload function for HTTP /reload endpoint
	proxyServer.SetReloadFunc(func() {
		reloadConfig(store, router, proxyServer)
	})

	// Set up signal handling for graceful shutdown and config reload
	// Requirements: 5.4
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)

	// Error channel for server startup failures
	errChan := make(chan error, 1)

	// Start proxy server in a goroutine
	go func() {
		log.Printf("Proxy server starting on %s:%d...", listenAddr, port)
		if err := proxyServer.Start(); err != nil {
			log.Printf("Proxy server error: %v", err)
			errChan <- err
		}
	}()

	log.Println("Cli Simple Hub is running. Press Ctrl+C to stop.")

	// Wait for shutdown signal, config reload signal, or startup error
	// Requirements: 5.4
	for {
		select {
		case err := <-errChan:
			log.Fatalf("Server startup failed: %v", err)
		case sig := <-sigChan:
			if sig == syscall.SIGHUP {
				log.Println("Received SIGHUP, reloading config...")
				reloadConfig(store, router, proxyServer)
				continue
			}
			log.Printf("Received signal %v, shutting down...", sig)
			goto shutdown
		}
	}

shutdown:

	// Graceful shutdown
	sseHub.Stop()
	if err := proxyServer.Stop(); err != nil {
		log.Printf("Error during shutdown: %v", err)
	}
	if err := plugin.CloseAll(); err != nil {
		log.Printf("Error closing plugins: %v", err)
	}
	if err := sqlitequeue.CloseAll(); err != nil {
		log.Printf("Error closing sqlite backends: %v", err)
	}

	log.Println("Cli Simple Hub stopped.")
}

var executablePath = os.Executable
var reloadMu sync.Mutex

func reloadConfig(store storage.Storage, router *proxy.DefaultRouter, proxyServer *proxy.ProxyServer) {
	reloadMu.Lock()
	defer reloadMu.Unlock()

	if store == nil {
		log.Println("Config reload skipped: storage not initialized")
		return
	}

	startTime := time.Now()
	log.Println("Config reload started")

	// Reload router settings and endpoints
	if router != nil {
		if v, err := store.GetConfig(ConfigKeyTempDisableMinutes); err != nil {
			log.Printf("Config reload: failed to read %s: %v (keeping previous TTL)", ConfigKeyTempDisableMinutes, err)
		} else if strings.TrimSpace(v) != "" {
			if minutes, err := strconv.Atoi(strings.TrimSpace(v)); err == nil && minutes > 0 {
				router.SetTempDisableTTL(time.Duration(minutes) * time.Minute)
				log.Printf("Config reload: tempDisableTTL=%dm", minutes)
			} else {
				log.Printf("Config reload: invalid %s=%q (keeping previous TTL)", ConfigKeyTempDisableMinutes, v)
			}
		}

		endpoints, err := store.GetEndpoints()
		if err != nil {
			log.Printf("Config reload: failed to reload endpoints: %v (keeping existing endpoints)", err)
		} else {
			router.LoadEndpoints(convertEndpoints(endpoints))
			log.Printf("Config reload: endpoints reloaded (%d)", len(endpoints))
		}
	}

	// Reload runtime proxy settings
	if proxyServer != nil {
		// Update debug logger
		proxyServer.UpdateDebugFileLogger()
		log.Println("Config reload: debug logger updated")

		// listenAddr: respect LISTEN_ADDR env priority
		if os.Getenv("LISTEN_ADDR") == "" {
			if v, err := store.GetConfig(ConfigKeyListenAddr); err != nil {
				log.Printf("Config reload: failed to read %s: %v (keeping current listen address)", ConfigKeyListenAddr, err)
			} else {
				target := strings.TrimSpace(v)
				if target == "" {
					target = "0.0.0.0"
				}
				if err := config.ValidateListenAddr(target); err != nil {
					log.Printf("Config reload: invalid %s=%q: %v (keeping current listen address)", ConfigKeyListenAddr, v, err)
				} else {
					current := strings.TrimSpace(proxyServer.GetListenAddr())
					if current == "" {
						current = "0.0.0.0"
					}
					if target != current {
						proxyServer.SetListenAddr(target)
						log.Printf("Config reload: listenAddr=%s (restart required to take effect)", target)
					} else {
						log.Printf("Config reload: listenAddr unchanged (%s)", target)
					}
				}
			}
		} else {
			log.Println("Config reload: listenAddr skipped (LISTEN_ADDR env set)")
		}

		if v, err := store.GetConfig(ConfigKeyAPIKey); err != nil {
			log.Printf("Config reload: failed to read %s: %v (keeping current auth key)", ConfigKeyAPIKey, err)
		} else {
			// Environment variable has highest priority
			apiKey := strings.TrimSpace(os.Getenv(config.EnvAPIKey))
			if apiKey == "" {
				// Fallback to config.json
				apiKey = strings.TrimSpace(v)
			}
			proxyServer.SetAuthKey(apiKey)
			if apiKey == "" {
				log.Println("Config reload: auth disabled (empty apiKey)")
			} else {
				log.Println("Config reload: auth enabled (apiKey set)")
			}
		}

		if v, err := store.GetConfig(ConfigKeyFallback); err != nil {
			log.Printf("Config reload: failed to read %s: %v (keeping current fallback setting)", ConfigKeyFallback, err)
		} else {
			enabled := strings.TrimSpace(v) == "true"
			proxyServer.SetFallbackEnabled(enabled)
			log.Printf("Config reload: fallbackEnabled=%v", enabled)
		}
	}

	// Reload all plugins
	for _, pl := range plugin.All() {
		if err := pl.Reload(); err != nil {
			log.Printf("Config reload: plugin %s reload failed: %v", pl.Name(), err)
		}
	}

	log.Printf("Config reload finished in %s", time.Since(startTime))
}

func resolveConfigPath() string {
	if envPath := strings.TrimSpace(os.Getenv("CONFIG_PATH")); envPath != "" {
		return envPath
	}

	if exePath, err := executablePath(); err == nil {
		exeDir := filepath.Dir(exePath)
		exeConfig := filepath.Join(exeDir, config.ConfigFileName)
		if fileExists(exeConfig) {
			return exeConfig
		}
	}

	if fileExists(config.ConfigFileName) {
		return config.ConfigFileName
	}

	return config.FindOrCreateConfigPath()
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	return !info.IsDir()
}

// getEnvInt returns the environment variable value as int or the default
func getEnvInt(key string, defaultValue int) int {
	if value := os.Getenv(key); value != "" {
		if intValue, err := strconv.Atoi(value); err == nil {
			return intValue
		}
	}
	return defaultValue
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

// printUsage prints usage information
func printUsage() {
	fmt.Println("Cli Simple Hub - Headless Server Mode")
	fmt.Println("")
	fmt.Println("Environment Variables:")
	fmt.Println("  PORT         - Proxy server port (default: 5600)")
	fmt.Println("  CONFIG_PATH  - Path to config.json file (highest priority)")
	fmt.Println("  LISTEN_ADDR  - Listen address (e.g. 0.0.0.0 for Docker)")
	fmt.Println("  API_KEY      - API key for authentication (highest priority)")
	fmt.Println("  DATA         - Data dir (fallback config location if no local config.json)")
	fmt.Println("")
	fmt.Println("Config path resolution order:")
	fmt.Println("  1) CONFIG_PATH")
	fmt.Println("  2) <executable_dir>/config.json")
	fmt.Println("  3) ./config.json")
	fmt.Println("  4) $DATA/config.json or $HOME/.clisimplehub/config.json (auto-create if missing)")
	fmt.Println("")
	fmt.Println("Example:")
	fmt.Println("  PORT=9090 API_KEY=secret CONFIG_PATH=/etc/proxy/config.json ./cliSimpleHub-server")
}
