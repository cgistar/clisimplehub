// Package config handles application configuration loading and validation.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

const (
	// ConfigFileName is the default configuration file name
	ConfigFileName = "config.json"
	// ConfigDirName is the directory name under user home
	ConfigDirName = ".clisimplehub"
	// EnvDataDir is the environment variable for data directory (highest priority)
	EnvDataDir = "DATA"
	// EnvPort is the environment variable for proxy port (highest priority)
	EnvPort = "PORT"
)

// Validation errors
var (
	ErrInvalidPort         = errors.New("port must be between 1 and 65535")
	ErrEmptyEndpointName   = errors.New("endpoint name is required")
	ErrEmptyEndpointAPIURL = errors.New("endpoint apiUrl is required")
	ErrEmptyEndpointAPIKey = errors.New("endpoint apiKey is required")
)

// VendorConfig represents vendor configuration in JSON
type VendorConfig struct {
	ID        int64            `json:"id,omitempty"`
	Name      string           `json:"name"`
	HomeURL   string           `json:"homeUrl"`
	APIURL    string           `json:"apiUrl"`
	Remark    string           `json:"remark,omitempty"`
	Endpoints []EndpointConfig `json:"endpoints"`
}

// EndpointConfig represents endpoint configuration in JSON
type EndpointConfig struct {
	ID            int64             `json:"id,omitempty"`
	Name          string            `json:"name"`
	APIURL        string            `json:"apiUrl"`
	APIKey        string            `json:"apiKey"`
	Active        bool              `json:"active"`
	Enabled       bool              `json:"enabled"`
	InterfaceType string            `json:"interfaceType"`
	Transformer   string            `json:"transformer,omitempty"`
	Model         string            `json:"model,omitempty"`
	Remark        string            `json:"remark,omitempty"`
	Priority      int               `json:"priority,omitempty"`
	ProxyURL      string            `json:"proxyUrl,omitempty"`
	Models        []ModelMapping    `json:"models,omitempty"`
	Headers       map[string]string `json:"headers,omitempty"`
}

// ModelMapping represents a model name mapping configuration
type ModelMapping struct {
	Name  string `json:"name"`  // 实际模型名（上游模型名）
	Alias string `json:"alias"` // API 使用的别名（客户端传入的 model）
}

// AppConfig represents the complete application configuration
type AppConfig struct {
	AppConfigKV map[string]interface{} `json:"appConfig,omitempty"`
	Vendors     []VendorConfig         `json:"vendors"`
}

// ConfigLoader handles loading configuration from JSON file
type ConfigLoader struct {
	path string
}

// NewConfigLoader creates a new ConfigLoader with the specified path
// If path is empty, it will search for config.json in the following order:
// 1. Current directory
// 2. User home directory under .clishub
// 3. If not found, create in user home directory under .clishub
func NewConfigLoader(path string) *ConfigLoader {
	if path == "" {
		path = FindOrCreateConfigPath()
	}
	return &ConfigLoader{path: path}
}

// GetDataDir returns the data directory path
// Priority order:
// 1. CODESP_DATA environment variable (highest priority)
// 2. Current directory (if config.json exists)
// 3. User home directory under .clishub
func GetDataDir() string {
	// 1. Check environment variable (highest priority)
	if envDir := os.Getenv(EnvDataDir); envDir != "" {
		// Ensure directory exists
		if err := os.MkdirAll(envDir, 0755); err == nil {
			return envDir
		}
	}

	// 2. Check current directory
	if fileExists(ConfigFileName) {
		cwd, err := os.Getwd()
		if err == nil {
			return cwd
		}
	}

	// 3. Use user home directory
	homeDir, err := os.UserHomeDir()
	if err == nil {
		configDir := filepath.Join(homeDir, ConfigDirName)
		os.MkdirAll(configDir, 0755)
		return configDir
	}

	// Fallback to current directory
	return "."
}

// GetPortFromEnv returns the port from environment variable if set
// Returns 0 if not set or invalid
func GetPortFromEnv() int {
	if envPort := os.Getenv(EnvPort); envPort != "" {
		if port, err := strconv.Atoi(envPort); err == nil && port >= 1 && port <= 65535 {
			return port
		}
	}
	return 0
}

