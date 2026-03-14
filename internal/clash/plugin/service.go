package clashplugin

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"gopkg.in/yaml.v3"
)

const (
	maxSubscriptionBytes  = 8 << 20 // 8MiB
	subscriptionUserAgent = "mozilla/5.0 (iPhone; CPU iPhone OS 15_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/15.0 Mobile/15E148 Safari/604.1"

	runtimeGroupSelector = "selector"
	runtimeGroupChain    = "chain"
	runtimeGroupExit     = "chain-exit"
	runtimeGroupMiddle   = "chain-middle"
	runtimeGroupAllNodes = "all-nodes"

	runtimeGroupHub        = "Hub"
	runtimeGroupAutoSelect = "auto-select"
	runtimeGroupFailover   = "failover"

	runtimeGroupTestURL          = "http://www.gstatic.com/generate_204"
	runtimeAutoSelectIntervalSec = 86400
	runtimeFailoverIntervalSec   = 7200
)

// ClashService manages the mihomo instance lifecycle and node state.
type ClashService struct {
	mu       sync.RWMutex
	config   *configStore
	nodes    []ProxyNode
	running  bool
	instance io.Closer
	dataDir  string

	speedTestCtrlMu     sync.Mutex
	speedTestRootCtx    context.Context
	speedTestRootCancel context.CancelFunc
}

// SyncSelectedNodesFromController pulls current selector choices from the running external controller
// and writes them back to the subscription selectedNode fields in clash-config.json.
func (s *ClashService) SyncSelectedNodesFromController(ctx context.Context) (int, error) {
	s.mu.RLock()
	running := s.running
	s.mu.RUnlock()
	if !running {
		return 0, nil
	}

	cfg := s.config.Get()
	controllerCfg, err := resolveRuntimeControllerConfig(cfg)
	if err != nil {
		return 0, err
	}

	_, plan, ready, err := buildRuntimeForConfig(cfg)
	if err != nil {
		return 0, err
	}
	if !ready || plan == nil {
		return 0, nil
	}

	refByRuntimeName := runtimeNodeRefByRuntimeProxyName(cfg)

	updates := make(map[string]string)
	applyNow := func(runtimeName string, onlyIfNotSet bool) {
		runtimeName = strings.TrimSpace(runtimeName)
		if runtimeName == "" {
			return
		}
		ref, ok := refByRuntimeName[runtimeName]
		if !ok {
			return
		}
		if onlyIfNotSet {
			if _, exists := updates[ref.SubscriptionID]; exists {
				return
			}
		}
		updates[ref.SubscriptionID] = ref.NodeName
	}

	// 1) Traffic group selection drives the active subscription.
	if now, err := getRuntimeProxyNow(ctx, controllerCfg, plan.trafficGroup); err == nil {
		applyNow(now, false)
	}
	// 2) Chain selections drive their referenced subscriptions.
	if now, err := getRuntimeProxyNow(ctx, controllerCfg, runtimeGroupExit); err == nil {
		applyNow(now, false)
	}
	if now, err := getRuntimeProxyNow(ctx, controllerCfg, runtimeGroupMiddle); err == nil {
		applyNow(now, false)
	}
	if len(updates) == 0 {
		return 0, nil
	}

	updated := 0
	if err := s.config.Update(func(c *ClashConfig) {
		for i := range c.Subscriptions {
			sub := &c.Subscriptions[i]
			next, ok := updates[sub.ID]
			if !ok {
				continue
			}
			if next != "" && hasNodeByName(sub.Nodes, next) {
				sub.SelectedNode = next
				updated++
			}
		}
		clampChainReferences(c)
	}); err != nil {
		return 0, err
	}
	return updated, nil
}

func (s *ClashService) ReloadConfigFromDisk() error {
	before := s.config.Get()

	s.mu.RLock()
	running := s.running
	s.mu.RUnlock()
	if running {
		ctx, cancel := context.WithTimeout(context.Background(), 800*time.Millisecond)
		_, _ = s.SyncSelectedNodesFromController(ctx)
		cancel()
	}

	if err := s.config.Load(); err != nil {
		return err
	}
	s.loadCachedNodes()

	after := s.config.Get()
	s.reconcileRuntimeAfterConfigChangeBestEffort(before, after, "", "reload config from disk")
	return nil
}

type runtimePlan struct {
	trafficGroup     string
	trafficSelection string
	exitSelection    string
	middleSelection  string
}

func defaultHubProxyGroups(allProxyNames []string) []any {
	if len(allProxyNames) == 0 {
		return nil
	}

	hubProxies := make([]string, 0, len(allProxyNames)+2)
	hubProxies = append(hubProxies, runtimeGroupAutoSelect, runtimeGroupFailover)
	hubProxies = append(hubProxies, allProxyNames...)

	return []any{
		map[string]any{
			"name":    runtimeGroupHub,
			"type":    "select",
			"proxies": hubProxies,
		},
		map[string]any{
			"name":     runtimeGroupAutoSelect,
			"type":     "url-test",
			"url":      runtimeGroupTestURL,
			"interval": runtimeAutoSelectIntervalSec,
			"proxies":  allProxyNames,
		},
		map[string]any{
			"name":     runtimeGroupFailover,
			"type":     "fallback",
			"url":      runtimeGroupTestURL,
			"interval": runtimeFailoverIntervalSec,
			"proxies":  allProxyNames,
		},
	}
}

func NewClashService(cfgPath string) (*ClashService, error) {
	cs := newConfigStore(cfgPath)
	if err := cs.Load(); err != nil {
		log.Printf("[clash] config load warning: %v (using defaults)", err)
	}

	svc := &ClashService{
		config:  cs,
		dataDir: filepath.Dir(cfgPath),
	}
	svc.speedTestRootCtx, svc.speedTestRootCancel = context.WithCancel(context.Background())
	svc.loadCachedNodes()
	return svc, nil
}

func (s *ClashService) Start() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.running {
		return fmt.Errorf("clash already running")
	}

	cfg := s.config.Get()
	runtimeYAML, plan, ready, err := buildRuntimeForConfig(cfg)
	if err != nil {
		return err
	}
	if !ready {
		return fmt.Errorf("no active subscription with valid nodes")
	}

	if err := s.startWithRuntime(runtimeYAML); err != nil {
		return err
	}
	if err := applyRuntimeSelectionsWithRetry(cfg, plan, 20, 50*time.Millisecond); err != nil {
		log.Printf("[clash] apply runtime selections warning: %v", err)
	}
	return nil
}

func (s *ClashService) Stop() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.stopLocked()
}

func (s *ClashService) stopLocked() error {
	if !s.running || s.instance == nil {
		s.running = false
		return nil
	}
	if err := s.instance.Close(); err != nil {
		s.instance = nil
		s.running = false
		return fmt.Errorf("clash stop: %w", err)
	}
	s.instance = nil
	s.running = false
	log.Println("[clash] stopped")
	return nil
}

