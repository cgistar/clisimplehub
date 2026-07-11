export type XaiAccountStatus = 'valid' | 'banned' | 'exhausted' | 'unknown' | string

export type XaiAuthKind = 'oauth' | 'api_key' | string

export interface XaiAccount {
  id?: string
  email?: string
  subject?: string
  accessToken?: string
  refreshToken?: string
  idToken?: string
  authKind?: XaiAuthKind
  apiKey?: string
  baseURL?: string
  tokenEndpoint?: string
  redirectURI?: string
  enabled?: boolean
  websockets?: boolean
  status?: XaiAccountStatus
  weight?: number
  proxyUrl?: string
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
  baseURL?: string
  redirectURI?: string
  tokenEndpoint?: string
  lastRefresh?: string
}

export interface XaiTestResult {
  success: boolean
  account?: XaiAccount
  error?: string
}
