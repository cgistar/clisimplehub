import type { XaiAccount, XaiAccountInput } from '@/types/xai'

/** Grok CLI / 项目根目录 auth.json 使用的 OIDC 常量 */
export const XAI_OIDC_ISSUER = 'https://auth.x.ai'
export const XAI_OIDC_CLIENT_ID = 'b1a00492-073a-47ea-816f-4c329264a828'
export const XAI_AUTH_JSON_KEY = `${XAI_OIDC_ISSUER}::${XAI_OIDC_CLIENT_ID}`

export interface XaiGrokAuthEntry {
  key: string
  auth_mode: string
  create_time?: string
  user_id?: string
  email?: string
  first_name?: string
  last_name?: string
  principal_type?: string
  principal_id?: string
  team_id?: string
  coding_data_retention_opt_out?: boolean | string
  refresh_token?: string
  expires_at?: string
  oidc_issuer?: string
  oidc_client_id?: string
}

export type XaiGrokAuthFile = Record<string, XaiGrokAuthEntry>

function decodeJwtClaims(token: string | undefined | null): Record<string, unknown> {
  const raw = String(token || '').trim()
  if (!raw) return {}
  const parts = raw.split('.')
  if (parts.length < 2) return {}
  try {
    let payload = parts[1].replace(/-/g, '+').replace(/_/g, '/')
    const pad = (4 - (payload.length % 4)) % 4
    if (pad) payload += '='.repeat(pad)
    const json = atob(payload)
    const parsed = JSON.parse(json)
    return parsed && typeof parsed === 'object' ? (parsed as Record<string, unknown>) : {}
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
  if (typeof exp === 'number' && exp > 0) {
    return new Date(exp * 1000).toISOString()
  }
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

/** 将内部账号转为 Grok CLI auth.json 根对象（单账号） */
export function buildXaiAuthJson(account: XaiAccount): XaiGrokAuthFile {
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

  const firstName =
    claimString(idClaims, 'given_name', 'first_name') ||
    claimString(accessClaims, 'given_name', 'first_name')

  const lastName =
    claimString(idClaims, 'family_name', 'last_name') ||
    claimString(accessClaims, 'family_name', 'last_name')

  // 若无独立 name 字段，尝试拆分 name
  let resolvedFirst = firstName
  let resolvedLast = lastName
  if (!resolvedFirst && !resolvedLast) {
    const fullName = claimString(idClaims, 'name') || claimString(accessClaims, 'name')
    if (fullName) {
      const parts = fullName.split(/\s+/)
      resolvedFirst = parts[0] || ''
      resolvedLast = parts.slice(1).join(' ')
    }
  }

  const teamId =
    claimString(idClaims, 'team_id') ||
    claimString(accessClaims, 'team_id')

  const principalType =
    claimString(idClaims, 'principal_type') ||
    claimString(accessClaims, 'principal_type') ||
    'User'

  const expiresAt =
    toIsoOrEmpty(account.expiresAt) ||
    expToIso(idClaims.exp) ||
    expToIso(accessClaims.exp)

  const createTime =
    toIsoOrEmpty(account.createdAt) ||
    expToIso(idClaims.iat) ||
    expToIso(accessClaims.iat) ||
    new Date().toISOString()

  const entry: XaiGrokAuthEntry = {
    key: accessToken,
    auth_mode: 'oidc',
    create_time: createTime,
    user_id: principalId,
    email,
    first_name: resolvedFirst,
    last_name: resolvedLast,
    principal_type: principalType || 'User',
    principal_id: principalId,
    team_id: teamId,
    coding_data_retention_opt_out: false,
    refresh_token: String(account.refreshToken || '').trim(),
    expires_at: expiresAt,
    oidc_issuer: XAI_OIDC_ISSUER,
    oidc_client_id: XAI_OIDC_CLIENT_ID
  }

  return { [XAI_AUTH_JSON_KEY]: entry }
}

export function buildXaiAccountCopyJson(account: XaiAccount): string {
  return JSON.stringify(buildXaiAuthJson(account), null, 2)
}

export function buildXaiAccountsCopyJson(accounts: XaiAccount[]): string {
  if (accounts.length === 1) {
    return buildXaiAccountCopyJson(accounts[0])
  }
  return JSON.stringify(accounts.map((a) => buildXaiAuthJson(a)), null, 2)
}

/** @deprecated 兼容旧调用名 */
export function buildXaiAccountCopyData(account: XaiAccount): XaiGrokAuthFile {
  return buildXaiAuthJson(account)
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value)
}

function isGrokAuthEntry(value: unknown): value is XaiGrokAuthEntry {
  if (!isRecord(value)) return false
  const key = String(value.key || value.access_token || value.accessToken || '').trim()
  const refresh = String(value.refresh_token || value.refreshToken || '').trim()
  return Boolean(key || refresh)
}

function isGrokAuthFile(value: unknown): value is XaiGrokAuthFile {
  if (!isRecord(value)) return false
  // 典型 key：https://auth.x.ai::clientId
  for (const [k, v] of Object.entries(value)) {
    if (k.includes('auth.x.ai') || k.includes('::')) {
      if (isGrokAuthEntry(v)) return true
    }
  }
  // 单层 entry（无外层 map）
  if (isGrokAuthEntry(value) && (value.auth_mode || value.oidc_issuer || value.oidc_client_id)) {
    return true
  }
  return false
}

function grokEntryToAccount(entry: XaiGrokAuthEntry): XaiAccountInput {
  const accessToken = String(entry.key || '').trim()
  const refreshToken = String(entry.refresh_token || '').trim()
  const email = String(entry.email || '').trim()
  const subject = String(entry.principal_id || entry.user_id || '').trim()
  return {
    email,
    subject,
    accessToken,
    refreshToken,
    authKind: 'oauth',
    enabled: true,
    websockets: true,
    status: 'valid',
    expiresAt: String(entry.expires_at || '').trim(),
    weight: 1
  }
}

function extractGrokEntries(raw: unknown): XaiGrokAuthEntry[] {
  if (!raw) return []
  if (Array.isArray(raw)) {
    const out: XaiGrokAuthEntry[] = []
    for (const item of raw) {
      out.push(...extractGrokEntries(item))
    }
    return out
  }
  if (!isRecord(raw)) return []

  // 完整 auth.json map
  const entries: XaiGrokAuthEntry[] = []
  let foundMap = false
  for (const [k, v] of Object.entries(raw)) {
    if ((k.includes('auth.x.ai') || k.includes('::')) && isGrokAuthEntry(v)) {
      entries.push(v)
      foundMap = true
    }
  }
  if (foundMap) return entries

  // 单层 entry
  if (isGrokAuthEntry(raw) && (raw.auth_mode || raw.oidc_issuer || raw.oidc_client_id || raw.key)) {
    return [raw]
  }
  return []
}

function sessionIdFromSso(sso: string): string {
  const raw = String(sso || '').trim()
  const parts = raw.split('.')
  if (parts.length < 2) return ''
  try {
    let payload = parts[1].replace(/-/g, '+').replace(/_/g, '/')
    const pad = (4 - (payload.length % 4)) % 4
    if (pad) payload += '='.repeat(pad)
    const claims = JSON.parse(atob(payload)) as Record<string, unknown>
    return typeof claims.session_id === 'string' ? claims.session_id.trim() : ''
  } catch {
    return ''
  }
}

function parseInternalAccount(item: Record<string, unknown>): XaiAccountInput | null {
  const refreshToken = String(item.refreshToken || item.refresh_token || '').trim()
  const accessToken = String(item.accessToken || item.access_token || item.key || '').trim()
  const apiKey = String(item.apiKey || item.api_key || '').trim()
  const sso = String(item.sso || item.SSO || '').trim()
  if (!refreshToken && !accessToken && !apiKey && !sso) return null
  let subject = String(item.subject || item.sub || item.principal_id || item.user_id || '').trim()
  if (!subject && sso) subject = sessionIdFromSso(sso)
  return {
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
    expiresAt: String(item.expiresAt || item.expired || item.expires_at || '').trim()
  }
}

function hasCredentials(a: XaiAccountInput): boolean {
  return Boolean(a.accessToken || a.refreshToken || a.apiKey || a.sso)
}

/** 解析导入 JSON：支持内部格式 + Grok CLI auth.json + 纯 sso 对象 */
export function parseXaiImportAccounts(raw: unknown): XaiAccountInput[] {
  // 优先识别 auth.json
  if (isGrokAuthFile(raw) || (Array.isArray(raw) && raw.some(isGrokAuthFile))) {
    return extractGrokEntries(raw).map(grokEntryToAccount).filter(hasCredentials)
  }

  // 数组内嵌 auth.json
  if (Array.isArray(raw)) {
    const fromGrok = extractGrokEntries(raw)
    if (fromGrok.length > 0) {
      return fromGrok.map(grokEntryToAccount).filter(hasCredentials)
    }
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
    // 数组元素也可能是 auth.json 根
    const grok = extractGrokEntries(item)
    if (grok.length > 0) {
      for (const e of grok) {
        const acc = grokEntryToAccount(e)
        if (hasCredentials(acc)) out.push(acc)
      }
      continue
    }
    const acc = parseInternalAccount(item)
    if (acc) out.push(acc)
  }
  return out
}
