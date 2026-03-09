package xrayplugin

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// XRayService manages the xray-core instance lifecycle and node state.

const (
	maxSubscriptionBytes  = 8 << 20 // 8MiB
	subscriptionUserAgent = "mozilla/5.0 (iPhone; CPU iPhone OS 15_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/15.0 Mobile/15E148 Safari/604.1"
)

type XRayService struct {
	mu       sync.RWMutex
	config   *configStore
	nodes    []ProxyNode
	running  bool
	instance io.Closer // xray instance (closed on stop)
	dataDir  string
}

// NewXRayService creates a new XRayService with the given config path.
func NewXRayService(cfgPath string) (*XRayService, error) {
	cs := newConfigStore(cfgPath)
	if err := cs.Load(); err != nil {
		log.Printf("[xray] config load warning: %v (using defaults)", err)
	}

	svc := &XRayService{
		config:  cs,
		dataDir: filepath.Dir(cfgPath),
	}

	// Load cached subscription data
	svc.loadCachedNodes()

	return svc, nil
}

// Start starts the xray proxy with the currently selected node.
func (s *XRayService) Start() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.running {
		return fmt.Errorf("xray already running")
	}

	cfg := s.config.Get()
	activeIdx := activeSubscriptionIndex(cfg)
	if activeIdx < 0 {
		return fmt.Errorf("no active subscription")
	}

	activeSub := cfg.Subscriptions[activeIdx]
	selected := strings.TrimSpace(activeSub.SelectedNode)

	if selected == "" {
		return fmt.Errorf("no selected node in active subscription")
	}
	if !hasNodeByName(activeSub.Nodes, selected) {
		return fmt.Errorf("selected node not found in active subscription: %s", selected)
	}

	// Find node directly from subscription nodes (value copy — cfg is a snapshot)
	var node ProxyNode
	found := false
	for i := range activeSub.Nodes {
		if activeSub.Nodes[i].Name == selected {
			node = activeSub.Nodes[i]
			found = true
			break
		}
	}
	if !found {
		return fmt.Errorf("selected node not found in active subscription: %s", selected)
	}

	return s.startWithNode(&node, cfg)
}

// Stop stops the running xray instance.
func (s *XRayService) Stop() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.stopLocked()
}

func (s *XRayService) stopLocked() error {
	if !s.running || s.instance == nil {
		s.running = false
		return nil
	}
	err := s.instance.Close()
	s.instance = nil
	s.running = false
	if err != nil {
		return fmt.Errorf("xray stop: %w", err)
	}
	log.Println("[xray] stopped")
	return nil
}

// Reload reloads config, stops current instance (if any), then starts only when
// there is an active subscription with a valid selected node.
func (s *XRayService) Reload() error {
	if err := s.config.Load(); err != nil {
		return err
	}
	s.loadCachedNodes()

	if err := s.Stop(); err != nil {
		log.Printf("[xray] reload stop error: %v", err)
	}

	cfg := s.config.Get()
	activeIdx := activeSubscriptionIndex(cfg)
	if activeIdx < 0 {
		return nil
	}

	activeSub := cfg.Subscriptions[activeIdx]
	selected := strings.TrimSpace(activeSub.SelectedNode)
	if selected == "" {
		return nil
	}
	if !hasNodeByName(activeSub.Nodes, selected) {
		log.Printf("[xray] reload: selected node not found in active subscription: %s", selected)
		return nil
	}

	return s.Start()
}

// SelectNode changes the selected node and restarts if running.
func (s *XRayService) SelectNode(nodeName string) error {
	nodeName = strings.TrimSpace(nodeName)
	if nodeName == "" {
		return fmt.Errorf("node name is required")
	}

	cfg := s.config.Get()
	activeIdx := activeSubscriptionIndex(cfg)
	if activeIdx < 0 {
		return fmt.Errorf("no active subscription")
	}
	activeSub := cfg.Subscriptions[activeIdx]

	if s.findNodeInSubscriptionSafe(activeSub.ID, nodeName) == nil {
		return fmt.Errorf("node not found in active subscription: %s", nodeName)
	}
	if err := s.setSubscriptionSelectedNode(activeSub.ID, nodeName); err != nil {
		return fmt.Errorf("update selected node: %w", err)
	}

	s.mu.RLock()
	wasRunning := s.running
	s.mu.RUnlock()
	if wasRunning {
		if err := s.Stop(); err != nil {
			log.Printf("[xray] select node stop error: %v", err)
		}
		return s.Start()
	}
	return nil
}

// GetStatus returns the current service status.
func (s *XRayService) GetStatus() StatusResponse {
	s.mu.RLock()
	defer s.mu.RUnlock()

	cfg := s.config.Get()
	resp := StatusResponse{
		Running:      s.running,
		SelectedNode: activeSelectedNode(cfg),
		NodeCount:    len(s.nodes),
	}
	if s.running {
		resp.SocksAddr = fmt.Sprintf("%s:%d", cfg.SocksListen, cfg.SocksPort)
	}
	return resp
}

