<script setup lang="ts">
import { computed } from 'vue';
import { useI18n } from 'vue-i18n';
import { NTag } from 'naive-ui';
import type { UILogItem } from '@/types/endpoint';
import { formatTimeSafe, toTimestampMs } from '@/utils/datetime';

const { t } = useI18n();

const props = defineProps<{
  log: UILogItem
  nowMs?: number
}>();

const emit = defineEmits<{
  click: []
}>();

const statusType = computed<'default' | 'success' | 'info' | 'error'>(() => {
  switch (props.log.status) {
    case 'success':
    case 'COMPLETED':
      return 'success';
    case 'error':
    case 'FAILED':
      return 'error';
    case 'in_progress':
    case 'PENDING':
    case 'STREAMING':
      return 'info';
    default:
      return 'default';
  }
});

const statusText = computed(() => {
  const status = props.log.status;
  if (status === 'COMPLETED' || status === 'success') return t('logs.done');
  if (status === 'FAILED' || status === 'error') return t('logs.failed');
  if (status === 'PENDING' || status === 'STREAMING' || status === 'in_progress') return t('logs.inProgress');
  return status;
});

const formattedTime = computed(() => {
  return formatTimeSafe(props.log.timestamp, '-');
});

const transportText = computed(() => {
  return props.log.transport === 'websocket' ? 'WebSocket' : 'HTTP';
});

const isRealtimeInProgress = computed(() => {
  const status = props.log.status;
  return (
    props.log.isRealtime &&
    (status === 'PENDING' || status === 'STREAMING' || status === 'in_progress')
  );
});

const resolvedDurationMs = computed(() => {
  const backendDuration = Number(props.log.runTime || props.log.displayDuration || 0);
  if (!isRealtimeInProgress.value) {
    return Number.isFinite(backendDuration) ? backendDuration : 0;
  }

  const startMs = toTimestampMs(props.log.startTime || props.log.timestamp);
  if (!Number.isFinite(startMs)) {
    return Number.isFinite(backendDuration) ? backendDuration : 0;
  }

  const elapsed = Math.max(0, Math.floor((props.nowMs || Date.now()) - startMs));
  return Math.max(elapsed, Number.isFinite(backendDuration) ? backendDuration : 0);
});

const formattedDuration = computed(() => {
  const ms = resolvedDurationMs.value;
  if (ms < 1000) return `${ms}ms`;
  return `${(ms / 1000).toFixed(2)}s`;
});

const endpointProviderText = computed(() => {
  const endpointName = (props.log.endpointName || '').trim();
  const providerName = (props.log.providerName || '').trim();
  const path = (props.log.path || '').trim();
  const model = (props.log.model || '').trim();

  let result = '';
  if (endpointName && providerName) {
    result = `${endpointName}(${providerName})`;
  } else if (endpointName) {
    result = endpointName;
  } else if (providerName) {
    result = providerName;
  } else {
    result = '-';
  }

  // 添加请求路径
  if (path) {
    result += ` ${path}`;
  }

  // 添加模型名称
  if (model) {
    result += ` ${model}`;
  }

  return result;
});

function handleClick(): void {
  emit('click');
}
</script>

<template>
  <div
    class="log-card"
    :class="{
      realtime: log.isRealtime,
      clickable: true
    }"
    @click="handleClick"
  >
    <div class="log-card-header">
      <div class="log-meta">
        <span class="log-time">{{ formattedTime }}</span>
        <span class="log-separator">•</span>
        <span class="log-interface-type">{{ log.interfaceType }}</span>
        <n-tag size="small" round :bordered="false" class="transport-tag">
          {{ transportText }}
        </n-tag>
        <n-tag :type="statusType" size="small" round>
          {{ statusText }}
        </n-tag>
        <span v-if="log.isRealtime" class="realtime-badge">
          {{ t('logs.live') }}
        </span>
      </div>
      <span class="log-duration" :class="{ 'log-duration-live': isRealtimeInProgress }">
        {{ formattedDuration }}
      </span>
    </div>

    <div class="log-card-body">
      <div class="log-endpoint">
        <span class="log-provider">{{ endpointProviderText }}</span>
      </div>
    </div>
  </div>
</template>

<style scoped>
.log-card {
  background: var(--bg-secondary, #f9f9f9);
  border: 1px solid var(--border-color, #e0e0e0);
  border-radius: 6px;
  padding: 12px;
  margin-bottom: 8px;
  transition: all 0.2s ease;
}

.log-card.clickable {
  cursor: pointer;
}

.log-card.clickable:hover {
  border-color: var(--primary-color, #4A90E2);
  box-shadow: 0 2px 8px rgba(74, 144, 226, 0.1);
  transform: translateY(-1px);
}

.log-card.realtime {
  border-left: 3px solid var(--primary-color, #4A90E2);
}

.log-card-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
  margin-bottom: 8px;
}

.log-meta {
  display: flex;
  align-items: center;
  gap: 8px;
}

.log-time {
  font-size: 12px;
  color: var(--text-tertiary, #999);
  font-family: 'SF Mono', 'Monaco', 'Consolas', monospace;
}

.realtime-badge {
  font-size: 10px;
  color: var(--primary-color, #4A90E2);
  font-weight: 600;
  text-transform: uppercase;
  letter-spacing: 0.5px;
}

.log-duration {
  font-size: 12px;
  color: var(--text-secondary, #666);
  font-family: 'SF Mono', 'Monaco', 'Consolas', monospace;
}

.log-duration-live {
  color: #5b59d3;
  font-weight: 600;
}

.log-endpoint {
  display: flex;
  align-items: center;
  gap: 6px;
  font-size: 13px;
}

.log-interface-type {
  font-weight: 600;
  color: var(--text-primary, #333);
  text-transform: uppercase;
  font-size: 11px;
  letter-spacing: 0.5px;
}

.transport-tag {
  font-family: 'SF Mono', 'Monaco', 'Consolas', monospace;
  font-size: 10px;
}

.log-separator {
  color: var(--text-tertiary, #999);
}

.log-provider {
  color: var(--text-secondary, #666);
}
</style>
