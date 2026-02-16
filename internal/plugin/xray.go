package plugin

import (
	"context"
	"encoding/json"
	"sync"
)

// XRayDesktopProvider is an optional interface for plugins that provide
// XRay proxy management for the desktop GUI.
type XRayDesktopProvider interface {
	GetStatus() (json.RawMessage, error)
	GetNodes() (json.RawMessage, error)
	SelectNode(nodeName string) error
	GetConfig(configPath string) (json.RawMessage, error)
	SaveConfig(configPath string, dto json.RawMessage) error
	RefreshSubscriptions(ctx context.Context) (json.RawMessage, error)
	AddSubscription(name, url string) error
	RemoveSubscription(id string) error
	TestNode(ctx context.Context, nodeName string) (json.RawMessage, error)
	Start() error
	Stop() error
	ToggleSubscription(id string) error
	RefreshSingleSubscription(ctx context.Context, id string) (json.RawMessage, error)
	ActivateSubscription(id string) error
	SetActiveSubscription(id string) error
	UpdateSubscriptionSelectedNode(id, nodeName string) error
	UpdateSubscription(id, name, url string) error
	GetNodeConfig(nodeName string) (string, error)
	AddNodesToSubscription(id, content string) (int, error)
	RemoveNodeFromSubscription(id, nodeName string) error
	ParseNodesForSubscription(id, content string) (json.RawMessage, error)
	ReplaceSubscriptionNodes(id string, nodes json.RawMessage, selectedNode string) error
}

// GetXRayDesktopProvider returns the XRayDesktopProvider from the "xray" plugin, or nil.
func GetXRayDesktopProvider() XRayDesktopProvider {
	p := ByName("xray")
	if p == nil {
		return nil
	}
	if vp, ok := p.(XRayDesktopProvider); ok {
		return vp
	}
	return nil
}

var (
	xrayDesktopProviderOnce sync.Once
	xrayDesktopProviderInst XRayDesktopProvider
)

// GetXRayDesktopProviderCached returns the cached XRayDesktopProvider (resolved once).
func GetXRayDesktopProviderCached() XRayDesktopProvider {
	xrayDesktopProviderOnce.Do(func() {
		xrayDesktopProviderInst = GetXRayDesktopProvider()
	})
	return xrayDesktopProviderInst
}
