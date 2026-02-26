<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { useMessage } from 'naive-ui'
import { useI18n } from 'vue-i18n'
import { Globe, Plus, RefreshCcw, Settings2, Square, TriangleAlert, Play } from 'lucide-vue-next'
import { useFeedback } from '@/composables/useFeedback'
import { useXrayStore } from '@/stores/xrayStore'
import type { XrayConfig, XraySubscription } from '@/types/xray'
import XrayAddNodesModal from './XrayAddNodesModal.vue'
import XrayConfigModal from './XrayConfigModal.vue'
import XrayNodesModal from './XrayNodesModal.vue'
import XraySubscriptionCard from './XraySubscriptionCard.vue'
import XraySubscriptionFormModal from './XraySubscriptionFormModal.vue'

const props = withDefaults(
  defineProps<{
    active?: boolean
  }>(),
  {
    active: false
  }
)

const { t } = useI18n()
const message = useMessage()
const feedback = useFeedback()
const xrayStore = useXrayStore()

const showConfigModal = ref(false)
const showSubscriptionModal = ref(false)
const showNodesModal = ref(false)
const showAddNodesModal = ref(false)
const subscriptionSaving = ref(false)
const editingSubscriptionId = ref('')
const pendingSubscriptionIds = ref<Set<string>>(new Set())
const startStopPending = ref(false)

const editingSubscription = computed<XraySubscription | null>(() => {
  if (!editingSubscriptionId.value) return null
  return xrayStore.subscriptions.find((subscription) => subscription.id === editingSubscriptionId.value) || null
})

const subscriptionModalTitle = computed(() => {
  if (editingSubscription.value) {
    return t('xray.editSub')
  }
  return t('xray.addSub')
})

const statusText = computed(() => {
  return xrayStore.status.running ? t('xray.running') : t('xray.stopped')
})

const statusClass = computed(() => {
  if (xrayStore.status.running) {
    return 'border-emerald-300 bg-emerald-100 text-emerald-700'
  }
  return 'border-slate-300 bg-slate-100 text-slate-600'
})

const currentNodesSubscriptionName = computed(() => {
  return xrayStore.currentNodesDialogSubscription?.name || xrayStore.currentNodesDialogSubscription?.id || ''
})

function toErrorMessage(error: unknown): string {
  if (error instanceof Error) return error.message
  return String(error)
}

function isSubscriptionPending(id: string): boolean {
  return pendingSubscriptionIds.value.has(id)
}

function setSubscriptionPending(id: string, pending: boolean): void {
  const next = new Set(pendingSubscriptionIds.value)
  if (pending) {
    next.add(id)
  } else {
    next.delete(id)
  }
  pendingSubscriptionIds.value = next
}

async function runWithSubscriptionPending(id: string, task: () => Promise<void>): Promise<void> {
  if (!id || isSubscriptionPending(id)) return

  setSubscriptionPending(id, true)
  try {
    await task()
  } finally {
    setSubscriptionPending(id, false)
  }
}

async function loadXrayPage(): Promise<void> {
  try {
    await xrayStore.loadAll()
  } catch (error) {
    message.error(toErrorMessage(error))
  }
}

onMounted(async () => {
  if (!props.active) return
  await loadXrayPage()
})

watch(
  () => props.active,
  async (active) => {
    if (!active) return
    await loadXrayPage()
  }
)

async function handleStartStop(): Promise<void> {
  if (startStopPending.value) return
  startStopPending.value = true

  try {
    if (xrayStore.status.running) {
      await xrayStore.stop()
    } else {
      await xrayStore.start()
    }
  } catch (error) {
    message.error(
      (xrayStore.status.running ? t('xray.stopFailed') : t('xray.startFailed')) + toErrorMessage(error)
    )
  } finally {
    startStopPending.value = false
  }
}

async function handleRefreshSubscriptions(): Promise<void> {
  try {
    const result = await xrayStore.refreshSubscriptions()
    if (result.errors?.length) {
      message.warning(result.errors.join('; '))
    }
  } catch (error) {
    message.error(t('xray.refreshFailed') + toErrorMessage(error))
  }
}

function openAddSubscription(): void {
  editingSubscriptionId.value = ''
  showSubscriptionModal.value = true
}

