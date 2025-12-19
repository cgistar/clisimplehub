// Package proxy implements the HTTP proxy server for AI API requests.
package proxy

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"clisimplehub/internal/logger"
	"clisimplehub/internal/statsdb"
	"clisimplehub/internal/storage"

	"github.com/google/uuid"
)

// RetryConfig defines retry behavior constants
const (
	MaxRetriesPerEndpoint = 2  // Max retries per endpoint before rotating
	MaxTotalRetries       = 10 // Max total retries across all endpoints
	// CircuitBreakerFailureThreshold opens (temporarily disables) an endpoint after N consecutive failures.
	CircuitBreakerFailureThreshold = 2
)

// ProxyServer represents the main proxy server implementation
type ProxyServer struct {
	port            int
	router          Router
	server          *http.Server
	stats           *StatsManager
	wsHub           *WSHub
	mu              sync.RWMutex
	authKey         string
	store           storage.Storage
	vendorStats     statsdb.VendorStatsStore
	failureCounts   map[string]int // Track consecutive failures per endpoint
	failureMu       sync.RWMutex
	fallbackEnabled bool // Whether fallback is enabled
}

func normalizeAPIURL(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	if strings.HasPrefix(raw, "http://") || strings.HasPrefix(raw, "https://") {
		return strings.TrimRight(raw, "/")
	}
	if strings.HasPrefix(raw, "//") {
		return "https:" + strings.TrimRight(raw, "/")
	}
	return "https://" + strings.TrimRight(raw, "/")
}

func buildTargetURL(apiURL string, path string, rawQuery string) (string, error) {
	base := normalizeAPIURL(apiURL)
	if base == "" {
		return "", fmt.Errorf("empty api_url")
	}
	u, err := url.Parse(base)
	if err != nil {
		return "", fmt.Errorf("invalid api_url: %w", err)
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	u.Path = strings.TrimRight(u.Path, "/") + path
	u.RawQuery = rawQuery
	return u.String(), nil
}

func shouldCopyRequestHeader(key string) bool {
	// Hop-by-hop / computed headers
	if strings.EqualFold(key, "Host") || strings.EqualFold(key, "Accept-Encoding") {
		return false
	}
	// Content-Length will be recalculated by http.NewRequest
	if strings.EqualFold(key, "Content-Length") {
		return false
	}
	// Auth headers: always replace with endpoint api_key
	if strings.EqualFold(key, "Authorization") || strings.EqualFold(key, "x-api-key") {
		return false
	}
	return true
}

func applyEndpointAuth(req *http.Request, endpoint *Endpoint, isStreaming bool) {
	if req == nil || endpoint == nil {
		return
	}

	switch InterfaceType(endpoint.InterfaceType) {
	case InterfaceTypeGemini:
		q := req.URL.Query()
		q.Set("key", endpoint.APIKey)
		if isStreaming {
			q.Set("alt", "sse")
		}
		req.URL.RawQuery = q.Encode()
	case InterfaceTypeCodex, InterfaceTypeChat:
		req.Header.Set("Authorization", "Bearer "+endpoint.APIKey)
	default:
		// 根据调用方使用的鉴权头类型进行替换；若两者都不存在则保持兼容（同时写入两种）。
		req.Header.Set("Authorization", "Bearer "+endpoint.APIKey)
		req.Header.Set("x-api-key", endpoint.APIKey)
	}
}

// NewProxyServer creates a new ProxyServer instance
func NewProxyServer(port int, router Router) *ProxyServer {
	return &ProxyServer{
		port:          port,
		router:        router,
		stats:         NewStatsManager(),
		failureCounts: make(map[string]int),
	}
}

// NewProxyServerWithWSHub creates a new ProxyServer with WebSocket hub integration
// Requirements: 7.1, 8.5
func NewProxyServerWithWSHub(port int, router Router, wsHub *WSHub) *ProxyServer {
	stats := NewStatsManager()
	stats.SetWSHub(wsHub)

	return &ProxyServer{
		port:          port,
		router:        router,
		stats:         stats,
		wsHub:         wsHub,
		failureCounts: make(map[string]int),
	}
}

// SetWSHub sets the WebSocket hub for real-time updates
// Requirements: 7.1, 8.5
func (p *ProxyServer) SetWSHub(hub *WSHub) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.wsHub = hub
	// Also set the hub on the stats manager for broadcasting
	if p.stats != nil {
		p.stats.SetWSHub(hub)
	}
}

