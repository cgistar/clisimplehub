// Package proxy implements the HTTP proxy server for AI API requests.
package proxy

import "time"

// RequestLog represents a single request log entry
type RequestLog struct {
	ID            string    `json:"id"`
	InterfaceType string    `json:"interfaceType"`
	VendorName    string    `json:"vendorName"`
	VendorID      int64     `json:"vendorId,omitempty"`
	EndpointName  string    `json:"endpointName"`
	Path          string    `json:"path"`
	RunTime       int64     `json:"runTime"` // milliseconds
	Status        string    `json:"status"`
	Timestamp     time.Time `json:"timestamp"`
	UpstreamAuth  string    `json:"upstreamAuth,omitempty"`
	// Extended fields for detail view
	Method         string            `json:"method,omitempty"`
	StatusCode     int               `json:"statusCode,omitempty"`
	TargetURL      string            `json:"targetUrl,omitempty"`
	RequestHeaders map[string]string `json:"requestHeaders,omitempty"`
	RequestStream  string            `json:"requestStream,omitempty"`
	ResponseStream string            `json:"responseStream,omitempty"`
}

// TokenStats represents token usage statistics
type TokenStats struct {
	EndpointName string `json:"endpointName"`
	VendorName   string `json:"vendorName"`
	InputTokens  int64  `json:"inputTokens"`
	CachedCreate int64  `json:"cachedCreate"`
	CachedRead   int64  `json:"cachedRead"`
	OutputTokens int64  `json:"outputTokens"`
	Reasoning    int64  `json:"reasoning"`
	Total        int64  `json:"total"`
}

// TokenUsage represents token usage from a single request
type TokenUsage struct {
	InputTokens  int64 `json:"input_tokens"`
	CachedCreate int64 `json:"cached_create"`
	CachedRead   int64 `json:"cached_read"`
	OutputTokens int64 `json:"output_tokens"`
	Reasoning    int64 `json:"reasoning"`
}

// Stats defines statistics operations interface
type Stats interface {
	// RecordRequest records a request log entry
	RecordRequest(log *RequestLog)

	// RecordTokens records token usage for an endpoint
	RecordTokens(endpointName string, tokens *TokenUsage)

	// GetRecentLogs returns the most recent request logs
	GetRecentLogs(limit int) []*RequestLog

	// GetTokenStats returns token statistics for all endpoints
	GetTokenStats() []*TokenStats
}

// Proxy defines the proxy server operations interface
type Proxy interface {
	// Start starts the proxy server
	Start() error

	// Stop stops the proxy server
	Stop() error

	// GetCurrentEndpoint returns the current active endpoint for the given interface type
	GetCurrentEndpoint(interfaceType string) *Endpoint

	// SetCurrentEndpoint sets the current active endpoint for the given interface type
	SetCurrentEndpoint(interfaceType, endpointName string) error

	// GetStats returns the statistics manager
	GetStats() Stats
}