func (s *ClashService) Reload() error {
	if err := s.config.Load(); err != nil {
		return err
	}
	s.loadCachedNodes()

	s.mu.RLock()
	wasRunning := s.running
	s.mu.RUnlock()
	if !wasRunning {
		return nil
	}

	if err := s.Stop(); err != nil {
		log.Printf("[clash] reload stop error: %v", err)
	}
	if err := s.Start(); err != nil {
		log.Printf("[clash] reload start error: %v", err)
		return err
	}
	return nil
}

// SelectNode updates selected node on active subscription and keeps chain entry aligned.
func (s *ClashService) SelectNode(nodeName string) error {
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
	if resolved, ok := resolveSubscriptionNodeName(cfg, activeSub.ID, nodeName); ok && resolved != "" {
		nodeName = resolved
	}
	if !hasNodeByName(activeSub.Nodes, nodeName) {
		return fmt.Errorf("node not found in active subscription: %s", nodeName)
	}

	beforeCfg := cfg
	if err := s.config.Update(func(c *ClashConfig) {
		for i := range c.Subscriptions {
			if c.Subscriptions[i].ID != activeSub.ID {
				continue
			}
			c.Subscriptions[i].SelectedNode = nodeName
			break
		}
		if strings.TrimSpace(c.Chain.Entry.SubscriptionID) == activeSub.ID {
			c.Chain.Entry.NodeName = nodeName
		}
	}); err != nil {
		return err
	}

	afterCfg := s.config.Get()
	if s.shouldRestartRuntime(beforeCfg, afterCfg) {
		if err := s.restartRuntime(); err != nil {
			return err
		}
		return nil
	}
	return s.syncRuntimeSelections(afterCfg)
}

func (s *ClashService) GetStatus() StatusResponse {
	s.mu.RLock()
	defer s.mu.RUnlock()

	cfg := s.config.Get()
	resp := StatusResponse{
		Running:      s.running,
		SelectedNode: activeSelectedNodeName(cfg),
		NodeCount:    len(s.nodes),
	}
	if s.running {
		resp.SocksAddr = fmt.Sprintf("%s:%d", cfg.SocksListen, cfg.SocksPort)
	}
	return resp
}

func (s *ClashService) GetNodes() []ProxyNode {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]ProxyNode, len(s.nodes))
	for i := range s.nodes {
		out[i] = cloneNode(s.nodes[i])
	}
	return out
}

func (s *ClashService) RefreshSubscriptions(ctx context.Context) (*RefreshResult, error) {
	cfg := s.config.Get()
	result := &RefreshResult{}
	var allNodes []ProxyNode

	client := &http.Client{Timeout: 30 * time.Second}

	for i := range cfg.Subscriptions {
		sub := &cfg.Subscriptions[i]
		if !sub.Enabled || strings.TrimSpace(sub.URL) == "" {
			allNodes = append(allNodes, sub.Nodes...)
			continue
		}

		req, err := http.NewRequestWithContext(ctx, http.MethodGet, sub.URL, nil)
		if err != nil {
			result.Errors = append(result.Errors, fmt.Sprintf("%s: %v", sub.Name, err))
			allNodes = append(allNodes, sub.Nodes...)
			continue
		}
		req.Header.Set("User-Agent", subscriptionUserAgent)

		resp, err := client.Do(req)
		if err != nil {
			result.Errors = append(result.Errors, fmt.Sprintf("%s: %v", sub.Name, err))
			allNodes = append(allNodes, sub.Nodes...)
			continue
		}
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			resp.Body.Close()
			result.Errors = append(result.Errors, fmt.Sprintf("%s: http %d", sub.Name, resp.StatusCode))
			allNodes = append(allNodes, sub.Nodes...)
			continue
		}
		body, err := io.ReadAll(io.LimitReader(resp.Body, maxSubscriptionBytes))
		resp.Body.Close()
		if err != nil {
			result.Errors = append(result.Errors, fmt.Sprintf("%s: read body: %v", sub.Name, err))
			allNodes = append(allNodes, sub.Nodes...)
			continue
		}

		nodes, warnings := DetectAndParse(string(body), sub.Format, sub.ID)
		for _, w := range warnings {
			log.Printf("[clash] %s: %s", sub.Name, w)
		}
		sub.Nodes = normalizeSubscriptionNodes(nodes, sub.ID)
		sub.LastUpdated = time.Now().Format(time.RFC3339)
		if sub.SelectedNode != "" && !hasNodeByName(sub.Nodes, sub.SelectedNode) {
			sub.SelectedNode = ""
		}
		if sub.SelectedNode == "" && len(sub.Nodes) > 0 {
			sub.SelectedNode = nodeName(sub.Nodes[0])
		}

		allNodes = append(allNodes, sub.Nodes...)
	}

	if err := s.config.Update(func(c *ClashConfig) {
		for i := range c.Subscriptions {
			for _, updatedSub := range cfg.Subscriptions {
				if c.Subscriptions[i].ID != updatedSub.ID {
					continue
				}
				c.Subscriptions[i].Nodes = updatedSub.Nodes
				c.Subscriptions[i].LastUpdated = updatedSub.LastUpdated
				c.Subscriptions[i].SelectedNode = updatedSub.SelectedNode
				break
			}
		}
		clampChainReferences(c)
	}); err != nil {
		result.Errors = append(result.Errors, fmt.Sprintf("update config: %v", err))
	}

	s.mu.Lock()
	s.nodes = allNodes
	s.mu.Unlock()

	result.TotalNodes = len(allNodes)
	afterCfg := s.config.Get()
	if s.shouldRestartRuntime(cfg, afterCfg) {
		if err := s.restartRuntime(); err != nil {
			result.Errors = append(result.Errors, fmt.Sprintf("restart runtime: %v", err))
		}
	} else if err := s.syncRuntimeSelections(afterCfg); err != nil {
		result.Errors = append(result.Errors, fmt.Sprintf("sync runtime selection: %v", err))
	}

	return result, nil
}

func (s *ClashService) AddSubscription(name, rawURL string) error {
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

	return s.config.Update(func(cfg *ClashConfig) {
		cfg.Subscriptions = append(cfg.Subscriptions, Subscription{
			ID:      id,
			Name:    name,
			URL:     rawURL,
			Enabled: true,
			Format:  "auto",
			Nodes:   []ProxyNode{},
		})
	})
}

