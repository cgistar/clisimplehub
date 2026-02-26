package xrayplugin

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

	// TLS
	Security      string `json:"security,omitempty"` // tls/reality/none
	SNI           string `json:"sni,omitempty"`
	AllowInsecure bool   `json:"allowInsecure,omitempty"`
	// Comma-separated SHA256 cert fingerprints (Xray: pinnedPeerCertSha256 / pcs).
	PinnedPeerCertSha256 string `json:"pinnedPeerCertSha256,omitempty"`
	// Comma-separated peer names (Xray: verifyPeerCertByName / vcn).
	VerifyPeerCertByName string `json:"verifyPeerCertByName,omitempty"`
	Fingerprint          string `json:"fingerprint,omitempty"`
	PublicKey            string `json:"publicKey,omitempty"`
	ShortId              string `json:"shortId,omitempty"`
}

// Subscription represents a subscription source configuration.
type Subscription struct {
	ID           string      `json:"id"`
	Name         string      `json:"name"`
	URL          string      `json:"url"`
	Enabled      bool        `json:"enabled"`
	Active       bool        `json:"active"`       // only one subscription can be active
	SelectedNode string      `json:"selectedNode"` // selected node name for this subscription
	Nodes        []ProxyNode `json:"nodes"`        // parsed nodes from this subscription
	Format       string      `json:"format"`       // auto/uri/clash
	LastUpdated  string      `json:"lastUpdated"`
}

// XRayConfig represents the plugin configuration stored in xray-config.json.
type XRayConfig struct {
	SocksListen   string          `json:"socksListen"`
	SocksPort     int             `json:"socksPort"`
	LogLevel      string          `json:"logLevel"`
	GlobalProxy   bool            `json:"globalProxy"`
	Template      json.RawMessage `json:"template,omitempty"`
	Subscriptions []Subscription  `json:"subscriptions"`
}

// StatusResponse represents the xray service status.
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

// XRaySyncData holds the complete XRay state for sync/backup.
type XRaySyncData struct {
	Config XRayConfig  `json:"config"`
	Nodes  []ProxyNode `json:"nodes,omitempty"` // legacy flat node payload (backward compatibility)
}

// RefreshResult represents the result of refreshing subscriptions.
type RefreshResult struct {
	TotalNodes int      `json:"totalNodes"`
	Errors     []string `json:"errors,omitempty"`
}
