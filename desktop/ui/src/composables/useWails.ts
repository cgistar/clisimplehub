import * as App from '../../wailsjs/go/main/App'
import { codexApi, endpointApi, kiroApi } from '@/api'
import type {
  CodexAccountInput,
  CodexAccountsPage,
  CodexLoginResult,
  CodexTestResult,
  CodexUsageResult
} from '@/types/codex'
import type { KiroAccountInput, KiroAccountsResponse, KiroTestResult, KiroUsageResult } from '@/types/kiro'
import type { RequestLogDetail, RequestLogInfo } from '@/types/endpoint'

export class WailsApiError extends Error {
  method: string
  code: string
  causeRaw: unknown
  context?: Record<string, unknown>

  constructor(
    method: string,
    code: string,
    message: string,
    causeRaw: unknown,
    context?: Record<string, unknown>
  ) {
    super(message)
    this.name = 'WailsApiError'
    this.method = method
    this.code = code
    this.causeRaw = causeRaw
    this.context = context
  }
}

type WailsMethodName = keyof typeof App

function safeErrorMessage(cause: unknown): string {
  if (cause instanceof Error && cause.message) return cause.message
  return String(cause)
}

function parseJSONOrThrow<T>(method: string, raw: string): T {
  try {
    return JSON.parse(raw) as T
  } catch (cause) {
    throw new WailsApiError(
      method,
      'WAILS_RESPONSE_PARSE_FAILED',
      `[Wails:${method}] Failed to parse JSON response`,
      cause,
      { rawSnippet: raw.slice(0, 500) }
    )
  }
}

interface WailsFacade {
  call<K extends WailsMethodName>(
    method: K,
    ...args: Parameters<(typeof App)[K]>
  ): Promise<Awaited<ReturnType<(typeof App)[K]>>>

  parseJSON<T>(method: string, raw: string): T

  GetSSEURL(): Promise<string>
  GetRecentLogs(): Promise<RequestLogInfo[]>
  GetLogDetail(logId: string): Promise<RequestLogDetail>

  GetKiroAccounts(): Promise<KiroAccountsResponse>
  SetActiveKiroAccount(refreshToken: string): Promise<void>
  AddKiroAccount(input: KiroAccountInput): Promise<void>
  UpdateKiroAccount(refreshToken: string, input: KiroAccountInput): Promise<void>
  DeleteKiroAccount(refreshToken: string): Promise<void>
  TestKiroAccount(refreshToken: string): Promise<KiroTestResult>
  GetKiroAccountUsage(refreshToken: string): Promise<KiroUsageResult>

  domains: {
    codex: {
      getAccountsPage(offset: number, limit: number): Promise<CodexAccountsPage>
      setActiveAccount(accountId: string): Promise<void>
      addAccount(input: CodexAccountInput): Promise<void>
      updateAccount(input: CodexAccountInput): Promise<void>
      deleteAccount(accountId: string): Promise<void>
      testAccount(accountId: string): Promise<CodexTestResult>
      getAccountUsage(accountId: string): Promise<CodexUsageResult>
      getAccountPrimaryUsage(accountId: string): Promise<CodexUsageResult>
      startLoginWithURL(): Promise<string>
      submitLoginCallbackURL(callbackURL: string): Promise<void>
      waitForLoginCallback(): Promise<CodexLoginResult>
      cancelLogin(): Promise<void>
    }
    app: {
      openURLInIncognito(url: string): Promise<void>
    }
  }
}

let singleton: WailsFacade | null = null

export function useWails(): WailsFacade {
  if (singleton) return singleton

  const call = async <K extends WailsMethodName>(
    method: K,
    ...args: Parameters<(typeof App)[K]>
  ): Promise<Awaited<ReturnType<(typeof App)[K]>>> => {
    const fn = App[method] as (...fnArgs: Parameters<(typeof App)[K]>) => ReturnType<(typeof App)[K]>
    if (typeof fn !== 'function') {
      throw new WailsApiError(
        String(method),
        'WAILS_METHOD_UNAVAILABLE',
        `[Wails:${String(method)}] Runtime method is not available`,
        null
      )
    }

    try {
      return await fn(...args)
    } catch (cause) {
      throw new WailsApiError(
        String(method),
        'WAILS_INVOKE_FAILED',
        `[Wails:${String(method)}] ${safeErrorMessage(cause)}`,
        cause
      )
    }
  }

  singleton = {
    call,
    parseJSON: parseJSONOrThrow,

    GetSSEURL: () => endpointApi.getSSEUrl(),
    GetRecentLogs: () => endpointApi.getRecentLogs(),
    GetLogDetail: (logId: string) => endpointApi.getLogDetail(logId),

    GetKiroAccounts: () => kiroApi.getAccounts(),
    SetActiveKiroAccount: (refreshToken: string) => kiroApi.setActiveAccount(refreshToken),
    AddKiroAccount: (input: KiroAccountInput) => kiroApi.addAccount(input),
    UpdateKiroAccount: (refreshToken: string, input: KiroAccountInput) =>
      kiroApi.updateAccount(refreshToken, input),
    DeleteKiroAccount: (refreshToken: string) => kiroApi.deleteAccount(refreshToken),
    TestKiroAccount: (refreshToken: string) => kiroApi.testAccount(refreshToken),
    GetKiroAccountUsage: (refreshToken: string) => kiroApi.getAccountUsage(refreshToken),

    domains: {
      codex: {
        getAccountsPage: (offset: number, limit: number) => codexApi.getAccountsPage(offset, limit),
        setActiveAccount: (accountId: string) => codexApi.setActiveAccount(accountId),
        addAccount: async (input: CodexAccountInput) => {
          await codexApi.addAccount(input)
        },
        updateAccount: (input: CodexAccountInput) => codexApi.updateAccount(input),
        deleteAccount: (accountId: string) => codexApi.deleteAccount(accountId),
        testAccount: (accountId: string) => codexApi.testAccount(accountId),
        getAccountUsage: (accountId: string) => codexApi.getAccountUsage(accountId),
        getAccountPrimaryUsage: (accountId: string) => codexApi.getAccountPrimaryUsage(accountId),
        startLoginWithURL: () => codexApi.startLoginWithURL(),
        submitLoginCallbackURL: (callbackURL: string) => codexApi.submitLoginCallbackURL(callbackURL),
        waitForLoginCallback: () => codexApi.waitForLoginCallback(),
        cancelLogin: () => codexApi.cancelLogin()
      },
      app: {
        openURLInIncognito: (url: string) => codexApi.openURLInIncognito(url)
      }
    }
  }

  return singleton
}
