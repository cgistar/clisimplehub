/**
 * Kiro configuration module
 */
import { showError, showSuccess } from './utils.js'
import { t } from '../i18n/index.js'
import { clearTransformersCache } from './endpoint-form.js'
import { logError, logKiroUsageDetails } from './console.js'
import { generateRandomString, generateCodeChallenge, generateUUID } from './utils-crypto.js'

let initialRefreshToken = ''
let testedRefreshToken = ''
let testedKiroCreds = null
let loadedKiroProvider = ''

function getKiroUsageInfoEl() {
  return document.getElementById('kiroUsageInfo')
}

function setKiroUsageInfoText(text) {
  const el = getKiroUsageInfoEl()
  if (!el) return

  const normalized = String(text || '').trim()
  el.textContent = normalized
  el.style.display = normalized ? '' : 'none'
}

function updateKiroUsageButton() {
  const accessToken = (document.getElementById('kiroAccessToken')?.value || '').trim()
  const btn = document.getElementById('fetchKiroUsageBtn')
  if (btn) btn.disabled = !accessToken

  if (!accessToken) {
    setKiroUsageInfoText('')
  }
}

function extractRegionFromProfileArn(profileArn) {
  const arn = String(profileArn || '').trim()
  if (!arn) return ''

  const parts = arn.split(':')
  if (parts.length < 6 || parts[0] !== 'arn') return ''

  const region = (parts[3] || '').trim()
  if (!/^[a-z]{2}-[a-z0-9-]+-\\d+$/.test(region)) return ''

  return region
}

function trySelectKiroRegion(region) {
  const normalized = String(region || '').trim()
  if (!normalized) return false

  const select = document.getElementById('kiroRegion')
  if (!select) return false

  const hasOption = Array.from(select.options).some((o) => o.value === normalized)
  if (!hasOption) return false

  select.value = normalized
  syncKiroRegionDisplay()
  return true
}

export async function showKiroConfigModal() {
  try {
    if (window.go?.main?.App?.GetKiroConfig) {
      const config = await window.go.main.App.GetKiroConfig()
      document.getElementById('kiroRefreshToken').value = config.refreshToken || ''
      document.getElementById('kiroAccessToken').value = config.accessToken || ''
      document.getElementById('kiroProfileArn').value = config.profileArn || ''
      document.getElementById('kiroRegion').value = config.region || 'us-east-1'
      document.getElementById('kiroProxyUrl').value = config.proxyUrl || ''
      document.getElementById('kiroUserAgent').value = config.userAgent || ''
      document.getElementById('kiroVersion').value = config.version || ''
      document.getElementById('kiroBufferedStream').checked = config.bufferedStream || false
      document.getElementById('kiroAuthMethod').value = config.authMethod || 'social'
      document.getElementById('kiroClientId').value = config.clientId || ''
      document.getElementById('kiroClientSecret').value = config.clientSecret || ''
      loadedKiroProvider = config.provider || ''

      // Sync region dropdown display
      syncKiroRegionDisplay()
      // Sync auth method dropdown display
      syncKiroAuthMethodDisplay()
      // Update IdC fields visibility
      updateIdcFieldsVisibility()

      initialRefreshToken = (config.refreshToken || '').trim()
      testedRefreshToken = ''
      testedKiroCreds = null
      setKiroUsageInfoText('')
      updateKiroConfigButtons()
    }
  } catch (error) {
    console.error('Failed to load Kiro config:', error)
  }

  document.getElementById('kiroConfigModal').classList.add('active')
}

export function closeKiroConfigModal() {
  document.getElementById('kiroConfigModal').classList.remove('active')
}

export function onKiroRefreshTokenInput() {
  const current = (document.getElementById('kiroRefreshToken')?.value || '').trim()
  if (current !== testedRefreshToken) {
    testedRefreshToken = ''
    testedKiroCreds = null
    setKiroUsageInfoText('')
    const accessTokenEl = document.getElementById('kiroAccessToken')
    if (accessTokenEl) accessTokenEl.value = ''
    const profileArnEl = document.getElementById('kiroProfileArn')
    if (profileArnEl) profileArnEl.value = ''
  }
  updateKiroConfigButtons()
}

export function onKiroIdcFieldsInput() {
  // Reset tested state when IdC fields change
  const current = (document.getElementById('kiroRefreshToken')?.value || '').trim()
  if (current === testedRefreshToken) {
    testedRefreshToken = ''
    testedKiroCreds = null
    setKiroUsageInfoText('')
    const accessTokenEl = document.getElementById('kiroAccessToken')
    if (accessTokenEl) accessTokenEl.value = ''
    const profileArnEl = document.getElementById('kiroProfileArn')
    if (profileArnEl) profileArnEl.value = ''
  }
  updateKiroConfigButtons()
}

export function onKiroAuthMethodChange() {
  updateIdcFieldsVisibility()
  onKiroIdcFieldsInput()
}

function updateIdcFieldsVisibility() {
  const authMethod = (document.getElementById('kiroAuthMethod')?.value || 'social').trim().toLowerCase()
  const idcFields = document.getElementById('kiroIdcFields')
  const idcSecretField = document.getElementById('kiroIdcSecretField')
  const idcLoginButton = document.getElementById('kiroIdcLoginButton')

  if (authMethod === 'idc') {
    if (idcFields) idcFields.style.display = ''
    if (idcSecretField) idcSecretField.style.display = ''
    if (idcLoginButton) idcLoginButton.style.display = ''
  } else {
    if (idcFields) idcFields.style.display = 'none'
    if (idcSecretField) idcSecretField.style.display = 'none'
    if (idcLoginButton) idcLoginButton.style.display = 'none'
  }
}

function isRefreshTokenChanged() {
  const current = (document.getElementById('kiroRefreshToken')?.value || '').trim()
  return current !== initialRefreshToken
}

function updateKiroConfigButtons() {
  const refreshToken = (document.getElementById('kiroRefreshToken')?.value || '').trim()
  const changed = isRefreshTokenChanged()

  const testBtn = document.getElementById('testKiroRefreshTokenBtn')
  if (testBtn) {
    testBtn.disabled = !refreshToken
  }

  const saveBtn = document.getElementById('saveKiroConfigBtn')
  if (saveBtn) {
    const canSave = !changed || (testedRefreshToken === refreshToken && testedKiroCreds?.accessToken)
    saveBtn.disabled = !canSave
  }

  updateKiroUsageButton()
}

export async function testKiroRefreshToken() {
  const refreshTokenEl = document.getElementById('kiroRefreshToken')
  const regionEl = document.getElementById('kiroRegion')
  const proxyUrlEl = document.getElementById('kiroProxyUrl')
  const versionEl = document.getElementById('kiroVersion')
  const authMethodEl = document.getElementById('kiroAuthMethod')
  const clientIdEl = document.getElementById('kiroClientId')
  const clientSecretEl = document.getElementById('kiroClientSecret')

  const refreshToken = (refreshTokenEl?.value || '').trim()
  const region = (regionEl?.value || 'us-east-1').trim()
  const proxyUrl = (proxyUrlEl?.value || '').trim()
  const version = (versionEl?.value || '').trim()
  const authMethod = (authMethodEl?.value || 'social').trim()
  const clientId = (clientIdEl?.value || '').trim()
  const clientSecret = (clientSecretEl?.value || '').trim()

  if (!refreshToken) {
    showError(t('kiro.refreshTokenRequired'))
    return
  }

  // Validate IdC fields
  if (authMethod.toLowerCase() === 'idc') {
    if (!clientId || !clientSecret) {
      showError(t('kiro.idcFieldsRequired'))
      return
    }
  }

  const btn = document.getElementById('testKiroRefreshTokenBtn')
  const btnText = document.getElementById('testKiroRefreshTokenBtnText')
  const saveBtn = document.getElementById('saveKiroConfigBtn')
  try {
    if (btn) btn.disabled = true
    if (saveBtn) saveBtn.disabled = true
    if (btnText) btnText.textContent = t('kiro.testing')

    if (!window.go?.main?.App?.TestKiroRefreshToken) {
      throw new Error('TestKiroRefreshToken is not available')
    }

    const result = await window.go.main.App.TestKiroRefreshToken({
      refreshToken,
      region,
      proxyUrl,
      version,
      authMethod,
      clientId,
      clientSecret,
    })

    if (!result?.accessToken) {
      throw new Error('Empty accessToken')
    }

    // Some backends may rotate refreshToken; if so, persist the new one for saving.
    const refreshTokenToSave = (result.refreshToken || refreshToken).trim()
    if (refreshTokenEl && refreshTokenToSave && refreshTokenToSave !== refreshToken) {
      refreshTokenEl.value = refreshTokenToSave
    }

    testedRefreshToken = refreshTokenToSave
    testedKiroCreds = {
      accessToken: result.accessToken,
      expiresAt: result.expiresAt || '',
      profileArn: result.profileArn || '',
      region: result.region || region,
      refreshToken: refreshTokenToSave,
      authMethod,
      clientId,
      clientSecret,
    }

    const accessTokenEl = document.getElementById('kiroAccessToken')
    if (accessTokenEl) accessTokenEl.value = result.accessToken
    const profileArnEl = document.getElementById('kiroProfileArn')
    if (profileArnEl) profileArnEl.value = result.profileArn || ''

    const regionFromArn = extractRegionFromProfileArn(result.profileArn)
    if (regionFromArn && trySelectKiroRegion(regionFromArn) && testedKiroCreds) {
      testedKiroCreds.region = regionFromArn
    }

    showSuccess(t('kiro.testSuccess'))
  } catch (error) {
    console.error('TestKiroRefreshToken error:', error)
    showError(t('kiro.testFailedPrefix') + (error?.message || error || 'Unknown error'))
  } finally {
    if (btn) btn.disabled = false
    if (btnText) btnText.textContent = t('kiro.test')
    updateKiroConfigButtons()
  }
}

