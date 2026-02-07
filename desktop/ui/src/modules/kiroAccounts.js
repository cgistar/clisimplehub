/**
 * Kiro 多账号管理模块
 * refreshToken 作为账号主键
 */
import { showError, showSuccess } from './utils.js'
import { t } from '../i18n/index.js'
import { createIcon } from './icons.js'
import { confirm as confirmDialog } from './confirm.js'

// 状态
let kiroAccounts = []
let activeRefreshToken = null

function escapeHTML(value) {
  return String(value ?? '').replace(/[&<>"']/g, (ch) => {
    switch (ch) {
      case '&': return '&amp;'
      case '<': return '&lt;'
      case '>': return '&gt;'
      case '"': return '&quot;'
      case "'": return '&#39;'
      default: return ch
    }
  })
}

// 获取状态
export function getKiroAccounts() {
  return kiroAccounts
}

export function getActiveRefreshToken() {
  return activeRefreshToken
}

// 加载账号列表
export async function loadKiroAccounts() {
  try {
    if (!window.go?.main?.App?.GetKiroAccounts) {
      console.warn('GetKiroAccounts API not available')
      return
    }
    const result = await window.go.main.App.GetKiroAccounts()
    kiroAccounts = result?.accounts || []
    activeRefreshToken = result?.activeRefreshToken || null
    return { accounts: kiroAccounts, activeRefreshToken }
  } catch (error) {
    console.error('Failed to load kiro accounts:', error)
    showError(t('kiro.loadAccountsFailed') + (error?.message || error))
    return null
  }
}

// 设置激活账号（参数为 refreshToken）
export async function setActiveKiroAccount(refreshToken) {
  try {
    if (!window.go?.main?.App?.SetActiveKiroAccount) {
      throw new Error('SetActiveKiroAccount API not available')
    }
    await window.go.main.App.SetActiveKiroAccount(refreshToken)
    activeRefreshToken = refreshToken

    showSuccess(t('kiro.accountSwitched'))

    // 刷新列表
    await loadKiroAccounts()
    renderKiroAccountCards()
  } catch (error) {
    console.error('Failed to set active account:', error)
    showError(t('kiro.switchAccountFailed') + (error?.message || error))

    // 错误时也要刷新列表，以获取最新状态
    try {
      await loadKiroAccounts()
      renderKiroAccountCards()
    } catch (refreshError) {
      console.error('Failed to refresh account list after error:', refreshError)
    }
  }
}

// 添加账号
export async function addKiroAccount(accountData) {
  try {
    if (!window.go?.main?.App?.AddKiroAccount) {
      throw new Error('AddKiroAccount API not available')
    }
    const result = await window.go.main.App.AddKiroAccount(accountData)
    showSuccess(t('kiro.accountAdded'))

    // 刷新列表
    await loadKiroAccounts()
    renderKiroAccountCards()
    return result
  } catch (error) {
    console.error('Failed to add account:', error)
    showError(t('kiro.addAccountFailed') + (error?.message || error))

    // 错误时也要刷新列表，以获取最新状态
    try {
      await loadKiroAccounts()
      renderKiroAccountCards()
    } catch (refreshError) {
      console.error('Failed to refresh account list after error:', refreshError)
    }

    return null
  }
}

// 更新账号
export async function updateKiroAccount(accountData) {
  try {
    if (!window.go?.main?.App?.UpdateKiroAccount) {
      throw new Error('UpdateKiroAccount API not available')
    }
    await window.go.main.App.UpdateKiroAccount(accountData)
    showSuccess(t('kiro.accountUpdated'))

    // 刷新列表
    await loadKiroAccounts()
    renderKiroAccountCards()
  } catch (error) {
    console.error('Failed to update account:', error)
    showError(t('kiro.updateAccountFailed') + (error?.message || error))

    // 错误时也要刷新列表，以获取最新状态
    try {
      await loadKiroAccounts()
      renderKiroAccountCards()
    } catch (refreshError) {
      console.error('Failed to refresh account list after error:', refreshError)
    }
  }
}

// 删除账号（参数为 refreshToken）
export async function deleteKiroAccount(refreshToken) {
  try {
    if (!window.go?.main?.App?.DeleteKiroAccount) {
      throw new Error('DeleteKiroAccount API not available')
    }
    await window.go.main.App.DeleteKiroAccount(refreshToken)
    showSuccess(t('kiro.accountDeleted'))

    // 刷新列表
    await loadKiroAccounts()
    renderKiroAccountCards()
  } catch (error) {
    console.error('Failed to delete account:', error)
    showError(t('kiro.deleteAccountFailed') + (error?.message || error))

    // 错误时也要刷新列表，以获取最新状态
    try {
      await loadKiroAccounts()
      renderKiroAccountCards()
    } catch (refreshError) {
      console.error('Failed to refresh account list after error:', refreshError)
    }
  }
}

// 测试账号（参数为 refreshToken）
export async function testKiroAccount(refreshToken) {
  try {
    if (!window.go?.main?.App?.TestKiroAccount) {
      throw new Error('TestKiroAccount API not available')
    }
    const result = await window.go.main.App.TestKiroAccount(refreshToken)
    showSuccess(t('kiro.testSuccess'))

    // 刷新列表以更新状态
    await loadKiroAccounts()
    renderKiroAccountCards()
    return result
  } catch (error) {
    console.error('Failed to test account:', error)
    showError(t('kiro.testFailedPrefix') + (error?.message || error))

    // 错误时也要刷新列表，因为后端可能已更新账号状态
    try {
      await loadKiroAccounts()
      renderKiroAccountCards()
    } catch (refreshError) {
      console.error('Failed to refresh account list after error:', refreshError)
    }

    return null
  }
}