// FindOrCreateConfigPath finds existing config.json or creates a new one
// Priority order:
// 1. CODESP_DATA environment variable directory
// 2. Current directory (./config.json)
// 3. User home directory (~/.clishub/config.json)
// 4. If not found, create in the determined data directory
func FindOrCreateConfigPath() string {
	dataDir := GetDataDir()
	configPath := filepath.Join(dataDir, ConfigFileName)

	// If config exists in data dir, return it
	if fileExists(configPath) {
		return configPath
	}

	// Check current directory as fallback (if not using env var)
	if os.Getenv(EnvDataDir) == "" {
		currentDirPath := ConfigFileName
		if fileExists(currentDirPath) {
			return currentDirPath
		}
	}

	// Create config file in data directory
	if err := os.MkdirAll(dataDir, 0755); err == nil {
		emptyConfig := &AppConfig{Vendors: []VendorConfig{}}
		if data, err := emptyConfig.ToJSON(); err == nil {
			_ = os.WriteFile(configPath, data, 0644)
		}
	}

	return configPath
}

// GetConfigDir returns the config directory path in user home
func GetConfigDir() (string, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("failed to get user home directory: %w", err)
	}
	return filepath.Join(homeDir, ConfigDirName), nil
}

// fileExists checks if a file exists
func fileExists(path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	return !info.IsDir()
}

// GetPath returns the current configuration file path
func (c *ConfigLoader) GetPath() string {
	return c.path
}

// SetPath sets the configuration file path
func (c *ConfigLoader) SetPath(path string) {
	c.path = path
}

// Load reads and parses the configuration from the JSON file
func (c *ConfigLoader) Load() (*AppConfig, error) {
	if c.path == "" {
		return nil, errors.New("config path is not set")
	}

	data, err := os.ReadFile(c.path)
	if err != nil {
		return nil, fmt.Errorf("failed to read config file: %w", err)
	}

	var config AppConfig
	if err := json.Unmarshal(data, &config); err != nil {
		return nil, fmt.Errorf("failed to parse config JSON: %w", err)
	}

	return &config, nil
}

// LoadAndValidate reads, parses, and validates the configuration
func (c *ConfigLoader) LoadAndValidate() (*AppConfig, []error) {
	config, err := c.Load()
	if err != nil {
		return nil, []error{err}
	}

	var validationErrors []error
	for i, vendor := range config.Vendors {
		for j, endpoint := range vendor.Endpoints {
			if errs := ValidateEndpoint(&endpoint); len(errs) > 0 {
				for _, e := range errs {
					validationErrors = append(validationErrors,
						fmt.Errorf("vendor[%d].endpoints[%d]: %w", i, j, e))
				}
			}
		}
	}

	return config, validationErrors
}

// ParseJSON parses JSON data into AppConfig
func ParseJSON(data []byte) (*AppConfig, error) {
	var config AppConfig
	if err := json.Unmarshal(data, &config); err != nil {
		return nil, fmt.Errorf("failed to parse config JSON: %w", err)
	}
	return &config, nil
}

// ToJSON serializes AppConfig to JSON
func (c *AppConfig) ToJSON() ([]byte, error) {
	return json.MarshalIndent(c, "", "  ")
}

// ValidatePort checks if a port number is valid (1-65535)
func ValidatePort(port int) error {
	if port < 1 || port > 65535 {
		return ErrInvalidPort
	}
	return nil
}

// ValidateEndpoint validates an endpoint configuration
// Returns a slice of validation errors (empty if valid)
func ValidateEndpoint(endpoint *EndpointConfig) []error {
	var errs []error

	if strings.TrimSpace(endpoint.Name) == "" {
		errs = append(errs, ErrEmptyEndpointName)
	}
	if strings.TrimSpace(endpoint.APIURL) == "" {
		errs = append(errs, ErrEmptyEndpointAPIURL)
	}
	if strings.TrimSpace(endpoint.APIKey) == "" {
		errs = append(errs, ErrEmptyEndpointAPIKey)
	}

	return errs
}

// IsValidEndpoint returns true if the endpoint has all required fields
func IsValidEndpoint(endpoint *EndpointConfig) bool {
	return len(ValidateEndpoint(endpoint)) == 0
}

// ConfigSyncer handles syncing configuration between JSON file and database
type ConfigSyncer struct {
	loader  *ConfigLoader
	storage Storage
}

// Storage interface for config syncing (subset of storage.Storage)
type Storage interface {
	GetVendors() ([]*Vendor, error)
	SaveVendor(vendor *Vendor) error
	GetEndpoints() ([]*Endpoint, error)
	SaveEndpoint(endpoint *Endpoint) error
	GetConfig(key string) (string, error)
	SetConfig(key, value string) error
}

// Vendor represents an API vendor for config syncing
type Vendor struct {
	ID      int64  `json:"id"`
	Name    string `json:"name"`
	HomeURL string `json:"homeUrl"`
	APIURL  string `json:"apiUrl"`
	Remark  string `json:"remark,omitempty"`
}

