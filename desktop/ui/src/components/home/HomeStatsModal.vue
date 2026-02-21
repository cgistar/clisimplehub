<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import type { DataTableColumns } from 'naive-ui'
import { NButton, NCard, NDataTable, NEmpty, NModal, NSpace, NSpin, NTabPane, NTabs, NTag, NText } from 'naive-ui'
import { useI18n } from 'vue-i18n'
import { endpointApi } from '@/api/endpoint'
import { useFeedback } from '@/composables/useFeedback'
import type { EndpointStatsSummaryInfo, InterfaceTypeStatsSummaryInfo } from '@/types/endpoint'

type StatsRange = 'today' | 'yesterday' | 'week' | 'month' | 'all'

const props = withDefaults(defineProps<{
  show?: boolean
}>(), {
  show: false
})

const emit = defineEmits<{
  'update:show': [show: boolean]
}>()

const { t } = useI18n()
const feedback = useFeedback()

const visible = ref(false)
const currentRange = ref<StatsRange>('today')
const statsData = ref<InterfaceTypeStatsSummaryInfo[]>([])
const loading = ref(false)
const refreshing = ref(false)
const clearing = ref(false)
const latestRequestId = ref(0)

const rangeTabs = computed<Array<{ key: StatsRange; label: string }>>(() => [
  { key: 'today', label: `📅 ${t('stats.timeRange.today')}` },
  { key: 'yesterday', label: `📆 ${t('stats.timeRange.yesterday')}` },
  { key: 'week', label: `📊 ${t('stats.timeRange.week')}` },
  { key: 'month', label: `📈 ${t('stats.timeRange.month')}` },
  { key: 'all', label: `🗂️ ${t('stats.timeRange.all')}` }
])

const tableColumns = computed<DataTableColumns<EndpointStatsSummaryInfo>>(() => {
  const columns: DataTableColumns<EndpointStatsSummaryInfo> = [
    {
      title: t('stats.endpoint'),
      key: 'endpointName',
      width: 260,
      ellipsis: {
        tooltip: true
      },
      render: (row) => {
        const provider = row.providerName || 'unknown'
        return `${provider} - ${row.endpointName || '-'}`
      }
    }
  ]

  if (currentRange.value === 'all') {
    columns.push({
      title: t('stats.date'),
      key: 'date',
      width: 130,
      render: (row) => row.date || '-'
    })
  }

  columns.push(
    {
      title: t('stats.requestCount'),
      key: 'requestCount',
      width: 110,
      render: (row) => String(row.requestCount || 0)
    },
    {
      title: t('stats.input'),
      key: 'inputTokens',
      width: 110,
      render: (row) => formatTokensWithUnit(row.inputTokens)
    },
    {
      title: t('stats.cachedCreate'),
      key: 'cachedCreate',
      width: 120,
      render: (row) => formatTokensWithUnit(row.cachedCreate)
    },
    {
      title: t('stats.cachedRead'),
      key: 'cachedRead',
      width: 120,
      render: (row) => formatTokensWithUnit(row.cachedRead)
    },
    {
      title: t('stats.output'),
      key: 'outputTokens',
      width: 110,
      render: (row) => formatTokensWithUnit(row.outputTokens)
    },
    {
      title: t('stats.reasoning'),
      key: 'reasoning',
      width: 110,
      render: (row) => formatTokensWithUnit(row.reasoning)
    },
    {
      title: t('stats.total'),
      key: 'total',
      width: 110,
      render: (row) => formatTokensWithUnit(row.total)
    }
  )

  return columns
})

function formatTokensWithUnit(num?: number): string {
  if (num === undefined || num === null || num === 0) return '0'
  if (num >= 1000000) return `${(num / 1000000).toFixed(1)}m`
  if (num >= 1000) return `${(num / 1000).toFixed(1)}k`
  return String(num)
}

function toErrorMessage(error: unknown): string {
  if (error instanceof Error) return error.message
  return String(error)
}

function isStatsRange(value: string): value is StatsRange {
  return value === 'today' || value === 'yesterday' || value === 'week' || value === 'month' || value === 'all'
}

function handleRangeChange(value: string): void {
  if (!isStatsRange(value)) return
  currentRange.value = value
}

async function loadStats(options: { silent?: boolean } = {}): Promise<void> {
  const { silent = false } = options
  const requestId = latestRequestId.value + 1
  latestRequestId.value = requestId

  if (silent) {
    refreshing.value = true
  } else {
    loading.value = true
  }

  try {
    const result = await endpointApi.getStatsByInterfaceType(currentRange.value)
    if (latestRequestId.value !== requestId) return
    statsData.value = result || []
  } catch (error) {
    if (latestRequestId.value !== requestId) return
    feedback.error(`${t('stats.refresh')}: ${toErrorMessage(error)}`)
  } finally {
    if (latestRequestId.value !== requestId) return
    loading.value = false
    refreshing.value = false
  }
}

async function handleRefresh(): Promise<void> {
  await loadStats({ silent: true })
}

async function handleClear(): Promise<void> {
  const confirmed = await feedback.confirm(t('stats.clearConfirm'), {
    title: t('stats.clear'),
    confirmText: t('common.ok'),
    cancelText: t('common.cancel'),
    danger: true
  })
  if (!confirmed) return

  clearing.value = true
  try {
    await endpointApi.clearTokenStats(currentRange.value)
    feedback.success(t('stats.clearSuccess'))
    await loadStats({ silent: true })
  } catch (error) {
    feedback.error(`${t('stats.clear')}: ${toErrorMessage(error)}`)
  } finally {
    clearing.value = false
  }
}

function close(): void {
  visible.value = false
}

