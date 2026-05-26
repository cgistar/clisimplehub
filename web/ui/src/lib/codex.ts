import type { CodexAccount, CodexConfigForm, CodexEditForm } from '@/types'
import { formatRemainingSeconds } from './format'

export const DEFAULT_CODEX_CONFIG: CodexConfigForm = {
  rotationMode: 'fixed',
  proxyUrl: '',
  baseURL: 'https://chatgpt.com/backend-api/codex',
  clientVersion: '',
  userAgent: '',
  originator: '',
  customHeaders: {},
}

export function createCodexConfigForm(globalConfig: Partial<CodexConfigForm> = {}): CodexConfigForm {
  return {
    rotationMode: globalConfig.rotationMode || DEFAULT_CODEX_CONFIG.rotationMode,
    proxyUrl: globalConfig.proxyUrl || DEFAULT_CODEX_CONFIG.proxyUrl,
    baseURL: globalConfig.baseURL || DEFAULT_CODEX_CONFIG.baseURL,
    clientVersion: globalConfig.clientVersion || DEFAULT_CODEX_CONFIG.clientVersion,
    userAgent: globalConfig.userAgent || DEFAULT_CODEX_CONFIG.userAgent,
    originator: globalConfig.originator || DEFAULT_CODEX_CONFIG.originator,
    customHeaders: normalizeCustomHeaders(globalConfig.customHeaders),
  }
}

function normalizeCustomHeaders(headers: Partial<Record<string, string>> | undefined): Record<string, string> {
  const normalized: Record<string, string> = {}
  Object.entries(headers || {}).forEach(([key, value]) => {
    const name = String(key || '').trim()
    const headerValue = String(value || '').trim()
    if (name && headerValue) normalized[name] = headerValue
  })
  return normalized
}

export function createCodexEditForm(account: CodexAccount | null = null): CodexEditForm {
  return {
    id: account?.id || '',
    accountId: account?.accountId || '',
    refreshToken: account?.refreshToken || '',
    password: account?.password || '',
    mfaCode: account?.mfaCode || '',
    proxyUrl: account?.proxyUrl || '',
    weight: Number(account?.weight || 0),
    enabled: account?.enabled !== false,
    websockets: Boolean(account?.websockets),
  }
}

type CodexAccountCopyValue = string | number

export function buildCodexAccountCopyData(account: CodexAccount): Record<string, CodexAccountCopyValue> {
  const copyData: Record<string, CodexAccountCopyValue> = {}

  if (account.refreshToken) copyData.refreshToken = account.refreshToken
  if (account.email) copyData.email = account.email
  if (account.accountId) copyData.accountId = account.accountId
  if (account.planType) copyData.planType = account.planType
  if (account.accessToken) copyData.accessToken = account.accessToken
  if (account.idToken) copyData.idToken = account.idToken
  if (account.password) copyData.password = account.password
  if (account.mfaCode) copyData.mfaCode = account.mfaCode
  if (account.expiresAt) copyData.expiresAt = account.expiresAt
  if (account.proxyUrl) copyData.proxyUrl = account.proxyUrl
  if (typeof account.weight === 'number') copyData.weight = account.weight

  return copyData
}

export function buildCodexAccountsCopyJson(accounts: CodexAccount[]): string {
  return JSON.stringify(accounts.map(buildCodexAccountCopyData), null, 2)
}

export function getCodexStatus(account: CodexAccount): { label: string; variant: string } {
  const cooldownRemaining = Number(account.cooldownRemaining || 0)
  if (cooldownRemaining > 0) {
    const reason = account.cooldownReason === 'rate_limit' ? '限流' : account.cooldownReason || '冷却'
    return {
      label: `${reason} ${formatRemainingSeconds(cooldownRemaining)}`,
      variant: 'warning',
    }
  }

  switch (account.status) {
    case 'valid':
      return { label: '正常', variant: 'success' }
    case 'banned':
      return { label: '已封禁', variant: 'danger' }
    case 'reused':
      return { label: '已弃用', variant: 'danger' }
    case 'exhausted':
      return { label: '用量耗尽', variant: 'warning' }
    case 'rate_limited':
      return { label: '限流中', variant: 'warning' }
    default:
      return { label: account.status || '未知', variant: 'muted' }
  }
}

export function getExpireInfo(account: CodexAccount): { text: string; expired: boolean } {
  if (!account.expiresAt) {
    return { text: 'Token 过期时间：-', expired: false }
  }

  const expiresAt = new Date(account.expiresAt)
  if (Number.isNaN(expiresAt.getTime())) {
    return { text: `Token 过期时间：${account.expiresAt}`, expired: false }
  }

  const now = Date.now()
  const diffMs = expiresAt.getTime() - now
  if (diffMs <= 0) {
    return { text: 'Token 已过期', expired: true }
  }

  const diffMinutes = Math.floor(diffMs / 60000)
  const diffHours = Math.floor(diffMs / 3600000)
  const diffDays = Math.floor(diffMs / 86400000)

  let remaining = `${diffMinutes}m`
  if (diffDays > 0) remaining = `${diffDays}d`
  else if (diffHours > 0) remaining = `${diffHours}h`

  return {
    text: `Token剩余 ${remaining}`,
    expired: false,
  }
}

export function getCodexPlanLabel(planType?: string): string {
  const normalized = String(planType || '').trim().toLowerCase()
  if (!normalized) return ''

  const planMap: Record<string, string> = {
    free: 'Free',
    plus: 'Plus',
    pro: 'Pro',
    team: 'Team',
    enterprise: 'Enterprise',
    business: 'Business',
  }

  if (planMap[normalized]) return planMap[normalized]
  return normalized.charAt(0).toUpperCase() + normalized.slice(1)
}

function decodeBase64Url(value: string): string {
  const base64 = value.replace(/-/g, '+').replace(/_/g, '/')
  const padded = base64.padEnd(base64.length + ((4 - (base64.length % 4)) % 4), '=')
  const binary = atob(padded)
  const bytes = Uint8Array.from(binary, (char) => char.charCodeAt(0))
  return new TextDecoder().decode(bytes)
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return Boolean(value) && typeof value === 'object' && !Array.isArray(value)
}

export function getCodexSubscriptionActiveUntil(idToken?: string): string {
  if (!idToken) return ''

  const parts = idToken.split('.')
  if (parts.length < 2 || !parts[1]) return ''

  try {
    const claims: unknown = JSON.parse(decodeBase64Url(parts[1]))
    if (!isRecord(claims)) return ''

    const authClaims = claims['https://api.openai.com/auth']
    if (!isRecord(authClaims)) return ''

    const activeUntil = authClaims.chatgpt_subscription_active_until
    return typeof activeUntil === 'string' ? activeUntil : ''
  } catch {
    return ''
  }
}
