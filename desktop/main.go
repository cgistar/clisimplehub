package main

import (
	"context"
	"embed"
	"io"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"clisimplehub/internal/appdb"
	"clisimplehub/internal/config"
	"clisimplehub/internal/dbconfig"
	"clisimplehub/internal/plugin"
	"clisimplehub/internal/proxy"
	"clisimplehub/internal/sqlitequeue"
	"clisimplehub/internal/statsdb"
	"clisimplehub/internal/storage"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	wailsRuntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

//go:embed all:ui/dist
var assets embed.FS

// Default configuration values
const (
	DefaultPort = 5600
	// Keep one desktop process per user session to avoid duplicate backend services.
	desktopSingleInstanceID = "clisimplehub.desktop.instance"
)

// Config keys for config.json appConfig
const (
	ConfigKeyPort      = "port"
	ConfigKeyAPIKey    = "apiKey"
	ConfigKeyProxyURL  = "proxyUrl"
	ConfigKeyClashPath = "clashPath"
	ConfigKeyDBDriver  = "dbDriver"
	ConfigKeyDBSource  = "dbSource"
	ConfigKeyFallback  = "fallback"
	ConfigKeyDebugMode = "debugMode"
	// Temporary disable TTL for failed endpoints (minutes)
	ConfigKeyTempDisableMinutes = "tempDisableMinutes"
	// CLI config directories
	ConfigKeyClaudeConfigDir = "claudeConfigDir"
	ConfigKeyCodexConfigDir  = "codexConfigDir"
	// Listen address for proxy server
	ConfigKeyListenAddr = "listenAddr"
)

func main() {
	// Create the app instance early so startup logs can be buffered and replayed to frontend.
	app := NewApp()

	log.SetPrefix("[clisimplehub] ")
	log.SetFlags(log.Ldate | log.Ltime | log.Lshortfile)
	log.SetOutput(io.MultiWriter(os.Stderr, app.GoLogWriter()))

	log.Println("Starting Cli Simple Hub in GUI mode...")

	if isBindingsBuild() {
		log.Println("Bindings generation mode: skipping runtime initialization")
		err := wails.Run(&options.App{
			Title:            "Cli Simple Hub",
			Width:            1200,
			Height:           800,
			AssetServer:      &assetserver.Options{Assets: assets},
			OnStartup:        app.startup,
			BackgroundColour: &options.RGBA{R: 27, G: 38, B: 54, A: 1},
			Bind:             []interface{}{app},
		})
		if err != nil {
			log.Printf("Error: %v", err.Error())
		}
		return
	}

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

	// Initialize usage stats store (best-effort; never blocks proxy startup).
	var usageStatsStore statsdb.UsageStatsStore
	dbCfg, err := dbconfig.Resolve(configLoader.GetPath(), store.GetConfig)
	if err != nil {
		log.Printf("Warning: Failed to resolve usage stats db config: %v", err)
	} else if db, err := statsdb.OpenUsageStatsStore(dbCfg); err != nil {
		log.Printf("Warning: Failed to initialize usage stats db (%s): %v", dbconfig.DisplaySource(dbCfg), err)
	} else {
		usageStatsStore = db
		log.Printf("Usage stats db (%s): %s", dbCfg.Driver, dbconfig.DisplaySource(dbCfg))
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

	// 仅校验端口合法性：端口是否被占用交给 ProxyServer.Start() 处理（失败只影响代理服务，不阻塞 UI 启动）。
	if err := config.ValidatePort(port); err != nil {
		log.Fatalf("启动失败: %v\n请检查端口配置是否正确(1-65535)", err)
	}

	// Load listen address configuration
	listenAddr := "0.0.0.0"
	if savedAddr, err := store.GetConfig(ConfigKeyListenAddr); err == nil && savedAddr != "" {
		if err := config.ValidateListenAddr(savedAddr); err != nil {
			log.Printf("Warning: Invalid listen address '%s': %v, using default 0.0.0.0", savedAddr, err)
		} else {
			listenAddr = savedAddr
			log.Printf("Using listen address from config: %s", listenAddr)
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

	// Initialize SSE hub for real-time updates
	sseHub := proxy.NewSSEHub()
	go sseHub.Run()

	// Initialize proxy server with SSE hub
	proxyServer := proxy.NewProxyServerWithSSEHub(port, router, sseHub)
	proxyServer.SetListenAddr(listenAddr)
	proxyServer.SetStorage(store)
	proxyServer.SetUsageStatsStore(usageStatsStore)
	proxyServer.SetDatabaseApplyFunc(func(cfg dbconfig.Config) error {
		if err := appdb.ApplyRuntimeDatabase(cfg, proxyServer); err != nil {
			return err
		}
		app.setUsageStatsStore(proxyServer.GetUsageStatsStore())
		return nil
	})
	proxyServer.SetConfigPath(configPath)

	// Wire up app before plugin init so plugins can trigger reload safely.
	app.SetStorage(store)
	app.SetProxyServer(proxyServer)
	app.SetRouter(router)
	app.SetSSEHub(sseHub)
	app.SetConfigLoader(configLoader)
	app.setUsageStatsStore(usageStatsStore)

	reloadOnce := func() {
		if err := app.ReloadConfig(); err != nil {
			log.Printf("Warning: reload config failed: %v", err)
		}
	}
	// Allow /reload and /sync/config to hot-reload runtime state in GUI mode too.
	// Without this, incoming server sync writes config to disk but leaves router/plugins stale until manual refresh.
	proxyServer.SetReloadFunc(reloadOnce)
	configGetter := func(key string) (string, error) {
		if strings.TrimSpace(key) == "configPath" {
			return configPath, nil
		}
		return store.GetConfig(key)
	}
	plugin.SetConfigGetter(configGetter)

	// Initialize all plugins
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

	if key, err := store.GetConfig(ConfigKeyAPIKey); err == nil {
		if key = strings.TrimSpace(key); key != "" {
			proxyServer.SetAuthKey(key)
		}
	}
	// Load fallback setting from config
	if fallbackStr, err := store.GetConfig(ConfigKeyFallback); err == nil && fallbackStr == "true" {
		proxyServer.SetFallbackEnabled(true)
	}

	// 启动代理服务，但不阻塞 UI 启动。
	// Requirements: 5.1
	log.Printf("Starting proxy server on %s:%d...", listenAddr, port)
	if err := app.StartProxy(); err != nil {
		log.Printf("Proxy server startup blocked: %v", err)
	}

	// Create Wails application with options
	// Requirements: 10.1, 10.2
	err = wails.Run(&options.App{
		Title:  "Cli Simple Hub",
		Width:  1200,
		Height: 800,
		SingleInstanceLock: &options.SingleInstanceLock{
			UniqueId: desktopSingleInstanceID,
			OnSecondInstanceLaunch: func(data options.SecondInstanceData) {
				log.Printf("Second instance blocked (cwd=%s args=%v)", data.WorkingDirectory, data.Args)
				if app.ctx != nil {
					wailsRuntime.WindowUnminimise(app.ctx)
					wailsRuntime.WindowShow(app.ctx)
				}
			},
		},
		AssetServer: &assetserver.Options{
			Assets: assets,
		},
		BackgroundColour: &options.RGBA{R: 27, G: 38, B: 54, A: 1},
		OnStartup:        app.startup,
		OnShutdown: func(ctx context.Context) {
			// Graceful shutdown
			// Requirements: 5.4
			log.Println("Shutting down...")
			if clashProvider := plugin.GetClashDesktopProvider(); clashProvider != nil {
				if err := clashProvider.Stop(); err != nil {
					log.Printf("Error stopping clash runtime: %v", err)
				}
			}
			sseHub.Stop()
			if err := proxyServer.Stop(); err != nil {
				log.Printf("Error stopping proxy server: %v", err)
			}
			if err := store.Close(); err != nil {
				log.Printf("Error closing storage: %v", err)
			}
			if statsStore := proxyServer.ReplaceUsageStatsStore(nil); statsStore != nil {
				if err := statsStore.Close(); err != nil {
					log.Printf("Error closing usage stats db: %v", err)
				}
			}
			if err := plugin.CloseAll(); err != nil {
				log.Printf("Error closing plugins: %v", err)
			}
			if err := sqlitequeue.CloseAll(); err != nil {
				log.Printf("Error closing sqlite backends: %v", err)
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
