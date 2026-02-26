<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue';
import { useI18n } from 'vue-i18n';
import { NEmpty, NSpin } from 'naive-ui';
import { useLogsStore } from '@/stores/logsStore';
import { useRealtimeStore } from '@/stores/realtimeStore';
import type { RealtimeRequest, UILogItem } from '@/types/endpoint';
import { toTimestampMs } from '@/utils/datetime';
import LogCard from './LogCard.vue';

const { t } = useI18n();
const logsStore = useLogsStore();
const realtimeStore = useRealtimeStore();

const props = withDefaults(defineProps<{
  height?: number | string
  showHeader?: boolean
}>(), {
  showHeader: true
});

const emit = defineEmits<{
  'select-log': [log: UILogItem]
}>();

const nowMs = ref(Date.now());
let durationTimer: ReturnType<typeof setInterval> | null = null;

function startDurationTimer(): void {
  if (durationTimer) return;
  nowMs.value = Date.now();
  durationTimer = setInterval(() => {
    nowMs.value = Date.now();
  }, 200);
}

function stopDurationTimer(): void {
  if (!durationTimer) return;
  clearInterval(durationTimer);
  durationTimer = null;
}

function isRealtimeActiveStatus(status: unknown): boolean {
  return status === 'PENDING' || status === 'STREAMING' || status === 'in_progress';
}

const realtimeLogs = computed((): UILogItem[] =>
  Array.from(logsStore.realtimeRequests.values())
    .filter((req) => isRealtimeActiveStatus(req.status))
    .map((req: RealtimeRequest): UILogItem => ({
      id: req.request_id,
      interfaceType: req.interfaceType,
      providerName: req.providerName,
      endpointName: req.endpointName,
      path: req.path,
      model: req.model || '',
      runTime: req.runTime,
      status: req.status,
      timestamp: req.timestamp,
      startTime: req.startTime,
      displayDuration: req.displayDuration,
      isRealtime: true
    }))
    .sort((a, b) => {
      const aMs = toTimestampMs(a.startTime || a.timestamp);
      const bMs = toTimestampMs(b.startTime || b.timestamp);
      return (Number.isFinite(bMs) ? bMs : 0) - (Number.isFinite(aMs) ? aMs : 0);
    })
);

const activeRealtimeCount = computed(() => realtimeLogs.value.length);

watch(
  activeRealtimeCount,
  (count) => {
    if (count > 0) {
      startDurationTimer();
      return;
    }
    stopDurationTimer();
  },
  { immediate: true }
);

onMounted(() => {
  if (activeRealtimeCount.value > 0) {
    startDurationTimer();
  }
});

onBeforeUnmount(() => {
  stopDurationTimer();
});

const recentLogsDisplay = computed((): UILogItem[] =>
  logsStore.recentLogs.map((log): UILogItem => ({ ...log, isRealtime: false }))
);

const loading = computed(() => logsStore.loading);

function handleSelectLog(log: UILogItem): void {
  emit('select-log', log);
}
</script>

<template>
  <div class="log-list-container">
    <!-- Top: Realtime Requests -->
    <div class="realtime-section">
      <div v-if="showHeader" class="section-header">
        <h3>{{ t('logs.realtime') }}</h3>
        <div class="connection-status">
          <span
            class="status-indicator"
            :class="{ connected: realtimeStore.isConnected }"
          />
          <span class="status-text">
            {{ realtimeStore.isConnected ? t('logs.wsConnected') : t('logs.wsDisconnected') }}
          </span>
        </div>
      </div>

      <div class="realtime-body">
        <div v-if="realtimeLogs.length === 0" class="realtime-empty">
          {{ t('logs.noRealtime') }}
        </div>
        <div v-else class="realtime-list">
          <div
            v-for="item in realtimeLogs"
            :key="`rt-${item.id}`"
            class="log-row"
          >
            <LogCard
              :log="item"
              :now-ms="nowMs"
              @click="handleSelectLog(item)"
            />
          </div>
        </div>
      </div>
    </div>

    <!-- Bottom: History Logs -->
    <div class="history-section">
      <div v-if="showHeader" class="section-header">
        <h3>{{ t('logs.title') }}</h3>
      </div>

      <n-spin :show="loading" class="history-body">
        <n-empty
          v-if="recentLogsDisplay.length === 0 && !loading"
          :description="t('logs.noLogs')"
          class="log-empty"
        />

        <div v-else class="log-scroll-area">
          <div
            v-for="item in recentLogsDisplay"
            :key="`log-${item.id}-${item.timestamp || ''}`"
            class="log-row"
          >
            <LogCard
              :log="item"
              :now-ms="nowMs"
              @click="handleSelectLog(item)"
            />
          </div>
        </div>
      </n-spin>
    </div>
  </div>
</template>

<style scoped>
.log-list-container {
  display: flex;
  flex-direction: column;
  flex: 1;
  min-height: 0;
  height: 100%;
  background: var(--bg-primary, #ffffff);
  border-radius: 8px;
  overflow: hidden;
}

.realtime-section {
  flex: 0 0 auto;
  border-bottom: 1px solid var(--border-color, #e0e0e0);
}

.history-section {
  flex: 1;
  min-height: 0;
  display: flex;
  flex-direction: column;
  overflow: hidden;
}

.section-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
  padding: 12px 16px;
  border-bottom: 1px solid var(--border-color, #e0e0e0);
}

.section-header h3 {
  margin: 0;
  font-size: 14px;
  font-weight: 600;
  color: var(--text-primary, #333);
}

.connection-status {
  display: flex;
  align-items: center;
  gap: 6px;
  font-size: 12px;
  color: var(--text-secondary, #666);
}

.status-indicator {
  width: 8px;
  height: 8px;
  border-radius: 50%;
  background: var(--error-color, #f56c6c);
  transition: background 0.3s ease;
}

.status-indicator.connected {
  background: var(--success-color, #67c23a);
}

.realtime-body {
  padding: 8px;
  max-height: 40vh;
  overflow-y: auto;
  overscroll-behavior: contain;
  scrollbar-width: thin;
  scrollbar-color: transparent transparent;
}

.realtime-body:hover {
  scrollbar-color: #c3d0de transparent;
}

.realtime-empty {
  padding: 12px;
  text-align: center;
  color: var(--text-secondary, #999);
  font-size: 12px;
}

.realtime-list .log-row + .log-row {
  margin-top: 8px;
}

.history-body {
  flex: 1;
  min-height: 0;
  display: flex;
  flex-direction: column;
  overflow: hidden;
}

.history-body :deep(.n-spin-content) {
  flex: 1;
  min-height: 0;
  display: flex;
  flex-direction: column;
  overflow: hidden;
}

.log-empty {
  margin: auto 0;
  padding: 40px 0;
}

.log-scroll-area {
  flex: 1;
  min-height: 0;
  overflow-x: hidden;
  overflow-y: auto !important;
  overscroll-behavior: contain;
  scrollbar-width: thin;
  scrollbar-color: transparent transparent;
  padding: 8px;
}

.log-scroll-area:hover {
  scrollbar-color: #c3d0de transparent;
}

.log-scroll-area::-webkit-scrollbar {
  width: 10px;
}

.log-scroll-area::-webkit-scrollbar-thumb {
  background: transparent;
  border-radius: 999px;
  border: 2px solid transparent;
  background-clip: content-box;
}

.log-scroll-area:hover::-webkit-scrollbar-thumb {
  background: #c3d0de;
}

.log-scroll-area::-webkit-scrollbar-track {
  background: transparent;
}

.log-row + .log-row {
  margin-top: 8px;
}
</style>
