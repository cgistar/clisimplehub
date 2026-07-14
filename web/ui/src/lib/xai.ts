import type { XaiAccount, XaiAccountInput, XaiConfigForm, XaiEditForm } from '@/types'

export const XAI_OIDC_ISSUER = 'https://auth.x.ai'
export const XAI_OIDC_CLIENT_ID = 'b1a00492-073a-47ea-816f-4c329264a828'
export const XAI_AUTH_JSON_KEY = `${XAI_OIDC_ISSUER}::${XAI_OIDC_CLIENT_ID}`

export const DEFAULT_XAI_CONFIG: XaiConfigForm = {
  rotationMode: 'fixed',
  proxyUrl: '',
  baseURL: 'https://api.x.ai/v1',
  clientVersion: '',
  userAgent: '',
  tokenAuth: '',
  clientSurface: '',
  dynamicStatsig: true,
  autoRefreshToken: false,
  customHeaders: {},
}

export function createXaiConfigForm(globalConfig: Partial<XaiConfigForm> = {}): XaiConfigForm {
  return {
    rotationMode: globalConfig.rotationMode || DEFAULT_XAI_CONFIG.rotationMode,
    proxyUrl: globalConfig.proxyUrl || DEFAULT_XAI_CONFIG.proxyUrl,
    baseURL: globalConfig.baseURL || DEFAULT_XAI_CONFIG.baseURL,
    clientVersion: globalConfig.clientVersion || DEFAULT_XAI_CONFIG.clientVersion,
    userAgent: globalConfig.userAgent || DEFAULT_XAI_CONFIG.userAgent,
    tokenAuth: globalConfig.tokenAuth || DEFAULT_XAI_CONFIG.tokenAuth,
    clientSurface: globalConfig.clientSurface || DEFAULT_XAI_CONFIG.clientSurface,
    dynamicStatsig: globalConfig.dynamicStatsig !== false,
    autoRefreshToken: Boolean(globalConfig.autoRefreshToken),
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

export function createXaiEditForm(account: XaiAccount | null = null): XaiEditForm {
  const authKind = account?.authKind || (account?.apiKey && !account?.refreshToken ? 'api_key' : 'oauth')
  const defaultUsingApi = authKind === 'api_key'
  return {
    id: account?.id || '',
    email: account?.email || '',
    refreshToken: account?.refreshToken || '',
    accessToken: account?.accessToken || '',
    apiKey: account?.apiKey || '',
    sso: account?.sso || '',
    proxyUrl: account?.proxyUrl || '',
    weight: Number(account?.weight || 1),
    enabled: account?.enabled !== false,
    websockets: account?.websockets !== false,
    usingApi: account?.usingApi !== undefined ? Boolean(account.usingApi) : defaultUsingApi,
    status: account?.status || 'valid',
  }
}

function decodeJwtClaims(token: string | undefined | null): Record<string, unknown> {
  const raw = String(token || '').trim()
  if (!raw) return {}
  const parts = raw.split('.')
  if (parts.length < 2) return {}
  try {
    let payload = parts[1].replace(/-/g, '+').replace(/_/g, '/')
    const pad = (4 - (payload.length % 4)) % 4
    if (pad) payload += '='.repeat(pad)
    const json = JSON.parse(atob(payload))
    return json && typeof json === 'object' ? (json as Record<string, unknown>) : {}
  } catch {
    return {}
  }
}

function claimString(claims: Record<string, unknown>, ...keys: string[]): string {
  for (const key of keys) {
    const v = claims[key]
    if (typeof v === 'string' && v.trim()) return v.trim()
    if (typeof v === 'number' && Number.isFinite(v)) return String(v)
  }
  return ''
}

function expToIso(exp: unknown): string {
  if (typeof exp === 'number' && exp > 0) return new Date(exp * 1000).toISOString()
  if (typeof exp === 'string' && exp.trim()) {
    const n = Number(exp)
    if (Number.isFinite(n) && n > 0) return new Date(n * 1000).toISOString()
    const d = new Date(exp)
    if (!Number.isNaN(d.getTime())) return d.toISOString()
  }
  return ''
}

function toIsoOrEmpty(value: string | undefined): string {
  const s = String(value || '').trim()
  if (!s) return ''
  const d = new Date(s)
  if (Number.isNaN(d.getTime())) return s
  return d.toISOString()
}

export function buildXaiAuthJson(account: XaiAccount): Record<string, unknown> {
  const accessToken = String(account.accessToken || account.apiKey || '').trim()
  const idClaims = decodeJwtClaims(account.idToken)
  const accessClaims = decodeJwtClaims(accessToken)
  const principalId =
    claimString(idClaims, 'principal_id', 'sub', 'user_id') ||
    claimString(accessClaims, 'principal_id', 'sub', 'user_id') ||
    String(account.subject || '').trim()
  const email =
    claimString(idClaims, 'email') ||
    claimString(accessClaims, 'email') ||
    String(account.email || '').trim()
  let firstName = claimString(idClaims, 'given_name', 'first_name') || claimString(accessClaims, 'given_name', 'first_name')
  let lastName = claimString(idClaims, 'family_name', 'last_name') || claimString(accessClaims, 'family_name', 'last_name')
  if (!firstName && !lastName) {
    const fullName = claimString(idClaims, 'name') || claimString(accessClaims, 'name')
    if (fullName) {
      const parts = fullName.split(/\s+/)
      firstName = parts[0] || ''
      lastName = parts.slice(1).join(' ')
    }
  }
  const entry = {
    key: accessToken,
    auth_mode: 'oidc',
    create_time: toIsoOrEmpty(account.createdAt) || expToIso(idClaims.iat) || expToIso(accessClaims.iat) || new Date().toISOString(),
    user_id: principalId,
    email,
    first_name: firstName,
    last_name: lastName,
    principal_type: claimString(idClaims, 'principal_type') || claimString(accessClaims, 'principal_type') || 'User',
    principal_id: principalId,
    team_id: claimString(idClaims, 'team_id') || claimString(accessClaims, 'team_id'),
    coding_data_retention_opt_out: false,
    refresh_token: String(account.refreshToken || '').trim(),
    expires_at: toIsoOrEmpty(account.expiresAt) || expToIso(idClaims.exp) || expToIso(accessClaims.exp),
    oidc_issuer: XAI_OIDC_ISSUER,
    oidc_client_id: XAI_OIDC_CLIENT_ID,
  }
  return { [XAI_AUTH_JSON_KEY]: entry }
}

export function buildXaiAccountCopyJson(account: XaiAccount): string {
  return JSON.stringify(buildXaiAuthJson(account), null, 2)
}

export function buildXaiAccountsCopyJson(accounts: XaiAccount[]): string {
  if (accounts.length === 1) return buildXaiAccountCopyJson(accounts[0])
  return JSON.stringify(accounts.map((a) => buildXaiAuthJson(a)), null, 2)
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value)
}

function isGrokAuthEntry(value: unknown): boolean {
  if (!isRecord(value)) return false
  const key = String(value.key || value.access_token || value.accessToken || '').trim()
  const refresh = String(value.refresh_token || value.refreshToken || '').trim()
  return Boolean(key || refresh)
}

function extractGrokEntries(raw: unknown): Record<string, unknown>[] {
  if (!raw) return []
  if (Array.isArray(raw)) {
    const out: Record<string, unknown>[] = []
    for (const item of raw) out.push(...extractGrokEntries(item))
    return out
  }
  if (!isRecord(raw)) return []
  const entries: Record<string, unknown>[] = []
  let found = false
  for (const [k, v] of Object.entries(raw)) {
    if ((k.includes('auth.x.ai') || k.includes('::')) && isGrokAuthEntry(v) && isRecord(v)) {
      entries.push(v)
      found = true
    }
  }
  if (found) return entries
  if (isGrokAuthEntry(raw) && (raw.auth_mode || raw.oidc_issuer || raw.oidc_client_id || raw.key)) {
    return [raw]
  }
  return []
}

function grokEntryToAccount(entry: Record<string, unknown>): XaiAccountInput {
  return {
    email: String(entry.email || '').trim(),
    subject: String(entry.principal_id || entry.user_id || '').trim(),
    accessToken: String(entry.key || '').trim(),
    refreshToken: String(entry.refresh_token || '').trim(),
    authKind: 'oauth',
    enabled: true,
    websockets: true,
    status: 'valid',
    expiresAt: String(entry.expires_at || '').trim(),
    weight: 1,
  }
}

export function parseXaiImportAccounts(raw: unknown): XaiAccountInput[] {
  const grok = extractGrokEntries(raw)
  if (grok.length > 0) {
    return grok.map(grokEntryToAccount).filter((a) => a.accessToken || a.refreshToken)
  }
  const list = Array.isArray(raw)
    ? raw
    : isRecord(raw) && Array.isArray(raw.accounts)
      ? raw.accounts
      : isRecord(raw)
        ? [raw]
        : []
  const out: XaiAccountInput[] = []
  for (const item of list) {
    if (!isRecord(item)) continue
    const nested = extractGrokEntries(item)
    if (nested.length > 0) {
      for (const e of nested) {
        const acc = grokEntryToAccount(e)
        if (acc.accessToken || acc.refreshToken) out.push(acc)
      }
      continue
    }
    const refreshToken = String(item.refreshToken || item.refresh_token || '').trim()
    const accessToken = String(item.accessToken || item.access_token || item.key || '').trim()
    const apiKey = String(item.apiKey || item.api_key || '').trim()
    const sso = String(item.sso || item.SSO || '').trim()
    if (!refreshToken && !accessToken && !apiKey && !sso) continue
    let subject = String(item.subject || item.sub || item.principal_id || item.user_id || '').trim()
    if (!subject && sso) {
      try {
        const parts = sso.split('.')
        if (parts.length >= 2) {
          let payload = parts[1].replace(/-/g, '+').replace(/_/g, '/')
          const pad = (4 - (payload.length % 4)) % 4
          if (pad) payload += '='.repeat(pad)
          const claims = JSON.parse(atob(payload)) as Record<string, unknown>
          if (typeof claims.session_id === 'string') subject = claims.session_id.trim()
        }
      } catch {
        // ignore
      }
    }
    out.push({
      email: String(item.email || '').trim(),
      subject,
      accessToken,
      refreshToken,
      idToken: String(item.idToken || item.id_token || '').trim(),
      apiKey,
      sso,
      authKind: String(item.authKind || item.auth_kind || (apiKey && !refreshToken ? 'api_key' : 'oauth')),
      proxyUrl: String(item.proxyUrl || item.proxy_url || '').trim(),
      weight: Number(item.weight || 1) || 1,
      enabled: item.enabled !== false,
      websockets: item.websockets !== false,
      usingApi: item.usingApi !== undefined ? Boolean(item.usingApi) : item.using_api !== undefined ? Boolean(item.using_api) : undefined,
      status: String(item.status || 'valid'),
      expiresAt: String(item.expiresAt || item.expired || item.expires_at || '').trim(),
    })
  }
  return out
}

export function getXaiStatus(account: XaiAccount): { label: string; variant: string } {
  const cooldown = Number(account.cooldownRemaining || 0)
  if (cooldown > 0) {
    if (cooldown < 60) return { label: `冷却中 ${cooldown}s`, variant: 'warning' }
    const mins = Math.ceil(cooldown / 60)
    if (mins >= 60) {
      const h = Math.floor(mins / 60)
      const m = mins % 60
      return { label: `冷却中 ${h}h${m > 0 ? m + 'm' : ''}`, variant: 'warning' }
    }
    return { label: `冷却中 ${mins}m`, variant: 'warning' }
  }
  switch (account.status) {
    case 'valid':
      return { label: '正常', variant: 'success' }
    case 'banned':
      return { label: '已封禁', variant: 'danger' }
    case 'exhausted':
      return { label: '用尽', variant: 'warning' }
    default:
      return { label: '未知', variant: 'muted' }
  }
}

/** Token 2h / Token 3d / Token 已过期 */
export function getXaiExpireInfo(account: XaiAccount, nowMs: number = Date.now()): { text: string; expired: boolean } {
  const raw = String(account.expiresAt || '').trim()
  if (!raw) return { text: '', expired: false }
  const expiresDate = new Date(raw)
  if (Number.isNaN(expiresDate.getTime())) return { text: '', expired: false }
  const diffMs = expiresDate.getTime() - nowMs
  if (diffMs <= 0) return { text: '过期', expired: true }
  const diffMinutes = Math.floor(diffMs / (1000 * 60))
  const diffHours = Math.floor(diffMs / (1000 * 60 * 60))
  const diffDays = Math.floor(diffMs / (1000 * 60 * 60 * 24))
  if (diffDays > 0) return { text: `${diffDays}d`, expired: false }
  if (diffHours > 0) return { text: `${diffHours}h`, expired: false }
  return { text: `${diffMinutes}m`, expired: false }
}
