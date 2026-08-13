import { apiFetch } from './client'
import type {
  ActionResponse,
  CodexAccount,
  CodexAccountInput,
  CodexImportAccountsResult,
  CodexResetCreditsList,
  AuthSessionResponse,
  BackupData,
  BackupDataResponse,
  CodexConfigForm,
  CodexEditForm,
  CodexModelPrice,
  CodexPageData,
  ClashConfig,
  ClashNode,
  ClashPageData,
  ClashRefreshResult,
  ClashSpeedTestResult,
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
  XaiAccount,
  XaiAccountInput,
  XaiConfigForm,
  XaiEditForm,
  XaiPageData,
  XaiSSOImportResult,
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
  getXai: () => apiFetch<XaiPageData>('/web/api/xai'),
  getClash: () => apiFetch<ClashPageData>('/web/api/clash'),
  startClash: () => apiFetch<ActionResponse>('/web/api/clash/start', { method: 'POST' }),
  stopClash: () => apiFetch<ActionResponse>('/web/api/clash/stop', { method: 'POST' }),
  reloadClash: () => apiFetch<ActionResponse>('/web/api/clash/reload', { method: 'POST' }),
  saveClashConfig: (config: ClashConfig) => apiFetch<ActionResponse>('/web/api/clash/config', { method: 'PUT', body: JSON.stringify(config) }),
  addClashSubscription: (name: string, url: string) => apiFetch<ActionResponse>('/web/api/clash/subscriptions', { method: 'POST', body: JSON.stringify({ name, url }) }),
  updateClashSubscription: (id: string, name: string, url: string) => apiFetch<ActionResponse>(`/web/api/clash/subscriptions/${encodeURIComponent(id)}`, { method: 'PUT', body: JSON.stringify({ name, url }) }),
  removeClashSubscription: (id: string) => apiFetch<ActionResponse>(`/web/api/clash/subscriptions/${encodeURIComponent(id)}`, { method: 'DELETE' }),
  toggleClashSubscription: (id: string) => apiFetch<ActionResponse>(`/web/api/clash/subscriptions/${encodeURIComponent(id)}/toggle`, { method: 'POST' }),
  setActiveClashSubscription: (id: string) => apiFetch<ActionResponse>(`/web/api/clash/subscriptions/${encodeURIComponent(id)}/active`, { method: 'POST' }),
  refreshClashSubscription: (id: string) => apiFetch<ClashRefreshResult>(`/web/api/clash/subscriptions/${encodeURIComponent(id)}/refresh`, { method: 'POST' }),
  setClashChain: (subscriptionId: string) => apiFetch<ActionResponse>('/web/api/clash/chain', { method: 'POST', body: JSON.stringify({ subscriptionId }) }),
  parseClashNodes: (id: string, content: string) => apiFetch<ClashNode[]>(`/web/api/clash/subscriptions/${encodeURIComponent(id)}/nodes/parse`, { method: 'POST', body: JSON.stringify({ content }) }),
  replaceClashNodes: (id: string, nodes: ClashNode[], selectedNode: string) => apiFetch<ActionResponse>(`/web/api/clash/subscriptions/${encodeURIComponent(id)}/nodes`, { method: 'PUT', body: JSON.stringify({ nodes, selectedNode }) }),
  getClashNodeConfig: (nodeName: string) => apiFetch<{ config: string }>(`/web/api/clash/nodes/config?nodeName=${encodeURIComponent(nodeName)}`),
  testClashNode: (nodeName: string, mode: 'http' | 'tcp') => apiFetch<ClashSpeedTestResult>('/web/api/clash/nodes/test', { method: 'POST', body: JSON.stringify({ nodeName, mode }) }),
  cancelClashTests: () => apiFetch<ActionResponse>('/web/api/clash/nodes/tests/cancel', { method: 'POST' }),
  activateXaiAccount: (accountId: string) =>
    apiFetch<ActionResponse>('/web/api/xai/active', {
      method: 'POST',
      body: JSON.stringify({ accountId }),
    }),
  refreshXaiToken: (accountId: string) =>
    apiFetch<ActionResponse>('/web/api/xai/refresh-token', {
      method: 'POST',
      body: JSON.stringify({ accountId }),
    }),
  probeXaiStream: (accountId: string) =>
    apiFetch<ActionResponse>('/web/api/xai/probe-stream', {
      method: 'POST',
      body: JSON.stringify({ accountId }),
    }),
  refreshXaiQuota: (accountId: string) =>
    apiFetch<ActionResponse & { account?: XaiAccount }>('/web/api/xai/refresh-quota', {
      method: 'POST',
      body: JSON.stringify({ accountId }),
    }),
  sso2authXaiAccount: (accountId: string) =>
    apiFetch<ActionResponse & { account?: XaiAccount; warning?: string }>('/web/api/xai/sso2auth', {
      method: 'POST',
      body: JSON.stringify({ accountId }),
    }),
  importXaiSSOAccount: (sso: string) =>
    apiFetch<XaiSSOImportResult>('/web/api/xai/sso-import', {
      method: 'POST',
      body: JSON.stringify({ sso }),
    }),
  addXaiAccount: (payload: XaiAccountInput) =>
    apiFetch<XaiAccount & ActionResponse>('/web/api/xai/accounts', {
      method: 'POST',
      body: JSON.stringify(payload),
    }),
  updateXaiAccount: (payload: XaiEditForm) =>
    apiFetch<ActionResponse>('/web/api/xai/accounts/update', {
      method: 'POST',
      body: JSON.stringify(payload),
    }),
  deleteXaiAccount: (accountId: string) =>
    apiFetch<ActionResponse>(`/web/api/xai/accounts/${encodeURIComponent(accountId)}`, { method: 'DELETE' }),
  saveXaiConfig: (payload: XaiConfigForm) =>
    apiFetch<ActionResponse>('/web/api/xai/config', {
      method: 'POST',
      body: JSON.stringify(payload),
    }),
  setXaiAutoRefreshToken: (enabled: boolean) =>
    apiFetch<ActionResponse & { enabled: boolean }>('/web/api/xai/auto-refresh-token', {
      method: 'POST',
      body: JSON.stringify({ enabled }),
    }),
  getCodexModelPrices: () => apiFetch<CodexModelPrice[]>('/web/api/codex/model-prices'),
  saveCodexModelPrices: (prices: CodexModelPrice[]) =>
    apiFetch<CodexModelPrice[]>('/web/api/codex/model-prices', {
      method: 'PUT',
      body: JSON.stringify(prices),
    }),
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
  consumeCodexResetCredit: (accountId: string, creditId: string) =>
    apiFetch<ActionResponse>('/web/api/codex/reset', {
      method: 'POST',
      body: JSON.stringify({ accountId, creditId }),
    }),
  listCodexResetCredits: (accountId: string) =>
    apiFetch<CodexResetCreditsList>('/web/api/codex/reset-credits', {
      method: 'POST',
      body: JSON.stringify({ accountId }),
    }),
  addCodexAccount: (payload: CodexAccountInput) =>
    apiFetch<CodexAccount & ActionResponse>('/web/api/codex/accounts', {
      method: 'POST',
      body: JSON.stringify({ ...payload, websockets: payload.websockets !== false }),
    }),
  importCodexAccounts: (accounts: CodexAccountInput[]) =>
    apiFetch<CodexImportAccountsResult>('/web/api/codex/accounts/import', {
      method: 'POST',
      body: JSON.stringify({
        accounts: accounts.map((payload) => ({ ...payload, websockets: payload.websockets !== false })),
      }),
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
