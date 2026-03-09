package main

import (
	"context"
	"encoding/json"
	"fmt"

	"clisimplehub/internal/plugin"
)

// xrayProvider returns the cached XRayDesktopProvider or nil.
func xrayProvider() plugin.XRayDesktopProvider {
	return plugin.GetXRayDesktopProviderCached()
}

// IsXRayAvailable returns true if the xray plugin is compiled and available.
func (a *App) IsXRayAvailable() bool {
	return xrayProvider() != nil
}

// GetXRayStatus returns the current XRay service status.
func (a *App) GetXRayStatus() (map[string]interface{}, error) {
	vp := xrayProvider()
	if vp == nil {
		return nil, fmt.Errorf("xray plugin not available")
	}
	raw, err := vp.GetStatus()
	if err != nil {
		return nil, err
	}
	var result map[string]interface{}
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, err
	}
	return result, nil
}

// GetXRayNodes returns all parsed proxy nodes.
func (a *App) GetXRayNodes() ([]map[string]interface{}, error) {
	vp := xrayProvider()
	if vp == nil {
		return nil, fmt.Errorf("xray plugin not available")
	}
	raw, err := vp.GetNodes()
	if err != nil {
		return nil, err
	}
	var result []map[string]interface{}
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, err
	}
	return result, nil
}

// SelectXRayNode selects a proxy node by name.
func (a *App) SelectXRayNode(nodeName string) error {
	vp := xrayProvider()
	if vp == nil {
		return fmt.Errorf("xray plugin not available")
	}
	return vp.SelectNode(nodeName)
}

// TestXRayNode tests a single node's latency.
func (a *App) TestXRayNode(nodeName string) (map[string]interface{}, error) {
	vp := xrayProvider()
	if vp == nil {
		return nil, fmt.Errorf("xray plugin not available")
	}
	ctx := a.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	raw, err := vp.TestNode(ctx, nodeName)
	if err != nil {
		return nil, err
	}
	var result map[string]interface{}
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, err
	}
	return result, nil
}

// TestXRayNodeTCP tests a single node's TCP connect latency.
func (a *App) TestXRayNodeTCP(nodeName string) (map[string]interface{}, error) {
	vp := xrayProvider()
	if vp == nil {
		return nil, fmt.Errorf("xray plugin not available")
	}
	ctx := a.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	raw, err := vp.TestNodeTCP(ctx, nodeName)
	if err != nil {
		return nil, err
	}
	var result map[string]interface{}
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, err
	}
	return result, nil
}

// GetXRayConfig returns the XRay plugin configuration.
func (a *App) GetXRayConfig() (map[string]interface{}, error) {
	vp := xrayProvider()
	if vp == nil {
		return nil, fmt.Errorf("xray plugin not available")
	}
	configPath := ""
	if a.configLoader != nil {
		configPath = a.configLoader.GetPath()
	}
	raw, err := vp.GetConfig(configPath)
	if err != nil {
		return nil, err
	}
	var result map[string]interface{}
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, err
	}
	return result, nil
}

// SaveXRayConfig saves the XRay plugin configuration.
func (a *App) SaveXRayConfig(config string) error {
	vp := xrayProvider()
	if vp == nil {
		return fmt.Errorf("xray plugin not available")
	}
	configPath := ""
	if a.configLoader != nil {
		configPath = a.configLoader.GetPath()
	}
	return vp.SaveConfig(configPath, json.RawMessage(config))
}

// RefreshXRaySubscriptions refreshes all enabled subscriptions.
func (a *App) RefreshXRaySubscriptions() (map[string]interface{}, error) {
	vp := xrayProvider()
	if vp == nil {
		return nil, fmt.Errorf("xray plugin not available")
	}
	ctx := a.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	raw, err := vp.RefreshSubscriptions(ctx)
	if err != nil {
		return nil, err
	}
	var result map[string]interface{}
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, err
	}
	return result, nil
}

// AddXRaySubscription adds a new subscription source.
func (a *App) AddXRaySubscription(name, url string) error {
	vp := xrayProvider()
	if vp == nil {
		return fmt.Errorf("xray plugin not available")
	}
	return vp.AddSubscription(name, url)
}

// RemoveXRaySubscription removes a subscription by ID.
func (a *App) RemoveXRaySubscription(id string) error {
	vp := xrayProvider()
	if vp == nil {
		return fmt.Errorf("xray plugin not available")
	}
	return vp.RemoveSubscription(id)
}

// StartXRay starts the XRay proxy service.
func (a *App) StartXRay() error {
	vp := xrayProvider()
	if vp == nil {
		return fmt.Errorf("xray plugin not available")
	}
	return vp.Start()
}

// StopXRay stops the XRay proxy service.
func (a *App) StopXRay() error {
	vp := xrayProvider()
	if vp == nil {
		return fmt.Errorf("xray plugin not available")
	}
	return vp.Stop()
}

// ToggleXRaySubscription toggles the enabled state of a subscription.
func (a *App) ToggleXRaySubscription(id string) error {
	vp := xrayProvider()
	if vp == nil {
		return fmt.Errorf("xray plugin not available")
	}
	return vp.ToggleSubscription(id)
}

// RefreshSingleXRaySubscription refreshes a single subscription by ID.
func (a *App) RefreshSingleXRaySubscription(id string) (map[string]interface{}, error) {
	vp := xrayProvider()
	if vp == nil {
		return nil, fmt.Errorf("xray plugin not available")
	}
	ctx := a.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	raw, err := vp.RefreshSingleSubscription(ctx, id)
	if err != nil {
		return nil, err
	}
	var result map[string]interface{}
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, err
	}
	return result, nil
}