// 获取账号用量（参数为 refreshToken）
export async function fetchKiroAccountUsage(refreshToken) {
  try {
    const account = kiroAccounts.find((acc) => acc.refreshToken === refreshToken)
    if (!account) {
      throw new Error(t('kiro.accountNotFound'))
    }

    if (account.expiresAt) {
      const expiresAt = new Date(account.expiresAt)
      const now = new Date()
      if (!isNaN(expiresAt.getTime()) && expiresAt < now) {
        showError(t('kiro.tokenExpiredClickTest'))
        // 刷新列表，避免界面展示过期状态不一致
        await loadKiroAccounts()
        renderKiroAccountCards()
        return null
      }
    }

    // 调用后端 API（会将用量写回 kiro.json）
    if (!window.go?.main?.App?.GetKiroAccountUsage) {
      throw new Error('GetKiroAccountUsage API not available')
    }
    const result = await window.go.main.App.GetKiroAccountUsage(refreshToken)

    // 刷新列表以更新用量信息
    await loadKiroAccounts()
    renderKiroAccountCards()
    return result
  } catch (error) {
    console.error('Failed to fetch account usage:', error)
    showError(t('kiro.usageFailedPrefix') + (error?.message || error))

    // 错误时也要刷新列表，因为后端可能已更新账号状态（如封禁、耗尽等）
    try {
      await loadKiroAccounts()
      renderKiroAccountCards()
    } catch (refreshError) {
      console.error('Failed to refresh account list after error:', refreshError)
    }

    return null
  }
}

// 获取状态徽章样式
function getStatusBadgeClass(status) {
  switch (status) {
    case 'valid':
      return 'status-valid'
    case 'banned':
      return 'status-banned'
    case 'exhausted':
      return 'status-exhausted'
    default:
      return 'status-unknown'
  }
}

// 获取状态文本
function getStatusText(status) {
  switch (status) {
    case 'valid':
      return t('kiro.statusValid')
    case 'banned':
      return t('kiro.statusBanned')
    case 'exhausted':
      return t('kiro.statusExhausted')
    default:
      return t('kiro.statusUnknown')
  }
}

// 获取认证方式标签
function getAuthMethodLabel(account) {
  if (account.provider) {
    return account.provider.charAt(0).toUpperCase() + account.provider.slice(1)
  }
  return account.authMethod
}

// 格式化用量数字
function formatUsageNumber(value) {
  const num = Number(value)
  if (!Number.isFinite(num)) return '0'
  return num.toLocaleString(undefined, { maximumFractionDigits: 2 })
}

// 截断 refreshToken 用于显示
function truncateRefreshToken(token) {
  if (!token || token.length <= 16) return token || ''
  return token.substring(0, 8) + '...' + token.substring(token.length - 8)
}

// 格式化日期显示
function formatDate(dateString) {
  if (!dateString) return ''
  const date = new Date(dateString)
  if (isNaN(date.getTime())) return ''
  return date.toLocaleDateString('zh-CN', { year: 'numeric', month: '2-digit', day: '2-digit' })
}

// 计算 token 剩余时间
function getTokenExpireInfo(expiresAt) {
  if (!expiresAt) return { text: '', isExpired: false }

  const expiresDate = new Date(expiresAt)
  const now = new Date()

  if (isNaN(expiresDate.getTime())) return { text: '', isExpired: false }

  const diffMs = expiresDate - now

  if (diffMs <= 0) {
    return { text: t('kiro.tokenExpired'), isExpired: true }
  }

  const diffMinutes = Math.floor(diffMs / (1000 * 60))
  const diffHours = Math.floor(diffMs / (1000 * 60 * 60))
  const diffDays = Math.floor(diffMs / (1000 * 60 * 60 * 24))

  if (diffDays > 0) {
    return { text: `${diffDays} ${t('kiro.days')}`, isExpired: false }
  } else if (diffHours > 0) {
    return { text: `${diffHours} ${t('kiro.hours')}`, isExpired: false }
  } else {
    return { text: `${diffMinutes} ${t('kiro.minutes')}`, isExpired: false }
  }
}

