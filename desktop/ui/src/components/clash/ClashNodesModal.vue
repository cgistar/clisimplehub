<script setup lang="ts">
import { computed } from 'vue'
import { NModal } from 'naive-ui'
import { useI18n } from 'vue-i18n'
import { Check, ClipboardCopy, Plus, RefreshCw, Save, Trash2, Zap } from 'lucide-vue-next'
import type { ClashDraftNode } from '@/types/clash'

const props = withDefaults(
  defineProps<{
    show: boolean
    subscriptionName?: string
    nodes: ClashDraftNode[]
    selectedNodeName?: string
    dirty?: boolean
    refreshing?: boolean
    testingAll?: boolean
    testingAllTCP?: boolean
    saving?: boolean
    testingNodeMap?: Record<string, boolean>
    testingNodeTCPMap?: Record<string, boolean>
  }>(),
  {
    subscriptionName: '',
    selectedNodeName: '',
    dirty: false,
    refreshing: false,
    testingAll: false,
    testingAllTCP: false,
    saving: false,
    testingNodeMap: () => ({}),
    testingNodeTCPMap: () => ({})
  }
)

const emit = defineEmits<{
  'update:show': [value: boolean]
  'request-close': []
  'select-node': [name: string]
  refresh: []
  'test-all': []
  'test-all-tcp': []
  save: []
  'open-add': []
  'delete-node': [name: string]
  'copy-node': [name: string]
  'test-node': [name: string]
  'test-node-tcp': [name: string]
}>()

const { t } = useI18n()

const title = computed(() => {
  const name = props.subscriptionName || '--'
  return `${t('clash.manageNodes')} · ${name}`
})

function close(): void {
  emit('request-close')
}

function handleShowUpdate(value: boolean): void {
  if (value) {
    emit('update:show', true)
    return
  }
  close()
}

function latencyClass(node: ClashDraftNode): string {
  if (typeof node.latency !== 'number') return 'text-slate-500'
  if (node.latency < 0) return 'text-red-600'
  if (node.latency > 0 && node.latency < 200) return 'text-emerald-600'
  if (node.latency >= 200 && node.latency < 500) return 'text-amber-600'
  if (node.latency >= 500) return 'text-red-600'
  return 'text-slate-500'
}

function latencyText(node: ClashDraftNode): string {
  if (typeof node.latency !== 'number' || node.latency === 0) return '--'
  if (node.latency < 0) return t('clash.testFailedShort')
  return `${node.latency}ms`
}

function isNodeTesting(nodeName: string): boolean {
  return !!props.testingNodeMap[nodeName]
}

function isNodeTestingTCP(nodeName: string): boolean {
  return !!props.testingNodeTCPMap[nodeName]
}
</script>

