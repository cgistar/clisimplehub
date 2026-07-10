import * as App from '../../../wailsjs/go/main/App'
import type { main } from '../../../wailsjs/go/models'
import type {
  CodexAccount,
  CodexAccountInput,
  CodexAccountsPage,
  CodexGlobalConfig,
  CodexLoginResult,

  CodexModelPrice,
  CodexTestResult,
  CodexUsage,
  CodexResetResult,
  CodexUsageResult,
  CodexUsageWindow,
  HeadlessLoginState,
  CodexSignupRequest,
  CodexVerificationCodeRequest,
  CodexVerificationCodeResult,
  SignupState
} from '@/types/codex'

const DEFAULT_BASE_URL = 'https://chatgpt.com/backend-api/codex'

function hasWailsMethod(name: string): boolean {
  return typeof window !== 'undefined' && typeof window.go?.main?.App?.[name] === 'function'
}

async function postWebCodexAccountAction<T>(path: string, accountId: string): Promise<T> {
  const response = await fetch(path, {
    method: 'POST',
    credentials: 'same-origin',
    headers: {
      'Content-Type': 'application/json'
    },
    body: JSON.stringify({ accountId })
  })
  const payload = await response.json().catch(() => ({})) as Record<string, unknown>
  if (!response.ok) {
    throw new Error(String(payload.error || response.statusText || 'request failed'))
  }
  return payload as T
}

function toUsageWindow(raw: unknown): CodexUsageWindow | undefined {
  if (!raw || typeof raw !== 'object') return undefined
  const value = raw as Record<string, unknown>
  const usedPercent = Number(value.usedPercent ?? 0)
  const remainingSeconds = Number(value.remainingSeconds ?? 0)

  return {
    usedPercent,
    remainingSeconds
  }
}

function normalizeUsage(raw: unknown): CodexUsage | undefined {
  if (!raw || typeof raw !== 'object') return undefined
  const value = raw as Record<string, unknown>

  const primary = toUsageWindow(value.primary)
  const secondary = toUsageWindow(value.secondary)
  const resetCreditsAvailableCount = Number(value.resetCreditsAvailableCount ?? 0)

  if (!primary && !secondary && resetCreditsAvailableCount <= 0) return undefined

  return {
    primary,
    secondary,
    resetCreditsAvailableCount
  }
}

function normalizeAccount(account: main.CodexAccountDTO): CodexAccount {
  return {
    ...account,
    enabled: account.enabled !== false,
    websockets: Boolean(account.websockets),
    status: account.status || 'valid',
    codexUsage: normalizeUsage(account.codexUsage)
  }
}

function toCodexDto(input: CodexAccountInput): main.CodexAccountDTO {
  return {
    refreshToken: input.refreshToken ?? '',
    enabled: input.enabled !== false,
    websockets: Boolean(input.websockets),
    status: input.status ?? 'valid',
    isActive: Boolean(input.isActive),
    ...input
  }
}