// SetStorage sets the storage for stats persistence and vendor lookup.
func (p *ProxyServer) SetStorage(store storage.Storage) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.store = store
	if p.stats != nil {
		p.stats.SetStorage(store)
	}
}

func (p *ProxyServer) SetVendorStatsStore(store statsdb.VendorStatsStore) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.vendorStats = store
}

// GetWSHub returns the WebSocket hub
// Requirements: 7.1, 8.5
func (p *ProxyServer) GetWSHub() *WSHub {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.wsHub
}

// Start starts the proxy server
// Requirements: 1.1, 5.1, 7.1, 8.5
func (p *ProxyServer) Start() error {
	mux := http.NewServeMux()

	// Set up route handlers
	mux.HandleFunc("/", p.handleProxy)
	mux.HandleFunc("/health", p.handleHealth)
	mux.HandleFunc("/stats", p.handleStats)

	// Set up WebSocket endpoint for real-time updates
	// Requirements: 7.1, 8.5
	if p.wsHub != nil {
		mux.HandleFunc("/ws", p.wsHub.HandleWebSocket)
	}

	p.server = &http.Server{
		Addr:         fmt.Sprintf(":%d", p.port),
		Handler:      mux,
		ReadTimeout:  300 * time.Second,
		WriteTimeout: 300 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	return p.server.ListenAndServe()
}

// Stop stops the proxy server gracefully
func (p *ProxyServer) Stop() error {
	if p.server == nil {
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	return p.server.Shutdown(ctx)
}

// GetPort returns the configured port
func (p *ProxyServer) GetPort() int {
	return p.port
}

// SetPort updates the server port (requires restart to take effect)
func (p *ProxyServer) SetPort(port int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.port = port
}

func (p *ProxyServer) SetAuthKey(key string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.authKey = strings.TrimSpace(key)
}

func (p *ProxyServer) getAuthKey() string {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.authKey
}

// SetFallbackEnabled sets whether fallback is enabled
func (p *ProxyServer) SetFallbackEnabled(enabled bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.fallbackEnabled = enabled
}

// IsFallbackEnabled returns whether fallback is enabled
func (p *ProxyServer) IsFallbackEnabled() bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.fallbackEnabled
}

// GetCurrentEndpoint returns the current active endpoint for the given interface type
func (p *ProxyServer) GetCurrentEndpoint(interfaceType string) *Endpoint {
	return p.router.GetActiveEndpoint(InterfaceType(interfaceType))
}

// SetCurrentEndpoint sets the current active endpoint for the given interface type
func (p *ProxyServer) SetCurrentEndpoint(interfaceType, endpointName string) error {
	eps := p.router.GetEndpointsByType(InterfaceType(interfaceType))
	for _, ep := range eps {
		if ep.Name == endpointName {
			return p.router.SetActiveEndpoint(InterfaceType(interfaceType), ep)
		}
	}
	return ErrEndpointNotFound
}

// GetStats returns the statistics manager
func (p *ProxyServer) GetStats() *StatsManager {
	return p.stats
}

// handleHealth handles health check requests
func (p *ProxyServer) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)

	response := map[string]interface{}{
		"status": "healthy",
		"port":   p.port,
	}

	json.NewEncoder(w).Encode(response)
}

// handleStats handles statistics requests
func (p *ProxyServer) handleStats(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	stats := map[string]interface{}{
		"recent_logs": p.stats.GetRecentLogs(5),
		"token_stats": p.stats.GetTokenStats(),
	}

	json.NewEncoder(w).Encode(stats)
}

