import * as App from '../../../wailsjs/go/main/App'
import type { main } from '../../../wailsjs/go/models'
import type {
  CLIConfigResult,
  CLIConfigDirsPayload,
  Endpoint,
  EndpointInputPayload,
  FetchModelsResult,
  LocalIPInfo,
  PingResult,
  InterfaceTypeStatsSummaryInfo,
  DatabaseApplyResult,
  DatabaseTestResult,
  ProcessCodexConfigResult,
  ProxyStatusPayload,
  RequestLogDetail,
  RequestLogInfo,
  SettingsPayload,
  TestEndpointParamsInput,
  TestEndpointResult,
  Vendor,
  VendorInput,
  BackupData,
  RestoreMode,
  ServerConfig,
  WebDAVConfig,
  WebDAVRequestPayload,
  WebDAVResponse
} from '@/types/endpoint'

function parseJsonSafe<T>(raw: string, fallback: T): T {
  try {
    return JSON.parse(raw) as T
  } catch {
    return fallback
  }
}

type EndpointConfigTarget = 'claude' | 'codex'

function isEndpointConfigTarget(value: string): value is EndpointConfigTarget {
  return value === 'claude' || value === 'codex'
}

function formatTomlString(value: string): string {
  return `"${value.replace(/\\/g, '\\\\').replace(/"/g, '\\"')}"`
}

function isCodexChatGPTAuth(auth: Record<string, unknown>): boolean {
  return typeof auth.auth_mode === 'string' && auth.auth_mode.trim().toLowerCase() === 'chatgpt'
}

function setCodexOpenAIAPIKey(auth: Record<string, unknown>, apiKey: string): void {
  if (isCodexChatGPTAuth(auth)) {
    auth.OPENAI_API_KEY = null
    return
  }
  auth.OPENAI_API_KEY = apiKey
}

function tomlLineKey(line: string): string {
  const separatorIndex = line.indexOf('=')
  if (separatorIndex < 0) return ''
  return line.slice(0, separatorIndex).trim()
}

function updateCodexLocalProviderConfig(
  configToml: string,
  apiUrl: string,
  experimentalBearerToken?: string
): string {
  const lines = configToml.split('\n')
  const newLines: string[] = []
  let inLocalProvider = false
  let localProviderFound = false
  let baseUrlUpdated = false
  let tokenUpdated = false

  const appendMissingLocalProviderFields = () => {
    if (!baseUrlUpdated) {
      newLines.push(`base_url = '${apiUrl}'`)
      baseUrlUpdated = true
    }
    if (experimentalBearerToken && !tokenUpdated) {
      newLines.push(`experimental_bearer_token = ${formatTomlString(experimentalBearerToken)}`)
      tokenUpdated = true
    }
  }

  for (const line of lines) {
    const trimmed = line.trim()
    const isSection = trimmed.startsWith('[')

    if (isSection && inLocalProvider && !trimmed.startsWith('[model_providers.shub]')) {
      appendMissingLocalProviderFields()
      inLocalProvider = false
    }

    if (trimmed.startsWith('[model_providers.shub]')) {
      inLocalProvider = true
      localProviderFound = true
    }

    if (inLocalProvider && tomlLineKey(line) === 'base_url') {
      newLines.push(`base_url = '${apiUrl}'`)
      baseUrlUpdated = true
    } else if (inLocalProvider && experimentalBearerToken && tomlLineKey(line) === 'experimental_bearer_token') {
      newLines.push(`experimental_bearer_token = ${formatTomlString(experimentalBearerToken)}`)
      tokenUpdated = true
    } else {
      newLines.push(line)
    }
  }

  if (inLocalProvider) {
    appendMissingLocalProviderFields()
  }

  if (!localProviderFound) {
    if (newLines.length > 0 && newLines[newLines.length - 1].trim() !== '') {
      newLines.push('')
    }
    newLines.push('[model_providers.shub]')
    newLines.push(`base_url = '${apiUrl}'`)
    if (experimentalBearerToken) {
      newLines.push(`experimental_bearer_token = ${formatTomlString(experimentalBearerToken)}`)
    }
  }

  return newLines.join('\n')
}

async function applyClaudeEndpointUrl(apiUrl: string, apiKey?: string): Promise<void> {
  const result = await App.GetClaudeConfig()
  if (!result.success) {
    throw new Error(result.message || 'Failed to load Claude config')
  }

  const files = result.files || []
  const settingsFile = files.find((file) => file.name === 'settings.json')
  if (!settingsFile) {
    throw new Error('settings.json not found')
  }

  let settings: Record<string, unknown>
  try {
    settings = JSON.parse(settingsFile.content) as Record<string, unknown>
  } catch {
    throw new Error('Invalid JSON in settings.json')
  }

  const envRaw = settings.env
  const env: Record<string, string> =
    envRaw && typeof envRaw === 'object' && !Array.isArray(envRaw)
      ? { ...(envRaw as Record<string, string>) }
      : {}

  env.ANTHROPIC_BASE_URL = apiUrl
  if (apiKey) {
    env.ANTHROPIC_AUTH_TOKEN = apiKey
  }

  settings.env = env
  await App.SaveClaudeConfig(JSON.stringify(settings, null, 2))
}