export const codexApi = {
  async getAccountsPage(offset: number, limit: number): Promise<CodexAccountsPage> {
    const page = await App.GetCodexAccountsPage(offset, limit)

    return {
      activeRefreshToken: page?.activeRefreshToken || '',
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
    await App.SetActiveCodexAccount(accountId)
  },

  async addAccount(input: CodexAccountInput): Promise<CodexAccount> {
    const account = await App.AddCodexAccount(toCodexDto(input))
    return normalizeAccount(account)
  },

  async updateAccount(input: CodexAccountInput): Promise<void> {
    await App.UpdateCodexAccount(toCodexDto(input))
  },

  async writeAccountToAuthJson(account: CodexAccount): Promise<void> {
    await App.WriteCodexAccountToAuthJSON(toCodexDto(account))
  },

  async restoreAccount(accountId: string): Promise<void> {
    await App.RestoreCodexAccount(accountId)
  },

  async deleteAccount(accountId: string): Promise<void> {
    await App.DeleteCodexAccount(accountId)
  },

  async deleteAccounts(accountIds: string[]): Promise<void> {
    await App.DeleteCodexAccounts(accountIds)
  },

  async testAccount(accountId: string): Promise<CodexTestResult> {
    return App.TestCodexAccount(accountId)
  },

  async getAccountUsage(accountId: string): Promise<CodexUsageResult> {
    return App.GetCodexAccountUsage(accountId)
  },

  async getAccountPrimaryUsage(accountId: string): Promise<CodexUsageResult> {
    if (!hasWailsMethod('GetCodexAccountPrimaryUsage')) {
      return postWebCodexAccountAction<CodexUsageResult>('/web/api/codex/usage/primary', accountId)
    }
    return App.GetCodexAccountPrimaryUsage(accountId)
  },

  async consumeAccountResetCredit(accountId: string): Promise<CodexResetResult> {
    if (!hasWailsMethod('ConsumeCodexAccountResetCredit')) {
      return postWebCodexAccountAction<CodexResetResult>('/web/api/codex/reset', accountId)
    }
    return window.go.main.App.ConsumeCodexAccountResetCredit(accountId) as Promise<CodexResetResult>
  },

  async getGlobalConfig(): Promise<CodexGlobalConfig> {
    const config = await App.GetCodexGlobalConfig()
    return {
      rotationMode: config?.rotationMode || 'fixed',
      proxyUrl: config?.proxyUrl || '',
      baseURL: config?.baseURL || DEFAULT_BASE_URL,
      clientVersion: config?.clientVersion || '',
      userAgent: config?.userAgent || '',
      originator: config?.originator || '',
      customHeaders: config?.customHeaders || {}
    }
  },

  async saveGlobalConfig(config: CodexGlobalConfig): Promise<void> {
    await App.SaveCodexGlobalConfig({
      rotationMode: config.rotationMode || 'fixed',
      proxyUrl: config.proxyUrl || '',
      baseURL: config.baseURL || '',
      clientVersion: config.clientVersion || '',
      userAgent: config.userAgent || '',
      originator: config.originator || '',
      customHeaders: config.customHeaders || {}
    })
  },

  async getModelPrices(): Promise<CodexModelPrice[]> {
    return App.GetCodexModelPrices()
  },

  async saveModelPrices(prices: CodexModelPrice[]): Promise<CodexModelPrice[]> {
    return App.SaveCodexModelPrices(prices)
  },

  async startLoginWithURL(): Promise<string> {
    return App.StartCodexLoginWithURL()
  },

  async submitLoginCallbackURL(callbackURL: string): Promise<void> {
    await App.SubmitCodexLoginCallbackURL(callbackURL)
  },

  async waitForLoginCallback(): Promise<CodexLoginResult> {
    return App.WaitForCodexLoginCallback()
  },

  async cancelLogin(): Promise<void> {
    if (window.go?.main?.App?.CancelCodexLogin) {
      await window.go.main.App.CancelCodexLogin()
    }
  },

  async openURLInIncognito(url: string): Promise<void> {
    await App.OpenURLInIncognito(url)
  },

  async startHeadlessLogin(email: string, password: string, clientId: string): Promise<HeadlessLoginState> {
    return App.StartCodexHeadlessLogin(email, password, clientId)
  },

  async startHeadlessLoginWithProvider(
    email: string,
    password: string,
    clientId: string,
    emailProvider: string,
    providerParams: Record<string, string>
  ): Promise<HeadlessLoginState> {
    return App.StartCodexHeadlessLoginWithProvider(email, password, clientId, emailProvider, providerParams)
  },

  async submitHeadlessOTP(code: string): Promise<HeadlessLoginState> {
    return App.SubmitCodexHeadlessOTP(code)
  },

  async cancelHeadlessLogin(): Promise<void> {
    if (window.go?.main?.App?.CancelCodexHeadlessLogin) {
      await window.go.main.App.CancelCodexHeadlessLogin()
    }
  },

  async startSignup(req: CodexSignupRequest): Promise<SignupState> {
    return App.StartCodexSignup(req)
  },

  async submitSignupOTP(code: string): Promise<SignupState> {
    return App.SubmitCodexSignupOTP(code)
  },

  async cancelSignup(): Promise<void> {
    if (window.go?.main?.App?.CancelCodexSignup) {
      await window.go.main.App.CancelCodexSignup()
    }
  },

  async getEmailProviders(): Promise<string[]> {
    return App.GetCodexEmailProviders()
  },

  async generateRandomEmail(provider: string, params: Record<string, string>): Promise<{
    email: string; password: string; providerState: Record<string, string>
  }> {
    return App.GenerateCodexRandomEmail(provider, params)
  },

  async fetchVerificationCode(req: CodexVerificationCodeRequest): Promise<CodexVerificationCodeResult> {
    return App.FetchCodexVerificationCode({
      emailProvider: req.emailProvider,
      providerParams: req.providerParams || {},
      email: req.email,
      timeoutSec: req.timeoutSec || 120
    })
  }
}