// GetNodes returns the current node list.
func (s *XRayService) GetNodes() []ProxyNode {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]ProxyNode, len(s.nodes))
	copy(out, s.nodes)
	return out
}

// RefreshSubscriptions downloads and parses all enabled subscriptions.
func (s *XRayService) RefreshSubscriptions(ctx context.Context) (*RefreshResult, error) {
	cfg := s.config.Get()
	result := &RefreshResult{}
	var allNodes []ProxyNode

	client := &http.Client{Timeout: 30 * time.Second}

	for i := range cfg.Subscriptions {
		sub := &cfg.Subscriptions[i]
		if !sub.Enabled {
			continue
		}
		if strings.TrimSpace(sub.URL) == "" {
			continue
		}

		req, err := http.NewRequestWithContext(ctx, "GET", sub.URL, nil)
		if err != nil {
			result.Errors = append(result.Errors, fmt.Sprintf("%s: %v", sub.Name, err))
			continue
		}
		req.Header.Set("User-Agent", subscriptionUserAgent)

		resp, err := client.Do(req)
		if err != nil {
			result.Errors = append(result.Errors, fmt.Sprintf("%s: %v", sub.Name, err))
			continue
		}
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			resp.Body.Close()
			result.Errors = append(result.Errors, fmt.Sprintf("%s: http %d", sub.Name, resp.StatusCode))
			continue
		}
		body, err := io.ReadAll(io.LimitReader(resp.Body, maxSubscriptionBytes))
		resp.Body.Close()
		if err != nil {
			result.Errors = append(result.Errors, fmt.Sprintf("%s: read body: %v", sub.Name, err))
			continue
		}

		nodes, warnings := DetectAndParse(string(body), sub.Format, sub.ID)
		for _, w := range warnings {
			log.Printf("[xray] %s: %s", sub.Name, w)
		}
		allNodes = append(allNodes, nodes...)

		sub.LastUpdated = time.Now().Format(time.RFC3339)
		sub.Nodes = nodes // Save nodes to subscription
	}

	// Update subscription timestamps and nodes
	if err := s.config.Update(func(c *XRayConfig) {
		for i := range c.Subscriptions {
			for _, sub := range cfg.Subscriptions {
				if c.Subscriptions[i].ID == sub.ID {
					c.Subscriptions[i].LastUpdated = sub.LastUpdated
					c.Subscriptions[i].Nodes = sub.Nodes
				}
			}
		}
	}); err != nil {
		result.Errors = append(result.Errors, fmt.Sprintf("update config: %v", err))
	}

	s.mu.Lock()
	s.nodes = allNodes
	s.mu.Unlock()

	result.TotalNodes = len(allNodes)
	return result, nil
}

// AddSubscription adds a new subscription source. URL may be empty for local-only subscriptions.
func (s *XRayService) AddSubscription(name, rawURL string) error {
	rawURL = strings.TrimSpace(rawURL)
	if rawURL != "" {
		if _, err := url.ParseRequestURI(rawURL); err != nil {
			return fmt.Errorf("invalid subscription url: %w", err)
		}
	}
	name = strings.TrimSpace(name)
	if name == "" {
		name = "Unnamed"
	}
	id := fmt.Sprintf("sub_%d", time.Now().UnixMilli())
	return s.config.Update(func(cfg *XRayConfig) {
		cfg.Subscriptions = append(cfg.Subscriptions, Subscription{
			ID:      id,
			Name:    name,
			URL:     rawURL,
			Enabled: true,
			Format:  "auto",
		})
	})
}

// RemoveSubscription removes a subscription by ID.
func (s *XRayService) RemoveSubscription(id string) error {
	id = strings.TrimSpace(id)
	if id == "" {
		return fmt.Errorf("subscription id is required")
	}

	beforeCfg := s.config.Get()

	removed := false
	removedActive := false
	if err := s.config.Update(func(cfg *XRayConfig) {
		for i, sub := range cfg.Subscriptions {
			if sub.ID == id {
				removed = true
				removedActive = sub.Active
				cfg.Subscriptions = append(cfg.Subscriptions[:i], cfg.Subscriptions[i+1:]...)
				break
			}
		}
		if strings.TrimSpace(cfg.DialerProxyID) == id {
			cfg.DialerProxyID = ""
		}
	}); err != nil {
		return err
	}
	if !removed {
		return fmt.Errorf("subscription not found: %s", id)
	}

	afterCfg := s.config.Get()
	runtimeChanged := runtimeChangedBetweenConfigs(beforeCfg, afterCfg)

	wasRunning := false

	// Clean up nodes and stop proxy if needed (atomic operation)
	s.mu.Lock()
	var newNodes []ProxyNode
	for _, n := range s.nodes {
		if n.SourceID != id {
			newNodes = append(newNodes, n)
		}
	}
	s.nodes = newNodes
	wasRunning = s.running

	// Stop proxy if we removed the active subscription
	if removedActive && s.running {
		if err := s.stopLocked(); err != nil {
			log.Printf("[xray] remove subscription stop error: %v", err)
		}
	}
	s.mu.Unlock()

	if !removedActive && wasRunning && runtimeChanged {
		if err := s.Stop(); err != nil {
			log.Printf("[xray] remove subscription stop error: %v", err)
		}
		if err := s.Start(); err != nil {
			return fmt.Errorf("restart with updated dialer proxy: %w", err)
		}
	}

	log.Printf("[xray] removed subscription: %s (active: %v)", id, removedActive)
	return nil
}

