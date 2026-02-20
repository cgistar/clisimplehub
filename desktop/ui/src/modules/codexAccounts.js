/**
 * Codex 多账号管理模块
 * accountId 作为账号主键
 */
import { showError, showSuccess } from './utils.js'
import { t } from '../i18n/index.js'
import { createIcon } from './icons.js'
import { confirm as confirmDialog } from './confirm.js'

let codexAccounts = []
let activeAccountId = null

function escapeHTML(value) {
  return String(value ?? '').replace(/[&<>"']/g, (ch) => {
    switch (ch) {
      case '&':
        return '&amp;'
      case '<':
        return '&lt;'
      case '>':
        return '&gt;'
      case '"':
        return '&quot;'
      case "'":
        return '&#39;'
      default:
        return ch
    }
  })
}

export async function initCodexTabVisibility() {
  try {
    const available = await window.go.main.App.IsCodexAccountsAvailable()
    const tab = document.querySelector('.header-tab[data-tab="codex-accounts"]')
    if (tab) tab.style.display = available ? '' : 'none'
  } catch (e) {
    const tab = document.querySelector('.header-tab[data-tab="codex-accounts"]')
    if (tab) tab.style.display = 'none'
  }
}

export function getCodexAccounts() {
  return codexAccounts
}
export function getActiveCodexRefreshToken() {
  return activeAccountId
}
export function getActiveCodexAccountId() {
  return activeAccountId
}

export async function loadCodexAccounts() {
  try {
    if (!window.go?.main?.App?.GetCodexAccounts) {
      console.warn('GetCodexAccounts API not available')
      return
    }
    const result = await window.go.main.App.GetCodexAccounts()
    codexAccounts = result?.accounts || []
    activeAccountId = result?.activeAccountId || null
    if (!activeAccountId && result?.activeRefreshToken) {
      const activeByToken = codexAccounts.find((a) => a.refreshToken === result.activeRefreshToken)
      activeAccountId = activeByToken?.accountId || null
    }
    return { accounts: codexAccounts, activeAccountId, activeRefreshToken: result?.activeRefreshToken || null }
  } catch (error) {
    console.error('Failed to load codex accounts:', error)
    showError(t('codex.loadAccountsFailed') + (error?.message || error))
    return null
  }
}

export async function setActiveCodexAccount(accountId) {
  try {
    if (!window.go?.main?.App?.SetActiveCodexAccount) throw new Error('SetActiveCodexAccount API not available')
    await window.go.main.App.SetActiveCodexAccount(accountId)
    activeAccountId = accountId
    showSuccess(t('codex.accountSwitched'))
    await loadCodexAccounts()
    renderCodexAccountCards()
  } catch (error) {
    console.error('Failed to set active codex account:', error)
    showError(t('codex.switchAccountFailed') + (error?.message || error))
    try {
      await loadCodexAccounts()
      renderCodexAccountCards()
    } catch (_) {}
  }
}

export async function addCodexAccount(accountData) {
  try {
    if (!window.go?.main?.App?.AddCodexAccount) throw new Error('AddCodexAccount API not available')
    const result = await window.go.main.App.AddCodexAccount(accountData)
    showSuccess(t('codex.accountAdded'))
    await loadCodexAccounts()
    renderCodexAccountCards()
    return result
  } catch (error) {
    console.error('Failed to add codex account:', error)
    const errorMsg = error?.message || String(error)

    // Check if it's a duplicate account error - try to update instead
    if (errorMsg.includes('already exists') || errorMsg.includes('duplicate')) {
      console.log('Account already exists, attempting to update with latest info:', accountData.email || accountData.accountId)

      try {
        // Update the existing account with latest OAuth data
        if (!window.go?.main?.App?.UpdateCodexAccount) throw new Error('UpdateCodexAccount API not available')
        await window.go.main.App.UpdateCodexAccount(accountData)
        showSuccess(t('codex.accountUpdatedWithLatest'))
        await loadCodexAccounts()
        renderCodexAccountCards()
        return null
      } catch (updateError) {
        console.error('Failed to update existing account:', updateError)
        showError(t('codex.updateAccountFailed') + (updateError?.message || updateError))
        return null
      }
    }

    // Show error for other failures
    showError(t('codex.addAccountFailed') + errorMsg)
    try {
      await loadCodexAccounts()
      renderCodexAccountCards()
    } catch (_) {}
    return null
  }
}

export async function updateCodexAccount(accountData) {
  try {
    if (!window.go?.main?.App?.UpdateCodexAccount) throw new Error('UpdateCodexAccount API not available')
    await window.go.main.App.UpdateCodexAccount(accountData)
    showSuccess(t('codex.accountUpdated'))
    await loadCodexAccounts()
    renderCodexAccountCards()
  } catch (error) {
    console.error('Failed to update codex account:', error)
    showError(t('codex.updateAccountFailed') + (error?.message || error))
    try {
      await loadCodexAccounts()
      renderCodexAccountCards()
    } catch (_) {}
  }
}