// Endpoint represents an API endpoint for config syncing
type Endpoint struct {
	ID            int64  `json:"id"`
	Name          string `json:"name"`
	APIURL        string `json:"apiUrl"`
	APIKey        string `json:"apiKey"`
	Active        bool   `json:"active"`
	Enabled       bool   `json:"enabled"`
	InterfaceType string `json:"interfaceType"`
	VendorID      int64  `json:"vendorId"`
	Model         string `json:"model,omitempty"`
	Remark        string `json:"remark,omitempty"`
	Priority      int    `json:"priority,omitempty"`
}

// NewConfigSyncer creates a new ConfigSyncer
func NewConfigSyncer(loader *ConfigLoader, storage Storage) *ConfigSyncer {
	return &ConfigSyncer{
		loader:  loader,
		storage: storage,
	}
}

// SyncToDatabase loads configuration from JSON and saves to database
// Returns the number of vendors and endpoints synced, and any errors encountered
func (s *ConfigSyncer) SyncToDatabase() (vendorCount, endpointCount int, err error) {
	config, loadErr := s.loader.Load()
	if loadErr != nil {
		// Handle malformed JSON gracefully - log and continue with existing data
		return 0, 0, fmt.Errorf("failed to load config: %w", loadErr)
	}

	// Sync vendors and their endpoints
	for _, vendorConfig := range config.Vendors {
		vendor := &Vendor{
			Name:    vendorConfig.Name,
			HomeURL: vendorConfig.HomeURL,
			APIURL:  vendorConfig.APIURL,
			Remark:  vendorConfig.Remark,
		}

		if err := s.storage.SaveVendor(vendor); err != nil {
			return vendorCount, endpointCount, fmt.Errorf("failed to save vendor %s: %w", vendor.Name, err)
		}
		vendorCount++

		// Sync endpoints for this vendor
		for _, endpointConfig := range vendorConfig.Endpoints {
			// Skip invalid endpoints
			if !IsValidEndpoint(&endpointConfig) {
				continue
			}

			// Default priority to 5 if not set
			priority := endpointConfig.Priority
			if priority == 0 {
				priority = 5
			}

			endpoint := &Endpoint{
				Name:          endpointConfig.Name,
				APIURL:        endpointConfig.APIURL,
				APIKey:        endpointConfig.APIKey,
				Active:        endpointConfig.Active,
				Enabled:       endpointConfig.Enabled,
				InterfaceType: endpointConfig.InterfaceType,
				VendorID:      vendor.ID,
				Model:         endpointConfig.Model,
				Remark:        endpointConfig.Remark,
				Priority:      priority,
			}

			if err := s.storage.SaveEndpoint(endpoint); err != nil {
				return vendorCount, endpointCount, fmt.Errorf("failed to save endpoint %s: %w", endpoint.Name, err)
			}
			endpointCount++
		}
	}

	return vendorCount, endpointCount, nil
}

// SyncToFile exports database configuration to JSON file
func (s *ConfigSyncer) SyncToFile() error {
	vendors, err := s.storage.GetVendors()
	if err != nil {
		return fmt.Errorf("failed to get vendors: %w", err)
	}

	endpoints, err := s.storage.GetEndpoints()
	if err != nil {
		return fmt.Errorf("failed to get endpoints: %w", err)
	}

	// Build vendor map for endpoint lookup
	vendorEndpoints := make(map[int64][]EndpointConfig)
	for _, ep := range endpoints {
		vendorEndpoints[ep.VendorID] = append(vendorEndpoints[ep.VendorID], EndpointConfig{
			Name:          ep.Name,
			APIURL:        ep.APIURL,
			APIKey:        ep.APIKey,
			Active:        ep.Active,
			Enabled:       ep.Enabled,
			InterfaceType: ep.InterfaceType,
			Model:         ep.Model,
			Remark:        ep.Remark,
			Priority:      ep.Priority,
		})
	}

	// Build config
	config := &AppConfig{
		Vendors: make([]VendorConfig, 0, len(vendors)),
	}

	for _, v := range vendors {
		config.Vendors = append(config.Vendors, VendorConfig{
			Name:      v.Name,
			HomeURL:   v.HomeURL,
			APIURL:    v.APIURL,
			Remark:    v.Remark,
			Endpoints: vendorEndpoints[v.ID],
		})
	}

	// Write to file
	data, err := config.ToJSON()
	if err != nil {
		return fmt.Errorf("failed to serialize config: %w", err)
	}

	if err := os.WriteFile(s.loader.path, data, 0644); err != nil {
		return fmt.Errorf("failed to write config file: %w", err)
	}

	return nil
}