function formatUsageNumber(value) {
  const num = Number(value)
  if (!Number.isFinite(num)) return '0'
  return num.toLocaleString(undefined, { maximumFractionDigits: 4 })
}

function formatUsagePct(value) {
  const num = Number(value)
  if (!Number.isFinite(num)) return ''
  return `${num.toFixed(1)}%`
}

function normalizeErrorMessage(error) {
  if (error === undefined || error === null) return ''
  if (typeof error === 'string') return error
  if (typeof error?.message === 'string') return error.message
  try {
    return JSON.stringify(error)
  } catch {
    return String(error)
  }
}

function parseKiroUsageHttpError(error) {
  const message = normalizeErrorMessage(error)
  const marker = 'KIRO_USAGE_HTTP_ERROR:'
  const idx = message.indexOf(marker)
  if (idx < 0) return null

  const jsonText = message.slice(idx + marker.length).trim()
  if (!jsonText) return null

  try {
    const parsed = JSON.parse(jsonText)
    if (!parsed || typeof parsed !== 'object') return null
    return parsed
  } catch {
    return null
  }
}

function formatKiroUsageHttpError(detail) {
  const statusCode = Number(detail?.statusCode)
  const hasStatusCode = Number.isFinite(statusCode) && statusCode > 0
  const statusText = hasStatusCode ? `HTTP ${statusCode}` : 'HTTP error'

  const hint = String(detail?.hint || '').trim()
  const body = String(detail?.body || '').trim()

  let msg = hint || statusText
  if (hint && hasStatusCode) msg += ` (${statusText})`
  if (!hint && hasStatusCode) msg = statusText
  if (body) msg += `: ${body}`

  return msg
}

export async function fetchKiroUsage() {
  const accessToken = (document.getElementById('kiroAccessToken')?.value || '').trim()
  const refreshToken = (document.getElementById('kiroRefreshToken')?.value || '').trim()
  const profileArn = (document.getElementById('kiroProfileArn')?.value || '').trim()
  const region = (document.getElementById('kiroRegion')?.value || 'us-east-1').trim()
  const proxyUrl = (document.getElementById('kiroProxyUrl')?.value || '').trim()
  const userAgent = (document.getElementById('kiroUserAgent')?.value || '').trim()
  const version = (document.getElementById('kiroVersion')?.value || '').trim()

  if (!accessToken) {
    showError(t('kiro.accessTokenRequiredForUsage'))
    return
  }

  const btn = document.getElementById('fetchKiroUsageBtn')
  const btnText = document.getElementById('fetchKiroUsageBtnText')
  if (btn) btn.disabled = true
  if (btnText) btnText.textContent = t('kiro.usageLoading')
  setKiroUsageInfoText(t('kiro.usageLoading'))

  try {
    if (!window.go?.main?.App?.GetKiroUsage) {
      throw new Error('GetKiroUsage is not available')
    }
    const result = await window.go.main.App.GetKiroUsage({
      accessToken,
      refreshToken,
      profileArn,
      region,
      proxyUrl,
      userAgent,
      version,
    })

    const subscriptionTitle = String(result?.subscriptionTitle || '').trim()
    const used = formatUsageNumber(result?.currentUsage)
    const limit = formatUsageNumber(result?.usageLimit)
    const balance = formatUsageNumber(result?.balance)
    const pct = formatUsagePct(result?.usagePct)

    let text = `${t('kiro.usageInfoPrefix')}${used} / ${limit}`
    if (pct) text += ` (${pct})`
    text += `，${t('kiro.usageRemainingPrefix')}${balance}`

    if (subscriptionTitle) {
      text = `${t('kiro.usageSubscriptionPrefix')}${subscriptionTitle} · ${text}`
    }

    setKiroUsageInfoText(text)
    logKiroUsageDetails(result)
  } catch (error) {
    console.error('GetKiroUsage error:', error)
    const httpDetail = parseKiroUsageHttpError(error)
    const detailMessage = httpDetail
      ? formatKiroUsageHttpError(httpDetail)
      : normalizeErrorMessage(error) || 'Unknown error'
    const msg = t('kiro.usageFailedPrefix') + detailMessage
    showError(msg)
    setKiroUsageInfoText(msg)
    logError(`[KiroUsage] ${msg}`)
  } finally {
    if (btnText) btnText.textContent = t('kiro.usage')
    updateKiroConfigButtons()
  }
}

// 为账号管理界面获取用量（用于激活账号）
export async function fetchKiroUsageForAccount(account) {
  if (!account) {
    throw new Error('Account is required')
  }

  if (!account.accessToken) {
    throw new Error(t('kiro.accessTokenRequiredForUsage'))
  }

  try {
    if (!window.go?.main?.App?.GetKiroUsage) {
      throw new Error('GetKiroUsage is not available')
    }

    const result = await window.go.main.App.GetKiroUsage({
      accessToken: account.accessToken || '',
      refreshToken: account.refreshToken || '',
      profileArn: account.profileArn || '',
      region: account.region || 'us-east-1',
      proxyUrl: account.proxyUrl || '',
      userAgent: account.userAgent || '',
      version: account.version || '',
    })

    logKiroUsageDetails(result)
    return result
  } catch (error) {
    console.error('GetKiroUsage error:', error)
    const httpDetail = parseKiroUsageHttpError(error)
    const detailMessage = httpDetail
      ? formatKiroUsageHttpError(httpDetail)
      : normalizeErrorMessage(error) || 'Unknown error'
    const msg = t('kiro.usageFailedPrefix') + detailMessage
    logError(`[KiroUsage] ${msg}`)
    throw new Error(detailMessage)
  }
}

export async function saveKiroConfig() {
  const refreshToken = document.getElementById('kiroRefreshToken').value.trim()
  const region = document.getElementById('kiroRegion').value
  const proxyUrl = document.getElementById('kiroProxyUrl').value.trim()
  const userAgent = document.getElementById('kiroUserAgent').value.trim()
  const version = document.getElementById('kiroVersion').value.trim()
  const bufferedStream = document.getElementById('kiroBufferedStream').checked
  const authMethod = document.getElementById('kiroAuthMethod').value.trim()
  const clientId = document.getElementById('kiroClientId').value.trim()
  const clientSecret = document.getElementById('kiroClientSecret').value.trim()

  if (!refreshToken) {
    showError(t('kiro.refreshTokenRequired'))
    return
  }

  // Validate IdC fields
  if (authMethod.toLowerCase() === 'idc') {
    if (!clientId || !clientSecret) {
      showError(t('kiro.idcFieldsRequired'))
      return
    }
  }

  const changed = isRefreshTokenChanged()
  if (changed) {
    if (testedRefreshToken !== refreshToken || !testedKiroCreds?.accessToken) {
      showError(t('kiro.testRequired'))
      return
    }
  }

  const shouldPersistAuthState = testedRefreshToken === refreshToken && testedKiroCreds?.accessToken

  try {
    if (window.go?.main?.App?.SaveKiroConfig) {
      // 根据authMethod构建配置对象，只包含相应类型的字段
      const config = {
        refreshToken: testedKiroCreds?.refreshToken || refreshToken,
        region: testedKiroCreds?.region || region,
        proxyUrl,
        userAgent,
        version,
        bufferedStream,
        authMethod,
        accessToken: shouldPersistAuthState ? testedKiroCreds?.accessToken || '' : '',
        expiresAt: shouldPersistAuthState ? testedKiroCreds?.expiresAt || '' : '',
      }

      // 根据authMethod添加类型特定的字段
      if (authMethod.toLowerCase() === 'idc') {
        // IdC类型：包含clientId和clientSecret，不包含profileArn
        config.clientId = clientId
        config.clientSecret = clientSecret
        config.provider = testedKiroCreds?.provider || loadedKiroProvider || ''
        config.profileArn = ''
      } else {
        // Social类型：包含provider和profileArn，不包含clientId和clientSecret
        config.provider = testedKiroCreds?.provider || ''
        config.profileArn = shouldPersistAuthState ? testedKiroCreds?.profileArn || '' : ''
        config.clientId = ''
        config.clientSecret = ''
      }

      await window.go.main.App.SaveKiroConfig(config)
    }

    // 检查并创建 kiro 端点
    await ensureKiroEndpoint()

    closeKiroConfigModal()
    showSuccess(t('kiro.saveSuccess'))
    clearTransformersCache()
  } catch (error) {
    console.error('SaveKiroConfig error:', error)
    showError(t('kiro.saveFailedPrefix') + (error?.message || error || 'Unknown error'))
  }
}

