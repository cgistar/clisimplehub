// Package proxy implements the HTTP proxy server for AI API requests.
package proxy

import "time"

// InterfaceType represents supported API interface types
type InterfaceType string

const (
	// InterfaceTypeClaude represents Claude API interface
	InterfaceTypeClaude InterfaceType = "claude"
	// InterfaceTypeCodex represents Codex/OpenAI API interface
	InterfaceTypeCodex InterfaceType = "codex"
	// InterfaceTypeGemini represents Gemini API interface
	InterfaceTypeGemini InterfaceType = "gemini"
	// InterfaceTypeChat represents generic Chat API interface
	InterfaceTypeChat InterfaceType = "chat"
)

// Vendor represents an API vendor/provider
type Vendor struct {
	ID         int64     `json:"id"`
	Name       string    `json:"name"`
	HomeURL    string    `json:"home_url"`
	APIURL     string    `json:"api_url"`
	Remark     string    `json:"remark,omitempty"`
	CreateTime time.Time `json:"create_time"`
	UpdateTime time.Time `json:"update_time"`
}

// Endpoint represents an API endpoint configuration
type Endpoint struct {
	ID            int64             `json:"id"`
	Name          string            `json:"name"`
	APIURL        string            `json:"api_url"`
	APIKey        string            `json:"api_key"`
	Active        bool              `json:"active"`
	Enabled       bool              `json:"enabled"`
	InterfaceType string            `json:"interface_type"`
	Transformer   string            `json:"transformer,omitempty"`
	VendorID      int64             `json:"vendor_id"`
	Model         string            `json:"model,omitempty"`
	Remark        string            `json:"remark,omitempty"`
	Priority      int               `json:"priority,omitempty"`
	ProxyURL      string            `json:"proxy_url,omitempty"`
	Models        []ModelMapping    `json:"models,omitempty"`
	Headers       map[string]string `json:"headers,omitempty"`
	CreateTime    time.Time         `json:"create_time"`
	UpdateTime    time.Time         `json:"update_time"`
}

// ModelMapping represents a model name mapping configuration
type ModelMapping struct {
	Name  string `json:"name"`  // 实际模型名（上游模型名）
	Alias string `json:"alias"` // API 使用的别名（客户端传入的 model）
}
