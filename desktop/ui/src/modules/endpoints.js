/**
 * Endpoint management module
 */
import { state } from './state.js'
import { t } from '../i18n/index.js'
import { showError, showSuccess, formatTokensWithUnit } from './utils.js'
import { logInfo } from './console.js'
import { getRealTimeManager } from './realtime.js'
import { confirm } from './confirm.js'
import { createIcon } from './icons.js'

let endpointsRealtimeUnsubscribe = null
let endpointsRefreshTimer = null
let endpointsRefreshPendingTab = null

export function initEndpointsRealtimeUpdates() {
  if (endpointsRealtimeUnsubscribe) return

  const manager = getRealTimeManager()
  endpointsRealtimeUnsubscribe = manager.addListener((event) => {
    // Only refresh endpoint stats when a request finishes; avoid work on progress updates.
    if (event.type !== 'completed' && event.type !== 'failed' && event.type !== 'token_stats') {
      return
    }

    const eventInterfaceType = event?.data?.interfaceType
    if (eventInterfaceType && eventInterfaceType !== state.currentTab) {
      return
    }

    refreshCurrentTabEndpointsDebounced()
  })
}

export function cleanupEndpointsRealtimeUpdates() {
  if (endpointsRealtimeUnsubscribe) {
    endpointsRealtimeUnsubscribe()
    endpointsRealtimeUnsubscribe = null
  }
  if (endpointsRefreshTimer) {
    clearTimeout(endpointsRefreshTimer)
    endpointsRefreshTimer = null
  }
  endpointsRefreshPendingTab = null
}

function refreshCurrentTabEndpointsDebounced() {
  endpointsRefreshPendingTab = state.currentTab
  if (endpointsRefreshTimer) return

  endpointsRefreshTimer = setTimeout(async () => {
    const tab = endpointsRefreshPendingTab
    endpointsRefreshTimer = null
    endpointsRefreshPendingTab = null

    // Avoid rendering other interface types into the same DOM if user switched tabs.
    if (!tab || tab !== state.currentTab) return
    await loadEndpoints(tab)
  }, 3000)
}

export function switchTab(interfaceType) {
  state.currentTab = interfaceType
  document.querySelectorAll('.tab-btn').forEach((btn) => {
    btn.classList.toggle('active', btn.dataset.type === interfaceType)
  })
  loadEndpoints(interfaceType)

  // Update CLI config editor button visibility
  import('./cliconfig.js').then((m) => m.updateCLIConfigEditorButton())
}

export async function loadEndpoints(interfaceType) {
  const listContainer = document.getElementById('endpointList')
  if (!listContainer) return

  try {
    if (!window.go?.main?.App?.GetEndpointsByType) {
      listContainer.innerHTML = `<div class="empty-state">Backend not available</div>`
      return
    }

    const endpoints = await window.go.main.App.GetEndpointsByType(interfaceType)
    state.endpoints[interfaceType] = endpoints || []
    renderEndpointList(endpoints || [])
    updateActiveSelector(endpoints || [])
  } catch (error) {
    console.error('Failed to load endpoints:', error)
    listContainer.innerHTML = `<div class="empty-state">${t('endpoints.noEndpoints')}</div>`
  }
}