export async function deleteCodexAccount(accountId) {
  try {
    if (!window.go?.main?.App?.DeleteCodexAccount) throw new Error('DeleteCodexAccount API not available')
    await window.go.main.App.DeleteCodexAccount(accountId)
    showSuccess(t('codex.accountDeleted'))
    await loadCodexAccounts()
    renderCodexAccountCards()
  } catch (error) {
    console.error('Failed to delete codex account:', error)
    showError(t('codex.deleteAccountFailed') + (error?.message || error))
    try {
      await loadCodexAccounts()
      renderCodexAccountCards()
    } catch (_) {}
  }
}

export async function testCodexAccount(accountId) {
  try {
    if (!window.go?.main?.App?.TestCodexAccount) throw new Error('TestCodexAccount API not available')
    const result = await window.go.main.App.TestCodexAccount(accountId)
    showSuccess(t('codex.testSuccess'))
    await loadCodexAccounts()
    renderCodexAccountCards()
    return result
  } catch (error) {
    console.error('Failed to test codex account:', error)
    showError(t('codex.testFailed') + (error?.message || error))
    try {
      await loadCodexAccounts()
      renderCodexAccountCards()
    } catch (_) {}
    return null
  }
}

function getStatusBadgeClass(status) {
  switch (status) {
    case 'valid':
      return 'status-valid'
    case 'banned':
      return 'status-banned'
    case 'exhausted':
      return 'status-exhausted'
    case 'reused':
      return 'status-banned'
    case 'rate_limited':
      return 'status-exhausted'
    default:
      return 'status-unknown'
  }
}

function getStatusText(account) {
  if (account.cooldownRemaining > 0) {
    const mins = Math.ceil(account.cooldownRemaining / 60)
    const reason =
      account.cooldownReason === 'rate_limit' ? t('codex.rateLimit') : account.cooldownReason || t('codex.cooldown')
    if (mins >= 60) {
      const h = Math.floor(mins / 60)
      const m = mins % 60
      return `${reason} ${h}h${m > 0 ? m + 'm' : ''}`
    }
    return `${reason} ${mins}m`
  }
  switch (account.status) {
    case 'valid':
      return t('codex.statusValid')
    case 'banned':
      return t('codex.statusBanned')
    case 'exhausted':
      return t('codex.statusExhausted')
    case 'reused':
      return t('codex.statusReused')
    default:
      return t('codex.statusUnknown')
  }
}

function getPlanTypeLabel(planType) {
  if (!planType) return ''
  return planType.charAt(0).toUpperCase() + planType.slice(1)
}

function truncateToken(token) {
  if (!token || token.length <= 16) return token || ''
  return token.substring(0, 8) + '...' + token.substring(token.length - 8)
}

function formatDate(dateString) {
  if (!dateString) return ''
  const date = new Date(dateString)
  if (isNaN(date.getTime())) return ''
  return date.toLocaleDateString('zh-CN', { year: 'numeric', month: '2-digit', day: '2-digit' })
}

function getTokenExpireInfo(expiresAt) {
  if (!expiresAt) return { text: '', isExpired: false }
  const expiresDate = new Date(expiresAt)
  const now = new Date()
  if (isNaN(expiresDate.getTime())) return { text: '', isExpired: false }
  const diffMs = expiresDate - now
  if (diffMs <= 0) return { text: t('codex.tokenExpired'), isExpired: true }
  const diffMinutes = Math.floor(diffMs / (1000 * 60))
  const diffHours = Math.floor(diffMs / (1000 * 60 * 60))
  const diffDays = Math.floor(diffMs / (1000 * 60 * 60 * 24))
  if (diffDays > 0) return { text: `${diffDays}d`, isExpired: false }
  if (diffHours > 0) return { text: `${diffHours}h`, isExpired: false }
  return { text: `${diffMinutes}m`, isExpired: false }
}

function renderUsageBars(codexUsage) {
  if (!codexUsage) return ''
  const primary = codexUsage.primary
  const secondary = codexUsage.secondary
  if (!primary && !secondary) return ''

  const barColor = (pct) =>
    pct >= 90 ? 'var(--error-color)' : pct >= 70 ? 'var(--warning-color)' : 'var(--primary-color)'
  const resetText = (remaining) => {
    if (!remaining || remaining <= 0) return ''
    const d = Math.floor(remaining / 86400)
    const h = Math.floor((remaining % 86400) / 3600)
    const m = Math.floor((remaining % 3600) / 60)
    if (d > 0) return `${d}d${h > 0 ? h + 'h' : ''}`
    if (h > 0) return `${h}h${m > 0 ? m + 'm' : ''}`
    return `${m}m`
  }

  let html = ''
  if (primary) {
    const pct = Math.min(primary.usedPercent || 0, 100)
    html += `
      <div class="kiro-progress-section">
        <span style="font-size:10px;color:var(--text-tertiary);min-width:24px;">${t('codex.usage5h')}</span>
        <div class="kiro-progress-track">
          <div class="kiro-progress-fill" style="width:${pct}%;background:${barColor(pct)};"></div>
        </div>
        <span class="kiro-progress-text">${pct.toFixed(0)}%</span>
        ${resetText(primary.remainingSeconds) ? `<span style="font-size:10px;color:var(--text-tertiary);">${resetText(primary.remainingSeconds)}</span>` : ''}
      </div>`
  }
  if (secondary) {
    const pct = Math.min(secondary.usedPercent || 0, 100)
    html += `
      <div class="kiro-progress-section">
        <span style="font-size:10px;color:var(--text-tertiary);min-width:24px;">${t('codex.usageWeek')}</span>
        <div class="kiro-progress-track">
          <div class="kiro-progress-fill" style="width:${pct}%;background:${barColor(pct)};"></div>
        </div>
        <span class="kiro-progress-text">${pct.toFixed(0)}%</span>
        ${resetText(secondary.remainingSeconds) ? `<span style="font-size:10px;color:var(--text-tertiary);">${resetText(secondary.remainingSeconds)}</span>` : ''}
      </div>`
  }
  return html
}

