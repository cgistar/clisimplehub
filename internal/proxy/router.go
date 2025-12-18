// Package proxy implements the HTTP proxy server for AI API requests.
package proxy

import (
	"errors"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Router handles request routing based on URL path
type Router interface {
	// DetectInterfaceType determines the interface type from the request path
	DetectInterfaceType(path string) InterfaceType

	// GetActiveEndpoint returns the currently active endpoint for the given interface type
	GetActiveEndpoint(interfaceType InterfaceType) *Endpoint

	// GetNextEndpoint returns the next available endpoint after the current one
	GetNextEndpoint(interfaceType InterfaceType, current *Endpoint) *Endpoint

	// SetActiveEndpoint sets the active endpoint for the given interface type
	SetActiveEndpoint(interfaceType InterfaceType, endpoint *Endpoint) error

	// GetEndpointsByType returns all endpoints for the given interface type
	GetEndpointsByType(interfaceType InterfaceType) []*Endpoint

	// DisableEndpoint temporarily disables an endpoint in memory (does not persist).
	// Returns the until timestamp of this temporary disable (zero if no-op).
	DisableEndpoint(interfaceType InterfaceType, endpoint *Endpoint) time.Time

	// LoadEndpoints loads endpoints into the router
	LoadEndpoints(endpoints []*Endpoint)
}

// ErrNoEndpointFound is returned when no endpoint is found for the given interface type
var ErrNoEndpointFound = errors.New("no endpoint found for interface type")

// ErrEndpointNotFound is returned when the specified endpoint is not found
var ErrEndpointNotFound = errors.New("endpoint not found")

// DefaultRouter is the default implementation of the Router interface
type DefaultRouter struct {
	endpoints      map[InterfaceType][]*Endpoint
	active         map[InterfaceType]*Endpoint
	mu             sync.RWMutex
	tempDisableTTL time.Duration
	tempDisabled   map[InterfaceType]map[string]*tempDisableEntry
}

type tempDisableEntry struct {
	until           time.Time
	previousEnabled bool
}

const defaultTempDisableTTL = 5 * time.Minute

// NewRouter creates a new DefaultRouter instance
func NewRouter() *DefaultRouter {
	return &DefaultRouter{
		endpoints:      make(map[InterfaceType][]*Endpoint),
		active:         make(map[InterfaceType]*Endpoint),
		tempDisableTTL: defaultTempDisableTTL,
		tempDisabled:   make(map[InterfaceType]map[string]*tempDisableEntry),
	}
}

func endpointKey(ep *Endpoint) string {
	if ep == nil {
		return ""
	}
	if ep.ID != 0 {
		return "id:" + strconv.FormatInt(ep.ID, 10)
	}
	return "name:" + ep.Name
}

// SetTempDisableTTL sets the in-memory temporary disable duration (<=0 uses default).
func (r *DefaultRouter) SetTempDisableTTL(ttl time.Duration) {
	if ttl <= 0 {
		ttl = defaultTempDisableTTL
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.tempDisableTTL = ttl
}

func (r *DefaultRouter) restoreExpiredLocked(interfaceType InterfaceType) {
	disabled := r.tempDisabled[interfaceType]
	if len(disabled) == 0 {
		return
	}

	now := time.Now()
	eps := r.endpoints[interfaceType]

	for key, entry := range disabled {
		if entry == nil || now.Before(entry.until) {
			continue
		}

		for _, ep := range eps {
			if ep == nil {
				continue
			}
			if endpointKey(ep) != key {
				continue
			}
			ep.Enabled = entry.previousEnabled
			break
		}
		delete(disabled, key)
	}

	if len(disabled) == 0 {
		delete(r.tempDisabled, interfaceType)
	}
}

// DetectInterfaceType determines the interface type from the request path
// Requirements: 3.1, 3.2, 3.3, 3.4
func (r *DefaultRouter) DetectInterfaceType(path string) InterfaceType {
	// Normalize path to lowercase for comparison
	lowerPath := strings.ToLower(path)

	// Requirement 3.1: /v1/messages -> claude
	if strings.HasPrefix(lowerPath, "/v1/messages") {
		return InterfaceTypeClaude
	}

	// 兼容 OpenAI Chat Completions 路径：/v1/chat/completions 或以 /chat/completions 结尾的都走 chat
	if strings.HasPrefix(lowerPath, "/v1/chat/completions") || strings.HasSuffix(lowerPath, "/chat/completions") {
		return InterfaceTypeChat
	}

	// OpenAI Responses 路径：/v1/responses 或以 /responses 结尾的都走 codex
	if strings.HasPrefix(lowerPath, "/v1/responses") || strings.HasSuffix(lowerPath, "/responses") {
		return InterfaceTypeCodex
	}

	// Requirement 3.3: path containing /gemini -> gemini
	if strings.Contains(lowerPath, "/gemini") {
		return InterfaceTypeGemini
	}

	// Requirement 3.4: /chat -> chat
	if strings.HasPrefix(lowerPath, "/chat") {
		return InterfaceTypeChat
	}

	// Default to claude if no match (could also return empty string)
	return InterfaceTypeClaude
}

// LoadEndpoints loads endpoints into the router, organizing them by interface type
func (r *DefaultRouter) LoadEndpoints(endpoints []*Endpoint) {
	r.mu.Lock()
	defer r.mu.Unlock()

	// Save current active endpoint IDs before clearing
	previousActiveIDs := make(map[InterfaceType]int64)
	for interfaceType, ep := range r.active {
		if ep != nil && ep.ID != 0 {
			previousActiveIDs[interfaceType] = ep.ID
		}
	}

	// Clear existing endpoints and active map
	r.endpoints = make(map[InterfaceType][]*Endpoint)
	r.active = make(map[InterfaceType]*Endpoint)
	// Clear any runtime-only temporary disables
	r.tempDisabled = make(map[InterfaceType]map[string]*tempDisableEntry)

	// Group endpoints by interface type
	for _, ep := range endpoints {
		if ep == nil {
			continue
		}
		interfaceType := InterfaceType(ep.InterfaceType)
		r.endpoints[interfaceType] = append(r.endpoints[interfaceType], ep)
	}

	// Sort each group by priority ascending, then by name (Requirement 4.4, 6.3)
	for interfaceType := range r.endpoints {
		sort.Slice(r.endpoints[interfaceType], func(i, j int) bool {
			if r.endpoints[interfaceType][i].Priority != r.endpoints[interfaceType][j].Priority {
				return r.endpoints[interfaceType][i].Priority < r.endpoints[interfaceType][j].Priority
			}
			return r.endpoints[interfaceType][i].Name < r.endpoints[interfaceType][j].Name
		})
	}

	// Set active endpoints: restore previous active by ID, or use Active flag, or first enabled
	for interfaceType, eps := range r.endpoints {
		// First, try to restore the previously active endpoint by ID
		if prevID, ok := previousActiveIDs[interfaceType]; ok && prevID != 0 {
			for _, ep := range eps {
				if ep.ID == prevID && ep.Enabled {
					r.active[interfaceType] = ep
					break
				}
			}
		}

		// If not restored, try to find an endpoint marked as active and enabled
		if r.active[interfaceType] == nil {
			for _, ep := range eps {
				if ep.Active && ep.Enabled {
					r.active[interfaceType] = ep
					break
				}
			}
		}

		// If still no active endpoint found, use the first enabled endpoint
		if r.active[interfaceType] == nil {
			for _, ep := range eps {
				if ep.Enabled {
					r.active[interfaceType] = ep
					break
				}
			}
		}
	}
}

// GetActiveEndpoint returns the currently active endpoint for the given interface type
// Requirements: 3.5
func (r *DefaultRouter) GetActiveEndpoint(interfaceType InterfaceType) *Endpoint {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.restoreExpiredLocked(interfaceType)

	// Ensure active endpoint is enabled; if not, pick the first enabled one.
	if active := r.active[interfaceType]; active != nil && active.Enabled {
		return active
	}

	eps := r.endpoints[interfaceType]
	for _, ep := range eps {
		if ep != nil && ep.Enabled {
			r.active[interfaceType] = ep
			return ep
		}
	}

	r.active[interfaceType] = nil
	return nil
}

// GetNextEndpoint returns the next available enabled endpoint after the current one
// Endpoints are ordered by sort_order ascending (Requirement 4.4)
func (r *DefaultRouter) GetNextEndpoint(interfaceType InterfaceType, current *Endpoint) *Endpoint {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.restoreExpiredLocked(interfaceType)

	eps := r.endpoints[interfaceType]
	if len(eps) == 0 {
		return nil
	}

	// If current is nil, return the first enabled endpoint
	if current == nil {
		for _, ep := range eps {
			if ep.Enabled {
				return ep
			}
		}
		return nil
	}

	// Find the current endpoint's position
	currentIdx := -1
	for i, ep := range eps {
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

	// If current not found, return the first enabled endpoint
	if currentIdx == -1 {
		for _, ep := range eps {
			if ep.Enabled {
				return ep
			}
		}
		return nil
	}

	// Find the next enabled endpoint after current (wrapping around)
	for i := 1; i <= len(eps); i++ {
		nextIdx := (currentIdx + i) % len(eps)
		if !eps[nextIdx].Enabled {
			continue
		}
		if current.ID != 0 {
			if eps[nextIdx].ID != current.ID {
				return eps[nextIdx]
			}
			continue
		}
		if eps[nextIdx].Name != current.Name {
			return eps[nextIdx]
		}
	}

	return nil
}

// SetActiveEndpoint sets the active endpoint for the given interface type
// Requirements: 6.4 - only enabled endpoints can be set as active
func (r *DefaultRouter) SetActiveEndpoint(interfaceType InterfaceType, endpoint *Endpoint) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.restoreExpiredLocked(interfaceType)

	if endpoint == nil {
		return ErrEndpointNotFound
	}

	// Verify the endpoint exists and is enabled
	eps := r.endpoints[interfaceType]
	for _, ep := range eps {
		if !ep.Enabled {
			continue
		}
		// 优先用 ID 精确匹配，避免同名端点被错误选中
		if endpoint.ID != 0 {
			if ep.ID == endpoint.ID {
				r.active[interfaceType] = ep
				return nil
			}
			continue
		}
		if ep.Name == endpoint.Name {
			r.active[interfaceType] = ep
			return nil
		}
	}

	return ErrEndpointNotFound
}

// DisableEndpoint disables an endpoint in memory without persisting to config.json.
func (r *DefaultRouter) DisableEndpoint(interfaceType InterfaceType, endpoint *Endpoint) time.Time {
	if endpoint == nil {
		return time.Time{}
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	r.restoreExpiredLocked(interfaceType)

	eps := r.endpoints[interfaceType]
	if len(eps) == 0 {
		return time.Time{}
	}

	targetIdx := -1
	for i, ep := range eps {
		if endpoint.ID != 0 {
			if ep.ID == endpoint.ID {
				targetIdx = i
				break
			}
			continue
		}
		if ep.Name == endpoint.Name {
			targetIdx = i
			break
		}
	}
	if targetIdx == -1 {
		return time.Time{}
	}

	key := endpointKey(eps[targetIdx])
	if key == "" {
		return time.Time{}
	}

	if r.tempDisabled[interfaceType] == nil {
		r.tempDisabled[interfaceType] = make(map[string]*tempDisableEntry)
	}
	entry := r.tempDisabled[interfaceType][key]
	if entry == nil {
		entry = &tempDisableEntry{previousEnabled: eps[targetIdx].Enabled}
		r.tempDisabled[interfaceType][key] = entry
	}
	entry.until = time.Now().Add(r.tempDisableTTL)

	eps[targetIdx].Enabled = false

	active := r.active[interfaceType]
	if active == nil {
		return entry.until
	}

	isActiveTarget := false
	if endpoint.ID != 0 {
		isActiveTarget = active.ID == endpoint.ID
	} else {
		isActiveTarget = active.Name == endpoint.Name
	}
	if !isActiveTarget {
		return entry.until
	}

	// Pick the next enabled endpoint as active (if any); otherwise clear active.
	r.active[interfaceType] = nil
	for i := 1; i <= len(eps); i++ {
		nextIdx := (targetIdx + i) % len(eps)
		if eps[nextIdx].Enabled {
			r.active[interfaceType] = eps[nextIdx]
			return entry.until
		}
	}
	return entry.until
}

// GetEndpointsByType returns all endpoints for the given interface type
// Endpoints are sorted by sort_order ascending (Requirement 6.2, 6.3)
func (r *DefaultRouter) GetEndpointsByType(interfaceType InterfaceType) []*Endpoint {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.restoreExpiredLocked(interfaceType)

	eps := r.endpoints[interfaceType]
	if eps == nil {
		return []*Endpoint{}
	}

	// Return a copy to prevent external modification
	result := make([]*Endpoint, len(eps))
	copy(result, eps)
	return result
}

// GetEnabledEndpointsByType returns only enabled endpoints for the given interface type
// Requirements: 6.4 - active selector contains only enabled endpoints
func (r *DefaultRouter) GetEnabledEndpointsByType(interfaceType InterfaceType) []*Endpoint {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.restoreExpiredLocked(interfaceType)

	eps := r.endpoints[interfaceType]
	var result []*Endpoint
	for _, ep := range eps {
		if ep.Enabled {
			result = append(result, ep)
		}
	}
	return result
}