func (s *ClashService) RemoveSubscription(id string) error {
	id = strings.TrimSpace(id)
	if id == "" {
		return fmt.Errorf("subscription id is required")
	}

	beforeCfg := s.config.Get()
	removed := false
	removedActive := false

	if err := s.config.Update(func(cfg *ClashConfig) {
		for i, sub := range cfg.Subscriptions {
			if sub.ID != id {
				continue
			}
			removed = true
			removedActive = sub.Active
			cfg.Subscriptions = append(cfg.Subscriptions[:i], cfg.Subscriptions[i+1:]...)
			break
		}
		if cfg.Chain.Entry.SubscriptionID == id {
			cfg.Chain.Entry = NodeRef{}
		}
		if cfg.Chain.Exit.SubscriptionID == id {
			cfg.Chain.Exit = NodeRef{}
		}
		if cfg.Chain.Middle != nil && cfg.Chain.Middle.SubscriptionID == id {
			cfg.Chain.Middle = nil
		}
	}); err != nil {
		return err
	}
	if !removed {
		return fmt.Errorf("subscription not found: %s", id)
	}

	s.mu.Lock()
	newNodes := make([]ProxyNode, 0, len(s.nodes))
	for _, n := range s.nodes {
		if nodeSourceID(n) != id {
			newNodes = append(newNodes, n)
		}
	}
	s.nodes = newNodes
	s.mu.Unlock()

	afterCfg := s.config.Get()
	if removedActive || s.shouldRestartRuntime(beforeCfg, afterCfg) {
		if err := s.restartRuntime(); err != nil {
			return err
		}
		return nil
	}
	return s.syncRuntimeSelections(afterCfg)
}

func (s *ClashService) UpdateSubscription(id, name, rawURL string) error {
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

	updated := false
	if err := s.config.Update(func(cfg *ClashConfig) {
		for i := range cfg.Subscriptions {
			if cfg.Subscriptions[i].ID != id {
				continue
			}
			cfg.Subscriptions[i].Name = name
			cfg.Subscriptions[i].URL = rawURL
			updated = true
			return
		}
	}); err != nil {
		return err
	}
	if !updated {
		return fmt.Errorf("subscription not found: %s", id)
	}
	return nil
}

func (s *ClashService) UpdateNodeLatency(targetName string, latency int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.nodes {
		if nodeName(s.nodes[i]) == targetName {
			setNodeInt(s.nodes[i], nodeFieldLatency, latency)
			return
		}
	}
}

func (s *ClashService) ToggleSubscription(id string) error {
	id = strings.TrimSpace(id)
	if id == "" {
		return fmt.Errorf("subscription id is required")
	}

	beforeCfg := s.config.Get()
	updated := false
	if err := s.config.Update(func(cfg *ClashConfig) {
		for i := range cfg.Subscriptions {
			if cfg.Subscriptions[i].ID != id {
				continue
			}
			cfg.Subscriptions[i].Enabled = !cfg.Subscriptions[i].Enabled
			updated = true
			break
		}
		clampChainReferences(cfg)
	}); err != nil {
		return err
	}
	if !updated {
		return fmt.Errorf("subscription not found: %s", id)
	}

	afterCfg := s.config.Get()
	if s.shouldRestartRuntime(beforeCfg, afterCfg) {
		return s.restartRuntime()
	}
	return s.syncRuntimeSelections(afterCfg)
}

// SetActiveSubscription marks one subscription active and updates chain.entry.
func (s *ClashService) SetActiveSubscription(id string) error {
	id = strings.TrimSpace(id)
	if id == "" {
		return fmt.Errorf("subscription id is required")
	}

	beforeCfg := s.config.Get()
	updated := false
	if err := s.config.Update(func(cfg *ClashConfig) {
		for i := range cfg.Subscriptions {
			cfg.Subscriptions[i].Active = cfg.Subscriptions[i].ID == id
			if !cfg.Subscriptions[i].Active {
				continue
			}
			if cfg.Subscriptions[i].SelectedNode == "" && len(cfg.Subscriptions[i].Nodes) > 0 {
				cfg.Subscriptions[i].SelectedNode = nodeName(cfg.Subscriptions[i].Nodes[0])
			}
			if cfg.Subscriptions[i].SelectedNode != "" {
				cfg.Chain.Entry = NodeRef{SubscriptionID: id, NodeName: cfg.Subscriptions[i].SelectedNode}
			}
			updated = true
		}
		clampChainReferences(cfg)
	}); err != nil {
		return err
	}
	if !updated {
		return fmt.Errorf("subscription not found: %s", id)
	}

	afterCfg := s.config.Get()
	if s.shouldRestartRuntime(beforeCfg, afterCfg) {
		return s.restartRuntime()
	}
	return s.syncRuntimeSelections(afterCfg)
}