// Sync region dropdown display
// AWS regions list
const AWS_REGIONS = [
  { value: 'us-east-1', label: 'us-east-1 (N. Virginia)' },
  { value: 'us-east-2', label: 'us-east-2 (Ohio)' },
  { value: 'us-west-1', label: 'us-west-1 (N. California)' },
  { value: 'us-west-2', label: 'us-west-2 (Oregon)' },
  { value: 'af-south-1', label: 'af-south-1 (Cape Town)' },
  { value: 'eu-west-1', label: 'eu-west-1 (Ireland)' },
  { value: 'eu-central-1', label: 'eu-central-1 (Frankfurt)' },
  { value: 'ap-east-1', label: 'ap-east-1 (Hong Kong)' },
  { value: 'ap-east-2', label: 'ap-east-2 (Taipei)' },
  { value: 'ap-northeast-1', label: 'ap-northeast-1 (Tokyo)' },
  { value: 'ap-southeast-1', label: 'ap-southeast-1 (Singapore)' },
]

function syncKiroRegionDisplay() {
  // No-op: kiroRegion is now a direct editable input
}

// Handle region input changes (filter dropdown)
export function onKiroRegionInput() {
  const input = document.getElementById('kiroRegion')
  const dropdown = document.getElementById('kiroRegionDropdown')
  if (!input || !dropdown) return

  const query = (input.value || '').toLowerCase().trim()

  // Filter and render dropdown
  const filtered = AWS_REGIONS.filter(
    (r) => r.value.toLowerCase().includes(query) || r.label.toLowerCase().includes(query)
  )

  renderKiroRegionDropdown(filtered)

  // Auto-show dropdown when typing
  if (query && filtered.length > 0) {
    dropdown.classList.add('show')
  }
}

// Render region dropdown options
function renderKiroRegionDropdown(regions = AWS_REGIONS) {
  const input = document.getElementById('kiroRegion')
  const dropdown = document.getElementById('kiroRegionDropdown')
  if (!input || !dropdown) return

  const currentValue = (input.value || '').trim()

  dropdown.innerHTML = ''
  regions.forEach((region) => {
    const item = document.createElement('div')
    item.className = 'model-dropdown-item'
    if (region.value === currentValue) {
      item.classList.add('selected')
    }
    item.textContent = region.label
    item.onclick = () => selectKiroRegion(region.value)
    dropdown.appendChild(item)
  })
}

// Select region from dropdown
function selectKiroRegion(value) {
  const input = document.getElementById('kiroRegion')
  if (!input) return

  input.value = value

  // Close dropdown
  const dropdown = document.getElementById('kiroRegionDropdown')
  if (dropdown) {
    dropdown.classList.remove('show')
  }
}

// Toggle region dropdown
export function toggleKiroRegionDropdown() {
  const dropdown = document.getElementById('kiroRegionDropdown')
  const input = document.getElementById('kiroRegion')
  if (!dropdown || !input) return

  if (dropdown.classList.contains('show')) {
    dropdown.classList.remove('show')
  } else {
    renderKiroRegionDropdown()
    dropdown.classList.add('show')
  }
}

// Sync auth method dropdown display
function syncKiroAuthMethodDisplay() {
  const select = document.getElementById('kiroAuthMethod')
  const display = document.getElementById('kiroAuthMethodDisplay')
  if (!select || !display) return

  const selectedOption = select.options[select.selectedIndex]
  if (selectedOption) {
    display.value = selectedOption.textContent.trim()
  }

  renderKiroAuthMethodDropdown()
}

// Render auth method dropdown options
function renderKiroAuthMethodDropdown() {
  const select = document.getElementById('kiroAuthMethod')
  const dropdown = document.getElementById('kiroAuthMethodDropdown')
  if (!select || !dropdown) return

  dropdown.innerHTML = ''
  Array.from(select.options).forEach((option) => {
    const item = document.createElement('div')
    item.className = 'model-dropdown-item'
    if (option.selected) {
      item.classList.add('selected')
    }
    item.textContent = option.textContent.trim()
    item.onclick = () => selectKiroAuthMethod(option.value)
    dropdown.appendChild(item)
  })
}

// Select auth method from dropdown
function selectKiroAuthMethod(value) {
  const select = document.getElementById('kiroAuthMethod')
  if (!select) return

  select.value = value
  syncKiroAuthMethodDisplay()
  onKiroAuthMethodChange()

  // Close dropdown
  const dropdown = document.getElementById('kiroAuthMethodDropdown')
  if (dropdown) {
    dropdown.classList.remove('show')
  }
}

// Toggle auth method dropdown
export function toggleKiroAuthMethodDropdown() {
  const dropdown = document.getElementById('kiroAuthMethodDropdown')
  const select = document.getElementById('kiroAuthMethod')
  if (!dropdown || !select) return

  if (dropdown.classList.contains('show')) {
    dropdown.classList.remove('show')
  } else {
    renderKiroAuthMethodDropdown()
    dropdown.classList.add('show')
  }
}

// Sync rotation mode dropdown display
function syncKiroRotationModeDisplay() {
  const select = document.getElementById('kiroRotationMode')
  const display = document.getElementById('kiroRotationModeDisplay')
  if (!select || !display) return

  const selectedOption = select.options[select.selectedIndex]
  if (selectedOption) {
    display.value = selectedOption.textContent.trim()
  }

  renderKiroRotationModeDropdown()
}

// Render rotation mode dropdown options
function renderKiroRotationModeDropdown() {
  const select = document.getElementById('kiroRotationMode')
  const dropdown = document.getElementById('kiroRotationModeDropdown')
  if (!select || !dropdown) return

  dropdown.innerHTML = ''
  Array.from(select.options).forEach((option) => {
    const item = document.createElement('div')
    item.className = 'model-dropdown-item'
    if (option.selected) {
      item.classList.add('selected')
    }
    item.textContent = option.textContent.trim()
    item.onclick = () => selectKiroRotationMode(option.value)
    dropdown.appendChild(item)
  })
}

// Select rotation mode from dropdown
function selectKiroRotationMode(value) {
  const select = document.getElementById('kiroRotationMode')
  if (!select) return

  select.value = value
  syncKiroRotationModeDisplay()
  onKiroRotationModeChange()

  // Close dropdown
  const dropdown = document.getElementById('kiroRotationModeDropdown')
  if (dropdown) {
    dropdown.classList.remove('show')
  }
}

// Toggle rotation mode dropdown
export function toggleKiroRotationModeDropdown() {
  const dropdown = document.getElementById('kiroRotationModeDropdown')
  const select = document.getElementById('kiroRotationMode')
  if (!dropdown || !select) return

  if (dropdown.classList.contains('show')) {
    dropdown.classList.remove('show')
  } else {
    renderKiroRotationModeDropdown()
    dropdown.classList.add('show')
  }
}

// Handle rotation mode change
export function onKiroRotationModeChange() {
  // Placeholder for future logic if needed
}

// Register global functions for HTML onclick handlers
window.toggleKiroAuthMethodDropdown = toggleKiroAuthMethodDropdown
window.toggleKiroRotationModeDropdown = toggleKiroRotationModeDropdown
window.onKiroRotationModeChange = onKiroRotationModeChange
window.onKiroGlobalRegionInput = onKiroGlobalRegionInput
window.toggleKiroGlobalRegionDropdown = toggleKiroGlobalRegionDropdown
window.onEditKiroRegionInput = onEditKiroRegionInput
window.toggleEditKiroRegionDropdown = toggleEditKiroRegionDropdown
window.toggleEditKiroAuthMethodDropdown = toggleEditKiroAuthMethodDropdown
window.syncEditKiroAuthMethodDisplay = syncEditKiroAuthMethodDisplay

// =============================================================================
// Kiro Global Config Modal - Region Dropdown
// =============================================================================

// Handle global region input changes (filter dropdown)
export function onKiroGlobalRegionInput() {
  const input = document.getElementById('kiroGlobalRegion')
  const dropdown = document.getElementById('kiroGlobalRegionDropdown')
  if (!input || !dropdown) return

  const query = (input.value || '').toLowerCase().trim()

  const filtered = AWS_REGIONS.filter(
    (r) => r.value.toLowerCase().includes(query) || r.label.toLowerCase().includes(query)
  )

  renderKiroGlobalRegionDropdown(filtered)

  if (query && filtered.length > 0) {
    dropdown.classList.add('show')
  }
}

// Render global region dropdown options
function renderKiroGlobalRegionDropdown(regions = AWS_REGIONS) {
  const input = document.getElementById('kiroGlobalRegion')
  const dropdown = document.getElementById('kiroGlobalRegionDropdown')
  if (!input || !dropdown) return

  const currentValue = (input.value || '').trim()

  dropdown.innerHTML = ''
  regions.forEach((region) => {
    const item = document.createElement('div')
    item.className = 'model-dropdown-item'
    if (region.value === currentValue) {
      item.classList.add('selected')
    }
    item.textContent = region.label
    item.onclick = () => selectKiroGlobalRegion(region.value)
    dropdown.appendChild(item)
  })
}

// Select global region from dropdown
function selectKiroGlobalRegion(value) {
  const input = document.getElementById('kiroGlobalRegion')
  if (!input) return

  input.value = value

  const dropdown = document.getElementById('kiroGlobalRegionDropdown')
  if (dropdown) {
    dropdown.classList.remove('show')
  }
}

// Toggle global region dropdown
export function toggleKiroGlobalRegionDropdown() {
  const dropdown = document.getElementById('kiroGlobalRegionDropdown')
  const input = document.getElementById('kiroGlobalRegion')
  if (!dropdown || !input) return

  if (dropdown.classList.contains('show')) {
    dropdown.classList.remove('show')
  } else {
    renderKiroGlobalRegionDropdown()
    dropdown.classList.add('show')
  }
}

// =============================================================================
// Edit Kiro Account Modal - Region Dropdown
// =============================================================================

