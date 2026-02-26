import { computed, ref, shallowReactive } from 'vue'
import { endpointApi } from '@/api/endpoint'
import type {
  RealtimeConnectionState,
  RealtimeEventListener,
  RealtimeEventName,
  RealtimeEventPayloadMap,
  RealtimeRequest,
  RealtimeRequestStatus
} from '@/types/endpoint'

const MAX_ACTIVE_REQUESTS = 50
const DEFAULT_SSE_URL = 'http://localhost:5600/sse'

type GenericListener = (payload: unknown) => void

function toErrorMessage(cause: unknown): string {
  if (cause instanceof Error) return cause.message
  return String(cause)
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null
}

function readString(value: unknown, fallback = ''): string {
  if (typeof value === 'string') return value
  if (typeof value === 'number' || typeof value === 'boolean') return String(value)
  return fallback
}

function readNumber(value: unknown, fallback = 0): number {
  const parsed = Number(value)
  return Number.isFinite(parsed) ? parsed : fallback
}

function determineStatus(raw: Record<string, unknown>): RealtimeRequestStatus {
  const status = readString(raw.status).toLowerCase()
  const statusCode = readNumber(raw.statusCode)

  if (status === 'in_progress') return 'PENDING'
  if (status === 'streaming') return 'STREAMING'
  if (status === 'success' || statusCode === 200) return 'COMPLETED'
  if (status === 'canceled') return 'FAILED'
  if (status.startsWith('error')) return 'FAILED'
  return 'PENDING'
}

function normalizeHeaders(raw: unknown): Record<string, string> {
  if (!isRecord(raw)) return {}

  const headers: Record<string, string> = {}
  for (const [key, value] of Object.entries(raw)) {
    headers[key] = readString(value)
  }
  return headers
}

