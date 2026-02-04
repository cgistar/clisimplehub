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

// Social 登录状态
const socialLoginState = {
  provider: '', // 'google' 或 'github'
  verifier: '', // PKCE code_verifier
  state: '', // CSRF 防护 state
  loginUrl: '', // OAuth URL
  isWaiting: false, // 是否正在等待回调
  timeoutTimer: null, // 超时定时器
  originalKiroHandlerBundleId: '', // 登录开始前的 kiro:// 默认 handler（macOS）
  didOverrideKiroHandler: false, // 本次登录是否临时抢占了 handler
}

async function isDarwinPlatform() {
  if (!window.runtime?.Environment) return false
  try {
    const env = await window.runtime.Environment()
    return env?.platform === 'darwin'
  } catch (e) {
    console.warn('Failed to detect platform via runtime.Environment:', e)
    return false
  }
}

async function restoreKiroProtocolHandlerIfNeeded() {
  if (!socialLoginState.didOverrideKiroHandler) return
  if (!socialLoginState.originalKiroHandlerBundleId) return
  if (!window.go?.main?.App?.RestoreKiroProtocolHandler) return

  try {
    await window.go.main.App.RestoreKiroProtocolHandler(socialLoginState.originalKiroHandlerBundleId)
  } catch (e) {
    console.warn('RestoreKiroProtocolHandler failed:', e)
  }
}

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
  updateSocialLoginButtonsVisibility()
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

function updateSocialLoginButtonsVisibility() {
  const authMethod = (document.getElementById('kiroAuthMethod')?.value || 'social').trim().toLowerCase()
  const socialButtons = document.getElementById('kiroSocialLoginButtons')

  if (authMethod === 'social') {
    if (socialButtons) socialButtons.style.display = ''
  } else {
    if (socialButtons) socialButtons.style.display = 'none'
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
      await window.go.main.App.SaveKiroConfig({
        refreshToken: testedKiroCreds?.refreshToken || refreshToken,
        region: testedKiroCreds?.region || region,
        proxyUrl,
        userAgent,
        version,
        bufferedStream,
        authMethod,
        clientId,
        clientSecret,
        accessToken: shouldPersistAuthState ? testedKiroCreds?.accessToken || '' : '',
        expiresAt: shouldPersistAuthState ? testedKiroCreds?.expiresAt || '' : '',
        profileArn: shouldPersistAuthState ? testedKiroCreds?.profileArn || '' : '',
      })
    }

    closeKiroConfigModal()
    showSuccess(t('kiro.saveSuccess'))
    clearTransformersCache()
  } catch (error) {
    console.error('SaveKiroConfig error:', error)
    showError(t('kiro.saveFailedPrefix') + (error?.message || error || 'Unknown error'))
  }
}

// Sync region dropdown display
function syncKiroRegionDisplay() {
  const select = document.getElementById('kiroRegion')
  const display = document.getElementById('kiroRegionDisplay')
  if (!select || !display) return

  const selectedOption = select.options[select.selectedIndex]
  if (selectedOption) {
    display.value = selectedOption.textContent.trim()
  }

  // Render dropdown options
  renderKiroRegionDropdown()
}

// Render region dropdown options
function renderKiroRegionDropdown() {
  const select = document.getElementById('kiroRegion')
  const dropdown = document.getElementById('kiroRegionDropdown')
  if (!select || !dropdown) return

  dropdown.innerHTML = ''
  Array.from(select.options).forEach((option) => {
    const item = document.createElement('div')
    item.className = 'model-dropdown-item'
    if (option.selected) {
      item.classList.add('selected')
    }
    item.textContent = option.textContent.trim()
    item.onclick = () => selectKiroRegion(option.value)
    dropdown.appendChild(item)
  })
}

// Select region from dropdown
function selectKiroRegion(value) {
  const select = document.getElementById('kiroRegion')
  if (!select) return

  select.value = value
  syncKiroRegionDisplay()

  // Close dropdown
  const dropdown = document.getElementById('kiroRegionDropdown')
  if (dropdown) {
    dropdown.classList.remove('show')
  }
}