watch(() => props.show, (next) => {
  visible.value = !!next
  if (next) {
    void loadStats()
  }
}, { immediate: true })

watch(visible, (next) => {
  if (next !== !!props.show) {
    emit('update:show', next)
  }
})

watch(currentRange, () => {
  if (!visible.value) return
  void loadStats({ silent: true })
})
</script>

<template>
  <n-modal
    v-model:show="visible"
    :mask-closable="!(loading || refreshing || clearing)"
    :close-on-esc="!(loading || refreshing || clearing)"
    :block-scroll="true"
  >
    <n-card
      class="home-stats-modal"
      :title="`Token ${t('stats.title')}`"
      :closable="!(loading || refreshing || clearing)"
      :bordered="false"
      role="dialog"
      aria-modal="true"
      @close="close"
    >
      <div class="stats-modal-content">
        <n-tabs
          type="segment"
          size="small"
          :value="currentRange"
          @update:value="handleRangeChange"
        >
          <n-tab-pane
            v-for="item in rangeTabs"
            :key="item.key"
            :name="item.key"
            :tab="item.label"
          />
        </n-tabs>

        <div v-if="loading" class="stats-loading">
          <n-spin size="small" />
          <n-text>{{ t('common.loading') }}</n-text>
        </div>

        <n-empty v-else-if="statsData.length === 0" :description="t('stats.noStats')" />

        <div v-else class="stats-groups">
          <n-card
            v-for="group in statsData"
            :key="group.interfaceType || 'unknown'"
            class="stats-group-card"
            size="small"
            :segmented="{ content: true }"
          >
            <template #header>
              <div class="stats-group-title">{{ group.interfaceType || 'unknown' }}</div>
            </template>
            <template #header-extra>
              <n-tag size="small" type="info">
                {{ t('stats.total') }} {{ formatTokensWithUnit(group.total) }}
              </n-tag>
            </template>

            <div class="stats-summary-grid">
              <div class="summary-item"><span>{{ t('stats.requestCount') }}</span><strong>{{ group.requestCount || 0 }}</strong></div>
              <div class="summary-item"><span>{{ t('stats.input') }}</span><strong>{{ formatTokensWithUnit(group.inputTokens) }}</strong></div>
              <div class="summary-item"><span>{{ t('stats.cachedCreate') }}</span><strong>{{ formatTokensWithUnit(group.cachedCreate) }}</strong></div>
              <div class="summary-item"><span>{{ t('stats.cachedRead') }}</span><strong>{{ formatTokensWithUnit(group.cachedRead) }}</strong></div>
              <div class="summary-item"><span>{{ t('stats.output') }}</span><strong>{{ formatTokensWithUnit(group.outputTokens) }}</strong></div>
              <div class="summary-item"><span>{{ t('stats.reasoning') }}</span><strong>{{ formatTokensWithUnit(group.reasoning) }}</strong></div>
            </div>

            <n-data-table
              class="stats-table"
              size="small"
              :bordered="false"
              :single-line="false"
              :columns="tableColumns"
              :data="group.endpoints || []"
              :pagination="false"
              max-height="320"
            />
          </n-card>
        </div>
      </div>

      <template #footer>
        <n-space justify="end">
          <n-button :loading="refreshing" :disabled="loading || clearing" @click="handleRefresh">
            {{ t('stats.refresh') }}
          </n-button>
          <n-button type="error" :loading="clearing" :disabled="loading || refreshing" @click="handleClear">
            {{ t('stats.clear') }}
          </n-button>
          <n-button :disabled="loading || refreshing || clearing" @click="close">
            {{ t('stats.close') }}
          </n-button>
        </n-space>
      </template>
    </n-card>
  </n-modal>
</template>

<style scoped>
.home-stats-modal {
  width: min(1240px, calc(100vw - 30px));
  max-height: calc(100vh - 30px);
  overflow: hidden;
}

.stats-modal-content {
  display: flex;
  flex-direction: column;
  gap: 12px;
  max-height: calc(100vh - 220px);
}

.stats-loading {
  min-height: 220px;
  display: flex;
  align-items: center;
  justify-content: center;
  gap: 8px;
}

.stats-groups {
  display: flex;
  flex-direction: column;
  gap: 10px;
  overflow: auto;
  padding-right: 4px;
}

.stats-group-card {
  border: 1px solid var(--border-light);
  border-radius: 10px;
}

.stats-group-title {
  text-transform: capitalize;
  font-weight: 700;
}

.stats-summary-grid {
  display: grid;
  grid-template-columns: repeat(6, minmax(0, 1fr));
  gap: 8px;
  margin-bottom: 10px;
}

.summary-item {
  border: 1px solid var(--border-light);
  border-radius: 8px;
  padding: 8px 10px;
  background: var(--bg-secondary);
  display: flex;
  flex-direction: column;
  gap: 4px;
  min-width: 0;
}

.summary-item span {
  font-size: 12px;
  color: var(--text-tertiary);
}

.summary-item strong {
  font-size: 13px;
  color: var(--text-primary);
}

.stats-table {
  border-top: 1px solid var(--border-light);
  padding-top: 8px;
}

@media (max-width: 1200px) {
  .stats-summary-grid {
    grid-template-columns: repeat(3, minmax(0, 1fr));
  }
}

@media (max-width: 900px) {
  .home-stats-modal {
    width: calc(100vw - 16px);
    max-height: calc(100vh - 16px);
  }

  .stats-modal-content {
    max-height: calc(100vh - 240px);
  }

  .stats-summary-grid {
    grid-template-columns: repeat(2, minmax(0, 1fr));
  }
}
</style>
