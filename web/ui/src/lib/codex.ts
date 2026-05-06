import type { CodexAccount, CodexConfigForm, CodexEditForm } from '@/types'
import { formatRemainingSeconds } from './format'

export const DEFAULT_CODEX_CONFIG: CodexConfigForm = {
  rotationMode: 'fixed',
  proxyUrl: '',
  baseURL: 'https://chatgpt.com/backend-api/codex',
  clientVersion: '',
  userAgent: '',
  originator: 'codex_cli_rs',
}

export function createCodexConfigForm(globalConfig: Partial<CodexConfigForm> = {}): CodexConfigForm {
  return {
    rotationMode: globalConfig.rotationMode || DEFAULT_CODEX_CONFIG.rotationMode,
    proxyUrl: globalConfig.proxyUrl || DEFAULT_CODEX_CONFIG.proxyUrl,
    baseURL: globalConfig.baseURL || DEFAULT_CODEX_CONFIG.baseURL,
    clientVersion: globalConfig.clientVersion || DEFAULT_CODEX_CONFIG.clientVersion,
    userAgent: globalConfig.userAgent || DEFAULT_CODEX_CONFIG.userAgent,
    originator: globalConfig.originator || DEFAULT_CODEX_CONFIG.originator,
  }
}

export function createCodexEditForm(account: CodexAccount | null = null): CodexEditForm {
  return {
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
