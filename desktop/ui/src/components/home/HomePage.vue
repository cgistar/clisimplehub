<script setup lang="ts">
import { onMounted, onBeforeUnmount, ref } from 'vue'
import { useMessage } from 'naive-ui'
import { useHomeEndpointsStore } from '@/stores/homeEndpointsStore'
import { useLogsStore } from '@/stores/logsStore'
import { useRealtimeStore } from '@/stores/realtimeStore'
import type { Endpoint } from '@/types/endpoint'
import '@/styles/pages/home.css'
import HomeEndpointsPanel from './HomeEndpointsPanel.vue'
import HomeLogsPanel from './HomeLogsPanel.vue'
import EndpointFormModal from './EndpointFormModal.vue'
import VendorManageModal from './VendorManageModal.vue'

const message = useMessage()
const endpointStore = useHomeEndpointsStore()
const logsStore = useLogsStore()
const realtimeStore = useRealtimeStore()
const endpointFormRef = ref<InstanceType<typeof EndpointFormModal> | null>(null)
const vendorManageVisible = ref(false)

let endpointRefreshTimer: ReturnType<typeof setTimeout> | null = null
let fallbackTimer: ReturnType<typeof setInterval> | null = null
const unsubs: Array<() => void> = []

function toErrorMessage(error: unknown): string {
  if (error instanceof Error) return error.message
  return String(error)
}

async function refreshHomeData(): Promise<void> {
  await Promise.all([
    endpointStore.refreshCurrent(),
    logsStore.loadRecentLogs()
  ])
}

function scheduleEndpointRefresh(interfaceType?: string): void {
  if (interfaceType && interfaceType !== endpointStore.currentTab) return
  if (endpointRefreshTimer) return

  endpointRefreshTimer = setTimeout(async () => {
    endpointRefreshTimer = null
    try {
      await endpointStore.refreshCurrent()
    } catch {
      // noop
    }
  }, 1200)
}

function attachRealtimeListeners(): void {
  unsubs.push(realtimeStore.onEvent('completed', ({ request }) => {
    scheduleEndpointRefresh(request.interfaceType)
  }))
  unsubs.push(realtimeStore.onEvent('failed', ({ request }) => {
    scheduleEndpointRefresh(request.interfaceType)
  }))
  unsubs.push(realtimeStore.onEvent('token_stats', () => {
    scheduleEndpointRefresh()
  }))
}

function bindHomeEvents(): void {
  const handleEndpointsUpdated = () => {
    void endpointStore.refreshCurrent()
  }
  const handleLogsUpdated = () => {
    void logsStore.loadRecentLogs()
  }
  const handleHomeVisible = () => {
    void refreshHomeData()
  }

  window.addEventListener('home:endpoints-updated', handleEndpointsUpdated)
  window.addEventListener('home:logs-updated', handleLogsUpdated)
  window.addEventListener('home:visible', handleHomeVisible)

  unsubs.push(() => window.removeEventListener('home:endpoints-updated', handleEndpointsUpdated))
  unsubs.push(() => window.removeEventListener('home:logs-updated', handleLogsUpdated))
  unsubs.push(() => window.removeEventListener('home:visible', handleHomeVisible))
}

onMounted(async () => {
  try {
    await refreshHomeData()
    await realtimeStore.start()
    logsStore.bindRealtime(realtimeStore)
    attachRealtimeListeners()
    bindHomeEvents()

    fallbackTimer = setInterval(async () => {
      if (realtimeStore.isConnected) return
      await logsStore.loadRecentLogs()
    }, 5000)
  } catch (error) {
    message.error('Home init failed: ' + toErrorMessage(error))
  }
})

onBeforeUnmount(() => {
  if (endpointRefreshTimer) {
    clearTimeout(endpointRefreshTimer)
    endpointRefreshTimer = null
  }
  if (fallbackTimer) {
    clearInterval(fallbackTimer)
    fallbackTimer = null
  }
  unsubs.forEach((off) => off())
  logsStore.unbindRealtime()
  realtimeStore.stop()
})

async function openEndpointForm(endpoint: Endpoint | null = null): Promise<void> {
  await endpointFormRef.value?.open(endpoint)
}

async function editEndpointById(endpointId: number): Promise<void> {
  await endpointFormRef.value?.editById(endpointId)
}

async function addEndpoint(): Promise<void> {
  await openEndpointForm()
}

function openVendorManage(): void {
  vendorManageVisible.value = true
}
</script>

<template>
  <div class="left-panel">
    <HomeEndpointsPanel
      @manage-vendors="openVendorManage"
      @add-endpoint="addEndpoint"
      @edit-endpoint="editEndpointById"
    />
  </div>

  <div class="right-panel">
    <HomeLogsPanel />
  </div>

  <EndpointFormModal ref="endpointFormRef" />
  <VendorManageModal v-model:show="vendorManageVisible" />
</template>
