<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { NButton, NSelect, NTabPane, NTabs } from 'naive-ui'
import { Pencil, Plus, Power, RefreshCw, Zap } from 'lucide-vue-next'
import { storeToRefs } from 'pinia'
import type { Endpoint, InterfaceType } from '@/types/endpoint'
import { useHomeEndpointsStore } from '@/stores/homeEndpointsStore'
import { useFeedback } from '@/composables/useFeedback'
import CLIConfigEditorModal from './CLIConfigEditorModal.vue'

type HomeInterfaceType = 'claude' | 'codex' | 'gemini' | 'chat'

const { t } = useI18n()
const feedback = useFeedback()
const endpointStore = useHomeEndpointsStore()
const emit = defineEmits<{
  'manage-vendors': []
  'add-endpoint': []
  'edit-endpoint': [endpointId: number]
}>()

const { currentTab, sortedCurrentEndpoints, enabledEndpoints, activeEndpointId, pingResults, loading } =
  storeToRefs(endpointStore)

const selectedEndpointId = ref<string>('')
const cliConfigEditorRef = ref<InstanceType<typeof CLIConfigEditorModal> | null>(null)

const tabs = computed<Array<{ key: HomeInterfaceType; label: string }>>(() => [
  { key: 'claude', label: 'Claude' },
  { key: 'codex', label: 'Codex' },
  { key: 'gemini', label: 'Gemini' },
  { key: 'chat', label: 'Chat' }
])
const endpointSelectOptions = computed<Array<{ label: string; value: string }>>(() =>
  enabledEndpoints.value.map((endpoint) => ({
    label: endpointSelectLabel(endpoint),
    value: String(endpoint.id)
  }))
)
const showCliEditorButton = computed(() => currentTab.value === 'claude' || currentTab.value === 'codex')

function toErrorMessage(error: unknown): string {
  if (error instanceof Error) return error.message
  return String(error)
}

function endpointDisplayName(endpoint: Endpoint): string {
  return endpoint.providerName ? `${endpoint.providerName} - ${endpoint.name}` : endpoint.name
}

function endpointSelectLabel(endpoint: Endpoint): string {
  return endpoint.providerName ? `${endpoint.providerName} - ${endpoint.name}` : endpoint.name
}

function pingText(endpoint: Endpoint): string {
  const result = pingResults.value[endpoint.id]
  if (!result) return ''
  if (result.loading) return '(...)'
  if (result.success) return `(${result.latency || 0}ms)`
  return `(${t('endpoints.pingFailed')})`
}

function syncSelectedEndpoint(): void {
  selectedEndpointId.value = activeEndpointId.value ? String(activeEndpointId.value) : ''
}

function formatTokens(value: number): string {
  const num = value || 0
  if (!num) return '0'
  if (num >= 1000000) return `${(num / 1000000).toFixed(1)}m`
  if (num >= 1000) return `${(num / 1000).toFixed(1)}k`
  return String(num)
}

async function handleTabSwitch(type: HomeInterfaceType): Promise<void> {
  try {
    await endpointStore.setTab(type)
    syncSelectedEndpoint()
  } catch (error) {
    feedback.error(t('endpoints.refreshFailed') + ': ' + toErrorMessage(error))
  }
}

function isHomeInterfaceType(value: string): value is HomeInterfaceType {
  return value === 'claude' || value === 'codex' || value === 'gemini' || value === 'chat'
}

function handleTabSwitchFromTabs(value: string): void {
  if (!isHomeInterfaceType(value)) return
  void handleTabSwitch(value)
}

async function handleRefresh(): Promise<void> {
  try {
    await endpointStore.refreshCurrent({ showLoading: true })
    syncSelectedEndpoint()
  } catch (error) {
    feedback.error(t('endpoints.refreshFailed') + ': ' + toErrorMessage(error))
  }
}

async function handlePingAll(): Promise<void> {
  try {
    await endpointStore.pingAll()
  } catch (error) {
    feedback.error('Ping failed: ' + toErrorMessage(error))
  }
}

async function handleSetActive(value: string | null): Promise<void> {
  selectedEndpointId.value = value || ''
  const endpointId = Number.parseInt(selectedEndpointId.value, 10)
  if (!endpointId) return

  try {
    await endpointStore.setActiveEndpointById(endpointId)
    syncSelectedEndpoint()
  } catch (error) {
    feedback.error('Failed to set active endpoint: ' + toErrorMessage(error))
  }
}

async function handleSwitchEndpoint(endpointId: number): Promise<void> {
  try {
    await endpointStore.setActiveEndpointById(endpointId)
    syncSelectedEndpoint()
  } catch (error) {
    feedback.error('Failed to set active endpoint: ' + toErrorMessage(error))
  }
}

