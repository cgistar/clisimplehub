/**
 * Console logs module
 */
import { t } from '../i18n/index.js'
import { getRealTimeManager } from './realtime.js'

let consolePanelExpanded = false
let currentLogLevel = 1 // Default to INFO

let realtimeUnsubscribe = null
const loggedRequestIds = new Set()

const LOG_LEVELS = {
  DEBUG: 0,
  INFO: 1,
  WARN: 2,
  ERROR: 3,
}

const LOG_ICONS = {
  0: '🔍',
  1: 'ℹ️',
  2: '⚠️',
  3: '❌',
}

const LOG_NAMES = {
  0: 'DEBUG',
  1: 'INFO',
  2: 'WARN',
  3: 'ERROR',
}

// Store all logs
let allLogs = []

export function toggleConsolePanel() {
  const panel = document.getElementById('consolePanel')
  const icon = document.getElementById('consoleToggleIcon')

  consolePanelExpanded = !consolePanelExpanded

  if (consolePanelExpanded) {
    panel.style.display = 'block'
    icon.textContent = '🔼'
  } else {
    panel.style.display = 'none'
    icon.textContent = '🔽'
  }
}

export function changeConsoleLogLevel() {
  const select = document.getElementById('consoleLogLevel')
  currentLogLevel = parseInt(select.value)
  renderLogs()
}

export function copyConsoleLogs() {
  const textarea = document.getElementById('consoleContent')
  textarea.select()
  document.execCommand('copy')

  // Visual feedback
  const btn = event.target.closest('button')
  const originalText = btn.innerHTML
  btn.innerHTML = '✅'
  setTimeout(() => {
    btn.innerHTML = originalText
  }, 1500)
}

export function clearConsoleLogs() {
  allLogs = []
  const textarea = document.getElementById('consoleContent')
  textarea.value = ''
}

export function appendLog(level, message) {
  const timestamp = new Date()
  allLogs.push({ level, message, timestamp })

  // Keep only last 1000 logs
  if (allLogs.length > 1000) {
    allLogs = allLogs.slice(-1000)
  }

  renderLogs()
}

function renderLogs() {
  const textarea = document.getElementById('consoleContent')
  if (!textarea) return

  // Filter logs by current level
  const filteredLogs = allLogs.filter((log) => log.level >= currentLogLevel)

  if (filteredLogs.length === 0) {
    textarea.value = ''
    return
  }

  // Check if user is at the bottom before updating
  const isAtBottom = textarea.scrollHeight - textarea.scrollTop - textarea.clientHeight < 50

  const logText = filteredLogs
    .map((log) => {
      const date = log.timestamp
      const year = date.getFullYear()
      const month = String(date.getMonth() + 1).padStart(2, '0')
      const day = String(date.getDate()).padStart(2, '0')
      const hours = String(date.getHours()).padStart(2, '0')
      const minutes = String(date.getMinutes()).padStart(2, '0')
      const seconds = String(date.getSeconds()).padStart(2, '0')
      const timeStr = `${year}${month}${day} ${hours}:${minutes}:${seconds}`

      const icon = LOG_ICONS[log.level] || 'ℹ️'
      const levelName = LOG_NAMES[log.level] || 'INFO'

      return `${timeStr} ${icon} ${levelName.padEnd(5)} ${log.message}`
    })
    .join('\n')

  textarea.value = logText

  // Auto-scroll to bottom if user was already at the bottom
  if (isAtBottom) {
    textarea.scrollTop = textarea.scrollHeight
  }
}

// Helper functions to log at specific levels
export function logDebug(message) {
  appendLog(LOG_LEVELS.DEBUG, message)
}

export function logInfo(message) {
  appendLog(LOG_LEVELS.INFO, message)
}

export function logWarn(message) {
  appendLog(LOG_LEVELS.WARN, message)
}

export function logError(message) {
  appendLog(LOG_LEVELS.ERROR, message)
}

function formatNumber(value) {
  const num = Number(value)
  if (!Number.isFinite(num)) return '0'
  return num.toLocaleString(undefined, { maximumFractionDigits: 4 })
}

function formatPct(value) {
  const num = Number(value)
  if (!Number.isFinite(num)) return ''
  return `${num.toFixed(1)}%`
}