function createSSEManager() {
  const connectionState = ref<RealtimeConnectionState>('idle')
  const isConnected = ref(false)
  const sseUrl = ref(DEFAULT_SSE_URL)
  const lastError = ref<string | null>(null)
  const activeRequests = shallowReactive<Map<string, RealtimeRequest>>(new Map())
  const listeners = new Map<RealtimeEventName, Set<GenericListener>>()

  let eventSource: EventSource | null = null

  const activeRequestList = computed(() =>
    Array.from(activeRequests.values()).sort((left, right) => {
      const leftTime = Date.parse(right.startTime)
      const rightTime = Date.parse(left.startTime)
      return leftTime - rightTime
    })
  )

  const emit = <E extends RealtimeEventName>(event: E, payload: RealtimeEventPayloadMap[E]): void => {
    const bucket = listeners.get(event)
    if (!bucket || bucket.size === 0) return

    for (const listener of bucket) {
      try {
        listener(payload)
      } catch (cause) {
        lastError.value = toErrorMessage(cause)
      }
    }
  }

  const onEvent = <E extends RealtimeEventName>(
    event: E,
    listener: RealtimeEventListener<E>
  ): (() => void) => {
    const bucket = listeners.get(event) ?? new Set<GenericListener>()
    const wrapped: GenericListener = (payload: unknown) => {
      listener(payload as RealtimeEventPayloadMap[E])
    }

    bucket.add(wrapped)
    listeners.set(event, bucket)

    return () => {
      const current = listeners.get(event)
      if (!current) return
      current.delete(wrapped)
      if (current.size === 0) listeners.delete(event)
    }
  }

  const parseData = (stage: string, rawData: string): unknown | null => {
    try {
      return JSON.parse(rawData)
    } catch (cause) {
      const message = `[SSE:${stage}] Failed to parse JSON`
      lastError.value = message
      emit('error', { stage, message, cause })
      return null
    }
  }

  const cleanupOldRequests = (): void => {
    if (activeRequests.size <= MAX_ACTIVE_REQUESTS) return

    const sorted = Array.from(activeRequests.entries()).sort((left, right) => {
      const leftTime = Date.parse(right[1].startTime)
      const rightTime = Date.parse(left[1].startTime)
      return leftTime - rightTime
    })

    const stale = sorted.slice(MAX_ACTIVE_REQUESTS)
    for (const [requestId] of stale) {
      activeRequests.delete(requestId)
    }
  }

  const processRequestLog = (raw: Record<string, unknown>): void => {
    const requestId = readString(raw.id).trim()
    if (!requestId) return

    const existing = activeRequests.get(requestId)
    const status = determineStatus(raw)

    const request: RealtimeRequest = {
      request_id: requestId,
      interfaceType: readString(raw.interfaceType),
      providerName: readString(raw.providerName),
      endpointName: readString(raw.endpointName),
      method: readString(raw.method, 'POST'),
      path: readString(raw.path),
      model: readString(raw.model || ''),
      status,
      statusCode: readNumber(raw.statusCode),
      runTime: readNumber(raw.runTime),
      timestamp: readString(raw.timestamp, new Date().toISOString()),
      targetUrl: readString(raw.targetUrl),
      upstreamAuth: readString(raw.upstreamAuth),
      requestHeaders: normalizeHeaders(raw.requestHeaders),
      requestStream: readString(raw.requestStream),
      responseStream: readString(raw.responseStream),
      displayDuration: readNumber(raw.runTime),
      startTime: existing?.startTime || new Date().toISOString()
    }

    activeRequests.set(requestId, request)
    cleanupOldRequests()

    if (!existing) {
      emit('started', { requestId, request })
      return
    }

    if (status === 'COMPLETED') {
      emit('completed', { requestId, request })
      activeRequests.delete(requestId)
      emit('removed', { requestId })
      return
    }

    if (status === 'FAILED') {
      emit('failed', { requestId, request })
      activeRequests.delete(requestId)
      emit('removed', { requestId })
      return
    }

    emit('progress', { requestId, request })
  }

  const resolveSSEUrl = async (): Promise<string> => {
    try {
      const url = await endpointApi.getSSEUrl()
      if (url) return url
    } catch (cause) {
      emit('error', {
        stage: 'resolve-url',
        message: '[SSE] Failed to resolve URL from backend, using fallback',
        cause
      })
    }

    return DEFAULT_SSE_URL
  }

  const attachSource = (source: EventSource): void => {
    source.onopen = () => {
      connectionState.value = 'connected'
      isConnected.value = true
      emit('connection', { status: 'connected', url: sseUrl.value })
    }

    source.onerror = () => {
      if (connectionState.value === 'destroyed') return
      const wasConnected = isConnected.value
      connectionState.value = 'reconnecting'
      isConnected.value = false

      if (wasConnected) {
        emit('connection', { status: 'disconnected', url: sseUrl.value })
      }
    }

    source.addEventListener('request_log', (evt: MessageEvent<string>) => {
      const data = parseData('request_log', evt.data)
      if (!isRecord(data)) return
      processRequestLog(data)
    })

    source.addEventListener('token_stats', (evt: MessageEvent<string>) => {
      const data = parseData('token_stats', evt.data)
      if (data === null) return
      emit('token_stats', data)
    })

    source.addEventListener('debug_log', (evt: MessageEvent<string>) => {
      const data = parseData('debug_log', evt.data)
      if (data === null) return
      emit('debug_log', data)
    })

    source.addEventListener('fallback_switch', (evt: MessageEvent<string>) => {
      const data = parseData('fallback_switch', evt.data)
      if (data === null) return
      emit('fallback_switch', data)
    })

    source.addEventListener('endpoint_temp_disabled', (evt: MessageEvent<string>) => {
      const data = parseData('endpoint_temp_disabled', evt.data)
      if (data === null) return
      emit('endpoint_temp_disabled', data)
    })
  }

  const connect = async (): Promise<void> => {
    if (connectionState.value === 'destroyed') {
      throw new Error('SSE manager is destroyed')
    }

    if (eventSource) return

    if (typeof window === 'undefined' || typeof window.EventSource === 'undefined') {
      const message = '[SSE] EventSource is not supported in this runtime'
      lastError.value = message
      emit('error', { stage: 'connect', message })
      throw new Error(message)
    }

    connectionState.value = 'connecting'
    sseUrl.value = await resolveSSEUrl()

    try {
      eventSource = new window.EventSource(sseUrl.value)
      attachSource(eventSource)
    } catch (cause) {
      connectionState.value = 'idle'
      isConnected.value = false
      lastError.value = toErrorMessage(cause)
      emit('error', {
        stage: 'connect',
        message: '[SSE] Failed to create EventSource',
        cause
      })
      throw cause
    }
  }

  const disconnect = (): void => {
    if (eventSource) {
      eventSource.close()
      eventSource = null
    }

    isConnected.value = false
    connectionState.value = connectionState.value === 'destroyed' ? 'destroyed' : 'idle'
  }

  const reconnect = async (): Promise<void> => {
    disconnect()
    await connect()
  }

  const destroy = (): void => {
    disconnect()
    listeners.clear()
    activeRequests.clear()
    connectionState.value = 'destroyed'
  }

  return {
    connectionState,
    isConnected,
    sseUrl,
    lastError,
    activeRequests,
    activeRequestList,
    connect,
    disconnect,
    reconnect,
    destroy,
    onEvent
  }
}

type SSEManager = ReturnType<typeof createSSEManager>

let singleton: SSEManager | null = null

export function useSSE(): SSEManager {
  if (singleton) return singleton

  singleton = createSSEManager()
  return singleton
}