// Handle edit region input changes (filter dropdown)
export function onEditKiroRegionInput() {
  const input = document.getElementById('editKiroRegion')
  const dropdown = document.getElementById('editKiroRegionDropdown')
  if (!input || !dropdown) return

  const query = (input.value || '').toLowerCase().trim()

  // Filter and render dropdown
  const filtered = AWS_REGIONS.filter(
    (r) => r.value.toLowerCase().includes(query) || r.label.toLowerCase().includes(query)
  )

  renderEditKiroRegionDropdown(filtered)

  // Auto-show dropdown when typing
  if (query && filtered.length > 0) {
    dropdown.classList.add('show')
  }
}

// Render edit region dropdown options
function renderEditKiroRegionDropdown(regions = AWS_REGIONS) {
  const input = document.getElementById('editKiroRegion')
  const dropdown = document.getElementById('editKiroRegionDropdown')
  if (!input || !dropdown) return

  const currentValue = (input.value || '').trim()

  dropdown.innerHTML = ''
  regions.forEach((region) => {
    const item = document.createElement('div')
    item.className = 'model-dropdown-item'
    if (region.value === currentValue) {
      item.classList.add('selected')
    }
    item.textContent = region.label
    item.onclick = () => selectEditKiroRegion(region.value)
    dropdown.appendChild(item)
  })
}

// Select edit region from dropdown
function selectEditKiroRegion(value) {
  const input = document.getElementById('editKiroRegion')
  if (!input) return

  input.value = value

  // Close dropdown
  const dropdown = document.getElementById('editKiroRegionDropdown')
  if (dropdown) {
    dropdown.classList.remove('show')
  }
}

// Toggle edit region dropdown
export function toggleEditKiroRegionDropdown() {
  const dropdown = document.getElementById('editKiroRegionDropdown')
  const input = document.getElementById('editKiroRegion')
  if (!dropdown || !input) return

  if (dropdown.classList.contains('show')) {
    dropdown.classList.remove('show')
  } else {
    renderEditKiroRegionDropdown()
    dropdown.classList.add('show')
  }
}

// =============================================================================
// Edit Kiro Account Modal - Auth Method Dropdown
// =============================================================================

// Sync edit auth method dropdown display
function syncEditKiroAuthMethodDisplay() {
  const select = document.getElementById('editKiroAuthMethod')
  const display = document.getElementById('editKiroAuthMethodDisplay')
  if (!select || !display) return

  const selectedOption = select.options[select.selectedIndex]
  if (selectedOption) {
    display.value = selectedOption.textContent.trim()
  }

  renderEditKiroAuthMethodDropdown()
}

// Render edit auth method dropdown options
function renderEditKiroAuthMethodDropdown() {
  const select = document.getElementById('editKiroAuthMethod')
  const dropdown = document.getElementById('editKiroAuthMethodDropdown')
  if (!select || !dropdown) return

  dropdown.innerHTML = ''
  Array.from(select.options).forEach((option) => {
    const item = document.createElement('div')
    item.className = 'model-dropdown-item'
    if (option.selected) {
      item.classList.add('selected')
    }
    item.textContent = option.textContent.trim()
    item.onclick = () => selectEditKiroAuthMethod(option.value)
    dropdown.appendChild(item)
  })
}

// Select edit auth method from dropdown
function selectEditKiroAuthMethod(value) {
  const select = document.getElementById('editKiroAuthMethod')
  if (!select) return

  select.value = value
  syncEditKiroAuthMethodDisplay()

  // Trigger the original change handler
  if (window.onEditKiroAuthMethodChange) {
    window.onEditKiroAuthMethodChange()
  }

  // Close dropdown
  const dropdown = document.getElementById('editKiroAuthMethodDropdown')
  if (dropdown) {
    dropdown.classList.remove('show')
  }
}

// Toggle edit auth method dropdown
export function toggleEditKiroAuthMethodDropdown() {
  const dropdown = document.getElementById('editKiroAuthMethodDropdown')
  const select = document.getElementById('editKiroAuthMethod')
  if (!dropdown || !select) return

  if (dropdown.classList.contains('show')) {
    dropdown.classList.remove('show')
  } else {
    renderEditKiroAuthMethodDropdown()
    dropdown.classList.add('show')
  }
}

// 切换 Client Secret 密码可见性
export function toggleKiroClientSecretVisibility() {
  const input = document.getElementById('kiroClientSecret')
  const btn = document.getElementById('toggleKiroClientSecretVisibility')
  if (!input || !btn) return

  if (input.type === 'password') {
    input.type = 'text'
    btn.textContent = '🙈'
  } else {
    input.type = 'password'
    btn.textContent = '👁️'
  }
}

// =============================================================================
// IDC Device Flow Authentication
// =============================================================================

// IDC Device Flow 状态管理
const idcDeviceFlowState = {
  loading: false,
  clientId: '',
  clientSecret: '',
  verifyUrl: '',
  userCode: '',
  deviceCode: '',
  pollTimer: null,
  pollIntervalMs: 2000,
  pollInFlight: false,
  pollingEnabled: false,
  lastPollResult: null,
  activeFlow: 'standard', // 'standard' (Builder ID) | 'org' (Organization Identity)
  region: 'us-east-1', // 当前使用的 region（用于轮询）
}

function getIdcProviderForActiveFlow(flow) {
  return flow === 'org' ? 'Enterprise' : 'BuilderId'
}

function getIdcAccountAddContext() {
  const ctx = window.__kiroIdcAccountAddContext
  if (!ctx || typeof ctx !== 'object') return null
  if (!ctx.enabled) return null
  return ctx
}

function clearIdcAccountAddContext() {
  window.__kiroIdcAccountAddContext = null
}

// 停止轮询
function stopIdcPolling() {
  idcDeviceFlowState.pollingEnabled = false
  if (idcDeviceFlowState.pollTimer) {
    clearTimeout(idcDeviceFlowState.pollTimer)
    idcDeviceFlowState.pollTimer = null
  }
  idcDeviceFlowState.pollInFlight = false
}

// 调度下一次轮询
function scheduleNextIdcPoll() {
  if (!idcDeviceFlowState.pollingEnabled) return
  if (idcDeviceFlowState.pollTimer) clearTimeout(idcDeviceFlowState.pollTimer)

  idcDeviceFlowState.pollTimer = setTimeout(async () => {
    await pollIdcTokenOnce()
    scheduleNextIdcPoll()
  }, idcDeviceFlowState.pollIntervalMs)
}

// 单次轮询
async function pollIdcTokenOnce() {
  if (!idcDeviceFlowState.pollingEnabled) return
  if (idcDeviceFlowState.pollInFlight) return

  idcDeviceFlowState.pollInFlight = true

  try {
    // 使用保存的 region（而不是从 DOM 读取）
    const region = idcDeviceFlowState.region || 'us-east-1'

    if (!window.go?.main?.App?.PollIdcToken) {
      throw new Error('PollIdcToken API not available')
    }

    const result = await window.go.main.App.PollIdcToken({
      clientId: idcDeviceFlowState.clientId,
      clientSecret: idcDeviceFlowState.clientSecret,
      deviceCode: idcDeviceFlowState.deviceCode,
      region: region,
    })

    // 成功获取 token
    if (result?.accessToken) {
      idcDeviceFlowState.lastPollResult = result
      stopIdcPolling()
      updateIdcDialogStatus('ok', t('kiro.idcStatusSuccess'), 'SUCCESS', idcDeviceFlowState.activeFlow)

      // 回填凭证到 Kiro Config Modal
      fillIdcCredentials(result)

      // 如果从账号管理界面发起，自动写入 kiro.json（多账号配置）
      await tryAddIdcAccountFromAccountManagement(result)

      // 延迟关闭 dialog
      setTimeout(() => {
        if (idcDeviceFlowState.activeFlow === 'org') {
          closeIdcOrgLoginDialog()
        } else {
          closeIdcDeviceFlowDialog()
        }
        showSuccess(t('kiro.idcLoginSuccess'))
      }, 1000)
      return
    }

    // PENDING 状态
    if (result?.error === 'authorization_pending') {
      updateIdcDialogStatus('pending', t('kiro.idcStatusPending'), 'PENDING', idcDeviceFlowState.activeFlow)
      return
    }

    // SLOW_DOWN
    if (result?.error === 'slow_down') {
      idcDeviceFlowState.pollIntervalMs = Math.min(idcDeviceFlowState.pollIntervalMs + 2000, 10000)
      updateIdcDialogStatus(
        'pending',
        `${t('kiro.idcStatusSlowDown')} ${(idcDeviceFlowState.pollIntervalMs / 1000).toFixed(1)}s`,
        'PENDING',
        idcDeviceFlowState.activeFlow
      )
      return
    }

    // EXPIRED
    if (result?.error === 'expired_token') {
      stopIdcPolling()
      updateIdcDialogStatus('error', t('kiro.idcStatusExpired'), 'EXPIRED', idcDeviceFlowState.activeFlow)
      return
    }

    // ACCESS_DENIED（用户拒绝授权）
    if (result?.error === 'access_denied') {
      stopIdcPolling()
      updateIdcDialogStatus('error', t('kiro.idcStatusAccessDenied'), 'ERROR', idcDeviceFlowState.activeFlow)
      return
    }

    // 其他不可重试错误
    const nonRetryableErrors = ['invalid_client', 'invalid_grant', 'unauthorized_client', 'unsupported_grant_type']
    if (result?.error && nonRetryableErrors.includes(result.error)) {
      stopIdcPolling()
      updateIdcDialogStatus(
        'error',
        `${t('kiro.idcStatusError')}: ${result.error}`,
        'ERROR',
        idcDeviceFlowState.activeFlow
      )
      return
    }

    // 其他错误 - 不停止轮询，继续重试
    const errorMsg = result?.error || 'Unknown error'
    console.warn('IDC polling error (will retry):', errorMsg)
    updateIdcDialogStatus(
      'pending',
      `${t('kiro.idcStatusPending')} (${errorMsg})`,
      'PENDING',
      idcDeviceFlowState.activeFlow
    )
  } catch (error) {
    // 网络错误或其他异常 - 不停止轮询，继续重试
    console.warn('IDC polling exception (will retry):', error)
    const errorMsg = error?.message || error || 'Network error'
    updateIdcDialogStatus(
      'pending',
      `${t('kiro.idcStatusPending')} (${errorMsg})`,
      'PENDING',
      idcDeviceFlowState.activeFlow
    )
  } finally {
    idcDeviceFlowState.pollInFlight = false
  }
}

