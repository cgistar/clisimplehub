// Package proxy implements the HTTP proxy server for AI API requests.
package proxy

import (
	"sync"

	"clisimplehub/internal/storage"
)

// MaxRecentLogs is the maximum number of recent logs to keep
const MaxRecentLogs = 5

// StatsManager manages request logs and token statistics (in-memory only)
type StatsManager struct {
	recentLogs []*RequestLog
	tokenStats map[string]*TokenStats // keyed by endpoint name
	mu         sync.RWMutex
	wsHub      *WSHub          // WebSocket hub for broadcasting
	storage    storage.Storage // Storage for vendor lookup
}

// NewStatsManager creates a new StatsManager instance
func NewStatsManager() *StatsManager {
	return &StatsManager{
		recentLogs: make([]*RequestLog, 0, MaxRecentLogs),
		tokenStats: make(map[string]*TokenStats),
	}
}

// NewStatsManagerWithDeps creates a new StatsManager with WebSocket hub and storage
// Requirements: 7.1, 8.4, 8.5
func NewStatsManagerWithDeps(wsHub *WSHub, store storage.Storage) *StatsManager {
	return &StatsManager{
		recentLogs: make([]*RequestLog, 0, MaxRecentLogs),
		tokenStats: make(map[string]*TokenStats),
		wsHub:      wsHub,
		storage:    store,
	}
}

// SetWSHub sets the WebSocket hub for broadcasting
func (s *StatsManager) SetWSHub(hub *WSHub) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.wsHub = hub
}

// SetStorage sets the storage for persistence
func (s *StatsManager) SetStorage(store storage.Storage) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.storage = store
}

// RecordRequest records a request log entry
// Requirements: 7.1, 7.2, 7.3, 7.4
func (s *StatsManager) RecordRequest(log *RequestLog) {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Validate required fields
	// Requirements: 7.2
	if log == nil {
		return
	}

	if log.VendorName == "" && log.VendorID != 0 && s.storage != nil {
		if vendor, err := s.storage.GetVendorByID(log.VendorID); err == nil && vendor != nil {
			log.VendorName = vendor.Name
		}
	}
	if log.EndpointName != "" && log.VendorName != "" {
		if stats, exists := s.tokenStats[log.EndpointName]; exists {
			stats.VendorName = log.VendorName
		}
	}

	// Upsert by ID to support "in_progress" -> "done" updates.
	for i, existing := range s.recentLogs {
		if existing != nil && existing.ID != "" && existing.ID == log.ID {
			s.recentLogs[i] = log
			goto broadcast
		}
	}

	// Prepend new log (newest first)
	// Requirements: 7.3
	s.recentLogs = append([]*RequestLog{log}, s.recentLogs...)

	// Maintain max size
	// Requirements: 7.4
	if len(s.recentLogs) > MaxRecentLogs {
		s.recentLogs = s.recentLogs[:MaxRecentLogs]
	}

broadcast:
	// Broadcast via WebSocket
	// Requirements: 7.1
	if s.wsHub != nil {
		s.wsHub.Broadcast(&WSMessage{
			Type:    WSMessageTypeRequestLog,
			Payload: log,
		})
	}
}

// RecordTokens records token usage for an endpoint
// Requirements: 8.1, 8.2, 8.3, 8.5
func (s *StatsManager) RecordTokens(endpointName string, tokens *TokenUsage) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if tokens == nil {
		return
	}

	stats, exists := s.tokenStats[endpointName]
	if !exists {
		stats = &TokenStats{
			EndpointName: endpointName,
		}
		s.tokenStats[endpointName] = stats
	}

	// Accumulate token counts
	// Requirements: 8.2
	stats.InputTokens += tokens.InputTokens
	stats.CachedCreate += tokens.CachedCreate
	stats.CachedRead += tokens.CachedRead
	stats.OutputTokens += tokens.OutputTokens
	stats.Reasoning += tokens.Reasoning

	// Calculate total
	// Requirements: 8.3
	stats.Total = stats.InputTokens + stats.CachedCreate + stats.CachedRead + stats.OutputTokens + stats.Reasoning

	// Broadcast via WebSocket
	// Requirements: 8.5
	if s.wsHub != nil {
		s.wsHub.Broadcast(&WSMessage{
			Type:    WSMessageTypeTokenStats,
			Payload: stats,
		})
	}
}

// GetRecentLogs returns the most recent request logs
// Requirements: 7.2, 7.3
func (s *StatsManager) GetRecentLogs(limit int) []*RequestLog {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if limit <= 0 || limit > len(s.recentLogs) {
		limit = len(s.recentLogs)
	}

	// Return a copy to prevent external modification
	result := make([]*RequestLog, limit)
	copy(result, s.recentLogs[:limit])
	return result
}

// GetTokenStats returns token statistics for all endpoints
// Requirements: 8.1, 8.2
func (s *StatsManager) GetTokenStats() []*TokenStats {
	s.mu.RLock()
	defer s.mu.RUnlock()

	result := make([]*TokenStats, 0, len(s.tokenStats))
	for _, stats := range s.tokenStats {
		// Return a copy
		statsCopy := *stats
		result = append(result, &statsCopy)
	}
	return result
}

// GetTokenStatsForEndpoint returns token statistics for a specific endpoint
func (s *StatsManager) GetTokenStatsForEndpoint(endpointName string) *TokenStats {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if stats, exists := s.tokenStats[endpointName]; exists {
		statsCopy := *stats
		return &statsCopy
	}
	return nil
}

// SetVendorName sets the vendor name for an endpoint's token stats
func (s *StatsManager) SetVendorName(endpointName, vendorName string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if stats, exists := s.tokenStats[endpointName]; exists {
		stats.VendorName = vendorName
	}
}

// Clear resets all statistics
func (s *StatsManager) Clear() {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.recentLogs = make([]*RequestLog, 0, MaxRecentLogs)
	s.tokenStats = make(map[string]*TokenStats)
}

// RecordTokensWithVendor records token usage with vendor name
// Requirements: 8.1, 8.2, 8.3, 8.5
func (s *StatsManager) RecordTokensWithVendor(endpointName, vendorName string, tokens *TokenUsage) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if tokens == nil {
		return
	}

	stats, exists := s.tokenStats[endpointName]
	if !exists {
		stats = &TokenStats{
			EndpointName: endpointName,
			VendorName:   vendorName,
		}
		s.tokenStats[endpointName] = stats
	}

	// Update vendor name if not set
	// Requirements: 8.1
	if stats.VendorName == "" {
		stats.VendorName = vendorName
	}

	// Accumulate token counts
	// Requirements: 8.2
	stats.InputTokens += tokens.InputTokens
	stats.CachedCreate += tokens.CachedCreate
	stats.CachedRead += tokens.CachedRead
	stats.OutputTokens += tokens.OutputTokens
	stats.Reasoning += tokens.Reasoning

	// Calculate total
	// Requirements: 8.3
	stats.Total = stats.InputTokens + stats.CachedCreate + stats.CachedRead + stats.OutputTokens + stats.Reasoning

	// Broadcast via WebSocket
	// Requirements: 8.5
	if s.wsHub != nil {
		s.wsHub.Broadcast(&WSMessage{
			Type:    WSMessageTypeTokenStats,
			Payload: stats,
		})
	}
}

// GetAllTokenStats returns all token statistics as a slice
// Requirements: 8.1, 8.2
func (s *StatsManager) GetAllTokenStats() []*TokenStats {
	return s.GetTokenStats()
}