function renderAccountCard(account) {
  const hasAccountId = Boolean(account.accountId)
  const isActive = account.isActive || (hasAccountId && account.accountId === activeAccountId)
  const isCoolingDown = account.cooldownRemaining > 0
  const encodedAccountId = btoa(account.accountId || '')
  const hasRefreshToken = Boolean(account.refreshToken)

  const powerIcon = createIcon('power', { size: 14 })
  const copyIcon = createIcon('copy', { size: 14 })
  const editIcon = createIcon('edit', { size: 14 })
  const trashIcon = createIcon('trash', { size: 14 })
  const refreshIcon = createIcon('refreshCw', { size: 14 })
  const batteryIcon = createIcon('battery', { size: 14 })

  const statusBadgeClass = isCoolingDown ? 'status-exhausted' : getStatusBadgeClass(account.status)
  const statusText = getStatusText(account)
  const expireInfo = getTokenExpireInfo(account.expiresAt)
  const canActivate = hasAccountId && !isActive && account.status !== 'banned' && account.status !== 'reused'
  const canOperate = hasAccountId
  const planLabel = getPlanTypeLabel(account.planType)

  return `
    <div class="kiro-account-card ${isActive ? 'active' : ''} ${account.status === 'banned' ? 'banned' : ''} ${isCoolingDown ? 'banned' : ''}" data-account-id="${encodedAccountId}">
      <div class="kiro-card-header" style="align-items: center;">
        <span class="kiro-account-email" style="flex: 1; min-width: 0; font-weight: 600; font-size: 13px; color: var(--text-primary); overflow: hidden; text-overflow: ellipsis; white-space: nowrap; margin-right: 10px;" title="${escapeHTML(account.email || account.accountId || truncateToken(account.refreshToken))}">${escapeHTML(account.email || account.accountId || truncateToken(account.refreshToken))}</span>
        <span class="kiro-status-badge ${statusBadgeClass}" style="margin-left: auto; flex-shrink: 0;">${statusText}</span>
      </div>
      <div class="kiro-header-tags" style="display: flex; align-items: center;">
        ${planLabel ? `<span class="kiro-tag kiro-tag-plan">${escapeHTML(planLabel)}</span>` : ''}
        <span class="kiro-tag kiro-tag-auth">OpenAI</span>
        ${account.weight > 0 ? `<span class="kiro-tag" style="background: var(--bg-tertiary);">W:${account.weight}</span>` : ''}
        ${!hasRefreshToken ? `<span class="kiro-tag" style="background: var(--warning-bg); color: var(--warning-text);" title="${t('codex.noRefreshToken')}">${t('codex.tempToken')}</span>` : ''}
        ${isActive ? `<span class="kiro-badge-active" style="margin-left: auto;">${t('codex.active')}</span>` : ''}
      </div>
      <div class="kiro-card-body">
        ${renderUsageBars(account.codexUsage)}
        <div class="kiro-usage-meta" style="display: flex; justify-content: space-between; align-items: center;">
          ${account.proxyUrl ? `<span style="font-size: 11px; color: var(--text-tertiary);">${t('codex.proxy')}: ${escapeHTML(account.proxyUrl.substring(0, 30))}${account.proxyUrl.length > 30 ? '...' : ''}</span>` : '<span></span>'}
        </div>
      </div>
      <div class="kiro-card-footer">
        <div class="kiro-expire-info ${expireInfo.isExpired ? 'expired' : ''}">
          ${expireInfo.text ? `Token ${expireInfo.text}` : ''}
        </div>
        <div class="kiro-card-actions">
          ${canActivate ? `<button class="kiro-icon-btn primary" onclick="setActiveCodexAccountFromCard('${encodedAccountId}')" title="${t('codex.activate')}">${powerIcon}</button>` : ''}
          ${canOperate && hasRefreshToken ? `<button class="kiro-icon-btn" onclick="testCodexAccountFromCard('${encodedAccountId}')" title="${t('codex.refreshToken')}">${refreshIcon}</button>` : ''}
          ${canOperate && !hasRefreshToken ? `<button class="kiro-icon-btn" disabled style="opacity: 0.3; cursor: not-allowed;" title="${t('codex.noRefreshTokenHint')}">${refreshIcon}</button>` : ''}
          ${canOperate ? `<button class="kiro-icon-btn" onclick="fetchCodexAccountUsageFromCard('${encodedAccountId}')" title="${t('codex.usage')}">${batteryIcon}</button>` : ''}
          ${canOperate ? `<button class="kiro-icon-btn" onclick="copyCodexAccountFromCard('${encodedAccountId}')" title="${t('codex.copy')}">${copyIcon}</button>` : ''}
          ${canOperate ? `<button class="kiro-icon-btn" onclick="editCodexAccountFromCard('${encodedAccountId}')" title="${t('codex.edit')}">${editIcon}</button>` : ''}
          ${canOperate ? `<button class="kiro-icon-btn kiro-icon-btn-danger" onclick="deleteCodexAccountFromCard('${encodedAccountId}')" title="${t('common.delete')}">${trashIcon}</button>` : ''}
        </div>
      </div>
    </div>`
}

