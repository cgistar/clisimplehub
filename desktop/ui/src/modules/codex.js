/**
 * Codex global configuration module
 */
import { showError, showSuccess } from './utils.js'
import { t } from '../i18n/index.js'

export async function showCodexGlobalConfigModal() {
  try {
    if (!window.go?.main?.App?.GetCodexGlobalConfig) return
    const cfg = await window.go.main.App.GetCodexGlobalConfig()

    const rotEl = document.getElementById('codexRotationMode')
    const proxyEl = document.getElementById('codexGlobalProxyUrl')
    const baseURLEl = document.getElementById('codexBaseURL')
    const clientVersionEl = document.getElementById('codexClientVersion')
    const userAgentEl = document.getElementById('codexUserAgent')
    const originatorEl = document.getElementById('codexOriginator')

    if (rotEl) rotEl.value = cfg?.rotationMode || 'fixed'
    if (proxyEl) proxyEl.value = cfg?.proxyUrl || ''
    if (baseURLEl) baseURLEl.value = cfg?.baseURL || 'https://chatgpt.com/backend-api/codex'
    if (clientVersionEl) clientVersionEl.value = cfg?.clientVersion || '0.101.0'
    if (userAgentEl) userAgentEl.value = cfg?.userAgent || 'codex_cli_rs/0.101.0 (Mac OS 26.0.1; arm64) Apple_Terminal/464'
    if (originatorEl) originatorEl.value = cfg?.originator || 'codex_cli_rs'

    syncCodexRotationModeDisplay()
  } catch (error) {
    console.error('Failed to load Codex global config:', error)
    showError(t('codex.loadConfigFailed') + (error?.message || error))
    return
  }
  const modal = document.getElementById('codexGlobalConfigModal')
  if (modal) modal.classList.add('active')
}

export function closeCodexGlobalConfigModal() {
  const modal = document.getElementById('codexGlobalConfigModal')
  if (modal) modal.classList.remove('active')
}

export async function saveCodexGlobalConfig() {
  try {
    if (!window.go?.main?.App?.SaveCodexGlobalConfig) return
    const dto = {
      rotationMode: (document.getElementById('codexRotationMode')?.value || 'fixed').trim(),
      proxyUrl: (document.getElementById('codexGlobalProxyUrl')?.value || '').trim(),
      baseURL: (document.getElementById('codexBaseURL')?.value || '').trim(),
      clientVersion: (document.getElementById('codexClientVersion')?.value || '').trim(),
      userAgent: (document.getElementById('codexUserAgent')?.value || '').trim(),
      originator: (document.getElementById('codexOriginator')?.value || '').trim(),
    }
    await window.go.main.App.SaveCodexGlobalConfig(dto)
    closeCodexGlobalConfigModal()
    showSuccess(t('codex.globalConfigSaved'))
  } catch (error) {
    console.error('SaveCodexGlobalConfig error:', error)
    showError(t('codex.globalConfigSaveFailed') + (error?.message || error))
  }
}

// Rotation mode dropdown
function syncCodexRotationModeDisplay() {
  const select = document.getElementById('codexRotationMode')
  const display = document.getElementById('codexRotationModeDisplay')
  if (!select || !display) return
  const selectedOption = select.options[select.selectedIndex]
  if (selectedOption) display.value = selectedOption.textContent.trim()
  renderCodexRotationModeDropdown()
}

function renderCodexRotationModeDropdown() {
  const select = document.getElementById('codexRotationMode')
  const dropdown = document.getElementById('codexRotationModeDropdown')
  if (!select || !dropdown) return
  dropdown.innerHTML = ''
  Array.from(select.options).forEach((option) => {
    const item = document.createElement('div')
    item.className = 'model-dropdown-item'
    if (option.selected) item.classList.add('selected')
    item.textContent = option.textContent.trim()
    item.onclick = () => selectCodexRotationMode(option.value)
    dropdown.appendChild(item)
  })
}

function selectCodexRotationMode(value) {
  const select = document.getElementById('codexRotationMode')
  if (!select) return
  select.value = value
  syncCodexRotationModeDisplay()
  const dropdown = document.getElementById('codexRotationModeDropdown')
  if (dropdown) dropdown.classList.remove('show')
}

export function toggleCodexRotationModeDropdown() {
  const dropdown = document.getElementById('codexRotationModeDropdown')
  if (!dropdown) return
  if (dropdown.classList.contains('show')) {
    dropdown.classList.remove('show')
  } else {
    renderCodexRotationModeDropdown()
    dropdown.classList.add('show')
  }
}

window.toggleCodexRotationModeDropdown = toggleCodexRotationModeDropdown
