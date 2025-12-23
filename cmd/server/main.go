// Package main provides the headless server mode for Cli Simple Hub.
package main

import (
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	"clisimplehub/internal/config"
	"clisimplehub/internal/proxy"
	"clisimplehub/internal/statsdb"
	"clisimplehub/internal/storage"
)

// Default configuration values
const (
	DefaultPort       = 5600
	DefaultConfigPath = "config.json"
)

// Config keys for config.json appConfig
const (
	ConfigKeyPort     = "port"
	ConfigKeyAPIKey   = "apiKey"
	ConfigKeyFallback = "fallback"
	// Temporary disable TTL for failed endpoints (minutes)
	ConfigKeyTempDisableMinutes = "tempDisableMinutes"
)

func main() {
	log.SetPrefix("[clisimplehub] ")
	log.SetFlags(log.Ldate | log.Ltime | log.Lshortfile)

	log.Println("Starting Cli Simple Hub in headless mode...")

	// Get configuration from environment variables or defaults
	port := getEnvInt("PORT", DefaultPort)
	configPath := getEnvString("CONFIG_PATH", DefaultConfigPath)

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

	// Initialize vendor stats SQLite store (best-effort; never blocks proxy startup).
	dataDir := filepath.Dir(configLoader.GetPath())
	vendorStatsPath := filepath.Join(dataDir, "data.sqlite")
	var vendorStatsStore statsdb.VendorStatsStore
	if db, err := statsdb.OpenSQLiteVendorStatsStore(vendorStatsPath); err != nil {
		log.Printf("Warning: Failed to initialize vendor stats db (%s): %v", vendorStatsPath, err)
	} else {
		vendorStatsStore = db
		defer vendorStatsStore.Close()
		log.Printf("Vendor stats db: %s", vendorStatsPath)
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

	// Initialize WebSocket hub for real-time updates
	// Requirements: 7.1, 8.5
	wsHub := proxy.NewWSHub()
	go wsHub.Run()
	defer wsHub.Stop()

	// Initialize proxy server
	// Requirements: 5.1
	proxyServer := proxy.NewProxyServerWithWSHub(port, router, wsHub)
	proxyServer.SetStorage(store)
	proxyServer.SetVendorStatsStore(vendorStatsStore)
	if key, err := store.GetConfig(ConfigKeyAPIKey); err == nil {
		proxyServer.SetAuthKey(key)
	}
	// Load fallback setting from config
	if fallbackStr, err := store.GetConfig(ConfigKeyFallback); err == nil && fallbackStr == "true" {
		proxyServer.SetFallbackEnabled(true)
		log.Println("Fallback mode enabled")
	}

	// Set up signal handling for graceful shutdown
	// Requirements: 5.4
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	// Start proxy server in a goroutine
	go func() {
		log.Printf("Proxy server starting on port %d...", port)
		if err := proxyServer.Start(); err != nil {
			log.Printf("Proxy server error: %v", err)
		}
	}()

	log.Println("Cli Simple Hub is running. Press Ctrl+C to stop.")

	// Wait for shutdown signal
	// Requirements: 5.4
	sig := <-sigChan
	log.Printf("Received signal %v, shutting down...", sig)

	// Graceful shutdown
	if err := proxyServer.Stop(); err != nil {
		log.Printf("Error during shutdown: %v", err)
	}

	log.Println("Cli Simple Hub stopped.")
}

// getEnvString returns the environment variable value or the default
func getEnvString(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
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
			VendorID:      e.VendorID,
			Model:         e.Model,
			Remark:        e.Remark,
			Priority:      e.Priority,
			ProxyURL:      e.ProxyURL,
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
	fmt.Println("  CONFIG_PATH  - Path to config.json file (default: config.json)")
	fmt.Println("")
	fmt.Println("Example:")
	fmt.Println("  PORT=9090 CONFIG_PATH=/etc/proxy/config.json ./server")
}
