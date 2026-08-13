export type RouteKey = 'home' | 'codex' | 'xai' | 'proxy' | 'settings'

export interface AuthSessionResponse {
  authenticated: boolean
  hasApiKey: boolean
  proxyAvailable?: boolean
}

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
  globalProxy?: boolean
  userYaml: string
  chain?: ClashChainConfig
  dialerProxyId?: string
  subscriptions: ClashSubscription[]
  [key: string]: unknown
}

export interface ClashPageData {
  available: boolean
  message?: string
  status?: ClashStatus
  config?: ClashConfig
  nodes?: ClashNode[]
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

export interface CodexResetCredit {
  id: string
  reset_type: string
  is_supported_by_plan: boolean
  status: string
  granted_at: string
  expires_at: string
  redeem_started_at?: string
  redeemed_at?: string
  profile_image_url?: string | null
  profile_user_id?: string | null
  title?: string | null
  description?: string | null
}

export interface CodexResetCreditsList {
  credits: CodexResetCredit[]
  available_count: number
  total_earned_count: number
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

export interface CodexImportAccountError {
  index: number
  email?: string
  reason: string
}

export interface CodexImportAccountsResult {
  success: number
  failed: number
  skipped: number
  imported?: number
  message?: string
  errors?: CodexImportAccountError[]
}

export interface CodexConfigForm {
  rotationMode: string
  proxyUrl: string
  baseURL: string
  clientVersion: string
  userAgent: string
  originator: string
  betaFeatures: string
  customHeaders: Record<string, string>
}

export interface XaiQuotaWindow {
  remaining?: number
  total?: number
  windowSeconds?: number
  resetAt?: number
  syncedAt?: number
}

export interface XaiQuota {
  auto?: XaiQuotaWindow
  fast?: XaiQuotaWindow
  expert?: XaiQuotaWindow
  heavy?: XaiQuotaWindow
  grok43?: XaiQuotaWindow
}

export interface XaiAccount {
  id?: string
  email?: string
  subject?: string
  accessToken?: string
  refreshToken?: string
  idToken?: string
  authKind?: string
  apiKey?: string
  sso?: string
  enabled?: boolean
  websockets?: boolean
  usingApi?: boolean
  pool?: string
  quota?: XaiQuota
  lastQuotaSync?: string
  status?: string
  weight?: number
  proxyUrl?: string
  expiresAt?: string
  cooldownRemaining?: number
  cooldownReason?: string
  isActive?: boolean
  createdAt?: string
  updatedAt?: string
}

export interface XaiAccountInput {
  id?: string
  email?: string
  subject?: string
  accessToken?: string
  refreshToken?: string
  idToken?: string
  authKind?: string
  apiKey?: string
  sso?: string
  enabled?: boolean
  websockets?: boolean
  usingApi?: boolean
  status?: string
  weight?: number
  proxyUrl?: string
  expiresAt?: string
}

export interface XaiSSOImportResult extends ActionResponse {
  success: boolean
  action: 'created' | 'updated' | string
  account?: XaiAccount
  warning?: string
}

export interface XaiConfigForm {
  rotationMode: string
  proxyUrl: string
  baseURL: string
  clientVersion: string
  userAgent: string
  tokenAuth: string
  clientSurface: string
  dynamicStatsig: boolean
  autoRefreshToken: boolean
  customHeaders: Record<string, string>
}

export interface XaiPageData {
  available: boolean
  message?: string
  configPath?: string
  activeAccountId?: string
  accounts?: XaiAccount[]
  globalConfig?: Partial<XaiConfigForm>
}

export interface XaiEditForm {
  id: string
  email: string
  refreshToken: string
  accessToken: string
  apiKey: string
  sso: string
  proxyUrl: string
  weight: number
  enabled: boolean
  websockets: boolean
  usingApi: boolean
  status?: string
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
  xaiConfig?: unknown
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