// handleProxy handles the main proxy logic
// Requirements: 3.1, 3.2, 3.3, 3.4, 3.5, 4.1, 4.2, 4.3, 4.4, 4.5, 4.6
func (p *ProxyServer) handleProxy(w http.ResponseWriter, r *http.Request) {
	startTime := time.Now()
	requestID := uuid.New().String()

	// Collect request headers for detail view
	reqHeaders := sanitizeHeadersForLog(r.Header)

	// Detect interface type from path (needed for logging even when unauthorized)
	interfaceType := p.router.DetectInterfaceType(r.URL.Path)

	// Check if this path should have retry/failover and vendor_stats recording
	// Only /v1/messages (Claude) and /responses (Codex) paths support these features
	isRetryable := IsRetryablePath(r.URL.Path)
	shouldRecordStats := ShouldRecordVendorStats(interfaceType, r.URL.Path)
	fallbackEnabled := p.IsFallbackEnabled()

	// Optional proxy authentication (empty token => no auth)
	if required := p.getAuthKey(); required != "" && !isAuthorized(r, required) {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		detail := &RequestDetail{Method: r.Method, StatusCode: http.StatusUnauthorized, RequestHeaders: reqHeaders}
		runTime := time.Since(startTime).Milliseconds()
		p.recordRequestWithDetail(requestID, interfaceType, nil, r.URL.Path, startTime, "error_401", runTime, detail)
		if shouldRecordStats {
			p.insertVendorStat(r.Context(), interfaceType, nil, r.URL.Path, map[string]string{}, runTime, http.StatusUnauthorized, "error_401", nil)
		}
		return
	}

	// Read request body
	bodyBytes, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "Failed to read request body", http.StatusBadRequest)
		return
	}
	defer r.Body.Close()

	// Check if streaming is requested
	var streamReq struct {
		Stream bool `json:"stream"`
	}
	json.Unmarshal(bodyBytes, &streamReq)

	// Get endpoints for this interface type
	endpoint := p.router.GetActiveEndpoint(interfaceType)
	if endpoint == nil {
		http.Error(w, "No enabled endpoints available", http.StatusServiceUnavailable)
		detail := &RequestDetail{Method: r.Method, StatusCode: http.StatusServiceUnavailable, RequestHeaders: reqHeaders}
		runTime := time.Since(startTime).Milliseconds()
		p.recordRequestWithDetail(requestID, interfaceType, nil, r.URL.Path, startTime, "error_503", runTime, detail)
		return
	}

	// Build initial target URL for detail
	targetURL := strings.TrimSuffix(endpoint.APIURL, "/") + r.URL.Path
	detail := &RequestDetail{
		Method:         r.Method,
		TargetURL:      targetURL,
		RequestHeaders: reqHeaders,
		UpstreamAuth:   formatUpstreamAuthForLog(endpoint),
	}
	detail.StatusCode = 0
	detail.ResponseStream = ""
	p.recordRequestWithDetail(requestID, interfaceType, endpoint, r.URL.Path, startTime, "in_progress", 0, detail)

	// When fallback is disabled (or path isn't retryable), forward directly without retry/failover.
	// Note: We still capture details and (for retryable paths) record vendor stats on the single attempt.
	if !isRetryable || !fallbackEnabled {
		result := p.forwardRequestWithDetail(r, endpoint, bodyBytes, streamReq.Stream, w)
		detail.TargetURL = result.TargetURL
		detail.StatusCode = result.StatusCode
		detail.ResponseStream = result.ResponseStream
		runTime := time.Since(startTime).Milliseconds()

		if result.Error != nil {
			status := "error"
			if result.StatusCode > 0 {
				status = fmt.Sprintf("error_%d", result.StatusCode)
			}
			p.recordRequestWithDetail(requestID, interfaceType, endpoint, r.URL.Path, startTime, status, runTime, detail)
			// Only retryable paths record vendor_stats; keep behavior consistent even when fallback is disabled.
			if isRetryable && shouldRecordStats {
				p.insertVendorStat(r.Context(), interfaceType, endpoint, r.URL.Path, result.TargetHeaders, runTime, result.StatusCode, status, nil)
			}
			if result.StatusCode > 0 {
				writeResponseWithHeaders(w, result.StatusCode, result.Headers, result.Body)
			} else {
				http.Error(w, fmt.Sprintf("Request failed: %v", result.Error), http.StatusBadGateway)
			}
			return
		}

		status := "success"
		if result.StatusCode != http.StatusOK {
			status = fmt.Sprintf("error_%d", result.StatusCode)
		}
		p.recordRequestWithDetail(requestID, interfaceType, endpoint, r.URL.Path, startTime, status, runTime, detail)
		if isRetryable && shouldRecordStats {
			tokens := result.Tokens
			if tokens == nil {
				tokens = p.extractAndRecordTokens(endpoint, result.Body)
			}
			p.insertVendorStat(r.Context(), interfaceType, endpoint, r.URL.Path, result.TargetHeaders, runTime, result.StatusCode, status, tokens)
		}

		if !result.Streamed {
			writeResponseWithHeaders(w, result.StatusCode, result.Headers, result.Body)
		}
		return
	}

	// Retry loop for retryable paths (/v1/messages, /responses)
	// Requirements: 4.1, 4.2, 4.3, 4.4, 4.5, 4.6
	var lastErr error
	triedEndpoints := make(map[string]int)      // Track attempts per endpoint in this request (keyed by endpoint id/name)
	exhaustedEndpoints := make(map[string]bool) // Track endpoints that have been fully tried in this request

	for totalRetries := 0; totalRetries < MaxTotalRetries; totalRetries++ {
		if endpoint == nil {
			break
		}

		currentKey := endpointFailureKey(endpoint)

		// Skip endpoints that have been exhausted or disabled in this request
		if exhaustedEndpoints[currentKey] || !endpoint.Enabled {
			nextEndpoint := p.findNextUntriedEndpoint(interfaceType, endpoint, exhaustedEndpoints)
			if nextEndpoint == nil {
				break // All endpoints exhausted
			}
			endpoint = nextEndpoint
			currentKey = endpointFailureKey(endpoint)
		}

		// Track attempts for this endpoint
		triedEndpoints[currentKey]++

		// Make the request with detail capture
		result := p.forwardRequestWithDetail(r, endpoint, bodyBytes, streamReq.Stream, w)

		// Update detail with result
		detail.TargetURL = result.TargetURL
		detail.StatusCode = result.StatusCode
		detail.ResponseStream = result.ResponseStream

		// For streaming responses, the response has already been written to the client,
		// so we cannot retry regardless of the status code. Record and return immediately.
		if result.Streamed {
			runTime := time.Since(startTime).Milliseconds()
			status := "success"
			if result.StatusCode != http.StatusOK {
				status = fmt.Sprintf("error_%d", result.StatusCode)
			}
			p.recordRequestWithDetail(requestID, interfaceType, endpoint, r.URL.Path, startTime, status, runTime, detail)

			p.updateCircuitBreaker(interfaceType, endpoint, result.StatusCode, result.Error)

			tokens := result.Tokens
			if tokens == nil {
				tokens = p.extractAndRecordTokens(endpoint, result.Body)
			}
			if shouldRecordStats {
				p.insertVendorStat(r.Context(), interfaceType, endpoint, r.URL.Path, result.TargetHeaders, runTime, result.StatusCode, status, tokens)
			}
			return
		}

		if result.Error == nil && result.StatusCode == http.StatusOK {
			// Success - record and return
			runTime := time.Since(startTime).Milliseconds()
			p.recordRequestWithDetail(requestID, interfaceType, endpoint, r.URL.Path, startTime, "success", runTime, detail)

			writeResponseWithHeaders(w, result.StatusCode, result.Headers, result.Body)

			p.updateCircuitBreaker(interfaceType, endpoint, result.StatusCode, nil)

			tokens := result.Tokens
			if tokens == nil {
				tokens = p.extractAndRecordTokens(endpoint, result.Body)
			}
			if shouldRecordStats {
				p.insertVendorStat(r.Context(), interfaceType, endpoint, r.URL.Path, result.TargetHeaders, runTime, http.StatusOK, "success", tokens)
			}

			return
		}

		lastErr = result.Error
		if result.Error != nil {
			p.updateCircuitBreaker(interfaceType, endpoint, result.StatusCode, result.Error)

			// Check if this endpoint has been tried enough times
			if triedEndpoints[currentKey] >= MaxRetriesPerEndpoint {
				exhaustedEndpoints[currentKey] = true
				// Find next untried endpoint
				nextEndpoint := p.findNextUntriedEndpoint(interfaceType, endpoint, exhaustedEndpoints)
				if nextEndpoint != nil {
					prevEndpoint := endpoint
					endpoint = nextEndpoint
					// Broadcast fallback switch notification
					p.broadcastFallbackSwitch(prevEndpoint, endpoint, r.URL.Path, 0, result.Error.Error())
					detail.TargetURL = strings.TrimSuffix(endpoint.APIURL, "/") + r.URL.Path
					detail.UpstreamAuth = formatUpstreamAuthForLog(endpoint)
					detail.StatusCode = 0
					detail.ResponseStream = ""
					p.recordRequestWithDetail(requestID, interfaceType, endpoint, r.URL.Path, startTime, "in_progress", 0, detail)
				} else {
					break // All endpoints exhausted
				}
			}
			continue
		}

		// Check if we should retry based on status code
		// Requirements: 4.1, 4.2, 4.3
		if !shouldRetry(result.StatusCode) {
			// Non-retryable error - return to client
			detail.StatusCode = result.StatusCode
			status := fmt.Sprintf("error_%d", result.StatusCode)
			runTime := time.Since(startTime).Milliseconds()
			p.recordRequestWithDetail(requestID, interfaceType, endpoint, r.URL.Path, startTime, status, runTime, detail)
			if shouldRecordStats {
				p.insertVendorStat(r.Context(), interfaceType, endpoint, r.URL.Path, result.TargetHeaders, runTime, result.StatusCode, status, nil)
			}
			writeResponseWithHeaders(w, result.StatusCode, result.Headers, result.Body)
			return
		}

		p.updateCircuitBreaker(interfaceType, endpoint, result.StatusCode, nil)

		// Retryable error (5xx) - check if endpoint exhausted
		// Requirements: 4.4, 4.6
		if triedEndpoints[currentKey] >= MaxRetriesPerEndpoint {
			exhaustedEndpoints[currentKey] = true
			// Find next untried endpoint
			nextEndpoint := p.findNextUntriedEndpoint(interfaceType, endpoint, exhaustedEndpoints)
			if nextEndpoint != nil {
				prevEndpoint := endpoint
				endpoint = nextEndpoint
				// Broadcast fallback switch notification
				errMsg := fmt.Sprintf("HTTP %d", result.StatusCode)
				p.broadcastFallbackSwitch(prevEndpoint, endpoint, r.URL.Path, result.StatusCode, errMsg)
				// Update target URL for new endpoint
				detail.TargetURL = strings.TrimSuffix(endpoint.APIURL, "/") + r.URL.Path
				detail.UpstreamAuth = formatUpstreamAuthForLog(endpoint)
				detail.StatusCode = 0
				detail.ResponseStream = ""
				p.recordRequestWithDetail(requestID, interfaceType, endpoint, r.URL.Path, startTime, "in_progress", 0, detail)
			} else {
				// All endpoints exhausted
				break
			}
		}
	}

	// All retries exhausted
	// Requirements: 4.5
	detail.StatusCode = http.StatusServiceUnavailable
	runTime := time.Since(startTime).Milliseconds()
	p.recordRequestWithDetail(requestID, interfaceType, endpoint, r.URL.Path, startTime, "error_503", runTime, detail)
	if shouldRecordStats {
		p.insertVendorStat(r.Context(), interfaceType, endpoint, r.URL.Path, map[string]string{}, runTime, http.StatusServiceUnavailable, "error_503", nil)
	}

	if lastErr != nil {
		http.Error(w, fmt.Sprintf("All endpoints failed: %v", lastErr), http.StatusServiceUnavailable)
	} else {
		http.Error(w, "All endpoints failed", http.StatusServiceUnavailable)
	}
}

