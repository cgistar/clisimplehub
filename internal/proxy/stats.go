// Package proxy implements the HTTP proxy server for AI API requests.
package proxy

import (
	"strings"
	"sync"

	"clisimplehub/internal/storage"
)

// MaxRecentLogs is the maximum number of recent logs to keep
const MaxRecentLogs = 10

// StatsManager manages request logs and token statistics (in-memory only)
type StatsManager struct {
	recentLogs    []*RequestLog
	inProgressMap map[string]*RequestLog // keyed by ID
	tokenStats    map[string]*TokenStats // keyed by endpoint name
	mu            sync.RWMutex
	sseHub        *SSEHub
	storage       storage.Storage // Storage for vendor lookup
}

// NewStatsManager creates a new StatsManager instance
func NewStatsManager() *StatsManager {
	return &StatsManager{
		recentLogs:    make([]*RequestLog, 0, MaxRecentLogs),
		inProgressMap: make(map[string]*RequestLog),
		tokenStats:    make(map[string]*TokenStats),
	}
}

// SetSSEHub sets the SSE hub for broadcasting.
func (s *StatsManager) SetSSEHub(hub *SSEHub) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sseHub = hub
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

	if log.EndpointName != "" && log.ProviderName != "" {
		if stats, exists := s.tokenStats[log.EndpointName]; exists {
			stats.ProviderName = log.ProviderName
		}
	}

	inProgress := isInProgressStatus(log.Status)

	if inProgress {
		// Track in-progress requests separately
		if log.ID != "" {
			s.inProgressMap[log.ID] = log
		}
	} else {
		// Remove from in-progress when finished
		if log.ID != "" {
			delete(s.inProgressMap, log.ID)
		}

		// Upsert by ID in recentLogs
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
	}

broadcast:
	if s.sseHub != nil {
		s.sseHub.BroadcastRequestLog(log)
	}
}

func isInProgressStatus(status string) bool {
	s := strings.ToLower(strings.TrimSpace(status))
	return s == "in_progress" || s == "streaming" || s == "pending"
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

	if s.sseHub != nil {
		s.sseHub.BroadcastTokenStats(stats)
	}
}

// GetRecentLogs returns the most recent completed request logs
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

// GetInProgressLogs returns all currently in-progress request logs
func (s *StatsManager) GetInProgressLogs() []*RequestLog {
	s.mu.RLock()
	defer s.mu.RUnlock()

	result := make([]*RequestLog, 0, len(s.inProgressMap))
	for _, log := range s.inProgressMap {
		cp := *log
		result = append(result, &cp)
	}
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
