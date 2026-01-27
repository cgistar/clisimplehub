package storage

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"

	"clisimplehub/internal/config"
	"strings"
)

type ConfigFileStore struct {
	loader *config.ConfigLoader
	mu     sync.Mutex
}

func kiroAllEmpty(cfg *config.KiroConfig) bool {
	if cfg == nil {
		return true
	}
	return strings.TrimSpace(cfg.ProxyURL) == "" &&
		strings.TrimSpace(cfg.UserAgent) == "" &&
		strings.TrimSpace(cfg.Version) == "" &&
		strings.TrimSpace(cfg.MachineID) == ""
}

func NewConfigFileStore(loader *config.ConfigLoader) (*ConfigFileStore, error) {
	if loader == nil {
		return nil, errors.New("config loader is nil")
	}

	store := &ConfigFileStore{loader: loader}
	if err := store.ensureFileExists(); err != nil {
		return nil, err
	}
	return store, nil
}

func (s *ConfigFileStore) Close() error { return nil }

func (s *ConfigFileStore) GetVendors() ([]*Vendor, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	cfg, err := s.loadAndNormalizeLocked()
	if err != nil {
		return nil, err
	}

	vendors := make([]*Vendor, 0, len(cfg.Vendors))
	for _, v := range cfg.Vendors {
		vendors = append(vendors, &Vendor{
			ID:      v.ID,
			Name:    v.Name,
			HomeURL: v.HomeURL,
			APIURL:  v.APIURL,
			Remark:  v.Remark,
		})
	}

	sort.Slice(vendors, func(i, j int) bool {
		return vendors[i].Name < vendors[j].Name
	})
	return vendors, nil
}