function openEditSubscription(id: string): void {
  editingSubscriptionId.value = id
  showSubscriptionModal.value = true
}

async function handleSubmitSubscription(payload: { name: string; url: string }): Promise<void> {
  subscriptionSaving.value = true

  try {
    const name = payload.name || 'Unnamed'
    const url = payload.url || ''

    if (editingSubscription.value) {
      await xrayStore.updateSubscription(editingSubscription.value.id, name, url)
    } else {
      await xrayStore.addSubscription(name, url)
    }

    showSubscriptionModal.value = false
    editingSubscriptionId.value = ''
  } catch (error) {
    const failedKey = editingSubscription.value ? t('xray.updateSubFailed') : t('xray.addSubFailed')
    message.error(failedKey + toErrorMessage(error))
  } finally {
    subscriptionSaving.value = false
  }
}

async function handleSetActiveSubscription(id: string): Promise<void> {
  await runWithSubscriptionPending(id, async () => {
    try {
      await xrayStore.setActiveSubscription(id)
    } catch (error) {
      message.error(t('xray.setActiveFailed') + toErrorMessage(error))
    }
  })
}

async function handleToggleSubscription(id: string): Promise<void> {
  await runWithSubscriptionPending(id, async () => {
    try {
      await xrayStore.toggleSubscription(id)
    } catch (error) {
      message.error(t('xray.toggleSubFailed') + toErrorMessage(error))
    }
  })
}

async function handleRefreshSingleSubscription(id: string): Promise<void> {
  await runWithSubscriptionPending(id, async () => {
    try {
      const result = await xrayStore.refreshSingleSubscription(id)
      if (result.errors?.length) {
        message.warning(result.errors.join('; '))
      }
    } catch (error) {
      message.error(t('xray.refreshFailed') + toErrorMessage(error))
    }
  })
}

async function handleRemoveSubscription(id: string): Promise<void> {
  await runWithSubscriptionPending(id, async () => {
    const confirmed = await feedback.confirm(t('xray.removeSubConfirm'), { danger: true })
    if (!confirmed) return

    try {
      await xrayStore.removeSubscription(id)
    } catch (error) {
      message.error(t('xray.removeSubFailed') + toErrorMessage(error))
    }
  })
}

async function handleSaveConfig(payload: XrayConfig): Promise<void> {
  try {
    await xrayStore.saveConfig(payload)
    showConfigModal.value = false
  } catch (error) {
    message.error(t('xray.configSaveFailed') + toErrorMessage(error))
  }
}

async function handleOpenNodes(subscriptionId: string): Promise<void> {
  await runWithSubscriptionPending(subscriptionId, async () => {
    try {
      await xrayStore.openSubscriptionNodesDraft(subscriptionId)
      showNodesModal.value = true
    } catch (error) {
      message.error(t('xray.subscriptionNotFound') + ': ' + toErrorMessage(error))
    }
  })
}

async function handleCloseNodesModal(): Promise<void> {
  if (xrayStore.hasUnsavedNodeDraftChanges) {
    const confirmed = await feedback.confirm(t('xray.discardNodeChangesConfirm'), { danger: true })
    if (!confirmed) return
  }

  showNodesModal.value = false
  showAddNodesModal.value = false
  xrayStore.resetNodesDraftState()
}

async function handleRefreshNodesDraftSubscription(): Promise<void> {
  if (xrayStore.hasUnsavedNodeDraftChanges) {
    const confirmed = await feedback.confirm(t('xray.discardNodeChangesConfirm'), { danger: true })
    if (!confirmed) return
  }

  try {
    const result = await xrayStore.refreshNodesDraftSubscription()
    if (result.errors?.length) {
      message.warning(result.errors.join('; '))
    }
  } catch (error) {
    message.error(t('xray.refreshFailed') + toErrorMessage(error))
  }
}

async function handleSaveNodesDraft(): Promise<void> {
  if (xrayStore.nodesDialogDraftNodes.length > 0 && !xrayStore.nodesDialogSelectedNodeName) {
    message.error(t('xray.pleaseSelectNode'))
    return
  }

  try {
    await xrayStore.saveDraftNodes()
    showNodesModal.value = false
    showAddNodesModal.value = false
  } catch (error) {
    message.error(t('xray.saveNodeFailed') + toErrorMessage(error))
  }
}