// 渲染单个账号卡片
function renderAccountCard(account) {
  const isActive = account.refreshToken === activeRefreshToken
  const authLabel = getAuthMethodLabel(account)

  const usagePercent = account.usagePct || 0
  const currentUsage = formatUsageNumber(account.currentUsage || 0)
  const usageLimit = formatUsageNumber(account.usageLimit || 0)

  const encodedToken = btoa(account.refreshToken)

  // 创建图标
  const powerIcon = createIcon('power', { size: 14 })
  const copyIcon = createIcon('copy', { size: 14 })
  const eyeIcon = createIcon('eye', { size: 14 })
  const editIcon = createIcon('edit', { size: 14 })
  const trashIcon = createIcon('trash', { size: 14 })
  const refreshIcon = createIcon('refreshCw', { size: 14 })
  const batteryIcon = createIcon('battery', { size: 14 })

  const statusBadgeClass = getStatusBadgeClass(account.status)
  const statusText = getStatusText(account.status)

  // 计算 token 剩余时间
  const expireInfo = getTokenExpireInfo(account.expiresAt)

  // 检查账号是否可以被激活（非当前激活 && 状态不是 banned）
  const canActivate = !isActive && account.status !== 'banned'

  return `
        <div class="kiro-account-card ${isActive ? 'active' : ''} ${account.status === 'banned' ? 'banned' : ''}" data-refresh-token="${encodedToken}">
            <div class="kiro-card-header" style="align-items: center;">
                <span class="kiro-account-email" style="flex: 1; min-width: 0; font-weight: 600; font-size: 13px; color: var(--text-primary); overflow: hidden; text-overflow: ellipsis; white-space: nowrap; margin-right: 10px;" title="${escapeHTML(account.email || '')}">${escapeHTML(account.email || '')}</span>
                <span class="kiro-status-badge ${statusBadgeClass}" style="margin-left: auto; flex-shrink: 0;">${statusText}</span>
            </div>
            <div class="kiro-header-tags" style="display: flex; align-items: center;">
                ${account.subscriptionTitle ? `<span class="kiro-tag kiro-tag-plan">${escapeHTML(account.subscriptionTitle)}</span>` : ''}
                <span class="kiro-tag kiro-tag-auth">${escapeHTML(authLabel)}</span>
                ${isActive ? `<span class="kiro-badge-active" style="margin-left: auto;">${t('kiro.currentActive')}</span>` : ''}
            </div>
            <div class="kiro-card-body">
                <div class="kiro-progress-section">
                    <div class="kiro-progress-track">
                        <div class="kiro-progress-fill" style="width: ${Math.min(usagePercent, 100)}%"></div>
                    </div>
                    <span class="kiro-progress-text">${usagePercent.toFixed(0)}%</span>
                </div>
                <div class="kiro-usage-meta" style="display: flex; justify-content: space-between; align-items: center;">
                    <span class="kiro-usage-nums">${currentUsage} / ${usageLimit}</span>
                    ${account.createdAt ? `<span class="kiro-added-date" style="font-size: 11px; color: var(--text-tertiary);">${formatDate(account.createdAt)}</span>` : ''}
                </div>
            </div>
            <div class="kiro-card-footer">
                <div class="kiro-expire-info ${expireInfo.isExpired ? 'expired' : ''}">
                    ${expireInfo.text ? `Token ${expireInfo.text}` : ''}
                </div>
                <div class="kiro-card-actions">
                    ${canActivate ? `<button class="kiro-icon-btn primary" onclick="setActiveKiroAccountFromCard('${encodedToken}')" title="${t('kiro.activate')}">${powerIcon}</button>` : ''}
                    <button class="kiro-icon-btn" onclick="testKiroAccountFromCard('${encodedToken}')" title="${t('kiro.test')}">${refreshIcon}</button>
                    <button class="kiro-icon-btn" onclick="fetchKiroAccountUsageFromCard('${encodedToken}')" title="${t('kiro.usage')}">${batteryIcon}</button>
                    <button class="kiro-icon-btn" onclick="copyKiroAccountFromCard('${encodedToken}')" title="${t('kiro.copy')}">${copyIcon}</button>
                    <button class="kiro-icon-btn" onclick="editKiroAccountFromCard('${encodedToken}')" title="${t('kiro.edit')}">${editIcon}</button>
                    <button class="kiro-icon-btn" onclick="viewKiroAccountFromCard('${encodedToken}')" title="${t('kiro.view')}">${eyeIcon}</button>
                    <button class="kiro-icon-btn kiro-icon-btn-danger" onclick="deleteKiroAccountFromCard('${encodedToken}')" title="${t('kiro.deleteAccount')}">${trashIcon}</button>
                </div>
            </div>
        </div>`
}

// 渲染添加账号卡片
function renderAddAccountCard() {
  return `
        <div class="kiro-account-card kiro-add-card" onclick="showAddKiroAccountOptions()">
            <div class="kiro-add-card-content">
                <div class="kiro-add-icon">${createIcon('plus', { size: 32 })}</div>
                <div class="kiro-add-text">${t('kiro.addAccount')}</div>
            </div>
        </div>`
}

// 渲染账号卡片列表
export function renderKiroAccountCards() {
  const container = document.getElementById('kiroAccountsGrid')
  if (!container) return

  if (kiroAccounts.length === 0) {
    container.innerHTML = `
            <div class="kiro-no-accounts">
                <p>${t('kiro.noAccounts')}</p>
            </div>`
    return
  }

  const cardsHtml = kiroAccounts.map((account) => renderAccountCard(account)).join('')
  container.innerHTML = cardsHtml
}

// 显示账号管理弹窗
export async function showKiroAccountsModal() {
  await loadKiroAccounts()

  const modal = document.getElementById('kiroAccountsModal')
  if (modal) {
    modal.classList.add('active')
    renderKiroAccountCards()
  }
}

// 关闭账号管理弹窗
export function closeKiroAccountsModal() {
  const modal = document.getElementById('kiroAccountsModal')
  if (modal) {
    modal.classList.remove('active')
  }
}

// 切换添加账号下拉菜单
export function toggleKiroAddAccountDropdown() {
  const menu = document.getElementById('kiroAddAccountMenu')
  if (menu) {
    const isVisible = menu.style.display !== 'none'
    menu.style.display = isVisible ? 'none' : 'block'
  }
}