func (s *ConfigFileStore) GetVendorByID(id int64) (*Vendor, error) {
	if id <= 0 {
		return nil, nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	cfg, err := s.loadAndNormalizeLocked()
	if err != nil {
		return nil, err
	}
	for _, v := range cfg.Vendors {
		if v.ID == id {
			return &Vendor{
				ID:      v.ID,
				Name:    v.Name,
				HomeURL: v.HomeURL,
				APIURL:  v.APIURL,
				Remark:  v.Remark,
			}, nil
		}
	}
	return nil, nil
}

func (s *ConfigFileStore) SaveVendor(vendor *Vendor) error {
	if vendor == nil {
		return errors.New("vendor is nil")
	}
	if vendor.Name == "" {
		return errors.New("vendor name is required")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	cfg, err := s.loadAndNormalizeLocked()
	if err != nil {
		return err
	}

	if vendor.ID <= 0 {
		vendor.ID = nextVendorID(cfg)
		cfg.Vendors = append(cfg.Vendors, config.VendorConfig{
			ID:      vendor.ID,
			Name:    vendor.Name,
			HomeURL: vendor.HomeURL,
			APIURL:  vendor.APIURL,
			Remark:  vendor.Remark,
		})
		return s.saveLocked(cfg)
	}

	for i := range cfg.Vendors {
		if cfg.Vendors[i].ID != vendor.ID {
			continue
		}

		cfg.Vendors[i].Name = vendor.Name
		cfg.Vendors[i].HomeURL = vendor.HomeURL
		cfg.Vendors[i].APIURL = vendor.APIURL
		cfg.Vendors[i].Remark = vendor.Remark
		return s.saveLocked(cfg)
	}

	return fmt.Errorf("vendor not found: %d", vendor.ID)
}

func (s *ConfigFileStore) DeleteVendor(id int64) error {
	if id <= 0 {
		return nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	cfg, err := s.loadAndNormalizeLocked()
	if err != nil {
		return err
	}

	kept := cfg.Vendors[:0]
	for _, v := range cfg.Vendors {
		if v.ID == id {
			continue
		}
		kept = append(kept, v)
	}
	cfg.Vendors = kept
	return s.saveLocked(cfg)
}

func (s *ConfigFileStore) GetEndpoints() ([]*Endpoint, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	cfg, err := s.loadAndNormalizeLocked()
	if err != nil {
		return nil, err
	}

	return flattenEndpoints(cfg), nil
}

func (s *ConfigFileStore) GetEndpointsByType(interfaceType string) ([]*Endpoint, error) {
	endpoints, err := s.GetEndpoints()
	if err != nil {
		return nil, err
	}

	result := make([]*Endpoint, 0)
	for _, ep := range endpoints {
		if ep.InterfaceType == interfaceType {
			result = append(result, ep)
		}
	}

	sort.Slice(result, func(i, j int) bool {
		if result[i].Priority != result[j].Priority {
			return result[i].Priority < result[j].Priority
		}
		return result[i].Name < result[j].Name
	})
	return result, nil
}

func (s *ConfigFileStore) GetEndpointByID(id int64) (*Endpoint, error) {
	if id <= 0 {
		return nil, nil
	}
	endpoints, err := s.GetEndpoints()
	if err != nil {
		return nil, err
	}
	for _, ep := range endpoints {
		if ep.ID == id {
			return ep, nil
		}
	}
	return nil, nil
}

func (s *ConfigFileStore) SaveEndpoint(endpoint *Endpoint) error {
	if endpoint == nil {
		return errors.New("endpoint is nil")
	}
	if endpoint.Name == "" {
		return errors.New("endpoint name is required")
	}
	if endpoint.APIURL == "" {
		return errors.New("endpoint api_url is required")
	}
	if endpoint.ID <= 0 && endpoint.APIKey == "" {
		return errors.New("endpoint api_key is required")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	cfg, err := s.loadAndNormalizeLocked()
	if err != nil {
		return err
	}

	if endpoint.ID <= 0 {
		endpoint.ID = nextEndpointID(cfg)
		if err := addEndpointToConfig(cfg, endpoint); err != nil {
			return err
		}
		return s.saveLocked(cfg)
	}

	found, err := updateEndpointByID(cfg, endpoint)
	if err != nil {
		return err
	}
	if !found {
		return fmt.Errorf("endpoint not found: %d", endpoint.ID)
	}
	return s.saveLocked(cfg)
}

func (s *ConfigFileStore) UpdateEndpoint(endpoint *Endpoint) error {
	if endpoint == nil {
		return errors.New("endpoint is nil")
	}
	if endpoint.ID <= 0 {
		return errors.New("endpoint id is required")
	}
	return s.SaveEndpoint(endpoint)
}

func (s *ConfigFileStore) DeleteEndpoint(id int64) error {
	if id <= 0 {
		return nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	cfg, err := s.loadAndNormalizeLocked()
	if err != nil {
		return err
	}

	// 从顶层 endpoints 中删除
	kept := cfg.Endpoints[:0]
	for _, ep := range cfg.Endpoints {
		if ep.ID == id {
			continue
		}
		kept = append(kept, ep)
	}
	cfg.Endpoints = kept

	return s.saveLocked(cfg)
}

func (s *ConfigFileStore) GetConfig(key string) (string, error) {
	if key == "" {
		return "", nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	cfg, err := s.loadAndNormalizeLocked()
	if err != nil {
		return "", err
	}

	switch key {
	case "kiro.proxyUrl":
		if cfg.Kiro != nil {
			return cfg.Kiro.ProxyURL, nil
		}
	case "kiro.userAgent":
		if cfg.Kiro != nil {
			return cfg.Kiro.UserAgent, nil
		}
	case "kiro.version":
		if cfg.Kiro != nil {
			return cfg.Kiro.Version, nil
		}
	case "kiro.machineId":
		if cfg.Kiro != nil {
			return cfg.Kiro.MachineID, nil
		}
	}

	if cfg.AppConfigKV == nil {
		return "", nil
	}
	val := cfg.AppConfigKV[key]
	if val == nil {
		return "", nil
	}
	switch v := val.(type) {
	case string:
		return v, nil
	case bool:
		if v {
			return "true", nil
		}
		return "false", nil
	case float64:
		return fmt.Sprintf("%.0f", v), nil
	default:
		return fmt.Sprintf("%v", v), nil
	}
}

func (s *ConfigFileStore) SetConfig(key, value string) error {
	if key == "" {
		return nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	cfg, err := s.loadAndNormalizeLocked()
	if err != nil {
		return err
	}

	switch key {
	case "kiro.proxyUrl":
		if cfg.Kiro == nil {
			cfg.Kiro = &config.KiroConfig{}
		}
		cfg.Kiro.ProxyURL = strings.TrimSpace(value)
		if kiroAllEmpty(cfg.Kiro) {
			cfg.Kiro = nil
		}
		return s.saveLocked(cfg)
	case "kiro.userAgent":
		if cfg.Kiro == nil {
			cfg.Kiro = &config.KiroConfig{}
		}
		cfg.Kiro.UserAgent = strings.TrimSpace(value)
		if kiroAllEmpty(cfg.Kiro) {
			cfg.Kiro = nil
		}
		return s.saveLocked(cfg)
	case "kiro.version":
		if cfg.Kiro == nil {
			cfg.Kiro = &config.KiroConfig{}
		}
		cfg.Kiro.Version = strings.TrimSpace(value)
		if kiroAllEmpty(cfg.Kiro) {
			cfg.Kiro = nil
		}
		return s.saveLocked(cfg)
	case "kiro.machineId":
		if cfg.Kiro == nil {
			cfg.Kiro = &config.KiroConfig{}
		}
		cfg.Kiro.MachineID = strings.TrimSpace(value)
		if kiroAllEmpty(cfg.Kiro) {
			cfg.Kiro = nil
		}
		return s.saveLocked(cfg)
	}

	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		if cfg.AppConfigKV != nil {
			delete(cfg.AppConfigKV, key)
			if len(cfg.AppConfigKV) == 0 {
				cfg.AppConfigKV = nil
			}
		}
		return s.saveLocked(cfg)
	}

	if cfg.AppConfigKV == nil {
		cfg.AppConfigKV = make(map[string]interface{})
	}
	cfg.AppConfigKV[key] = trimmed
	return s.saveLocked(cfg)
}

func (s *ConfigFileStore) SetConfigBool(key string, value bool) error {
	if key == "" {
		return nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	cfg, err := s.loadAndNormalizeLocked()
	if err != nil {
		return err
	}
	if cfg.AppConfigKV == nil {
		cfg.AppConfigKV = make(map[string]interface{})
	}
	cfg.AppConfigKV[key] = value
	return s.saveLocked(cfg)
}

func (s *ConfigFileStore) ensureFileExists() error {
	path := s.loader.GetPath()
	if path == "" {
		return errors.New("config path is empty")
	}

	_, statErr := os.Stat(path)
	if statErr == nil {
		return nil
	}
	if !os.IsNotExist(statErr) {
		return fmt.Errorf("failed to stat config file: %w", statErr)
	}

	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return fmt.Errorf("failed to create config dir: %w", err)
	}
	empty := &config.AppConfig{Vendors: []config.VendorConfig{}}
	data, err := empty.ToJSON()
	if err != nil {
		return fmt.Errorf("failed to serialize empty config: %w", err)
	}
	if err := os.WriteFile(path, data, 0644); err != nil {
		return fmt.Errorf("failed to create config file: %w", err)
	}
	return nil
}

func (s *ConfigFileStore) loadAndNormalizeLocked() (*config.AppConfig, error) {
	if err := s.ensureFileExists(); err != nil {
		return nil, err
	}

	cfg, err := s.loader.Load()
	if err != nil {
		return nil, err
	}

	changed := ensureIDs(cfg)
	if cfg.Kiro != nil && kiroAllEmpty(cfg.Kiro) {
		cfg.Kiro = nil
		changed = true
	}
	if changed {
		_ = s.saveLocked(cfg)
	}
	return cfg, nil
}

func (s *ConfigFileStore) saveLocked(cfg *config.AppConfig) error {
	if cfg == nil {
		return nil
	}
	data, err := cfg.ToJSON()
	if err != nil {
		return fmt.Errorf("failed to serialize config: %w", err)
	}
	path := s.loader.GetPath()
	if path == "" {
		return errors.New("config path is empty")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return fmt.Errorf("failed to create config dir: %w", err)
	}
	if err := os.WriteFile(path, data, 0644); err != nil {
		return fmt.Errorf("failed to write config file: %w", err)
	}
	return nil
}

func ensureIDs(cfg *config.AppConfig) bool {
	if cfg == nil {
		return false
	}

	changed := false
	nextVendor := int64(1)
	nextEndpoint := int64(1)

	// 找到最大的 vendor ID 和 endpoint ID
	for _, v := range cfg.Vendors {
		if v.ID >= nextVendor {
			nextVendor = v.ID + 1
		}
	}
	for _, ep := range cfg.Endpoints {
		if ep.ID >= nextEndpoint {
			nextEndpoint = ep.ID + 1
		}
	}

	// 为没有 ID 的 vendor 分配 ID
	for vi := range cfg.Vendors {
		if cfg.Vendors[vi].ID == 0 {
			cfg.Vendors[vi].ID = nextVendor
			nextVendor++
			changed = true
		}
	}

	// 为没有 ID 的 endpoint 分配 ID
	for ei := range cfg.Endpoints {
		if cfg.Endpoints[ei].ID == 0 {
			cfg.Endpoints[ei].ID = nextEndpoint
			nextEndpoint++
			changed = true
		}
	}

	return changed
}

func nextVendorID(cfg *config.AppConfig) int64 {
	maxID := int64(0)
	for _, v := range cfg.Vendors {
		if v.ID > maxID {
			maxID = v.ID
		}
	}
	return maxID + 1
}

func nextEndpointID(cfg *config.AppConfig) int64 {
	maxID := int64(0)
	for _, ep := range cfg.Endpoints {
		if ep.ID > maxID {
			maxID = ep.ID
		}
	}
	return maxID + 1
}

func flattenEndpoints(cfg *config.AppConfig) []*Endpoint {
	if cfg == nil {
		return []*Endpoint{}
	}

	out := make([]*Endpoint, 0, len(cfg.Endpoints))

	for _, ep := range cfg.Endpoints {
		// Convert config.ModelMapping to storage.ModelMapping
		var models []ModelMapping
		for _, m := range ep.Models {
			models = append(models, ModelMapping{
				Name:  m.Name,
				Alias: m.Alias,
			})
		}

		out = append(out, &Endpoint{
			ID:            ep.ID,
			Name:          ep.Name,
			APIURL:        ep.APIURL,
			APIKey:        ep.APIKey,
			Active:        ep.Active,
			Enabled:       ep.Enabled,
			InterfaceType: ep.InterfaceType,
			Transformer:   ep.Transformer,
			ProviderName:  ep.ProviderName,
			Model:         ep.Model,
			Remark:        ep.Remark,
			Priority:      ep.Priority,
			ProxyURL:      ep.ProxyURL,
			Models:        models,
			Headers:       ep.Headers,
		})
	}
	return out
}

func addEndpointToConfig(cfg *config.AppConfig, endpoint *Endpoint) error {
	// Convert storage.ModelMapping to config.ModelMapping
	var models []config.ModelMapping
	for _, m := range endpoint.Models {
		models = append(models, config.ModelMapping{
			Name:  m.Name,
			Alias: m.Alias,
		})
	}

	// 添加到顶层 endpoints
	cfg.Endpoints = append(cfg.Endpoints, config.EndpointConfig{
		ID:            endpoint.ID,
		Name:          endpoint.Name,
		APIURL:        endpoint.APIURL,
		APIKey:        endpoint.APIKey,
		Active:        endpoint.Active,
		Enabled:       endpoint.Enabled,
		InterfaceType: endpoint.InterfaceType,
		Transformer:   endpoint.Transformer,
		Model:         endpoint.Model,
		Remark:        endpoint.Remark,
		Priority:      endpoint.Priority,
		ProxyURL:      endpoint.ProxyURL,
		Models:        models,
		Headers:       endpoint.Headers,
		ProviderName:  endpoint.ProviderName,
	})
	return nil
}

func updateEndpointByID(cfg *config.AppConfig, endpoint *Endpoint) (bool, error) {
	// Convert storage.ModelMapping to config.ModelMapping
	var models []config.ModelMapping
	for _, m := range endpoint.Models {
		models = append(models, config.ModelMapping{
			Name:  m.Name,
			Alias: m.Alias,
		})
	}

	// 在顶层 endpoints 中查找并更新
	for i := range cfg.Endpoints {
		if cfg.Endpoints[i].ID != endpoint.ID {
			continue
		}

		cfg.Endpoints[i].Name = endpoint.Name
		cfg.Endpoints[i].APIURL = endpoint.APIURL
		if endpoint.APIKey != "" {
			cfg.Endpoints[i].APIKey = endpoint.APIKey
		}
		cfg.Endpoints[i].Active = endpoint.Active
		cfg.Endpoints[i].Enabled = endpoint.Enabled
		cfg.Endpoints[i].InterfaceType = endpoint.InterfaceType
		cfg.Endpoints[i].Transformer = endpoint.Transformer
		cfg.Endpoints[i].Model = endpoint.Model
		cfg.Endpoints[i].Remark = endpoint.Remark
		cfg.Endpoints[i].Priority = endpoint.Priority
		cfg.Endpoints[i].ProxyURL = endpoint.ProxyURL
		cfg.Endpoints[i].Models = models
		cfg.Endpoints[i].Headers = endpoint.Headers
		cfg.Endpoints[i].ProviderName = endpoint.ProviderName
		return true, nil
	}

	return false, nil
}