// shouldRetry determines if a request should be retried based on status code
// Requirements: 4.1, 4.2, 4.3
func shouldRetry(statusCode int) bool {
	// Requirement 4.1: Retry on 429 (rate limit)
	// if statusCode == http.StatusTooManyRequests {
	// 	return true
	// }
	// Requirement 4.2: Retry on 404 (not found)
	// if statusCode == http.StatusNotFound {
	// 	return true
	// }
	// Requirement 4.3: Retry on 500-599 (server errors)
	if statusCode >= 500 && statusCode <= 599 {
		return true
	}
	return false
}

func endpointFailureKey(endpoint *Endpoint) string {
	if endpoint == nil {
		return ""
	}
	if endpoint.ID != 0 {
		return fmt.Sprintf("id:%d", endpoint.ID)
	}
	if strings.TrimSpace(endpoint.Name) == "" {
		return ""
	}
	return "name:" + endpoint.Name
}

func isIgnorableCircuitBreakerError(err error) bool {
	if err == nil {
		return false
	}
	// Client cancelled or timed out: don't treat as endpoint failure.
	return errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
}

func (p *ProxyServer) updateCircuitBreaker(interfaceType InterfaceType, endpoint *Endpoint, statusCode int, err error) {
	if endpoint == nil || !p.IsFallbackEnabled() {
		return
	}
	// Reset on success.
	if err == nil && statusCode == http.StatusOK {
		p.resetFailureCount(endpointFailureKey(endpoint))
		return
	}

	// Count only upstream/network failures and 5xx responses.
	isFailure := false
	if err != nil && !isIgnorableCircuitBreakerError(err) {
		isFailure = true
	}
	if err == nil && statusCode >= 500 && statusCode <= 599 {
		isFailure = true
	}
	if !isFailure {
		return
	}

	key := endpointFailureKey(endpoint)
	if key == "" {
		return
	}
	failures := p.incrementFailureCount(key)
	if failures < CircuitBreakerFailureThreshold {
		return
	}
	// Open circuit: temporarily disable endpoint and reset counter.
	p.resetFailureCount(key)
	disabledUntil := p.router.DisableEndpoint(interfaceType, endpoint)
	p.broadcastEndpointTempDisabled(interfaceType, endpoint, disabledUntil)
}