export function renderCodexAccountCards() {
  const container = document.getElementById('codexAccountsGrid')
  if (!container) return

  if (codexAccounts.length === 0) {
    container.innerHTML = `<div class="kiro-no-accounts"><p>${t('codex.noAccounts')}</p></div>`
    return
  }

  container.innerHTML = codexAccounts.map(renderAccountCard).join('')
}

function decodeAccountId(encoded) {
  try {
    return atob(encoded)
  } catch (e) {
    return encoded
  }
}

function getButtonElement(target) {
  if (!target) return null
  let btn = target
  while (btn && !btn.classList.contains('kiro-icon-btn')) {
    btn = btn.parentElement
    if (!btn || btn.tagName === 'BODY') return null
  }
  return btn
}

async function withButtonLoading(encodedAccountId, fn) {
  const btn = getButtonElement(event?.target)
  if (btn?.disabled) return
  const originalHTML = btn?.innerHTML
  if (btn) {
    btn.disabled = true
    btn.classList.add('loading')
  }
  try {
    const accountId = decodeAccountId(encodedAccountId)
    if (!accountId) {
      showError(t('codex.accountNotFound'))
      return
    }
    await fn(accountId)
  } finally {
    if (btn) {
      btn.disabled = false
      btn.classList.remove('loading')
      if (originalHTML) btn.innerHTML = originalHTML
    }
  }
}

window.setActiveCodexAccountFromCard = (t) => withButtonLoading(t, setActiveCodexAccount)
window.testCodexAccountFromCard = (t) => withButtonLoading(t, testCodexAccount)

export async function fetchCodexAccountUsage(accountId) {
  try {
    const account = codexAccounts.find((acc) => acc.accountId === accountId)
    if (!account) {
      throw new Error(t('codex.accountNotFound'))
    }

    if (account.expiresAt) {
      const expiresAt = new Date(account.expiresAt)
      const now = new Date()
      if (!isNaN(expiresAt.getTime()) && expiresAt < now) {
        showError(t('codex.tokenExpiredClickTest'))
        await loadCodexAccounts()
        renderCodexAccountCards()
        return null
      }
    }

    if (!window.go?.main?.App?.GetCodexAccountUsage) {
      throw new Error('GetCodexAccountUsage API not available')
    }
    const result = await window.go.main.App.GetCodexAccountUsage(accountId)

    await loadCodexAccounts()
    renderCodexAccountCards()
    return result
  } catch (error) {
    console.error('Failed to fetch account usage:', error)
    showError(t('codex.usageFailedPrefix') + (error?.message || error))

    try {
      await loadCodexAccounts()
      renderCodexAccountCards()
    } catch (refreshError) {
      console.error('Failed to refresh account list after error:', refreshError)
    }
    return null
  }
}

window.fetchCodexAccountUsageFromCard = async function (encodedAccountId) {
  const btn = getButtonElement(event?.target)
  if (btn?.disabled) return

  const originalHTML = btn?.innerHTML
  if (btn) {
    btn.disabled = true
    btn.classList.add('loading')
  }
  try {
    const accountId = decodeAccountId(encodedAccountId)
    await fetchCodexAccountUsage(accountId)
  } finally {
    if (btn) {
      btn.disabled = false
      btn.classList.remove('loading')
      if (originalHTML) {
        btn.innerHTML = originalHTML
      }
    }
  }
}

window.deleteCodexAccountFromCard = async function (encodedToken) {
  const btn = getButtonElement(event?.target)
  if (btn?.disabled) return
  const confirmed = await confirmDialog(t('codex.deleteConfirm'), { danger: true })
  if (!confirmed) return
  const originalHTML = btn?.innerHTML
  if (btn) {
    btn.disabled = true
    btn.classList.add('loading')
  }
  try {
    const accountId = decodeAccountId(encodedToken)
    if (!accountId) {
      showError(t('codex.accountNotFound'))
      return
    }
    await deleteCodexAccount(accountId)
  } finally {
    if (btn) {
      btn.disabled = false
      btn.classList.remove('loading')
      if (originalHTML) btn.innerHTML = originalHTML
    }
  }
}

window.copyCodexAccountFromCard = async function (encodedToken) {
  const btn = getButtonElement(event?.target)
  if (btn?.disabled) return
  const originalHTML = btn?.innerHTML
  if (btn) {
    btn.disabled = true
    btn.classList.add('loading')
  }
  try {
    const accountId = decodeAccountId(encodedToken)
    const account = codexAccounts.find((a) => a.accountId === accountId)
    if (!account) {
      showError(t('codex.accountNotFound'))
      return
    }
    const copyData = {}
    ;['refreshToken', 'email', 'accountId', 'planType', 'proxyUrl', 'weight'].forEach((f) => {
      if (account[f]) copyData[f] = account[f]
    })
    await navigator.clipboard.writeText(JSON.stringify(copyData, null, 2))
    showSuccess(t('codex.copySuccess'))
  } catch (error) {
    showError(t('codex.copyFailed') + (error?.message || error))
  } finally {
    if (btn) {
      btn.disabled = false
      btn.classList.remove('loading')
      if (originalHTML) btn.innerHTML = originalHTML
    }
  }
}

