package clashplugin

import "encoding/json"

// ProxyNode represents a unified proxy node model parsed from subscriptions.
type ProxyNode struct {
	Name     string `json:"name"`
	Type     string `json:"type"` // vmess/vless/trojan/ss
	Server   string `json:"server"`
	Port     int    `json:"port"`
	SourceID string `json:"sourceId"` // subscription source ID
	Latency  int    `json:"latency"`  // speed test result (ms), 0=untested

	// Protocol-specific fields
	UUID     string `json:"uuid,omitempty"`
	Password string `json:"password,omitempty"`
	AlterId  int    `json:"alterId,omitempty"`
	Cipher   string `json:"cipher,omitempty"`
	Flow     string `json:"flow,omitempty"`
	Mode     string `json:"mode,omitempty"`

	// Transport layer
	Network string `json:"network,omitempty"` // tcp/ws/grpc/httpupgrade/xhttp
	Path    string `json:"path,omitempty"`
	Host    string `json:"host,omitempty"`

	// TLS / REALITY
	Security      string `json:"security,omitempty"` // tls/reality/none
	SNI           string `json:"sni,omitempty"`
	AllowInsecure bool   `json:"allowInsecure,omitempty"`

	PinnedPeerCertSha256 string `json:"pinnedPeerCertSha256,omitempty"`
	VerifyPeerCertByName string `json:"verifyPeerCertByName,omitempty"`
	Fingerprint          string `json:"fingerprint,omitempty"`
	PublicKey            string `json:"publicKey,omitempty"`
	ShortId              string `json:"shortId,omitempty"`
	ServerName           string `json:"serverName,omitempty"`
	SpiderX              string `json:"spiderX,omitempty"`
}

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
