export type XrayLogLevel = 'debug' | 'info' | 'warning' | 'error' | 'none'

export interface XrayNode {
  name: string
  type: string
  server: string
  port: number
  sourceId: string
  latency: number
  uuid?: string
  password?: string
  alterId?: number
  cipher?: string
  flow?: string
  mode?: string
  network?: string
  path?: string
  host?: string
  security?: string
  sni?: string
  allowInsecure?: boolean
  pinnedPeerCertSha256?: string
  verifyPeerCertByName?: string
  fingerprint?: string
  publicKey?: string
  shortId?: string
}

export interface XrayDraftNode extends XrayNode {
  _draftAdded?: boolean
}

export interface XraySubscription {
  id: string
  name: string
  url: string
  enabled: boolean
  active: boolean
  selectedNode: string
  nodes: XrayNode[]
  format: string
  lastUpdated: string
}

export interface XrayStatus {
  running: boolean
  socksAddr?: string
  selectedNode?: string
  nodeCount: number
}

export interface XrayConfig {
  socksListen: string
  socksPort: number
  logLevel: XrayLogLevel | string
  globalProxy: boolean
  dialerProxyId?: string
  subscriptions: XraySubscription[]
}

export interface XrayRefreshResult {
  totalNodes: number
  errors?: string[]
}

export interface XraySpeedTestResult {
  nodeName: string
  latency: number
  error?: string
}

export interface XraySubscriptionInput {
  name: string
  url: string
}