// Toggle region dropdown
export function toggleKiroRegionDropdown() {
  const dropdown = document.getElementById('kiroRegionDropdown')
  const select = document.getElementById('kiroRegion')
  if (!dropdown || !select) return

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
    const region = document.getElementById('kiroRegion')?.value || 'us-east-1'

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
      updateIdcDialogStatus('ok', t('kiro.idcStatusSuccess'), 'SUCCESS')

      // 回填凭证到 Kiro Config Modal
      fillIdcCredentials(result)

      // 延迟关闭 dialog
      setTimeout(() => {
        closeIdcDeviceFlowDialog()
        showSuccess(t('kiro.idcLoginSuccess'))
      }, 1000)
      return
    }

    // PENDING 状态
    if (result?.error === 'authorization_pending') {
      updateIdcDialogStatus('pending', t('kiro.idcStatusPending'), 'PENDING')
      return
    }

    // SLOW_DOWN
    if (result?.error === 'slow_down') {
      idcDeviceFlowState.pollIntervalMs = Math.min(idcDeviceFlowState.pollIntervalMs + 2000, 10000)
      updateIdcDialogStatus(
        'pending',
        `${t('kiro.idcStatusSlowDown')} ${(idcDeviceFlowState.pollIntervalMs / 1000).toFixed(1)}s`,
        'PENDING',
      )
      return
    }

    // EXPIRED
    if (result?.error === 'expired_token') {
      stopIdcPolling()
      updateIdcDialogStatus('error', t('kiro.idcStatusExpired'), 'EXPIRED')
      return
    }

    // 其他错误 - 不停止轮询，继续重试
    const errorMsg = result?.error || 'Unknown error'
    console.warn('IDC polling error (will retry):', errorMsg)
    updateIdcDialogStatus('pending', `${t('kiro.idcStatusPending')} (${errorMsg})`, 'PENDING')
  } catch (error) {
    // 网络错误或其他异常 - 不停止轮询，继续重试
    console.warn('IDC polling exception (will retry):', error)
    const errorMsg = error?.message || error || 'Network error'
    updateIdcDialogStatus('pending', `${t('kiro.idcStatusPending')} (${errorMsg})`, 'PENDING')
  } finally {
    idcDeviceFlowState.pollInFlight = false
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

  if (clientIdEl) clientIdEl.value = idcDeviceFlowState.clientId
  if (clientSecretEl) clientSecretEl.value = idcDeviceFlowState.clientSecret
  if (refreshTokenEl && result.refreshToken) refreshTokenEl.value = result.refreshToken
  if (accessTokenEl && result.accessToken) accessTokenEl.value = result.accessToken

  // 更新 testedKiroCreds 以便保存
  testedRefreshToken = result.refreshToken || ''
  testedKiroCreds = {
    accessToken: result.accessToken || '',
    refreshToken: result.refreshToken || '',
    expiresAt: result.expiresAt || '',
    profileArn: result.profileArn || '',
    region: document.getElementById('kiroRegion')?.value || 'us-east-1',
  }

  // 更新按钮状态
  updateKiroConfigButtons()
}