// ForwardResult contains the result of a forwarded request
type ForwardResult struct {
	StatusCode     int
	Body           []byte
	TargetURL      string
	TargetHeaders  map[string]string
	Headers        http.Header
	ResponseStream string
	Tokens         *TokenUsage
	Error          error
	Streamed       bool
}

// forwardRequest forwards the request to the target endpoint
func (p *ProxyServer) forwardRequest(r *http.Request, endpoint *Endpoint, body []byte, isStreaming bool, w http.ResponseWriter) (int, []byte, error) {
	result := p.forwardRequestWithDetail(r, endpoint, body, isStreaming, w)
	return result.StatusCode, result.Body, result.Error
}

// forwardRequestWithDetail forwards the request and returns detailed result
func (p *ProxyServer) forwardRequestWithDetail(r *http.Request, endpoint *Endpoint, body []byte, isStreaming bool, w http.ResponseWriter) *ForwardResult {
	result := &ForwardResult{}

	targetURL, err := buildTargetURL(endpoint.APIURL, r.URL.Path, r.URL.RawQuery)
	if err != nil {
		result.Error = err
		return result
	}
	result.TargetURL = targetURL

	// Override model in request body if endpoint has model configured
	if endpoint.Model != "" {
		body = overrideModelInBody(body, endpoint.Model)
	}

	// Create proxy request
	proxyReq, err := http.NewRequest(r.Method, targetURL, bytes.NewReader(body))
	if err != nil {
		result.Error = fmt.Errorf("failed to create request: %w", err)
		return result
	}
	proxyReq = proxyReq.WithContext(r.Context())

	// Copy headers
	for key, values := range r.Header {
		if !shouldCopyRequestHeader(key) {
			continue
		}
		for _, value := range values {
			proxyReq.Header.Add(key, value)
		}
	}

	applyEndpointAuth(proxyReq, endpoint, isStreaming)
	result.TargetHeaders = sanitizeHeadersForLog(proxyReq.Header)

	// Send request
	client := &http.Client{
		Timeout: 300 * time.Second,
	}

	resp, err := client.Do(proxyReq)
	if err != nil {
		result.Error = fmt.Errorf("request failed: %w", err)
		return result
	}
	defer resp.Body.Close()

	result.StatusCode = resp.StatusCode
	result.Headers = resp.Header.Clone()

	// Handle streaming response
	contentType := resp.Header.Get("Content-Type")
	if isStreaming && (contentType == "text/event-stream" || strings.Contains(contentType, "text/event-stream")) {
		statusCode, respBody, streamData, tokens, err := p.handleStreamingResponseWithCaptureAndTokens(w, resp, endpoint)
		result.StatusCode = statusCode
		result.Body = respBody
		result.ResponseStream = streamData
		result.Tokens = tokens
		result.Error = err
		result.Streamed = true
		return result
	}

	// Read response body
	var reader io.Reader = resp.Body
	if strings.EqualFold(resp.Header.Get("Content-Encoding"), "gzip") {
		gzipReader, gzErr := gzip.NewReader(resp.Body)
		if gzErr != nil {
			result.Error = fmt.Errorf("failed to init gzip reader: %w", gzErr)
			return result
		}
		defer gzipReader.Close()
		reader = gzipReader
		// We've decompressed, so remove encoding headers for downstream.
		result.Headers.Del("Content-Encoding")
		result.Headers.Del("Content-Length")
	}
	respBody, err := io.ReadAll(reader)
	if err != nil {
		result.Error = fmt.Errorf("failed to read response: %w", err)
		return result
	}

	result.Body = respBody
	return result
}

