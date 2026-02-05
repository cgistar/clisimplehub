/**
 * Kiro 多账号管理模块
 * refreshToken 作为账号主键
 */
import { showError, showSuccess } from './utils.js'
import { t } from '../i18n/index.js'
import { createIcon } from './icons.js'

// 状态
let kiroAccounts = []
let activeRefreshToken = null

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

    // 如果是激活账号，使用 fetchKiroUsage() 获取数据
    if (refreshToken === activeRefreshToken) {
      // 导入 kiro.js 中的函数
      const { fetchKiroUsageForAccount } = await import('./kiro.js')
      const result = await fetchKiroUsageForAccount(account)

      if (result) {
        // 更新账号数据
        account.subscriptionTitle = result.subscriptionTitle
        account.usageLimit = result.usageLimit
        account.currentUsage = result.currentUsage
        account.balance = result.balance
        account.usagePct = result.usagePct

        // 刷新列表以更新用量信息
        await loadKiroAccounts()
        renderKiroAccountCards()
      }
      return result
    }

    // 非激活账号：检查 expiresAt 是否过期
    if (account.expiresAt) {
      const expiresAt = new Date(account.expiresAt)
      const now = new Date()
      if (expiresAt < now) {
        showError(t('kiro.tokenExpired'))
        // 即使 token 过期也刷新列表，以防后端已更新状态
        await loadKiroAccounts()
        renderKiroAccountCards()
        return null
      }
    }

    // 未过期，调用后端 API
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
  if (account.authMethod === 'idc') {
    return 'IdC'
  }
  if (account.provider) {
    return account.provider.charAt(0).toUpperCase() + account.provider.slice(1)
  }
  return 'Social'
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
            <div class="kiro-card-header">
                ${isActive ? `<span class="kiro-badge-active">${t('kiro.currentActive')}</span>` : ''}
                <span class="kiro-status-badge ${statusBadgeClass}" style="margin-left: auto;">${statusText}</span>
            </div>
            <div class="kiro-header-tags">
                ${account.subscriptionTitle ? `<span class="kiro-tag kiro-tag-plan">${account.subscriptionTitle}</span>` : ''}
                <span class="kiro-tag kiro-tag-auth">${authLabel}</span>
            </div>
            <div class="kiro-card-body">
                ${account.createdAt ? `<div class="kiro-added-date">${t('kiro.createdAt')}: ${formatDate(account.createdAt)}</div>` : ''}
                <div class="kiro-progress-section">
                    <div class="kiro-progress-track">
                        <div class="kiro-progress-fill" style="width: ${Math.min(usagePercent, 100)}%"></div>
                    </div>
                    <span class="kiro-progress-text">${usagePercent.toFixed(0)}%</span>
                </div>
                <div class="kiro-usage-meta">
                    <span class="kiro-usage-nums">${currentUsage} / ${usageLimit}</span>
                </div>
            </div>
            <div class="kiro-card-footer">
                <div class="kiro-expire-info ${expireInfo.isExpired ? 'expired' : ''}">
                    ${expireInfo.text ? `⏱ ${expireInfo.text}` : ''}
                </div>
                <div class="kiro-card-actions">
                    ${canActivate ? `<button class="kiro-icon-btn primary" onclick="setActiveKiroAccountFromCard('${encodedToken}')" title="${t('kiro.activate')}">${powerIcon}</button>` : ''}
                    <button class="kiro-icon-btn" onclick="testKiroAccountFromCard('${encodedToken}')" title="${t('kiro.test')}">${refreshIcon}</button>
                    <button class="kiro-icon-btn" onclick="fetchKiroAccountUsageFromCard('${encodedToken}')" title="${t('kiro.usage')}">${batteryIcon}</button>
                    <button class="kiro-icon-btn" onclick="copyKiroAccountFromCard('${encodedToken}')" title="${t('kiro.copy')}">${copyIcon}</button>
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
                <div class="kiro-add-icon">➕</div>
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

  if (!confirm(t('kiro.deleteConfirm'))) {
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

// 渲染详情字段
function renderDetailField(label, value) {
  return `
        <div class="kiro-detail-field">
            <div class="kiro-detail-label">${label}</div>
            <div class="kiro-detail-value">${value}</div>
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
