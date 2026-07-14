export type XaiAccountStatus = 'valid' | 'banned' | 'exhausted' | 'unknown' | string

export type XaiAuthKind = 'oauth' | 'api_key' | string

export type XaiPoolType = 'basic' | 'super' | 'heavy' | string

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
  authKind?: XaiAuthKind
  apiKey?: string
  /** grok.com / accounts.x.ai 的 sso Cookie JWT */
  sso?: string
  enabled?: boolean
  websockets?: boolean
  /** true=官方 API；false=cli-chat-proxy（OAuth 默认 false） */
  usingApi?: boolean
  /** basic / super / heavy（rate-limits 推断） */
  pool?: XaiPoolType
  quota?: XaiQuota
  lastQuotaSync?: string
  status?: XaiAccountStatus
  weight?: number
  proxyUrl?: string
  customHeaders?: Record<string, string>
  expiresAt?: string
  lastRefresh?: string
  cooldownUntil?: string
  cooldownReason?: string
  cooldownRemaining?: number
  createdAt?: string
  updatedAt?: string
  isActive?: boolean
}

export type XaiAccountInput = Partial<XaiAccount>

export interface XaiGlobalConfig {
  rotationMode: 'fixed' | 'failover' | 'loadbalance' | string
  proxyUrl: string
  baseURL: string
  clientVersion?: string
  userAgent?: string
  tokenAuth?: string
  clientSurface?: string
  /** 默认 true：动态生成 x-statsig-id（grok.com rate-limits 等） */
  dynamicStatsig?: boolean
  /** 默认 false：后台刷新临近过期的 OAuth token */
  autoRefreshToken?: boolean
  customHeaders?: Record<string, string>
}

export interface XaiAccountsPage {
  activeAccountId: string
  accounts: XaiAccount[]
  offset: number
  limit: number
  nextOffset: number
  total: number
  hasMore: boolean
}

export interface XaiLoginResult {
  accessToken?: string
  refreshToken?: string
  idToken?: string
  email?: string
  subject?: string
  expiresAt?: string
  lastRefresh?: string
}

export interface XaiDeviceLoginInfo {
  deviceCode?: string
  userCode: string
  verificationUri?: string
  verificationUriComplete?: string
  expiresIn?: number
  interval?: number
}

export interface XaiTestResult {
  success: boolean
  account?: XaiAccount
  error?: string
  warning?: string
}

export interface XaiSSOImportResult {
  success: boolean
  action: 'created' | 'updated' | string
  account?: XaiAccount
  error?: string
  warning?: string
}
