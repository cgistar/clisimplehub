import * as App from '../../../wailsjs/go/main/App'
import type { main } from '../../../wailsjs/go/models'
import type {
  KiroAccount,
  KiroAccountInput,
  KiroAccountsResponse,
  KiroConfig,
  KiroGlobalConfig,
  KiroTestResult,
  KiroUsageInput,
  IdcRegisterRequest,
  IdcRegisterResponse,
  IdcDeviceAuthRequest,
  IdcDeviceAuthResponse,
  IdcPollTokenRequest,
  IdcPollTokenResponse,
  IdcAuthCodeRegisterRequest,
  IdcAuthCodeRegisterResponse,
  IdcAuthCodeTokenRequest,
  IdcAuthCodeTokenResponse,
  KiroUsageResult
} from '@/types/kiro'

function normalizeAccount(account: main.KiroAccountDTO): KiroAccount {
  return {
    ...account,
    status: account.status || 'active'
  }
}

function toKiroDto(input: KiroAccountInput): main.KiroAccountDTO {
  return {
    refreshToken: input.refreshToken,
    status: input.status ?? 'active',
    balance: input.balance ?? 0,
    isActive: Boolean(input.isActive),
    ...input
  }
}

export const kiroApi = {
  async getAccounts(): Promise<KiroAccountsResponse> {
    const result = await App.GetKiroAccounts()
    return {
      activeRefreshToken: result?.activeRefreshToken || '',
      accounts: (result?.accounts || []).map(normalizeAccount)
    }
  },

  async setActiveAccount(refreshToken: string): Promise<void> {
    await App.SetActiveKiroAccount(refreshToken)
  },

  async addAccount(input: KiroAccountInput): Promise<void> {
    await App.AddKiroAccount(toKiroDto(input))
  },

  async updateAccount(refreshToken: string, input: KiroAccountInput): Promise<void> {
    await App.UpdateKiroAccount(
      toKiroDto({
        ...input,
        refreshToken
      })
    )
  },

  async deleteAccount(refreshToken: string): Promise<void> {
    await App.DeleteKiroAccount(refreshToken)
  },

  async deleteAccounts(refreshTokens: string[]): Promise<void> {
    await App.DeleteKiroAccounts(refreshTokens)
  },

  async testAccount(refreshToken: string): Promise<KiroTestResult> {
    return App.TestKiroAccount(refreshToken)
  },

  async getAccountUsage(refreshToken: string): Promise<KiroUsageResult> {
    return App.GetKiroAccountUsage(refreshToken)
  },

  async getConfig(): Promise<KiroConfig> {
    return App.GetKiroConfig()
  },

  async saveConfig(config: KiroConfig): Promise<void> {
    await App.SaveKiroConfig(config)
  },

  async testRefreshToken(config: KiroConfig): Promise<KiroTestResult> {
    return App.TestKiroRefreshToken(config)
  },

  async getUsage(input: KiroUsageInput): Promise<KiroUsageResult> {
    return App.GetKiroUsage(input)
  },

  async getGlobalConfig(): Promise<KiroGlobalConfig> {
    return App.GetKiroGlobalConfig()
  },

  async saveGlobalConfig(config: KiroGlobalConfig): Promise<void> {
    await App.SaveKiroGlobalConfig(config)
  },

  async getDefaultModelMapping(): Promise<Record<string, string>> {
    return App.GetKiroDefaultModelMapping()
  },

  async registerIdcClient(input: IdcRegisterRequest): Promise<IdcRegisterResponse> {
    return App.RegisterIdcClient(input)
  },

  async startDeviceAuthorization(input: IdcDeviceAuthRequest): Promise<IdcDeviceAuthResponse> {
    return App.StartDeviceAuthorization(input)
  },

  async pollIdcToken(input: IdcPollTokenRequest): Promise<IdcPollTokenResponse> {
    return App.PollIdcToken(input)
  },

  async registerIdcAuthCodeClient(input: IdcAuthCodeRegisterRequest): Promise<IdcAuthCodeRegisterResponse> {
    return App.RegisterIdcAuthCodeClient(input)
  },

  async exchangeIdcAuthCode(input: IdcAuthCodeTokenRequest): Promise<IdcAuthCodeTokenResponse> {
    return App.ExchangeIdcAuthCode(input)
  },

  async startKiroSignCallbackServer(): Promise<void> {
    await App.StartKiroSignCallbackServer()
  },

  async stopKiroSignCallbackServer(): Promise<void> {
    await App.StopKiroSignCallbackServer()
  },

  async startKiroSignIdcCallbackServer(): Promise<number> {
    return App.StartKiroSignIdcCallbackServer()
  },

  async stopKiroSignIdcCallbackServer(): Promise<void> {
    await App.StopKiroSignIdcCallbackServer()
  },

  async openURLInIncognito(url: string): Promise<void> {
    await App.OpenURLInIncognito(url)
  },

  async getAllEndpoints(): Promise<main.EndpointInfo[]> {
    return App.GetAllEndpoints()
  },

  async saveEndpointData(endpoint: main.EndpointInput): Promise<main.EndpointInfo> {
    return App.SaveEndpointData(endpoint)
  }
}