async function tryAddIdcAccountFromAccountManagement(result) {
  const ctx = getIdcAccountAddContext()
  if (!ctx) return

  // 防止重复触发（即使后续 add 失败，也由用户重新发起）
  clearIdcAccountAddContext()

  try {
    const refreshToken = String(result?.refreshToken || '').trim()
    if (!refreshToken) {
      showError(t('kiro.addAccountFailed') + ': empty refreshToken')
      return
    }

    const provider = String(ctx.provider || '').trim() || getIdcProviderForActiveFlow(idcDeviceFlowState.activeFlow)

    const { addKiroAccount } = await import('./kiroAccounts.js')
    await addKiroAccount({
      refreshToken,
      accessToken: result?.accessToken || '',
      region: idcDeviceFlowState.region || 'us-east-1',
      authMethod: 'idc',
      provider,
      clientId: idcDeviceFlowState.clientId,
      clientSecret: idcDeviceFlowState.clientSecret,
    })
  } catch (error) {
    console.error('Failed to add IdC account from account management:', error)
    showError(t('kiro.addAccountFailed') + ': ' + (error?.message || error))
  }
}

// 回填凭证
function fillIdcCredentials(result) {
  if (!result) return

  // 回填 clientId 和 clientSecret
  const clientIdEl = document.getElementById('kiroClientId')
  const clientSecretEl = document.getElementById('kiroClientSecret')
  const refreshTokenEl = document.getElementById('kiroRefreshToken')
  const accessTokenEl = document.getElementById('kiroAccessToken')
  const profileArnEl = document.getElementById('kiroProfileArn')

  if (clientIdEl) clientIdEl.value = idcDeviceFlowState.clientId
  if (clientSecretEl) clientSecretEl.value = idcDeviceFlowState.clientSecret
  if (refreshTokenEl && result.refreshToken) refreshTokenEl.value = result.refreshToken
  if (accessTokenEl && result.accessToken) accessTokenEl.value = result.accessToken
  if (profileArnEl && result.profileArn) profileArnEl.value = result.profileArn

  // 更新 testedKiroCreds 以便保存
  testedRefreshToken = result.refreshToken || ''
  testedKiroCreds = {
    accessToken: result.accessToken || '',
    refreshToken: result.refreshToken || '',
    expiresAt: result.expiresAt || '',
    profileArn: result.profileArn || '',
    region: document.getElementById('kiroRegion')?.value || 'us-east-1',
    authMethod: 'idc',
    provider: getIdcProviderForActiveFlow(idcDeviceFlowState.activeFlow),
    clientId: idcDeviceFlowState.clientId,
    clientSecret: idcDeviceFlowState.clientSecret,
  }

  // 更新按钮状态
  updateKiroConfigButtons()
}

// 开始 IDC Device Flow 登录
export async function startIdcDeviceFlowLogin() {
  idcDeviceFlowState.activeFlow = 'standard'
  const region = document.getElementById('kiroRegion')?.value || 'us-east-1'

  // 重置状态
  stopIdcPolling()
  idcDeviceFlowState.clientId = ''
  idcDeviceFlowState.clientSecret = ''
  idcDeviceFlowState.verifyUrl = ''
  idcDeviceFlowState.userCode = ''
  idcDeviceFlowState.deviceCode = ''
  idcDeviceFlowState.lastPollResult = null
  idcDeviceFlowState.pollIntervalMs = 2000
  idcDeviceFlowState.region = region // 保存 region 用于轮询

  // 关闭其他 Modal（互斥）
  closeIdcOrgLoginDialog({ clearAddContext: false })

  // 显示 dialog
  showIdcDeviceFlowDialog()
  updateIdcDialogStatus('polling', t('kiro.idcStatusRegistering'), 'WORKING', 'standard')

  try {
    // Step 1: 注册 OIDC 客户端
    if (!window.go?.main?.App?.RegisterIdcClient) {
      throw new Error('RegisterIdcClient API not available')
    }

    const registerResult = await window.go.main.App.RegisterIdcClient({ region })
    idcDeviceFlowState.clientId = registerResult.clientId
    idcDeviceFlowState.clientSecret = registerResult.clientSecret

    updateIdcDialogStatus('polling', t('kiro.idcStatusGettingAuth'), 'WORKING', 'standard')

    // Step 2: 获取 Device Authorization
    if (!window.go?.main?.App?.StartDeviceAuthorization) {
      throw new Error('StartDeviceAuthorization API not available')
    }

    const deviceAuthResult = await window.go.main.App.StartDeviceAuthorization({
      clientId: idcDeviceFlowState.clientId,
      clientSecret: idcDeviceFlowState.clientSecret,
      region: region,
    })

    idcDeviceFlowState.verifyUrl = deviceAuthResult.verificationUriComplete || deviceAuthResult.verificationUri || ''
    idcDeviceFlowState.userCode = deviceAuthResult.userCode || ''
    idcDeviceFlowState.deviceCode = deviceAuthResult.deviceCode || ''
    idcDeviceFlowState.pollIntervalMs = Math.max(2000, (deviceAuthResult.interval || 2) * 1000)

    // 更新 dialog 显示
    updateIdcDialogContent('standard')
    updateIdcDialogStatus('pending', t('kiro.idcStatusPending'), 'PENDING', 'standard')

    // Step 3: 开始轮询
    idcDeviceFlowState.pollingEnabled = true
    pollIdcTokenOnce().finally(() => {
      scheduleNextIdcPoll()
    })
  } catch (error) {
    stopIdcPolling()
    updateIdcDialogStatus('error', `${t('kiro.idcStatusError')}: ${error?.message || error}`, 'ERROR', 'standard')
  }
}

// 显示 IDC Device Flow Dialog
function showIdcDeviceFlowDialog() {
  const modal = document.getElementById('idcDeviceFlowModal')
  if (modal) {
    modal.classList.add('active')
  }
}

// 关闭 IDC Device Flow Dialog
export function closeIdcDeviceFlowDialog(options = {}) {
  const clearAddContext = options.clearAddContext !== false
  stopIdcPolling()
  if (clearAddContext) clearIdcAccountAddContext()
  const modal = document.getElementById('idcDeviceFlowModal')
  if (modal) {
    modal.classList.remove('active')
  }
}

// 开始 IDC 组织身份登录
export function startIdcOrgLogin() {
  idcDeviceFlowState.activeFlow = 'org'
  stopIdcPolling()

  // 关闭其他 Modal（互斥）
  closeIdcDeviceFlowDialog({ clearAddContext: false })

  // 重置 UI
  const region = document.getElementById('kiroRegion')?.value || 'us-east-1'
  const orgRegionEl = document.getElementById('idcOrgRegion')
  if (orgRegionEl) orgRegionEl.value = region

  const startUrlEl = document.getElementById('idcOrgStartUrl')
  if (startUrlEl) startUrlEl.value = ''

  const configStep = document.getElementById('idcOrgConfigStep')
  const verifyStep = document.getElementById('idcOrgVerifyStep')
  const backBtn = document.getElementById('idcOrgBackBtn')
  if (configStep) configStep.style.display = 'block'
  if (verifyStep) verifyStep.style.display = 'none'
  if (backBtn) backBtn.style.display = 'none'

  const modal = document.getElementById('idcOrgLoginModal')
  if (modal) modal.classList.add('active')
}

// 关闭 IDC 组织身份登录 Dialog
export function closeIdcOrgLoginDialog(options = {}) {
  const clearAddContext = options.clearAddContext !== false
  stopIdcPolling()
  if (clearAddContext) clearIdcAccountAddContext()
  document.getElementById('idcOrgLoginModal').classList.remove('active')
}

