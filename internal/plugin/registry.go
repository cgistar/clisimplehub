package plugin

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"

	"clisimplehub/internal/storage"
)

// Plugin defines the interface that all plugins must implement.
type Plugin interface {
	Name() string
	Init(cfg InitConfig) error
	RegisterRoutes(r RouteRegistrar)
	Reload() error
}

// InitConfig provides configuration data to plugins during initialization.
type InitConfig struct {
	ConfigPath    string
	ConfigGetter  func(key string) (string, error)
	Storage       storage.Storage
	TriggerReload func()
}

// RouteRegistrar allows plugins to register HTTP routes.
type RouteRegistrar interface {
	HandleFunc(pattern string, handler http.HandlerFunc)
	RequireAuth(handler http.HandlerFunc) http.HandlerFunc
	RequireAuthStrict(handler http.HandlerFunc) http.HandlerFunc
}

// ForwardRequestMiddleware mutates outbound proxy request body before forwarding.
// It can also modify req metadata (e.g. path/header) in-place.
type ForwardRequestMiddleware func(ctx context.Context, req *http.Request, body []byte) ([]byte, error)

// ForwardMiddlewareRegistrar allows plugins to register forward middlewares.
type ForwardMiddlewareRegistrar interface {
	UseForwardRequestMiddleware(name string, middleware ForwardRequestMiddleware)
}

// ForwardMiddlewareProvider is an optional interface for plugins that contribute
// request mutation middlewares before upstream forwarding.
type ForwardMiddlewareProvider interface {
	RegisterForwardMiddlewares(r ForwardMiddlewareRegistrar)
}

// TokenEstimator is an optional interface for plugins that provide token estimation.
type TokenEstimator interface {
	EstimateInputTokens(body []byte) int
}

// TransformerRoundTripperProvider is an optional interface for plugins that provide upstream round-trippers.
type TransformerRoundTripperProvider interface {
	TransformerRoundTripperSpecs() []string
}

// ConfigSyncExporter is an optional interface for plugins that export config for sync.
type ConfigSyncExporter interface {
	SyncExport(configPath string) (fieldName string, data json.RawMessage, err error)
}

// ConfigSyncImporter is an optional interface for plugins that import synced config.
type ConfigSyncImporter interface {
	SyncImport(configPath string, data json.RawMessage) error
}

// ConfigSyncDecoder is an optional interface for plugins that decode encoded sync payloads.
type ConfigSyncDecoder interface {
	SyncDecode(encoded string) (json.RawMessage, error)
}

var (
	mu      sync.RWMutex
	plugins []Plugin
)

// Register adds a plugin to the global registry.
func Register(p Plugin) {
	mu.Lock()
	defer mu.Unlock()
	plugins = append(plugins, p)
}

// All returns all registered plugins.
func All() []Plugin {
	mu.RLock()
	defer mu.RUnlock()
	out := make([]Plugin, len(plugins))
	copy(out, plugins)
	return out
}