// ActivateXRaySubscription activates a subscription and starts xray with its selected node.
func (a *App) ActivateXRaySubscription(id string) error {
	vp := xrayProvider()
	if vp == nil {
		return fmt.Errorf("xray plugin not available")
	}
	return vp.ActivateSubscription(id)
}

// SetActiveXRaySubscription sets a subscription as active (only one can be active).
func (a *App) SetActiveXRaySubscription(id string) error {
	vp := xrayProvider()
	if vp == nil {
		return fmt.Errorf("xray plugin not available")
	}
	return vp.SetActiveSubscription(id)
}

// SetXRayDialerProxySubscription sets which subscription is used as dialer proxy.
func (a *App) SetXRayDialerProxySubscription(id string) error {
	vp := xrayProvider()
	if vp == nil {
		return fmt.Errorf("xray plugin not available")
	}
	return vp.SetDialerProxySubscription(id)
}

// UpdateXRaySubscriptionSelectedNode updates the selected node for a subscription.
func (a *App) UpdateXRaySubscriptionSelectedNode(id, nodeName string) error {
	vp := xrayProvider()
	if vp == nil {
		return fmt.Errorf("xray plugin not available")
	}
	return vp.UpdateSubscriptionSelectedNode(id, nodeName)
}

// UpdateXRaySubscription updates a subscription's name and URL.
func (a *App) UpdateXRaySubscription(id, name, url string) error {
	vp := xrayProvider()
	if vp == nil {
		return fmt.Errorf("xray plugin not available")
	}
	return vp.UpdateSubscription(id, name, url)
}

// GetXRayNodeConfig returns the configuration of a specific node as JSON string.
func (a *App) GetXRayNodeConfig(nodeName string) (string, error) {
	vp := xrayProvider()
	if vp == nil {
		return "", fmt.Errorf("xray plugin not available")
	}
	return vp.GetNodeConfig(nodeName)
}

// AddXRayNodesToSubscription parses content and adds nodes to a subscription.
func (a *App) AddXRayNodesToSubscription(id, content string) (int, error) {
	vp := xrayProvider()
	if vp == nil {
		return 0, fmt.Errorf("xray plugin not available")
	}
	return vp.AddNodesToSubscription(id, content)
}

// RemoveXRayNodeFromSubscription removes a node from a subscription.
func (a *App) RemoveXRayNodeFromSubscription(id, nodeName string) error {
	vp := xrayProvider()
	if vp == nil {
		return fmt.Errorf("xray plugin not available")
	}
	return vp.RemoveNodeFromSubscription(id, nodeName)
}

// ParseXRayNodesForSubscription parses node content for preview without persisting.
func (a *App) ParseXRayNodesForSubscription(id, content string) ([]map[string]interface{}, error) {
	vp := xrayProvider()
	if vp == nil {
		return nil, fmt.Errorf("xray plugin not available")
	}
	raw, err := vp.ParseNodesForSubscription(id, content)
	if err != nil {
		return nil, err
	}
	var result []map[string]interface{}
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, err
	}
	return result, nil
}

// ReplaceXRaySubscriptionNodes replaces all nodes in a subscription atomically.
func (a *App) ReplaceXRaySubscriptionNodes(id, nodesJSON, selectedNode string) error {
	vp := xrayProvider()
	if vp == nil {
		return fmt.Errorf("xray plugin not available")
	}
	return vp.ReplaceSubscriptionNodes(id, json.RawMessage(nodesJSON), selectedNode)
}

// xraySyncExportRaw exports XRay sync data as raw JSON via the plugin interface.
// Returns nil when the xray plugin is unavailable or has no subscriptions and no nodes.
func (a *App) xraySyncExportRaw() json.RawMessage {
	p := plugin.ByName("xray")
	if p == nil {
		return nil
	}
	exporter, ok := p.(plugin.ConfigSyncExporter)
	if !ok {
		return nil
	}
	configPath := ""
	if a.configLoader != nil {
		configPath = a.configLoader.GetPath()
	}
	_, data, err := exporter.SyncExport(configPath)
	if err != nil || len(data) == 0 {
		return nil
	}
	// Skip when empty (no subscriptions and no nodes)
	var probe struct {
		Config struct {
			Subscriptions []json.RawMessage `json:"subscriptions"`
		} `json:"config"`
		Nodes []json.RawMessage `json:"nodes"`
	}
	if json.Unmarshal(data, &probe) != nil || (len(probe.Config.Subscriptions) == 0 && len(probe.Nodes) == 0) {
		return nil
	}
	return data
}

// restoreXRayConfig restores XRay config from backup data via the plugin interface.
func (a *App) restoreXRayConfig(data interface{}) {
	p := plugin.ByName("xray")
	if p == nil {
		return
	}
	importer, ok := p.(plugin.ConfigSyncImporter)
	if !ok {
		return
	}
	jsonData, err := json.Marshal(data)
	if err != nil {
		fmt.Printf("warning: failed to marshal xray backup data: %v\n", err)
		return
	}
	configPath := ""
	if a.configLoader != nil {
		configPath = a.configLoader.GetPath()
	}
	if err := importer.SyncImport(configPath, jsonData); err != nil {
		fmt.Printf("warning: failed to restore xray config: %v\n", err)
	}
}