// 提交 IDC 组织身份登录
export async function submitIdcOrgLogin() {
  const startUrl = document.getElementById('idcOrgStartUrl')?.value.trim()
  const region = document.getElementById('idcOrgRegion')?.value

  if (!startUrl) {
    showError(t('kiro.idcOrgStartUrlRequired'))
    return
  }

  // 防抖：禁用 Connect 按钮
  const submitBtn = document.getElementById('idcOrgSubmitBtn')
  if (submitBtn) {
    if (submitBtn.disabled) return // 已经在处理中，直接返回
    submitBtn.disabled = true
  }

  // 切换到 Step 2 UI (loading)
  const configStep = document.getElementById('idcOrgConfigStep')
  const verifyStep = document.getElementById('idcOrgVerifyStep')
  const backBtn = document.getElementById('idcOrgBackBtn')
  if (configStep) configStep.style.display = 'none'
  if (verifyStep) verifyStep.style.display = 'block'
  if (backBtn) backBtn.style.display = 'inline-block'
  updateIdcDialogStatus('polling', t('kiro.idcStatusRegistering'), 'WORKING', 'org')

  try {
    // 重置状态
    idcDeviceFlowState.clientId = ''
    idcDeviceFlowState.clientSecret = ''
    idcDeviceFlowState.verifyUrl = ''
    idcDeviceFlowState.userCode = ''
    idcDeviceFlowState.deviceCode = ''
    idcDeviceFlowState.lastPollResult = null
    idcDeviceFlowState.pollIntervalMs = 2000
    idcDeviceFlowState.region = region // 保存 region 用于轮询

    // Step 1: 注册 OIDC 客户端
    if (!window.go?.main?.App?.RegisterIdcClient) {
      throw new Error('RegisterIdcClient API not available')
    }

    const registerResult = await window.go.main.App.RegisterIdcClient({ region })
    idcDeviceFlowState.clientId = registerResult.clientId
    idcDeviceFlowState.clientSecret = registerResult.clientSecret

    updateIdcDialogStatus('polling', t('kiro.idcStatusGettingAuth'), 'WORKING', 'org')

    // Step 2: 获取 Device Authorization (传入自定义 Start URL)
    if (!window.go?.main?.App?.StartDeviceAuthorization) {
      throw new Error('StartDeviceAuthorization API not available')
    }

    const deviceAuthResult = await window.go.main.App.StartDeviceAuthorization({
      clientId: idcDeviceFlowState.clientId,
      clientSecret: idcDeviceFlowState.clientSecret,
      region: region,
      startUrl: startUrl,
    })

    idcDeviceFlowState.verifyUrl = deviceAuthResult.verificationUriComplete || deviceAuthResult.verificationUri || ''
    idcDeviceFlowState.userCode = deviceAuthResult.userCode || ''
    idcDeviceFlowState.deviceCode = deviceAuthResult.deviceCode || ''
    idcDeviceFlowState.pollIntervalMs = Math.max(2000, (deviceAuthResult.interval || 2) * 1000)

    // 更新 dialog 显示
    updateIdcDialogContent('org')
    updateIdcDialogStatus('pending', t('kiro.idcStatusPending'), 'PENDING', 'org')

    // Step 3: 开始轮询
    idcDeviceFlowState.pollingEnabled = true
    pollIdcTokenOnce().finally(() => {
      scheduleNextIdcPoll()
    })
  } catch (error) {
    stopIdcPolling()
    updateIdcDialogStatus('error', `${t('kiro.idcStatusError')}: ${error?.message || error}`, 'ERROR', 'org')

    // 错误恢复：返回 Step 1 并重新启用按钮
    const configStep = document.getElementById('idcOrgConfigStep')
    const verifyStep = document.getElementById('idcOrgVerifyStep')
    const submitBtn = document.getElementById('idcOrgSubmitBtn')
    if (configStep) configStep.style.display = 'block'
    if (verifyStep) verifyStep.style.display = 'none'
    if (submitBtn) submitBtn.disabled = false
  }
}

// 返回组织登录 Step 1（用于错误恢复）
export function backToOrgLoginStep1() {
  stopIdcPolling()
  const configStep = document.getElementById('idcOrgConfigStep')
  const verifyStep = document.getElementById('idcOrgVerifyStep')
  const submitBtn = document.getElementById('idcOrgSubmitBtn')
  const backBtn = document.getElementById('idcOrgBackBtn')
  if (configStep) configStep.style.display = 'block'
  if (verifyStep) verifyStep.style.display = 'none'
  if (submitBtn) submitBtn.disabled = false
  if (backBtn) backBtn.style.display = 'none'
}

// 更新 dialog 内容
function updateIdcDialogContent(flow = 'standard') {
  const isOrg = flow === 'org'
  const verifyUrlEl = document.getElementById(isOrg ? 'idcOrgVerifyUrl' : 'idcVerifyUrl')
  const copyLinkBtn = document.getElementById(isOrg ? 'idcOrgCopyLinkBtn' : 'idcCopyLinkBtn')
  const openLinkBtn = document.getElementById(isOrg ? 'idcOrgOpenLinkBtn' : 'idcOpenLinkBtn')

  if (verifyUrlEl) {
    verifyUrlEl.textContent = idcDeviceFlowState.verifyUrl || '—'
    verifyUrlEl.title = idcDeviceFlowState.verifyUrl || ''
  }
  if (copyLinkBtn) copyLinkBtn.disabled = !idcDeviceFlowState.verifyUrl
  if (openLinkBtn) openLinkBtn.disabled = !idcDeviceFlowState.verifyUrl
}

// 更新 dialog 状态
function updateIdcDialogStatus(kind, text, label, flow = 'standard') {
  const isOrg = flow === 'org'
  const statusTextEl = document.getElementById(isOrg ? 'idcOrgStatusText' : 'idcStatusText')
  const statusLabelEl = document.getElementById(isOrg ? 'idcOrgStatusLabel' : 'idcStatusLabel')
  const statusBoxEl = document.getElementById(isOrg ? 'idcOrgStatusBox' : 'idcStatusBox')
  const statusDotEl = document.getElementById(isOrg ? 'idcOrgStatusDot' : 'idcStatusDot')

  if (statusTextEl) statusTextEl.textContent = text
  if (statusLabelEl) statusLabelEl.textContent = label

  if (statusBoxEl && statusDotEl) {
    statusBoxEl.classList.remove('ok', 'bad', 'warn')
    statusDotEl.classList.remove('ping')
    statusDotEl.style.background = 'rgba(255,255,255,.55)'

    if (kind === 'pending' || kind === 'polling') {
      statusBoxEl.classList.add('warn')
      statusDotEl.classList.add('ping')
      statusDotEl.style.background = 'rgba(245,158,11,.9)'
    } else if (kind === 'ok') {
      statusBoxEl.classList.add('ok')
      statusDotEl.style.background = 'rgba(16,185,129,.95)'
    } else if (kind === 'error') {
      statusBoxEl.classList.add('bad')
      statusDotEl.style.background = 'rgba(239,68,68,.95)'
    }
  }
}

// 复制授权链接
export async function copyIdcVerifyUrl() {
  if (!idcDeviceFlowState.verifyUrl) return
  try {
    await navigator.clipboard.writeText(idcDeviceFlowState.verifyUrl)
    updateIdcDialogStatus('pending', t('kiro.idcLinkCopied'), 'PENDING', idcDeviceFlowState.activeFlow)
  } catch (error) {
    showError(t('kiro.idcCopyFailed') + (error?.message || error))
  }
}

// 在浏览器中打开授权链接
export async function openIdcVerifyUrl() {
  if (!idcDeviceFlowState.verifyUrl) return

  try {
    // 尝试使用后端方法以无痕模式打开
    if (window.go?.main?.App?.OpenURLInIncognito) {
      await window.go.main.App.OpenURLInIncognito(idcDeviceFlowState.verifyUrl)
      updateIdcDialogStatus('pending', t('kiro.idcLinkOpened'), 'PENDING', idcDeviceFlowState.activeFlow)
    } else {
      // 降级方案：使用普通方式打开
      window.open(idcDeviceFlowState.verifyUrl, '_blank', 'noopener,noreferrer')
      updateIdcDialogStatus('pending', t('kiro.idcLinkOpened'), 'PENDING', idcDeviceFlowState.activeFlow)
    }
  } catch (error) {
    console.warn('Failed to open in incognito mode, falling back to normal mode:', error)
    // 降级方案：使用普通方式打开
    window.open(idcDeviceFlowState.verifyUrl, '_blank', 'noopener,noreferrer')
    updateIdcDialogStatus('pending', t('kiro.idcLinkOpened'), 'PENDING', idcDeviceFlowState.activeFlow)
  }
}

// =============================================================================
// Social 登录功能 (Google/GitHub OAuth)
// =============================================================================


/**
 * 确保存在 kiro 端点
 * 参考后端 internal/proxy/kiro_handler.go:ensureKiroEndpoint 的实现
 */
async function ensureKiroEndpoint() {
  try {
    if (!window.go?.main?.App?.GetAllEndpoints || !window.go?.main?.App?.SaveEndpointData) {
      console.warn('GetAllEndpoints or SaveEndpointData API not available')
      return
    }

    // 获取所有端点
    const allEndpoints = await window.go.main.App.GetAllEndpoints()

    // 检查是否已存在 kiro/claude 端点
    const hasKiroEndpoint = allEndpoints.some((ep) => ep.transformer === 'kiro/claude' && ep.interfaceType === 'claude')

    if (hasKiroEndpoint) {
      // 已存在，无需创建
      return
    }

    // 不存在，创建新端点
    const claudeEndpoints = allEndpoints.filter((ep) => ep.interfaceType === 'claude')
    const isOnlyClaudeEndpoint = claudeEndpoints.length === 0

    const newEndpoint = {
      id: 0, // 新端点，ID 为 0
      name: 'kiro',
      apiUrl: 'https://q.us-east-1.amazonaws.com',
      apiKey: '-',
      active: isOnlyClaudeEndpoint, // 如果是唯一的 claude 端点，自动设为 active
      enabled: true,
      interfaceType: 'claude',
      transformer: 'kiro/claude',
      transformerSet: true,
      priority: 8,
      remark: 'Auto-created Kiro endpoint',
    }

    // 保存端点
    await window.go.main.App.SaveEndpointData(newEndpoint)
    console.log('Kiro endpoint created successfully')
  } catch (error) {
    // 记录错误但不影响主流程
    console.error('Failed to ensure kiro endpoint:', error)
  }
}