async function applyCodexEndpointUrl(apiUrl: string, apiKey?: string): Promise<void> {
  const result = await App.GetCodexConfig()
  if (!result.success) {
    throw new Error(result.message || 'Failed to load Codex config')
  }

  const files = result.files || []
  const configFile = files.find((file) => file.name === 'config.toml')
  const authFile = files.find((file) => file.name === 'auth.json')
  if (!configFile) {
    throw new Error('config.toml not found')
  }

  let auth: Record<string, unknown>
  try {
    auth = authFile?.content
      ? (JSON.parse(authFile.content) as Record<string, unknown>)
      : {}
  } catch {
    auth = {}
  }

  const newConfigToml = updateCodexLocalProviderConfig(
    configFile.content,
    apiUrl,
    apiKey && isCodexChatGPTAuth(auth) ? apiKey : undefined
  )

  if (apiKey) {
    setCodexOpenAIAPIKey(auth, apiKey)
  }

  await App.SaveCodexConfig(newConfigToml, JSON.stringify(auth, null, 2))
}

function toWebDAVRequestInput(
  input: WebDAVRequestPayload,
  method: 'PROPFIND' | 'GET' | 'PUT' | 'DELETE' | 'MKCOL',
  depth = '1'
): main.WebDAVRequestInput {
  return {
    config: {
      serverUrl: input.config.serverUrl,
      username: input.config.username,
      password: input.config.password
    },
    method,
    path: input.path,
    depth: input.depth || depth,
    body: input.body,
    headers: input.headers,
    destPath: input.destPath
  }
}

