import type { CodexAccountInput } from '@/types'

interface BuildImportResult {
  dtos: CodexAccountInput[]
  errors: string[]
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
  return getStringField(data, ['planType']) || 'free'
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

  const account: CodexAccountInput = {
    accessToken: getStringField(data, ['access_token', 'accessToken']),
    idToken: getStringField(data, ['id_token', 'idToken']),
    accountId: getStringField(data, ['account_id', 'accountId']),
    email: getStringField(data, ['email']),
    password: getStringField(data, ['password', 'Password']),
    planType: getPlanType(data),
    expiresAt: getStringField(data, ['expired', 'expiresAt']),
  }

  const refreshToken = getStringField(data, ['refresh_token', 'refreshToken'])
  if (refreshToken) {
    account.refreshToken = refreshToken
  }

  if (!account.accessToken || !account.accountId) {
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
    const accountId = getStringField(item, ['accountId', 'account_id', 'AccountId'])
    if (!accountId) {
      errors.push(`#${index + 1}: missing accountId`)
      return
    }
    if (seen.has(accountId)) {
      errors.push(`#${index + 1}: duplicate accountId`)
      return
    }
    seen.add(accountId)

    const refreshToken = getStringField(item, ['refreshToken', 'refresh_token', 'RefreshToken'])
    const accessToken = getStringField(item, ['accessToken', 'access_token', 'AccessToken'])
    if (!refreshToken && !accessToken) {
      errors.push(`#${index + 1}: missing refreshToken/accessToken`)
      return
    }

    const dto: CodexAccountInput = {
      accountId,
      refreshToken,
      accessToken,
      idToken: getStringField(item, ['idToken', 'id_token', 'IdToken']),
      expiresAt: getStringField(item, ['expiresAt', 'expires_at', 'ExpiresAt']),
      email: getStringField(item, ['email', 'Email']),
      password: getStringField(item, ['password', 'Password']),
      planType: getStringField(item, ['planType', 'plan_type', 'PlanType']),
      proxyUrl: getStringField(item, ['proxyUrl', 'proxy_url', 'ProxyUrl']),
    }

    const weightStr = getStringField(item, ['weight', 'Weight'])
    if (weightStr) dto.weight = parseInt(weightStr, 10) || 0

    removeEmptyFields(dto)
    dtos.push(dto)
  })

  return { dtos, errors }
}
