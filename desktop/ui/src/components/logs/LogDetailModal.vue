<script setup lang="ts">
import { computed, ref, watch } from 'vue';
import { useI18n } from 'vue-i18n';
import { useMessage, NCode, NTag, NButton, NIcon, NModal, NInput } from 'naive-ui';
import { Copy } from 'lucide-vue-next';
import { useLogsStore } from '@/stores/logsStore';
import { formatDateTimeSafe } from '@/utils/datetime';

const { t } = useI18n();
const message = useMessage();
const logsStore = useLogsStore();

const props = withDefaults(defineProps<{
  show?: boolean
  logId?: string | null
}>(), {
  show: false,
  logId: null
});

const emit = defineEmits<{
  'update:show': [show: boolean]
}>();

const visible = ref(false);

const logDetail = computed(() => logsStore.selectedLogDetail);
const loading = computed(() => logsStore.loading);
const expandedRequest = ref(false);
const expandedResponse = ref(false);
const requestStream = computed(() => logDetail.value?.requestStream || '');
const responseStream = computed(() => logDetail.value?.responseStream || '');
const requestHeadersText = computed(() => {
  const method = (logDetail.value?.method || '').trim();
  const targetUrl = (logDetail.value?.targetUrl || '').trim();
  const requestLine = [method, targetUrl].filter(Boolean).join(' ').trim();

  const headers = logDetail.value?.requestHeaders;
  const headerLines = headers
    ? Object.entries(headers).map(([key, value]) => `${key}: ${String(value)}`)
    : [];

  if (!requestLine && headerLines.length === 0) return '';
  if (!requestLine) return headerLines.join('\n');
  if (headerLines.length === 0) return requestLine;

  return `${requestLine}\n\n${headerLines.join('\n')}`;
});

const statusType = computed<'default' | 'success' | 'error' | 'info'>(() => {
  if (!logDetail.value) return 'default';
  const status = logDetail.value.status;
  if (status === 'success' || status === 'COMPLETED') return 'success';
  if (status === 'error' || status === 'FAILED') return 'error';
  return 'info';
});

const statusTagText = computed(() => {
  const status = (logDetail.value?.status || '').trim();
  const code = Number(logDetail.value?.statusCode || 0);
  if (!status) return '-';
  if (Number.isFinite(code) && code > 0) {
    return `${status} (${code})`;
  }
  return status;
});

const formattedTimestamp = computed(() => {
  return formatDateTimeSafe(logDetail.value?.timestamp, '-');
});

const formattedDuration = computed(() => {
  if (!logDetail.value?.runTime) return '-';
  const ms = logDetail.value.runTime;
  if (ms < 1000) return `${ms}ms`;
  return `${(ms / 1000).toFixed(2)}s`;
});

function onNegativeClick(): void {
  visible.value = false;
}

function resetExpandedState(): void {
  expandedRequest.value = false;
  expandedResponse.value = false;
}

async function copyStream(streamType: 'headers' | 'request' | 'response'): Promise<void> {
  const content =
    streamType === 'headers'
      ? requestHeadersText.value
      : streamType === 'request'
        ? requestStream.value
        : responseStream.value;
  if (!content) return;

  try {
    await navigator.clipboard.writeText(content);
    message.success(t('logs.copyToClipboard'));
  } catch (error) {
    const msg = error instanceof Error ? error.message : String(error);
    message.error(msg);
  }
}

async function loadDetail() {
  if (props.logId && visible.value) {
    await logsStore.loadLogDetail(props.logId);
  }
}

watch(() => props.show, (newVal) => {
  visible.value = !!newVal;
  if (newVal) {
    resetExpandedState();
    void loadDetail();
  } else {
    resetExpandedState();
  }
}, { immediate: true });

watch(() => props.logId, () => {
  resetExpandedState();
  if (visible.value) {
    void loadDetail();
  }
});

watch(visible, (newVal) => {
  if (newVal !== !!props.show) {
    emit('update:show', newVal);
  }
});
</script>