export function logKiroUsageDetails(usageResult) {
  if (!usageResult) {
    logWarn('[KiroUsage] Empty usage result')
    return
  }

  const subscriptionTitle = String(usageResult.subscriptionTitle || '').trim()
  const used = formatNumber(usageResult.currentUsage)
  const limit = formatNumber(usageResult.usageLimit)
  const balance = formatNumber(usageResult.balance)
  const pct = formatPct(usageResult.usagePct)

  const subscriptionLabel = subscriptionTitle ? ` subscription=${subscriptionTitle}` : ''
  logInfo(`[KiroUsage]${subscriptionLabel} used=${used}/${limit} (${pct || 'n/a'}) remaining=${balance}`)
  if (usageResult.isLowBalance) {
    logWarn('[KiroUsage] Low remaining balance (<20%)')
  }

  const details = usageResult.details || null
  const breakdowns = Array.isArray(details?.usageBreakdownList) ? details.usageBreakdownList : []
  if (!details || breakdowns.length === 0) return

  const subscriptionType = String(details?.subscriptionInfo?.type || '').trim()
  if (subscriptionType) {
    logInfo(`[KiroUsage] subscriptionType=${subscriptionType}`)
  }

  breakdowns.forEach((b, index) => {
    const name = String(b?.displayName || `Item ${index + 1}`).trim()
    const itemUsed = formatNumber(b?.currentUsageWithPrecision)
    const itemLimit = formatNumber(b?.usageLimitWithPrecision)
    logInfo(`[KiroUsage] - ${name}: ${itemUsed}/${itemLimit}`)

    if (b?.freeTrialInfo) {
      const ftUsed = formatNumber(b.freeTrialInfo.currentUsageWithPrecision)
      const ftLimit = formatNumber(b.freeTrialInfo.usageLimitWithPrecision)
      const ftStatus = String(b.freeTrialInfo.freeTrialStatus || '').trim()
      const ftLabel = ftStatus ? ` status=${ftStatus}` : ''
      logInfo(`[KiroUsage]   freeTrial:${ftLabel} ${ftUsed}/${ftLimit}`)
    }

    const bonuses = Array.isArray(b?.bonuses) ? b.bonuses : []
    bonuses.forEach((bonus) => {
      const code = String(bonus?.bonusCode || '').trim() || 'bonus'
      const status = String(bonus?.status || '').trim()
      const bonusUsed = formatNumber(bonus?.currentUsage)
      const bonusLimit = formatNumber(bonus?.usageLimit)
      const statusLabel = status ? ` status=${status}` : ''
      logInfo(`[KiroUsage]   bonus:${code}${statusLabel} ${bonusUsed}/${bonusLimit}`)
    })
  })
}

function handleRealtimeEvent(event) {
  if (!event) return

  const eventType = event.type || ''

  if (eventType === 'debug_log' && event.data) {
    const level = Number.isInteger(event.data.level) ? event.data.level : LOG_LEVELS.DEBUG
    const requestId = (event.data.requestId || '').trim()
    const prefix = requestId ? `[${requestId.slice(0, 8)}] ` : ''
    const message = (event.data.message || '').trim()
    if (message) {
      appendLog(level, `${prefix}${message}`)
    }
    return
  }

  const requestId = event.request_id || event.data?.request_id
  if (requestId && (eventType === 'completed' || eventType === 'failed' || eventType === 'removed')) {
    loggedRequestIds.delete(requestId)
    return
  }

  if (eventType !== 'started' || !event.data) return

  const request = event.data
  const startedRequestId = request.request_id || requestId
  if (!startedRequestId || loggedRequestIds.has(startedRequestId)) return
  loggedRequestIds.add(startedRequestId)

  const url = (request.targetUrl || request.path || '').trim()
  if (!url) return

  const method = (request.method || 'POST').toUpperCase()
  const endpointLabel =
    request.providerName && request.endpointName
      ? ` (${request.providerName}-${request.endpointName})`
      : request.endpointName
  const transformerLabel = (request.transformer || '').trim() ? ` tr=${(request.transformer || '').trim()}` : ''

  logInfo(`${method} ${url}${endpointLabel}${transformerLabel}`)
}

// Toggle bottom console panel visibility (from logs-card button)
let bottomConsoleVisible = false

export function toggleBottomConsole() {
  const bottomPanel = document.getElementById('bottomPanel')
  const toggleBtn = document.getElementById('consoleToggleBtn')

  bottomConsoleVisible = !bottomConsoleVisible

  if (bottomConsoleVisible) {
    bottomPanel.style.display = 'block'
    toggleBtn.classList.add('active')
  } else {
    bottomPanel.style.display = 'none'
    toggleBtn.classList.remove('active')
  }
}

// Initialize console with connection status
export function initConsole() {
  logInfo('Console initialized')

  if (realtimeUnsubscribe) return
  try {
    const manager = getRealTimeManager()
    realtimeUnsubscribe = manager.addListener(handleRealtimeEvent)
  } catch (e) {
    logDebug(`Failed to subscribe realtime events: ${e?.message || e}`)
  }
}
