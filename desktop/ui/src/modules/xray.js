/**
 * XRay proxy state management and operations
 */
import { t } from '../i18n/index.js'
import { createIcon } from './icons.js'
import { confirm } from './confirm.js'

let xrayStatus = null
let xrayNodes = []
let xrayConfig = null
const TEST_ALL_CONCURRENCY = 4

export function getXRayStatus() { return xrayStatus }
export function getXRayNodes() { return xrayNodes }
export function getXRayConfig() { return xrayConfig }

function escapeHTML(s) {
  if (s == null) return ''
  return String(s).replace(/&/g,'&amp;').replace(/</g,'&lt;').replace(/>/g,'&gt;').replace(/"/g,'&quot;').replace(/'/g,'&#39;')
}

function escapeAttr(s) {
  return escapeHTML(s)
}

// UTF-8 safe base64 encoding/decoding
function utf8Btoa(str) {
  return btoa(encodeURIComponent(str).replace(/%([0-9A-F]{2})/g, (match, p1) => {
    return String.fromCharCode('0x' + p1)
  }))
}

function utf8Atob(str) {
  return decodeURIComponent(atob(str).split('').map(c => {
    return '%' + ('00' + c.charCodeAt(0).toString(16)).slice(-2)
  }).join(''))
}

export async function loadXRayStatus() {
  try {
    xrayStatus = await window.go.main.App.GetXRayStatus()
  } catch (e) {
    console.warn('[xray] load status:', e)
    xrayStatus = null
  }
  return xrayStatus
}

export async function loadXRayNodes() {
  try {
    xrayNodes = await window.go.main.App.GetXRayNodes() || []
  } catch (e) {
    console.warn('[xray] load nodes:', e)
    xrayNodes = []
  }
  return xrayNodes
}

export async function loadXRayConfig() {
  try {
    xrayConfig = await window.go.main.App.GetXRayConfig()
  } catch (e) {
    console.warn('[xray] load config:', e)
    xrayConfig = null
  }
  return xrayConfig
}

export async function refreshXRayView() {
  await Promise.all([loadXRayStatus(), loadXRayNodes(), loadXRayConfig()])
  renderXRayView()
}

export async function startXRay() {
  try {
    await window.go.main.App.StartXRay()
    await refreshXRayView()
  } catch (e) {
    alert(t('xray.startFailed') + e)
  }
}

export async function stopXRay() {
  try {
    await window.go.main.App.StopXRay()
    await refreshXRayView()
  } catch (e) {
    alert(t('xray.stopFailed') + e)
  }
}

export async function selectXRayNode(nodeName) {
  try {
    await window.go.main.App.SelectXRayNode(nodeName)
    await refreshXRayView()
  } catch (e) {
    alert(t('xray.selectFailed') + e)
  }
}

export async function testXRayNode(nodeName) {
  const encoded = encodeURIComponent(nodeName)
  const selector = CSS.escape ? CSS.escape(encoded) : encoded
  const btn = document.querySelector(`[data-test-node="${selector}"]`)
  if (btn) {
    btn.disabled = true
    btn.textContent = '...'
  }
  try {
    const result = await window.go.main.App.TestXRayNode(nodeName)
    await loadXRayNodes()
    return result
  } catch (e) {
    console.error('[xray] test node:', e)
    return { nodeName, latency: -1, error: String(e) }
  } finally {
    if (btn) {
      btn.disabled = false
      btn.textContent = t('xray.test')
    }
  }
}

export async function refreshXRaySubscriptions() {
  const btn = document.getElementById('xrayRefreshBtn')
  if (btn) {
    btn.disabled = true
    btn.textContent = t('xray.refreshing')
  }
  try {
    const result = await window.go.main.App.RefreshXRaySubscriptions()
    await refreshXRayView()
    return result
  } catch (e) {
    alert(t('xray.refreshFailed') + e)
    return null
  } finally {
    if (btn) {
      btn.disabled = false
      btn.textContent = t('xray.refreshSubs')
    }
  }
}

export async function addXRaySubscription() {
  const nameInput = document.getElementById('xraySubName')
  const urlInput = document.getElementById('xraySubUrl')
  const name = nameInput ? nameInput.value.trim() : ''
  const url = urlInput ? urlInput.value.trim() : ''

  try {
    await window.go.main.App.AddXRaySubscription(name || 'Unnamed', url)
    closeXRayAddSubscriptionModal()
    await loadXRayConfig()
    renderXRaySubscriptions()
  } catch (e) {
    alert(t('xray.addSubFailed') + e)
  }
}

export async function setActiveXRaySubscription(id) {
  try {
    await window.go.main.App.SetActiveXRaySubscription(id)
    await refreshXRayView()
  } catch (e) {
    alert(t('xray.setActiveFailed') + e)
  }
}

export async function updateSubscriptionSelectedNode(id, nodeName) {
  try {
    await window.go.main.App.UpdateXRaySubscriptionSelectedNode(id, nodeName)
    await loadXRayConfig()
    renderXRaySubscriptions()
  } catch (e) {
    alert(t('xray.updateNodeFailed') + e)
  }
}

export async function toggleXRaySubscription(id) {
  try {
    await window.go.main.App.ToggleXRaySubscription(id)
    await loadXRayConfig()
    renderXRaySubscriptions()
  } catch (e) {
    alert(t('xray.toggleSubFailed') + e)
  }
}

export async function refreshSingleXRaySubscription(id) {
  const btn = document.querySelector(`[data-refresh-sub="${CSS.escape(id)}"]`)
  if (btn) {
    btn.disabled = true
    const originalHTML = btn.innerHTML
    btn.innerHTML = '...'
    try {
      await window.go.main.App.RefreshSingleXRaySubscription(id)
      await refreshXRayView()
    } catch (e) {
      alert(t('xray.refreshFailed') + e)
    } finally {
      if (btn) {
        btn.disabled = false
        btn.innerHTML = originalHTML
      }
    }
  }
}

export async function activateXRaySubscription(id) {
  try {
    await window.go.main.App.ActivateXRaySubscription(id)
    await refreshXRayView()
  } catch (e) {
    alert(t('xray.activateSubFailed') + e)
  }
}

export async function removeXRaySubscription(id) {
  const confirmed = await confirm(t('xray.removeSubConfirm'), { danger: true })
  if (!confirmed) return
  try {
    await window.go.main.App.RemoveXRaySubscription(id)
    await loadXRayConfig()
    renderXRaySubscriptions()
  } catch (e) {
    alert(t('xray.removeSubFailed') + e)
  }
}

export async function saveXRayConfig() {
  const socksListen = document.getElementById('xraySocksListen')
  const socksPort = document.getElementById('xraySocksPort')
  const logLevel = document.getElementById('xrayLogLevel')

  if (!socksListen || !socksPort || !logLevel) return

  try {
    const config = {
      ...xrayConfig,
      socksListen: socksListen.value.trim() || '127.0.0.1',
      socksPort: parseInt(socksPort.value) || 10808,
      logLevel: logLevel.value
    }

    await window.go.main.App.SaveXRayConfig(JSON.stringify(config))
    await refreshXRayView()
    closeXRayConfigModal()
  } catch (e) {
    alert(t('xray.configSaveFailed') + e)
  }
}

export function showXRayConfigModal() {
  const modal = document.getElementById('xrayConfigModal')
  const socksListen = document.getElementById('xraySocksListen')
  const socksPort = document.getElementById('xraySocksPort')
  const logLevel = document.getElementById('xrayLogLevel')

  if (modal && socksListen && socksPort && logLevel && xrayConfig) {
    socksListen.value = xrayConfig.socksListen || '127.0.0.1'
    socksPort.value = xrayConfig.socksPort || 10808
    logLevel.value = xrayConfig.logLevel || 'warning'
    modal.style.display = 'flex'
  }
}

export function closeXRayConfigModal() {
  const modal = document.getElementById('xrayConfigModal')
  if (modal) modal.style.display = 'none'
}

export function showXRayAddSubscriptionModal() {
  const modal = document.getElementById('xrayAddSubscriptionModal')
  if (modal) {
    // 清空输入框
    const nameInput = document.getElementById('xraySubName')
    const urlInput = document.getElementById('xraySubUrl')
    if (nameInput) nameInput.value = ''
    if (urlInput) urlInput.value = ''
    modal.style.display = 'flex'
  }
}

export function closeXRayAddSubscriptionModal() {
  const modal = document.getElementById('xrayAddSubscriptionModal')
  if (modal) modal.style.display = 'none'
}

// --- Rendering ---

export function renderXRayView() {
  renderXRayStatusBar()
  renderXRaySubscriptions()
}

function renderXRayStatusBar() {
  const statusIcon = document.getElementById('xrayStatusIcon')
  const startStopBtn = document.getElementById('xrayStartStopBtn')
  const startStopText = document.getElementById('xrayStartStopText')

  if (!statusIcon || !startStopBtn || !startStopText) return

  const status = xrayStatus || {}
  const running = status.running || false
  const dot = running ? '🟢' : '🔴'
  const socksAddr = status.socksAddr || '--'
  const selectedNode = status.selectedNode || '--'

  // Update status icon in title
  statusIcon.textContent = dot

  // Update start/stop button
  startStopBtn.className = `btn btn-sm ${running ? 'btn-danger' : 'btn-primary'}`
  startStopBtn.onclick = running ? stopXRay : startXRay
  startStopText.textContent = running ? t('xray.stop') : t('xray.start')
  startStopBtn.title = running ? t('xray.stop') : t('xray.start')

  // Show status bar only when running
  const statusBar = document.getElementById('xrayStatusBar')
  if (statusBar) {
    if (running) {
      statusBar.style.display = 'flex'
      statusBar.innerHTML = `
        <div class="xray-status-info">
          <span class="xray-status-detail">SOCKS5: ${escapeHTML(socksAddr)} | ${escapeHTML(t('xray.node'))}: ${escapeHTML(selectedNode)}</span>
        </div>`
    } else {
      statusBar.style.display = 'none'
    }
  }
}

function renderXRaySubscriptions() {
  const el = document.getElementById('xraySubscriptions')
  if (!el) return

  const cfg = xrayConfig || {}
  const subs = cfg.subscriptions || []

  if (subs.length === 0) {
    el.innerHTML = `<div class="xray-empty">${t('xray.noSubscriptions')}</div>`
    return
  }

  const status = xrayStatus || {}
  const running = status.running || false

  let html = '<div class="xray-sub-list">'
  for (const sub of subs) {
    const nodeCount = (xrayNodes || []).filter(n => n.sourceId === sub.id).length
    const isActive = sub.active || false
    const isEnabled = sub.enabled || false
    const selectedNodeName = sub.selectedNode || '--'

    // 创建图标
    const powerIcon = createIcon('power', { size: 14 })
    const refreshIcon = createIcon('refreshCw', { size: 14 })
    const trashIcon = createIcon('trash', { size: 14 })
    const listIcon = createIcon('list', { size: 14 })
    const editIcon = createIcon('edit', { size: 14 })

    const encodedId = utf8Btoa(sub.id)

    html += `
      <div class="xray-sub-card ${isActive ? 'active' : ''} ${!isEnabled ? 'disabled' : ''}">
        <div class="xray-sub-card-header">
          <span class="xray-sub-name" title="${escapeHTML(sub.name || sub.id)}">${escapeHTML(sub.name || sub.id)}</span>
          <span class="xray-sub-badge ${isActive ? 'active' : 'disabled'}">${isActive ? t('xray.active') : t('xray.inactive')}</span>
        </div>
        <div class="xray-sub-card-body">
          <div class="xray-sub-info">
            <span class="xray-sub-count">${nodeCount} ${t('xray.nodes')}</span>
          </div>
          <div class="xray-sub-selected-node" title="${escapeHTML(selectedNodeName)}">
            ${t('xray.selectedNode')}: ${escapeHTML(selectedNodeName)}
          </div>
        </div>
        <div class="xray-sub-card-footer">
          <button class="kiro-icon-btn ${isActive ? 'primary' : ''}" onclick="setActiveXRaySubscriptionFromCard('${encodedId}')" title="${t('xray.setActive')}">${powerIcon}</button>
          <button class="kiro-icon-btn" onclick="showSubscriptionNodesDialog('${encodedId}')" title="${t('xray.manageNodes')}">${listIcon}</button>
          <button class="kiro-icon-btn" onclick="showEditSubscriptionDialog('${encodedId}')" title="${t('xray.editSub')}">${editIcon}</button>
          <button class="kiro-icon-btn" data-refresh-sub="${escapeAttr(sub.id)}" onclick="refreshSingleXRaySubscriptionFromCard('${encodedId}')" title="${t('xray.refreshSub')}">${refreshIcon}</button>
          <button class="kiro-icon-btn kiro-icon-btn-danger" onclick="removeXRaySubscriptionFromCard('${encodedId}')" title="${t('common.delete')}">${trashIcon}</button>
        </div>
      </div>`
  }
  html += '</div>'

  el.innerHTML = html
}

// 从卡片调用的全局函数
window.setActiveXRaySubscriptionFromCard = async function(encodedId) {
  const id = utf8Atob(encodedId)
  await setActiveXRaySubscription(id)
}

window.toggleXRaySubscriptionFromCard = async function(encodedId) {
  const id = utf8Atob(encodedId)
  await toggleXRaySubscription(id)
}

window.refreshSingleXRaySubscriptionFromCard = async function(encodedId) {
  const id = utf8Atob(encodedId)
  await refreshSingleXRaySubscription(id)
}

window.activateXRaySubscriptionFromCard = async function(encodedId) {
  const id = utf8Atob(encodedId)
  await activateXRaySubscription(id)
}

window.removeXRaySubscriptionFromCard = async function(encodedId) {
  const id = utf8Atob(encodedId)
  await removeXRaySubscription(id)
}

// 显示订阅节点选择对话框
let currentSubscriptionId = null
let currentSelectedNodeName = null
let initialSelectedNodeName = null
let currentSubscriptionNodesOriginal = []
let currentSubscriptionNodesDraft = []

function cloneNodeForDraft(node, draftAdded = false) {
  return { ...node, _draftAdded: draftAdded }
}

function stripDraftMeta(node) {
  const { _draftAdded, ...clean } = node || {}
  return clean
}

function normalizeNodesForDiff(nodes) {
  return (nodes || []).map((node) => {
    const clean = stripDraftMeta(node)
    const { latency, ...stable } = clean
    return stable
  })
}

function hasUnsavedNodeDraftChanges() {
  if (currentSelectedNodeName !== initialSelectedNodeName) return true
  const original = normalizeNodesForDiff(currentSubscriptionNodesOriginal)
  const draft = normalizeNodesForDiff(currentSubscriptionNodesDraft)
  return JSON.stringify(original) !== JSON.stringify(draft)
}

function resetSubscriptionNodesDraftState() {
  currentSubscriptionId = null
  currentSelectedNodeName = null
  initialSelectedNodeName = null
  currentSubscriptionNodesOriginal = []
  currentSubscriptionNodesDraft = []
}

function hasNodeByName(nodes, name) {
  return (nodes || []).some((n) => n && n.name === name)
}

function uniqueDraftNodeName(name, existing) {
  if (!hasNodeByName(existing, name)) return name
  for (let i = 2; ; i++) {
    const candidate = `${name}_${i}`
    if (!hasNodeByName(existing, candidate)) return candidate
  }
}

function initSubscriptionNodesDraft(id, selectedNodeFromConfig) {
  const nodes = (xrayNodes || []).filter((n) => n.sourceId === id)
  currentSubscriptionNodesOriginal = nodes.map((n) => cloneNodeForDraft(n, false))
  currentSubscriptionNodesDraft = nodes.map((n) => cloneNodeForDraft(n, false))
  const selected = selectedNodeFromConfig || (currentSubscriptionNodesDraft[0] && currentSubscriptionNodesDraft[0].name) || null
  currentSelectedNodeName = selected
  initialSelectedNodeName = selected
}

export async function showSubscriptionNodesDialog(encodedId) {
  const id = utf8Atob(encodedId)
  currentSubscriptionId = id

  const cfg = xrayConfig || {}
  const sub = cfg.subscriptions?.find(s => s.id === id)
  if (!sub) {
    alert(t('xray.subscriptionNotFound'))
    return
  }

  // 重新加载节点数据
  await loadXRayNodes()
  initSubscriptionNodesDraft(id, sub.selectedNode)

  const modal = document.getElementById('xrayNodesModal')
  if (modal) {
    modal.style.display = 'flex'
    renderSubscriptionNodes(id)
  }
}

export async function closeSubscriptionNodesDialog(force = false) {
  if (!force && hasUnsavedNodeDraftChanges()) {
    const discard = await confirm(t('xray.discardNodeChangesConfirm'), { danger: true })
    if (!discard) return
  }

  const modal = document.getElementById('xrayNodesModal')
  if (modal) {
    modal.style.display = 'none'
  }
  resetSubscriptionNodesDraftState()
}

export async function refreshCurrentSubscriptionNodes() {
  if (!currentSubscriptionId) return

  if (hasUnsavedNodeDraftChanges()) {
    const discard = await confirm(t('xray.discardNodeChangesConfirm'), { danger: true })
    if (!discard) return
  }

  const btn = document.getElementById('refreshNodesBtn')
  if (btn) {
    btn.disabled = true
    const originalText = btn.textContent
    btn.textContent = t('xray.refreshing')

    try {
      await refreshSingleXRaySubscription(currentSubscriptionId)
      await Promise.all([loadXRayNodes(), loadXRayConfig()])
      const cfg = xrayConfig || {}
      const sub = cfg.subscriptions?.find(s => s.id === currentSubscriptionId)
      initSubscriptionNodesDraft(currentSubscriptionId, sub?.selectedNode || '')
      renderSubscriptionNodes(currentSubscriptionId)
    } finally {
      if (btn) {
        btn.disabled = false
        btn.textContent = originalText
      }
    }
  }
}

export async function testAllNodesInSubscription() {
  if (!currentSubscriptionId) return

  const nodes = (currentSubscriptionNodesDraft || []).filter(n => !n._draftAdded)
  if (nodes.length === 0) return

  // Clear previous test results before starting a new full test run.
  currentSubscriptionNodesDraft = (currentSubscriptionNodesDraft || []).map((node) => ({
    ...node,
    latency: 0
  }))
  renderSubscriptionNodes(currentSubscriptionId)

  const btn = document.getElementById('testAllNodesBtn')
  if (btn) {
    btn.disabled = true
    const originalText = btn.textContent
    btn.textContent = t('xray.testing')

    try {
      const queue = nodes.slice()
      const concurrency = Math.min(TEST_ALL_CONCURRENCY, queue.length)
      let completed = 0

      const worker = async () => {
        while (queue.length > 0) {
          const node = queue.shift()
          if (!node) return

          const result = await testXRayNode(node.name)
          const target = currentSubscriptionNodesDraft.find(n => n.name === node.name)
          if (target) {
            target.latency = (result && typeof result.latency === 'number') ? result.latency : -1
          }

          completed += 1
          if (btn && document.body.contains(btn)) {
            btn.textContent = `${t('xray.testing')} ${completed}/${nodes.length}`
          }
          renderSubscriptionNodes(currentSubscriptionId)
        }
      }

      await Promise.all(Array.from({ length: concurrency }, () => worker()))
    } finally {
      if (btn) {
        btn.disabled = false
        btn.textContent = originalText
      }
    }
  }
}

export function selectNodeInDialog(nodeName) {
  if (!nodeName) return
  try {
    currentSelectedNodeName = utf8Atob(nodeName)
  } catch (_) {
    currentSelectedNodeName = nodeName
  }
  renderSubscriptionNodes(currentSubscriptionId)
}

export async function saveSelectedNode() {
  if (!currentSubscriptionId) return

  const draftNodes = (currentSubscriptionNodesDraft || []).map((node) => ({
    ...stripDraftMeta(node),
    sourceId: currentSubscriptionId
  }))

  let selectedNodeToSave = currentSelectedNodeName
  if (draftNodes.length > 0 && (!selectedNodeToSave || !hasNodeByName(draftNodes, selectedNodeToSave))) {
    selectedNodeToSave = draftNodes[0].name
  }
  if (draftNodes.length > 0 && !selectedNodeToSave) {
    alert(t('xray.pleaseSelectNode'))
    return
  }

  try {
    await window.go.main.App.ReplaceXRaySubscriptionNodes(
      currentSubscriptionId,
      JSON.stringify(draftNodes),
      selectedNodeToSave || ''
    )
    await Promise.all([loadXRayNodes(), loadXRayConfig()])
    await closeSubscriptionNodesDialog(true)
    renderXRaySubscriptions()
  } catch (e) {
    alert(t('xray.saveNodeFailed') + e)
  }
}

function renderSubscriptionNodes(subId) {
  const container = document.getElementById('xrayNodesGrid')
  if (!container) return

  const nodes = currentSubscriptionNodesDraft || []
  const encodedSubId = utf8Btoa(subId)

  if (nodes.length === 0) {
    container.innerHTML = `<div class="xray-empty">${t('xray.noNodes')}</div>`
    return
  }

  let html = ''
  for (const node of nodes) {
    const isSelected = node.name === currentSelectedNodeName
    let latencyText = '--'
    let latencyClass = ''
    if (typeof node.latency === 'number') {
      if (node.latency > 0) {
        latencyText = `${node.latency}ms`
        latencyClass = node.latency < 200
          ? 'xray-latency-good'
          : (node.latency < 500 ? 'xray-latency-ok' : 'xray-latency-bad')
      } else if (node.latency < 0) {
        latencyText = t('xray.testFailedShort')
        latencyClass = 'xray-latency-bad'
      }
    }

    const testIcon = createIcon('zap', { size: 14 })
    const copyIcon = createIcon('copy', { size: 14 })
    const trashIcon = createIcon('trash', { size: 14 })
    const encodedName = utf8Btoa(node.name)
    const typeText = node._draftAdded
      ? `${node.type} · ${t('xray.unsavedNode')}`
      : node.type

    html += `
      <div class="xray-node-card ${isSelected ? 'selected' : ''}" onclick="selectNodeInDialog('${encodedName}')">
        <div class="xray-node-card-header">
          <div class="xray-node-card-title">
            <span class="xray-node-card-name" title="${escapeHTML(node.name)}">${escapeHTML(node.name)}</span>
            <span class="xray-node-type">${escapeHTML(typeText)}</span>
          </div>
          <div class="xray-node-card-actions">
            <button class="kiro-icon-btn" onclick="copyNodeConfig('${encodedName}', event)" title="${t('xray.copyConfig')}">${copyIcon}</button>
            <button class="kiro-icon-btn kiro-icon-btn-danger" onclick="deleteNodeFromSubscription('${encodedSubId}', '${encodedName}', event)" title="${t('xray.deleteNode')}">${trashIcon}</button>
            <button class="kiro-icon-btn" data-test-node="${escapeAttr(encodedName)}" onclick="testNodeInDialog('${encodedName}', event)" title="${node._draftAdded ? t('xray.saveBeforeTest') : t('xray.test')}" ${node._draftAdded ? 'disabled' : ''}>${testIcon}</button>
          </div>
        </div>
        <div class="xray-node-card-body">
          <div class="xray-node-card-server-row">
            <span class="xray-node-card-server">${escapeHTML(node.server)}:${node.port}</span>
            <span class="xray-node-latency ${latencyClass}">${latencyText}</span>
          </div>
          ${isSelected ? `<div class="xray-node-card-info"><span class="xray-node-selected-badge">${createIcon('check', { size: 14 })} ${t('xray.selected')}</span></div>` : ''}
        </div>
      </div>`
  }

  container.innerHTML = html
}

window.showSubscriptionNodesDialog = showSubscriptionNodesDialog
window.closeSubscriptionNodesDialog = closeSubscriptionNodesDialog
window.refreshCurrentSubscriptionNodes = refreshCurrentSubscriptionNodes
window.testAllNodesInSubscription = testAllNodesInSubscription
window.selectNodeInDialog = selectNodeInDialog
window.saveSelectedNode = saveSelectedNode

window.testNodeInDialog = async function(encodedName, event) {
  if (event && typeof event.stopPropagation === 'function') {
    event.stopPropagation()
  }
  const nodeName = utf8Atob(encodedName)
  const draftNode = (currentSubscriptionNodesDraft || []).find(n => n.name === nodeName)
  if (draftNode && draftNode._draftAdded) {
    alert(t('xray.saveBeforeTest'))
    return
  }
  const btn = document.querySelector(`[data-test-node="${CSS.escape(encodedName)}"]`)
  if (btn) {
    btn.disabled = true
    const originalHTML = btn.innerHTML
    btn.innerHTML = '...'

    try {
      const result = await testXRayNode(nodeName)
      if (result && typeof result.latency === 'number') {
        const target = (currentSubscriptionNodesDraft || []).find(n => n.name === nodeName)
        if (target) target.latency = result.latency
      }
      if (currentSubscriptionId) renderSubscriptionNodes(currentSubscriptionId)
    } finally {
      if (btn && document.body.contains(btn)) {
        btn.disabled = false
        btn.innerHTML = originalHTML
      }
    }
  }
}

window.copyNodeConfig = async function(encodedName, event) {
  if (event && typeof event.stopPropagation === 'function') {
    event.stopPropagation()
  }
  const nodeName = utf8Atob(encodedName)
  const node = (currentSubscriptionNodesDraft || []).find(n => n.name === nodeName)
  if (!node) return

  try {
    const configText = node._draftAdded
      ? JSON.stringify(stripDraftMeta(node), null, 2)
      : await window.go.main.App.GetXRayNodeConfig(nodeName)
    await navigator.clipboard.writeText(configText)

    // Show a brief success indicator
    const btn = document.querySelector(`[onclick*="copyNodeConfig('${encodedName}'"]`)
    if (btn) {
      const originalHTML = btn.innerHTML
      btn.innerHTML = createIcon('check', { size: 14 })
      setTimeout(() => {
        if (btn && document.body.contains(btn)) {
          btn.innerHTML = originalHTML
        }
      }, 1000)
    }
  } catch (e) {
    console.error('[xray] copy node config:', e)
    alert(t('xray.copyFailed') + e)
  }
}

// 编辑订阅对话框
let editingSubscriptionId = null

export function showEditSubscriptionDialog(encodedId) {
  const id = utf8Atob(encodedId)
  editingSubscriptionId = id

  const cfg = xrayConfig || {}
  const sub = cfg.subscriptions?.find(s => s.id === id)
  if (!sub) {
    alert(t('xray.subscriptionNotFound'))
    return
  }

  const modal = document.getElementById('xrayEditSubscriptionModal')
  if (modal) {
    const nameInput = document.getElementById('xrayEditSubName')
    const urlInput = document.getElementById('xrayEditSubUrl')
    if (nameInput) nameInput.value = sub.name || ''
    if (urlInput) urlInput.value = sub.url || ''
    modal.style.display = 'flex'
  }
}

export function closeEditSubscriptionDialog() {
  const modal = document.getElementById('xrayEditSubscriptionModal')
  if (modal) {
    modal.style.display = 'none'
  }
  editingSubscriptionId = null
}

export async function saveEditSubscription() {
  if (!editingSubscriptionId) return

  const nameInput = document.getElementById('xrayEditSubName')
  const urlInput = document.getElementById('xrayEditSubUrl')
  const name = nameInput ? nameInput.value.trim() : ''
  const url = urlInput ? urlInput.value.trim() : ''

  try {
    await window.go.main.App.UpdateXRaySubscription(editingSubscriptionId, name || 'Unnamed', url)
    closeEditSubscriptionDialog()
    await loadXRayConfig()
    renderXRaySubscriptions()
  } catch (e) {
    alert(t('xray.updateSubFailed') + e)
  }
}

window.showEditSubscriptionDialog = showEditSubscriptionDialog
window.closeEditSubscriptionDialog = closeEditSubscriptionDialog
window.saveEditSubscription = saveEditSubscription

// --- Add Node Dialog ---

export function showAddNodeDialog() {
  const modal = document.getElementById('xrayAddNodeModal')
  if (modal) {
    const textarea = document.getElementById('xrayAddNodeContent')
    if (textarea) textarea.value = ''
    modal.style.display = 'flex'
  }
}

export function closeAddNodeDialog() {
  const modal = document.getElementById('xrayAddNodeModal')
  if (modal) modal.style.display = 'none'
}

export async function addNodeFromInput() {
  if (!currentSubscriptionId) return

  const textarea = document.getElementById('xrayAddNodeContent')
  const content = textarea ? textarea.value.trim() : ''
  if (!content) return

  try {
    const parsed = await window.go.main.App.ParseXRayNodesForSubscription(currentSubscriptionId, content)
    const parsedNodes = Array.isArray(parsed) ? parsed : []
    if (parsedNodes.length === 0) {
      alert(t('xray.addNodeFailed') + t('xray.noValidNodesParsed'))
      return
    }

    const existing = (currentSubscriptionNodesDraft || []).map((n) => ({ name: n.name }))
    const addedNodes = []
    for (const raw of parsedNodes) {
      const node = cloneNodeForDraft(raw, true)
      node.sourceId = currentSubscriptionId
      const baseName = (node.name || 'node').trim() || 'node'
      node.name = uniqueDraftNodeName(baseName, existing)
      existing.push({ name: node.name })
      addedNodes.push(node)
    }

    currentSubscriptionNodesDraft = (currentSubscriptionNodesDraft || []).concat(addedNodes)
    if (!currentSelectedNodeName && currentSubscriptionNodesDraft.length > 0) {
      currentSelectedNodeName = currentSubscriptionNodesDraft[0].name
    }

    closeAddNodeDialog()
    renderSubscriptionNodes(currentSubscriptionId)
    alert(addedNodes.length + t('xray.parsedNodes'))
  } catch (e) {
    alert(t('xray.addNodeFailed') + e)
  }
}

window.showAddNodeDialog = showAddNodeDialog
window.closeAddNodeDialog = closeAddNodeDialog
window.addNodeFromInput = addNodeFromInput

window.deleteNodeFromSubscription = async function(encodedSubId, encodedName, event) {
  if (event && typeof event.stopPropagation === 'function') {
    event.stopPropagation()
  }
  let subId = ''
  let nodeName = ''
  try {
    subId = utf8Atob(encodedSubId)
    nodeName = utf8Atob(encodedName)
  } catch (e) {
    alert(t('xray.deleteNodeFailed') + e)
    return
  }

  const confirmed = await confirm(t('xray.deleteNodeConfirm'), { danger: true })
  if (!confirmed) return
  if (!subId) return

  if (subId !== currentSubscriptionId) return

  const before = (currentSubscriptionNodesDraft || []).length
  currentSubscriptionNodesDraft = (currentSubscriptionNodesDraft || []).filter((n) => n.name !== nodeName)
  if (currentSubscriptionNodesDraft.length === before) {
    alert(t('xray.deleteNodeFailed') + t('xray.deleteNodeNotFound'))
    return
  }

  if (currentSelectedNodeName === nodeName) {
    currentSelectedNodeName = (currentSubscriptionNodesDraft[0] && currentSubscriptionNodesDraft[0].name) || null
  }
  renderSubscriptionNodes(subId)
}

// Make functions available globally for onclick handlers
window.startXRay = startXRay
window.stopXRay = stopXRay
window.selectXRayNode = selectXRayNode
window.testXRayNode = testXRayNode
window.refreshXRaySubscriptions = refreshXRaySubscriptions
window.addXRaySubscription = addXRaySubscription
window.removeXRaySubscription = removeXRaySubscription
window.toggleXRaySubscription = toggleXRaySubscription
window.refreshSingleXRaySubscription = refreshSingleXRaySubscription
window.activateXRaySubscription = activateXRaySubscription
window.setActiveXRaySubscription = setActiveXRaySubscription
window.updateSubscriptionSelectedNode = updateSubscriptionSelectedNode
window.showXRayConfigModal = showXRayConfigModal
window.closeXRayConfigModal = closeXRayConfigModal
window.showXRayAddSubscriptionModal = showXRayAddSubscriptionModal
window.closeXRayAddSubscriptionModal = closeXRayAddSubscriptionModal
window.saveXRayConfig = saveXRayConfig
