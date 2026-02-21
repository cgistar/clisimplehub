<script setup lang="ts">
import { computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { NButton } from 'naive-ui'
import type { UILogItem } from '@/types/endpoint'
import LogList from '@/components/logs/LogList.vue'
import LogDetailModal from '@/components/logs/LogDetailModal.vue'
import HomeStatsModal from './HomeStatsModal.vue'

const { t } = useI18n()

const showDetail = ref(false)
const showStatsModal = ref(false)
const selectedLogId = ref<string | null>(null)

function handleSelectLog(log: UILogItem): void {
  openLogDetail(log.id)
}

function showStats(): void {
  showStatsModal.value = true
}

function openLogDetail(logId: string): void {
  selectedLogId.value = logId
  showDetail.value = true
}

function closeLogDetail(): void {
  showDetail.value = false
}

const statsTitle = computed(() => t('stats.title'))

defineExpose({
  openLogDetail,
  closeLogDetail
})
</script>

<template>
  <div class="card logs-card">
    <div class="card-header">
      <h2>{{ t('logs.title') }}</h2>
      <div class="card-header-actions">
        <n-button size="small" secondary :title="statsTitle" @click="showStats">
          {{ t('stats.title') }}
        </n-button>
      </div>
    </div>

    <LogList class="logs-list" :show-header="false" @select-log="handleSelectLog" />

    <LogDetailModal v-model:show="showDetail" :log-id="selectedLogId" />
    <HomeStatsModal v-model:show="showStatsModal" />
  </div>
</template>

<style scoped>
.logs-card {
  flex: 1;
  min-height: 0;
  height: 100%;
  overflow: hidden;
}

.logs-list {
  flex: 1;
  min-height: 0;
}

:deep(.log-list-container) {
  flex: 1;
  min-height: 0;
  background: transparent;
  border-radius: 0;
}

:deep(.log-scroll-area) {
  flex: 1;
  min-height: 0;
}
</style>
