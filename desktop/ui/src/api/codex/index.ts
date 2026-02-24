import * as App from '../../../wailsjs/go/main/App'
import type { main } from '../../../wailsjs/go/models'
import type {
  CodexAccount,
  CodexAccountInput,
  CodexAccountsPage,
  CodexGlobalConfig,
  CodexLoginResult,
  CodexTestResult,
  CodexUsage,
  CodexUsageResult,
  CodexUsageWindow,
  HeadlessLoginState,
  CodexSignupRequest,
  SignupState
} from '@/types/codex'

const DEFAULT_BASE_URL = 'https://chatgpt.com/backend-api/codex'
const DEFAULT_CLIENT_VERSION = '0.101.0'
const DEFAULT_USER_AGENT = 'codex_cli_rs/0.101.0 (Mac OS 26.0.1; arm64) Apple_Terminal/464'
const DEFAULT_ORIGINATOR = 'codex_cli_rs'

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

  if (!primary && !secondary) return undefined

  return {
    primary,
    secondary
  }
}

function normalizeAccount(account: main.CodexAccountDTO): CodexAccount {
  return {
    ...account,
    status: account.status || 'valid',
    codexUsage: normalizeUsage(account.codexUsage)
  }
}

function toCodexDto(input: CodexAccountInput): main.CodexAccountDTO {
  return {
    refreshToken: input.refreshToken ?? '',
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

  async deleteAccount(accountId: string): Promise<void> {
    await App.DeleteCodexAccount(accountId)
  },

  async testAccount(accountId: string): Promise<CodexTestResult> {
    return App.TestCodexAccount(accountId)
  },

  async getAccountUsage(accountId: string): Promise<CodexUsageResult> {
    return App.GetCodexAccountUsage(accountId)
  },

  async getGlobalConfig(): Promise<CodexGlobalConfig> {
    const config = await App.GetCodexGlobalConfig()
    return {
      rotationMode: config?.rotationMode || 'fixed',
      proxyUrl: config?.proxyUrl || '',
      baseURL: config?.baseURL || DEFAULT_BASE_URL,
      clientVersion: config?.clientVersion || DEFAULT_CLIENT_VERSION,
      userAgent: config?.userAgent || DEFAULT_USER_AGENT,
      originator: config?.originator || DEFAULT_ORIGINATOR
    }
  },

  async saveGlobalConfig(config: CodexGlobalConfig): Promise<void> {
    await App.SaveCodexGlobalConfig({
      rotationMode: config.rotationMode || 'fixed',
      proxyUrl: config.proxyUrl || '',
      baseURL: config.baseURL || '',
      clientVersion: config.clientVersion || '',
      userAgent: config.userAgent || '',
      originator: config.originator || ''
    })
  },

  async startLoginWithURL(): Promise<string> {
    return App.StartCodexLoginWithURL()
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
  }
}