func writeResponseWithHeaders(w http.ResponseWriter, statusCode int, headers http.Header, body []byte) {
	if headers != nil {
		for key, values := range headers {
			if strings.EqualFold(key, "Content-Length") {
				continue
			}
			for _, v := range values {
				w.Header().Add(key, v)
			}
		}
	}
	w.WriteHeader(statusCode)
	_, _ = w.Write(body)
}

func (p *ProxyServer) insertVendorStat(ctx context.Context, interfaceType InterfaceType, endpoint *Endpoint, path string, targetHeaders map[string]string, durationMs int64, statusCode int, status string, tokens *TokenUsage) {
	p.mu.RLock()
	store := p.store
	vendorStats := p.vendorStats
	p.mu.RUnlock()

	if vendorStats == nil {
		return
	}
	// 没有有效端点（目标节点）时不写 vendor_stats，避免污染统计。
	if endpoint == nil {
		return
	}

	var vendorID int64
	var endpointID int64
	vendorName := "unknown"
	endpointName := "unknown"
	if endpoint != nil {
		vendorID = endpoint.VendorID
		endpointID = endpoint.ID
		endpointName = endpoint.Name
	}
	if store != nil && vendorID != 0 {
		if vendor, err := store.GetVendorByID(vendorID); err == nil && vendor != nil && strings.TrimSpace(vendor.Name) != "" {
			vendorName = vendor.Name
		}
	}

	stat := statsdb.VendorStat{
		VendorID:      strconv.FormatInt(vendorID, 10),
		VendorName:    vendorName,
		EndpointID:    strconv.FormatInt(endpointID, 10),
		EndpointName:  endpointName,
		Path:          path,
		Date:          time.Now().Format("2006-01-02"),
		InterfaceType: string(interfaceType),
		TargetHeaders: statsdb.MustJSON(targetHeaders),
		DurationMs:    durationMs,
		StatusCode:    statusCode,
		Status:        status,
	}

	if tokens != nil {
		stat.InputTokens = tokens.InputTokens
		stat.OutputTokens = tokens.OutputTokens
		stat.CachedCreate = tokens.CachedCreate
		stat.CachedRead = tokens.CachedRead
		stat.Reasoning = tokens.Reasoning
	}

	insertCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if err := vendorStats.InsertVendorStat(insertCtx, stat); err != nil {
		log.Printf("Warning: insert vendor_stats failed: %v", err)
	}
}

