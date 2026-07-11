import * as App from '../../../wailsjs/go/main/App'
import type { main } from '../../../wailsjs/go/models'
import type {
  XaiAccount,
  XaiAccountInput,
  XaiAccountsPage,
  XaiGlobalConfig,
  XaiDeviceLoginInfo,
  XaiLoginResult,
  XaiTestResult
} from '@/types/xai'

const DEFAULT_BASE_URL = 'https://api.x.ai/v1'

function normalizeAccount(account: main.XaiAccountDTO | XaiAccount | null | undefined): XaiAccount {
  if (!account) {
    return { enabled: true, status: 'valid' }
  }
  const authKind = account.authKind || (account.apiKey && !account.refreshToken ? 'api_key' : 'oauth')
  // 缺省：api_key → true；oauth → false
  const defaultUsingApi = authKind === 'api_key'
  return {
    ...account,
    enabled: account.enabled !== false,
    websockets: account.websockets !== false,
    usingApi: account.usingApi !== undefined ? Boolean(account.usingApi) : defaultUsingApi,
    status: account.status || 'valid',
    authKind,
    weight: Number(account.weight || 1)
  }
}

function toXaiDto(input: XaiAccountInput): main.XaiAccountDTO {
  const authKind = input.authKind || ''
  const defaultUsingApi = authKind === 'api_key'
  return {
    id: input.id || '',
    email: input.email || '',
    subject: input.subject || '',
    accessToken: input.accessToken || '',
    refreshToken: input.refreshToken || '',
    idToken: input.idToken || '',
    authKind,
    apiKey: input.apiKey || '',
    sso: input.sso || '',
    enabled: input.enabled !== false,
    websockets: input.websockets !== false,
    usingApi: input.usingApi !== undefined ? Boolean(input.usingApi) : defaultUsingApi,
    status: input.status || 'valid',
    weight: input.weight || 1,
    proxyUrl: input.proxyUrl || '',
    customHeaders: input.customHeaders || {},
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

  async probeAccountStream(accountId: string): Promise<XaiTestResult> {
    const result = await App.ProbeXaiAccountStream(accountId)
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

  async refreshAccountQuota(accountId: string): Promise<XaiTestResult> {
    const result = await App.RefreshXaiAccountQuota(accountId)
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
      clientVersion: config?.clientVersion || '',
      userAgent: config?.userAgent || '',
      tokenAuth: config?.tokenAuth || '',
      clientSurface: config?.clientSurface || '',
      dynamicStatsig: config?.dynamicStatsig !== false,
      customHeaders: config?.customHeaders || {}
    }
  },

  async saveGlobalConfig(config: XaiGlobalConfig): Promise<void> {
    await App.SaveXaiGlobalConfig({
      rotationMode: config.rotationMode || 'fixed',
      proxyUrl: config.proxyUrl || '',
      baseURL: config.baseURL || '',
      clientVersion: config.clientVersion || '',
      userAgent: config.userAgent || '',
      tokenAuth: config.tokenAuth || '',
      clientSurface: config.clientSurface || '',
      dynamicStatsig: config.dynamicStatsig !== false,
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

  async startDeviceLogin(): Promise<XaiDeviceLoginInfo> {
    const info = await App.StartXaiDeviceLogin()
    return {
      deviceCode: info?.deviceCode || '',
      userCode: info?.userCode || '',
      verificationUri: info?.verificationUri || '',
      verificationUriComplete: info?.verificationUriComplete || '',
      expiresIn: info?.expiresIn || 0,
      interval: info?.interval || 0
    }
  },

  async waitForDeviceLogin(): Promise<XaiLoginResult> {
    return App.WaitForXaiDeviceLogin()
  },

  async openLoginURL(url: string): Promise<void> {
    await App.OpenXaiLoginURL(url)
  },

  async openURLInIncognito(url: string): Promise<void> {
    await App.OpenURLInIncognito(url)
  }
}
