<template>
  <div class="codex-usage-bar">
    <span v-if="label" class="usage-label">{{ label }}</span>
    <button
      v-if="refreshable"
      type="button"
      class="usage-refresh-btn"
      :title="refreshTitle"
      :aria-label="refreshTitle"
      :disabled="refreshDisabled"
      @click="emit('refresh')"
    >
      <RefreshCw class="usage-refresh-icon" />
    </button>
    <div class="usage-track">
      <div class="usage-fill" :class="`usage-fill-${usageTone}`" :style="{ width: `${percentage}%` }"></div>
    </div>
    <span class="usage-text">{{ percentage.toFixed(0) }}%</span>
    <span v-if="resetText" class="usage-reset">{{ resetText }}</span>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { RefreshCw } from 'lucide-vue-next'

const props = withDefaults(defineProps<{
  label?: string
  usedPercent?: number
  remainingSeconds?: number
  refreshable?: boolean
  refreshDisabled?: boolean
  refreshTitle?: string
}>(), {
  label: '',
  usedPercent: 0,
  remainingSeconds: 0,
  refreshable: false,
  refreshDisabled: false,
  refreshTitle: 'Refresh'
})

const emit = defineEmits<{
  refresh: []
}>()

const percentage = computed(() => {
  const value = Number(props.usedPercent) || 0
  return Math.max(0, Math.min(value, 100))
})

const usageTone = computed<'primary' | 'warning' | 'error'>(() => {
  if (percentage.value >= 90) return 'error'
  if (percentage.value >= 70) return 'warning'
  return 'primary'
})

const resetText = computed(() => {
  const remaining = props.remainingSeconds
  if (!remaining || remaining <= 0) return ''
  const d = Math.floor(remaining / 86400)
  const h = Math.floor((remaining % 86400) / 3600)
  const m = Math.floor((remaining % 3600) / 60)
  if (d > 0) return `${d}d${h > 0 ? h + 'h' : ''}`
  if (h > 0) return `${h}h${m > 0 ? m + 'm' : ''}`
  return `${m}m`
})
</script>

<style scoped>
.codex-usage-bar {
  display: flex;
  align-items: center;
  gap: 8px;
  margin-bottom: 8px;
}

.usage-label {
  font-size: 10px;
  color: var(--text-tertiary);
  min-width: 24px;
}

.usage-refresh-btn {
  width: 16px;
  height: 16px;
  border: none;
  border-radius: 4px;
  background: transparent;
  color: var(--text-tertiary);
  display: inline-flex;
  align-items: center;
  justify-content: center;
  padding: 0;
  cursor: pointer;
}

.usage-refresh-btn:hover:not(:disabled) {
  color: var(--accent, #0284c7);
  background: var(--bg-tertiary, #e2e8f0);
}

.usage-refresh-btn:disabled {
  cursor: default;
  opacity: 0.35;
}

.usage-refresh-icon {
  width: 12px;
  height: 12px;
}

.usage-track {
  flex: 1;
  height: 6px;
  background: var(--bg-tertiary, #e2e8f0);
  border-radius: 3px;
  overflow: hidden;
}

.usage-fill {
  height: 100%;
  transition: width 0.3s ease, background-color 0.3s ease;
  border-radius: 3px;
}

.usage-fill-primary {
  background: var(--accent, #0284c7);
}

.usage-fill-warning {
  background: #f59e0b;
}

.usage-fill-error {
  background: var(--danger, #dc2626);
}

.usage-text {
  font-size: 11px;
  font-weight: 600;
  color: var(--text-secondary);
  min-width: 32px;
  text-align: right;
}

.usage-reset {
  font-size: 10px;
  color: var(--text-tertiary);
}
</style>
