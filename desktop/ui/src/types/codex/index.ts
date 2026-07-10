import type { main } from '../../../wailsjs/go/models'

export type CodexAccountStatus =
  | 'valid'
  | 'banned'
  | 'exhausted'
  | 'reused'
  | 'rate_limited'
  | string

export interface CodexUsageWindow {
  usedPercent: number
  remainingSeconds: number
}

export interface CodexUsage {
  primary?: CodexUsageWindow
  secondary?: CodexUsageWindow
  resetCreditsAvailableCount?: number
}

export type CodexAccount = Omit<main.CodexAccountDTO, 'status' | 'codexUsage'> & {
  status: CodexAccountStatus
  codexUsage?: CodexUsage
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
  status?: CodexAccountStatus
  weight?: number
  proxyUrl?: string
  password?: string
  mfaCode?: string
  expiresAt?: string
  cooldownUntil?: string
  cooldownReason?: string
  cooldownRemaining?: number
  createdAt?: string
  updatedAt?: string
  todayRequests?: number
  todayTotalTokens?: number
  todayCachedTokens?: number
  todayReasoningTokens?: number
  isActive?: boolean
}

export interface CodexModelPrice {
  model: string
  inputPer1M: number
  cachedInputPer1M: number
  cacheWritePer1M: number
  outputPer1M: number
  updatedAt?: string
}

export interface CodexGlobalConfig {
  rotationMode: 'fixed' | 'failover' | 'loadbalance' | string
  proxyUrl: string
  baseURL: string
  clientVersion: string
  userAgent: string
  originator: string
  customHeaders?: Record<string, string>
}

export interface CodexAccountsPage {
  activeRefreshToken: string
  activeAccountId: string
  accounts: CodexAccount[]
  offset: number
  limit: number
  nextOffset: number
  total: number
  hasMore: boolean
}

export interface CodexPagination {
  offset: number
  limit: number
  nextOffset: number
  total: number
  hasMore: boolean
}

export type CodexLoginResult = main.CodexLoginResultDTO
export type CodexTestResult = main.CodexTestResult
export type CodexUsageResult = main.CodexUsageResult

export interface CodexResetCredit {
  id: string
  reset_type: string
  status: string
  granted_at: string
  expires_at: string
  redeem_started_at: string
  redeemed_at: string
  profile_image_url: string | null
  profile_user_id: string | null
  title: string | null
  description: string | null
}

export interface CodexResetResult {
  code: string
  credit: CodexResetCredit
  windows_reset: number
}

export interface HeadlessLoginState {
  state: number
  needOTP?: boolean
  result?: CodexLoginResult
  error?: string
}

export interface CodexSignupRequest {
  emailProvider: string
  providerParams: Record<string, string>
  email: string
  password: string
  clientId: string
}

export interface CodexVerificationCodeRequest {
  emailProvider: string
  providerParams: Record<string, string>
  email: string
  timeoutSec?: number
}

export interface CodexVerificationCodeResult {
  code: string
}

export interface SignupState {
  state: number
  needOTP?: boolean
  password?: string
  result?: CodexLoginResult
  error?: string
}