// =============================================================================
// Kiro Sign Login（统一登录页 app.kiro.dev/signin）
// =============================================================================

const kiroSignState = {
  verifier: '',
  state: '',
  loginUrl: '',
  isWaiting: false,
  fromAccountManagement: false,
}

function loginOptionToProvider(loginOption) {
  const opt = String(loginOption || '').toLowerCase()
  if (opt === 'google') return { authMethod: 'social', provider: 'Google' }
  if (opt === 'github') return { authMethod: 'social', provider: 'Github' }
  return { authMethod: 'social', provider: loginOption || '' }
}

async function startKiroSignLogin(fromAccountManagement = false) {
  try {
    // 生成 PKCE 参数
    const verifier = generateRandomString(128)
    const challenge = await generateCodeChallenge(verifier)
    const state = generateUUID()

    const redirectUri = 'http://localhost:3128'
    const loginUrl =
      `https://app.kiro.dev/signin?` +
      `state=${state}&` +
      `code_challenge=${challenge}&` +
      `code_challenge_method=S256&` +
      `redirect_uri=${encodeURIComponent(redirectUri)}&` +
      `redirect_from=KiroIDE`

    // 保存状态
    kiroSignState.verifier = verifier
    kiroSignState.state = state
    kiroSignState.loginUrl = loginUrl
    kiroSignState.isWaiting = true
    kiroSignState.fromAccountManagement = fromAccountManagement

    // 启动回调服务器
    if (!window.go?.main?.App?.StartKiroSignCallbackServer) {
      throw new Error('StartKiroSignCallbackServer API not available')
    }
    await window.go.main.App.StartKiroSignCallbackServer()

    // 显示等待 Modal
    showKiroSignLoginModal()

    // 监听回调事件
    if (window.runtime?.EventsOn) {
      window.runtime.EventsOn('kiro-sign-callback', handleKiroSignCallback)
      window.runtime.EventsOn('kiro-sign-timeout', handleKiroSignTimeout)
    }

    // 打开浏览器
    try {
      if (window.go?.main?.App?.OpenURLInIncognito) {
        await window.go.main.App.OpenURLInIncognito(loginUrl)
      } else {
        window.open(loginUrl, '_blank', 'noopener,noreferrer')
      }
    } catch (browserError) {
      console.warn('Failed to open browser:', browserError)
      window.open(loginUrl, '_blank', 'noopener,noreferrer')
    }
  } catch (error) {
    console.error('Failed to start Kiro Sign login:', error)
    const msg = String(error?.message || error || '')
    if (msg.includes('3128')) {
      showError(t('kiro.kiroSignLoginPortInUse'))
    } else {
      showError(t('kiro.kiroSignLoginFailed') + ': ' + msg)
    }
    closeKiroSignLoginModal()
  }
}

async function handleKiroSignCallback(data) {
  if (!kiroSignState.isWaiting) return

  try {
    // 验证 state
    if (data.state !== kiroSignState.state) {
      throw new Error('State mismatch')
    }

    if (data.type === 'social') {
      // Social 登录：交换 token
      // redirect_uri 必须与实际回调 URL 完全一致（含路径和查询参数）
      const redirectUri = `http://localhost:3128/oauth/callback?login_option=${encodeURIComponent(data.loginOption)}`
      const tokenResponse = await exchangeCodeForToken(data.code, kiroSignState.verifier, redirectUri)

      const { authMethod, provider } = loginOptionToProvider(data.loginOption)

      if (kiroSignState.fromAccountManagement) {
        const { addKiroAccount } = await import('./kiroAccounts.js')
        await addKiroAccount({
          refreshToken: tokenResponse.refreshToken || '',
          accessToken: tokenResponse.accessToken || '',
          profileArn: tokenResponse.profileArn || '',
          expiresAt: tokenResponse.expiresAt || '',
          region: 'us-east-1',
          authMethod,
          provider,
        })
      } else {
        // 回填到配置表单
        const refreshTokenEl = document.getElementById('kiroRefreshToken')
        const accessTokenEl = document.getElementById('kiroAccessToken')
        const profileArnEl = document.getElementById('kiroProfileArn')
        if (refreshTokenEl && tokenResponse.refreshToken) refreshTokenEl.value = tokenResponse.refreshToken
        if (accessTokenEl && tokenResponse.accessToken) accessTokenEl.value = tokenResponse.accessToken
        if (profileArnEl && tokenResponse.profileArn) profileArnEl.value = tokenResponse.profileArn

        testedRefreshToken = tokenResponse.refreshToken || ''
        testedKiroCreds = {
          accessToken: tokenResponse.accessToken || '',
          refreshToken: tokenResponse.refreshToken || '',
          expiresAt: tokenResponse.expiresAt || '',
          profileArn: tokenResponse.profileArn || '',
          region: document.getElementById('kiroRegion')?.value || 'us-east-1',
          authMethod,
          provider,
          clientId: '',
          clientSecret: '',
        }
        updateKiroConfigButtons()
      }

      closeKiroSignLoginModal()
      showSuccess(t('kiro.kiroSignLoginSuccess'))
    } else if (data.type === 'idc') {
      // IDC 登录：Authorization Code Flow with PKCE
      closeKiroSignLoginModal()
      await handleKiroSignIdcAuthCodeFlow(data)
    }
  } catch (error) {
    console.error('Kiro Sign callback error:', error)
    showError(t('kiro.kiroSignLoginFailed') + ': ' + (error?.message || error))
    closeKiroSignLoginModal()
  } finally {
    resetKiroSignState()
  }
}

// IDC Authorization Code Flow with PKCE（Builder ID / IAM Identity Center）
async function handleKiroSignIdcAuthCodeFlow(data) {
  const region = data.idcRegion || 'us-east-1'
  const issuerUrl = data.issuerUrl || 'https://view.awsapps.com/start'
  const provider = data.loginOption === 'builderid' ? 'BuilderId' : 'Enterprise'
  const fromAccountManagement = kiroSignState.fromAccountManagement

  try {
    // 1. 注册 Auth Code 客户端
    const regResp = await window.go.main.App.RegisterIdcAuthCodeClient({
      region,
      issuerUrl,
    })

    // 2. 生成 PKCE 参数
    const verifier = generateRandomString(128)
    const challenge = await generateCodeChallenge(verifier)
    const state = generateUUID()

    // 3. 启动随机端口回调服务器
    const port = await window.go.main.App.StartKiroSignIdcCallbackServer()

    const redirectUri = `http://127.0.0.1:${port}/oauth/callback`
    const scopes = [
      'codewhisperer:completions',
      'codewhisperer:analysis',
      'codewhisperer:conversations',
      'codewhisperer:transformations',
      'codewhisperer:taskassist',
    ].join(',')

    const authorizeUrl =
      `https://oidc.${region}.amazonaws.com/authorize?` +
      `response_type=code&` +
      `client_id=${encodeURIComponent(regResp.clientId)}&` +
      `redirect_uri=${encodeURIComponent(redirectUri)}&` +
      `scopes=${encodeURIComponent(scopes)}&` +
      `state=${state}&` +
      `code_challenge=${challenge}&` +
      `code_challenge_method=S256`

    // 4. 等待回调的 Promise
    const codeResult = await new Promise((resolve, reject) => {
      const cleanup = () => {
        if (window.runtime?.EventsOff) {
          window.runtime.EventsOff('kiro-sign-idc-callback')
          window.runtime.EventsOff('kiro-sign-idc-timeout')
        }
      }

      if (window.runtime?.EventsOn) {
        window.runtime.EventsOn('kiro-sign-idc-callback', (cbData) => {
          cleanup()
          if (cbData.state !== state) {
            reject(new Error('IDC auth code state mismatch'))
          } else {
            resolve(cbData)
          }
        })
        window.runtime.EventsOn('kiro-sign-idc-timeout', () => {
          cleanup()
          reject(new Error(t('kiro.kiroSignLoginTimeout')))
        })
      }

      // 5. 打开浏览器
      if (window.go?.main?.App?.OpenURLInIncognito) {
        window.go.main.App.OpenURLInIncognito(authorizeUrl).catch(() => {
          window.open(authorizeUrl, '_blank', 'noopener,noreferrer')
        })
      } else {
        window.open(authorizeUrl, '_blank', 'noopener,noreferrer')
      }
    })

    // 6. 用 authorization code 交换 token
    const tokenResp = await window.go.main.App.ExchangeIdcAuthCode({
      region,
      clientId: regResp.clientId,
      clientSecret: regResp.clientSecret,
      code: codeResult.code,
      redirectUri: `http://127.0.0.1:${port}/oauth/callback`,
      codeVerifier: verifier,
    })

    // 7. 处理结果
    if (fromAccountManagement) {
      const { addKiroAccount } = await import('./kiroAccounts.js')
      await addKiroAccount({
        refreshToken: tokenResp.refreshToken || '',
        accessToken: tokenResp.accessToken || '',
        region,
        authMethod: 'idc',
        provider,
        clientId: regResp.clientId,
        clientSecret: regResp.clientSecret,
      })
    } else {
      const refreshTokenEl = document.getElementById('kiroRefreshToken')
      const accessTokenEl = document.getElementById('kiroAccessToken')
      if (refreshTokenEl && tokenResp.refreshToken) refreshTokenEl.value = tokenResp.refreshToken
      if (accessTokenEl && tokenResp.accessToken) accessTokenEl.value = tokenResp.accessToken

      const clientIdEl = document.getElementById('kiroClientId')
      const clientSecretEl = document.getElementById('kiroClientSecret')
      if (clientIdEl) clientIdEl.value = regResp.clientId
      if (clientSecretEl) clientSecretEl.value = regResp.clientSecret

      testedRefreshToken = tokenResp.refreshToken || ''
      testedKiroCreds = {
        accessToken: tokenResp.accessToken || '',
        refreshToken: tokenResp.refreshToken || '',
        expiresAt: '',
        profileArn: '',
        region,
        authMethod: 'idc',
        provider,
        clientId: regResp.clientId,
        clientSecret: regResp.clientSecret,
      }
      updateKiroConfigButtons()
    }

    showSuccess(t('kiro.kiroSignLoginSuccess'))
  } catch (error) {
    console.error('IDC auth code flow error:', error)
    showError(t('kiro.kiroSignLoginFailed') + ': ' + (error?.message || error))
    if (window.go?.main?.App?.StopKiroSignIdcCallbackServer) {
      window.go.main.App.StopKiroSignIdcCallbackServer()
    }
  }
}

