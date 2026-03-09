<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { useMessage } from 'naive-ui'
import { useI18n } from 'vue-i18n'
import { Globe, Plus, RefreshCcw, Settings2, Square, TriangleAlert, Play, Copy } from 'lucide-vue-next'
import { useFeedback } from '@/composables/useFeedback'
import { useClashStore } from '@/stores/clashStore'
import type { ClashConfig, ClashSubscription } from '@/types/clash'
import ClashAddNodesModal from './ClashAddNodesModal.vue'
import ClashConfigModal from './ClashConfigModal.vue'
import ClashNodesModal from './ClashNodesModal.vue'
import ClashSubscriptionCard from './ClashSubscriptionCard.vue'
import ClashSubscriptionFormModal from './ClashSubscriptionFormModal.vue'

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
const clashStore = useClashStore()

const showConfigModal = ref(false)
const showSubscriptionModal = ref(false)
const showNodesModal = ref(false)
const showAddNodesModal = ref(false)
const subscriptionSaving = ref(false)
const editingSubscriptionId = ref('')
const pendingSubscriptionIds = ref<Set<string>>(new Set())
const startStopPending = ref(false)

const editingSubscription = computed<ClashSubscription | null>(() => {
  if (!editingSubscriptionId.value) return null
  return clashStore.subscriptions.find((subscription) => subscription.id === editingSubscriptionId.value) || null
})

const subscriptionModalTitle = computed(() => {
  if (editingSubscription.value) {
    return t('clash.editSub')
  }
  return t('clash.addSub')
})

const statusText = computed(() => {
  return clashStore.status.running ? t('clash.running') : t('clash.stopped')
})

const statusClass = computed(() => {
  if (clashStore.status.running) {
    return 'border-emerald-300 bg-emerald-100 text-emerald-700'
  }
  return 'border-slate-300 bg-slate-100 text-slate-600'
})

