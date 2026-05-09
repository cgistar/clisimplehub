import type { CodexAccount } from '@/types/codex'

type CopyValue = string | number

export function buildCodexAccountCopyData(account: CodexAccount): Record<string, CopyValue> {
  const copyData: Record<string, CopyValue> = {}

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