export function renderEndpointList(endpoints) {
  const container = document.getElementById('endpointList')
  if (!container) return

  if (!endpoints || endpoints.length === 0) {
    container.innerHTML = `<div class="empty-state">${t('endpoints.noEndpoints')}</div>`
    return
  }

  // Sort by priority (descending), then by displayName
  const sortedEndpoints = [...endpoints].sort((a, b) => {
    const priorityA = a.priority || 5
    const priorityB = b.priority || 5
    if (priorityA !== priorityB) return priorityB - priorityA
    const nameA = a.providerName ? `${a.providerName} / ${a.name}` : a.name
    const nameB = b.providerName ? `${b.providerName} / ${b.name}` : b.name
    return nameA.localeCompare(nameB)
  })

  container.innerHTML = sortedEndpoints
    .map((ep) => {
      const displayName = ep.providerName ? `${ep.providerName} - ${ep.name}` : ep.name
      // Enabled switch: active endpoints cannot be disabled
      const canToggleEnabled = !ep.active
      // Active button: show "当前使用" if active, "切换" if not
      const activeButton = ep.active
        ? `<span class="badge badge-active">${t('endpoints.currentUse')}</span>`
        : ep.enabled
          ? `<button class="btn-switch" onclick="event.stopPropagation(); setActiveEndpointById(${ep.id})">${t('endpoints.switch')}</button>`
          : ''

      // Get ping result from state if available
      const pingResult = state.pingResults?.[ep.id]
      let pingDisplay = ''
      if (pingResult) {
        if (pingResult.loading) {
          pingDisplay = `<span class="ping-result ping-loading">(...)</span>`
        } else if (pingResult.success) {
          pingDisplay = `<span class="ping-result ping-success">(${pingResult.latency}ms)</span>`
        } else {
          pingDisplay = `<span class="ping-result ping-error">(${t('endpoints.pingFailed')})</span>`
        }
      }

      // Show apply-to-config button for claude/codex endpoints
      const showApplyButton = ep.interfaceType === 'claude' || ep.interfaceType === 'codex'
      const applyButton = showApplyButton
        ? `<button class="icon-btn endpoint-apply-btn" onclick="event.stopPropagation(); applyEndpointToConfig(${ep.id})" title="${t('endpoints.applyToConfig')}">${createIcon('power', { size: 14 })}</button>`
        : ''

      return `
        <div class="endpoint-item ${ep.active ? 'active' : ''} ${!ep.enabled ? 'disabled' : ''}"
            onclick="editEndpointFromList(${ep.id})">
            <div class="endpoint-header">
                <div class="endpoint-title">
                    <span class="endpoint-name">${displayName}</span>
                    ${activeButton}
                </div>
                <div class="endpoint-controls-top" onclick="event.stopPropagation()">
                    <label class="switch ${!canToggleEnabled ? 'switch-locked' : ''}">
                        <input type="checkbox" ${ep.enabled ? 'checked' : ''}
                            ${!canToggleEnabled ? 'disabled' : ''}
                            onchange="toggleEndpointEnabled(${ep.id}, this.checked)">
                        <span class="slider"></span>
                    </label>
                </div>
            </div>
            <div class="endpoint-info">
                <div class="endpoint-url" onclick="event.stopPropagation(); pingSingleEndpoint(${ep.id})" title="${t('endpoints.ping')}">🌐 ${ep.apiUrl} ${pingDisplay}</div>
                <div class="endpoint-daily-stats">
                    <span class="stat-item">📊 ${t('endpoints.requests')}: ${ep.todayRequests || 0}</span>
                    <span class="stat-separator">|</span>
                    <span class="stat-item ${ep.todayErrors > 0 ? 'stat-error' : ''}">${t('endpoints.errors')}: ${ep.todayErrors || 0}</span>
                </div>
                <div class="endpoint-token-stats">
                    <span class="stat-item">🔄 Token ${t('stats.total')}: ${formatTokensWithUnit((ep.todayInput || 0) + (ep.todayOutput || 0))} (${t('stats.input')}: ${formatTokensWithUnit(ep.todayInput || 0)}, ${t('stats.output')}: ${formatTokensWithUnit(ep.todayOutput || 0)})</span>
                    ${applyButton}
                </div>
            </div>
        </div>
    `
    })
    .join('')
}

export function updateActiveSelector(endpoints) {
  const select = document.getElementById('activeEndpointSelect')
  if (!select) return

  const enabledEndpoints = endpoints.filter((ep) => ep.enabled)

  select.innerHTML =
    `<option value="">${t('endpoints.selectActive')}</option>` +
    enabledEndpoints
      .map((ep) => {
        const displayName = ep.providerName ? `${ep.providerName} - ${ep.name}` : ep.name
        return `<option value="${ep.id}" ${ep.active ? 'selected' : ''}>${displayName}</option>`
      })
      .join('')
}