// SetDialerProxySubscription updates chain.exit to selected node in the target subscription.
func (s *ClashService) SetDialerProxySubscription(id string) error {
	id = strings.TrimSpace(id)
	beforeCfg := s.config.Get()

	if err := s.config.Update(func(cfg *ClashConfig) {
		if id == "" {
			cfg.Chain.Exit = NodeRef{}
			return
		}
		for i := range cfg.Subscriptions {
			if cfg.Subscriptions[i].ID != id {
				continue
			}
			if cfg.Subscriptions[i].SelectedNode == "" && len(cfg.Subscriptions[i].Nodes) > 0 {
				cfg.Subscriptions[i].SelectedNode = nodeName(cfg.Subscriptions[i].Nodes[0])
			}
			cfg.Chain.Exit = NodeRef{SubscriptionID: id, NodeName: cfg.Subscriptions[i].SelectedNode}
			return
		}
	}); err != nil {
		return err
	}

	afterCfg := s.config.Get()
	if id != "" {
		found := false
		for i := range afterCfg.Subscriptions {
			if afterCfg.Subscriptions[i].ID == id {
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("subscription not found: %s", id)
		}
	}

	if s.shouldRestartRuntime(beforeCfg, afterCfg) {
		return s.restartRuntime()
	}
	return s.syncRuntimeSelections(afterCfg)
}

func (s *ClashService) UpdateSubscriptionSelectedNode(id, nodeName string) error {
	id = strings.TrimSpace(id)
	nodeName = strings.TrimSpace(nodeName)
	if id == "" {
		return fmt.Errorf("subscription id is required")
	}

	beforeCfg := s.config.Get()
	updated := false
	if err := s.config.Update(func(cfg *ClashConfig) {
		for i := range cfg.Subscriptions {
			if cfg.Subscriptions[i].ID != id {
				continue
			}
			resolvedNodeName := nodeName
			if resolvedNodeName != "" && !hasNodeByName(cfg.Subscriptions[i].Nodes, resolvedNodeName) {
				if resolved, ok := resolveSubscriptionNodeName(cfg, id, resolvedNodeName); ok && resolved != "" {
					resolvedNodeName = resolved
				} else {
					return
				}
			}
			oldNode := strings.TrimSpace(cfg.Subscriptions[i].SelectedNode)
			cfg.Subscriptions[i].SelectedNode = resolvedNodeName
			if cfg.Chain.Entry.SubscriptionID == id && cfg.Chain.Entry.NodeName == oldNode {
				cfg.Chain.Entry.NodeName = resolvedNodeName
			}
			if cfg.Chain.Exit.SubscriptionID == id && cfg.Chain.Exit.NodeName == oldNode {
				cfg.Chain.Exit.NodeName = resolvedNodeName
			}
			if cfg.Chain.Middle != nil && cfg.Chain.Middle.SubscriptionID == id && cfg.Chain.Middle.NodeName == oldNode {
				cfg.Chain.Middle.NodeName = resolvedNodeName
			}
			updated = true
			return
		}
	}); err != nil {
		return err
	}
	if !updated {
		return fmt.Errorf("subscription not found or node invalid")
	}

	afterCfg := s.config.Get()
	s.reconcileRuntimeAfterConfigChangeBestEffort(beforeCfg, afterCfg, id, "update subscription selected node")
	return nil
}

func (s *ClashService) RefreshSingleSubscription(ctx context.Context, id string) (*RefreshResult, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return nil, fmt.Errorf("subscription id is required")
	}

	cfg := s.config.Get()
	var target *Subscription
	for i := range cfg.Subscriptions {
		if cfg.Subscriptions[i].ID == id {
			target = &cfg.Subscriptions[i]
			break
		}
	}
	if target == nil {
		return nil, fmt.Errorf("subscription not found: %s", id)
	}

	result := &RefreshResult{}
	if strings.TrimSpace(target.URL) == "" {
		result.TotalNodes = len(target.Nodes)
		return result, nil
	}

	client := &http.Client{Timeout: 30 * time.Second}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target.URL, nil)
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

	nodes, warnings := DetectAndParse(string(body), target.Format, target.ID)
	for _, w := range warnings {
		log.Printf("[clash] %s: %s", target.Name, w)
	}
	nodes = normalizeSubscriptionNodes(nodes, id)

	beforeCfg := s.config.Get()
	if err := s.config.Update(func(c *ClashConfig) {
		for i := range c.Subscriptions {
			if c.Subscriptions[i].ID != id {
				continue
			}
			c.Subscriptions[i].Nodes = nodes
			c.Subscriptions[i].LastUpdated = time.Now().Format(time.RFC3339)
			if c.Subscriptions[i].SelectedNode != "" && !hasNodeByName(c.Subscriptions[i].Nodes, c.Subscriptions[i].SelectedNode) {
				c.Subscriptions[i].SelectedNode = ""
			}
			if c.Subscriptions[i].SelectedNode == "" && len(c.Subscriptions[i].Nodes) > 0 {
				c.Subscriptions[i].SelectedNode = nodeName(c.Subscriptions[i].Nodes[0])
			}
			break
		}
		clampChainReferences(c)
	}); err != nil {
		result.Errors = append(result.Errors, fmt.Sprintf("update config: %v", err))
	}

	s.mu.Lock()
	newNodes := make([]ProxyNode, 0, len(s.nodes))
	for _, n := range s.nodes {
		if nodeSourceID(n) != id {
			newNodes = append(newNodes, n)
		}
	}
	newNodes = append(newNodes, nodes...)
	s.nodes = newNodes
	s.mu.Unlock()

	result.TotalNodes = len(nodes)
	afterCfg := s.config.Get()
	if !subscriptionParticipatesInRuntime(beforeCfg, id) && !subscriptionParticipatesInRuntime(afterCfg, id) {
		return result, nil
	}
	if s.shouldRestartRuntime(beforeCfg, afterCfg) {
		if err := s.restartRuntime(); err != nil {
			result.Errors = append(result.Errors, fmt.Sprintf("restart runtime: %v", err))
		}
	} else if err := s.syncRuntimeSelections(afterCfg); err != nil {
		result.Errors = append(result.Errors, fmt.Sprintf("sync runtime selection: %v", err))
	}
	return result, nil
}

func (s *ClashService) ActivateSubscription(id string) error {
	if err := s.SetActiveSubscription(id); err != nil {
		return err
	}

	s.mu.RLock()
	wasRunning := s.running
	s.mu.RUnlock()
	if wasRunning {
		return nil
	}
	return nil
}

func (s *ClashService) GetNodeConfig(nodeName string) (string, error) {
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

func (s *ClashService) ParseNodesForSubscription(id, content string) ([]ProxyNode, error) {
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
		log.Printf("[clash] parse nodes preview: %s", w)
	}
	if len(nodes) == 0 {
		return nil, fmt.Errorf("no valid nodes parsed from input")
	}
	return normalizeSubscriptionNodes(nodes, id), nil
}

func (s *ClashService) ReplaceSubscriptionNodes(id string, nodes []ProxyNode, selectedNode string) error {
	id = strings.TrimSpace(id)
	selectedNode = strings.TrimSpace(selectedNode)
	if id == "" {
		return fmt.Errorf("subscription id is required")
	}

	normalized := normalizeSubscriptionNodes(nodes, id)
	beforeCfg := s.config.Get()
	if selectedNode != "" && !hasNodeByName(normalized, selectedNode) {
		if resolved, ok := resolveSubscriptionNodeName(beforeCfg, id, selectedNode); ok && resolved != "" && hasNodeByName(normalized, resolved) {
			selectedNode = resolved
		} else {
			return fmt.Errorf("selected node not found: %s", selectedNode)
		}
	}
	subFound := false
	if err := s.config.Update(func(cfg *ClashConfig) {
		for i := range cfg.Subscriptions {
			if cfg.Subscriptions[i].ID != id {
				continue
			}
			subFound = true
			oldSelected := strings.TrimSpace(cfg.Subscriptions[i].SelectedNode)
			cfg.Subscriptions[i].Nodes = append([]ProxyNode(nil), normalized...)
			switch {
			case len(normalized) == 0:
				cfg.Subscriptions[i].SelectedNode = ""
			case selectedNode != "":
				cfg.Subscriptions[i].SelectedNode = selectedNode
			case oldSelected != "" && hasNodeByName(normalized, oldSelected):
				cfg.Subscriptions[i].SelectedNode = oldSelected
			default:
				cfg.Subscriptions[i].SelectedNode = nodeName(normalized[0])
			}
			break
		}
		clampChainReferences(cfg)
	}); err != nil {
		return err
	}
	if !subFound {
		return fmt.Errorf("subscription not found: %s", id)
	}

	s.mu.Lock()
	remaining := make([]ProxyNode, 0, len(s.nodes))
	for _, n := range s.nodes {
		if nodeSourceID(n) != id {
			remaining = append(remaining, n)
		}
	}
	remaining = append(remaining, normalized...)
	s.nodes = remaining
	s.mu.Unlock()

	afterCfg := s.config.Get()
	s.reconcileRuntimeAfterConfigChangeBestEffort(beforeCfg, afterCfg, id, "replace subscription nodes")
	return nil
}