function handleKiroSignTimeout() {
  if (!kiroSignState.isWaiting) return
  showError(t('kiro.kiroSignLoginTimeout'))
  closeKiroSignLoginModal()
  resetKiroSignState()
}

function showKiroSignLoginModal() {
  const modal = document.getElementById('kiroSignLoginModal')
  if (modal) {
    const urlEl = document.getElementById('kiroSignLoginUrl')
    if (urlEl) {
      urlEl.textContent = kiroSignState.loginUrl
      urlEl.title = kiroSignState.loginUrl
    }
    modal.classList.add('active')
  }
}

function closeKiroSignLoginModal() {
  const modal = document.getElementById('kiroSignLoginModal')
  if (modal) {
    modal.classList.remove('active')
  }
  // 停止回调服务器
  if (window.go?.main?.App?.StopKiroSignCallbackServer) {
    window.go.main.App.StopKiroSignCallbackServer().catch((err) => {
      console.warn('Failed to stop Kiro Sign callback server:', err)
    })
  }
}

function resetKiroSignState() {
  kiroSignState.verifier = ''
  kiroSignState.state = ''
  kiroSignState.loginUrl = ''
  kiroSignState.isWaiting = false
  kiroSignState.fromAccountManagement = false

  if (window.runtime?.EventsOff) {
    window.runtime.EventsOff('kiro-sign-callback')
    window.runtime.EventsOff('kiro-sign-timeout')
  }
}

function copyKiroSignLoginUrl() {
  navigator.clipboard
    .writeText(kiroSignState.loginUrl)
    .then(() => showSuccess(t('kiro.linkCopied')))
    .catch((err) => showError(t('kiro.copyFailed') + ': ' + err))
}

async function openKiroSignLoginUrl() {
  try {
    if (!kiroSignState.loginUrl) {
      console.warn('No login URL available')
      return
    }
    if (window.go?.main?.App?.OpenURLInIncognito) {
      await window.go.main.App.OpenURLInIncognito(kiroSignState.loginUrl)
    } else {
      window.open(kiroSignState.loginUrl, '_blank', 'noopener,noreferrer')
    }
  } catch (error) {
    console.error('Failed to open login URL:', error)
    // Fallback to window.open
    window.open(kiroSignState.loginUrl, '_blank', 'noopener,noreferrer')
  }
}

// 注册到 window
window.startKiroSignLogin = startKiroSignLogin
window.closeKiroSignLoginModal = function () {
  closeKiroSignLoginModal()
  resetKiroSignState()
  if (window.go?.main?.App?.StopKiroSignCallbackServer) {
    window.go.main.App.StopKiroSignCallbackServer()
  }
}
window.copyKiroSignLoginUrl = copyKiroSignLoginUrl
window.openKiroSignLoginUrl = openKiroSignLoginUrl

// =============================================================================
// Kiro Global Config Modal (model mapping + global settings)
// =============================================================================

let kiroGlobalConfigDefaults = null

function renderKiroModelMappingList(mapping) {
  const container = document.getElementById('kiroModelMappingList')
  if (!container) return
  container.replaceChildren()
  if (!mapping || typeof mapping !== 'object') return

  Object.entries(mapping).forEach(([alias, name]) => {
    container.appendChild(createModelMappingRow(alias, name))
  })
}

function createModelMappingRow(alias, name) {
  const row = document.createElement('div')
  row.className = 'model-mapping-row'
  row.style.cssText = 'display:flex;gap:6px;align-items:center;margin-bottom:4px;'

  const aliasInput = document.createElement('input')
  aliasInput.type = 'text'
  aliasInput.className = 'kiro-mm-alias'
  aliasInput.placeholder = t('kiro.mappingAlias')
  aliasInput.value = String(alias || '')
  aliasInput.style.cssText = 'flex:1;font-size:12px;'

  const arrow = document.createElement('span')
  arrow.style.cssText = 'color:var(--text-secondary);font-size:12px;'
  arrow.textContent = '\u2192'

  const nameInput = document.createElement('input')
  nameInput.type = 'text'
  nameInput.className = 'kiro-mm-name'
  nameInput.placeholder = t('kiro.mappingName')
  nameInput.value = String(name || '')
  nameInput.style.cssText = 'flex:1;font-size:12px;'

  const removeBtn = document.createElement('button')
  removeBtn.type = 'button'
  removeBtn.className = 'btn btn-sm btn-danger'
  removeBtn.style.cssText = 'padding:2px 6px;font-size:11px;'
  removeBtn.textContent = '\u00d7'
  removeBtn.addEventListener('click', () => row.remove())

  row.appendChild(aliasInput)
  row.appendChild(arrow)
  row.appendChild(nameInput)
  row.appendChild(removeBtn)
  return row
}

function collectModelMapping() {
  const container = document.getElementById('kiroModelMappingList')
  if (!container) return {}
  const rows = container.querySelectorAll('.model-mapping-row')
  const mapping = {}
  rows.forEach((row) => {
    const alias = (row.querySelector('.kiro-mm-alias')?.value || '').trim()
    const name = (row.querySelector('.kiro-mm-name')?.value || '').trim()
    if (alias && name) {
      mapping[alias] = name
    }
  })
  return mapping
}

export async function showKiroGlobalConfigModal() {
  try {
    if (!window.go?.main?.App?.GetKiroGlobalConfig) return
    const cfg = await window.go.main.App.GetKiroGlobalConfig()
    kiroGlobalConfigDefaults = cfg?.modelMapping || {}

    document.getElementById('kiroGlobalProxyUrl').value = cfg?.proxyUrl || ''
    document.getElementById('kiroGlobalUserAgent').value = cfg?.userAgent || ''
    document.getElementById('kiroGlobalVersion').value = cfg?.version || ''
    document.getElementById('kiroGlobalBufferedStream').checked = cfg?.bufferedStream || false
    document.getElementById('kiroRotationMode').value = cfg?.rotationMode || 'fixed'
    document.getElementById('kiroGlobalRegion').value = cfg?.region || 'us-east-1'

    // Sync rotation mode dropdown display
    syncKiroRotationModeDisplay()

    renderKiroModelMappingList(cfg?.modelMapping || {})
  } catch (error) {
    console.error('Failed to load Kiro global config:', error)
    showError(t('kiro.globalConfigSaveFailed') + (error?.message || error))
    return
  }
  document.getElementById('kiroGlobalConfigModal').classList.add('active')
}

export function closeKiroGlobalConfigModal() {
  document.getElementById('kiroGlobalConfigModal').classList.remove('active')
}

export function addKiroModelMappingRow() {
  const container = document.getElementById('kiroModelMappingList')
  if (!container) return
  const row = createModelMappingRow('', '')
  container.appendChild(row)
  row.querySelector('.kiro-mm-alias')?.focus()
}

export function resetKiroModelMappingDefaults() {
  renderKiroModelMappingList(kiroGlobalConfigDefaults)
}

export async function saveKiroGlobalConfig() {
  try {
    if (!window.go?.main?.App?.SaveKiroGlobalConfig) return

    const dto = {
      region: (document.getElementById('kiroGlobalRegion')?.value || 'us-east-1').trim(),
      proxyUrl: (document.getElementById('kiroGlobalProxyUrl')?.value || '').trim(),
      userAgent: (document.getElementById('kiroGlobalUserAgent')?.value || '').trim(),
      version: (document.getElementById('kiroGlobalVersion')?.value || '').trim(),
      bufferedStream: document.getElementById('kiroGlobalBufferedStream')?.checked || false,
      rotationMode: (document.getElementById('kiroRotationMode')?.value || 'fixed').trim(),
      modelMapping: collectModelMapping(),
    }
    await window.go.main.App.SaveKiroGlobalConfig(dto)
    closeKiroGlobalConfigModal()
    showSuccess(t('kiro.globalConfigSaved'))
  } catch (error) {
    console.error('SaveKiroGlobalConfig error:', error)
    showError(t('kiro.globalConfigSaveFailed') + (error?.message || error))
  }
}