// RequestDetail holds extended request information for detail view
type RequestDetail struct {
	Method         string
	StatusCode     int
	TargetURL      string
	RequestHeaders map[string]string
	ResponseStream string
	UpstreamAuth   string
}

// recordRequest records a request in the stats manager
func (p *ProxyServer) recordRequest(id string, interfaceType InterfaceType, endpoint *Endpoint, path string, startTime time.Time, status string, runTime int64) {
	p.recordRequestWithDetail(id, interfaceType, endpoint, path, startTime, status, runTime, nil)
}

// recordRequestWithDetail records a request with extended detail information
func (p *ProxyServer) recordRequestWithDetail(id string, interfaceType InterfaceType, endpoint *Endpoint, path string, startTime time.Time, status string, runTime int64, detail *RequestDetail) {
	log := &RequestLog{
		ID:            id,
		InterfaceType: string(interfaceType),
		Path:          path,
		RunTime:       runTime,
		Status:        status,
		Timestamp:     startTime,
	}

	if endpoint != nil {
		log.EndpointName = endpoint.Name
		log.VendorID = endpoint.VendorID
	}

	if detail != nil {
		log.Method = detail.Method
		log.StatusCode = detail.StatusCode
		log.TargetURL = detail.TargetURL
		log.RequestHeaders = detail.RequestHeaders
		log.ResponseStream = detail.ResponseStream
		log.UpstreamAuth = detail.UpstreamAuth
	}

	p.stats.RecordRequest(log)
}

// extractAndRecordTokens extracts token usage from response and records it
func (p *ProxyServer) extractAndRecordTokens(endpoint *Endpoint, respBody []byte) *TokenUsage {
	tokens := ExtractTokenUsageFromResponseBody(respBody)
	if tokens == nil {
		return nil
	}
	if p.stats != nil && endpoint != nil {
		p.stats.RecordTokens(endpoint.Name, tokens)
	}
	return tokens
}