// 开始 IDC Device Flow 登录
export async function startIdcDeviceFlowLogin() {
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

  // 显示 dialog
  showIdcDeviceFlowDialog()
  updateIdcDialogStatus('polling', t('kiro.idcStatusRegistering'), 'WORKING')

  try {
    // Step 1: 注册 OIDC 客户端
    if (!window.go?.main?.App?.RegisterIdcClient) {
      throw new Error('RegisterIdcClient API not available')
    }

    const registerResult = await window.go.main.App.RegisterIdcClient({ region })
    idcDeviceFlowState.clientId = registerResult.clientId
    idcDeviceFlowState.clientSecret = registerResult.clientSecret

    updateIdcDialogStatus('polling', t('kiro.idcStatusGettingAuth'), 'WORKING')

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
    updateIdcDialogContent()
    updateIdcDialogStatus('pending', t('kiro.idcStatusPending'), 'PENDING')

    // Step 3: 开始轮询
    idcDeviceFlowState.pollingEnabled = true
    pollIdcTokenOnce().finally(() => {
      scheduleNextIdcPoll()
    })
  } catch (error) {
    stopIdcPolling()
    updateIdcDialogStatus('error', `${t('kiro.idcStatusError')}: ${error?.message || error}`, 'ERROR')
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
export function closeIdcDeviceFlowDialog() {
  stopIdcPolling()
  const modal = document.getElementById('idcDeviceFlowModal')
  if (modal) {
    modal.classList.remove('active')
  }
}

// 更新 dialog 内容
function updateIdcDialogContent() {
  const verifyUrlEl = document.getElementById('idcVerifyUrl')
  const copyLinkBtn = document.getElementById('idcCopyLinkBtn')
  const openLinkBtn = document.getElementById('idcOpenLinkBtn')

  if (verifyUrlEl) {
    verifyUrlEl.textContent = idcDeviceFlowState.verifyUrl || '—'
    verifyUrlEl.title = idcDeviceFlowState.verifyUrl || ''
  }
  if (copyLinkBtn) copyLinkBtn.disabled = !idcDeviceFlowState.verifyUrl
  if (openLinkBtn) openLinkBtn.disabled = !idcDeviceFlowState.verifyUrl
}

// 更新 dialog 状态
function updateIdcDialogStatus(kind, text, label) {
  const statusTextEl = document.getElementById('idcStatusText')
  const statusLabelEl = document.getElementById('idcStatusLabel')
  const statusBoxEl = document.getElementById('idcStatusBox')
  const statusDotEl = document.getElementById('idcStatusDot')

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
    updateIdcDialogStatus('pending', t('kiro.idcLinkCopied'), 'PENDING')
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
      updateIdcDialogStatus('pending', t('kiro.idcLinkOpened'), 'PENDING')
    } else {
      // 降级方案：使用普通方式打开
      window.open(idcDeviceFlowState.verifyUrl, '_blank', 'noopener,noreferrer')
      updateIdcDialogStatus('pending', t('kiro.idcLinkOpened'), 'PENDING')
    }
  } catch (error) {
    console.warn('Failed to open in incognito mode, falling back to normal mode:', error)
    // 降级方案：使用普通方式打开
    window.open(idcDeviceFlowState.verifyUrl, '_blank', 'noopener,noreferrer')
    updateIdcDialogStatus('pending', t('kiro.idcLinkOpened'), 'PENDING')
  }
}

// =============================================================================
// Social 登录功能 (Google/GitHub OAuth)
// =============================================================================

/**
 * 开始 Social 登录流程
 * @param {string} provider - 'google' 或 'github'
 */
export async function startSocialLogin(provider) {
  try {
    // 0. 清理旧的监听器（防止重复注册）
    resetSocialLoginState()

    // 0.1 确保本应用能接收到 kiro:// 回调（macOS 上如果被 kiro.app 抢占，会导致回调落到别的应用）
    if ((await isDarwinPlatform()) && window.go?.main?.App?.EnsureKiroProtocolHandler) {
      try {
        if (window.go?.main?.App?.GetKiroProtocolHandlerStatus) {
          const existing = await window.go.main.App.GetKiroProtocolHandlerStatus()
          socialLoginState.originalKiroHandlerBundleId = existing?.defaultHandlerBundleId || ''
        }

        const status = await window.go.main.App.EnsureKiroProtocolHandler()
        if (status?.supported === false) {
          showError(t('kiro.protocolHandlerUnsupported'))
          return
        }
        if (status && !status.isDefaultHandler) {
          showError(t('kiro.protocolHandlerNotDefault'))
          return
        }
        socialLoginState.didOverrideKiroHandler =
          !!socialLoginState.originalKiroHandlerBundleId &&
          !!status?.ourBundleId &&
          socialLoginState.originalKiroHandlerBundleId !== status.ourBundleId
      } catch (e) {
        console.warn('EnsureKiroProtocolHandler failed:', e)
        showError(t('kiro.protocolHandlerSetFailed') + ': ' + (e?.message || e))
        return
      }
    }

    // 1. 生成 PKCE 参数
    const verifier = generateRandomString(128)
    const challenge = await generateCodeChallenge(verifier)
    const state = generateUUID()

    // 2. 构建 OAuth URL
    const redirectUri = 'kiro://kiro.kiroAgent/authenticate-success'
    const loginUrl =
      `https://prod.us-east-1.auth.desktop.kiro.dev/login?` +
      `idp=${provider}&` +
      `redirect_uri=${encodeURIComponent(redirectUri)}&` +
      `code_challenge=${challenge}&` +
      `code_challenge_method=S256&` +
      `state=${state}`

    // 3. 保存状态
    socialLoginState.provider = provider
    socialLoginState.verifier = verifier
    socialLoginState.state = state
    socialLoginState.loginUrl = loginUrl
    socialLoginState.isWaiting = true

    // 4. 显示等待 Modal
    showSocialLoginModal()

    // 5. 打开浏览器（带降级方案）
    try {
      if (window.go?.main?.App?.OpenURLInIncognito) {
        await window.go.main.App.OpenURLInIncognito(loginUrl)
      } else {
        window.open(loginUrl, '_blank', 'noopener,noreferrer')
      }
    } catch (browserError) {
      console.warn('Failed to open browser, falling back to window.open:', browserError)
      window.open(loginUrl, '_blank', 'noopener,noreferrer')
    }

    // 6. 开始监听 deeplink 回调
    startDeeplinkListener()

    // 7. 设置超时定时器（5 分钟）
    socialLoginState.timeoutTimer = setTimeout(
      () => {
        if (socialLoginState.isWaiting) {
          console.warn('Social login timeout after 5 minutes')
          showError(t('kiro.socialLoginTimeout'))
          closeSocialLoginModal()
        }
      },
      5 * 60 * 1000,
    ) // 5 分钟
  } catch (error) {
    console.error('Failed to start social login:', error)
    showError(t('kiro.socialLoginFailed') + ': ' + (error?.message || error))
    closeSocialLoginModal()
  }
}