export async function setActiveEndpoint() {
  const select = document.getElementById('activeEndpointSelect')
  const endpointId = select?.value ? parseInt(select.value, 10) : null

  if (!endpointId) return

  try {
    if (window.go?.main?.App?.SetActiveEndpoint) {
      // Get current active endpoint before switching
      const endpoints = state.endpoints[state.currentTab] || []
      const prevEndpoint = endpoints.find((ep) => ep.active)
      const newEndpoint = endpoints.find((ep) => ep.id === endpointId)

      await window.go.main.App.SetActiveEndpoint(state.currentTab, endpointId)
      await loadEndpoints(state.currentTab)

      // Log the switch
      logEndpointSwitch(prevEndpoint, newEndpoint)
    }
  } catch (error) {
    showError('Failed to set active endpoint: ' + error.message)
  }
}

// Set active endpoint by ID (called from radio button)
export async function setActiveEndpointById(endpointId) {
  try {
    if (window.go?.main?.App?.SetActiveEndpoint) {
      // Get current active endpoint before switching
      const endpoints = state.endpoints[state.currentTab] || []
      const prevEndpoint = endpoints.find((ep) => ep.active)
      const newEndpoint = endpoints.find((ep) => ep.id === endpointId)

      await window.go.main.App.SetActiveEndpoint(state.currentTab, endpointId)
      await loadEndpoints(state.currentTab)

      // Log the switch
      logEndpointSwitch(prevEndpoint, newEndpoint)
    }
  } catch (error) {
    showError('Failed to set active endpoint: ' + error.message)
    await loadEndpoints(state.currentTab) // Refresh to reset UI
  }
}

// Log endpoint switch to console
function logEndpointSwitch(prevEndpoint, newEndpoint) {
  const prevName = prevEndpoint ? `${prevEndpoint.providerName || 'unknown'}-${prevEndpoint.name}` : '无'
  const newName = newEndpoint ? `${newEndpoint.providerName || 'unknown'}-${newEndpoint.name}` : '无'

  if (prevName !== newName) {
    logInfo(`手动切换端点: ${prevName} -> ${newName}`)
  }
}

// Toggle endpoint enabled status
export async function toggleEndpointEnabled(endpointId, enabled) {
  try {
    if (window.go?.main?.App?.ToggleEndpointEnabled) {
      await window.go.main.App.ToggleEndpointEnabled(endpointId, enabled)
      await loadEndpoints(state.currentTab)
    }
  } catch (error) {
    showError('Failed to toggle endpoint: ' + error.message)
    await loadEndpoints(state.currentTab) // Refresh to reset UI
  }
}

// Ping a single endpoint
export async function pingSingleEndpoint(endpointId) {
  try {
    if (!window.go?.main?.App?.PingEndpoint) {
      showError('Ping not available')
      return
    }

    // Initialize pingResults if not exists
    if (!state.pingResults) {
      state.pingResults = {}
    }

    // Show loading state
    state.pingResults[endpointId] = { loading: true }
    renderEndpointList(state.endpoints[state.currentTab] || [])

    const result = await window.go.main.App.PingEndpoint(endpointId)
    state.pingResults[endpointId] = result
    renderEndpointList(state.endpoints[state.currentTab] || [])
  } catch (error) {
    state.pingResults[endpointId] = { success: false, error: error.message }
    renderEndpointList(state.endpoints[state.currentTab] || [])
  }
}

// Ping all endpoints of current tab
export async function pingAllEndpoints() {
  try {
    if (!window.go?.main?.App?.PingAllEndpoints) {
      showError('Ping not available')
      return
    }

    // Initialize pingResults if not exists
    if (!state.pingResults) {
      state.pingResults = {}
    }

    // Show loading state for all endpoints
    const endpoints = state.endpoints[state.currentTab] || []
    endpoints.forEach((ep) => {
      state.pingResults[ep.id] = { loading: true }
    })
    renderEndpointList(endpoints)

    const results = await window.go.main.App.PingAllEndpoints(state.currentTab)

    // Update state with results
    if (results) {
      results.forEach((result) => {
        state.pingResults[result.endpointId] = result
      })
    }
    renderEndpointList(state.endpoints[state.currentTab] || [])
  } catch (error) {
    showError('Ping failed: ' + error.message)
  }
}