// 隐藏添加账号下拉菜单
export function hideKiroAddAccountDropdown() {
  const menu = document.getElementById('kiroAddAccountMenu')
  if (menu) {
    menu.style.display = 'none'
  }
}

// 显示添加账号选项（保留兼容性）
export function showAddKiroAccountOptions() {
  toggleKiroAddAccountDropdown()
}

// 隐藏添加账号选项（保留兼容性）
export function hideAddKiroAccountOptions() {
  hideKiroAddAccountDropdown()
}

// 解码 base64 编码的 refreshToken
function decodeRefreshToken(encoded) {
  try {
    return atob(encoded)
  } catch (e) {
    console.error('Failed to decode refreshToken:', e)
    return encoded
  }
}

// 获取实际按钮元素（处理点击 SVG 内部的情况）
function getButtonElement(target) {
  if (!target) return null
  // 如果点击的是按钮内的 SVG 或其子元素，向上查找按钮
  let btn = target
  while (btn && !btn.classList.contains('kiro-icon-btn')) {
    btn = btn.parentElement
    if (!btn || btn.tagName === 'BODY') return null
  }
  return btn
}

// 全局函数绑定（供 HTML onclick 调用）
window.setActiveKiroAccountFromCard = async function (encodedToken) {
  const btn = getButtonElement(event?.target)
  if (btn?.disabled) return

  const originalHTML = btn?.innerHTML
  if (btn) {
    btn.disabled = true
    btn.classList.add('loading')
  }
  try {
    const refreshToken = decodeRefreshToken(encodedToken)
    await setActiveKiroAccount(refreshToken)
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

window.testKiroAccountFromCard = async function (encodedToken) {
  const btn = getButtonElement(event?.target)
  if (btn?.disabled) return

  const originalHTML = btn?.innerHTML
  if (btn) {
    btn.disabled = true
    btn.classList.add('loading')
  }
  try {
    const refreshToken = decodeRefreshToken(encodedToken)
    await testKiroAccount(refreshToken)
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

window.fetchKiroAccountUsageFromCard = async function (encodedToken) {
  const btn = getButtonElement(event?.target)
  if (btn?.disabled) return

  const originalHTML = btn?.innerHTML
  if (btn) {
    btn.disabled = true
    btn.classList.add('loading')
  }
  try {
    const refreshToken = decodeRefreshToken(encodedToken)
    await fetchKiroAccountUsage(refreshToken)
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

window.deleteKiroAccountFromCard = async function (encodedToken) {
  const btn = getButtonElement(event?.target)
  if (btn?.disabled) return

  const confirmed = await confirmDialog(t('kiro.deleteConfirm'), { danger: true })
  if (!confirmed) {
    return
  }

  const originalHTML = btn?.innerHTML
  if (btn) {
    btn.disabled = true
    btn.classList.add('loading')
  }
  try {
    const refreshToken = decodeRefreshToken(encodedToken)
    await deleteKiroAccount(refreshToken)
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

// 复制账号信息到剪贴板
window.copyKiroAccountFromCard = async function (encodedToken) {
  const btn = getButtonElement(event?.target)
  if (btn?.disabled) return

  const originalHTML = btn?.innerHTML
  if (btn) {
    btn.disabled = true
    btn.classList.add('loading')
  }

  try {
    const refreshToken = decodeRefreshToken(encodedToken)
    const account = kiroAccounts.find((acc) => acc.refreshToken === refreshToken)
    if (!account) {
      showError(t('kiro.accountNotFound'))
      return
    }

    // 只复制指定的非空字段
    const fields = [
      'refreshToken',
      'accessToken',
      'expiresAt',
      'region',
      'profileArn',
      'authMethod',
      'provider',
      'clientId',
      'clientSecret',
    ]
    const copyData = {}
    fields.forEach((field) => {
      if (account[field]) {
        copyData[field] = account[field]
      }
    })

    await navigator.clipboard.writeText(JSON.stringify(copyData, null, 2))
    showSuccess(t('kiro.copySuccess'))
  } catch (error) {
    console.error('Failed to copy:', error)
    showError(t('kiro.copyFailed') + (error?.message || error))
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

// 查看账号详细信息
window.viewKiroAccountFromCard = function (encodedToken) {
  const refreshToken = decodeRefreshToken(encodedToken)
  const account = kiroAccounts.find((acc) => acc.refreshToken === refreshToken)
  if (!account) {
    showError(t('kiro.accountNotFound'))
    return
  }

  // 创建详情弹窗
  const modal = document.createElement('div')
  modal.className = 'modal active'
  modal.innerHTML = `
        <div class="modal-content">
            <div class="modal-header">
                <h2>${t('kiro.accountDetails')}</h2>
                <button class="modal-close" onclick="this.closest('.modal').remove()">&times;</button>
            </div>
            <div class="modal-body">
                <div class="kiro-detail-grid">
                    ${renderDetailField(t('kiro.authMethod'), account.authMethod || '-')}
                    ${account.provider ? renderDetailField(t('kiro.provider'), account.provider) : ''}
                    ${renderDetailField(t('kiro.refreshToken'), truncateRefreshToken(account.refreshToken))}
                    ${account.accessToken ? renderDetailField(t('kiro.accessToken'), truncateToken(account.accessToken)) : ''}
                    ${account.expiresAt ? renderDetailField(t('kiro.expiresAt'), formatDate(account.expiresAt)) : ''}
                    ${account.region ? renderDetailField(t('kiro.region'), account.region) : ''}
                    ${account.profileArn ? renderDetailField(t('kiro.profileArn'), account.profileArn) : ''}
                    ${account.clientId ? renderDetailField(t('kiro.clientId'), account.clientId) : ''}
                    ${account.clientSecret ? renderDetailField(t('kiro.clientSecret'), '••••••••') : ''}
                    ${account.subscriptionTitle ? renderDetailField(t('kiro.subscription'), account.subscriptionTitle) : ''}
                    ${renderDetailField(t('kiro.status'), getStatusText(account.status))}
                    ${renderDetailField(t('kiro.usage'), `${formatUsageNumber(account.currentUsage || 0)} / ${formatUsageNumber(account.usageLimit || 0)} (${(account.usagePct || 0).toFixed(1)}%)`)}
                    ${account.createdAt ? renderDetailField(t('kiro.createdAt'), formatDate(account.createdAt)) : ''}
                    ${account.updatedAt ? renderDetailField(t('kiro.updatedAt'), formatDate(account.updatedAt)) : ''}
                </div>
            </div>
            <div class="modal-footer">
                <button class="btn btn-secondary" onclick="this.closest('.modal').remove()">${t('common.close')}</button>
            </div>
        </div>
    `
  document.body.appendChild(modal)
}

// 编辑账号 — 记录当前编辑的账号对象
let editingRefreshToken = ''
let editingAccount = null

window.editKiroAccountFromCard = function (encodedToken) {
  const refreshToken = decodeRefreshToken(encodedToken)
  const account = kiroAccounts.find((acc) => acc.refreshToken === refreshToken)
  if (!account) {
    showError(t('kiro.accountNotFound'))
    return
  }

  editingRefreshToken = refreshToken
  editingAccount = account // 保存完整的账号对象

  // 填充字段
  const set = (id, v) => { const el = document.getElementById(id); if (el) el.value = v || '' }
  set('editKiroRefreshToken', account.refreshToken)
  set('editKiroRegion', account.region || 'us-east-1')
  set('editKiroAuthMethod', account.authMethod || 'social')
  set('editKiroProvider', account.provider || '')
  set('editKiroProfileArn', account.profileArn)
  set('editKiroClientId', account.clientId)
  set('editKiroClientSecret', account.clientSecret)
  set('editKiroProxyUrl', account.proxyUrl)
  set('editKiroUserAgent', account.userAgent)
  set('editKiroVersion', account.version)
  set('editKiroWeight', account.weight > 0 ? String(account.weight) : '')
  set('editKiroMachineId', account.machineId)

  // 同步自定义下拉组件显示
  if (window.syncEditKiroAuthMethodDisplay) {
    window.syncEditKiroAuthMethodDisplay()
  }

  // 切换 social/idc 区域可见性
  syncEditAuthMethodFields()

  const modal = document.getElementById('kiroAccountEditModal')
  if (modal) modal.classList.add('active')
}

function syncEditAuthMethodFields() {
  const method = (document.getElementById('editKiroAuthMethod')?.value || 'social').toLowerCase()
  const isSocial = method !== 'idc'
  const show = (id, visible) => { const el = document.getElementById(id); if (el) el.style.display = visible ? '' : 'none' }
  show('editKiroProfileArnGroup', isSocial)
  show('editKiroClientIdGroup', !isSocial)
  show('editKiroClientSecretGroup', !isSocial)
}

window.onEditKiroAuthMethodChange = syncEditAuthMethodFields

window.toggleEditKiroClientSecretVisibility = function () {
  const input = document.getElementById('editKiroClientSecret')
  if (!input) return
  input.type = input.type === 'password' ? 'text' : 'password'
}

window.closeKiroAccountEditModal = function () {
  const modal = document.getElementById('kiroAccountEditModal')
  if (modal) modal.classList.remove('active')
  editingRefreshToken = ''
  editingAccount = null
}

window.saveKiroAccountEdit = async function () {
  if (!editingRefreshToken || !editingAccount) return

  const val = (id) => (document.getElementById(id)?.value || '').trim()

  // 基于原始账号对象创建 DTO，只更新表单中的字段
  const dto = {
    ...editingAccount, // 保留所有原始字段
    // 更新表单中编辑的字段
    refreshToken: editingRefreshToken,
    region: val('editKiroRegion') || 'us-east-1',
    authMethod: val('editKiroAuthMethod') || 'social',
    provider: val('editKiroProvider'),
    profileArn: val('editKiroProfileArn'),
    clientId: val('editKiroClientId'),
    clientSecret: val('editKiroClientSecret'),
    proxyUrl: val('editKiroProxyUrl'),
    userAgent: val('editKiroUserAgent'),
    version: val('editKiroVersion'),
    weight: parseInt(val('editKiroWeight'), 10) || 0,
  }

  try {
    await updateKiroAccount(dto)
    closeKiroAccountEditModal()
  } catch (error) {
    console.error('Failed to save account edit:', error)
    showError(t('kiro.updateAccountFailed') + (error?.message || error))
  }
}

// 渲染详情字段
function renderDetailField(label, value) {
  return `
        <div class="kiro-detail-field">
            <div class="kiro-detail-label">${escapeHTML(label)}</div>
            <div class="kiro-detail-value">${escapeHTML(value)}</div>
        </div>
    `
}

// 截断 token 用于显示
function truncateToken(token) {
  if (!token || token.length <= 32) return token || ''
  return token.substring(0, 16) + '...' + token.substring(token.length - 16)
}

window.showAddKiroAccountOptions = showAddKiroAccountOptions
window.showKiroAccountsModal = showKiroAccountsModal
window.closeKiroAccountsModal = closeKiroAccountsModal

let jsonImportInProgress = false

function normalizeAuthMethod(value) {
  const v = String(value || '')
    .trim()
    .toLowerCase()
  if (!v) return ''
  if (v === 'idc') return 'idc'
  if (v === 'social') return 'social'
  return ''
}

function normalizeSocialProvider(provider) {
  const raw = String(provider || '').trim()
  if (!raw) return ''
  const v = raw.toLowerCase()
  if (v === 'google') return 'Google'
  if (v === 'github' || v === 'git-hub' || v === 'git_hub') return 'Github'
  return raw
}

function looksLikeBuilderIDCProvider(provider) {
  const v = String(provider || '').trim().toLowerCase()
  return v === 'builderid' || v === 'builder-id' || v === 'builder_id' || v === 'builder'
}

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

function buildImportDTOs(jsonText) {
  const errors = []
  let payload
  try {
    payload = JSON.parse(jsonText)
  } catch (e) {
    return { dtos: [], errors: [t('kiro.jsonImportParseFailed') + (e?.message || e)] }
  }

  const items = parseImportItems(payload)
  const dtos = []
  const seen = new Set()

  items.forEach((item, index) => {
    const refreshToken = getStringField(item, ['refreshToken', 'refresh_token', 'RefreshToken'])
    if (!refreshToken) {
      errors.push(`#${index + 1}: missing refreshToken`)
      return
    }
    if (!refreshToken.startsWith('aor')) {
      errors.push(`#${index + 1}: refreshToken should start with "aor"`)
      return
    }
    if (seen.has(refreshToken)) {
      errors.push(`#${index + 1}: duplicated refreshToken`)
      return
    }
    seen.add(refreshToken)

    const clientId = getStringField(item, ['clientId', 'client_id', 'ClientId'])
    const clientSecret = getStringField(item, ['clientSecret', 'client_secret', 'ClientSecret'])
    const explicitAuthMethod = normalizeAuthMethod(getStringField(item, ['authMethod', 'auth_method', 'AuthMethod']))

    let provider = getStringField(item, ['provider', 'Provider'])
    const inferredAuthMethod = clientId && clientSecret ? 'idc' : 'social'
    // 兼容外部导出的字段：有些导出会把 BuilderId 账号标记为 social，但实际上携带 clientId/clientSecret
    const authMethod =
      explicitAuthMethod === 'idc'
        ? 'idc'
        : explicitAuthMethod === 'social' && (clientId && clientSecret ? true : looksLikeBuilderIDCProvider(provider))
          ? 'idc'
          : explicitAuthMethod || inferredAuthMethod

    if (authMethod === 'idc') {
      if (!clientId || !clientSecret) {
        errors.push(`#${index + 1}: IdC account requires both clientId and clientSecret`)
        return
      }
    }

    if (authMethod === 'social') {
      provider = normalizeSocialProvider(provider) || 'Google'
      if (!['Google', 'Github'].includes(provider)) {
        errors.push(`#${index + 1}: invalid provider for social account (Google/Github)`)
        return
      }
    }

    const region = getStringField(item, ['region', 'Region'])
    const accessToken = getStringField(item, ['accessToken', 'access_token', 'AccessToken'])
    const profileArn = getStringField(item, ['profileArn', 'profile_arn', 'ProfileArn'])
    const expiresAt = getStringField(item, ['expiresAt', 'expires_at', 'ExpiresAt'])
    const email = getStringField(item, ['email', 'Email'])

    const dto = {
      refreshToken,
      authMethod,
      provider,
      clientId,
      clientSecret,
      region,
      accessToken,
      profileArn,
      expiresAt,
      email,
    }

    Object.keys(dto).forEach((k) => {
      if (!dto[k]) delete dto[k]
    })

    dtos.push(dto)
  })

  return { dtos, errors }
}

window.showJsonImportDialog = function () {
  hideKiroAddAccountDropdown()
  const existing = document.getElementById('kiroJsonImportModal')
  if (existing) existing.remove()

  const modal = document.createElement('div')
  modal.className = 'modal active'
  modal.id = 'kiroJsonImportModal'

  modal.innerHTML = `
        <div class="modal-content" style="max-width: 600px; display: flex; flex-direction: column;">
            <div class="modal-header">
                <h3>${t('kiro.jsonImportTitle')}</h3>
                <button class="btn-icon" onclick="closeJsonImportDialog()">${createIcon('close', { size: 20 })}</button>
            </div>
            <div class="modal-body" style="padding: 20px; flex: 1;">
                <textarea id="jsonImportTextarea" 
                    placeholder="${t('kiro.jsonImportPlaceholder')}" 
                    style="width: 100%; height: 300px; font-family: monospace; padding: 10px; border: 1px solid var(--border-color); border-radius: 4px; resize: vertical; background: var(--bg-primary); color: var(--text-primary);"></textarea>
            </div>
            <div class="modal-footer">
                <button class="btn" onclick="closeJsonImportDialog()">${t('common.cancel') || 'Cancel'}</button>
                <button class="btn btn-primary" onclick="executeJsonImport()">${t('kiro.import')}</button>
            </div>
        </div>
    `

  document.body.appendChild(modal)

  setTimeout(() => {
    const textarea = document.getElementById('jsonImportTextarea')
    if (textarea) textarea.focus()
  }, 100)
}

window.closeJsonImportDialog = function () {
  const modal = document.getElementById('kiroJsonImportModal')
  if (modal) modal.remove()
}

window.executeJsonImport = async function () {
  const textarea = document.getElementById('jsonImportTextarea')
  if (!textarea) return

  const jsonText = textarea.value.trim()
  if (!jsonText) {
    showError(t('kiro.jsonImportNoValidAccounts') || 'Please paste JSON content')
    return
  }

  closeJsonImportDialog()
  await processJsonImport(jsonText)
}

async function processJsonImport(jsonText) {
  if (jsonImportInProgress) return
  jsonImportInProgress = true

  try {
    if (!window.go?.main?.App?.AddKiroAccount) {
      throw new Error('AddKiroAccount API not available')
    }

    const { dtos, errors } = buildImportDTOs(jsonText)
    if (!dtos.length) {
      if (errors.length) {
        console.warn('JSON import validation errors:', errors)
        if (errors[0] && String(errors[0]).startsWith(t('kiro.jsonImportParseFailed'))) {
          showError(errors[0])
        } else {
          const max = 3
          const msg = errors.slice(0, max).join('\n') + (errors.length > max ? `\n...(${errors.length})` : '')
          showError(msg || t('kiro.jsonImportNoValidAccounts'))
        }
      } else {
        showError(t('kiro.jsonImportNoValidAccounts'))
      }
      return
    }

    if (errors.length) {
      console.warn('JSON import validation errors (skipped):', errors)
      const ok = await confirmDialog(
        `${t('kiro.jsonImportConfirmPrefix')}${dtos.length}${t('kiro.jsonImportConfirmSuffix')}\n` +
          `${t('kiro.jsonImportInvalidSkippedPrefix')}${errors.length}${t('kiro.jsonImportInvalidSkippedSuffix')}`
      )
      if (!ok) return
    } else {
      const ok = await confirmDialog(`${t('kiro.jsonImportConfirmPrefix')}${dtos.length}${t('kiro.jsonImportConfirmSuffix')}`)
      if (!ok) return
    }

    let successCount = 0
    let failedCount = 0
    const failures = []

    for (const dto of dtos) {
      try {
        await window.go.main.App.AddKiroAccount(dto)
        successCount++
      } catch (e) {
        failedCount++
        failures.push({ refreshToken: dto.refreshToken, error: e?.message || String(e) })
      }
    }

    await loadKiroAccounts()
    renderKiroAccountCards()

    showSuccess(
      `${t('kiro.jsonImportResultPrefix')}${successCount}${t('kiro.jsonImportResultMiddle')}${failedCount}${t('kiro.jsonImportResultSuffix')}`
    )

    if (failures.length) {
      console.warn('JSON import failures:', failures)
    }
  } catch (error) {
    console.error('Failed to import accounts from JSON:', error)
    showError(t('kiro.jsonImportFailedPrefix') + (error?.message || error))
  } finally {
    jsonImportInProgress = false
  }
}

window.importKiroAccountsFromJson = function () {
  showJsonImportDialog()
}

// 从账号管理界面启动登录流程
// 使用 Set 跟踪正在进行的登录流程
const activeLogins = new Set()

window.startAddAccountLogin = async function (authMethod, provider) {
  const loginKey = `${authMethod}-${provider}`

  // 防止重复点击
  if (activeLogins.has(loginKey)) {
    console.log(`Login already in progress for ${loginKey}`)
    return
  }

  activeLogins.add(loginKey)
  hideKiroAddAccountDropdown()

  try {
    // 根据认证方式调用不同的登录流程
    if (authMethod === 'social') {
      // 使用 kiro.js 中的 Social 登录，传入 fromAccountManagement=true
      if (window.startSocialLogin) {
        await window.startSocialLogin(provider, true)
      }
    } else if (authMethod === 'idc') {
      // 账号管理界面发起的 IdC 登录：记录 provider 以便登录完成后写入 kiro.json
      // - AWS Builder ID -> BuilderId
      // - IAM Identity Center -> Enterprise
      const accountProvider = provider === 'org' ? 'Enterprise' : 'BuilderId'
      window.__kiroIdcAccountAddContext = { enabled: true, provider: accountProvider }

      if (provider === 'org') {
        // 组织身份登录
        if (window.startIdcOrgLogin) {
          window.startIdcOrgLogin()
        }
      } else {
        // Builder ID 登录
        if (window.startIdcDeviceFlowLogin) {
          window.startIdcDeviceFlowLogin()
        }
      }
    }
  } finally {
    // 延迟 1 秒后清除标记，防止误操作
    setTimeout(() => {
      activeLogins.delete(loginKey)
    }, 1000)
  }
}

// 批量删除对话框
window.showBulkDeleteDialog = function () {
  if (!kiroAccounts || kiroAccounts.length === 0) {
    showError(t('kiro.noAccounts') || 'No accounts')
    return
  }

  const modal = document.createElement('div')
  modal.className = 'modal active'
  modal.id = 'kiroBulkDeleteModal'

  const listHtml = kiroAccounts
    .map((acc) => {
      const isBanned = acc.status === 'banned'
      const label = acc.email || truncateRefreshToken(acc.refreshToken)
      const statusText = getStatusText(acc.status)
      const statusClass = getStatusBadgeClass(acc.status)
      const encodedToken = btoa(acc.refreshToken)

      return `
            <div class="kiro-bulk-item" style="display: flex; align-items: center; padding: 10px; border-bottom: 1px solid var(--border-light); hover: background-color: var(--bg-secondary);">
                <input type="checkbox" class="kiro-bulk-checkbox" value="${encodedToken}" data-banned="${isBanned}" id="chk-${encodedToken}" style="margin-right: 12px; transform: scale(1.2);">
                <label for="chk-${encodedToken}" style="flex: 1; cursor: pointer; display: flex; justify-content: space-between; align-items: center;">
                    <span style="font-weight: 500; font-size: 13px;">${label}</span>
                    <span class="kiro-status-badge ${statusClass}">${statusText}</span>
                </label>
            </div>
        `
    })
    .join('')

  modal.innerHTML = `
        <div class="modal-content" style="max-width: 500px; display: flex; flex-direction: column; max-height: 85vh;">
            <div class="modal-header">
                <h2>${t('kiro.bulkDelete') || '批量删除账号'}</h2>
                <button class="modal-close" onclick="this.closest('.modal').remove()">&times;</button>
            </div>
            <div class="modal-body" style="flex: 1; overflow: hidden; display: flex; flex-direction: column; padding: 0;">
                <div class="kiro-bulk-actions" style="padding: 15px; border-bottom: 1px solid var(--border-light); display: flex; gap: 10px; background: var(--bg-secondary);">
                    <button class="btn btn-sm btn-secondary" onclick="toggleBulkDeleteAll(true)">${t('kiro.selectAll') || '全选'}</button>
                    <button class="btn btn-sm btn-secondary" onclick="toggleBulkDeleteAll(false)">${t('kiro.deselectAll') || '全不选'}</button>
                    <button class="btn btn-sm btn-warning" onclick="selectBulkDeleteBanned()">
                        ${t('kiro.selectBanned') || '全选封禁项'}
                    </button>
                </div>
                <div class="kiro-bulk-list" style="overflow-y: auto; padding: 0 15px;">
                    ${listHtml}
                </div>
            </div>
            <div class="modal-footer" style="padding: 15px; border-top: 1px solid var(--border-light);">
                <button class="btn btn-secondary" onclick="this.closest('.modal').remove()">${t('common.cancel')}</button>
                <button class="btn btn-danger" onclick="executeBulkDelete()">${t('common.delete')}</button>
            </div>
        </div>
    `

  document.body.appendChild(modal)
}

// 批量全选/全不选
window.toggleBulkDeleteAll = function (checked) {
  const checkboxes = document.querySelectorAll('#kiroBulkDeleteModal .kiro-bulk-checkbox')
  checkboxes.forEach((cb) => (cb.checked = checked))
}

// 选中所有封禁账号
window.selectBulkDeleteBanned = function () {
  const checkboxes = document.querySelectorAll('#kiroBulkDeleteModal .kiro-bulk-checkbox')
  checkboxes.forEach((cb) => {
    if (cb.dataset.banned === 'true') {
      cb.checked = true
    }
  })
}

// 执行批量删除
window.executeBulkDelete = async function () {
  const checkboxes = document.querySelectorAll('#kiroBulkDeleteModal .kiro-bulk-checkbox:checked')
  if (checkboxes.length === 0) {
    showError(t('kiro.noAccountSelected') || '请至少选择一个账号')
    return
  }

  const confirmed = await confirmDialog(`${t('kiro.bulkDeleteConfirm') || '确定要删除选中的账号吗？'} (${checkboxes.length})`, {
    danger: true,
  })
  if (!confirmed) {
    return
  }

  const modal = document.getElementById('kiroBulkDeleteModal')
  const deleteBtn = modal.querySelector('.btn-danger')
  const originalText = deleteBtn.innerHTML

  if (deleteBtn) {
    deleteBtn.disabled = true
    deleteBtn.innerHTML = 'Deleting...'
  }

  try {
    let successCount = 0
    const total = checkboxes.length

    for (const cb of checkboxes) {
      try {
        const refreshToken = atob(cb.value)
        if (window.go?.main?.App?.DeleteKiroAccount) {
          await window.go.main.App.DeleteKiroAccount(refreshToken)
          successCount++
        }
      } catch (e) {
        console.error('Delete failed for token', cb.value, e)
      }
    }

    showSuccess(`${t('kiro.bulkDeleteSuccess') || '删除成功'} (${successCount}/${total})`)

    modal.remove()
    await loadKiroAccounts()
    renderKiroAccountCards()
  } catch (e) {
    console.error('Bulk delete error', e)
    showError(t('kiro.bulkDeleteFailed') || '批量删除失败')
  } finally {
    if (document.body.contains(modal) && deleteBtn) {
      deleteBtn.disabled = false
      deleteBtn.innerHTML = originalText
    }
  }
}