export const endpointApi = {
  async getSSEUrl(): Promise<string> {
    return App.GetSSEURL()
  },

  async getRecentLogs(): Promise<RequestLogInfo[]> {
    return App.GetRecentLogs()
  },

  async getLogDetail(logId: string): Promise<RequestLogDetail> {
    return App.GetLogDetail(logId)
  },

  async getByType(interfaceType: string): Promise<Endpoint[]> {
    return App.GetEndpointsByType(interfaceType)
  },

  async setActive(interfaceType: string, endpointId: number): Promise<void> {
    await App.SetActiveEndpoint(interfaceType, endpointId)
  },

  async toggleEnabled(endpointId: number, enabled: boolean): Promise<void> {
    await App.ToggleEndpointEnabled(endpointId, enabled)
  },

  async ping(endpointId: number): Promise<PingResult> {
    return App.PingEndpoint(endpointId)
  },

  async pingAll(interfaceType: string): Promise<PingResult[]> {
    return App.PingAllEndpoints(interfaceType)
  },

  async getVendors(): Promise<Vendor[]> {
    return App.GetVendors()
  },

  async saveVendor(input: VendorInput): Promise<Vendor> {
    const payload: main.VendorInfo = {
      id: input.id || 0,
      name: input.name.trim(),
      homeUrl: input.homeUrl.trim(),
      apiUrl: input.apiUrl.trim(),
      remark: input.remark?.trim() || ''
    }
    return App.SaveVendor(payload)
  },

  async deleteVendor(vendorId: number): Promise<void> {
    await App.DeleteVendor(vendorId)
  },

  async getTransformers(): Promise<Record<string, string[]>> {
    return App.GetTransformers()
  },

  async getById(endpointId: number): Promise<Endpoint> {
    return App.GetEndpointByID(endpointId)
  },

  async saveEndpoint(input: EndpointInputPayload): Promise<Endpoint> {
    return App.SaveEndpointData(input as unknown as main.EndpointInput)
  },

  async deleteEndpoint(endpointId: number): Promise<void> {
    await App.DeleteEndpoint(endpointId)
  },

  async fetchModels(apiUrl: string, apiKey: string, interfaceType: string): Promise<FetchModelsResult> {
    const raw = await App.FetchModels(apiUrl, apiKey, interfaceType)
    return parseJsonSafe<FetchModelsResult>(raw, { success: false, message: 'invalid_json', models: [] })
  },

  async testEndpointWithParams(params: TestEndpointParamsInput): Promise<TestEndpointResult> {
    const raw = await App.TestEndpointWithParams(params)
    return parseJsonSafe<TestEndpointResult>(raw, {
      success: false,
      message: 'invalid_json'
    })
  },

  async getSettings(): Promise<SettingsPayload> {
    return App.GetSettings()
  },

  async pickClashExecutable(): Promise<string> {
    return App.PickClashExecutable()
  },

  async getStatsByInterfaceType(range: string): Promise<InterfaceTypeStatsSummaryInfo[]> {
    return App.GetStatsByInterfaceType(range)
  },

  async clearTokenStats(range: string): Promise<void> {
    await App.ClearTokenStats(range)
  },

  async saveSettings(settings: SettingsPayload): Promise<void> {
    await App.SaveSettings(settings)
  },

  async testDatabaseConnection(dbSource: string): Promise<DatabaseTestResult> {
    return App.TestDatabaseConnection({ dbSource })
  },

  async applyDatabaseConfig(dbSource: string): Promise<DatabaseApplyResult> {
    return App.ApplyDatabaseConfig({ dbSource })
  },

  async getProxyStatus(): Promise<ProxyStatusPayload> {
    return App.GetProxyStatus() as Promise<ProxyStatusPayload>
  },

  async startProxy(): Promise<void> {
    await App.StartProxy()
  },

  async reloadConfig(): Promise<void> {
    await App.ReloadConfig()
  },

  async getCLIConfigDirs(): Promise<CLIConfigDirsPayload> {
    return App.GetCLIConfigDirs()
  },

  async saveCLIConfigDirs(payload: CLIConfigDirsPayload): Promise<void> {
    await App.SaveCLIConfigDirs(payload)
  },

  async getLocalIPs(): Promise<LocalIPInfo[]> {
    return App.GetLocalIPs()
  },

  async getClaudeConfig(): Promise<CLIConfigResult> {
    return App.GetClaudeConfig()
  },

  async getCodexConfig(): Promise<CLIConfigResult> {
    return App.GetCodexConfig()
  },

  async saveClaudeConfig(settingsJson: string): Promise<void> {
    await App.SaveClaudeConfig(settingsJson)
  },

  async saveCodexConfig(configToml: string, authJson: string): Promise<void> {
    await App.SaveCodexConfig(configToml, authJson)
  },

  async processClaudeConfigWithIP(settingsJson: string, selectedIP: string): Promise<string> {
    return App.ProcessClaudeConfigWithIP(settingsJson, selectedIP)
  },

  async processCodexConfigWithIP(
    configToml: string,
    authJson: string,
    selectedIP: string
  ): Promise<ProcessCodexConfigResult> {
    return App.ProcessCodexConfigWithIP(configToml, authJson, selectedIP)
  },

  async saveListenAddr(listenAddr: string): Promise<void> {
    await App.SaveListenAddr(listenAddr)
  },

  async getWebDAVConfig(): Promise<WebDAVConfig> {
    return App.GetWebDAVConfig()
  },

  async saveWebDAVConfig(config: WebDAVConfig): Promise<void> {
    await App.SaveWebDAVConfig(config)
  },

  async webdavList(input: WebDAVRequestPayload): Promise<WebDAVResponse> {
    const payload = toWebDAVRequestInput(input, 'PROPFIND', '1')
    return App.WebDAVList(payload)
  },

  async testWebDAVConnection(config: WebDAVConfig): Promise<WebDAVResponse> {
    const payload: main.WebDAVRequestInput = {
      config: {
        serverUrl: config.serverUrl,
        username: config.username,
        password: config.password
      },
      method: 'PROPFIND',
      path: '/',
      depth: '0'
    }
    return App.WebDAVList(payload)
  },

  async webdavGet(input: WebDAVRequestPayload): Promise<WebDAVResponse> {
    const payload = toWebDAVRequestInput(input, 'GET', '1')
    return App.WebDAVGet(payload)
  },

  async webdavPut(input: WebDAVRequestPayload): Promise<WebDAVResponse> {
    const payload = toWebDAVRequestInput(input, 'PUT', '1')
    return App.WebDAVPut(payload)
  },

  async webdavDelete(input: WebDAVRequestPayload): Promise<WebDAVResponse> {
    const payload = toWebDAVRequestInput(input, 'DELETE', '1')
    return App.WebDAVDelete(payload)
  },

  async webdavMkcol(input: WebDAVRequestPayload): Promise<WebDAVResponse> {
    const payload = toWebDAVRequestInput(input, 'MKCOL', '1')
    return App.WebDAVMkcol(payload)
  },

  async createBackupData(): Promise<main.BackupDataResponse> {
    return App.CreateBackupData()
  },

  async restoreBackupData(data: BackupData, mode: RestoreMode): Promise<void> {
    await App.RestoreBackupData(data, mode)
  },

  async getServers(): Promise<ServerConfig[]> {
    return App.GetServers()
  },

  async saveServers(servers: ServerConfig[]): Promise<void> {
    await App.SaveServers(servers)
  },

  async testServerConnection(url: string, apiKey: string): Promise<void> {
    await App.TestServerConnection(url, apiKey)
  },

  async syncConfigToServer(index: number): Promise<void> {
    await App.SyncConfigToServer(index)
  },

  async buildSyncConfigCurl(index: number): Promise<string> {
    return App.BuildSyncConfigCurl(index)
  },

  async applyEndpointToConfig(endpoint: Endpoint): Promise<void> {
    if (!isEndpointConfigTarget(endpoint.interfaceType)) {
      throw new Error('unsupported_endpoint_type')
    }

    if (endpoint.interfaceType === 'claude') {
      await applyClaudeEndpointUrl(endpoint.apiUrl, endpoint.apiKey)
      return
    }

    await applyCodexEndpointUrl(endpoint.apiUrl, endpoint.apiKey)
  }
}
