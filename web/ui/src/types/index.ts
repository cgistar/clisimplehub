export type RouteKey = 'home' | 'codex' | 'settings'

export interface AuthSessionResponse {
  authenticated: boolean
  hasApiKey: boolean
}

export interface EndpointInfo {
  id: number
  name: string
  apiUrl: string
  apiKey?: string
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

export interface EndpointModelMapping {
  name: string
  alias: string
}

export interface EndpointImportInput {
  name: string
  apiUrl: string
  apiKey: string
  active?: boolean
  enabled?: boolean
  interfaceType: string
  providerName?: string
  model?: string
  transformer?: string
  proxyUrl?: string
  routes?: string[]
  models?: EndpointModelMapping[]
  headers?: Record<string, string>
  remark?: string
  priority?: number
}

export interface EndpointImportResponse extends ActionResponse {
  success?: number
  failed?: Array<{ index: number; error: string }>
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
  inProgressLogs?: RequestLogItem[]
}

export type StatsRange = 'today' | 'yesterday' | 'week' | 'month' | 'all'

export interface EndpointStatsSummary {
  endpointId: string
  endpointName: string
  providerName: string
  date?: string
  inputTokens: number
  outputTokens: number
  cachedCreate: number
  cachedRead: number
  reasoning: number
  total: number
  requestCount: number
}

export interface InterfaceTypeStatsSummary {
  interfaceType: string
  inputTokens: number
  outputTokens: number
  cachedCreate: number
  cachedRead: number
  reasoning: number
  total: number
  requestCount: number
  endpoints: EndpointStatsSummary[]
}

export interface HourlyStatsSummary {
  hour: number
  requestCount: number
  inputTokens: number
  outputTokens: number
  cachedCreate: number
  cachedRead: number
  reasoning: number
  total: number
}

export interface CodexUsageWindow {
  usedPercent?: number
  remainingSeconds?: number
}

export interface CodexUsage {
  primary?: CodexUsageWindow
  secondary?: CodexUsageWindow
  resetCreditsAvailableCount?: number
}

export interface CodexAccount {
  id?: string
  refreshToken?: string
  email?: string
  planType?: string
  accessToken?: string
  idToken?: string
  accountId?: string
  enabled?: boolean
  websockets?: boolean
  status?: string
  weight?: number
  proxyUrl?: string
  password?: string
  mfaCode?: string
  isActive?: boolean
  todayRequests?: number
  todayTotalTokens?: number
  todayCachedTokens?: number
  todayReasoningTokens?: number
  todayEstimatedCost?: number | null
  expiresAt?: string
  cooldownUntil?: string
  cooldownReason?: string
  cooldownRemaining?: number
  codexUsage?: CodexUsage
}

export interface CodexModelPrice {
  model: string
  inputPer1M: number
  cachedInputPer1M: number
  cacheWritePer1M: number
  outputPer1M: number
  updatedAt?: string
}

export interface CodexPageData {
  available: boolean
  message?: string
  configPath?: string
  activeAccountId?: string
  accounts?: CodexAccount[]
  globalConfig?: Partial<CodexConfigForm>
}

export interface CodexAccountInput {
  id?: string
  refreshToken?: string
  email?: string
  planType?: string
  accessToken?: string
  idToken?: string
  accountId?: string
  enabled?: boolean
  websockets?: boolean
  status?: string
  weight?: number
  proxyUrl?: string
  password?: string
  mfaCode?: string
  isActive?: boolean
  expiresAt?: string
}

export interface CodexConfigForm {
  rotationMode: string
  proxyUrl: string
  baseURL: string
  clientVersion: string
  userAgent: string
  originator: string
  customHeaders: Record<string, string>
}

export interface CodexEditForm {
  id: string
  accountId: string
  refreshToken: string
  password: string
  mfaCode: string
  proxyUrl: string
  weight: number
  enabled: boolean
  websockets: boolean
  status?: string
  cooldownRemaining?: number
}

export interface SettingsData {
  port?: number
  apiKey?: string
  fallback?: boolean
  debugMode?: string
  listenAddr?: string
  proxyUrl?: string
  clashPath?: string
  dbDriver?: string
  dbSource?: string
  configPath?: string
  disableImageGeneration?: string
}

export interface SettingsForm {
  port: number | string
  apiKey: string
  fallback: boolean
  debugMode: string
  listenAddr: string
  proxyUrl: string
  clashPath: string
  dbSource: string
  disableImageGeneration: string
}

export interface DatabaseTestResult {
  message?: string
  dbDriver?: string
  dbSource?: string
}

export interface PathPickerEntry {
  name: string
  path: string
  isDir: boolean
  isFile: boolean
  executable: boolean
}

export interface PathPickerData {
  currentPath: string
  parentPath?: string
  homePath?: string
  separator: string
  roots?: string[]
  entries: PathPickerEntry[]
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
