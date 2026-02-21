import type { main } from '../../../wailsjs/go/models'

export type KiroAccountStatus = 'active' | 'banned' | 'warning' | string

export type KiroAccount = Omit<main.KiroAccountDTO, 'status'> & {
  status: KiroAccountStatus
}

export interface KiroAccountsResponse {
  activeRefreshToken: string
  accounts: KiroAccount[]
}

export interface KiroAccountInput {
  refreshToken: string
  accessToken?: string
  profileArn?: string
  expiresAt?: string
  region?: string
  authMethod?: string
  provider?: string
  clientId?: string
  clientSecret?: string
  machineId?: string
  status?: KiroAccountStatus
  weight?: number
  subscriptionTitle?: string
  usageLimit?: number
  currentUsage?: number
  balance?: number
  usagePct?: number
  email?: string
  userId?: string
  daysUntilReset?: number
  nextDateReset?: number
  lastUsageCheck?: string
  usageBreakdownList?: number[]
  proxyUrl?: string
  userAgent?: string
  version?: string
  createdAt?: string
  updatedAt?: string
  isActive?: boolean
}

export type KiroTestResult = main.KiroTestResult
export type KiroUsageResult = main.KiroUsageResult
export type KiroConfig = main.KiroConfig
export type KiroGlobalConfig = main.KiroGlobalConfigDTO
export type KiroUsageInput = main.KiroUsageInput

export type IdcRegisterRequest = main.IdcRegisterRequest
export type IdcRegisterResponse = main.IdcRegisterResponse
export type IdcDeviceAuthRequest = main.IdcDeviceAuthRequest
export type IdcDeviceAuthResponse = main.IdcDeviceAuthResponse
export type IdcPollTokenRequest = main.IdcPollTokenRequest
export type IdcPollTokenResponse = main.IdcPollTokenResponse

export type IdcAuthCodeRegisterRequest = main.IdcAuthCodeRegisterRequest
export type IdcAuthCodeRegisterResponse = main.IdcAuthCodeRegisterResponse
export type IdcAuthCodeTokenRequest = main.IdcAuthCodeTokenRequest
export type IdcAuthCodeTokenResponse = main.IdcAuthCodeTokenResponse

export interface KiroAuthCredential {
  refreshToken: string
  accessToken: string
  region: string
  authMethod: 'social' | 'idc'
  provider?: string
  clientId?: string
  clientSecret?: string
  profileArn?: string
  expiresAt?: string
}