async function handleToggleEnabled(endpointId: number, enabled: boolean): Promise<void> {
  try {
    await endpointStore.toggleEndpointEnabled(endpointId, enabled)
    syncSelectedEndpoint()
  } catch (error) {
    feedback.error('Failed to toggle endpoint: ' + toErrorMessage(error))
  }
}

async function handlePingSingle(endpointId: number): Promise<void> {
  await endpointStore.pingSingle(endpointId)
}

function handleManageVendors(): void {
  emit('manage-vendors')
}

function handleAddEndpoint(): void {
  emit('add-endpoint')
}

async function handleOpenCliEditor(): Promise<void> {
  const tab = currentTab.value as InterfaceType
  if (tab !== 'claude' && tab !== 'codex') return

  try {
    await cliConfigEditorRef.value?.open(tab)
  } catch (error) {
    feedback.error(t('cliConfig.loadFailed') + ': ' + toErrorMessage(error))
  }
}

function handleEditEndpoint(endpointId: number): void {
  emit('edit-endpoint', endpointId)
}

async function handleApplyEndpoint(endpointId: number): Promise<void> {
  const endpoint = sortedCurrentEndpoints.value.find((item) => item.id === endpointId)
  if (!endpoint) {
    feedback.error(t('endpoints.notFound'))
    return
  }

  if (endpoint.interfaceType !== 'claude' && endpoint.interfaceType !== 'codex') {
    feedback.error(t('endpoints.unsupportedType'))
    return
  }

  const cliName = endpoint.interfaceType === 'claude' ? 'Claude Code' : 'Codex'
  const confirmed = await feedback.confirm(
    t('endpoints.applyUrlConfirm')
      .replace('{name}', cliName)
      .replace('{url}', endpoint.apiUrl),
    {
      title: t('endpoints.applyUrlTitle'),
      confirmText: t('common.ok'),
      cancelText: t('common.cancel')
    }
  )
  if (!confirmed) return

  try {
    await endpointStore.applyEndpointToConfig(endpointId)
    feedback.success(
      t('endpoints.applyUrlSuccess')
        .replace('{name}', cliName)
        .replace('{url}', endpoint.apiUrl)
    )
  } catch (error) {
    feedback.error(t('endpoints.applyUrlFailed') + ': ' + toErrorMessage(error))
  }
}

defineExpose({
  syncSelectedEndpoint,
  refresh: handleRefresh
})

watch(activeEndpointId, () => {
  syncSelectedEndpoint()
}, { immediate: true })
</script>