// Edit modal
let editingAccountId = ''
let editingAccount = null

window.editCodexAccountFromCard = function (encodedToken) {
  const accountId = decodeAccountId(encodedToken)
  const account = codexAccounts.find((a) => a.accountId === accountId)
  if (!account) {
    showError(t('codex.accountNotFound'))
    return
  }

  editingAccountId = accountId
  editingAccount = account

  const set = (id, v) => {
    const el = document.getElementById(id)
    if (el) el.value = v || ''
  }
  set('editCodexRefreshToken', account.refreshToken)
  set('editCodexProxyUrl', account.proxyUrl)
  set('editCodexWeight', account.weight > 0 ? String(account.weight) : '')

  const modal = document.getElementById('codexAccountEditModal')
  if (modal) modal.classList.add('active')
}

window.closeCodexAccountEditModal = function () {
  const modal = document.getElementById('codexAccountEditModal')
  if (modal) modal.classList.remove('active')
  editingAccountId = ''
  editingAccount = null
}

window.saveCodexAccountEdit = async function () {
  if (!editingAccountId || !editingAccount) return
  const val = (id) => (document.getElementById(id)?.value || '').trim()
  const dto = {
    ...editingAccount,
    accountId: editingAccountId,
    proxyUrl: val('editCodexProxyUrl'),
    weight: parseInt(val('editCodexWeight'), 10) || 0,
  }
  try {
    await updateCodexAccount(dto)
    closeCodexAccountEditModal()
  } catch (error) {
    showError(t('codex.updateAccountFailed') + (error?.message || error))
  }
}

// Add account dropdown
export function toggleCodexAddAccountDropdown() {
  const menu = document.getElementById('codexAddAccountMenu')
  if (menu) menu.style.display = menu.style.display !== 'none' ? 'none' : 'block'
}

export function hideCodexAddAccountDropdown() {
  const menu = document.getElementById('codexAddAccountMenu')
  if (menu) menu.style.display = 'none'
}

// OpenAI OAuth login
let codexLoginInProgress = false
let codexLoginAbortController = null

window.startCodexOAuthLogin = async function () {
  if (codexLoginInProgress) return
  codexLoginInProgress = true
  codexLoginAbortController = new AbortController()
  hideCodexAddAccountDropdown()

  try {
    if (!window.go?.main?.App?.StartCodexLoginWithURL) throw new Error('StartCodexLoginWithURL API not available')

    // Get OAuth URL
    const authURL = await window.go.main.App.StartCodexLoginWithURL()

    // Show dialog with URL
    showCodexOAuthLoginDialog(authURL)

    // Wait for callback in background with abort signal
    const waitPromise = window.go.main.App.WaitForCodexLoginCallback()
    const abortPromise = new Promise((_, reject) => {
      codexLoginAbortController.signal.addEventListener('abort', () => {
        reject(new Error('Login cancelled by user'))
      })
    })

    const result = await Promise.race([waitPromise, abortPromise])

    if (!result?.accountId) throw new Error('No account ID received')
    if (!result?.refreshToken && !result?.accessToken) throw new Error('No token payload received')

    // Close dialog
    closeCodexOAuthLoginModal()

    await addCodexAccount({
      refreshToken: result.refreshToken || '',
      accessToken: result.accessToken || '',
      idToken: result.idToken || '',
      expiresAt: result.expiresAt || '',
      email: result.email || '',
      accountId: result.accountId,
      planType: result.planType || '',
      status: 'valid',
    })
  } catch (error) {
    console.error('Codex OAuth login failed:', error)
    closeCodexOAuthLoginModal()
    if (error?.message !== 'Login cancelled by user') {
      showError(t('codex.oauthLoginFailed') + (error?.message || error))
    }
  } finally {
    codexLoginAbortController = null
    codexLoginInProgress = false
  }
}

function showCodexOAuthLoginDialog(authURL) {
  const existing = document.getElementById('codexOAuthLoginModal')
  if (existing) existing.remove()

  // Create modal container
  const container = document.createElement('div')
  container.innerHTML = window.codexOAuthLoginModalTemplate()
  const modal = container.firstElementChild
  document.body.appendChild(modal)

  // Add active class to show modal
  setTimeout(() => modal.classList.add('active'), 10)

  // Set URL
  const urlInput = document.getElementById('codexOAuthLoginUrl')
  if (urlInput) urlInput.value = authURL
}

window.closeCodexOAuthLoginModal = function () {
  const modal = document.getElementById('codexOAuthLoginModal')
  if (modal) modal.remove()

  // Abort ongoing login if exists
  if (codexLoginAbortController) {
    codexLoginAbortController.abort()
  }
}