// UpdateSubscription updates a subscription's name and URL. URL may be empty for local-only subscriptions.
func (s *XRayService) UpdateSubscription(id, name, rawURL string) error {
	id = strings.TrimSpace(id)
	if id == "" {
		return fmt.Errorf("subscription id is required")
	}
	rawURL = strings.TrimSpace(rawURL)
	if rawURL != "" {
		if _, err := url.ParseRequestURI(rawURL); err != nil {
			return fmt.Errorf("invalid subscription url: %w", err)
		}
	}
	name = strings.TrimSpace(name)
	if name == "" {
		name = "Unnamed"
	}
	return s.config.Update(func(cfg *XRayConfig) {
		for i := range cfg.Subscriptions {
			if cfg.Subscriptions[i].ID == id {
				cfg.Subscriptions[i].Name = name
				cfg.Subscriptions[i].URL = rawURL
				break
			}
		}
	})
}

// --- internal helpers ---

// activeSubscriptionIndex returns the index of the active subscription, or -1 if none.
func activeSubscriptionIndex(cfg *XRayConfig) int {
	for i := range cfg.Subscriptions {
		if cfg.Subscriptions[i].Active {
			return i
		}
	}
	return -1
}

// firstEnabledSubscriptionIndex returns the index of the first enabled subscription, or -1 if none.
func firstEnabledSubscriptionIndex(cfg *XRayConfig) int {
	for i := range cfg.Subscriptions {
		if cfg.Subscriptions[i].Enabled {
			return i
		}
	}
	return -1
}

// activeSelectedNode returns the selected node name from the active subscription.
func activeSelectedNode(cfg *XRayConfig) string {
	activeIdx := activeSubscriptionIndex(cfg)
	if activeIdx < 0 {
		return ""
	}
	return strings.TrimSpace(cfg.Subscriptions[activeIdx].SelectedNode)
}

func buildRuntimeJSONForActiveSubscription(cfg *XRayConfig) ([]byte, bool, error) {
	if cfg == nil {
		return nil, false, nil
	}

	activeIdx := activeSubscriptionIndex(cfg)
	if activeIdx < 0 {
		return nil, false, nil
	}
	activeSub := cfg.Subscriptions[activeIdx]

	selected := strings.TrimSpace(activeSub.SelectedNode)
	if selected == "" || !hasNodeByName(activeSub.Nodes, selected) {
		return nil, false, nil
	}

	for i := range activeSub.Nodes {
		if activeSub.Nodes[i].Name != selected {
			continue
		}
		node := activeSub.Nodes[i]
		runtimeJSON, err := BuildRuntimeJSON(&node, cfg)
		if err != nil {
			return nil, false, err
		}
		return runtimeJSON, true, nil
	}

	return nil, false, nil
}

func runtimeChangedBetweenConfigs(beforeCfg, afterCfg *XRayConfig) bool {
	beforeRuntime, beforeReady, beforeErr := buildRuntimeJSONForActiveSubscription(beforeCfg)
	afterRuntime, afterReady, afterErr := buildRuntimeJSONForActiveSubscription(afterCfg)

	if beforeErr != nil || afterErr != nil {
		if beforeErr != nil {
			log.Printf("[xray] evaluate runtime(before) failed: %v", beforeErr)
		}
		if afterErr != nil {
			log.Printf("[xray] evaluate runtime(after) failed: %v", afterErr)
		}
		return true
	}

	if beforeReady != afterReady {
		return true
	}
	if !beforeReady {
		return false
	}

	return !bytes.Equal(beforeRuntime, afterRuntime)
}

// setSubscriptionSelectedNode updates the selected node on a specific subscription.
func (s *XRayService) setSubscriptionSelectedNode(subscriptionID, nodeName string) error {
	nodeName = strings.TrimSpace(nodeName)
	updated := false
	if err := s.config.Update(func(cfg *XRayConfig) {
		for i := range cfg.Subscriptions {
			if cfg.Subscriptions[i].ID == subscriptionID {
				cfg.Subscriptions[i].SelectedNode = nodeName
				updated = true
				return
			}
		}
	}); err != nil {
		return err
	}
	if !updated {
		return fmt.Errorf("subscription not found: %s", subscriptionID)
	}
	return nil
}

