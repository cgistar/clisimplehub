<script setup lang="ts">
import type { LogInst } from 'naive-ui'
import { computed, nextTick, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { NButton, NLog, NSelect } from 'naive-ui'
import { X } from 'lucide-vue-next'
import { CONSOLE_LOG_LEVELS, useConsole } from '@/composables/useConsole'

const { t } = useI18n()
const {
  panelVisible,
  currentLogLevel,
  renderedLogs,
  setLogLevel,
  clearLogs,
  copyRenderedLogs,
  toggleBottomConsole,
  closeBottomConsole
} = useConsole()

const logInstRef = ref<LogInst | null>(null)
const copySucceeded = ref(false)
const logViewKey = ref(0)

const logLevelOptions = computed(() => [
  { label: `🔍 ${t('console.levels.debug')}`, value: CONSOLE_LOG_LEVELS.DEBUG },
  { label: `ℹ️ ${t('console.levels.info')}`, value: CONSOLE_LOG_LEVELS.INFO },
  { label: `⚠️ ${t('console.levels.warn')}`, value: CONSOLE_LOG_LEVELS.WARN },
  { label: `❌ ${t('console.levels.error')}`, value: CONSOLE_LOG_LEVELS.ERROR }
])

const selectedLogLevel = computed<number>({
  get: () => currentLogLevel.value,
  set: (value) => setLogLevel(value)
})

function scrollToBottom(silent = true): void {
  logInstRef.value?.scrollTo({ position: 'bottom', silent })
}

watch(
  () => (panelVisible.value ? renderedLogs.value : null),
  async (nextLogs) => {
    if (nextLogs === null) return
    await nextTick()
    scrollToBottom(true)
  }
)

watch(panelVisible, async (visible) => {
  if (!visible) return
  await nextTick()
  scrollToBottom(true)
})

async function handleCopy(): Promise<void> {
  const copied = await copyRenderedLogs()
  if (!copied) return

  copySucceeded.value = true
  window.setTimeout(() => {
    copySucceeded.value = false
  }, 1200)
}

function handleClear(): void {
  clearLogs()
  // Force remount to avoid stale internal render cache in NLog.
  logViewKey.value += 1
}
</script>

<template>
  <div class="bottom-panel" :class="{ expanded: panelVisible, collapsed: !panelVisible }">
    <button
      v-if="!panelVisible"
      class="console-collapsed-toggle"
      :title="t('console.expand')"
      @click="toggleBottomConsole"
    >
      <span class="console-collapsed-title">{{ t('console.title') }}</span>
      <span class="console-collapsed-action">{{ t('console.expand') }}</span>
    </button>

    <div v-else class="card console-card">
      <div class="console-card-header">
        <div class="console-header-left">
          <h2>🖥️ {{ t('console.title') }}</h2>
        </div>
        <div class="console-header-right">
          <n-select
            v-model:value="selectedLogLevel"
            class="console-level-select"
            size="small"
            :options="logLevelOptions"
          />
          <n-button size="small" secondary :title="t('console.copy')" @click="handleCopy">
            {{ copySucceeded ? '✅' : '📋' }}
          </n-button>
          <n-button size="small" secondary :title="t('console.clear')" @click="handleClear">
            🗑️
          </n-button>
          <n-button size="small" quaternary circle :title="t('common.close')" @click="closeBottomConsole">
            <X :size="14" :stroke-width="2" />
          </n-button>
        </div>
      </div>
      <div class="console-panel">
        <n-log
          :key="logViewKey"
          ref="logInstRef"
          class="console-log"
          :log="renderedLogs"
          language="naive-log"
          trim
        />
        <div v-if="!renderedLogs" class="console-placeholder">{{ t('console.placeholder') }}</div>
      </div>
    </div>
  </div>
</template>

<style scoped>
.bottom-panel {
  flex: 0 0 auto;
  border-top: 1px solid var(--border-light);
  background: linear-gradient(180deg, color-mix(in srgb, var(--bg-secondary) 82%, white) 0%, var(--bg-app) 100%);
  color: var(--text-primary);
}

.bottom-panel.collapsed {
  padding: 0 16px;
}

.bottom-panel.expanded {
  height: min(44vh, 360px);
  max-height: 44vh;
  overflow: hidden;
  padding: 10px 16px 14px;
}

.console-collapsed-toggle {
  width: 100%;
  height: 38px;
  border: 0;
  background: transparent;
  color: var(--text-secondary);
  display: flex;
  align-items: center;
  justify-content: space-between;
  cursor: pointer;
  padding: 0;
}

.console-collapsed-toggle:hover {
  color: var(--accent);
}

.console-collapsed-title {
  font-size: 13px;
  font-weight: 600;
}

.console-collapsed-action {
  font-size: 12px;
  color: var(--text-tertiary);
}

.console-card {
  height: 100%;
  min-height: 0;
  border-color: var(--border-light);
  background: var(--bg-primary);
  box-shadow: var(--shadow-sm);
}

.console-card-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
  gap: 10px;
  padding: 14px 16px;
  border-bottom: 1px solid var(--border-light);
  background: linear-gradient(180deg, #ffffff 0%, var(--bg-secondary) 100%);
}

.console-header-left h2 {
  margin: 0;
  font-size: 15px;
  line-height: 1.2;
  font-weight: 700;
  color: var(--text-primary);
}

.console-header-right {
  display: flex;
  align-items: center;
  gap: 8px;
}

.console-level-select {
  min-width: 154px;
}

.console-panel {
  flex: 1;
  min-height: 0;
  padding: 12px 14px 14px;
  position: relative;
}

.console-log {
  height: 100%;
  min-height: 170px;
  border: 1px solid var(--border-color);
  border-radius: 10px;
  background: color-mix(in srgb, var(--bg-tertiary) 68%, white);
}

.console-log :deep(.n-log-lines) {
  color: var(--text-primary);
  font-family: ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, "Liberation Mono", "Courier New", monospace;
  font-size: 12px;
  line-height: 1.45;
}

.console-log :deep(.n-log-line) {
  color: inherit;
}

.console-placeholder {
  position: absolute;
  top: 26px;
  left: 28px;
  right: 28px;
  pointer-events: none;
  user-select: none;
  color: var(--text-tertiary);
  font-size: 12px;
}

.console-log :deep(.n-log-scrollbar-container) {
  color: var(--text-primary);
}

@media (max-width: 900px) {
  .bottom-panel.collapsed {
    padding: 0 12px;
  }

  .bottom-panel.expanded {
    padding: 10px 12px 12px;
  }

  .console-header-right {
    flex-wrap: wrap;
    justify-content: flex-end;
  }

  .console-level-select {
    min-width: 140px;
  }
}
</style>
