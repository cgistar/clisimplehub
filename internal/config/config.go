// Package config handles application configuration loading and validation.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
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
	ID      int64  `json:"id,omitempty"`
	Name    string `json:"name"`
	HomeURL string `json:"homeUrl"`
	APIURL  string `json:"apiUrl"`
	Remark  string `json:"remark,omitempty"`
}

// EndpointConfig represents endpoint configuration in JSON
type EndpointConfig struct {
	ID            int64             `json:"id,omitempty"`
	Name          string            `json:"name"`
	ProviderName  string            `json:"providerName,omitempty"`
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

// KiroConfig represents top-level Kiro settings in config.json.
type KiroConfig struct {
	ProxyURL       string `json:"proxyUrl,omitempty"`
	UserAgent      string `json:"userAgent,omitempty"`
	Version        string `json:"version,omitempty"`
	MachineID      string `json:"machineId,omitempty"`
	BufferedStream bool   `json:"bufferedStream,omitempty"`
}

// AppConfig represents the complete application configuration
type AppConfig struct {
	AppConfigKV map[string]interface{} `json:"appConfig,omitempty"`
	Kiro        *KiroConfig            `json:"kiro,omitempty"`
	Vendors     []VendorConfig         `json:"vendors"`
	Endpoints   []EndpointConfig       `json:"endpoints,omitempty"`
}

// ConfigLoader handles loading configuration from JSON file
type ConfigLoader struct {
	path string
}

// NewConfigLoader creates a new ConfigLoader with the specified path
// If path is empty, it will search for config.json in the following order:
// 1. Current directory
// 2. User home directory under .clisimplehub
// 3. If not found, create in user home directory under .clisimplehub
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
// 3. User home directory under .clisimplehub
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
// 3. User home directory (~/.clisimplehub/config.json)
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

	// 验证顶层 endpoints
	for i, endpoint := range config.Endpoints {
		if errs := ValidateEndpoint(&endpoint); len(errs) > 0 {
			for _, e := range errs {
				validationErrors = append(validationErrors,
					fmt.Errorf("endpoints[%d]: %w", i, e))
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


// IsPortAvailable 检查端口是否可用
func IsPortAvailable(port int) error {
	if err := ValidatePort(port); err != nil {
		return err
	}

	addr := fmt.Sprintf(":%d", port)
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("端口 %d 已被占用或无法使用: %w", port, err)
	}
	listener.Close()
	return nil
}

// ValidateListenAddr validates a listen address
// Only allows 127.0.0.1, 0.0.0.0, ::1, and ::
func ValidateListenAddr(addr string) error {
	addr = strings.TrimSpace(addr)
	if addr == "" {
		return errors.New("listen address cannot be empty")
	}

	// Allow specific addresses only
	switch addr {
	case "127.0.0.1", "0.0.0.0", "::1", "::":
		return nil
	default:
		return fmt.Errorf("invalid listen address '%s': only 127.0.0.1, 0.0.0.0, ::1, and :: are allowed", addr)
	}
}