// hasNodeByName checks if a node with the given name exists in the slice.
func hasNodeByName(nodes []ProxyNode, nodeName string) bool {
	for _, n := range nodes {
		if n.Name == nodeName {
			return true
		}
	}
	return false
}

// uniqueNodeName returns a unique name by appending _2, _3, ... if the name already exists.
func uniqueNodeName(name string, existing []ProxyNode) string {
	if !hasNodeByName(existing, name) {
		return name
	}
	for i := 2; ; i++ {
		candidate := fmt.Sprintf("%s_%d", name, i)
		if !hasNodeByName(existing, candidate) {
			return candidate
		}
	}
}

func (s *XRayService) startWithNode(node *ProxyNode, cfg *XRayConfig) error {
	cfg, err := s.ensureRuntimeTemplate(cfg)
	if err != nil {
		return fmt.Errorf("ensure runtime template: %w", err)
	}

	runtimeJSON, err := BuildRuntimeJSON(node, cfg)
	if err != nil {
		return fmt.Errorf("build runtime config: %w", err)
	}

	// Save runtime JSON for debugging
	runtimePath := filepath.Join(s.dataDir, "xray-runtime.json")
	_ = os.WriteFile(runtimePath, runtimeJSON, 0600)

	inst, err := startXRayInstance(runtimeJSON)
	if err != nil {
		return fmt.Errorf("start xray: %w", err)
	}

	s.instance = inst
	s.running = true
	log.Printf("[xray] started with node %s (%s:%d)", node.Name, node.Server, node.Port)
	return nil
}

func (s *XRayService) ensureRuntimeTemplate(cfg *XRayConfig) (*XRayConfig, error) {
	if cfg != nil {
		raw := bytes.TrimSpace(cfg.Template)
		if len(raw) > 0 && !bytes.Equal(raw, jsonNull) {
			return cfg, nil
		}
	}

	templateJSON, err := BuildRuntimeTemplateJSON(cfg)
	if err != nil {
		return nil, err
	}

	if err := s.config.Update(func(c *XRayConfig) {
		raw := bytes.TrimSpace(c.Template)
		if len(raw) > 0 && !bytes.Equal(raw, jsonNull) {
			return
		}
		c.Template = append(json.RawMessage(nil), templateJSON...)
	}); err != nil {
		return nil, err
	}

	return s.config.Get(), nil
}

func (s *XRayService) findNodeSafe(name string) *ProxyNode {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for i := range s.nodes {
		if s.nodes[i].Name == name {
			n := s.nodes[i]
			return &n
		}
	}
	return nil
}

func (s *XRayService) findNodeInSubscriptionSafe(subscriptionID, name string) *ProxyNode {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for i := range s.nodes {
		if s.nodes[i].SourceID == subscriptionID && s.nodes[i].Name == name {
			n := s.nodes[i]
			return &n
		}
	}
	return nil
}

func (s *XRayService) loadCachedNodes() {
	cfg := s.config.Get()
	var allNodes []ProxyNode
	for _, sub := range cfg.Subscriptions {
		if sub.Enabled && len(sub.Nodes) > 0 {
			allNodes = append(allNodes, sub.Nodes...)
		}
	}
	s.mu.Lock()
	s.nodes = allNodes
	s.mu.Unlock()
}

// ExportSyncData returns the current config and nodes for sync/backup.
func (s *XRayService) ExportSyncData() *XRaySyncData {
	cfg := s.config.Get()
	return &XRaySyncData{Config: *cfg}
}

// ImportSyncData replaces config from sync data and rebuilds nodes from subscriptions.
func (s *XRayService) ImportSyncData(data *XRaySyncData) error {
	if data == nil {
		return fmt.Errorf("sync data is nil")
	}

	// Apply defaults to imported config (mirrors configStore.Load behavior)
	importedCfg := data.Config
	if importedCfg.SocksListen == "" {
		importedCfg.SocksListen = defaultConfig.SocksListen
	}
	if importedCfg.SocksPort == 0 {
		importedCfg.SocksPort = defaultConfig.SocksPort
	}
	if importedCfg.LogLevel == "" {
		importedCfg.LogLevel = defaultConfig.LogLevel
	}
	if importedCfg.Subscriptions == nil {
		importedCfg.Subscriptions = []Subscription{}
	}

	normalizeImportedConfig(&importedCfg, data.Nodes)

	// 1. Replace config on disk
	if err := s.config.Update(func(cfg *XRayConfig) {
		*cfg = importedCfg
	}); err != nil {
		return fmt.Errorf("save config: %w", err)
	}

	// 2. Rebuild in-memory nodes from subscription data
	s.loadCachedNodes()
	return nil
}