// getVendorName returns the vendor name for an endpoint
func (p *ProxyServer) getVendorName(endpoint *Endpoint) string {
	if endpoint == nil {
		return "unknown"
	}
	p.mu.RLock()
	store := p.store
	p.mu.RUnlock()

	if store != nil && endpoint.VendorID != 0 {
		if vendor, err := store.GetVendorByID(endpoint.VendorID); err == nil && vendor != nil && strings.TrimSpace(vendor.Name) != "" {
			return vendor.Name
		}
	}
	return "unknown"
}

// broadcastFallbackSwitch sends a fallback switch notification via WebSocket
func (p *ProxyServer) broadcastFallbackSwitch(fromEndpoint, toEndpoint *Endpoint, path string, statusCode int, errorMsg string) {
	if p.wsHub == nil || !p.IsFallbackEnabled() {
		return
	}

	payload := &FallbackSwitchPayload{
		FromVendor:   p.getVendorName(fromEndpoint),
		FromEndpoint: "",
		ToVendor:     p.getVendorName(toEndpoint),
		ToEndpoint:   "",
		Path:         path,
		StatusCode:   statusCode,
		ErrorMessage: errorMsg,
	}
	if fromEndpoint != nil {
		payload.FromEndpoint = fromEndpoint.Name
	}
	if toEndpoint != nil {
		payload.ToEndpoint = toEndpoint.Name
	}

	p.wsHub.BroadcastFallbackSwitch(payload)
}

func unixMillis(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.UnixNano() / int64(time.Millisecond)
}

func (p *ProxyServer) broadcastEndpointTempDisabled(interfaceType InterfaceType, endpoint *Endpoint, disabledUntil time.Time) {
	if p.wsHub == nil || endpoint == nil || disabledUntil.IsZero() {
		return
	}

	p.wsHub.BroadcastEndpointTempDisabled(&EndpointTempDisabledPayload{
		InterfaceType: string(interfaceType),
		EndpointID:    endpoint.ID,
		EndpointName:  endpoint.Name,
		DisabledUntil: unixMillis(disabledUntil),
	})
}

// Failure count management
func (p *ProxyServer) incrementFailureCount(endpointName string) int {
	if strings.TrimSpace(endpointName) == "" {
		return 0
	}
	p.failureMu.Lock()
	defer p.failureMu.Unlock()
	p.failureCounts[endpointName]++
	return p.failureCounts[endpointName]
}

func (p *ProxyServer) resetFailureCount(endpointName string) {
	if strings.TrimSpace(endpointName) == "" {
		return
	}
	p.failureMu.Lock()
	defer p.failureMu.Unlock()
	p.failureCounts[endpointName] = 0
}

// findNextUntriedEndpoint finds the next enabled endpoint that hasn't been exhausted
// Endpoints are already sorted by priority (ascending), so we search in order
func (p *ProxyServer) findNextUntriedEndpoint(interfaceType InterfaceType, current *Endpoint, exhausted map[string]bool) *Endpoint {
	eps := p.router.GetEndpointsByType(interfaceType)
	if len(eps) == 0 {
		return nil
	}

	// Find current endpoint's position
	currentIdx := -1
	for i, ep := range eps {
		if current == nil || ep == nil {
			continue
		}
		if current.ID != 0 {
			if ep.ID == current.ID {
				currentIdx = i
				break
			}
			continue
		}
		if ep.Name == current.Name {
			currentIdx = i
			break
		}
	}

	// First, search for next untried endpoint after current position (same or higher priority)
	for i := currentIdx + 1; i < len(eps); i++ {
		ep := eps[i]
		if ep == nil || !ep.Enabled {
			continue
		}
		if exhausted[endpointFailureKey(ep)] {
			continue
		}
		return ep
	}

	// Then, search from the beginning (lower priority endpoints)
	for i := 0; i < currentIdx; i++ {
		ep := eps[i]
		if ep == nil || !ep.Enabled {
			continue
		}
		if exhausted[endpointFailureKey(ep)] {
			continue
		}
		return ep
	}

	return nil
}

// overrideModelInBody replaces the model field in the request body with the specified model
func overrideModelInBody(body []byte, model string) []byte {
	if model == "" {
		return body
	}

	var data map[string]interface{}
	if err := json.Unmarshal(body, &data); err != nil {
		return body
	}

	originalModel, _ := data["model"].(string)
	data["model"] = model
	newBody, err := json.Marshal(data)
	if err != nil {
		return body
	}

	logger.Info("[ModelOverride] Replaced model: %s -> %s", originalModel, model)
	return newBody
}
