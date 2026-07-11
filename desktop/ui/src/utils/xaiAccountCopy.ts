import type { XaiAccount } from '@/types/xai'

export function buildXaiAccountCopyData(account: XaiAccount): Record<string, unknown> {
  return {
    id: account.id,
    email: account.email,
    subject: account.subject,
    accessToken: account.accessToken,
    refreshToken: account.refreshToken,
    idToken: account.idToken,
    authKind: account.authKind,
    apiKey: account.apiKey,
    baseURL: account.baseURL,
    tokenEndpoint: account.tokenEndpoint,
    redirectURI: account.redirectURI,
    enabled: account.enabled,
    websockets: account.websockets,
    status: account.status,
    weight: account.weight,
    proxyUrl: account.proxyUrl,
    expiresAt: account.expiresAt
  }
}

export function buildXaiAccountsCopyJson(accounts: XaiAccount[]): string {
  return JSON.stringify(accounts.map(buildXaiAccountCopyData), null, 2)
}