window.copyCodexOAuthLoginUrl = async function () {
  const urlInput = document.getElementById('codexOAuthLoginUrl')
  if (!urlInput) return
  try {
    await navigator.clipboard.writeText(urlInput.value)
    showSuccess(t('codex.linkCopied'))
  } catch (error) {
    showError(t('codex.copyFailed') + (error?.message || error))
  }
}

window.openCodexOAuthLoginUrl = async function (incognito = false) {
  const urlInput = document.getElementById('codexOAuthLoginUrl')
  if (!urlInput || !urlInput.value) return

  try {
    if (incognito) {
      if (window.go?.main?.App?.OpenURLInIncognito) {
        await window.go.main.App.OpenURLInIncognito(urlInput.value)
      } else {
        showError(t('codex.incognitoNotSupported'))
      }
    } else {
      if (window.runtime?.BrowserOpenURL) {
        window.runtime.BrowserOpenURL(urlInput.value)
      } else {
        window.open(urlInput.value, '_blank', 'noopener,noreferrer')
      }
    }
  } catch (error) {
    console.error('Failed to open URL:', error)
    showError(t('codex.openLinkFailed') + ': ' + (error?.message || error))
  }
}

// JSON import
let jsonImportInProgress = false

function getStringField(item, keys) {
  if (!item || typeof item !== 'object') return ''
  for (const k of keys) {
    const v = item[k]
    if (typeof v === 'string') {
      const trimmed = v.trim()
      if (trimmed) return trimmed
    }
  }
  return ''
}

function parseImportItems(payload) {
  if (Array.isArray(payload)) return payload
  if (payload && typeof payload === 'object') {
    if (Array.isArray(payload.accounts)) return payload.accounts
    if (Array.isArray(payload.Accounts)) return payload.Accounts
  }
  return [payload]
}

function buildCodexImportDTOs(jsonText) {
  const errors = []
  let payload
  try {
    payload = JSON.parse(jsonText)
  } catch (e) {
    return { dtos: [], errors: ['JSON parse failed: ' + (e?.message || e)] }
  }

  const items = parseImportItems(payload)
  const dtos = []
  const seen = new Set()

  items.forEach((item, index) => {
    const accountId = getStringField(item, ['accountId', 'account_id', 'AccountId'])
    if (!accountId) {
      errors.push(`#${index + 1}: missing accountId`)
      return
    }
    if (seen.has(accountId)) {
      errors.push(`#${index + 1}: duplicate accountId`)
      return
    }
    seen.add(accountId)

    const refreshToken = getStringField(item, ['refreshToken', 'refresh_token', 'RefreshToken'])
    const accessToken = getStringField(item, ['accessToken', 'access_token', 'AccessToken'])
    if (!refreshToken && !accessToken) {
      errors.push(`#${index + 1}: missing refreshToken/accessToken`)
      return
    }

    const dto = {
      accountId,
      refreshToken,
      accessToken,
      idToken: getStringField(item, ['idToken', 'id_token', 'IdToken']),
      expiresAt: getStringField(item, ['expiresAt', 'expires_at', 'ExpiresAt']),
      email: getStringField(item, ['email', 'Email']),
      planType: getStringField(item, ['planType', 'plan_type', 'PlanType']),
      proxyUrl: getStringField(item, ['proxyUrl', 'proxy_url', 'ProxyUrl']),
    }
    const weightStr = getStringField(item, ['weight', 'Weight'])
    if (weightStr) dto.weight = parseInt(weightStr, 10) || 0

    Object.keys(dto).forEach((k) => {
      if (!dto[k]) delete dto[k]
    })
    dtos.push(dto)
  })

  return { dtos, errors }
}

window.showCodexJsonImportDialog = function () {
  hideCodexAddAccountDropdown()
  const existing = document.getElementById('codexJsonImportModal')
  if (existing) existing.remove()

  const modal = document.createElement('div')
  modal.className = 'modal active'
  modal.id = 'codexJsonImportModal'
  modal.innerHTML = `
    <div class="modal-content" style="max-width: 600px; display: flex; flex-direction: column;">
      <div class="modal-header">
        <h3>${t('codex.jsonImportModalTitle')}</h3>
        <button class="btn-icon" onclick="closeCodexJsonImportDialog()">${createIcon('close', { size: 20 })}</button>
      </div>
      <div class="modal-body" style="padding: 20px; flex: 1;">
        <div style="margin-bottom: 15px;">
          <label class="btn btn-secondary" for="codexJsonFileInput" style="cursor: pointer; display: inline-block;">
            ${createIcon('upload', { size: 16 })} ${t('codex.selectJsonFiles')}
          </label>
          <input type="file" id="codexJsonFileInput" accept=".json" multiple style="display: none;" onchange="handleCodexJsonFileSelect(event)">
          <span id="codexFileCount" style="margin-left: 10px; color: var(--text-secondary); font-size: 13px;"></span>
        </div>
        <div style="margin-bottom: 10px; color: var(--text-secondary); font-size: 12px;">
          ${t('codex.orPasteJson')}
        </div>
        <textarea id="codexJsonImportTextarea"
          placeholder='${t('codex.jsonImportPlaceholderText')}'
          style="width: 100%; height: 250px; font-family: monospace; padding: 10px; border: 1px solid var(--border-color); border-radius: 4px; resize: vertical; background: var(--bg-primary); color: var(--text-primary);"></textarea>
      </div>
      <div class="modal-footer">
        <button class="btn" onclick="closeCodexJsonImportDialog()">${t('common.cancel')}</button>
        <button class="btn btn-primary" onclick="executeCodexJsonImport()">${t('codex.importButton')}</button>
      </div>
    </div>`
  document.body.appendChild(modal)
  setTimeout(() => document.getElementById('codexJsonImportTextarea')?.focus(), 100)
}