// Apply endpoint URL to CLI config
export async function applyEndpointToConfig(endpointId) {
  try {
    const endpoints = state.endpoints[state.currentTab] || []
    const endpoint = endpoints.find((ep) => ep.id === endpointId)

    if (!endpoint) {
      showError(t('endpoints.notFound'))
      return
    }

    const interfaceType = endpoint.interfaceType
    if (interfaceType !== 'claude' && interfaceType !== 'codex') {
      showError(t('endpoints.unsupportedType'))
      return
    }

    const cliName = interfaceType === 'claude' ? 'Claude Code' : 'Codex'
    const confirmMessage = t('endpoints.applyUrlConfirm')
      .replace('{name}', cliName)
      .replace('{url}', endpoint.apiUrl)

    const confirmed = await confirm(confirmMessage, {
      title: t('endpoints.applyUrlTitle'),
      confirmText: t('common.ok'),
      cancelText: t('common.cancel'),
    })

    if (!confirmed) {
      return
    }

    if (interfaceType === 'claude') {
      await applyClaudeEndpointUrl(endpoint.apiUrl, endpoint.apiKey)
    } else if (interfaceType === 'codex') {
      await applyCodexEndpointUrl(endpoint.apiUrl, endpoint.apiKey)
    }

    const successMessage = t('endpoints.applyUrlSuccess')
      .replace('{name}', cliName)
      .replace('{url}', endpoint.apiUrl)
    showSuccess(successMessage)
    logInfo(`应用端点 URL 到 ${cliName} 配置: ${endpoint.apiUrl}`)
  } catch (error) {
    showError(t('endpoints.applyUrlFailed') + ': ' + error.message)
  }
}

// Apply Claude endpoint URL to config
async function applyClaudeEndpointUrl(apiUrl, apiKey) {
  if (!window.go?.main?.App?.GetClaudeConfig || !window.go?.main?.App?.SaveClaudeConfig) {
    throw new Error('Backend not available')
  }

  const result = await window.go.main.App.GetClaudeConfig()
  if (!result.success) {
    throw new Error(result.message)
  }

  const settingsFile = result.files.find((f) => f.name === 'settings.json')
  if (!settingsFile) {
    throw new Error('settings.json not found')
  }

  let config
  try {
    config = JSON.parse(settingsFile.content)
  } catch (e) {
    throw new Error('Invalid JSON in settings.json')
  }

  if (!config.env) {
    config.env = {}
  }

  config.env.ANTHROPIC_BASE_URL = apiUrl
  if (apiKey) {
    config.env.ANTHROPIC_AUTH_TOKEN = apiKey
  }

  const newContent = JSON.stringify(config, null, 2)
  await window.go.main.App.SaveClaudeConfig(newContent)
}

// Apply Codex endpoint URL to config
async function applyCodexEndpointUrl(apiUrl, apiKey) {
  if (!window.go?.main?.App?.GetCodexConfig || !window.go?.main?.App?.SaveCodexConfig) {
    throw new Error('Backend not available')
  }

  const result = await window.go.main.App.GetCodexConfig()
  if (!result.success) {
    throw new Error(result.message)
  }

  const configFile = result.files.find((f) => f.name === 'config.toml')
  const authFile = result.files.find((f) => f.name === 'auth.json')

  if (!configFile) {
    throw new Error('config.toml not found')
  }

  let configToml = configFile.content
  const lines = configToml.split('\n')
  const newLines = []
  let inLocalProvider = false

  for (const line of lines) {
    const trimmed = line.trim()

    if (trimmed.startsWith('[model_providers.local]')) {
      inLocalProvider = true
    } else if (trimmed.startsWith('[') && !trimmed.startsWith('[model_providers.local]')) {
      inLocalProvider = false
    }

    if (inLocalProvider && trimmed.startsWith('base_url')) {
      newLines.push(`base_url = '${apiUrl}'`)
    } else {
      newLines.push(line)
    }
  }

  const newConfigToml = newLines.join('\n')

  // Update auth.json with API key
  let authJson = authFile ? authFile.content : '{}'
  let auth
  try {
    auth = JSON.parse(authJson)
  } catch (e) {
    auth = {}
  }

  if (apiKey) {
    auth.OPENAI_API_KEY = apiKey
  }

  const newAuthJson = JSON.stringify(auth, null, 2)
  await window.go.main.App.SaveCodexConfig(newConfigToml, newAuthJson)
}
