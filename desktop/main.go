package main

import (
	"context"
	"embed"
	"log"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"clisimplehub/internal/config"
	"clisimplehub/internal/proxy"
	"clisimplehub/internal/statsdb"
	"clisimplehub/internal/storage"
	kiro_claude "clisimplehub/internal/transformer/kiro/claude"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
)

//go:embed all:ui/dist
var assets embed.FS

// Default configuration values
const (
	DefaultPort = 5600
)

// Config keys for config.json appConfig
const (
	ConfigKeyPort     = "port"
	ConfigKeyAPIKey   = "apiKey"
	ConfigKeyFallback = "fallback"
	ConfigKeyDebugMode = "debugMode"
	// Temporary disable TTL for failed endpoints (minutes)
	ConfigKeyTempDisableMinutes = "tempDisableMinutes"
	// CLI config directories
	ConfigKeyClaudeConfigDir = "claudeConfigDir"
	ConfigKeyCodexConfigDir  = "codexConfigDir"
)

// onSecondInstanceLaunch 当检测到第二个实例启动时的回调函数
func onSecondInstanceLaunch(data options.SecondInstanceData) {
	log.Printf("检测到第二个实例启动，参数: %v", data.Args)
	// 注意：此时 app.ctx 尚未初始化，无法使用 runtime 激活窗口
	// Wails 会自动处理窗口激活（Windows/macOS）
}

func main() {
	log.SetPrefix("[clisimplehub] ")
	log.SetFlags(log.Ldate | log.Ltime | log.Lshortfile)

	log.Println("Starting Cli Simple Hub in GUI mode...")

	// Get data directory with priority:
	// 1. CODESP_DATA environment variable (highest priority)
	// 2. Current directory (if config.json exists)
	// 3. User home directory under .clisimplehub
	dataDir := config.GetDataDir()
	configPath := filepath.Join(dataDir, config.ConfigFileName)

	log.Printf("Data directory: %s", dataDir)
	log.Printf("Config path: %s", configPath)

	// Initialize config loader (config.json is the single source of truth)
	configLoader := config.NewConfigLoader(configPath)

	// Initialize storage using config.json only
	store, err := storage.NewConfigFileStore(configLoader)
	if err != nil {
		log.Fatalf("Failed to initialize storage: %v", err)
	}

	// Initialize vendor stats SQLite store (best-effort; never blocks proxy startup).
	vendorStatsPath := filepath.Join(dataDir, "data.sqlite")
	var vendorStatsStore statsdb.VendorStatsStore
	if db, err := statsdb.OpenSQLiteVendorStatsStore(vendorStatsPath); err != nil {
		log.Printf("Warning: Failed to initialize vendor stats db (%s): %v", vendorStatsPath, err)
	} else {
		vendorStatsStore = db
		log.Printf("Vendor stats db: %s", vendorStatsPath)
	}

	// Load port configuration with priority:
	// 1. PORT environment variable (highest priority)
	// 2. config.json app_config
	// 3. Default port
	port := DefaultPort

	// Check environment variable first (highest priority)
	if envPort := config.GetPortFromEnv(); envPort > 0 {
		port = envPort
		log.Printf("Using port from PORT environment variable: %d", port)
	} else if savedPort, err := store.GetConfig(ConfigKeyPort); err == nil && savedPort != "" {
		if p, err := strconv.Atoi(savedPort); err == nil {
			port = p
		}
	}

	// Load endpoints from config.json
	endpoints, err := store.GetEndpoints()
	if err != nil {
		log.Printf("Warning: Failed to load endpoints: %v", err)
		endpoints = []*storage.Endpoint{}
	}
	log.Printf("Loaded %d endpoints from config.json", len(endpoints))

	// Initialize router and load endpoints
	// Requirements: 3.1, 3.2, 3.3, 3.4, 3.5
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

	// Initialize proxy server with WebSocket hub
	// Requirements: 1.1, 5.1, 7.1, 8.5
	proxyServer := proxy.NewProxyServerWithWSHub(port, router, wsHub)
	proxyServer.SetStorage(store)
	proxyServer.SetVendorStatsStore(vendorStatsStore)

	// Initialize kiro transformer config getter
	kiro_claude.SetConfigGetter(func(key string) (string, error) {
		if strings.TrimSpace(key) == "configPath" {
			return configPath, nil
		}
		return store.GetConfig(key)
	})
	if key, err := store.GetConfig(ConfigKeyAPIKey); err == nil {
		if key = strings.TrimSpace(key); key != "" {
			proxyServer.SetAuthKey(key)
		}
	}
	// Load fallback setting from config
	if fallbackStr, err := store.GetConfig(ConfigKeyFallback); err == nil && fallbackStr == "true" {
		proxyServer.SetFallbackEnabled(true)
	}

	// Create the app instance
	app := NewApp()

	// Wire up all components
	// Requirements: All - Final integration
	app.SetStorage(store)
	app.SetProxyServer(proxyServer)
	app.SetRouter(router)
	app.SetWSHub(wsHub)
	app.SetConfigLoader(configLoader)
	if sqliteStore, ok := vendorStatsStore.(*statsdb.SQLiteVendorStatsStore); ok {
		app.SetVendorStats(sqliteStore)
	}

	// Start proxy server in background
	// Requirements: 5.1
	go func() {
		log.Printf("Starting proxy server on port %d...", port)
		if err := proxyServer.Start(); err != nil {
			log.Printf("Proxy server error: %v", err)
		}
	}()

	// Create Wails application with options
	// Requirements: 10.1, 10.2
	err = wails.Run(&options.App{
		Title:  "Cli Simple Hub",
		Width:  1200,
		Height: 800,
		AssetServer: &assetserver.Options{
			Assets: assets,
		},
		BackgroundColour: &options.RGBA{R: 27, G: 38, B: 54, A: 1},
		SingleInstanceLock: &options.SingleInstanceLock{
			UniqueId:               "com.clisimplehub.app",
			OnSecondInstanceLaunch: onSecondInstanceLaunch,
		},
		OnStartup: app.startup,
		OnShutdown: func(ctx context.Context) {
			// Graceful shutdown
			// Requirements: 5.4
			log.Println("Shutting down...")
			if err := proxyServer.Stop(); err != nil {
				log.Printf("Error stopping proxy server: %v", err)
			}
			wsHub.Stop()
			if err := store.Close(); err != nil {
				log.Printf("Error closing storage: %v", err)
			}
			if vendorStatsStore != nil {
				if err := vendorStatsStore.Close(); err != nil {
					log.Printf("Error closing vendor stats db: %v", err)
				}
			}
			log.Println("Shutdown complete")
		},
		Bind: []interface{}{
			app,
		},
	})

	if err != nil {
		log.Printf("Error: %v", err.Error())
	}
}
