import { ref, shallowReactive } from 'vue'
import { defineStore } from 'pinia'
import { endpointApi } from '@/api/endpoint'
import type { RealtimeRequest, RealtimeSource, RequestLogDetail, RequestLogInfo } from '@/types/endpoint'

const MAX_RECENT_LOGS = 10

function requestToLogInfo(request: RealtimeRequest): RequestLogInfo {
  const status =
    request.status === 'COMPLETED'
      ? 'success'
      : request.status === 'FAILED'
        ? 'error'
        : 'in_progress'

  return {
    id: request.request_id,
    interfaceType: request.interfaceType || '',
    providerName: request.providerName || '',
    endpointName: request.endpointName || '',
    path: request.path || '',
    runTime: Number(request.runTime || 0),
    status,
    timestamp: request.timestamp || new Date().toISOString()
  }
}

export const useLogsStore = defineStore('logs', () => {
  const recentLogs = ref<RequestLogInfo[]>([])
  const realtimeRequests = shallowReactive<Map<string, RealtimeRequest>>(new Map())
  const selectedLogDetail = ref<RequestLogDetail | null>(null)
  const loading = ref(false)
  const error = ref<string | null>(null)

  let subscriptions: Array<() => void> = []

  function clearError(): void {
    error.value = null
  }

  function upsertRecentLog(log: RequestLogInfo): void {
    const idx = recentLogs.value.findIndex((item) => item.id === log.id)
    if (idx >= 0) {
      recentLogs.value[idx] = log
      return
    }

    recentLogs.value.unshift(log)
    if (recentLogs.value.length > MAX_RECENT_LOGS) {
      recentLogs.value = recentLogs.value.slice(0, MAX_RECENT_LOGS)
    }
  }

  async function loadRecentLogs(): Promise<void> {
    loading.value = true
    clearError()

    try {
      recentLogs.value = (await endpointApi.getRecentLogs()) || []
    } catch (cause) {
      error.value = String(cause)
      throw cause
    } finally {
      loading.value = false
    }
  }

  async function loadLogDetail(logId: string): Promise<RequestLogDetail | null> {
    clearError()

    const realtime = realtimeRequests.get(logId)
    if (realtime) {
      selectedLogDetail.value = {
        id: realtime.request_id,
        interfaceType: realtime.interfaceType,
        providerName: realtime.providerName,
        endpointName: realtime.endpointName,
        path: realtime.path,
        runTime: realtime.runTime,
        status: realtime.status,
        timestamp: realtime.timestamp,
        method: realtime.method,
        statusCode: realtime.statusCode,
        targetUrl: realtime.targetUrl,
        upstreamAuth: realtime.upstreamAuth,
        requestHeaders: realtime.requestHeaders,
        requestStream: realtime.requestStream,
        responseStream: realtime.responseStream
      }
      return selectedLogDetail.value
    }

    try {
      selectedLogDetail.value = await endpointApi.getLogDetail(logId)
      return selectedLogDetail.value
    } catch (cause) {
      error.value = String(cause)
      const cached = recentLogs.value.find((log) => log.id === logId)
      if (cached) {
        selectedLogDetail.value = {
          id: cached.id,
          interfaceType: cached.interfaceType,
          providerName: cached.providerName,
          endpointName: cached.endpointName,
          path: cached.path,
          runTime: cached.runTime,
          status: cached.status,
          timestamp: cached.timestamp,
          method: '',
          statusCode: 0,
          targetUrl: '',
          upstreamAuth: '',
          requestHeaders: {},
          requestStream: '',
          responseStream: ''
        }
        return selectedLogDetail.value
      }
      return null
    }
  }

  function bindRealtime(source: RealtimeSource): void {
    if (subscriptions.length > 0) return

    subscriptions.push(
      source.onEvent('started', ({ requestId, request }) => {
        realtimeRequests.set(requestId, request)
      })
    )

    subscriptions.push(
      source.onEvent('progress', ({ requestId, request }) => {
        const { status } = request
        if (status === 'PENDING' || status === 'STREAMING') {
          realtimeRequests.set(requestId, request)
        }
      })
    )

    subscriptions.push(
      source.onEvent('completed', ({ requestId, request }) => {
        realtimeRequests.delete(requestId)
        upsertRecentLog(requestToLogInfo(request))
      })
    )

    subscriptions.push(
      source.onEvent('failed', ({ requestId, request }) => {
        realtimeRequests.delete(requestId)
        upsertRecentLog(requestToLogInfo(request))
      })
    )

    subscriptions.push(
      source.onEvent('removed', ({ requestId }) => {
        realtimeRequests.delete(requestId)
      })
    )
  }

  function unbindRealtime(): void {
    subscriptions.forEach((off) => off())
    subscriptions = []
    realtimeRequests.clear()
  }

  return {
    recentLogs,
    realtimeRequests,
    selectedLogDetail,
    loading,
    error,
    loadRecentLogs,
    loadLogDetail,
    bindRealtime,
    unbindRealtime,
    clearError
  }
})