window.handleCodexJsonFileSelect = async function (event) {
  const files = event.target.files
  if (!files || files.length === 0) return

  const fileCountSpan = document.getElementById('codexFileCount')
  if (fileCountSpan) {
    fileCountSpan.textContent = `${t('codex.selectedFiles')}: ${files.length}`
  }

  const allAccounts = []
  let errorCount = 0

  for (const file of files) {
    try {
      const text = await file.text()
      const data = JSON.parse(text)

      // Parse the file format (codex-xxx.json)
      const account = parseCodexJsonFile(data)
      if (account) {
        allAccounts.push(account)
      } else {
        errorCount++
      }
    } catch (e) {
      console.error(`Failed to parse file ${file.name}:`, e)
      errorCount++
    }
  }

  // Update textarea with parsed accounts
  const textarea = document.getElementById('codexJsonImportTextarea')
  if (textarea && allAccounts.length > 0) {
    textarea.value = JSON.stringify(allAccounts, null, 2)
  }

  if (errorCount > 0) {
    showError(`${t('codex.fileParseErrors')}: ${errorCount}/${files.length}`)
  }
}

function parseCodexJsonFile(data) {
  try {
    const account = {
      accessToken: data.access_token || data.accessToken || '',
      idToken: data.id_token || data.idToken || '',
      accountId: data.account_id || data.accountId || '',
      email: data.email || '',
      planType: data['https://api.openai.com/auth']?.chatgpt_plan_type || data.planType || 'free',
      expiresAt: data.expired || data.expiresAt || ''
    }

    // Only include refreshToken if it exists
    if (data.refresh_token || data.refreshToken) {
      account.refreshToken = data.refresh_token || data.refreshToken
    }

    // Validate required fields
    if (!account.accessToken || !account.accountId) {
      console.warn('Invalid account data: missing accessToken or accountId')
      return null
    }

    return account
  } catch (e) {
    console.error('Failed to parse account data:', e)
    return null
  }
}

window.closeCodexJsonImportDialog = function () {
  const modal = document.getElementById('codexJsonImportModal')
  if (modal) modal.remove()
}

window.executeCodexJsonImport = async function () {
  const textarea = document.getElementById('codexJsonImportTextarea')
  if (!textarea) return
  const jsonText = textarea.value.trim()
  if (!jsonText) {
    showError(t('codex.pasteJsonContent'))
    return
  }
  closeCodexJsonImportDialog()
  await processCodexJsonImport(jsonText)
}

async function processCodexJsonImport(jsonText) {
  if (jsonImportInProgress) return
  jsonImportInProgress = true
  try {
    if (!window.go?.main?.App?.AddCodexAccount) throw new Error('AddCodexAccount API not available')
    const { dtos, errors } = buildCodexImportDTOs(jsonText)
    if (!dtos.length) {
      showError(errors.length ? errors.slice(0, 3).join('\n') : t('codex.noValidAccounts'))
      return
    }
    if (errors.length) {
      const ok = await confirmDialog(
        t('codex.importConfirmWithErrors').replace('{count}', dtos.length).replace('{errors}', errors.length),
      )
      if (!ok) return
    } else {
      const ok = await confirmDialog(t('codex.importConfirm').replace('{count}', dtos.length))
      if (!ok) return
    }
    let successCount = 0,
      failedCount = 0,
      skippedCount = 0
    const failedReasons = []
    for (const dto of dtos) {
      try {
        await window.go.main.App.AddCodexAccount(dto)
        successCount++
      } catch (e) {
        const reason = e?.message || String(e)
        // Check if it's a duplicate account error (skip silently)
        if (reason.includes('already exists') || reason.includes('duplicate')) {
          skippedCount++
          console.log('Skipped duplicate account:', dto.email || dto.accountId)
        } else {
          failedCount++
          failedReasons.push(`${dto.email || dto.accountId}: ${reason}`)
          console.error('Failed to add account:', dto.accountId, reason)
        }
      }
    }
    await loadCodexAccounts()
    renderCodexAccountCards()

    if (failedCount > 0) {
      const message = t('codex.importSuccess').replace('{success}', successCount).replace('{failed}', failedCount)
      const details = failedReasons.slice(0, 3).join('\n')
      showError(`${message}\n\n${t('codex.failedDetails')}:\n${details}${failedReasons.length > 3 ? '\n...' : ''}`)
    } else {
      let message = t('codex.importSuccess').replace('{success}', successCount).replace('{failed}', failedCount)
      if (skippedCount > 0) {
        message += ` (${t('codex.skippedDuplicate')}: ${skippedCount})`
      }
      showSuccess(message)
    }
  } catch (error) {
    showError(t('codex.importFailed') + ': ' + (error?.message || error))
  } finally {
    jsonImportInProgress = false
  }
}