// normalizeImportedConfig handles backward compatibility and config normalization.
func normalizeImportedConfig(cfg *XRayConfig, legacyNodes []ProxyNode) {
	if cfg.Subscriptions == nil {
		cfg.Subscriptions = []Subscription{}
	}

	// Backward compatibility: hydrate per-subscription nodes from legacy flat payload.
	if len(legacyNodes) > 0 {
		nodesBySource := make(map[string][]ProxyNode)
		var noSource []ProxyNode
		for _, n := range legacyNodes {
			sourceID := strings.TrimSpace(n.SourceID)
			if sourceID == "" {
				noSource = append(noSource, n)
				continue
			}
			nodesBySource[sourceID] = append(nodesBySource[sourceID], n)
		}
		for i := range cfg.Subscriptions {
			if len(cfg.Subscriptions[i].Nodes) > 0 {
				continue
			}
			var migrated []ProxyNode
			if nodes, ok := nodesBySource[cfg.Subscriptions[i].ID]; ok {
				migrated = append([]ProxyNode(nil), nodes...)
			} else if len(cfg.Subscriptions) == 1 && len(noSource) > 0 {
				migrated = append([]ProxyNode(nil), noSource...)
			}
			for j := range migrated {
				if strings.TrimSpace(migrated[j].SourceID) == "" {
					migrated[j].SourceID = cfg.Subscriptions[i].ID
				}
			}
			cfg.Subscriptions[i].Nodes = migrated
		}
	}

	// Normalize malformed configs with multiple active subscriptions.
	firstActive := -1
	for i := range cfg.Subscriptions {
		if !cfg.Subscriptions[i].Active {
			continue
		}
		if firstActive < 0 {
			firstActive = i
			continue
		}
		cfg.Subscriptions[i].Active = false
	}

	// Drop stale selected node on active subscription to avoid starting wrong nodes.
	if firstActive >= 0 {
		sub := &cfg.Subscriptions[firstActive]
		sub.SelectedNode = strings.TrimSpace(sub.SelectedNode)
		if sub.SelectedNode != "" && !hasNodeByName(sub.Nodes, sub.SelectedNode) {
			sub.SelectedNode = ""
		}
	}

	cfg.DialerProxyID = strings.TrimSpace(cfg.DialerProxyID)
	if cfg.DialerProxyID != "" {
		found := false
		for i := range cfg.Subscriptions {
			if cfg.Subscriptions[i].ID == cfg.DialerProxyID {
				found = true
				break
			}
		}
		if !found {
			cfg.DialerProxyID = ""
		}
	}
}

// UpdateNodeLatency updates a node's latency value.
func (s *XRayService) UpdateNodeLatency(nodeName string, latency int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.nodes {
		if s.nodes[i].Name == nodeName {
			s.nodes[i].Latency = latency
			return
		}
	}
}

// ToggleSubscription toggles the enabled state of a subscription.
func (s *XRayService) ToggleSubscription(id string) error {
	id = strings.TrimSpace(id)
	if id == "" {
		return fmt.Errorf("subscription id is required")
	}

	beforeCfg := s.config.Get()
	updated := false
	if err := s.config.Update(func(cfg *XRayConfig) {
		for i := range cfg.Subscriptions {
			if cfg.Subscriptions[i].ID == id {
				cfg.Subscriptions[i].Enabled = !cfg.Subscriptions[i].Enabled
				updated = true
				break
			}
		}
	}); err != nil {
		return err
	}
	if !updated {
		return fmt.Errorf("subscription not found: %s", id)
	}

	afterCfg := s.config.Get()
	runtimeChanged := runtimeChangedBetweenConfigs(beforeCfg, afterCfg)

	s.mu.RLock()
	wasRunning := s.running
	s.mu.RUnlock()
	if wasRunning && runtimeChanged {
		if err := s.Stop(); err != nil {
			log.Printf("[xray] toggle subscription stop error: %v", err)
		}
		if err := s.Start(); err != nil {
			return fmt.Errorf("restart with updated dialer proxy: %w", err)
		}
	}

	return nil
}

// SetActiveSubscription sets a subscription as active (only one can be active).
// If xray is already running, it restarts on the newly active subscription.
func (s *XRayService) SetActiveSubscription(id string) error {
	id = strings.TrimSpace(id)
	if id == "" {
		return fmt.Errorf("subscription id is required")
	}

	updated := false
	var subName string
	if err := s.config.Update(func(cfg *XRayConfig) {
		target := -1
		for i := range cfg.Subscriptions {
			if cfg.Subscriptions[i].ID == id {
				target = i
				subName = cfg.Subscriptions[i].Name
				break
			}
		}
		if target < 0 {
			return
		}
		// Deactivate all subscriptions first
		for i := range cfg.Subscriptions {
			cfg.Subscriptions[i].Active = (i == target)
		}
		updated = true
	}); err != nil {
		return err
	}
	if !updated {
		return fmt.Errorf("subscription not found: %s", id)
	}
	log.Printf("[xray] set active subscription: %s (%s)", subName, id)

	s.mu.RLock()
	wasRunning := s.running
	s.mu.RUnlock()
	if wasRunning {
		if err := s.Stop(); err != nil {
			log.Printf("[xray] set active subscription stop error: %v", err)
		}
		if err := s.Start(); err != nil {
			return fmt.Errorf("restart with active subscription: %w", err)
		}
	}

	return nil
}

