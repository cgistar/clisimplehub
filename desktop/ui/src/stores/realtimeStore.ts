import { computed } from 'vue'
import { defineStore } from 'pinia'
import { useSSE } from '@/composables/useSSE'
import type { RealtimeEventListener, RealtimeEventName } from '@/types/endpoint'

export const useRealtimeStore = defineStore('realtime', () => {
  const sse = useSSE()

  const connectionState = computed(() => sse.connectionState.value)
  const isConnected = computed(() => sse.isConnected.value)
  const sseUrl = computed(() => sse.sseUrl.value)
  const lastError = computed(() => sse.lastError.value)
  const activeRequests = computed(() => sse.activeRequestList.value)

  const onEvent = <E extends RealtimeEventName>(event: E, listener: RealtimeEventListener<E>) =>
    sse.onEvent(event, listener)

  async function start(): Promise<void> {
    await sse.connect()
  }

  function stop(): void {
    sse.disconnect()
  }

  async function reconnect(): Promise<void> {
    await sse.reconnect()
  }

  function destroy(): void {
    sse.destroy()
  }

  return {
    connectionState,
    isConnected,
    sseUrl,
    lastError,
    activeRequests,
    onEvent,
    start,
    stop,
    reconnect,
    destroy
  }
})
