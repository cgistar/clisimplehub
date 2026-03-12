package plugin

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
)

// KiroDesktopProvider is an optional interface for plugins that provide
// Kiro account management and authentication operations for the desktop GUI.
// Desktop discovers this via plugin.GetKiroDesktopProvider() + type assertion.
type KiroDesktopProvider interface {
	// --- Config path ---
	DefaultMultiConfigBasename() string
	GetProxyURL(configPath string) string

	// --- Defaults ---
	DefaultModelMapping() map[string]string
	DefaultUserAgent() string
	DefaultVersion() string

	// --- Config operations ---
	GetKiroConfig(configPath string) (json.RawMessage, error)
	SaveKiroConfig(configPath string, config json.RawMessage) (json.RawMessage, error)
	GetKiroGlobalConfig(configPath string) (json.RawMessage, error)
	SaveKiroGlobalConfig(configPath string, dto json.RawMessage) error

	// --- Account CRUD ---
	GetAccounts(configPath string) (json.RawMessage, error)
	GetActiveAccount(configPath string) (json.RawMessage, error)
	SetActiveAccount(configPath, refreshToken string) error
	AddAccount(configPath string, dto json.RawMessage) (json.RawMessage, error)
	UpdateAccount(configPath string, dto json.RawMessage) error
	DeleteAccount(configPath, refreshToken string) error
	DeleteAccounts(configPath string, refreshTokens []string) error

	// --- Auth testing ---
	TestRefreshToken(config json.RawMessage) (json.RawMessage, error)
	TestAccount(configPath, refreshToken string) (json.RawMessage, error)

	// --- Usage ---
	GetKiroUsage(ctx context.Context, input json.RawMessage) (json.RawMessage, error)
	GetAccountUsage(ctx context.Context, configPath, refreshToken string) (json.RawMessage, error)

	// --- Backup/Restore ---
	SaveMultiConfigFromBackup(configPath string, dto json.RawMessage) error
	LoadMultiConfigDTO(configPath string) (json.RawMessage, error)

	// --- Utilities ---
	ComputeMachineID(refreshToken string) string
	NormalizeMachineID(mid string) string

	// --- Transformer operations ---
	SetCachedBufferedStream(v bool)
	SetCachedModelMapping(m map[string]string)
	ReloadAllTransformers() error

	// --- IDC operations ---
	IdcRegisterClient(proxyURL, region string) (clientId, clientSecret string, err error)
	IdcStartDeviceAuth(proxyURL, region string, req json.RawMessage) (json.RawMessage, error)
	IdcPollToken(proxyURL, region string, req json.RawMessage) (json.RawMessage, error)
	IdcRegisterAuthCodeClient(proxyURL, region, issuerUrl string) (clientId, clientSecret string, err error)
	IdcExchangeAuthCode(proxyURL, region string, req json.RawMessage) (json.RawMessage, error)
}

// KiroUsageHTTPError represents an HTTP error from the Kiro usage API.
type KiroUsageHTTPError struct {
	StatusCode int
	Body       string
}

func (e *KiroUsageHTTPError) Error() string {
	b, err := json.Marshal(struct {
		StatusCode int    `json:"statusCode"`
		Body       string `json:"body"`
	}{e.StatusCode, e.Body})
	if err == nil {
		return "KIRO_USAGE_HTTP_ERROR: " + string(b)
	}
	if e.StatusCode > 0 && strings.TrimSpace(e.Body) != "" {
		return fmt.Sprintf("usage request failed (HTTP %d): %s", e.StatusCode, strings.TrimSpace(e.Body))
	}
	if e.StatusCode > 0 {
		return fmt.Sprintf("usage request failed (HTTP %d)", e.StatusCode)
	}
	return "usage request failed"
}

// ByName returns the first registered plugin with the given name, or nil.
func ByName(name string) Plugin {
	mu.RLock()
	defer mu.RUnlock()
	for _, p := range plugins {
		if p.Name() == name {
			return p
		}
	}
	return nil
}

// GetKiroDesktopProvider returns the KiroDesktopProvider from the "kiro" plugin, or nil.
func GetKiroDesktopProvider() KiroDesktopProvider {
	p := ByName("kiro")
	if p == nil {
		return nil
	}
	if kp, ok := p.(KiroDesktopProvider); ok {
		return kp
	}
	return nil
}

// kiroDesktopProviderOnce caches the provider lookup result.
var (
	kiroDesktopProviderOnce sync.Once
	kiroDesktopProviderInst KiroDesktopProvider
)

// GetKiroDesktopProviderCached returns the cached KiroDesktopProvider (resolved once).
func GetKiroDesktopProviderCached() KiroDesktopProvider {
	kiroDesktopProviderOnce.Do(func() {
		kiroDesktopProviderInst = GetKiroDesktopProvider()
	})
	return kiroDesktopProviderInst
}