// SetDialerProxySubscription sets the subscription used as dialer proxy.
// Pass empty id to clear.
func (s *XRayService) SetDialerProxySubscription(id string) error {
	id = strings.TrimSpace(id)

	beforeCfg := s.config.Get()
	if id != "" {
		found := false
		for i := range beforeCfg.Subscriptions {
			if beforeCfg.Subscriptions[i].ID == id {
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("subscription not found: %s", id)
		}
	}

	if strings.TrimSpace(beforeCfg.DialerProxyID) == id {
		return nil
	}

	if err := s.config.Update(func(cfg *XRayConfig) {
		cfg.DialerProxyID = id
	}); err != nil {
		return err
	}

	afterCfg := s.config.Get()
	runtimeChanged := runtimeChangedBetweenConfigs(beforeCfg, afterCfg)

	s.mu.RLock()
	wasRunning := s.running
	s.mu.RUnlock()
	if wasRunning && runtimeChanged {
		if err := s.Stop(); err != nil {
			log.Printf("[xray] set dialer proxy stop error: %v", err)
		}
		if err := s.Start(); err != nil {
			return fmt.Errorf("restart with updated dialer proxy: %w", err)
		}
	}

	return nil
}

// UpdateSubscriptionSelectedNode updates the selected node for a subscription.
func (s *XRayService) UpdateSubscriptionSelectedNode(id, nodeName string) error {
	id = strings.TrimSpace(id)
	if id == "" {
		return fmt.Errorf("subscription id is required")
	}
	nodeName = strings.TrimSpace(nodeName)

	cfg := s.config.Get()
	subIdx := -1
	for i := range cfg.Subscriptions {
		if cfg.Subscriptions[i].ID == id {
			subIdx = i
			break
		}
	}
	if subIdx < 0 {
		return fmt.Errorf("subscription not found: %s", id)
	}
	if nodeName != "" && !hasNodeByName(cfg.Subscriptions[subIdx].Nodes, nodeName) {
		return fmt.Errorf("node not found in subscription: %s", nodeName)
	}

	updated := false
	selectedChanged := false
	if err := s.config.Update(func(cfg *XRayConfig) {
		for i := range cfg.Subscriptions {
			if cfg.Subscriptions[i].ID == id {
				selectedChanged = strings.TrimSpace(cfg.Subscriptions[i].SelectedNode) != nodeName
				cfg.Subscriptions[i].SelectedNode = nodeName
				updated = true
				break
			}
		}
	}); err != nil {
		return err
	}
	if !updated {
		return fmt.Errorf("subscription not found: %s", id)
	}
	if !selectedChanged {
		return nil
	}

	afterCfg := s.config.Get()
	runtimeChanged := runtimeChangedBetweenConfigs(cfg, afterCfg)

	s.mu.RLock()
	wasRunning := s.running
	s.mu.RUnlock()
	if wasRunning && runtimeChanged {
		if err := s.Stop(); err != nil {
			log.Printf("[xray] update selected node stop error: %v", err)
		}
		if err := s.Start(); err != nil {
			return fmt.Errorf("restart with updated selected node: %w", err)
		}
	}

	return nil
}

// RefreshSingleSubscription refreshes a single subscription by ID.
func (s *XRayService) RefreshSingleSubscription(ctx context.Context, id string) (*RefreshResult, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return nil, fmt.Errorf("subscription id is required")
	}

	cfg := s.config.Get()
	var targetSub *Subscription
	for i := range cfg.Subscriptions {
		if cfg.Subscriptions[i].ID == id {
			targetSub = &cfg.Subscriptions[i]
			break
		}
	}
	if targetSub == nil {
		return nil, fmt.Errorf("subscription not found: %s", id)
	}

	result := &RefreshResult{}
	client := &http.Client{Timeout: 30 * time.Second}

	if strings.TrimSpace(targetSub.URL) == "" {
		result.TotalNodes = len(targetSub.Nodes)
		return result, nil
	}

	req, err := http.NewRequestWithContext(ctx, "GET", targetSub.URL, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("User-Agent", subscriptionUserAgent)

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch subscription: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("http %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxSubscriptionBytes))
	if err != nil {
		return nil, fmt.Errorf("read body: %w", err)
	}

	nodes, warnings := DetectAndParse(string(body), targetSub.Format, targetSub.ID)
	for _, w := range warnings {
		log.Printf("[xray] %s: %s", targetSub.Name, w)
	}

	// Update timestamp and nodes
	if err := s.config.Update(func(c *XRayConfig) {
		for i := range c.Subscriptions {
			if c.Subscriptions[i].ID == id {
				c.Subscriptions[i].LastUpdated = time.Now().Format(time.RFC3339)
				c.Subscriptions[i].Nodes = nodes
				break
			}
		}
	}); err != nil {
		result.Errors = append(result.Errors, fmt.Sprintf("update config: %v", err))
	}

	// Replace nodes from this subscription in memory
	s.mu.Lock()
	var newNodes []ProxyNode
	for _, n := range s.nodes {
		if n.SourceID != id {
			newNodes = append(newNodes, n)
		}
	}
	newNodes = append(newNodes, nodes...)
	s.nodes = newNodes
	s.mu.Unlock()

	result.TotalNodes = len(nodes)
	return result, nil
}

// ActivateSubscription activates a subscription and restarts xray if it was running.
func (s *XRayService) ActivateSubscription(id string) error {
	id = strings.TrimSpace(id)
	if id == "" {
		return fmt.Errorf("subscription id is required")
	}

	cfg := s.config.Get()
	var targetSub *Subscription
	for i := range cfg.Subscriptions {
		if cfg.Subscriptions[i].ID == id {
			targetSub = &cfg.Subscriptions[i]
			break
		}
	}
	if targetSub == nil {
		return fmt.Errorf("subscription not found: %s", id)
	}

	selected := strings.TrimSpace(targetSub.SelectedNode)
	if selected == "" {
		if len(targetSub.Nodes) == 0 {
			return fmt.Errorf("no node selected and no nodes available for subscription: %s", id)
		}
		selected = targetSub.Nodes[0].Name
	}
	if !hasNodeByName(targetSub.Nodes, selected) {
		return fmt.Errorf("selected node not found in subscription: %s", selected)
	}
	if err := s.config.Update(func(cfg *XRayConfig) {
		for i := range cfg.Subscriptions {
			cfg.Subscriptions[i].Active = (cfg.Subscriptions[i].ID == id)
			if cfg.Subscriptions[i].ID == id {
				cfg.Subscriptions[i].SelectedNode = selected
			}
		}
	}); err != nil {
		return err
	}

	// Only restart if xray was already running
	s.mu.RLock()
	wasRunning := s.running
	s.mu.RUnlock()

	if wasRunning {
		if err := s.Stop(); err != nil {
			log.Printf("[xray] activate subscription stop error: %v", err)
		}
		return s.Start()
	}

	log.Printf("[xray] activated subscription: %s (not running, no restart)", id)
	return nil
}

// GetNodeConfig returns a JSON representation of the node configuration.
func (s *XRayService) GetNodeConfig(nodeName string) (string, error) {
	node := s.findNodeSafe(nodeName)
	if node == nil {
		return "", fmt.Errorf("node not found: %s", nodeName)
	}

	data, err := json.MarshalIndent(node, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshal node config: %w", err)
	}

	return string(data), nil
}

// ParseNodesForSubscription parses node content for preview without persisting.
func (s *XRayService) ParseNodesForSubscription(id, content string) ([]ProxyNode, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return nil, fmt.Errorf("subscription id is required")
	}
	content = strings.TrimSpace(content)
	if content == "" {
		return nil, fmt.Errorf("content is empty")
	}

	nodes, warnings := DetectAndParse(content, "auto", id)
	for _, w := range warnings {
		log.Printf("[xray] parse nodes preview: %s", w)
	}
	if len(nodes) == 0 {
		return nil, fmt.Errorf("no valid nodes parsed from input")
	}

	return normalizeSubscriptionNodes(nodes, id), nil
}

// ReplaceSubscriptionNodes replaces all nodes in a subscription atomically.
func (s *XRayService) ReplaceSubscriptionNodes(id string, nodes []ProxyNode, selectedNode string) error {
	id = strings.TrimSpace(id)
	if id == "" {
		return fmt.Errorf("subscription id is required")
	}
	selectedNode = strings.TrimSpace(selectedNode)
	normalized := normalizeSubscriptionNodes(nodes, id)
	if selectedNode != "" && !hasNodeByName(normalized, selectedNode) {
		return fmt.Errorf("selected node not found: %s", selectedNode)
	}

	subFound := false
	targetSubActive := false
	targetSelectedChanged := false
	if err := s.config.Update(func(cfg *XRayConfig) {
		for i := range cfg.Subscriptions {
			if cfg.Subscriptions[i].ID != id {
				continue
			}
			subFound = true
			targetSubActive = cfg.Subscriptions[i].Active
			prevSelected := strings.TrimSpace(cfg.Subscriptions[i].SelectedNode)
			cfg.Subscriptions[i].Nodes = append([]ProxyNode(nil), normalized...)
			if len(normalized) == 0 {
				cfg.Subscriptions[i].SelectedNode = ""
				targetSelectedChanged = prevSelected != ""
				return
			}
			if selectedNode != "" {
				cfg.Subscriptions[i].SelectedNode = selectedNode
				targetSelectedChanged = prevSelected != strings.TrimSpace(cfg.Subscriptions[i].SelectedNode)
				return
			}
			current := strings.TrimSpace(cfg.Subscriptions[i].SelectedNode)
			if current == "" || !hasNodeByName(normalized, current) {
				cfg.Subscriptions[i].SelectedNode = normalized[0].Name
			}
			targetSelectedChanged = prevSelected != strings.TrimSpace(cfg.Subscriptions[i].SelectedNode)
			return
		}
	}); err != nil {
		return err
	}
	if !subFound {
		return fmt.Errorf("subscription not found: %s", id)
	}

	s.mu.Lock()
	remaining := make([]ProxyNode, 0, len(s.nodes))
	for _, n := range s.nodes {
		if n.SourceID != id {
			remaining = append(remaining, n)
		}
	}
	remaining = append(remaining, normalized...)
	s.nodes = remaining
	s.mu.Unlock()

	s.mu.RLock()
	wasRunning := s.running
	s.mu.RUnlock()
	if wasRunning && targetSubActive && targetSelectedChanged {
		if err := s.Stop(); err != nil {
			log.Printf("[xray] replace nodes stop error: %v", err)
		}
		if err := s.Start(); err != nil {
			return fmt.Errorf("restart with updated selected node: %w", err)
		}
	}

	return nil
}

// AddNodesToSubscription parses content (URI/Clash YAML/JSON) and appends parsed nodes to the subscription.
func (s *XRayService) AddNodesToSubscription(id, content string) (int, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return 0, fmt.Errorf("subscription id is required")
	}
	content = strings.TrimSpace(content)
	if content == "" {
		return 0, fmt.Errorf("content is empty")
	}

	nodes, warnings := DetectAndParse(content, "auto", id)
	for _, w := range warnings {
		log.Printf("[xray] add nodes: %s", w)
	}
	if len(nodes) == 0 {
		return 0, fmt.Errorf("no valid nodes parsed from input")
	}

	added := 0
	if err := s.config.Update(func(cfg *XRayConfig) {
		for i := range cfg.Subscriptions {
			if cfg.Subscriptions[i].ID == id {
				// Deduplicate: rename nodes that collide with existing names
				existing := cfg.Subscriptions[i].Nodes
				for k := range nodes {
					nodes[k].Name = uniqueNodeName(nodes[k].Name, existing)
					existing = append(existing, nodes[k])
				}
				cfg.Subscriptions[i].Nodes = existing
				added = len(nodes)
				return
			}
		}
	}); err != nil {
		return 0, err
	}
	if added == 0 {
		return 0, fmt.Errorf("subscription not found: %s", id)
	}

	s.mu.Lock()
	s.nodes = append(s.nodes, nodes...)
	s.mu.Unlock()

	return added, nil
}