func (s *ClashService) AddNodesToSubscription(id, content string) (int, error) {
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
		log.Printf("[clash] add nodes: %s", w)
	}
	if len(nodes) == 0 {
		return 0, fmt.Errorf("no valid nodes parsed from input")
	}

	beforeCfg := s.config.Get()
	added := 0
	if err := s.config.Update(func(cfg *ClashConfig) {
		for i := range cfg.Subscriptions {
			if cfg.Subscriptions[i].ID != id {
				continue
			}
			existing := cfg.Subscriptions[i].Nodes
			for k := range nodes {
				nodes[k] = normalizeProxyNode(nodes[k], id)
				setNodeString(nodes[k], nodeFieldName, uniqueNodeName(nodeName(nodes[k]), existing))
				existing = append(existing, nodes[k])
			}
			cfg.Subscriptions[i].Nodes = existing
			if cfg.Subscriptions[i].SelectedNode == "" && len(cfg.Subscriptions[i].Nodes) > 0 {
				cfg.Subscriptions[i].SelectedNode = nodeName(cfg.Subscriptions[i].Nodes[0])
			}
			added = len(nodes)
			break
		}
	}); err != nil {
		return 0, err
	}
	if added == 0 {
		return 0, fmt.Errorf("subscription not found: %s", id)
	}

	s.mu.Lock()
	s.nodes = append(s.nodes, normalizeSubscriptionNodes(nodes, id)...)
	s.mu.Unlock()

	afterCfg := s.config.Get()
	s.reconcileRuntimeAfterConfigChangeBestEffort(beforeCfg, afterCfg, id, "add nodes to subscription")
	return added, nil
}

func (s *ClashService) RemoveNodeFromSubscription(id, targetNodeName string) error {
	id = strings.TrimSpace(id)
	targetNodeName = strings.TrimSpace(targetNodeName)
	if id == "" {
		return fmt.Errorf("subscription id is required")
	}
	if targetNodeName == "" {
		return fmt.Errorf("node name is required")
	}

	beforeCfg := s.config.Get()
	removed := false
	subFound := false
	if err := s.config.Update(func(cfg *ClashConfig) {
		for i := range cfg.Subscriptions {
			if cfg.Subscriptions[i].ID != id {
				continue
			}
			subFound = true
			for j, n := range cfg.Subscriptions[i].Nodes {
				if nodeName(n) != targetNodeName {
					continue
				}
				cfg.Subscriptions[i].Nodes = append(cfg.Subscriptions[i].Nodes[:j], cfg.Subscriptions[i].Nodes[j+1:]...)
				if cfg.Subscriptions[i].SelectedNode == targetNodeName {
					cfg.Subscriptions[i].SelectedNode = ""
					if len(cfg.Subscriptions[i].Nodes) > 0 {
						cfg.Subscriptions[i].SelectedNode = nodeName(cfg.Subscriptions[i].Nodes[0])
					}
				}
				removed = true
				break
			}
			break
		}
		clampChainReferences(cfg)
	}); err != nil {
		return err
	}
	if !subFound {
		return fmt.Errorf("subscription not found: %s", id)
	}
	if !removed {
		return fmt.Errorf("node not found: %s", targetNodeName)
	}

	s.mu.Lock()
	for i := 0; i < len(s.nodes); i++ {
		if nodeSourceID(s.nodes[i]) == id && nodeName(s.nodes[i]) == targetNodeName {
			s.nodes = append(s.nodes[:i], s.nodes[i+1:]...)
			break
		}
	}
	s.mu.Unlock()

	afterCfg := s.config.Get()
	s.reconcileRuntimeAfterConfigChangeBestEffort(beforeCfg, afterCfg, id, "remove node from subscription")
	return nil
}

func (s *ClashService) ExportSyncData() *ClashSyncData {
	cfg := s.config.Get()
	return &ClashSyncData{Config: *cfg}
}

func (s *ClashService) ImportSyncData(data *ClashSyncData) error {
	if data == nil {
		return fmt.Errorf("sync data is nil")
	}
	beforeCfg := s.config.Get()
	imported := data.Config
	normalizeImportedConfig(&imported, data.Nodes)
	if err := s.config.Update(func(cfg *ClashConfig) {
		*cfg = imported
	}); err != nil {
		return fmt.Errorf("save config: %w", err)
	}
	s.loadCachedNodes()
	afterCfg := s.config.Get()
	s.reconcileRuntimeAfterConfigChangeBestEffort(beforeCfg, afterCfg, "", "import sync data")
	return nil
}

func normalizeImportedConfig(cfg *ClashConfig, legacyNodes []ProxyNode) {
	normalizeConfig(cfg)

	if len(legacyNodes) > 0 {
		nodesBySource := make(map[string][]ProxyNode)
		var noSource []ProxyNode
		for _, n := range legacyNodes {
			sourceID := strings.TrimSpace(nodeSourceID(n))
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
			if nodes, ok := nodesBySource[cfg.Subscriptions[i].ID]; ok {
				cfg.Subscriptions[i].Nodes = append([]ProxyNode(nil), nodes...)
				continue
			}
			if len(cfg.Subscriptions) == 1 && len(noSource) > 0 {
				cfg.Subscriptions[i].Nodes = append([]ProxyNode(nil), noSource...)
			}
		}
	}

	clampChainReferences(cfg)
}

func (s *ClashService) loadCachedNodes() {
	cfg := s.config.Get()
	allNodes := make([]ProxyNode, 0)
	for _, sub := range cfg.Subscriptions {
		if sub.Enabled && len(sub.Nodes) > 0 {
			allNodes = append(allNodes, sub.Nodes...)
		}
	}
	s.mu.Lock()
	s.nodes = allNodes
	s.mu.Unlock()
}

func (s *ClashService) findNodeSafe(name string) *ProxyNode {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for i := range s.nodes {
		if nodeName(s.nodes[i]) == name {
			n := cloneNode(s.nodes[i])
			return &n
		}
	}
	return nil
}

func hasNodeByName(nodes []ProxyNode, targetName string) bool {
	for _, n := range nodes {
		if nodeName(n) == targetName {
			return true
		}
	}
	return false
}

