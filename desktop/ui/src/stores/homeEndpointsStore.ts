import { computed, ref } from 'vue'
import { defineStore } from 'pinia'
import { endpointApi } from '@/api/endpoint'
import type { Endpoint, InterfaceType, PingResult } from '@/types/endpoint'

export interface PingResultView {
  endpointId?: number
  success?: boolean
  latency?: number
  error?: string
  loading?: boolean
}

interface EndpointsByType {
  claude: Endpoint[]
  codex: Endpoint[]
  chat: Endpoint[]
  gemini: Endpoint[]
}

const DEFAULT_ENDPOINTS: EndpointsByType = {
  claude: [],
  codex: [],
  chat: [],
  gemini: []
}

function sortEndpoints(endpoints: Endpoint[]): Endpoint[] {
  return [...endpoints].sort((left, right) => {
    const leftPriority = left.priority || 5
    const rightPriority = right.priority || 5
    if (leftPriority !== rightPriority) return rightPriority - leftPriority

    const leftName = left.providerName ? `${left.providerName} / ${left.name}` : left.name
    const rightName = right.providerName ? `${right.providerName} / ${right.name}` : right.name
    return leftName.localeCompare(rightName)
  })
}

export const useHomeEndpointsStore = defineStore('homeEndpoints', () => {
  const currentTab = ref<InterfaceType>('claude')
  const endpointsByType = ref<EndpointsByType>({ ...DEFAULT_ENDPOINTS })
  const pingResults = ref<Record<number, PingResultView>>({})
  const loading = ref(false)
  const error = ref<string | null>(null)

  const currentEndpoints = computed(() => endpointsByType.value[currentTab.value] || [])
  const sortedCurrentEndpoints = computed(() => sortEndpoints(currentEndpoints.value))
  const enabledEndpoints = computed(() => currentEndpoints.value.filter((endpoint) => endpoint.enabled))
  const activeEndpointId = computed<number | null>(() => {
    const active = currentEndpoints.value.find((endpoint) => endpoint.active)
    return active?.id ?? null
  })

  function clearError(): void {
    error.value = null
  }

  async function loadEndpoints(
    interfaceType = currentTab.value,
    options: { showLoading?: boolean } = {}
  ): Promise<void> {
    const showLoading = options.showLoading === true
    if (showLoading) {
      loading.value = true
    }
    clearError()

    try {
      const endpoints = await endpointApi.getByType(interfaceType)
      endpointsByType.value[interfaceType] = endpoints || []
    } catch (cause) {
      error.value = cause instanceof Error ? cause.message : String(cause)
      throw cause
    } finally {
      if (showLoading) {
        loading.value = false
      }
    }
  }

  async function refreshCurrent(options: { showLoading?: boolean } = {}): Promise<void> {
    await loadEndpoints(currentTab.value, options)
  }

  async function setTab(interfaceType: InterfaceType): Promise<void> {
    currentTab.value = interfaceType
    await loadEndpoints(interfaceType)
  }

  async function setActiveEndpointById(endpointId: number): Promise<void> {
    await endpointApi.setActive(currentTab.value, endpointId)
    await loadEndpoints(currentTab.value)
  }

  async function toggleEndpointEnabled(endpointId: number, enabled: boolean): Promise<void> {
    await endpointApi.toggleEnabled(endpointId, enabled)
    await loadEndpoints(currentTab.value)
  }

  async function pingSingle(endpointId: number): Promise<void> {
    pingResults.value[endpointId] = { loading: true }
    try {
      const result: PingResult = await endpointApi.ping(endpointId)
      pingResults.value[endpointId] = {
        endpointId: result.endpointId,
        success: result.success,
        latency: result.latency,
        error: result.error
      }
    } catch (cause) {
      pingResults.value[endpointId] = {
        endpointId,
        success: false,
        error: cause instanceof Error ? cause.message : String(cause)
      }
    }
  }

  async function pingAll(): Promise<void> {
    currentEndpoints.value.forEach((endpoint) => {
      pingResults.value[endpoint.id] = { loading: true }
    })

    try {
      const results = await endpointApi.pingAll(currentTab.value)
      results.forEach((result) => {
        pingResults.value[result.endpointId] = {
          endpointId: result.endpointId,
          success: result.success,
          latency: result.latency,
          error: result.error
        }
      })
    } catch (cause) {
      error.value = cause instanceof Error ? cause.message : String(cause)
      throw cause
    }
  }

  async function applyEndpointToConfig(endpointId: number): Promise<Endpoint> {
    const endpoint = currentEndpoints.value.find((item) => item.id === endpointId)
    if (!endpoint) {
      throw new Error('endpoint_not_found')
    }

    if (endpoint.interfaceType !== 'claude' && endpoint.interfaceType !== 'codex') {
      throw new Error('unsupported_endpoint_type')
    }

    await endpointApi.applyEndpointToConfig(endpoint)
    return endpoint
  }

  return {
    currentTab,
    endpointsByType,
    pingResults,
    loading,
    error,
    currentEndpoints,
    sortedCurrentEndpoints,
    enabledEndpoints,
    activeEndpointId,
    loadEndpoints,
    refreshCurrent,
    setTab,
    setActiveEndpointById,
    toggleEndpointEnabled,
    pingSingle,
    pingAll,
    applyEndpointToConfig,
    clearError
  }
})
