/**
 * Kiro configuration module
 */
import { showError, showSuccess } from './utils.js'
import { t } from '../i18n/index.js'
import { clearTransformersCache } from './endpoint-form.js'
import { logError, logKiroUsageDetails } from './console.js'

let initialRefreshToken = ''
let testedRefreshToken = ''
let testedKiroCreds = null

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

  const hasOption = Array.from(select.options).some(o => o.value === normalized)
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
  onKiroIdcFieldsInput()
}

function updateIdcFieldsVisibility() {
  const authMethod = (document.getElementById('kiroAuthMethod')?.value || 'social').trim().toLowerCase()
  const idcFields = document.getElementById('kiroIdcFields')
  const idcSecretField = document.getElementById('kiroIdcSecretField')

  if (authMethod === 'idc') {
    if (idcFields) idcFields.style.display = ''
    if (idcSecretField) idcSecretField.style.display = ''
  } else {
    if (idcFields) idcFields.style.display = 'none'
    if (idcSecretField) idcSecretField.style.display = 'none'
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
    const msg = t('kiro.usageFailedPrefix') + (error?.message || error || 'Unknown error')
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
  Array.from(select.options).forEach(option => {
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
  Array.from(select.options).forEach(option => {
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