/**
 * 监听 deeplink 回调
 */
function startDeeplinkListener() {
  // 使用 Wails 的事件系统
  if (window.runtime?.EventsOn) {
    window.runtime.EventsOn('deeplink-callback', handleDeeplinkCallback)
  }
}

/**
 * 处理 deeplink 回调
 * @param {string} url - deeplink URL
 */
async function handleDeeplinkCallback(url) {
  try {
    console.log('Deeplink callback received:', url)

    // 1. 解析 URL
    const urlObj = new URL(url)
    const code = urlObj.searchParams.get('code')
    const state = urlObj.searchParams.get('state')

    // 2. 验证 state（防止 CSRF）
    if (state !== socialLoginState.state) {
      throw new Error('State mismatch - possible CSRF attack')
    }

    if (!code) {
      throw new Error('No authorization code received')
    }

    // 3. 交换 token（获取完整响应）
    const tokenResponse = await exchangeCodeForToken(code)

    // 4. 回填到配置表单
    const refreshTokenEl = document.getElementById('kiroRefreshToken')
    const accessTokenEl = document.getElementById('kiroAccessToken')
    const profileArnEl = document.getElementById('kiroProfileArn')

    if (refreshTokenEl && tokenResponse.refreshToken) {
      refreshTokenEl.value = tokenResponse.refreshToken
    }
    if (accessTokenEl && tokenResponse.accessToken) {
      accessTokenEl.value = tokenResponse.accessToken
    }
    if (profileArnEl && tokenResponse.profileArn) {
      profileArnEl.value = tokenResponse.profileArn
    }

    // 5. 更新 testedKiroCreds 以便保存
    const regionEl = document.getElementById('kiroRegion')
    const region = regionEl?.value || 'us-east-1'

    testedRefreshToken = tokenResponse.refreshToken || ''
    testedKiroCreds = {
      accessToken: tokenResponse.accessToken || '',
      refreshToken: tokenResponse.refreshToken || '',
      expiresAt: tokenResponse.expiresAt || '',
      profileArn: tokenResponse.profileArn || '',
      region: region,
      authMethod: 'social',
      clientId: '',
      clientSecret: '',
    }

    // 6. 尝试从 profileArn 提取 region 并更新
    if (tokenResponse.profileArn) {
      const regionFromArn = extractRegionFromProfileArn(tokenResponse.profileArn)
      if (regionFromArn && trySelectKiroRegion(regionFromArn) && testedKiroCreds) {
        testedKiroCreds.region = regionFromArn
      }
    }

    // 7. 更新按钮状态
    updateKiroConfigButtons()

    // 8. 登录成功后恢复 kiro:// 默认 handler（避免长期抢占）
    await restoreKiroProtocolHandlerIfNeeded()

    // 8. 关闭 Modal 并显示成功
    closeSocialLoginModal()
    showSuccess(t('kiro.socialLoginSuccess'))

    // 9. 清理状态
    resetSocialLoginState()
  } catch (error) {
    console.error('Deeplink callback error:', error)
    showError(t('kiro.socialLoginFailed') + ': ' + (error?.message || error))
    closeSocialLoginModal()
  }
}

