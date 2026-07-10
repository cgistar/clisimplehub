import { apiFetch } from './client'
import type {
  ActionResponse,
  CodexAccount,
  CodexAccountInput,
  AuthSessionResponse,
  BackupData,
  BackupDataResponse,
  CodexConfigForm,
  CodexEditForm,
  CodexPageData,
  DatabaseTestResult,
  EndpointImportInput,
  EndpointImportResponse,
  HomePageData,
  HourlyStatsSummary,
  InterfaceTypeStatsSummary,
  PathPickerData,
  RestoreMode,
  ServerConfig,
  SettingsData,
  SettingsForm,
  StatsRange,
  WebDAVConfig,
  WebDAVRequestPayload,
  WebDAVResponse,
} from '@/types'

export const webApi = {
  getSession: () => apiFetch<AuthSessionResponse>('/web/api/auth/session'),
  login: (apiKey: string) =>
    apiFetch<ActionResponse>('/web/api/auth/login', {
      method: 'POST',
      body: JSON.stringify({ apiKey }),
    }),
  logout: () => apiFetch<ActionResponse>('/web/api/auth/logout', { method: 'POST' }),
  getHome: () => apiFetch<HomePageData>('/web/api/home'),
  getHomeStats: (range: StatsRange) =>
    apiFetch<InterfaceTypeStatsSummary[]>(`/web/api/home/stats?range=${encodeURIComponent(range)}`),
  clearHomeStats: (range: StatsRange) =>
    apiFetch<ActionResponse>(`/web/api/home/stats?range=${encodeURIComponent(range)}`, { method: 'DELETE' }),
  getTodayHourlyStats: () => apiFetch<HourlyStatsSummary[]>('/web/api/home/stats/hourly'),
  setActiveEndpoint: (interfaceType: string, endpointId: number) =>
    apiFetch<ActionResponse>('/web/api/home/endpoints/active', {
      method: 'POST',
      body: JSON.stringify({ interfaceType, endpointId }),
    }),
  deleteEndpoint: (endpointId: number) =>
    apiFetch<ActionResponse>(`/web/api/home/endpoints/${encodeURIComponent(String(endpointId))}`, { method: 'DELETE' }),
  importEndpoints: (endpoints: EndpointImportInput[]) =>
    apiFetch<EndpointImportResponse>('/web/api/home/endpoints/import', {
      method: 'POST',
      body: JSON.stringify(endpoints),
    }),
  getCodex: () => apiFetch<CodexPageData>('/web/api/codex'),
  activateCodexAccount: (accountId: string) =>
    apiFetch<ActionResponse>('/web/api/codex/active', {
      method: 'POST',
      body: JSON.stringify({ accountId }),
    }),
  refreshCodexToken: (accountId: string) =>
    apiFetch<ActionResponse>('/web/api/codex/refresh-token', {
      method: 'POST',
      body: JSON.stringify({ accountId }),
    }),
  fetchCodexUsage: (accountId: string) =>
    apiFetch<ActionResponse>('/web/api/codex/usage', {
      method: 'POST',
      body: JSON.stringify({ accountId }),
    }),
  fetchCodexPrimaryUsage: (accountId: string) =>
    apiFetch<ActionResponse>('/web/api/codex/usage/primary', {
      method: 'POST',
      body: JSON.stringify({ accountId }),
    }),
  consumeCodexResetCredit: (accountId: string) =>
    apiFetch<ActionResponse>('/web/api/codex/reset', {
      method: 'POST',
      body: JSON.stringify({ accountId }),
    }),
  addCodexAccount: (payload: CodexAccountInput) =>
    apiFetch<CodexAccount & ActionResponse>('/web/api/codex/accounts', {
      method: 'POST',
      body: JSON.stringify(payload),
    }),
  updateCodexAccount: (payload: CodexEditForm) =>
    apiFetch<ActionResponse>('/web/api/codex/accounts/update', {
      method: 'POST',
      body: JSON.stringify(payload),
    }),
  restoreCodexAccount: (accountId: string) =>
    apiFetch<ActionResponse>('/web/api/codex/accounts/restore', {
      method: 'POST',
      body: JSON.stringify({ accountId }),
    }),
  deleteCodexAccount: (accountId: string) =>
    apiFetch<ActionResponse>(`/web/api/codex/accounts/${encodeURIComponent(accountId)}`, { method: 'DELETE' }),
  saveCodexConfig: (payload: CodexConfigForm) =>
    apiFetch<ActionResponse>('/web/api/codex/config', {
      method: 'POST',
      body: JSON.stringify(payload),
    }),
  getSettings: () => apiFetch<SettingsData>('/web/api/settings'),
  saveSettings: (payload: SettingsForm) =>
    apiFetch<SettingsData & { message?: string; settings?: SettingsData; restartRequired?: boolean; reloginRequired?: boolean }>('/web/api/settings', {
      method: 'POST',
      body: JSON.stringify({
        port: Number(payload.port),
        apiKey: payload.apiKey,
        fallback: Boolean(payload.fallback),
        debugMode: payload.debugMode,
        listenAddr: payload.listenAddr,
        proxyUrl: payload.proxyUrl,
        clashPath: payload.clashPath,
        dbSource: payload.dbSource,
        disableImageGeneration: payload.disableImageGeneration,
      }),
    }),
  testDatabaseConnection: (payload: Pick<SettingsForm, 'dbSource'>) =>
    apiFetch<DatabaseTestResult>('/web/api/settings/database/test', {
      method: 'POST',
      body: JSON.stringify({
        dbSource: payload.dbSource,
      }),
    }),
  getClashPathPicker: (path?: string) =>
    apiFetch<PathPickerData>(`/web/api/settings/clash/path-picker${path ? `?path=${encodeURIComponent(path)}` : ''}`),
  getWebDAVConfig: () => apiFetch<WebDAVConfig>('/web/api/settings/webdav'),
  saveWebDAVConfig: (config: WebDAVConfig) =>
    apiFetch<ActionResponse & { config?: WebDAVConfig }>('/web/api/settings/webdav', {
      method: 'POST',
      body: JSON.stringify(config),
    }),
  testWebDAVConnection: (config: WebDAVConfig) =>
    apiFetch<WebDAVResponse>('/web/api/settings/webdav/test', {
      method: 'POST',
      body: JSON.stringify(config),
    }),
  webdavList: (payload: WebDAVRequestPayload) =>
    apiFetch<WebDAVResponse>('/web/api/settings/webdav/list', {
      method: 'POST',
      body: JSON.stringify(payload),
    }),
  webdavGet: (payload: WebDAVRequestPayload) =>
    apiFetch<WebDAVResponse>('/web/api/settings/webdav/get', {
      method: 'POST',
      body: JSON.stringify(payload),
    }),
  webdavPut: (payload: WebDAVRequestPayload) =>
    apiFetch<WebDAVResponse>('/web/api/settings/webdav/put', {
      method: 'POST',
      body: JSON.stringify(payload),
    }),
  webdavDelete: (payload: WebDAVRequestPayload) =>
    apiFetch<WebDAVResponse>('/web/api/settings/webdav/delete', {
      method: 'POST',
      body: JSON.stringify(payload),
    }),
  webdavMkcol: (payload: WebDAVRequestPayload) =>
    apiFetch<WebDAVResponse>('/web/api/settings/webdav/mkcol', {
      method: 'POST',
      body: JSON.stringify(payload),
    }),
  createBackupData: () =>
    apiFetch<BackupDataResponse>('/web/api/settings/backup/create', {
      method: 'POST',
    }),
  restoreBackupData: (data: BackupData, mode: RestoreMode) =>
    apiFetch<ActionResponse>('/web/api/settings/backup/restore', {
      method: 'POST',
      body: JSON.stringify({ data, mode }),
    }),
  getServers: () => apiFetch<ServerConfig[]>('/web/api/settings/servers'),
  saveServers: (servers: ServerConfig[]) =>
    apiFetch<ActionResponse & { servers?: ServerConfig[] }>('/web/api/settings/servers', {
      method: 'POST',
      body: JSON.stringify(servers),
    }),
  testServerConnection: (url: string, apiKey: string) =>
    apiFetch<ActionResponse>('/web/api/settings/servers/test', {
      method: 'POST',
      body: JSON.stringify({ url, apiKey }),
    }),
  syncConfigToServer: (index: number) =>
    apiFetch<ActionResponse>('/web/api/settings/servers/sync', {
      method: 'POST',
      body: JSON.stringify({ index }),
    }),
  buildSyncConfigCurl: (index: number) =>
    apiFetch<{ command: string }>('/web/api/settings/servers/curl', {
      method: 'POST',
      body: JSON.stringify({ index }),
    }),
}