func uniqueNodeName(name string, existing []ProxyNode) string {
	name = strings.TrimSpace(name)
	if name == "" {
		name = "node"
	}
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

func normalizeSubscriptionNodes(nodes []ProxyNode, sourceID string) []ProxyNode {
	existing := make([]ProxyNode, 0, len(nodes))
	out := make([]ProxyNode, 0, len(nodes))
	for i := range nodes {
		node := normalizeProxyNode(nodes[i], sourceID)
		setNodeString(node, nodeFieldName, uniqueNodeName(nodeName(node), existing))
		existing = append(existing, node)
		out = append(out, node)
	}
	return out
}

func activeSubscriptionIndex(cfg *ClashConfig) int {
	if cfg == nil {
		return -1
	}
	for i := range cfg.Subscriptions {
		if cfg.Subscriptions[i].Active {
			return i
		}
	}
	return -1
}

func activeSelectedNodeName(cfg *ClashConfig) string {
	sub := activeSubscriptionWithNodes(cfg)
	if sub == nil {
		return ""
	}
	return strings.TrimSpace(sub.SelectedNode)
}

func activeSubscriptionWithNodes(cfg *ClashConfig) *Subscription {
	if cfg == nil {
		return nil
	}
	activeIdx := activeSubscriptionIndex(cfg)
	if activeIdx >= 0 {
		sub := &cfg.Subscriptions[activeIdx]
		if sub.Enabled && len(sub.Nodes) > 0 {
			return sub
		}
	}
	for i := range cfg.Subscriptions {
		sub := &cfg.Subscriptions[i]
		if sub.Enabled && len(sub.Nodes) > 0 {
			return sub
		}
	}
	return nil
}

func resolveSubscriptionNodeName(cfg *ClashConfig, subscriptionID string, requested string) (string, bool) {
	subscriptionID = strings.TrimSpace(subscriptionID)
	requested = strings.TrimSpace(requested)
	if cfg == nil || subscriptionID == "" {
		return "", false
	}

	var sub *Subscription
	for i := range cfg.Subscriptions {
		if strings.TrimSpace(cfg.Subscriptions[i].ID) == subscriptionID {
			sub = &cfg.Subscriptions[i]
			break
		}
	}
	if sub == nil {
		return "", false
	}
	if requested == "" {
		return "", true
	}
	if hasNodeByName(sub.Nodes, requested) {
		return requested, true
	}

	if ref, ok := runtimeNodeRefByRuntimeProxyName(cfg)[requested]; ok {
		if strings.TrimSpace(ref.SubscriptionID) == subscriptionID && hasNodeByName(sub.Nodes, ref.NodeName) {
			return ref.NodeName, true
		}
	}

	prefixes := []string{strings.TrimSpace(sub.Name), strings.TrimSpace(sub.ID)}
	for _, prefix := range prefixes {
		if prefix == "" {
			continue
		}
		p := prefix + ":"
		if strings.HasPrefix(requested, p) {
			candidate := strings.TrimSpace(strings.TrimPrefix(requested, p))
			if candidate != "" && hasNodeByName(sub.Nodes, candidate) {
				return candidate, true
			}
		}
	}

	return "", false
}

func findEnabledSubscriptionWithNodes(cfg *ClashConfig, id string) *Subscription {
	id = strings.TrimSpace(id)
	if cfg == nil || id == "" {
		return nil
	}
	for i := range cfg.Subscriptions {
		sub := &cfg.Subscriptions[i]
		if sub.ID != id {
			continue
		}
		if !sub.Enabled || len(sub.Nodes) == 0 {
			return nil
		}
		return sub
	}
	return nil
}

func selectedNodeForSubscription(sub *Subscription) string {
	if sub == nil || len(sub.Nodes) == 0 {
		return ""
	}
	selected := strings.TrimSpace(sub.SelectedNode)
	if selected != "" && hasNodeByName(sub.Nodes, selected) {
		return selected
	}
	return strings.TrimSpace(nodeName(sub.Nodes[0]))
}

func runtimeProxyName(subName, subID, nodeName string) string {
	prefix := strings.TrimSpace(subName)
	if prefix == "" {
		prefix = strings.TrimSpace(subID)
	}
	return prefix + ":" + strings.TrimSpace(nodeName)
}

func runtimeNodeKey(subID, nodeName string) string {
	return strings.TrimSpace(subID) + "\x00" + strings.TrimSpace(nodeName)
}

func nextRuntimeProxyName(baseName string, usedRuntimeNames map[string]struct{}) string {
	baseName = strings.TrimSpace(baseName)
	if _, exists := usedRuntimeNames[baseName]; !exists {
		usedRuntimeNames[baseName] = struct{}{}
		return baseName
	}

	for suffix := 2; ; suffix++ {
		candidate := fmt.Sprintf("%s (%d)", baseName, suffix)
		if _, exists := usedRuntimeNames[candidate]; exists {
			continue
		}
		usedRuntimeNames[candidate] = struct{}{}
		return candidate
	}
}

func runtimeProxyNamesByNodeKey(cfg *ClashConfig) map[string]string {
	namesByKey := make(map[string]string)
	if cfg == nil {
		return namesByKey
	}

	usedRuntimeNames := make(map[string]struct{})
	for i := range cfg.Subscriptions {
		sub := &cfg.Subscriptions[i]
		if !sub.Enabled || len(sub.Nodes) == 0 {
			continue
		}
		for j := range sub.Nodes {
			node := sub.Nodes[j]
			baseName := runtimeProxyName(sub.Name, sub.ID, nodeName(node))
			runtimeName := nextRuntimeProxyName(baseName, usedRuntimeNames)
			namesByKey[runtimeNodeKey(sub.ID, nodeName(node))] = runtimeName
		}
	}
	return namesByKey
}

func runtimeProxyNameForNode(cfg *ClashConfig, subID, nodeName string) (string, bool) {
	name, ok := runtimeProxyNamesByNodeKey(cfg)[runtimeNodeKey(subID, nodeName)]
	return name, ok
}

func runtimeNodeRefByRuntimeProxyName(cfg *ClashConfig) map[string]NodeRef {
	out := make(map[string]NodeRef)
	for key, runtimeName := range runtimeProxyNamesByNodeKey(cfg) {
		parts := strings.SplitN(key, "\x00", 2)
		if len(parts) != 2 {
			continue
		}
		out[runtimeName] = NodeRef{SubscriptionID: parts[0], NodeName: parts[1]}
	}
	return out
}

func runtimeSubscriptionIDs(cfg *ClashConfig) map[string]struct{} {
	ids := make(map[string]struct{})
	if cfg == nil {
		return ids
	}

	for i := range cfg.Subscriptions {
		sub := &cfg.Subscriptions[i]
		if !sub.Enabled || len(sub.Nodes) == 0 {
			continue
		}
		ids[sub.ID] = struct{}{}
	}
	return ids
}

func subscriptionParticipatesInRuntime(cfg *ClashConfig, subscriptionID string) bool {
	subscriptionID = strings.TrimSpace(subscriptionID)
	if subscriptionID == "" {
		return false
	}
	_, ok := runtimeSubscriptionIDs(cfg)[subscriptionID]
	return ok
}

func buildRuntimeYAMLForConfig(cfg *ClashConfig) ([]byte, bool, error) {
	runtimeYAML, _, ready, err := buildRuntimeForConfig(cfg)
	return runtimeYAML, ready, err
}

func buildRuntimeForConfig(cfg *ClashConfig) ([]byte, *runtimePlan, bool, error) {
	if cfg == nil {
		return nil, nil, false, nil
	}

	normalized := copyConfig(cfg)
	clampChainReferences(normalized)

	activeSub := activeSubscriptionWithNodes(normalized)
	if activeSub == nil {
		return nil, nil, false, nil
	}

	chainEnabled := false
	var exitSub *Subscription
	if exitID := strings.TrimSpace(normalized.Chain.Exit.SubscriptionID); exitID != "" {
		exitSub = findEnabledSubscriptionWithNodes(normalized, exitID)
		if exitSub != nil && exitSub.ID != activeSub.ID {
			chainEnabled = true
		}
	}

	var middleSub *Subscription
	if chainEnabled && normalized.Chain.Middle != nil {
		middleID := strings.TrimSpace(normalized.Chain.Middle.SubscriptionID)
		if middleID != "" {
			middleSub = findEnabledSubscriptionWithNodes(normalized, middleID)
			if middleSub != nil && (middleSub.ID == activeSub.ID || middleSub.ID == exitSub.ID) {
				middleSub = nil
			}
		}
	}

	proxies := make([]any, 0)
	proxyGroups := make([]any, 0)
	allProxyNames := make([]string, 0)
	nodeNamesBySub := make(map[string][]string)
	selectedRuntimeBySub := make(map[string]string)
	usedRuntimeNames := make(map[string]struct{})
	nodeRuntimeByKey := make(map[string]string)

	appendSubscriptionProxies := func(sub *Subscription, dialerProxy string) error {
		if sub == nil || len(sub.Nodes) == 0 {
			return nil
		}

		names := make([]string, 0, len(sub.Nodes))
		selectedNodeName := selectedNodeForSubscription(sub)
		selectedRuntimeName := ""

		for i := range sub.Nodes {
			node := sub.Nodes[i]
			name := nextRuntimeProxyName(runtimeProxyName(sub.Name, sub.ID, nodeName(node)), usedRuntimeNames)
			nodeRuntimeByKey[runtimeNodeKey(sub.ID, nodeName(node))] = name

			nodeCopy := node
			proxyMap, err := buildProxyMap(name, &nodeCopy)
			if err != nil {
				return fmt.Errorf("build proxy %s: %w", name, err)
			}
			if dialerProxy != "" {
				proxyMap["dialer-proxy"] = dialerProxy
			}
			proxies = append(proxies, proxyMap)
			names = append(names, name)
			allProxyNames = append(allProxyNames, name)
			if nodeName(node) == selectedNodeName {
				selectedRuntimeName = name
			}
		}

		if len(names) > 0 && selectedRuntimeName == "" {
			selectedRuntimeName = names[0]
		}
		nodeNamesBySub[sub.ID] = names
		selectedRuntimeBySub[sub.ID] = selectedRuntimeName
		return nil
	}

	activeDialer := ""
	if chainEnabled {
		if middleSub != nil {
			activeDialer = runtimeGroupMiddle
		} else {
			activeDialer = runtimeGroupExit
		}
	}

	for i := range normalized.Subscriptions {
		sub := &normalized.Subscriptions[i]
		if !sub.Enabled || len(sub.Nodes) == 0 {
			continue
		}

		dialerProxy := ""
		switch sub.ID {
		case activeSub.ID:
			dialerProxy = activeDialer
		case strings.TrimSpace(normalized.Chain.Exit.SubscriptionID):
			if chainEnabled {
				dialerProxy = ""
			}
		}
		if middleSub != nil && sub.ID == middleSub.ID {
			dialerProxy = runtimeGroupExit
		}

		if err := appendSubscriptionProxies(sub, dialerProxy); err != nil {
			return nil, nil, false, err
		}
	}

	activeNames := nodeNamesBySub[activeSub.ID]
	if len(activeNames) == 0 {
		return nil, nil, false, nil
	}

	plan := &runtimePlan{
		trafficSelection: selectedRuntimeBySub[activeSub.ID],
	}
	if plan.trafficSelection == "" {
		plan.trafficSelection = activeNames[0]
	}

	if chainEnabled {
		exitNames := nodeNamesBySub[exitSub.ID]
		if len(exitNames) == 0 {
			chainEnabled = false
		} else {
			exitSelected := selectedRuntimeBySub[exitSub.ID]
			if exitSelected == "" {
				exitSelected = exitNames[0]
			}
			proxyGroups = append(proxyGroups, map[string]any{
				"name":    runtimeGroupExit,
				"type":    "select",
				"proxies": exitNames,
			})
			plan.exitSelection = exitSelected
		}
	}

	if chainEnabled && middleSub != nil {
		middleNames := nodeNamesBySub[middleSub.ID]
		if len(middleNames) > 0 {
			proxyGroups = append(proxyGroups, map[string]any{
				"name":    runtimeGroupMiddle,
				"type":    "select",
				"proxies": middleNames,
			})
			plan.middleSelection = selectedRuntimeBySub[middleSub.ID]
			if plan.middleSelection == "" {
				plan.middleSelection = middleNames[0]
			}
		}
	}

	if len(allProxyNames) > 0 {
		proxyGroups = append(proxyGroups, map[string]any{
			"name":    runtimeGroupAllNodes,
			"type":    "select",
			"proxies": allProxyNames,
		})
	}

	if chainEnabled {
		plan.trafficGroup = runtimeGroupChain
		proxyGroups = append(proxyGroups, map[string]any{
			"name":    runtimeGroupChain,
			"type":    "select",
			"proxies": activeNames,
		})
	} else {
		plan.trafficGroup = runtimeGroupSelector
		proxyGroups = append(proxyGroups, map[string]any{
			"name":    runtimeGroupSelector,
			"type":    "select",
			"proxies": activeNames,
		})
		plan.exitSelection = ""
		plan.middleSelection = ""
	}

	if len(allProxyNames) > 0 {
		proxyGroups = append(defaultHubProxyGroups(allProxyNames), proxyGroups...)
	}

	if current := nodeRuntimeByKey[runtimeNodeKey(activeSub.ID, selectedNodeForSubscription(activeSub))]; current != "" {
		plan.trafficSelection = current
	}
	if chainEnabled {
		if current := nodeRuntimeByKey[runtimeNodeKey(exitSub.ID, selectedNodeForSubscription(exitSub))]; current != "" {
			plan.exitSelection = current
		}
		if middleSub != nil {
			if current := nodeRuntimeByKey[runtimeNodeKey(middleSub.ID, selectedNodeForSubscription(middleSub))]; current != "" {
				plan.middleSelection = current
			}
		}
	}

	runtimeCfg := buildRuntimeBaseConfig(normalized)
	runtimeCfg["profile"] = map[string]any{
		"store-selected": false,
	}
	runtimeCfg["proxies"] = proxies
	runtimeCfg["proxy-groups"] = proxyGroups
	runtimeCfg["rules"] = []string{"MATCH," + plan.trafficGroup}

	runtimeCfg, err := mergeRuntimeConfigWithUserYAMLPrependRules(runtimeCfg, normalized,
		"mixed-port", "bind-address", "allow-lan", "mode", "log-level", "ipv6", "profile", "proxies", "proxy-groups", "rules",
	)
	if err != nil {
		return nil, nil, false, err
	}

	runtimeYAML, err := yaml.Marshal(runtimeCfg)
	if err != nil {
		return nil, nil, false, fmt.Errorf("marshal runtime yaml: %w", err)
	}
	return runtimeYAML, plan, true, nil
}

func clampChainReferences(cfg *ClashConfig) {
	if cfg == nil {
		return
	}

	activeIdx := -1
	for i := range cfg.Subscriptions {
		sub := &cfg.Subscriptions[i]
		if sub.Nodes == nil {
			sub.Nodes = []ProxyNode{}
		}
		if len(sub.Nodes) == 0 {
			sub.SelectedNode = ""
		} else if !hasNodeByName(sub.Nodes, sub.SelectedNode) {
			sub.SelectedNode = nodeName(sub.Nodes[0])
		}

		if sub.Active {
			if activeIdx == -1 && sub.Enabled {
				activeIdx = i
			} else {
				sub.Active = false
			}
		}
	}

	if activeIdx == -1 {
		for i := range cfg.Subscriptions {
			if cfg.Subscriptions[i].Enabled {
				cfg.Subscriptions[i].Active = true
				activeIdx = i
				break
			}
		}
	}
	for i := range cfg.Subscriptions {
		cfg.Subscriptions[i].Active = i == activeIdx
	}

	var activeSub *Subscription
	if activeIdx >= 0 && len(cfg.Subscriptions[activeIdx].Nodes) > 0 {
		activeSub = &cfg.Subscriptions[activeIdx]
		cfg.Chain.Entry = NodeRef{
			SubscriptionID: activeSub.ID,
			NodeName:       selectedNodeForSubscription(activeSub),
		}
	} else {
		cfg.Chain.Entry = NodeRef{}
	}

	exitID := strings.TrimSpace(cfg.Chain.Exit.SubscriptionID)
	exitSub := findEnabledSubscriptionWithNodes(cfg, exitID)
	if exitSub == nil || (activeSub != nil && exitSub.ID == activeSub.ID) {
		cfg.Chain.Exit = NodeRef{}
	} else {
		cfg.Chain.Exit = NodeRef{
			SubscriptionID: exitSub.ID,
			NodeName:       selectedNodeForSubscription(exitSub),
		}
	}

	if cfg.Chain.Middle != nil {
		middleID := strings.TrimSpace(cfg.Chain.Middle.SubscriptionID)
		middleSub := findEnabledSubscriptionWithNodes(cfg, middleID)
		if middleSub == nil ||
			(activeSub != nil && middleSub.ID == activeSub.ID) ||
			(strings.TrimSpace(cfg.Chain.Exit.SubscriptionID) != "" && middleSub.ID == cfg.Chain.Exit.SubscriptionID) {
			cfg.Chain.Middle = nil
		} else {
			cfg.Chain.Middle = &NodeRef{
				SubscriptionID: middleSub.ID,
				NodeName:       selectedNodeForSubscription(middleSub),
			}
		}
	}
}

func (s *ClashService) syncRuntimeSelections(cfg *ClashConfig) error {
	s.mu.RLock()
	running := s.running
	s.mu.RUnlock()
	if !running {
		return nil
	}

	_, plan, ready, err := buildRuntimeForConfig(cfg)
	if err != nil {
		return err
	}
	if !ready {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), runtimeControllerRequestTimeout)
	defer cancel()
	if err := applyRuntimeSelections(ctx, cfg, plan); err != nil {
		if isRuntimeSelectionMismatchError(err) {
			log.Printf("[clash] runtime selection mismatch, restarting runtime: %v", err)
			if restartErr := s.restartRuntime(); restartErr != nil {
				return fmt.Errorf("runtime selection mismatch: %v; restart failed: %w", err, restartErr)
			}
			return nil
		}
		return err
	}
	return nil
}