const currentNodesSubscriptionName = computed(() => {
  return clashStore.currentNodesDialogSubscription?.name || clashStore.currentNodesDialogSubscription?.id || ''
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

async function loadClashPage(): Promise<void> {
  try {
    await clashStore.loadAll()
  } catch (error) {
    message.error(toErrorMessage(error))
  }
}

onMounted(async () => {
  if (!props.active) return
  await loadClashPage()
})

watch(
  () => props.active,
  async (active) => {
    if (!active) return
    await loadClashPage()
  }
)

async function handleStartStop(): Promise<void> {
  if (startStopPending.value) return
  startStopPending.value = true

  try {
    if (clashStore.status.running) {
      await clashStore.stop()
    } else {
      await clashStore.start()
    }
  } catch (error) {
    message.error(
      (clashStore.status.running ? t('clash.stopFailed') : t('clash.startFailed')) + toErrorMessage(error)
    )
  } finally {
    startStopPending.value = false
  }
}

async function handleRefreshSubscriptions(): Promise<void> {
  try {
    const result = await clashStore.refreshSubscriptions()
    if (result.errors?.length) {
      message.warning(result.errors.join('; '))
    }
  } catch (error) {
    message.error(t('clash.refreshFailed') + toErrorMessage(error))
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
      await clashStore.updateSubscription(editingSubscription.value.id, name, url)
    } else {
      await clashStore.addSubscription(name, url)
    }

    showSubscriptionModal.value = false
    editingSubscriptionId.value = ''
  } catch (error) {
    const failedKey = editingSubscription.value ? t('clash.updateSubFailed') : t('clash.addSubFailed')
    message.error(failedKey + toErrorMessage(error))
  } finally {
    subscriptionSaving.value = false
  }
}

async function handleSetActiveSubscription(id: string): Promise<void> {
  await runWithSubscriptionPending(id, async () => {
    try {
      await clashStore.setActiveSubscription(id)
    } catch (error) {
      message.error(t('clash.setActiveFailed') + toErrorMessage(error))
    }
  })
}

async function handleToggleDialerProxy(id: string): Promise<void> {
  await runWithSubscriptionPending(id, async () => {
    try {
      const current = String(clashStore.config.dialerProxyId || '')
      const next = current === id ? '' : id
      await clashStore.setDialerProxySubscription(next)
    } catch (error) {
      message.error(t('clash.setDialerProxyFailed') + toErrorMessage(error))
    }
  })
}

async function handleToggleSubscription(id: string): Promise<void> {
  await runWithSubscriptionPending(id, async () => {
    try {
      await clashStore.toggleSubscription(id)
    } catch (error) {
      message.error(t('clash.toggleSubFailed') + toErrorMessage(error))
    }
  })
}

async function handleRefreshSingleSubscription(id: string): Promise<void> {
  await runWithSubscriptionPending(id, async () => {
    try {
      const result = await clashStore.refreshSingleSubscription(id)
      if (result.errors?.length) {
        message.warning(result.errors.join('; '))
      }
    } catch (error) {
      message.error(t('clash.refreshFailed') + toErrorMessage(error))
    }
  })
}

async function handleRemoveSubscription(id: string): Promise<void> {
  await runWithSubscriptionPending(id, async () => {
    const confirmed = await feedback.confirm(t('clash.removeSubConfirm'), { danger: true })
    if (!confirmed) return

    try {
      await clashStore.removeSubscription(id)
    } catch (error) {
      message.error(t('clash.removeSubFailed') + toErrorMessage(error))
    }
  })
}

async function handleSaveConfig(payload: ClashConfig): Promise<void> {
  try {
    await clashStore.saveConfig(payload)
    showConfigModal.value = false
  } catch (error) {
    message.error(t('clash.configSaveFailed') + toErrorMessage(error))
  }
}

async function handleOpenNodes(subscriptionId: string): Promise<void> {
  await runWithSubscriptionPending(subscriptionId, async () => {
    try {
      await clashStore.openSubscriptionNodesDraft(subscriptionId)
      showNodesModal.value = true
    } catch (error) {
      message.error(t('clash.subscriptionNotFound') + ': ' + toErrorMessage(error))
    }
  })
}

async function handleCloseNodesModal(): Promise<void> {
  if (clashStore.hasUnsavedNodeDraftChanges) {
    const confirmed = await feedback.confirm(t('clash.discardNodeChangesConfirm'), { danger: true })
    if (!confirmed) return
  }

  await clashStore.cancelSpeedTests()
  showNodesModal.value = false
  showAddNodesModal.value = false
  clashStore.resetNodesDraftState()
}

async function handleRefreshNodesDraftSubscription(): Promise<void> {
  if (clashStore.hasUnsavedNodeDraftChanges) {
    const confirmed = await feedback.confirm(t('clash.discardNodeChangesConfirm'), { danger: true })
    if (!confirmed) return
  }

  try {
    const result = await clashStore.refreshNodesDraftSubscription()
    if (result.errors?.length) {
      message.warning(result.errors.join('; '))
    }
  } catch (error) {
    message.error(t('clash.refreshFailed') + toErrorMessage(error))
  }
}

async function handleSaveNodesDraft(): Promise<void> {
  if (clashStore.nodesDialogDraftNodes.length > 0 && !clashStore.nodesDialogSelectedNodeName) {
    message.error(t('clash.pleaseSelectNode'))
    return
  }

  try {
    await clashStore.cancelSpeedTests()
    await clashStore.saveDraftNodes()
    showNodesModal.value = false
    showAddNodesModal.value = false
  } catch (error) {
    message.error(t('clash.saveNodeFailed') + toErrorMessage(error))
  }
}

async function handleDeleteDraftNode(nodeName: string): Promise<void> {
  const confirmed = await feedback.confirm(t('clash.deleteNodeConfirm'), { danger: true })
  if (!confirmed) return

  try {
    clashStore.deleteDraftNode(nodeName)
  } catch (error) {
    message.error(t('clash.deleteNodeFailed') + toErrorMessage(error))
  }
}

async function handleTestDraftNode(nodeName: string): Promise<void> {
  try {
    await clashStore.testDraftNode(nodeName)
  } catch (error) {
    if (String(error).toLowerCase().includes('save before test')) {
      message.warning(t('clash.saveBeforeTest'))
      return
    }
    message.error(t('clash.testFailedShort') + ': ' + toErrorMessage(error))
  }
}

async function handleTestAllDraftNodes(): Promise<void> {
  try {
    await clashStore.testAllDraftNodes()
  } catch (error) {
    message.error(t('clash.testFailedShort') + ': ' + toErrorMessage(error))
  }
}

async function handleTestDraftNodeTCP(nodeName: string): Promise<void> {
  try {
    await clashStore.testDraftNodeTCP(nodeName)
  } catch (error) {
    if (String(error).toLowerCase().includes('save before test')) {
      message.warning(t('clash.saveBeforeTest'))
      return
    }
    message.error(t('clash.testFailedShort') + ': ' + toErrorMessage(error))
  }
}

async function handleTestAllDraftNodesTCP(): Promise<void> {
  try {
    await clashStore.testAllDraftNodesTCP()
  } catch (error) {
    message.error(t('clash.testFailedShort') + ': ' + toErrorMessage(error))
  }
}

async function handleCopyNodeConfig(nodeName: string): Promise<void> {
  try {
    await clashStore.copyNodeConfig(nodeName)
    message.success(t('logs.copyToClipboard'))
  } catch (error) {
    message.error(t('clash.copyFailed') + toErrorMessage(error))
  }
}

async function handleAddNodes(content: string): Promise<void> {
  try {
    const count = await clashStore.addNodesToDraft(content)
    if (!count) {
      message.warning(t('clash.noValidNodesParsed'))
      return
    }

    showAddNodesModal.value = false
    message.success(`${count}${t('clash.parsedNodes')}`)
  } catch (error) {
    message.error(t('clash.addNodeFailed') + toErrorMessage(error))
  }
}

async function handleCopyProxyAddress(): Promise<void> {
  if (!clashStore.status.socksAddr) return

  try {
    const proxyUrl = `socks5://${clashStore.status.socksAddr}`
    await navigator.clipboard.writeText(proxyUrl)
    message.success(t('logs.copyToClipboard'))
  } catch (error) {
    message.error(t('clash.copyFailed') + toErrorMessage(error))
  }
}
</script>

<template>
  <div class="clash-page h-full min-h-0 w-full flex-1 p-3">
    <section class="flex h-full min-h-0 w-full flex-col rounded-xl border border-slate-200 bg-white shadow-sm">
      <header class="flex flex-wrap items-center justify-between gap-3 border-b border-slate-200 px-4 py-3">
        <div class="flex min-w-0 items-center gap-2">
          <span class="inline-flex h-7 w-7 items-center justify-center rounded-full bg-sky-100 text-sky-700">
            <Globe :size="16" />
          </span>
          <div>
            <h2 class="text-sm font-semibold text-slate-900">{{ t('clash.title') }}</h2>
            <div class="mt-1 flex items-center gap-2">
              <span class="inline-flex items-center rounded-full border px-2 py-0.5 text-xs" :class="statusClass">
                {{ statusText }}
              </span>
              <span v-if="clashStore.status.running" class="flex items-center gap-1 truncate text-xs text-slate-600">
                <span>SOCKS5://{{ clashStore.status.socksAddr || '--' }}</span>
                <button
                  v-if="clashStore.status.socksAddr"
                  type="button"
                  class="inline-flex items-center rounded p-0.5 text-slate-500 hover:bg-slate-100 hover:text-slate-700"
                  @click="handleCopyProxyAddress"
                  title="复制代理地址"
                >
                  <Copy :size="12" />
                </button>
              </span>
              <span class="truncate text-xs text-slate-600">
                {{ t('clash.node') }}: {{ clashStore.status.selectedNode || '--' }}
              </span>
            </div>
          </div>
        </div>

        <div class="flex flex-wrap items-center gap-2">
          <button
            type="button"
            class="inline-flex items-center gap-1 rounded-md border px-3 py-1.5 text-xs disabled:cursor-not-allowed disabled:opacity-60"
            :class="clashStore.status.running
              ? 'border-red-300 bg-red-50 text-red-700 hover:bg-red-100'
              : 'border-emerald-300 bg-emerald-50 text-emerald-700 hover:bg-emerald-100'"
            :disabled="startStopPending"
            @click="handleStartStop"
          >
            <Square v-if="clashStore.status.running" :size="14" />
            <Play v-else :size="14" />
            {{ clashStore.status.running ? t('clash.stop') : t('clash.start') }}
          </button>

          <button
            type="button"
            class="inline-flex items-center gap-1 rounded-md border border-slate-300 px-3 py-1.5 text-xs text-slate-700 hover:bg-slate-100"
            @click="showConfigModal = true"
          >
            <Settings2 :size="14" />
            {{ t('clash.config') }}
          </button>

          <button
            type="button"
            class="inline-flex items-center gap-1 rounded-md border border-slate-300 px-3 py-1.5 text-xs text-slate-700 hover:bg-slate-100 disabled:cursor-not-allowed disabled:opacity-60"
            :disabled="clashStore.refreshingAll"
            @click="handleRefreshSubscriptions"
          >
            <RefreshCcw :size="14" :class="{ 'animate-spin': clashStore.refreshingAll }" />
            {{ clashStore.refreshingAll ? t('clash.refreshing') : t('clash.refreshSubs') }}
          </button>

          <button
            type="button"
            class="inline-flex items-center gap-1 rounded-md border border-sky-600 bg-sky-600 px-3 py-1.5 text-xs text-white hover:bg-sky-700"
            @click="openAddSubscription"
          >
            <Plus :size="14" />
            {{ t('clash.addSub') }}
          </button>
        </div>
      </header>

      <main class="flex-1 overflow-y-auto p-4">
        <div v-if="clashStore.error" class="mb-3 rounded-md border border-amber-300 bg-amber-50 px-3 py-2 text-xs text-amber-800">
          <span class="inline-flex items-center gap-1">
            <TriangleAlert :size="14" />
            {{ clashStore.error }}
          </span>
        </div>

        <div v-if="!clashStore.subscriptions.length" class="rounded-md border border-dashed border-slate-300 bg-slate-50 px-4 py-8 text-center text-sm text-slate-500">
          {{ t('clash.noSubscriptions') }}
        </div>

        <div v-else class="grid gap-3 [grid-template-columns:repeat(auto-fill,minmax(280px,1fr))]">
          <ClashSubscriptionCard
            v-for="subscription in clashStore.subscriptions"
            :key="subscription.id"
            :subscription="subscription"
            :node-count="clashStore.getSubscriptionNodeCount(subscription.id)"
            :selected-node-label="subscription.selectedNode || '--'"
            :dialer-proxy-active="clashStore.config.dialerProxyId === subscription.id"
            :refreshing="!!clashStore.refreshingSubscriptions[subscription.id]"
            :busy="isSubscriptionPending(subscription.id)"
            @set-active="handleSetActiveSubscription"
            @toggle-dialer-proxy="handleToggleDialerProxy"
            @toggle="handleToggleSubscription"
            @refresh="handleRefreshSingleSubscription"
            @edit="openEditSubscription"
            @manage-nodes="handleOpenNodes"
            @remove="handleRemoveSubscription"
          />
        </div>
      </main>
    </section>

    <ClashConfigModal
      v-model:show="showConfigModal"
      :config="clashStore.config"
      :saving="clashStore.savingConfig"
      @save="handleSaveConfig"
    />

    <ClashSubscriptionFormModal
      v-model:show="showSubscriptionModal"
      :title="subscriptionModalTitle"
      :submit-text="editingSubscription ? t('common.save') : t('clash.addSub')"
      :initial-name="editingSubscription?.name || ''"
      :initial-url="editingSubscription?.url || ''"
      :saving="subscriptionSaving"
      @submit="handleSubmitSubscription"
    />

    <ClashNodesModal
      v-model:show="showNodesModal"
      :subscription-name="currentNodesSubscriptionName"
      :nodes="clashStore.nodesDialogDraftNodes"
      :selected-node-name="clashStore.nodesDialogSelectedNodeName"
      :dirty="clashStore.hasUnsavedNodeDraftChanges"
      :refreshing="!!clashStore.refreshingSubscriptions[clashStore.nodesDialogSubscriptionId]"
      :testing-all="clashStore.testingAllNodes"
      :testing-all-tcp="clashStore.testingAllNodesTCP"
      :saving="clashStore.savingNodesDraft"
      :testing-node-map="clashStore.testingNodes"
      :testing-node-tcp-map="clashStore.testingNodesTCP"
      @request-close="handleCloseNodesModal"
      @select-node="clashStore.setDraftSelectedNode"
      @refresh="handleRefreshNodesDraftSubscription"
      @test-all="handleTestAllDraftNodes"
      @test-all-tcp="handleTestAllDraftNodesTCP"
      @save="handleSaveNodesDraft"
      @open-add="showAddNodesModal = true"
      @delete-node="handleDeleteDraftNode"
      @copy-node="handleCopyNodeConfig"
      @test-node="handleTestDraftNode"
      @test-node-tcp="handleTestDraftNodeTCP"
    />

    <ClashAddNodesModal
      v-model:show="showAddNodesModal"
      :saving="clashStore.addingNodes"
      @submit="handleAddNodes"
    />
  </div>
</template>