<template>
  <n-modal
    v-model:show="visible"
    preset="dialog"
    :title="t('logs.detailTitle')"
    :style="{ width: 'min(960px, 92vw)' }"
    :negative-text="t('common.close')"
    :closable="true"
    :mask-closable="false"
    :close-on-esc="true"
    :block-scroll="true"
    @close="visible = false"
    @negative-click="onNegativeClick"
  >
    <div class="detail-panel">
      <div v-if="loading" class="detail-loading">{{ t('common.loading') }}</div>

      <div v-else-if="logDetail" class="detail-body">
        <div class="detail-kv-grid">
          <div class="kv-key">{{ t('logs.statusLabel') }}</div>
          <div class="kv-value">
            <n-tag :type="statusType" size="small">
              {{ statusTagText }}
            </n-tag>
          </div>

          <div class="kv-key">{{ t('logs.duration') }}</div>
          <div class="kv-value">{{ formattedDuration }}</div>

          <div class="kv-key">{{ t('logs.startTime') }}</div>
          <div class="kv-value">{{ formattedTimestamp }}</div>

          <div class="kv-key">{{ t('logs.service') }}</div>
          <div class="kv-value">{{ logDetail.interfaceType }}</div>

          <div class="kv-key">{{ t('logs.vendor') }}</div>
          <div class="kv-value">{{ logDetail.providerName || logDetail.endpointName }}</div>

          <div class="kv-key">{{ t('logs.path') }}</div>
          <div class="kv-value">
            <n-code :code="logDetail.path || '-'" />
          </div>
        </div>

        <div class="stream-sections">
          <div v-if="requestHeadersText" class="stream-section">
            <div class="stream-header">
              <span class="stream-title">{{ t('logs.requestHeaders') }}</span>
              <n-button
                quaternary
                circle
                size="tiny"
                :title="t('common.copy')"
                :aria-label="t('common.copy')"
                @click="copyStream('headers')"
              >
                <template #icon>
                  <n-icon><Copy :size="14" /></n-icon>
                </template>
              </n-button>
            </div>
            <div class="stream-content-wrap stream-content-wrap--headers">
              <n-input
                type="textarea"
                :value="requestHeadersText"
                readonly
                :autosize="false"
              />
            </div>
          </div>

          <div v-if="requestStream" class="stream-section">
            <div class="stream-header">
              <button class="stream-toggle" @click="expandedRequest = !expandedRequest">
                <span>{{ t('logs.request') }}</span>
                <span class="stream-toggle-state">
                  {{ expandedRequest ? t('logs.collapse') : t('logs.expand') }}
                </span>
              </button>
              <n-button
                quaternary
                circle
                size="tiny"
                :title="t('common.copy')"
                :aria-label="t('common.copy')"
                @click="copyStream('request')"
              >
                <template #icon>
                  <n-icon><Copy :size="14" /></n-icon>
                </template>
              </n-button>
            </div>
            <div v-show="expandedRequest" class="stream-content-wrap">
              <n-input
                type="textarea"
                :value="requestStream"
                readonly
                :autosize="false"
              />
            </div>
          </div>

          <div v-if="responseStream" class="stream-section">
            <div class="stream-header">
              <button class="stream-toggle" @click="expandedResponse = !expandedResponse">
                <span>{{ t('logs.response') }}</span>
                <span class="stream-toggle-state">
                  {{ expandedResponse ? t('logs.collapse') : t('logs.expand') }}
                </span>
              </button>
              <n-button
                quaternary
                circle
                size="tiny"
                :title="t('common.copy')"
                :aria-label="t('common.copy')"
                @click="copyStream('response')"
              >
                <template #icon>
                  <n-icon><Copy :size="14" /></n-icon>
                </template>
              </n-button>
            </div>
            <div v-show="expandedResponse" class="stream-content-wrap">
              <n-input
                type="textarea"
                :value="responseStream"
                readonly
                :autosize="false"
              />
            </div>
          </div>
        </div>
      </div>

      <div v-else class="detail-empty">
        {{ t('logs.noLogs') }}
      </div>
    </div>
  </n-modal>
</template>

<style scoped>
:deep(.n-code) {
  font-size: 12px;
  font-family: 'SF Mono', 'Monaco', 'Consolas', monospace;
}

.detail-panel {
  padding: 0;
  overflow: hidden;
  max-height: calc(88vh - 120px);
  display: flex;
}

.detail-loading {
  flex: 1;
  min-height: 220px;
  display: flex;
  align-items: center;
  justify-content: center;
  color: var(--text-secondary);
}

.detail-body {
  flex: 1;
  min-height: 0;
  overflow: auto;
  overscroll-behavior: contain;
  padding-right: 4px;
  scrollbar-width: thin;
  scrollbar-color: var(--text-tertiary) transparent;
}

.detail-kv-grid {
  display: grid;
  grid-template-columns: 120px minmax(180px, 1fr) 120px minmax(180px, 1fr);
  border: 1px solid var(--border-color);
  border-radius: 8px;
  overflow: hidden;
}

.kv-key,
.kv-value {
  padding: 9px 12px;
  border-bottom: 1px solid var(--border-color);
}

.kv-key {
  background: var(--bg-secondary);
  color: var(--text-secondary);
  font-size: 12px;
  font-weight: 600;
}

.kv-value {
  color: var(--text-primary);
  font-size: 12px;
  word-break: break-word;
}

@media (max-width: 900px) {
  .detail-kv-grid {
    grid-template-columns: 100px 1fr;
  }
}

.detail-empty {
  flex: 1;
  min-height: 220px;
  display: flex;
  align-items: center;
  justify-content: center;
  color: var(--text-tertiary);
}

.detail-body::-webkit-scrollbar {
  width: 8px;
  height: 8px;
}

.detail-body::-webkit-scrollbar-thumb {
  background: var(--text-tertiary);
  border-radius: 8px;
}

.detail-body::-webkit-scrollbar-track {
  background: transparent;
}

.stream-sections {
  margin-top: 12px;
  display: flex;
  flex-direction: column;
  gap: 10px;
}

.stream-section {
  border: 1px solid var(--border-color);
  border-radius: 8px;
  overflow: hidden;
}

.stream-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
  padding: 8px 10px;
  background: var(--bg-secondary);
}

.stream-toggle {
  flex: 1;
  display: flex;
  justify-content: space-between;
  align-items: center;
  border: none;
  background: transparent;
  color: var(--text-primary);
  font-size: 13px;
  font-weight: 600;
  cursor: pointer;
  text-align: left;
}

.stream-title {
  font-size: 13px;
  font-weight: 600;
  color: var(--text-primary);
}

.stream-toggle-state {
  font-size: 12px;
  color: var(--text-secondary);
  font-weight: 500;
}

.stream-content-wrap {
  min-height: 280px;
  height: clamp(280px, 38vh, 520px);
  max-height: 62vh;
  overflow: hidden;
  border-top: 1px solid var(--border-color);
  background: var(--bg-primary);
  overscroll-behavior: contain;
  display: flex;
}

.stream-content-wrap--headers {
  min-height: 140px;
  height: clamp(140px, 19vh, 260px);
  max-height: 31vh;
}
</style>