func isRuntimeSelectionMismatchError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "runtime group not found") ||
		strings.Contains(msg, "proxy group not found") ||
		strings.Contains(msg, "proxy not exist")
}

func (s *ClashService) reconcileRuntimeAfterConfigChange(beforeCfg, afterCfg *ClashConfig, touchedSubscriptionID string) error {
	if strings.TrimSpace(touchedSubscriptionID) != "" &&
		!subscriptionParticipatesInRuntime(beforeCfg, touchedSubscriptionID) &&
		!subscriptionParticipatesInRuntime(afterCfg, touchedSubscriptionID) {
		return nil
	}

	if s.shouldRestartRuntime(beforeCfg, afterCfg) {
		return s.restartRuntime()
	}
	return s.syncRuntimeSelections(afterCfg)
}

func (s *ClashService) reconcileRuntimeAfterConfigChangeBestEffort(beforeCfg, afterCfg *ClashConfig, touchedSubscriptionID, action string) {
	if err := s.reconcileRuntimeAfterConfigChange(beforeCfg, afterCfg, touchedSubscriptionID); err != nil {
		log.Printf("[clash] %s: runtime reconcile warning: %v", strings.TrimSpace(action), err)
	}
}

func (s *ClashService) shouldRestartRuntime(beforeCfg, afterCfg *ClashConfig) bool {
	s.mu.RLock()
	running := s.running
	s.mu.RUnlock()
	if !running {
		return false
	}

	beforeRuntime, beforeReady, beforeErr := buildRuntimeYAMLForConfig(beforeCfg)
	afterRuntime, afterReady, afterErr := buildRuntimeYAMLForConfig(afterCfg)
	if beforeErr != nil || afterErr != nil {
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

func (s *ClashService) restartRuntime() error {
	s.mu.RLock()
	running := s.running
	s.mu.RUnlock()
	if !running {
		return nil
	}
	if err := s.Stop(); err != nil {
		log.Printf("[clash] restart stop error: %v", err)
	}
	if err := s.Start(); err != nil {
		return fmt.Errorf("restart clash: %w", err)
	}
	return nil
}

func (s *ClashService) startWithRuntime(runtimeYAML []byte) error {
	inst, err := startRuntimeInstance(runtimeYAML, s.dataDir)
	if err != nil {
		return fmt.Errorf("start clash runtime: %w", err)
	}
	s.instance = inst
	s.running = true
	log.Printf("[clash] started with runtime config")
	return nil
}
