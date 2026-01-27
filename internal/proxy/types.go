// Package proxy implements the HTTP proxy server for AI API requests.
package proxy

import "clisimplehub/internal/storage"

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

// Type aliases for storage types
// Endpoint is an alias for storage.Endpoint
type Endpoint = storage.Endpoint

// ModelMapping is an alias for storage.ModelMapping  
type ModelMapping = storage.ModelMapping
