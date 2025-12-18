// Package storage provides data operations for the application.
// All data is stored in config.json file.
package storage

import "time"

// Vendor represents an API vendor/provider
type Vendor struct {
	ID         int64     `json:"id"`
	Name       string    `json:"name"`
	HomeURL    string    `json:"homeUrl"`
	APIURL     string    `json:"apiUrl"`
	Remark     string    `json:"remark,omitempty"`
	CreateTime time.Time `json:"createTime,omitempty"`
	UpdateTime time.Time `json:"updateTime,omitempty"`
}

// Endpoint represents an API endpoint configuration
type Endpoint struct {
	ID            int64     `json:"id"`
	Name          string    `json:"name"`
	APIURL        string    `json:"apiUrl"`
	APIKey        string    `json:"apiKey"`
	Active        bool      `json:"active"`
	Enabled       bool      `json:"enabled"`
	InterfaceType string    `json:"interfaceType"`
	VendorID      int64     `json:"vendorId"`
	Model         string    `json:"model,omitempty"`
	Remark        string    `json:"remark,omitempty"`
	Priority      int       `json:"priority,omitempty"`
	CreateTime    time.Time `json:"createTime,omitempty"`
	UpdateTime    time.Time `json:"updateTime,omitempty"`
}

// Storage defines the data operations interface
type Storage interface {
	// Vendor operations
	GetVendors() ([]*Vendor, error)
	GetVendorByID(id int64) (*Vendor, error)
	SaveVendor(vendor *Vendor) error
	DeleteVendor(id int64) error

	// Endpoint operations
	GetEndpoints() ([]*Endpoint, error)
	GetEndpointsByType(interfaceType string) ([]*Endpoint, error)
	GetEndpointsByVendorID(vendorID int64) ([]*Endpoint, error)
	GetEndpointByID(id int64) (*Endpoint, error)
	SaveEndpoint(endpoint *Endpoint) error
	UpdateEndpoint(endpoint *Endpoint) error
	DeleteEndpoint(id int64) error

	// Config operations
	GetConfig(key string) (string, error)
	SetConfig(key, value string) error
	SetConfigBool(key string, value bool) error

	// Close closes the storage
	Close() error
}
