import { computed, ref } from 'vue'
import { defineStore } from 'pinia'
import { xrayApi } from '@/api/xray'
import type {
  XrayConfig,
  XrayDraftNode,
  XrayNode,
  XrayRefreshResult,
  XraySpeedTestResult,
  XrayStatus
} from '@/types/xray'

const TEST_ALL_CONCURRENCY = 4

function createDefaultStatus(): XrayStatus {
  return {
    running: false,
    nodeCount: 0
  }
}

function createDefaultConfig(): XrayConfig {
  return {
    socksListen: '127.0.0.1',
    socksPort: 10808,
    logLevel: 'warning',
    globalProxy: false,
    subscriptions: []
  }
}

function cloneNode(node: XrayNode, draftAdded = false): XrayDraftNode {
  return {
    ...node,
    _draftAdded: draftAdded ? true : undefined
  }
}

function stripDraftMeta(node: XrayDraftNode): XrayNode {
  const { _draftAdded, ...clean } = node
  return clean
}

function normalizeNodeForDiff(node: XrayDraftNode): Omit<XrayNode, 'latency'> {
  const clean = stripDraftMeta(node)
  const { latency, ...stable } = clean
  return stable
}

function normalizeStatus(raw: XrayStatus): XrayStatus {
  return {
    running: !!raw?.running,
    socksAddr: raw?.socksAddr || '',
    selectedNode: raw?.selectedNode || '',
    nodeCount: Number(raw?.nodeCount || 0)
  }
}

function normalizeConfig(raw: XrayConfig): XrayConfig {
  return {
    socksListen: String(raw?.socksListen || '127.0.0.1'),
    socksPort: Number(raw?.socksPort || 10808),
    logLevel: raw?.logLevel || 'warning',
    globalProxy: !!raw?.globalProxy,
    subscriptions: Array.isArray(raw?.subscriptions) ? raw.subscriptions : []
  }
}

function hasNodeByName(nodes: Array<{ name: string }>, name: string): boolean {
  return nodes.some((node) => node.name === name)
}

function uniqueDraftNodeName(name: string, existing: Array<{ name: string }>): string {
  if (!hasNodeByName(existing, name)) return name

  for (let i = 2; ; i += 1) {
    const candidate = `${name}_${i}`
    if (!hasNodeByName(existing, candidate)) {
      return candidate
    }
  }
}

function fallbackCopyWithExecCommand(text: string): boolean {
  if (typeof document === 'undefined') return false

  const textarea = document.createElement('textarea')
  textarea.value = text
  textarea.setAttribute('readonly', '')
  textarea.style.position = 'fixed'
  textarea.style.opacity = '0'
  textarea.style.pointerEvents = 'none'
  document.body.appendChild(textarea)
  textarea.select()

  const copied = document.execCommand('copy')
  document.body.removeChild(textarea)
  return copied
}

function toErrorMessage(error: unknown): string {
  if (error instanceof Error) return error.message
  return String(error)
}

function setFlagByKey(record: Record<string, boolean>, key: string, value: boolean): Record<string, boolean> {
  if (value) {
    return { ...record, [key]: true }
  }

  const next = { ...record }
  delete next[key]
  return next
}