/**
 * 交换授权码获取 token
 * @param {string} code - 授权码
 * @returns {Promise<Object>} Token 响应对象
 */
async function exchangeCodeForToken(code) {
  const response = await fetch('https://prod.us-east-1.auth.desktop.kiro.dev/oauth/token', {
    method: 'POST',
    headers: {
      'Content-Type': 'application/json',
    },
    body: JSON.stringify({
      code: code,
      code_verifier: socialLoginState.verifier,
      redirect_uri: 'kiro://kiro.kiroAgent/authenticate-success',
    }),
  })

  if (!response.ok) {
    const errorText = await response.text()
    throw new Error(`Token exchange failed: ${response.status} - ${errorText}`)
  }

  const data = await response.json()

  // 返回完整的 token 响应（支持 snake_case 和 camelCase）
  return {
    accessToken: data.access_token || data.accessToken || '',
    refreshToken: data.refresh_token || data.refreshToken || '',
    profileArn: data.profile_arn || data.profileArn || '',
    expiresIn: data.expires_in || data.expiresIn || 0,
    expiresAt: calculateExpiresAt(data.expires_in || data.expiresIn || 3600),
    idToken: data.id_token || data.idToken || '',
    tokenType: data.token_type || data.tokenType || '',
    csrfToken: data.csrf_token || data.csrfToken || '',
  }
}

/**
 * 计算 token 过期时间
 * @param {number} expiresIn - 过期秒数
 * @returns {string} 格式化的过期时间
 */
function calculateExpiresAt(expiresIn) {
  const now = new Date()
  const expiresAt = new Date(now.getTime() + expiresIn * 1000)
  return expiresAt
    .toLocaleString('zh-CN', {
      year: 'numeric',
      month: '2-digit',
      day: '2-digit',
      hour: '2-digit',
      minute: '2-digit',
      second: '2-digit',
      hour12: false,
    })
    .replace(/\//g, '-')
}

/**
 * 显示 Social 登录 Modal
 */
function showSocialLoginModal() {
  const modal = document.getElementById('socialLoginModal')
  if (modal) {
    const urlEl = document.getElementById('socialLoginUrl')
    if (urlEl) {
      urlEl.textContent = socialLoginState.loginUrl
      urlEl.title = socialLoginState.loginUrl
    }
    modal.classList.add('active')
  }
}

/**
 * 关闭 Social 登录 Modal
 */
export function closeSocialLoginModal() {
  const modal = document.getElementById('socialLoginModal')
  if (modal) {
    modal.classList.remove('active')
  }
  resetSocialLoginState()
}

/**
 * 重置 Social 登录状态
 */
function resetSocialLoginState() {
  socialLoginState.provider = ''
  socialLoginState.verifier = ''
  socialLoginState.state = ''
  socialLoginState.loginUrl = ''
  socialLoginState.isWaiting = false
  socialLoginState.originalKiroHandlerBundleId = ''
  socialLoginState.didOverrideKiroHandler = false

  // 清除超时定时器
  if (socialLoginState.timeoutTimer) {
    clearTimeout(socialLoginState.timeoutTimer)
    socialLoginState.timeoutTimer = null
  }

  // 移除事件监听
  if (window.runtime?.EventsOff) {
    window.runtime.EventsOff('deeplink-callback')
  }
}

/**
 * 复制登录 URL
 */
export function copySocialLoginUrl() {
  navigator.clipboard
    .writeText(socialLoginState.loginUrl)
    .then(() => showSuccess(t('kiro.linkCopied')))
    .catch((err) => showError(t('kiro.copyFailed') + ': ' + err))
}

/**
 * 打开登录 URL
 */
export async function openSocialLoginUrl() {
  if (window.go?.main?.App?.OpenURLInIncognito) {
    await window.go.main.App.OpenURLInIncognito(socialLoginState.loginUrl)
  } else {
    window.open(socialLoginState.loginUrl, '_blank', 'noopener,noreferrer')
  }
}
