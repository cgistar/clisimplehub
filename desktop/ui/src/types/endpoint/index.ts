import type { main } from '../../../wailsjs/go/models'
import type { proxy } from '../../../wailsjs/go/models'
import type { config } from '../../../wailsjs/go/models'

export type Endpoint = main.EndpointInfo
export type PingResult = main.PingResult
export type RequestLogInfo = main.RequestLogInfo
export type RequestLogDetail = main.RequestLogDetailInfo
export type Vendor = main.VendorInfo
export type LocalIPInfo = main.LocalIPInfo
export type CLIConfigFile = main.CLIConfigFile
export type CLIConfigResult = main.CLIConfigResult
export type ProcessCodexConfigResult = main.ProcessCodexConfigResult
export type EndpointStatsSummaryInfo = main.EndpointStatsSummaryInfo
export type InterfaceTypeStatsSummaryInfo = main.InterfaceTypeStatsSummaryInfo
export type WebDAVConfig = main.WebDAVConfigInfo
export type WebDAVResponse = proxy.WebDAVResponse
export type BackupData = config.BackupData
export type ServerConfig = config.ServerConfig
export type InterfaceType = 'claude' | 'codex' | 'gemini' | 'chat'

export interface VendorInput {
  id?: number
  name: string
  homeUrl: string
  apiUrl: string
  remark?: string
}

export interface ModelMappingInput {
  alias: string
  name: string
}

export interface EndpointInputPayload {
  id: number
  name: string
  apiUrl: string
  apiKey: string
  active: boolean
  enabled: boolean
  interfaceType: InterfaceType
  providerName?: string
  model?: string
  transformer?: string
  transformerSet: boolean
  proxyUrl?: string
  proxyUrlSet: boolean
  models: ModelMappingInput[] | null
  modelsSet: boolean
  routes: string[] | null
  routesSet: boolean
  remark?: string
  priority: number
}

export interface FetchModelsResult {
  success: boolean
  message?: string
  models: string[]
}

export interface TestEndpointParamsInput {
  apiUrl: string
  apiKey: string
  interfaceType: InterfaceType
  model: string
}

export interface TestEndpointResult {
  success: boolean
  statusCode?: number
  message: string
  targetUrl?: string
  requestHeaders?: Record<string, string>
  errorMessage?: string
  responseText?: string
}

export interface SettingsPayload {
  port: number
  apiKey: string
  proxyUrl?: string
  clashPath?: string
  dbSource?: string
  fallback: boolean
  debugMode?: string
  listenAddr?: string
  disableImageGeneration?: string
}

export interface DatabaseTestResult {
  message?: string
  dbDriver?: string
  dbSource?: string
}

export interface DatabaseApplyResult extends DatabaseTestResult {}

export interface ProxyStatusPayload {
  running: boolean
  port: number
  listenAddr?: string
  lastError?: string
}

export interface CLIConfigDirsPayload {
  claudeConfigDir: string
  codexConfigDir: string
}

export interface WebDAVRequestPayload {
  config: WebDAVConfig
  path: string
  depth?: string
  body?: string
  headers?: Record<string, string>
  destPath?: string
}

export interface WebDAVBackupItem {
  filename: string
  displayName: string
  href?: string
  lastModified?: string
  name: string
}

export type RestoreMode = 'merge' | 'replace'

export type RealtimeRequestStatus = 'PENDING' | 'STREAMING' | 'COMPLETED' | 'FAILED'
export type RealtimeConnectionState = 'idle' | 'connecting' | 'connected' | 'reconnecting' | 'destroyed'

export interface RealtimeRequest extends Omit<RequestLogDetail, 'id' | 'status'> {
  request_id: string
  status: RealtimeRequestStatus
  displayDuration: number
  startTime: string
}

export interface UILogItem extends RequestLogInfo {
  isRealtime: boolean
  startTime?: string
  displayDuration?: number
}

export interface RealtimeRequestPayload {
  requestId: string
  request: RealtimeRequest
}

export interface RealtimeErrorPayload {
  stage: string
  message: string
  cause?: unknown
}

export interface RealtimeConnectionPayload {
  status: 'connected' | 'disconnected'
  url: string
}

export type RealtimeEventPayloadMap = {
  started: RealtimeRequestPayload
  progress: RealtimeRequestPayload
  completed: RealtimeRequestPayload
  failed: RealtimeRequestPayload
  removed: { requestId: string }
  connection: RealtimeConnectionPayload
  token_stats: unknown
  debug_log: unknown
  fallback_switch: unknown
  endpoint_temp_disabled: unknown
  error: RealtimeErrorPayload
}

export type RealtimeEventName = keyof RealtimeEventPayloadMap

export type RealtimeEventListener<E extends RealtimeEventName> = (
  payload: RealtimeEventPayloadMap[E]
) => void

export interface RealtimeSource {
  onEvent<E extends RealtimeEventName>(event: E, listener: RealtimeEventListener<E>): () => void
}
