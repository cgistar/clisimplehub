import type { CodexAccountInput } from '@/types'

interface BuildImportResult {
  dtos: CodexAccountInput[]
  errors: string[]
}

interface CodexJwtFields {
  accountId: string
  email: string
  planType: string
  expiresAt: string
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null
}

function getStringField(item: Record<string, unknown>, keys: string[]): string {
  for (const key of keys) {
    const value = item[key]
    if (typeof value === 'string') {
      const trimmed = value.trim()
      if (trimmed) return trimmed
    }
  }
  return ''
}

function getPlanType(data: Record<string, unknown>): string {
  const authData = data['https://api.openai.com/auth']
  if (isRecord(authData)) {
    const fromAuth = getStringField(authData, ['chatgpt_plan_type'])
    if (fromAuth) return fromAuth
  }
  return getStringField(data, ['planType'])
}

function getStringFromRecord(item: Record<string, unknown> | undefined, keys: string[]): string {
  if (!item) return ''
  return getStringField(item, keys)
}

function decodeBase64Url(value: string): string {
  const base64 = value.replace(/-/g, '+').replace(/_/g, '/')
  const padded = base64.padEnd(base64.length + ((4 - (base64.length % 4)) % 4), '=')
  const binary = atob(padded)
  const bytes = Uint8Array.from(binary, (char) => char.charCodeAt(0))
  return new TextDecoder().decode(bytes)
}

function readJwtClaims(token: string): Record<string, unknown> | null {
  const parts = token.split('.')
  if (parts.length !== 3 || !parts[1]) return null

  try {
    const claims: unknown = JSON.parse(decodeBase64Url(parts[1]))
    return isRecord(claims) ? claims : null
  } catch {
    return null
  }
}

function getJwtExpiresAt(claims: Record<string, unknown> | null): string {
  const rawExp = claims?.exp
  const exp = typeof rawExp === 'number' ? rawExp : typeof rawExp === 'string' ? Number(rawExp) : 0
  if (!Number.isFinite(exp) || exp <= 0) return ''
  return new Date(exp * 1000).toISOString()
}

function getJwtFields(accessToken: string): CodexJwtFields {
  const claims = readJwtClaims(accessToken)
  const authData = isRecord(claims?.['https://api.openai.com/auth'])
    ? claims['https://api.openai.com/auth']
    : undefined
  const profileData = isRecord(claims?.['https://api.openai.com/profile'])
    ? claims['https://api.openai.com/profile']
    : undefined

  return {
    accountId: getStringFromRecord(authData, ['chatgpt_account_id']),
    email: getStringFromRecord(claims || undefined, ['email']) || getStringFromRecord(profileData, ['email']),
    planType: getStringFromRecord(authData, ['chatgpt_plan_type']),
    expiresAt: getJwtExpiresAt(claims),
  }
}

function removeEmptyFields(dto: CodexAccountInput): void {
  const fields = Object.entries(dto) as Array<[keyof CodexAccountInput, CodexAccountInput[keyof CodexAccountInput]]>
  for (const [key, value] of fields) {
    if (value === '' || value === undefined || value === null) {
      delete dto[key]
    }
  }
}

function parseImportItems(payload: unknown): Array<Record<string, unknown>> {
  if (Array.isArray(payload)) {
    return payload.filter(isRecord)
  }

  if (isRecord(payload)) {
    const accounts = payload.accounts
    if (Array.isArray(accounts)) return accounts.filter(isRecord)

    const accountsUpper = payload.Accounts
    if (Array.isArray(accountsUpper)) return accountsUpper.filter(isRecord)

    return [payload]
  }

  return []
}

export function parseCodexJsonFile(data: unknown): CodexAccountInput | null {
  if (!isRecord(data)) return null

  const accessToken = getStringField(data, ['access_token', 'accessToken'])
  const refreshToken = getStringField(data, ['refresh_token', 'refreshToken'])
  const jwtFields = getJwtFields(accessToken)
  const account: CodexAccountInput = {
    accessToken,
    idToken: getStringField(data, ['id_token', 'idToken']),
    accountId: getStringField(data, ['account_id', 'accountId']) || jwtFields.accountId,
    email: getStringField(data, ['email']) || jwtFields.email,
    password: getStringField(data, ['password', 'Password']),
    planType: getPlanType(data) || jwtFields.planType || 'free',
    expiresAt: getStringField(data, ['expired', 'expiresAt', 'expires_at', 'ExpiresAt']) || jwtFields.expiresAt,
  }

  if (refreshToken) {
    account.refreshToken = refreshToken
  }

  if (!account.accessToken || !account.accountId || !account.email) {
    return null
  }
  if (!account.refreshToken && !account.expiresAt) {
    return null
  }

  removeEmptyFields(account)
  return account
}

export function buildCodexImportDTOs(rawJsonText: string): BuildImportResult {
  const errors: string[] = []
  let payload: unknown

  try {
    payload = JSON.parse(rawJsonText)
  } catch (error) {
    const message = error instanceof Error ? error.message : String(error)
    return { dtos: [], errors: [`JSON parse failed: ${message}`] }
  }

  const items = parseImportItems(payload)
  const dtos: CodexAccountInput[] = []
  const seen = new Set<string>()

  items.forEach((item, index) => {
    const refreshToken = getStringField(item, ['refreshToken', 'refresh_token', 'RefreshToken'])
    const accessToken = getStringField(item, ['accessToken', 'access_token', 'AccessToken'])
    const jwtFields = getJwtFields(accessToken)
    const accountId = getStringField(item, ['accountId', 'account_id', 'AccountId']) || jwtFields.accountId
    if (!accountId) {
      errors.push(`#${index + 1}: missing accountId`)
      return
    }
    const email = getStringField(item, ['email', 'Email']) || jwtFields.email
    if (!email) {
      errors.push(`#${index + 1}: missing email`)
      return
    }
    const localKey = `${accountId.trim().toLowerCase()}\x00${email.trim().toLowerCase()}`
    if (seen.has(localKey)) {
      errors.push(`#${index + 1}: duplicate account id`)
      return
    }
    seen.add(localKey)

    if (!refreshToken && !accessToken) {
      errors.push(`#${index + 1}: missing refreshToken/accessToken`)
      return
    }
    const expiresAt = getStringField(item, ['expiresAt', 'expires_at', 'expired', 'ExpiresAt']) || jwtFields.expiresAt
    if (!refreshToken && !expiresAt) {
      errors.push(`#${index + 1}: missing expiresAt/accessToken exp`)
      return
    }

    const dto: CodexAccountInput = {
      accountId,
      refreshToken,
      accessToken,
      idToken: getStringField(item, ['idToken', 'id_token', 'IdToken']),
      expiresAt,
      email,
      password: getStringField(item, ['password', 'Password']),
      planType: getStringField(item, ['planType', 'plan_type', 'PlanType']) || jwtFields.planType,
      proxyUrl: getStringField(item, ['proxyUrl', 'proxy_url', 'ProxyUrl']),
    }

    const weightStr = getStringField(item, ['weight', 'Weight'])
    if (weightStr) dto.weight = parseInt(weightStr, 10) || 0

    removeEmptyFields(dto)
    dtos.push(dto)
  })

  return { dtos, errors }
}