<template>
  <n-modal
    :show="show"
    :mask-closable="false"
    :close-on-esc="true"
    @update:show="handleShowUpdate"
  >
    <div class="mx-auto mt-[6vh] flex h-[86vh] w-[96vw] max-w-6xl flex-col rounded-xl border border-slate-200 bg-white shadow-xl">
      <div class="flex items-center justify-between border-b border-slate-200 px-5 py-4">
        <div>
          <h3 class="text-base font-semibold text-slate-900">{{ title }}</h3>
          <p v-if="dirty" class="mt-1 text-xs text-amber-700">{{ t('clash.unsavedNode') }}</p>
        </div>
        <button type="button" class="rounded px-2 py-1 text-slate-500 hover:bg-slate-100" @click="close">×</button>
      </div>

      <div class="flex flex-wrap items-center gap-2 border-b border-slate-200 px-5 py-3">
        <button
          type="button"
          class="inline-flex items-center gap-1 rounded-md border border-sky-600 bg-sky-600 px-3 py-1.5 text-xs text-white hover:bg-sky-700"
          @click="emit('open-add')"
        >
          <Plus :size="14" />
          {{ t('clash.addNode') }}
        </button>

        <button
          type="button"
          class="inline-flex items-center gap-1 rounded-md border border-slate-300 px-3 py-1.5 text-xs text-slate-700 hover:bg-slate-100 disabled:cursor-not-allowed disabled:opacity-60"
          :disabled="refreshing"
          @click="emit('refresh')"
        >
          <RefreshCw :size="14" :class="{ 'animate-spin': refreshing }" />
          {{ refreshing ? t('clash.refreshing') : t('clash.refreshSub') }}
        </button>

        <button
          type="button"
          class="inline-flex items-center gap-1 rounded-md border border-slate-300 px-3 py-1.5 text-xs text-slate-700 hover:bg-slate-100 disabled:cursor-not-allowed disabled:opacity-60"
          :disabled="testingAll"
          @click="emit('test-all')"
        >
          <Zap :size="14" />
          {{ testingAll ? t('clash.testing') : t('clash.testAll') }}
        </button>

        <button
          type="button"
          class="inline-flex items-center gap-1 rounded-md border border-slate-300 px-3 py-1.5 text-xs text-slate-700 hover:bg-slate-100 disabled:cursor-not-allowed disabled:opacity-60"
          :disabled="testingAllTCP"
          @click="emit('test-all-tcp')"
        >
          <Zap :size="14" />
          {{ testingAllTCP ? t('clash.testing') : t('clash.testAllTCP') }}
        </button>
      </div>

      <div class="flex-1 overflow-y-auto p-5">
        <div v-if="!nodes.length" class="rounded-md border border-dashed border-slate-300 bg-slate-50 px-4 py-8 text-center text-sm text-slate-500">
          {{ t('clash.noNodes') }}
        </div>

        <div v-else class="grid grid-cols-3 gap-3 2xl:grid-cols-4">
          <article
            v-for="node in nodes"
            :key="node.name"
            class="cursor-pointer rounded-lg border p-3 transition-colors"
            :class="node.name === selectedNodeName
              ? 'border-sky-400 bg-sky-50'
              : 'border-slate-200 bg-white hover:border-slate-300'"
            @click="emit('select-node', node.name)"
          >
            <div class="mb-2 flex items-start justify-between gap-2">
              <div class="min-w-0">
                <h4 class="truncate text-sm font-medium text-slate-900" :title="node.name">{{ node.name }}</h4>
                <p class="text-xs text-slate-500">
                  {{ node.type }}
                  <span v-if="node._draftAdded"> · {{ t('clash.unsavedNode') }}</span>
                </p>
              </div>

              <div
                v-if="node.name === selectedNodeName"
                class="inline-flex shrink-0 items-center gap-1 rounded-full bg-sky-100 px-2 py-0.5 text-xs text-sky-700"
              >
                <Check :size="13" />
                {{ t('clash.selected') }}
              </div>
            </div>

            <p class="truncate text-xs text-slate-600" :title="`${node.server}:${node.port}`">
              {{ node.server }}:{{ node.port }}
            </p>

            <div class="mt-1 flex items-center justify-between gap-2">
              <p class="text-xs font-medium" :class="latencyClass(node)">
                {{ latencyText(node) }}
              </p>

              <div class="flex items-center justify-end gap-1">
                <button
                  type="button"
                  class="rounded border border-slate-300 p-1 text-slate-600 hover:bg-slate-100"
                  :title="t('clash.copyConfig')"
                  @click.stop="emit('copy-node', node.name)"
                >
                  <ClipboardCopy :size="14" />
                </button>

                <button
                  type="button"
                  class="rounded border border-red-300 p-1 text-red-700 hover:bg-red-50"
                  :title="t('clash.deleteNode')"
                  @click.stop="emit('delete-node', node.name)"
                >
                  <Trash2 :size="14" />
                </button>

                <button
                  type="button"
                  class="rounded border border-slate-300 p-1 text-slate-600 hover:bg-slate-100 disabled:cursor-not-allowed disabled:opacity-50"
                  :disabled="node._draftAdded || isNodeTesting(node.name)"
                  :title="node._draftAdded ? t('clash.saveBeforeTest') : t('clash.test')"
                  @click.stop="emit('test-node', node.name)"
                >
                  <Zap :size="14" :class="{ 'animate-pulse': isNodeTesting(node.name) }" />
                </button>

                <button
                  type="button"
                  class="rounded border border-slate-300 p-1 text-slate-600 hover:bg-slate-100 disabled:cursor-not-allowed disabled:opacity-50"
                  :disabled="node._draftAdded || isNodeTestingTCP(node.name)"
                  :title="node._draftAdded ? t('clash.saveBeforeTest') : t('clash.testTCP')"
                  @click.stop="emit('test-node-tcp', node.name)"
                >
                  <Zap :size="14" :class="{ 'animate-pulse': isNodeTestingTCP(node.name) }" />
                </button>
              </div>
            </div>
          </article>
        </div>
      </div>

      <div class="flex justify-end gap-2 border-t border-slate-200 px-5 py-3">
        <button
          type="button"
          class="rounded-md border border-slate-300 px-3 py-1.5 text-sm text-slate-700 hover:bg-slate-100"
          @click="close"
        >
          {{ t('common.cancel') }}
        </button>
        <button
          type="button"
          class="inline-flex items-center gap-1 rounded-md border border-sky-600 bg-sky-600 px-3 py-1.5 text-sm text-white hover:bg-sky-700 disabled:cursor-not-allowed disabled:opacity-60"
          :disabled="saving"
          @click="emit('save')"
        >
          <Save :size="14" />
          {{ saving ? '...' : t('common.save') }}
        </button>
      </div>
    </div>
  </n-modal>
</template>