async function handleDeleteDraftNode(nodeName: string): Promise<void> {
  const confirmed = await feedback.confirm(t('xray.deleteNodeConfirm'), { danger: true })
  if (!confirmed) return

  try {
    xrayStore.deleteDraftNode(nodeName)
  } catch (error) {
    message.error(t('xray.deleteNodeFailed') + toErrorMessage(error))
  }
}

async function handleTestDraftNode(nodeName: string): Promise<void> {
  try {
    await xrayStore.testDraftNode(nodeName)
  } catch (error) {
    if (String(error).toLowerCase().includes('save before test')) {
      message.warning(t('xray.saveBeforeTest'))
      return
    }
    message.error(t('xray.testFailedShort') + ': ' + toErrorMessage(error))
  }
}

async function handleTestAllDraftNodes(): Promise<void> {
  try {
    await xrayStore.testAllDraftNodes()
  } catch (error) {
    message.error(t('xray.testFailedShort') + ': ' + toErrorMessage(error))
  }
}

async function handleCopyNodeConfig(nodeName: string): Promise<void> {
  try {
    await xrayStore.copyNodeConfig(nodeName)
    message.success(t('logs.copyToClipboard'))
  } catch (error) {
    message.error(t('xray.copyFailed') + toErrorMessage(error))
  }
}

async function handleAddNodes(content: string): Promise<void> {
  try {
    const count = await xrayStore.addNodesToDraft(content)
    if (!count) {
      message.warning(t('xray.noValidNodesParsed'))
      return
    }

    showAddNodesModal.value = false
    message.success(`${count}${t('xray.parsedNodes')}`)
  } catch (error) {
    message.error(t('xray.addNodeFailed') + toErrorMessage(error))
  }
}
</script>

