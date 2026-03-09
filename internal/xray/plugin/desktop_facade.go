package xrayplugin

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"

	"clisimplehub/internal/plugin"
)

// desktopFacade implements plugin.XRayDesktopProvider.
// It delegates to the plugin's service instance.
type desktopFacade struct{}

// Compile-time check.
var _ plugin.XRayDesktopProvider = (*XRayPlugin)(nil)

func (p *XRayPlugin) GetStatus() (json.RawMessage, error) {
	svc := p.getService()
	if svc == nil {
		return nil, fmt.Errorf("xray plugin not initialized")
	}
	data, err := json.Marshal(svc.GetStatus())
	return data, err
}

func (p *XRayPlugin) GetNodes() (json.RawMessage, error) {
	svc := p.getService()
	if svc == nil {
		return nil, fmt.Errorf("xray plugin not initialized")
	}
	data, err := json.Marshal(svc.GetNodes())
	return data, err
}

func (p *XRayPlugin) SelectNode(nodeName string) error {
	svc := p.getService()
	if svc == nil {
		return fmt.Errorf("xray plugin not initialized")
	}
	return svc.SelectNode(nodeName)
}

func (p *XRayPlugin) GetConfig(configPath string) (json.RawMessage, error) {
	svc := p.getService()
	if svc == nil {
		return nil, fmt.Errorf("xray plugin not initialized")
	}
	data, err := json.Marshal(svc.config.Get())
	return data, err
}

func (p *XRayPlugin) SaveConfig(configPath string, dto json.RawMessage) error {
	svc := p.getService()
	if svc == nil {
		return fmt.Errorf("xray plugin not initialized")
	}
	var cfg XRayConfig
	if err := json.Unmarshal(dto, &cfg); err != nil {
		return fmt.Errorf("invalid config: %w", err)
	}
	existing := svc.config.Get()
	raw := bytes.TrimSpace(cfg.Template)
	if len(raw) == 0 || bytes.Equal(raw, jsonNull) {
		cfg.Template = append(json.RawMessage(nil), existing.Template...)
	}
	return svc.config.Update(func(c *XRayConfig) {
		*c = cfg
	})
}

func (p *XRayPlugin) RefreshSubscriptions(ctx context.Context) (json.RawMessage, error) {
	svc := p.getService()
	if svc == nil {
		return nil, fmt.Errorf("xray plugin not initialized")
	}
	result, err := svc.RefreshSubscriptions(ctx)
	if err != nil {
		return nil, err
	}
	data, err := json.Marshal(result)
	return data, err
}

func (p *XRayPlugin) AddSubscription(name, url string) error {
	svc := p.getService()
	if svc == nil {
		return fmt.Errorf("xray plugin not initialized")
	}
	return svc.AddSubscription(name, url)
}

func (p *XRayPlugin) RemoveSubscription(id string) error {
	svc := p.getService()
	if svc == nil {
		return fmt.Errorf("xray plugin not initialized")
	}
	return svc.RemoveSubscription(id)
}

func (p *XRayPlugin) TestNode(ctx context.Context, nodeName string) (json.RawMessage, error) {
	svc := p.getService()
	if svc == nil {
		return nil, fmt.Errorf("xray plugin not initialized")
	}
	result := testSingleNode(ctx, svc, nodeName)
	data, err := json.Marshal(result)
	return data, err
}

func (p *XRayPlugin) TestNodeTCP(ctx context.Context, nodeName string) (json.RawMessage, error) {
	svc := p.getService()
	if svc == nil {
		return nil, fmt.Errorf("xray plugin not initialized")
	}
	result := testSingleNodeTCP(ctx, svc, nodeName)
	data, err := json.Marshal(result)
	return data, err
}

func (p *XRayPlugin) Start() error {
	svc := p.getService()
	if svc == nil {
		return fmt.Errorf("xray plugin not initialized")
	}
	return svc.Start()
}

func (p *XRayPlugin) Stop() error {
	svc := p.getService()
	if svc == nil {
		return fmt.Errorf("xray plugin not initialized")
	}
	return svc.Stop()
}

func (p *XRayPlugin) ToggleSubscription(id string) error {
	svc := p.getService()
	if svc == nil {
		return fmt.Errorf("xray plugin not initialized")
	}
	return svc.ToggleSubscription(id)
}

func (p *XRayPlugin) RefreshSingleSubscription(ctx context.Context, id string) (json.RawMessage, error) {
	svc := p.getService()
	if svc == nil {
		return nil, fmt.Errorf("xray plugin not initialized")
	}
	result, err := svc.RefreshSingleSubscription(ctx, id)
	if err != nil {
		return nil, err
	}
	data, err := json.Marshal(result)
	return data, err
}

func (p *XRayPlugin) ActivateSubscription(id string) error {
	svc := p.getService()
	if svc == nil {
		return fmt.Errorf("xray plugin not initialized")
	}
	return svc.ActivateSubscription(id)
}

func (p *XRayPlugin) SetActiveSubscription(id string) error {
	svc := p.getService()
	if svc == nil {
		return fmt.Errorf("xray plugin not initialized")
	}
	return svc.SetActiveSubscription(id)
}

func (p *XRayPlugin) SetDialerProxySubscription(id string) error {
	svc := p.getService()
	if svc == nil {
		return fmt.Errorf("xray plugin not initialized")
	}
	return svc.SetDialerProxySubscription(id)
}

func (p *XRayPlugin) UpdateSubscriptionSelectedNode(id, nodeName string) error {
	svc := p.getService()
	if svc == nil {
		return fmt.Errorf("xray plugin not initialized")
	}
	return svc.UpdateSubscriptionSelectedNode(id, nodeName)
}

func (p *XRayPlugin) UpdateSubscription(id, name, url string) error {
	svc := p.getService()
	if svc == nil {
		return fmt.Errorf("xray plugin not initialized")
	}
	return svc.UpdateSubscription(id, name, url)
}

func (p *XRayPlugin) GetNodeConfig(nodeName string) (string, error) {
	svc := p.getService()
	if svc == nil {
		return "", fmt.Errorf("xray plugin not initialized")
	}
	return svc.GetNodeConfig(nodeName)
}

func (p *XRayPlugin) AddNodesToSubscription(id, content string) (int, error) {
	svc := p.getService()
	if svc == nil {
		return 0, fmt.Errorf("xray plugin not initialized")
	}
	return svc.AddNodesToSubscription(id, content)
}

func (p *XRayPlugin) RemoveNodeFromSubscription(id, nodeName string) error {
	svc := p.getService()
	if svc == nil {
		return fmt.Errorf("xray plugin not initialized")
	}
	return svc.RemoveNodeFromSubscription(id, nodeName)
}

func (p *XRayPlugin) ParseNodesForSubscription(id, content string) (json.RawMessage, error) {
	svc := p.getService()
	if svc == nil {
		return nil, fmt.Errorf("xray plugin not initialized")
	}
	nodes, err := svc.ParseNodesForSubscription(id, content)
	if err != nil {
		return nil, err
	}
	data, err := json.Marshal(nodes)
	return data, err
}

func (p *XRayPlugin) ReplaceSubscriptionNodes(id string, nodes json.RawMessage, selectedNode string) error {
	svc := p.getService()
	if svc == nil {
		return fmt.Errorf("xray plugin not initialized")
	}
	var list []ProxyNode
	if err := json.Unmarshal(nodes, &list); err != nil {
		return fmt.Errorf("invalid nodes payload: %w", err)
	}
	return svc.ReplaceSubscriptionNodes(id, list, selectedNode)
}