export const useXrayStore = defineStore('xray', () => {
  const loading = ref(false)
  const error = ref<string | null>(null)

  const status = ref<XrayStatus>(createDefaultStatus())
  const config = ref<XrayConfig>(createDefaultConfig())
  const nodes = ref<XrayNode[]>([])

  const refreshingAll = ref(false)
  const refreshingSubscriptions = ref<Record<string, boolean>>({})
  const testingNodes = ref<Record<string, boolean>>({})
  const testingAllNodes = ref(false)
  const savingConfig = ref(false)

  const nodesDialogSubscriptionId = ref('')
  const nodesDialogSelectedNodeName = ref('')
  const nodesDialogInitialSelectedNodeName = ref('')
  const nodesDialogOriginalNodes = ref<XrayDraftNode[]>([])
  const nodesDialogDraftNodes = ref<XrayDraftNode[]>([])
  const savingNodesDraft = ref(false)
  const addingNodes = ref(false)

  const subscriptions = computed(() => config.value.subscriptions || [])

  const currentNodesDialogSubscription = computed(() => {
    if (!nodesDialogSubscriptionId.value) return null
    return subscriptions.value.find((subscription) => subscription.id === nodesDialogSubscriptionId.value) || null
  })

  const hasUnsavedNodeDraftChanges = computed(() => {
    if (nodesDialogSelectedNodeName.value !== nodesDialogInitialSelectedNodeName.value) {
      return true
    }

    const original = (nodesDialogOriginalNodes.value || []).map(normalizeNodeForDiff)
    const draft = (nodesDialogDraftNodes.value || []).map(normalizeNodeForDiff)
    return JSON.stringify(original) !== JSON.stringify(draft)
  })

  function clearError(): void {
    error.value = null
  }

  async function loadAll(silent = false): Promise<void> {
    clearError()
    if (!silent) loading.value = true

    try {
      const [nextStatus, nextConfig, nextNodes] = await Promise.all([
        xrayApi.getStatus(),
        xrayApi.getConfig(),
        xrayApi.getNodes()
      ])

      status.value = normalizeStatus(nextStatus)
      config.value = normalizeConfig(nextConfig)
      nodes.value = Array.isArray(nextNodes) ? nextNodes : []
    } catch (cause) {
      error.value = toErrorMessage(cause)
      throw cause
    } finally {
      if (!silent) loading.value = false
    }
  }

  async function start(): Promise<void> {
    clearError()
    try {
      await xrayApi.start()
      await loadAll(true)
    } catch (cause) {
      error.value = toErrorMessage(cause)
      throw cause
    }
  }

  async function stop(): Promise<void> {
    clearError()
    try {
      await xrayApi.stop()
      await loadAll(true)
    } catch (cause) {
      error.value = toErrorMessage(cause)
      throw cause
    }
  }

  async function selectNode(nodeName: string): Promise<void> {
    clearError()
    try {
      await xrayApi.selectNode(nodeName)
      await loadAll(true)
    } catch (cause) {
      error.value = toErrorMessage(cause)
      throw cause
    }
  }

  async function testNode(nodeName: string): Promise<XraySpeedTestResult> {
    testingNodes.value = setFlagByKey(testingNodes.value, nodeName, true)

    try {
      const result = await xrayApi.testNode(nodeName)

      const target = nodes.value.find((node) => node.name === nodeName)
      if (target) {
        target.latency = typeof result?.latency === 'number' ? result.latency : -1
      }

      return result
    } finally {
      testingNodes.value = setFlagByKey(testingNodes.value, nodeName, false)
    }
  }

  async function refreshSubscriptions(): Promise<XrayRefreshResult> {
    clearError()
    refreshingAll.value = true

    try {
      const result = await xrayApi.refreshSubscriptions()
      await loadAll(true)
      return result
    } catch (cause) {
      error.value = toErrorMessage(cause)
      throw cause
    } finally {
      refreshingAll.value = false
    }
  }

  async function addSubscription(name: string, url: string): Promise<void> {
    clearError()

    try {
      await xrayApi.addSubscription(name, url)
      await loadAll(true)
    } catch (cause) {
      error.value = toErrorMessage(cause)
      throw cause
    }
  }

  async function updateSubscription(id: string, name: string, url: string): Promise<void> {
    clearError()

    try {
      await xrayApi.updateSubscription(id, name, url)
      await loadAll(true)
    } catch (cause) {
      error.value = toErrorMessage(cause)
      throw cause
    }
  }

  async function removeSubscription(id: string): Promise<void> {
    clearError()

    try {
      await xrayApi.removeSubscription(id)
      await loadAll(true)
    } catch (cause) {
      error.value = toErrorMessage(cause)
      throw cause
    }
  }

  async function toggleSubscription(id: string): Promise<void> {
    clearError()

    try {
      await xrayApi.toggleSubscription(id)
      await loadAll(true)
    } catch (cause) {
      error.value = toErrorMessage(cause)
      throw cause
    }
  }

  async function setActiveSubscription(id: string): Promise<void> {
    clearError()

    try {
      await xrayApi.setActiveSubscription(id)
      await loadAll(true)
    } catch (cause) {
      error.value = toErrorMessage(cause)
      throw cause
    }
  }

  async function activateSubscription(id: string): Promise<void> {
    clearError()

    try {
      await xrayApi.activateSubscription(id)
      await loadAll(true)
    } catch (cause) {
      error.value = toErrorMessage(cause)
      throw cause
    }
  }

  async function refreshSingleSubscription(id: string): Promise<XrayRefreshResult> {
    clearError()
    refreshingSubscriptions.value = setFlagByKey(refreshingSubscriptions.value, id, true)

    try {
      const result = await xrayApi.refreshSingleSubscription(id)
      await loadAll(true)
      return result
    } catch (cause) {
      error.value = toErrorMessage(cause)
      throw cause
    } finally {
      refreshingSubscriptions.value = setFlagByKey(refreshingSubscriptions.value, id, false)
    }
  }

  async function saveConfig(input: XrayConfig): Promise<void> {
    clearError()
    savingConfig.value = true

    try {
      await xrayApi.saveConfig(input)
      await loadAll(true)
    } catch (cause) {
      error.value = toErrorMessage(cause)
      throw cause
    } finally {
      savingConfig.value = false
    }
  }

  function getSubscriptionNodeCount(subscriptionId: string): number {
    return nodes.value.filter((node) => node.sourceId === subscriptionId).length
  }

  function resetNodesDraftState(): void {
    nodesDialogSubscriptionId.value = ''
    nodesDialogSelectedNodeName.value = ''
    nodesDialogInitialSelectedNodeName.value = ''
    nodesDialogOriginalNodes.value = []
    nodesDialogDraftNodes.value = []
  }

  function initNodesDraft(subscriptionId: string, selectedNodeFromConfig: string): void {
    const targetNodes = nodes.value
      .filter((node) => node.sourceId === subscriptionId)
      .map((node) => cloneNode(node, false))

    nodesDialogOriginalNodes.value = targetNodes.map((node) => ({ ...node }))
    nodesDialogDraftNodes.value = targetNodes.map((node) => ({ ...node }))

    const selectedNode = selectedNodeFromConfig || targetNodes[0]?.name || ''
    nodesDialogSelectedNodeName.value = selectedNode
    nodesDialogInitialSelectedNodeName.value = selectedNode
  }

  async function openSubscriptionNodesDraft(subscriptionId: string): Promise<void> {
    clearError()
    await loadAll(true)

    const subscription = subscriptions.value.find((item) => item.id === subscriptionId)
    if (!subscription) {
      throw new Error('subscription not found')
    }

    nodesDialogSubscriptionId.value = subscriptionId
    initNodesDraft(subscriptionId, subscription.selectedNode || '')
  }

  function setDraftSelectedNode(nodeName: string): void {
    nodesDialogSelectedNodeName.value = nodeName
  }

  function deleteDraftNode(nodeName: string): void {
    const before = nodesDialogDraftNodes.value.length
    nodesDialogDraftNodes.value = nodesDialogDraftNodes.value.filter((node) => node.name !== nodeName)

    if (nodesDialogDraftNodes.value.length === before) {
      throw new Error('node not found')
    }

    if (nodesDialogSelectedNodeName.value === nodeName) {
      nodesDialogSelectedNodeName.value = nodesDialogDraftNodes.value[0]?.name || ''
    }
  }

  async function addNodesToDraft(content: string): Promise<number> {
    const subscriptionId = nodesDialogSubscriptionId.value
    if (!subscriptionId) {
      throw new Error('subscription not selected')
    }

    const trimmed = content.trim()
    if (!trimmed) return 0

    addingNodes.value = true

    try {
      const parsedNodes = await xrayApi.parseNodesForSubscription(subscriptionId, trimmed)
      if (!Array.isArray(parsedNodes) || parsedNodes.length === 0) return 0

      const existing = nodesDialogDraftNodes.value.map((node) => ({ name: node.name }))
      const addedNodes: XrayDraftNode[] = []

      for (const raw of parsedNodes) {
        const baseName = (raw.name || 'node').trim() || 'node'
        const nextName = uniqueDraftNodeName(baseName, existing)
        const nextNode = cloneNode(
          {
            ...raw,
            name: nextName,
            sourceId: subscriptionId,
            latency: Number(raw.latency || 0)
          },
          true
        )

        existing.push({ name: nextName })
        addedNodes.push(nextNode)
      }

      nodesDialogDraftNodes.value = [...nodesDialogDraftNodes.value, ...addedNodes]
      if (!nodesDialogSelectedNodeName.value && nodesDialogDraftNodes.value.length > 0) {
        nodesDialogSelectedNodeName.value = nodesDialogDraftNodes.value[0].name
      }

      return addedNodes.length
    } finally {
      addingNodes.value = false
    }
  }

  async function testDraftNode(nodeName: string): Promise<XraySpeedTestResult> {
    const node = nodesDialogDraftNodes.value.find((item) => item.name === nodeName)
    if (!node) {
      throw new Error('node not found')
    }
    if (node._draftAdded) {
      throw new Error('save before test')
    }

    testingNodes.value = setFlagByKey(testingNodes.value, nodeName, true)

    try {
      const result = await xrayApi.testNode(nodeName)
      const targetNode = nodesDialogDraftNodes.value.find((item) => item.name === nodeName)
      if (targetNode) {
        targetNode.latency = typeof result?.latency === 'number' ? result.latency : -1
      }
      await loadAll(true)
      return result
    } finally {
      testingNodes.value = setFlagByKey(testingNodes.value, nodeName, false)
    }
  }

  async function testAllDraftNodes(): Promise<void> {
    const targetNodes = nodesDialogDraftNodes.value.filter((node) => !node._draftAdded)
    if (targetNodes.length === 0) return

    testingAllNodes.value = true

    nodesDialogDraftNodes.value = nodesDialogDraftNodes.value.map((node) => ({
      ...node,
      latency: node._draftAdded ? node.latency : 0
    }))

    const queue = targetNodes.map((node) => node.name)
    const concurrency = Math.min(TEST_ALL_CONCURRENCY, queue.length)

    const worker = async () => {
      while (queue.length > 0) {
        const nodeName = queue.shift()
        if (!nodeName) return

        testingNodes.value = setFlagByKey(testingNodes.value, nodeName, true)

        try {
          const result = await xrayApi.testNode(nodeName)
          const target = nodesDialogDraftNodes.value.find((node) => node.name === nodeName)
          if (target) {
            target.latency = typeof result?.latency === 'number' ? result.latency : -1
          }
        } catch {
          const target = nodesDialogDraftNodes.value.find((node) => node.name === nodeName)
          if (target) {
            target.latency = -1
          }
        } finally {
          testingNodes.value = setFlagByKey(testingNodes.value, nodeName, false)
        }
      }
    }

    try {
      await Promise.all(Array.from({ length: concurrency }, () => worker()))
      await loadAll(true)
    } finally {
      testingAllNodes.value = false
    }
  }

  async function saveDraftNodes(): Promise<void> {
    const subscriptionId = nodesDialogSubscriptionId.value
    if (!subscriptionId) return

    const cleanNodes = nodesDialogDraftNodes.value.map((node) => ({
      ...stripDraftMeta(node),
      sourceId: subscriptionId
    }))

    let selectedNode = nodesDialogSelectedNodeName.value
    if (cleanNodes.length > 0 && !selectedNode) {
      selectedNode = cleanNodes[0].name
    }
    if (cleanNodes.length > 0 && !cleanNodes.some((node) => node.name === selectedNode)) {
      selectedNode = cleanNodes[0].name
    }

    savingNodesDraft.value = true

    try {
      await xrayApi.replaceSubscriptionNodes(subscriptionId, cleanNodes, selectedNode || '')
      await loadAll(true)
      resetNodesDraftState()
    } catch (cause) {
      error.value = toErrorMessage(cause)
      throw cause
    } finally {
      savingNodesDraft.value = false
    }
  }

  async function refreshNodesDraftSubscription(): Promise<XrayRefreshResult> {
    const subscriptionId = nodesDialogSubscriptionId.value
    if (!subscriptionId) {
      throw new Error('subscription not selected')
    }

    refreshingSubscriptions.value = setFlagByKey(refreshingSubscriptions.value, subscriptionId, true)

    try {
      const result = await xrayApi.refreshSingleSubscription(subscriptionId)
      await loadAll(true)

      const subscription = subscriptions.value.find((item) => item.id === subscriptionId)
      initNodesDraft(subscriptionId, subscription?.selectedNode || '')

      return result
    } finally {
      refreshingSubscriptions.value = setFlagByKey(refreshingSubscriptions.value, subscriptionId, false)
    }
  }

  async function copyNodeConfig(nodeName: string): Promise<void> {
    const node = nodesDialogDraftNodes.value.find((item) => item.name === nodeName)
    if (!node) {
      throw new Error('node not found')
    }

    const text = node._draftAdded
      ? JSON.stringify(stripDraftMeta(node), null, 2)
      : await xrayApi.getNodeConfig(nodeName)

    try {
      if (typeof navigator !== 'undefined' && navigator.clipboard?.writeText) {
        await navigator.clipboard.writeText(text)
        return
      }
    } catch {
      // fallback below
    }

    const copied = fallbackCopyWithExecCommand(text)
    if (!copied) {
      throw new Error('copy failed')
    }
  }

  return {
    loading,
    error,
    status,
    config,
    nodes,
    subscriptions,
    refreshingAll,
    refreshingSubscriptions,
    testingNodes,
    testingAllNodes,
    savingConfig,
    nodesDialogSubscriptionId,
    nodesDialogSelectedNodeName,
    nodesDialogInitialSelectedNodeName,
    nodesDialogOriginalNodes,
    nodesDialogDraftNodes,
    savingNodesDraft,
    addingNodes,
    currentNodesDialogSubscription,
    hasUnsavedNodeDraftChanges,
    clearError,
    loadAll,
    start,
    stop,
    selectNode,
    testNode,
    refreshSubscriptions,
    addSubscription,
    updateSubscription,
    removeSubscription,
    toggleSubscription,
    setActiveSubscription,
    activateSubscription,
    refreshSingleSubscription,
    saveConfig,
    getSubscriptionNodeCount,
    resetNodesDraftState,
    openSubscriptionNodesDraft,
    setDraftSelectedNode,
    deleteDraftNode,
    addNodesToDraft,
    testDraftNode,
    testAllDraftNodes,
    saveDraftNodes,
    refreshNodesDraftSubscription,
    copyNodeConfig
  }
})
