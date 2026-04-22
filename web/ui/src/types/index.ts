export type RouteKey = 'home' | 'codex' | 'settings'

export interface AuthSessionResponse {
  authenticated: boolean
  hasApiKey: boolean
}

export interface EndpointInfo {
  id: number
  name: string
  apiUrl: string
  active: boolean
  enabled: boolean
  interfaceType: string
  providerName?: string
  model?: string
  transformer?: string
  priority?: number
  todayRequests?: number
  todayErrors?: number
  todayInput?: number
  todayOutput?: number
}

export interface EndpointGroup {
  interfaceType: string
  activeEndpointId?: number
  activeEndpointName?: string
  endpoints: EndpointInfo[]
}

export interface RequestLogItem {
  id?: number | string
  timestamp?: string
  path?: string
  status?: string
  interfaceType?: string
  providerName?: string
  endpointName?: string
  model?: string
  runTime?: number
  statusCode?: number
}

export interface HomeSummary {
  endpointCount?: number
  enabledEndpointCount?: number
  interfaceTypeCount?: number
  recentLogCount?: number
}

export interface HomeServerStatus {
  running?: boolean
  port?: number
  listenAddr?: string
}

export interface HomePageData {
  configPath?: string
  serverStatus?: HomeServerStatus
  summary?: HomeSummary
  groupedEndpoints?: EndpointGroup[]
  recentLogs?: RequestLogItem[]
}

export interface CodexUsageWindow {
  usedPercent?: number
  remainingSeconds?: number
}

export interface CodexUsage {
  primary?: CodexUsageWindow
  secondary?: CodexUsageWindow
}

export interface CodexAccount {
  refreshToken?: string
  email?: string
  planType?: string
  accountId?: string
  status?: string
  weight?: number
  proxyUrl?: string
  password?: string
  mfaCode?: string
  isActive?: boolean
  todayRequests?: number
  todayTotalTokens?: number
  expiresAt?: string
  cooldownUntil?: string
  cooldownReason?: string
  cooldownRemaining?: number
  codexUsage?: CodexUsage
}

export interface CodexPageData {
  available: boolean
  message?: string
  configPath?: string
  activeAccountId?: string
  accounts?: CodexAccount[]
  globalConfig?: Partial<CodexConfigForm>
}

export interface CodexConfigForm {
  rotationMode: string
  proxyUrl: string
  baseURL: string
  clientVersion: string
  userAgent: string
  originator: string
}

export interface CodexEditForm {
  accountId: string
  refreshToken: string
  password: string
  mfaCode: string
  proxyUrl: string
  weight: number
}

export interface SettingsData {
  port?: number
  apiKey?: string
  fallback?: boolean
  debugMode?: string
  listenAddr?: string
  proxyUrl?: string
  configPath?: string
}

export interface SettingsForm {
  port: number | string
  apiKey: string
  fallback: boolean
  debugMode: string
  listenAddr: string
  proxyUrl: string
}

export interface WebDAVConfig {
  serverUrl: string
  username: string
  password: string
}

export interface WebDAVResponse {
  statusCode: number
  headers?: Record<string, string>
  body?: string
  error?: string
}

export interface WebDAVRequestPayload {
  config: WebDAVConfig
  path: string
  depth?: string
  body?: string
  headers?: Record<string, string>
  destPath?: string
}

export interface WebDAVBackupItem {
  filename: string
  displayName: string
  href?: string
  lastModified?: string
  name: string
}

export type RestoreMode = 'merge' | 'replace'

export interface BackupData {
  schemaVersion: number
  createdAt: string
  appConfig: Record<string, unknown>
  vendors: Array<Record<string, unknown>>
  endpoints: Array<Record<string, unknown>>
  kiroAuthToken?: Record<string, unknown>
  kiroMultiConfig?: unknown
  clashConfig?: unknown
  codexConfig?: unknown
}

export interface BackupDataResponse {
  filename?: string
  data?: BackupData
}

export interface ServerConfig {
  name?: string
  url: string
  apiKey?: string
}

export interface ActionResponse {
  message?: string
  [key: string]: unknown
}