// RemoveNodeFromSubscription removes a node by name from the specified subscription.
func (s *XRayService) RemoveNodeFromSubscription(id, nodeName string) error {
	id = strings.TrimSpace(id)
	if id == "" {
		return fmt.Errorf("subscription id is required")
	}
	nodeName = strings.TrimSpace(nodeName)
	if nodeName == "" {
		return fmt.Errorf("node name is required")
	}

	removed := false
	subFound := false
	if err := s.config.Update(func(cfg *XRayConfig) {
		for i := range cfg.Subscriptions {
			if cfg.Subscriptions[i].ID != id {
				continue
			}
			subFound = true
			for j, n := range cfg.Subscriptions[i].Nodes {
				if n.Name == nodeName {
					cfg.Subscriptions[i].Nodes = append(cfg.Subscriptions[i].Nodes[:j], cfg.Subscriptions[i].Nodes[j+1:]...)
					removed = true
					return
				}
			}
			return
		}
	}); err != nil {
		return err
	}
	if !subFound {
		return fmt.Errorf("subscription not found: %s", id)
	}
	if !removed {
		return fmt.Errorf("node not found: %s", nodeName)
	}

	s.mu.Lock()
	for i := range s.nodes {
		if s.nodes[i].SourceID == id && s.nodes[i].Name == nodeName {
			s.nodes = append(s.nodes[:i], s.nodes[i+1:]...)
			break
		}
	}
	s.mu.Unlock()

	return nil
}

func normalizeSubscriptionNodes(nodes []ProxyNode, sourceID string) []ProxyNode {
	existing := make([]ProxyNode, 0, len(nodes))
	out := make([]ProxyNode, 0, len(nodes))
	for i := range nodes {
		node := nodes[i]
		node.SourceID = sourceID
		node.Name = strings.TrimSpace(node.Name)
		if node.Name == "" {
			node.Name = "node"
		}
		node.Name = uniqueNodeName(node.Name, existing)
		existing = append(existing, node)
		out = append(out, node)
	}
	return out
}