<template>
  <div class="card home-endpoints-card">
    <div class="card-header">
      <h2>
        {{ t('endpoints.title') }}
      </h2>
    </div>

    <div class="tabs">
      <n-tabs
        type="line"
        size="small"
        :value="currentTab"
        @update:value="handleTabSwitchFromTabs"
      >
        <n-tab-pane
          v-for="tab in tabs"
          :key="tab.key"
          :name="tab.key"
          :tab="tab.label"
        />
        <template #suffix>
          <n-button
            v-if="showCliEditorButton"
            quaternary
            circle
            :title="t('cliConfig.title')"
            @click="handleOpenCliEditor"
          >
            <Pencil :size="14" :stroke-width="2" />
          </n-button>
        </template>
      </n-tabs>
    </div>

    <div class="active-selector">
      <label>{{ t('endpoints.activeEndpoint') }}:</label>
      <n-select
        :value="selectedEndpointId || null"
        :options="endpointSelectOptions"
        :placeholder="t('endpoints.selectActive')"
        clearable
        @update:value="handleSetActive"
      />
      <n-button quaternary circle :title="t('endpoints.refresh')" @click="handleRefresh">
        <RefreshCw :size="14" :stroke-width="2" />
      </n-button>
      <n-button quaternary circle :title="t('endpoints.pingAll')" @click="handlePingAll">
        <Zap :size="14" :stroke-width="2" />
      </n-button>
      <n-button quaternary circle :title="t('manage.addEndpoint')" @click="handleAddEndpoint">
        <Plus :size="14" :stroke-width="2" />
      </n-button>
    </div>

    <div class="endpoint-list">
      <div v-if="!loading && sortedCurrentEndpoints.length === 0" class="empty-state">{{ t('endpoints.noEndpoints') }}</div>

      <div
        v-for="endpoint in sortedCurrentEndpoints"
        v-else
        :key="endpoint.id"
        class="endpoint-item"
        :class="{ active: endpoint.active, disabled: !endpoint.enabled }"
        @click="handleEditEndpoint(endpoint.id)"
      >
        <div class="endpoint-header">
          <div class="endpoint-title">
            <span class="endpoint-name">{{ endpointDisplayName(endpoint) }}</span>
            <span v-if="endpoint.active" class="inline-flex shrink-0 items-center rounded-full border px-2 py-0.5 text-xs border-emerald-300 bg-emerald-100 text-emerald-700">{{ t('endpoints.currentUse') }}</span>
            <button
              v-else-if="endpoint.enabled"
              type="button"
              class="inline-flex items-center gap-1 rounded-md border border-slate-300 px-2 py-1 text-xs text-slate-700 hover:bg-slate-100"
              @click.stop="handleSwitchEndpoint(endpoint.id)"
            >
              {{ t('endpoints.switch') }}
            </button>
          </div>

          <div class="endpoint-controls-top" @click.stop>
            <label class="inline-flex items-center" :class="{ 'opacity-60': endpoint.active }">
              <span class="relative inline-flex h-6 w-11 shrink-0 items-center">
                <input
                  type="checkbox"
                  class="peer sr-only"
                  :checked="endpoint.enabled"
                  :disabled="endpoint.active"
                  @change="handleToggleEnabled(endpoint.id, ($event.target as HTMLInputElement).checked)"
                >
                <span
                  class="h-6 w-11 rounded-full bg-slate-300 transition-colors duration-200 peer-checked:bg-sky-600 peer-disabled:bg-slate-200"
                ></span>
                <span
                  class="pointer-events-none absolute left-0.5 top-0.5 h-5 w-5 rounded-full bg-white shadow transition-transform duration-200 peer-checked:translate-x-5"
                ></span>
              </span>
            </label>
          </div>
        </div>

        <div class="endpoint-info">
          <div
            class="endpoint-url"
            :title="t('endpoints.ping')"
            @click.stop="handlePingSingle(endpoint.id)"
          >
            🌐 {{ endpoint.apiUrl }}
            <span class="ping-result" :class="{ 'ping-loading': pingResults[endpoint.id]?.loading, 'ping-success': pingResults[endpoint.id]?.success, 'ping-error': pingResults[endpoint.id] && !pingResults[endpoint.id]?.success && !pingResults[endpoint.id]?.loading }">
              {{ pingText(endpoint) }}
            </span>
          </div>
          <div class="endpoint-daily-stats">
            <span class="stat-item">📊 {{ t('endpoints.requests') }}: {{ endpoint.todayRequests || 0 }}</span>
            <span class="stat-separator">|</span>
            <span class="stat-item" :class="{ 'stat-error': (endpoint.todayErrors || 0) > 0 }">
              {{ t('endpoints.errors') }}: {{ endpoint.todayErrors || 0 }}
            </span>
          </div>
          <div class="endpoint-token-stats">
            <span class="stat-item">
              🔄 Token {{ t('stats.total') }}:
              {{ formatTokens((endpoint.todayInput || 0) + (endpoint.todayOutput || 0)) }}
              ({{ t('stats.input') }}: {{ formatTokens(endpoint.todayInput || 0) }}, {{ t('stats.output') }}: {{ formatTokens(endpoint.todayOutput || 0) }})
            </span>
            <n-button
              v-if="endpoint.interfaceType === 'claude' || endpoint.interfaceType === 'codex'"
              quaternary
              circle
              class="endpoint-apply-btn"
              :title="t('endpoints.applyToConfig')"
              @click.stop="handleApplyEndpoint(endpoint.id)"
            >
              <Power :size="14" :stroke-width="2" />
            </n-button>
          </div>
        </div>
      </div>

      <div v-if="loading" class="endpoint-list-loading-mask">
        <span class="endpoint-list-loading-text">{{ t('common.loading') }}</span>
      </div>
    </div>

    <CLIConfigEditorModal ref="cliConfigEditorRef" />
  </div>
</template>

<style scoped>
.home-endpoints-card {
  flex: 1;
  min-height: 0;
  height: 100%;
}

.endpoint-list {
  position: relative;
}

.endpoint-list-loading-mask {
  position: absolute;
  inset: 0;
  z-index: 1;
  display: flex;
  align-items: center;
  justify-content: center;
  background: color-mix(in srgb, var(--bg-primary) 78%, transparent);
  backdrop-filter: blur(1px);
}

.endpoint-list-loading-text {
  padding: 4px 10px;
  border: 1px solid var(--border-light);
  border-radius: 999px;
  background: var(--bg-primary);
  color: var(--text-secondary);
  font-size: 12px;
}

.tabs :deep(.n-tabs) {
  flex: 1;
  min-width: 0;
}

.tabs :deep(.n-tabs-nav) {
  margin-bottom: 0;
}
</style>