<template>
  <div class="xray-page h-full min-h-0 w-full flex-1 p-3">
    <section class="flex h-full min-h-0 w-full flex-col rounded-xl border border-slate-200 bg-white shadow-sm">
      <header class="flex flex-wrap items-center justify-between gap-3 border-b border-slate-200 px-4 py-3">
        <div class="flex min-w-0 items-center gap-2">
          <span class="inline-flex h-7 w-7 items-center justify-center rounded-full bg-sky-100 text-sky-700">
            <Globe :size="16" />
          </span>
          <div>
            <h2 class="text-sm font-semibold text-slate-900">{{ t('xray.title') }}</h2>
            <div class="mt-1 flex items-center gap-2">
              <span class="inline-flex items-center rounded-full border px-2 py-0.5 text-xs" :class="statusClass">
                {{ statusText }}
              </span>
              <span v-if="xrayStore.status.running" class="truncate text-xs text-slate-600">
                SOCKS5: {{ xrayStore.status.socksAddr || '--' }}
              </span>
              <span class="truncate text-xs text-slate-600">
                {{ t('xray.node') }}: {{ xrayStore.status.selectedNode || '--' }}
              </span>
            </div>
          </div>
        </div>

        <div class="flex flex-wrap items-center gap-2">
          <button
            type="button"
            class="inline-flex items-center gap-1 rounded-md border px-3 py-1.5 text-xs disabled:cursor-not-allowed disabled:opacity-60"
            :class="xrayStore.status.running
              ? 'border-red-300 bg-red-50 text-red-700 hover:bg-red-100'
              : 'border-emerald-300 bg-emerald-50 text-emerald-700 hover:bg-emerald-100'"
            :disabled="startStopPending"
            @click="handleStartStop"
          >
            <Square v-if="xrayStore.status.running" :size="14" />
            <Play v-else :size="14" />
            {{ xrayStore.status.running ? t('xray.stop') : t('xray.start') }}
          </button>

          <button
            type="button"
            class="inline-flex items-center gap-1 rounded-md border border-slate-300 px-3 py-1.5 text-xs text-slate-700 hover:bg-slate-100"
            @click="showConfigModal = true"
          >
            <Settings2 :size="14" />
            {{ t('xray.config') }}
          </button>

          <button
            type="button"
            class="inline-flex items-center gap-1 rounded-md border border-slate-300 px-3 py-1.5 text-xs text-slate-700 hover:bg-slate-100 disabled:cursor-not-allowed disabled:opacity-60"
            :disabled="xrayStore.refreshingAll"
            @click="handleRefreshSubscriptions"
          >
            <RefreshCcw :size="14" :class="{ 'animate-spin': xrayStore.refreshingAll }" />
            {{ xrayStore.refreshingAll ? t('xray.refreshing') : t('xray.refreshSubs') }}
          </button>

          <button
            type="button"
            class="inline-flex items-center gap-1 rounded-md border border-sky-600 bg-sky-600 px-3 py-1.5 text-xs text-white hover:bg-sky-700"
            @click="openAddSubscription"
          >
            <Plus :size="14" />
            {{ t('xray.addSub') }}
          </button>
        </div>
      </header>

      <main class="flex-1 overflow-y-auto p-4">
        <div v-if="xrayStore.error" class="mb-3 rounded-md border border-amber-300 bg-amber-50 px-3 py-2 text-xs text-amber-800">
          <span class="inline-flex items-center gap-1">
            <TriangleAlert :size="14" />
            {{ xrayStore.error }}
          </span>
        </div>

        <div v-if="!xrayStore.subscriptions.length" class="rounded-md border border-dashed border-slate-300 bg-slate-50 px-4 py-8 text-center text-sm text-slate-500">
          {{ t('xray.noSubscriptions') }}
        </div>

        <div v-else class="grid grid-cols-1 gap-3 sm:grid-cols-2 xl:grid-cols-4">
          <XraySubscriptionCard
            v-for="subscription in xrayStore.subscriptions"
            :key="subscription.id"
            :subscription="subscription"
            :node-count="xrayStore.getSubscriptionNodeCount(subscription.id)"
            :selected-node-label="subscription.selectedNode || '--'"
            :refreshing="!!xrayStore.refreshingSubscriptions[subscription.id]"
            :busy="isSubscriptionPending(subscription.id)"
            @set-active="handleSetActiveSubscription"
            @toggle="handleToggleSubscription"
            @refresh="handleRefreshSingleSubscription"
            @edit="openEditSubscription"
            @manage-nodes="handleOpenNodes"
            @remove="handleRemoveSubscription"
          />
        </div>
      </main>
    </section>

    <XrayConfigModal
      v-model:show="showConfigModal"
      :config="xrayStore.config"
      :saving="xrayStore.savingConfig"
      @save="handleSaveConfig"
    />

    <XraySubscriptionFormModal
      v-model:show="showSubscriptionModal"
      :title="subscriptionModalTitle"
      :submit-text="editingSubscription ? t('common.save') : t('xray.addSub')"
      :initial-name="editingSubscription?.name || ''"
      :initial-url="editingSubscription?.url || ''"
      :saving="subscriptionSaving"
      @submit="handleSubmitSubscription"
    />

    <XrayNodesModal
      v-model:show="showNodesModal"
      :subscription-name="currentNodesSubscriptionName"
      :nodes="xrayStore.nodesDialogDraftNodes"
      :selected-node-name="xrayStore.nodesDialogSelectedNodeName"
      :dirty="xrayStore.hasUnsavedNodeDraftChanges"
      :refreshing="!!xrayStore.refreshingSubscriptions[xrayStore.nodesDialogSubscriptionId]"
      :testing-all="xrayStore.testingAllNodes"
      :saving="xrayStore.savingNodesDraft"
      :testing-node-map="xrayStore.testingNodes"
      @request-close="handleCloseNodesModal"
      @select-node="xrayStore.setDraftSelectedNode"
      @refresh="handleRefreshNodesDraftSubscription"
      @test-all="handleTestAllDraftNodes"
      @save="handleSaveNodesDraft"
      @open-add="showAddNodesModal = true"
      @delete-node="handleDeleteDraftNode"
      @copy-node="handleCopyNodeConfig"
      @test-node="handleTestDraftNode"
    />

    <XrayAddNodesModal
      v-model:show="showAddNodesModal"
      :saving="xrayStore.addingNodes"
      @submit="handleAddNodes"
    />
  </div>
</template>
