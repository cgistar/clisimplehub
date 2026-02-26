<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { CirclePower, Pencil, RefreshCw, Route, Trash2, Wifi, WifiOff } from 'lucide-vue-next'
import type { XraySubscription } from '@/types/xray'

const props = withDefaults(
  defineProps<{
    subscription: XraySubscription
    nodeCount: number
    selectedNodeLabel?: string
    refreshing?: boolean
    busy?: boolean
  }>(),
  {
    selectedNodeLabel: '--',
    refreshing: false,
    busy: false
  }
)

const emit = defineEmits<{
  'set-active': [id: string]
  toggle: [id: string]
  refresh: [id: string]
  edit: [id: string]
  'manage-nodes': [id: string]
  remove: [id: string]
}>()

const { t } = useI18n()

const activeBadgeClass = computed(() => {
  if (props.subscription.active) {
    return 'border-emerald-300 bg-emerald-100 text-emerald-700'
  }
  return 'border-slate-300 bg-slate-100 text-slate-600'
})

const cardClass = computed(() => {
  const classes = ['rounded-xl border p-4 transition-colors']

  if (props.subscription.active) {
    classes.push('border-sky-300 bg-sky-50/80')
  } else {
    classes.push('border-slate-200 bg-white')
  }

  if (!props.subscription.enabled) {
    classes.push('opacity-60')
  }

  return classes.join(' ')
})
</script>

<template>
  <article :class="cardClass">
    <div class="mb-3 flex items-start justify-between gap-3">
      <div class="min-w-0 flex-1">
        <h3 class="truncate text-sm font-semibold text-slate-900" :title="subscription.name || subscription.id">
          {{ subscription.name || subscription.id }}
        </h3>
        <p class="mt-1 text-xs text-slate-600">
          {{ nodeCount }} {{ t('xray.nodes') }}
        </p>
      </div>
      <span class="inline-flex shrink-0 items-center rounded-full border px-2 py-0.5 text-xs" :class="activeBadgeClass">
        {{ subscription.active ? t('xray.active') : t('xray.inactive') }}
      </span>
    </div>

    <p class="mb-3 truncate rounded-md bg-slate-50 px-2 py-1 text-xs text-slate-700" :title="selectedNodeLabel">
      {{ t('xray.selectedNode') }}: {{ selectedNodeLabel }}
    </p>

    <div class="flex flex-wrap gap-2">
      <button
        v-if="!subscription.active"
        type="button"
        class="inline-flex items-center gap-1 rounded-md border border-slate-300 px-2 py-1 text-xs text-slate-700 hover:bg-slate-100 disabled:cursor-not-allowed disabled:opacity-50"
        :title="t('xray.activate')"
        :disabled="busy"
        @click="emit('set-active', subscription.id)"
      >
        <CirclePower :size="14" />
        {{ t('xray.activate') }}
      </button>

      <button
        type="button"
        class="inline-flex items-center gap-1 rounded-md border border-slate-300 px-2 py-1 text-xs text-slate-700 hover:bg-slate-100 disabled:cursor-not-allowed disabled:opacity-50"
        :title="t('xray.manageNodes')"
        :disabled="busy"
        @click="emit('manage-nodes', subscription.id)"
      >
        <Route :size="14" />
        {{ t('xray.manageNodes') }}
      </button>

      <button
        type="button"
        class="inline-flex items-center gap-1 rounded-md border border-slate-300 px-2 py-1 text-xs text-slate-700 hover:bg-slate-100 disabled:cursor-not-allowed disabled:opacity-50"
        :title="t('xray.editSub')"
        :disabled="busy"
        @click="emit('edit', subscription.id)"
      >
        <Pencil :size="14" />
        {{ t('xray.editSub') }}
      </button>

      <button
        type="button"
        class="inline-flex items-center gap-1 rounded-md border border-slate-300 px-2 py-1 text-xs text-slate-700 hover:bg-slate-100 disabled:cursor-not-allowed disabled:opacity-50"
        :title="t('xray.refreshSub')"
        :disabled="busy || refreshing"
        @click="emit('refresh', subscription.id)"
      >
        <RefreshCw :size="14" :class="{ 'animate-spin': refreshing }" />
        {{ refreshing ? t('xray.refreshing') : t('xray.refreshSub') }}
      </button>

      <button
        type="button"
        class="inline-flex items-center gap-1 rounded-md border px-2 py-1 text-xs hover:bg-slate-100 disabled:cursor-not-allowed disabled:opacity-50"
        :class="subscription.enabled
          ? 'border-amber-300 text-amber-700 hover:bg-amber-50'
          : 'border-emerald-300 text-emerald-700 hover:bg-emerald-50'"
        :disabled="busy"
        @click="emit('toggle', subscription.id)"
      >
        <Wifi v-if="subscription.enabled" :size="14" />
        <WifiOff v-else :size="14" />
        {{ subscription.enabled ? t('xray.disable') : t('xray.enable') }}
      </button>

      <button
        type="button"
        class="inline-flex items-center gap-1 rounded-md border border-red-300 px-2 py-1 text-xs text-red-700 hover:bg-red-50 disabled:cursor-not-allowed disabled:opacity-50"
        :title="t('common.delete')"
        :disabled="busy"
        @click="emit('remove', subscription.id)"
      >
        <Trash2 :size="14" />
        {{ t('common.delete') }}
      </button>
    </div>
  </article>
</template>
