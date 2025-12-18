package storage

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"

	"clisimplehub/internal/config"
)

type ConfigFileStore struct {
	loader *config.ConfigLoader
	mu     sync.Mutex
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
			ID:        vendor.ID,
			Name:      vendor.Name,
			HomeURL:   vendor.HomeURL,
			APIURL:    vendor.APIURL,
			Remark:    vendor.Remark,
			Endpoints: []config.EndpointConfig{},
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

func (s *ConfigFileStore) GetEndpointsByVendorID(vendorID int64) ([]*Endpoint, error) {
	if vendorID <= 0 {
		return []*Endpoint{}, nil
	}
	endpoints, err := s.GetEndpoints()
	if err != nil {
		return nil, err
	}

	result := make([]*Endpoint, 0)
	for _, ep := range endpoints {
		if ep.VendorID == vendorID {
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
	if endpoint.VendorID <= 0 {
		return errors.New("vendor_id is required")
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
		if err := addEndpointToVendor(cfg, endpoint); err != nil {
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

	for vi := range cfg.Vendors {
		eps := cfg.Vendors[vi].Endpoints
		kept := eps[:0]
		for _, ep := range eps {
			if ep.ID == id {
				continue
			}
			kept = append(kept, ep)
		}
		cfg.Vendors[vi].Endpoints = kept
	}

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
	if cfg.AppConfigKV == nil {
		cfg.AppConfigKV = make(map[string]interface{})
	}
	cfg.AppConfigKV[key] = value
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

	for _, v := range cfg.Vendors {
		if v.ID >= nextVendor {
			nextVendor = v.ID + 1
		}
		for _, ep := range v.Endpoints {
			if ep.ID >= nextEndpoint {
				nextEndpoint = ep.ID + 1
			}
		}
	}

	for vi := range cfg.Vendors {
		if cfg.Vendors[vi].ID == 0 {
			cfg.Vendors[vi].ID = nextVendor
			nextVendor++
			changed = true
		}
		for ei := range cfg.Vendors[vi].Endpoints {
			if cfg.Vendors[vi].Endpoints[ei].ID == 0 {
				cfg.Vendors[vi].Endpoints[ei].ID = nextEndpoint
				nextEndpoint++
				changed = true
			}
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
	for _, v := range cfg.Vendors {
		for _, ep := range v.Endpoints {
			if ep.ID > maxID {
				maxID = ep.ID
			}
		}
	}
	return maxID + 1
}

func flattenEndpoints(cfg *config.AppConfig) []*Endpoint {
	if cfg == nil {
		return []*Endpoint{}
	}

	out := make([]*Endpoint, 0)
	for _, v := range cfg.Vendors {
		for _, ep := range v.Endpoints {
			out = append(out, &Endpoint{
				ID:            ep.ID,
				Name:          ep.Name,
				APIURL:        ep.APIURL,
				APIKey:        ep.APIKey,
				Active:        ep.Active,
				Enabled:       ep.Enabled,
				InterfaceType: ep.InterfaceType,
				VendorID:      v.ID,
				Model:         ep.Model,
				Remark:        ep.Remark,
				Priority:      ep.Priority,
			})
		}
	}
	return out
}

func addEndpointToVendor(cfg *config.AppConfig, endpoint *Endpoint) error {
	for i := range cfg.Vendors {
		if cfg.Vendors[i].ID != endpoint.VendorID {
			continue
		}
		cfg.Vendors[i].Endpoints = append(cfg.Vendors[i].Endpoints, config.EndpointConfig{
			ID:            endpoint.ID,
			Name:          endpoint.Name,
			APIURL:        endpoint.APIURL,
			APIKey:        endpoint.APIKey,
			Active:        endpoint.Active,
			Enabled:       endpoint.Enabled,
			InterfaceType: endpoint.InterfaceType,
			Model:         endpoint.Model,
			Remark:        endpoint.Remark,
			Priority:      endpoint.Priority,
		})
		return nil
	}
	return fmt.Errorf("vendor not found: %d", endpoint.VendorID)
}

func updateEndpointByID(cfg *config.AppConfig, endpoint *Endpoint) (bool, error) {
	for vi := range cfg.Vendors {
		eps := cfg.Vendors[vi].Endpoints
		for ei := range eps {
			if eps[ei].ID != endpoint.ID {
				continue
			}

			if endpoint.VendorID > 0 && cfg.Vendors[vi].ID != endpoint.VendorID {
				moved := eps[ei]
				moved.Name = endpoint.Name
				moved.APIURL = endpoint.APIURL
				if endpoint.APIKey != "" {
					moved.APIKey = endpoint.APIKey
				}
				moved.Active = endpoint.Active
				moved.Enabled = endpoint.Enabled
				moved.InterfaceType = endpoint.InterfaceType
				moved.Model = endpoint.Model
				moved.Remark = endpoint.Remark
				moved.Priority = endpoint.Priority

				cfg.Vendors[vi].Endpoints = append(eps[:ei], eps[ei+1:]...)
				for dest := range cfg.Vendors {
					if cfg.Vendors[dest].ID == endpoint.VendorID {
						cfg.Vendors[dest].Endpoints = append(cfg.Vendors[dest].Endpoints, moved)
						return true, nil
					}
				}
				return false, fmt.Errorf("vendor not found: %d", endpoint.VendorID)
			}

			eps[ei].Name = endpoint.Name
			eps[ei].APIURL = endpoint.APIURL
			if endpoint.APIKey != "" {
				eps[ei].APIKey = endpoint.APIKey
			}
			eps[ei].Active = endpoint.Active
			eps[ei].Enabled = endpoint.Enabled
			eps[ei].InterfaceType = endpoint.InterfaceType
			eps[ei].Model = endpoint.Model
			eps[ei].Remark = endpoint.Remark
			eps[ei].Priority = endpoint.Priority
			return true, nil
		}
	}
	return false, nil
}
