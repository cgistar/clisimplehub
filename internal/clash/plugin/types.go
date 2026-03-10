package clashplugin

import "encoding/json"

// ProxyNode stores a Clash-compatible proxy object plus local metadata fields
// such as sourceId and latency.
type ProxyNode map[string]any

// Subscription represents a subscription source configuration.
type Subscription struct {
	ID           string      `json:"id"`
	Name         string      `json:"name"`
	URL          string      `json:"url"`
	Enabled      bool        `json:"enabled"`
	Active       bool        `json:"active"`       // one active subscription for quick selection UX
	SelectedNode string      `json:"selectedNode"` // selected node name for this subscription
	Nodes        []ProxyNode `json:"nodes"`
	Format       string      `json:"format"` // auto/uri/clash
	LastUpdated  string      `json:"lastUpdated"`
}

// NodeRef references one node by (subscriptionId, nodeName).
type NodeRef struct {
	SubscriptionID string `json:"subscriptionId"`
	NodeName       string `json:"nodeName"`
}

// ChainConfig models device -> entry -> middle(optional) -> exit -> target.
type ChainConfig struct {
	Entry  NodeRef  `json:"entry"`
	Middle *NodeRef `json:"middle,omitempty"`
	Exit   NodeRef  `json:"exit"`
}

// ClashConfig represents plugin configuration stored in clash-config.json.
type ClashConfig struct {
	SocksListen   string         `json:"socksListen"`
	SocksPort     int            `json:"socksPort"`
	LogLevel      string         `json:"logLevel"`
	GlobalProxy   bool           `json:"globalProxy"`
	UserYAML      string         `json:"userYaml"`
	Chain         ChainConfig    `json:"chain"`
	Subscriptions []Subscription `json:"subscriptions"`
}

// StatusResponse represents the clash service status.
type StatusResponse struct {
	Running      bool   `json:"running"`
	SocksAddr    string `json:"socksAddr,omitempty"`
	SelectedNode string `json:"selectedNode,omitempty"`
	NodeCount    int    `json:"nodeCount"`
}

// SpeedTestResult represents a single node speed test result.
type SpeedTestResult struct {
	NodeName string `json:"nodeName"`
	Latency  int    `json:"latency"` // ms, -1 = failed
	Error    string `json:"error,omitempty"`
}

// ClashSyncData holds the complete clash state for sync/backup.
type ClashSyncData struct {
	Config ClashConfig `json:"config"`
	Nodes  []ProxyNode `json:"nodes,omitempty"` // legacy flat node payload (backward compatibility)
}

// RefreshResult represents the result of refreshing subscriptions.
type RefreshResult struct {
	TotalNodes int      `json:"totalNodes"`
	Errors     []string `json:"errors,omitempty"`
}

// Legacy payload accepted during config import.
type legacyChainCompat struct {
	DialerProxyID string          `json:"dialerProxyId,omitempty"`
	Template      json.RawMessage `json:"template,omitempty"`
}
