package main

import (
	"context"
	"encoding/json"
	"fmt"

	"clisimplehub/internal/plugin"
)

// clashProvider returns the cached ClashDesktopProvider or nil.
func clashProvider() plugin.ClashDesktopProvider {
	return plugin.GetClashDesktopProviderCached()
}

// IsClashAvailable returns true if the clash plugin is compiled and available.
func (a *App) IsClashAvailable() bool {
	return clashProvider() != nil
}

// GetClashStatus returns the current Clash service status.
func (a *App) GetClashStatus() (map[string]interface{}, error) {
	vp := clashProvider()
	if vp == nil {
		return nil, fmt.Errorf("clash plugin not available")
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

// GetClashNodes returns all parsed proxy nodes.
func (a *App) GetClashNodes() ([]map[string]interface{}, error) {
	vp := clashProvider()
	if vp == nil {
		return nil, fmt.Errorf("clash plugin not available")
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

// SelectClashNode selects a proxy node by name.
func (a *App) SelectClashNode(nodeName string) error {
	vp := clashProvider()
	if vp == nil {
		return fmt.Errorf("clash plugin not available")
	}
	return vp.SelectNode(nodeName)
}

// TestClashNode tests a single node's latency.
func (a *App) TestClashNode(nodeName string) (map[string]interface{}, error) {
	vp := clashProvider()
	if vp == nil {
		return nil, fmt.Errorf("clash plugin not available")
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

// TestClashNodeTCP tests a single node's TCP connect latency.
func (a *App) TestClashNodeTCP(nodeName string) (map[string]interface{}, error) {
	vp := clashProvider()
	if vp == nil {
		return nil, fmt.Errorf("clash plugin not available")
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

// CancelClashSpeedTests cancels all in-flight clash node speed tests.
func (a *App) CancelClashSpeedTests() error {
	vp := clashProvider()
	if vp == nil {
		return fmt.Errorf("clash plugin not available")
	}
	return vp.CancelSpeedTests()
}

// GetClashConfig returns the Clash plugin configuration.
func (a *App) GetClashConfig() (map[string]interface{}, error) {
	vp := clashProvider()
	if vp == nil {
		return nil, fmt.Errorf("clash plugin not available")
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

// SaveClashConfig saves the Clash plugin configuration.
func (a *App) SaveClashConfig(config string) error {
	vp := clashProvider()
	if vp == nil {
		return fmt.Errorf("clash plugin not available")
	}
	configPath := ""
	if a.configLoader != nil {
		configPath = a.configLoader.GetPath()
	}
	return vp.SaveConfig(configPath, json.RawMessage(config))
}

// RefreshClashSubscriptions refreshes all enabled subscriptions.
func (a *App) RefreshClashSubscriptions() (map[string]interface{}, error) {
	vp := clashProvider()
	if vp == nil {
		return nil, fmt.Errorf("clash plugin not available")
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

// AddClashSubscription adds a new subscription source.
func (a *App) AddClashSubscription(name, url string) error {
	vp := clashProvider()
	if vp == nil {
		return fmt.Errorf("clash plugin not available")
	}
	return vp.AddSubscription(name, url)
}

// RemoveClashSubscription removes a subscription by ID.
func (a *App) RemoveClashSubscription(id string) error {
	vp := clashProvider()
	if vp == nil {
		return fmt.Errorf("clash plugin not available")
	}
	return vp.RemoveSubscription(id)
}

// StartClash starts the Clash proxy service.
func (a *App) StartClash() error {
	vp := clashProvider()
	if vp == nil {
		return fmt.Errorf("clash plugin not available")
	}
	return vp.Start()
}

// StopClash stops the Clash proxy service.
func (a *App) StopClash() error {
	vp := clashProvider()
	if vp == nil {
		return fmt.Errorf("clash plugin not available")
	}
	return vp.Stop()
}

// ToggleClashSubscription toggles the enabled state of a subscription.
func (a *App) ToggleClashSubscription(id string) error {
	vp := clashProvider()
	if vp == nil {
		return fmt.Errorf("clash plugin not available")
	}
	return vp.ToggleSubscription(id)
}

// RefreshSingleClashSubscription refreshes a single subscription by ID.
func (a *App) RefreshSingleClashSubscription(id string) (map[string]interface{}, error) {
	vp := clashProvider()
	if vp == nil {
		return nil, fmt.Errorf("clash plugin not available")
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

// ActivateClashSubscription activates a subscription and starts clash with its selected node.
func (a *App) ActivateClashSubscription(id string) error {
	vp := clashProvider()
	if vp == nil {
		return fmt.Errorf("clash plugin not available")
	}
	return vp.ActivateSubscription(id)
}

// SetActiveClashSubscription sets a subscription as active (only one can be active).
func (a *App) SetActiveClashSubscription(id string) error {
	vp := clashProvider()
	if vp == nil {
		return fmt.Errorf("clash plugin not available")
	}
	return vp.SetActiveSubscription(id)
}

// SetClashDialerProxySubscription sets which subscription is used as dialer proxy.
func (a *App) SetClashDialerProxySubscription(id string) error {
	vp := clashProvider()
	if vp == nil {
		return fmt.Errorf("clash plugin not available")
	}
	return vp.SetDialerProxySubscription(id)
}

// UpdateClashSubscriptionSelectedNode updates the selected node for a subscription.
func (a *App) UpdateClashSubscriptionSelectedNode(id, nodeName string) error {
	vp := clashProvider()
	if vp == nil {
		return fmt.Errorf("clash plugin not available")
	}
	return vp.UpdateSubscriptionSelectedNode(id, nodeName)
}

// UpdateClashSubscription updates a subscription's name and URL.
func (a *App) UpdateClashSubscription(id, name, url string) error {
	vp := clashProvider()
	if vp == nil {
		return fmt.Errorf("clash plugin not available")
	}
	return vp.UpdateSubscription(id, name, url)
}

// GetClashNodeConfig returns the configuration of a specific node as JSON string.
func (a *App) GetClashNodeConfig(nodeName string) (string, error) {
	vp := clashProvider()
	if vp == nil {
		return "", fmt.Errorf("clash plugin not available")
	}
	return vp.GetNodeConfig(nodeName)
}

// AddClashNodesToSubscription parses content and adds nodes to a subscription.
func (a *App) AddClashNodesToSubscription(id, content string) (int, error) {
	vp := clashProvider()
	if vp == nil {
		return 0, fmt.Errorf("clash plugin not available")
	}
	return vp.AddNodesToSubscription(id, content)
}

// RemoveClashNodeFromSubscription removes a node from a subscription.
func (a *App) RemoveClashNodeFromSubscription(id, nodeName string) error {
	vp := clashProvider()
	if vp == nil {
		return fmt.Errorf("clash plugin not available")
	}
	return vp.RemoveNodeFromSubscription(id, nodeName)
}

// ParseClashNodesForSubscription parses node content for preview without persisting.
func (a *App) ParseClashNodesForSubscription(id, content string) ([]map[string]interface{}, error) {
	vp := clashProvider()
	if vp == nil {
		return nil, fmt.Errorf("clash plugin not available")
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

// ReplaceClashSubscriptionNodes replaces all nodes in a subscription atomically.
func (a *App) ReplaceClashSubscriptionNodes(id, nodesJSON, selectedNode string) error {
	vp := clashProvider()
	if vp == nil {
		return fmt.Errorf("clash plugin not available")
	}
	return vp.ReplaceSubscriptionNodes(id, json.RawMessage(nodesJSON), selectedNode)
}

// clashSyncExportRaw exports Clash sync data as raw JSON via the plugin interface.
// Returns nil when the clash plugin is unavailable or has no subscriptions and no nodes.
func (a *App) clashSyncExportRaw() json.RawMessage {
	p := plugin.ByName("clash")
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

// restoreClashConfig restores Clash config from backup data via the plugin interface.
func (a *App) restoreClashConfig(data interface{}) {
	p := plugin.ByName("clash")
	if p == nil {
		return
	}
	importer, ok := p.(plugin.ConfigSyncImporter)
	if !ok {
		return
	}
	jsonData, err := json.Marshal(data)
	if err != nil {
		fmt.Printf("warning: failed to marshal clash backup data: %v\n", err)
		return
	}
	configPath := ""
	if a.configLoader != nil {
		configPath = a.configLoader.GetPath()
	}
	if err := importer.SyncImport(configPath, jsonData); err != nil {
		fmt.Printf("warning: failed to restore clash config: %v\n", err)
	}
}