// Bulk delete
window.showCodexBulkDeleteDialog = function () {
  const manageableAccounts = (codexAccounts || []).filter((acc) => Boolean(acc.accountId))
  if (manageableAccounts.length === 0) {
    showError(t('codex.noAccountsAvailable'))
    return
  }
  const modal = document.createElement('div')
  modal.className = 'modal active'
  modal.id = 'codexBulkDeleteModal'

  const listHtml = manageableAccounts
    .map((acc) => {
      const isBanned = acc.status === 'banned' || acc.status === 'reused'
      const label = acc.email || truncateToken(acc.refreshToken)
      const statusText = getStatusText(acc)
      const statusClass = acc.cooldownRemaining > 0 ? 'status-exhausted' : getStatusBadgeClass(acc.status)
      const encodedAccountId = btoa(acc.accountId)
      return `
      <div class="kiro-bulk-item" style="display: flex; align-items: center; padding: 10px; border-bottom: 1px solid var(--border-light);">
        <input type="checkbox" class="codex-bulk-checkbox" value="${encodedAccountId}" data-banned="${isBanned}" id="codex-chk-${encodedAccountId}" style="margin-right: 12px; transform: scale(1.2);">
        <label for="codex-chk-${encodedAccountId}" style="flex: 1; cursor: pointer; display: flex; justify-content: space-between; align-items: center;">
          <span style="font-weight: 500; font-size: 13px;">${label}</span>
          <span class="kiro-status-badge ${statusClass}">${statusText}</span>
        </label>
      </div>`
    })
    .join('')

  modal.innerHTML = `
    <div class="modal-content" style="max-width: 500px; display: flex; flex-direction: column; max-height: 85vh;">
      <div class="modal-header">
        <h2>${t('codex.bulkDeleteTitle')}</h2>
        <button class="modal-close" onclick="this.closest('.modal').remove()">&times;</button>
      </div>
      <div class="modal-body" style="flex: 1; overflow: hidden; display: flex; flex-direction: column; padding: 0;">
        <div class="kiro-bulk-actions" style="padding: 15px; border-bottom: 1px solid var(--border-light); display: flex; gap: 10px; background: var(--bg-secondary);">
          <button class="btn btn-sm btn-secondary" onclick="toggleCodexBulkDeleteAll(true)">${t('codex.selectAll')}</button>
          <button class="btn btn-sm btn-secondary" onclick="toggleCodexBulkDeleteAll(false)">${t('codex.deselectAll')}</button>
          <button class="btn btn-sm btn-warning" onclick="selectCodexBulkDeleteBanned()">${t('codex.selectBannedAccounts')}</button>
        </div>
        <div class="kiro-bulk-list" style="overflow-y: auto; padding: 0 15px;">${listHtml}</div>
      </div>
      <div class="modal-footer" style="padding: 15px; border-top: 1px solid var(--border-light);">
        <button class="btn btn-secondary" onclick="this.closest('.modal').remove()">${t('common.cancel')}</button>
        <button class="btn btn-danger" onclick="executeCodexBulkDelete()">${t('common.delete')}</button>
      </div>
    </div>`
  document.body.appendChild(modal)
}

window.toggleCodexBulkDeleteAll = function (checked) {
  document.querySelectorAll('#codexBulkDeleteModal .codex-bulk-checkbox').forEach((cb) => (cb.checked = checked))
}

window.selectCodexBulkDeleteBanned = function () {
  document.querySelectorAll('#codexBulkDeleteModal .codex-bulk-checkbox').forEach((cb) => {
    if (cb.dataset.banned === 'true') cb.checked = true
  })
}

window.executeCodexBulkDelete = async function () {
  const checkboxes = document.querySelectorAll('#codexBulkDeleteModal .codex-bulk-checkbox:checked')
  if (checkboxes.length === 0) {
    showError(t('codex.selectAtLeastOne'))
    return
  }
  const confirmed = await confirmDialog(t('codex.bulkDeleteConfirm').replace('{count}', checkboxes.length), {
    danger: true,
  })
  if (!confirmed) return

  const modal = document.getElementById('codexBulkDeleteModal')
  const deleteBtn = modal?.querySelector('.btn-danger')
  const originalText = deleteBtn?.innerHTML
  if (deleteBtn) {
    deleteBtn.disabled = true
    deleteBtn.innerHTML = t('codex.deleting')
  }

  try {
    let successCount = 0
    for (const cb of checkboxes) {
      try {
        if (window.go?.main?.App?.DeleteCodexAccount) {
          await window.go.main.App.DeleteCodexAccount(decodeAccountId(cb.value))
          successCount++
        }
      } catch (e) {
        console.error('Delete failed:', e)
      }
    }
    showSuccess(`${t('codex.deletedCount')} ${successCount}/${checkboxes.length}`)
    if (modal) modal.remove()
    await loadCodexAccounts()
    renderCodexAccountCards()
  } catch (e) {
    showError(t('codex.bulkDeleteFailed'))
  } finally {
    if (document.body.contains(modal) && deleteBtn) {
      deleteBtn.disabled = false
      deleteBtn.innerHTML = originalText
    }
  }
}

window.toggleCodexAddAccountDropdown = toggleCodexAddAccountDropdown
window.hideCodexAddAccountDropdown = hideCodexAddAccountDropdown
