export type ClashLogLevel = 'debug' | 'info' | 'warning' | 'error' | 'none'

export interface ClashNode {
  name: string
  type: string
  server: string
  port: number
  sourceId: string
  latency: number
  [key: string]: unknown
}

export interface ClashDraftNode extends ClashNode {
  _draftAdded?: boolean
}

export interface ClashSubscription {
  id: string
  name: string
  url: string
  enabled: boolean
  active: boolean
  selectedNode: string
  nodes: ClashNode[]
  format: string
  lastUpdated: string
}

export interface ClashNodeRef {
  subscriptionId: string
  nodeName: string
}

export interface ClashChainConfig {
  entry: ClashNodeRef
  middle?: ClashNodeRef
  exit: ClashNodeRef
}

export interface ClashStatus {
  running: boolean
  socksAddr?: string
  selectedNode?: string
  nodeCount: number
}

export interface ClashConfig {
  socksListen: string
  socksPort: number
  logLevel: ClashLogLevel | string
  userYaml: string
  chain?: ClashChainConfig
  dialerProxyId?: string
  subscriptions: ClashSubscription[]
}

export interface ClashRefreshResult {
  totalNodes: number
  errors?: string[]
}

export interface ClashSpeedTestResult {
  nodeName: string
  latency: number
  error?: string
}

export interface ClashSubscriptionInput {
  name: string
  url: string
}
