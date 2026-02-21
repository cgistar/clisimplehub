<template>
  <div class="codex-usage-bar">
    <span class="usage-label">{{ label }}</span>
    <div class="usage-track">
      <div class="usage-fill" :class="`usage-fill-${usageTone}`" :style="{ width: `${percentage}%` }"></div>
    </div>
    <span class="usage-text">{{ percentage.toFixed(0) }}%</span>
    <span v-if="resetText" class="usage-reset">{{ resetText }}</span>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'

const props = withDefaults(defineProps<{
  label?: string
  usedPercent?: number
  remainingSeconds?: number
}>(), {
  label: '',
  usedPercent: 0,
  remainingSeconds: 0
})

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
