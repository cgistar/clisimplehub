package plugin

import (
	"context"
	"encoding/json"
	"sync"
)

// ClashDesktopProvider is an optional interface for plugins that provide
// Clash proxy management for the desktop GUI.
type ClashDesktopProvider interface {
	GetStatus() (json.RawMessage, error)
	GetNodes() (json.RawMessage, error)
	SelectNode(nodeName string) error
	GetConfig(configPath string) (json.RawMessage, error)
	SaveConfig(configPath string, dto json.RawMessage) error
	ReloadConfigFromDisk() error
	RefreshSubscriptions(ctx context.Context) (json.RawMessage, error)
	AddSubscription(name, url string) error
	RemoveSubscription(id string) error
	TestNode(ctx context.Context, nodeName string) (json.RawMessage, error)
	TestNodeTCP(ctx context.Context, nodeName string) (json.RawMessage, error)
	CancelSpeedTests() error
	Start() error
	Stop() error
	ToggleSubscription(id string) error
	RefreshSingleSubscription(ctx context.Context, id string) (json.RawMessage, error)
	ActivateSubscription(id string) error
	SetActiveSubscription(id string) error
	SetDialerProxySubscription(id string) error
	UpdateSubscriptionSelectedNode(id, nodeName string) error
	UpdateSubscription(id, name, url string) error
	GetNodeConfig(nodeName string) (string, error)
	AddNodesToSubscription(id, content string) (int, error)
	RemoveNodeFromSubscription(id, nodeName string) error
	ParseNodesForSubscription(id, content string) (json.RawMessage, error)
	ReplaceSubscriptionNodes(id string, nodes json.RawMessage, selectedNode string) error
}

// GetClashDesktopProvider returns the ClashDesktopProvider from the "clash" plugin, or nil.
func GetClashDesktopProvider() ClashDesktopProvider {
	p := ByName("clash")
	if p == nil {
		return nil
	}
	if vp, ok := p.(ClashDesktopProvider); ok {
		return vp
	}
	return nil
}

var (
	clashDesktopProviderOnce sync.Once
	clashDesktopProviderInst ClashDesktopProvider
)

// GetClashDesktopProviderCached returns the cached ClashDesktopProvider (resolved once).
func GetClashDesktopProviderCached() ClashDesktopProvider {
	clashDesktopProviderOnce.Do(func() {
		clashDesktopProviderInst = GetClashDesktopProvider()
	})
	return clashDesktopProviderInst
}
