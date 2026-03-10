package clashplugin

import (
	"context"
	"encoding/json"
	"fmt"

	"clisimplehub/internal/plugin"
)

// desktopFacade implements plugin.ClashDesktopProvider.
type desktopFacade struct{}

var _ plugin.ClashDesktopProvider = (*ClashPlugin)(nil)

func (p *ClashPlugin) GetStatus() (json.RawMessage, error) {
	svc := p.getService()
	if svc == nil {
		return nil, fmt.Errorf("clash plugin not initialized")
	}
	return json.Marshal(svc.GetStatus())
}

func (p *ClashPlugin) GetNodes() (json.RawMessage, error) {
	svc := p.getService()
	if svc == nil {
		return nil, fmt.Errorf("clash plugin not initialized")
	}
	return json.Marshal(svc.GetNodes())
}

func (p *ClashPlugin) SelectNode(nodeName string) error {
	svc := p.getService()
	if svc == nil {
		return fmt.Errorf("clash plugin not initialized")
	}
	return svc.SelectNode(nodeName)
}

func (p *ClashPlugin) GetConfig(_ string) (json.RawMessage, error) {
	svc := p.getService()
	if svc == nil {
		return nil, fmt.Errorf("clash plugin not initialized")
	}
	return json.Marshal(svc.config.Get())
}

func (p *ClashPlugin) SaveConfig(_ string, dto json.RawMessage) error {
	svc := p.getService()
	if svc == nil {
		return fmt.Errorf("clash plugin not initialized")
	}
	var cfg ClashConfig
	if err := json.Unmarshal(dto, &cfg); err != nil {
		return fmt.Errorf("invalid config: %w", err)
	}
	if _, err := parseUserYAMLOverride(&cfg); err != nil {
		return fmt.Errorf("invalid user yaml: %w", err)
	}
	before := svc.config.Get()
	if err := svc.config.Update(func(c *ClashConfig) {
		*c = cfg
	}); err != nil {
		return err
	}
	after := svc.config.Get()
	svc.reconcileRuntimeAfterConfigChangeBestEffort(before, after, "", "save config")
	return nil
}

func (p *ClashPlugin) RefreshSubscriptions(ctx context.Context) (json.RawMessage, error) {
	svc := p.getService()
	if svc == nil {
		return nil, fmt.Errorf("clash plugin not initialized")
	}
	result, err := svc.RefreshSubscriptions(ctx)
	if err != nil {
		return nil, err
	}
	return json.Marshal(result)
}

func (p *ClashPlugin) AddSubscription(name, url string) error {
	svc := p.getService()
	if svc == nil {
		return fmt.Errorf("clash plugin not initialized")
	}
	return svc.AddSubscription(name, url)
}

func (p *ClashPlugin) RemoveSubscription(id string) error {
	svc := p.getService()
	if svc == nil {
		return fmt.Errorf("clash plugin not initialized")
	}
	return svc.RemoveSubscription(id)
}

func (p *ClashPlugin) TestNode(ctx context.Context, nodeName string) (json.RawMessage, error) {
	svc := p.getService()
	if svc == nil {
		return nil, fmt.Errorf("clash plugin not initialized")
	}
	return json.Marshal(testSingleNode(ctx, svc, nodeName))
}

func (p *ClashPlugin) TestNodeTCP(ctx context.Context, nodeName string) (json.RawMessage, error) {
	svc := p.getService()
	if svc == nil {
		return nil, fmt.Errorf("clash plugin not initialized")
	}
	return json.Marshal(testSingleNodeTCP(ctx, svc, nodeName))
}

func (p *ClashPlugin) CancelSpeedTests() error {
	svc := p.getService()
	if svc == nil {
		return fmt.Errorf("clash plugin not initialized")
	}
	svc.CancelSpeedTests()
	return nil
}

func (p *ClashPlugin) Start() error {
	svc := p.getService()
	if svc == nil {
		return fmt.Errorf("clash plugin not initialized")
	}
	return svc.Start()
}

func (p *ClashPlugin) Stop() error {
	svc := p.getService()
	if svc == nil {
		return fmt.Errorf("clash plugin not initialized")
	}
	return svc.Stop()
}

func (p *ClashPlugin) ToggleSubscription(id string) error {
	svc := p.getService()
	if svc == nil {
		return fmt.Errorf("clash plugin not initialized")
	}
	return svc.ToggleSubscription(id)
}

func (p *ClashPlugin) RefreshSingleSubscription(ctx context.Context, id string) (json.RawMessage, error) {
	svc := p.getService()
	if svc == nil {
		return nil, fmt.Errorf("clash plugin not initialized")
	}
	result, err := svc.RefreshSingleSubscription(ctx, id)
	if err != nil {
		return nil, err
	}
	return json.Marshal(result)
}

func (p *ClashPlugin) ActivateSubscription(id string) error {
	svc := p.getService()
	if svc == nil {
		return fmt.Errorf("clash plugin not initialized")
	}
	return svc.ActivateSubscription(id)
}

func (p *ClashPlugin) SetActiveSubscription(id string) error {
	svc := p.getService()
	if svc == nil {
		return fmt.Errorf("clash plugin not initialized")
	}
	return svc.SetActiveSubscription(id)
}

func (p *ClashPlugin) SetDialerProxySubscription(id string) error {
	svc := p.getService()
	if svc == nil {
		return fmt.Errorf("clash plugin not initialized")
	}
	return svc.SetDialerProxySubscription(id)
}

func (p *ClashPlugin) UpdateSubscriptionSelectedNode(id, nodeName string) error {
	svc := p.getService()
	if svc == nil {
		return fmt.Errorf("clash plugin not initialized")
	}
	return svc.UpdateSubscriptionSelectedNode(id, nodeName)
}

func (p *ClashPlugin) UpdateSubscription(id, name, url string) error {
	svc := p.getService()
	if svc == nil {
		return fmt.Errorf("clash plugin not initialized")
	}
	return svc.UpdateSubscription(id, name, url)
}

func (p *ClashPlugin) GetNodeConfig(nodeName string) (string, error) {
	svc := p.getService()
	if svc == nil {
		return "", fmt.Errorf("clash plugin not initialized")
	}
	return svc.GetNodeConfig(nodeName)
}

func (p *ClashPlugin) AddNodesToSubscription(id, content string) (int, error) {
	svc := p.getService()
	if svc == nil {
		return 0, fmt.Errorf("clash plugin not initialized")
	}
	return svc.AddNodesToSubscription(id, content)
}

func (p *ClashPlugin) RemoveNodeFromSubscription(id, nodeName string) error {
	svc := p.getService()
	if svc == nil {
		return fmt.Errorf("clash plugin not initialized")
	}
	return svc.RemoveNodeFromSubscription(id, nodeName)
}

func (p *ClashPlugin) ParseNodesForSubscription(id, content string) (json.RawMessage, error) {
	svc := p.getService()
	if svc == nil {
		return nil, fmt.Errorf("clash plugin not initialized")
	}
	nodes, err := svc.ParseNodesForSubscription(id, content)
	if err != nil {
		return nil, err
	}
	return json.Marshal(nodes)
}

func (p *ClashPlugin) ReplaceSubscriptionNodes(id string, nodes json.RawMessage, selectedNode string) error {
	svc := p.getService()
	if svc == nil {
		return fmt.Errorf("clash plugin not initialized")
	}
	var list []ProxyNode
	if err := json.Unmarshal(nodes, &list); err != nil {
		return fmt.Errorf("invalid nodes payload: %w", err)
	}
	return svc.ReplaceSubscriptionNodes(id, list, selectedNode)
}
