import * as App from '../../../wailsjs/go/main/App'
import type { main } from '../../../wailsjs/go/models'
import type {
  XaiAccount,
  XaiAccountInput,
  XaiAccountsPage,
  XaiGlobalConfig,
  XaiLoginResult,
  XaiTestResult
} from '@/types/xai'

const DEFAULT_BASE_URL = 'https://api.x.ai/v1'

function normalizeAccount(account: main.XaiAccountDTO | XaiAccount | null | undefined): XaiAccount {
  if (!account) {
    return { enabled: true, status: 'valid' }
  }
  return {
    ...account,
    enabled: account.enabled !== false,
    websockets: Boolean(account.websockets),
    status: account.status || 'valid',
    authKind: account.authKind || (account.apiKey && !account.refreshToken ? 'api_key' : 'oauth'),
    weight: Number(account.weight || 1)
  }
}

function toXaiDto(input: XaiAccountInput): main.XaiAccountDTO {
  return {
    id: input.id || '',
    email: input.email || '',
    subject: input.subject || '',
    accessToken: input.accessToken || '',
    refreshToken: input.refreshToken || '',
    idToken: input.idToken || '',
    authKind: input.authKind || '',
    apiKey: input.apiKey || '',
    baseURL: input.baseURL || '',
    tokenEndpoint: input.tokenEndpoint || '',
    redirectURI: input.redirectURI || '',
    enabled: input.enabled !== false,
    websockets: Boolean(input.websockets),
    status: input.status || 'valid',
    weight: input.weight || 1,
    proxyUrl: input.proxyUrl || '',
    expiresAt: input.expiresAt || '',
    lastRefresh: input.lastRefresh || '',
    cooldownUntil: input.cooldownUntil || '',
    cooldownReason: input.cooldownReason || '',
    cooldownRemaining: input.cooldownRemaining || 0,
    createdAt: input.createdAt || '',
    updatedAt: input.updatedAt || '',
    isActive: Boolean(input.isActive)
  }
}

export const xaiApi = {
  async getAccountsPage(offset: number, limit: number): Promise<XaiAccountsPage> {
    const page = await App.GetXaiAccountsPage(offset, limit)
    return {
      activeAccountId: page?.activeAccountId || '',
      accounts: (page?.accounts || []).map(normalizeAccount),
      offset: page?.offset ?? offset,
      limit: page?.limit ?? limit,
      nextOffset: page?.nextOffset ?? offset,
      total: page?.total ?? 0,
      hasMore: Boolean(page?.hasMore)
    }
  },

  async setActiveAccount(accountId: string): Promise<void> {
    await App.SetActiveXaiAccount(accountId)
  },

  async addAccount(input: XaiAccountInput): Promise<XaiAccount> {
    const account = await App.AddXaiAccount(toXaiDto(input))
    return normalizeAccount(account)
  },

  async updateAccount(input: XaiAccountInput): Promise<void> {
    await App.UpdateXaiAccount(toXaiDto(input))
  },

  async deleteAccount(accountId: string): Promise<void> {
    await App.DeleteXaiAccount(accountId)
  },

  async deleteAccounts(accountIds: string[]): Promise<void> {
    await App.DeleteXaiAccounts(accountIds)
  },

  async testAccount(accountId: string): Promise<XaiTestResult> {
    const result = await App.TestXaiAccount(accountId)
    return {
      success: Boolean(result?.success),
      account: result?.account ? normalizeAccount(result.account) : undefined,
      error: result?.error || ''
    }
  },

  async refreshAccountToken(accountId: string): Promise<XaiTestResult> {
    const result = await App.RefreshXaiAccountToken(accountId)
    return {
      success: Boolean(result?.success),
      account: result?.account ? normalizeAccount(result.account) : undefined,
      error: result?.error || ''
    }
  },

  async getGlobalConfig(): Promise<XaiGlobalConfig> {
    const config = await App.GetXaiGlobalConfig()
    return {
      rotationMode: config?.rotationMode || 'fixed',
      proxyUrl: config?.proxyUrl || '',
      baseURL: config?.baseURL || DEFAULT_BASE_URL,
      customHeaders: config?.customHeaders || {}
    }
  },

  async saveGlobalConfig(config: XaiGlobalConfig): Promise<void> {
    await App.SaveXaiGlobalConfig({
      rotationMode: config.rotationMode || 'fixed',
      proxyUrl: config.proxyUrl || '',
      baseURL: config.baseURL || '',
      customHeaders: config.customHeaders || {}
    })
  },

  async startLoginWithURL(): Promise<string> {
    return App.StartXaiLoginWithURL()
  },

  async submitLoginCallbackURL(callbackURL: string): Promise<void> {
    await App.SubmitXaiLoginCallbackURL(callbackURL)
  },

  async waitForLoginCallback(): Promise<XaiLoginResult> {
    return App.WaitForXaiLoginCallback()
  },

  async cancelLogin(): Promise<void> {
    if (window.go?.main?.App?.CancelXaiLogin) {
      await window.go.main.App.CancelXaiLogin()
    }
  },

  async openLoginURL(url: string): Promise<void> {
    await App.OpenXaiLoginURL(url)
  },

  async openURLInIncognito(url: string): Promise<void> {
    await App.OpenURLInIncognito(url)
  }
}
