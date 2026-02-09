/**
 * Grok 多账号管理模块
 * ssoToken 作为账号主键
 */
import { showError, showSuccess } from './utils.js'
import { t } from '../i18n/index.js'
import { createIcon } from './icons.js'
import { confirm as confirmDialog } from './confirm.js'

let grokAccounts = []
let activeSsoToken = null

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

export async function loadGrokAccounts() {
  try {
    if (!window.go?.main?.App?.GetGrokAccounts) {
      console.warn('GetGrokAccounts API not available')
      return
    }
    const result = await window.go.main.App.GetGrokAccounts()
    grokAccounts = result?.accounts || []
    activeSsoToken = result?.activeSsoToken || null
    return { accounts: grokAccounts, activeSsoToken }
  } catch (error) {
    console.error('Failed to load grok accounts:', error)
    showError(t('grok.loadAccountsFailed') + (error?.message || error))
    return null
  }
}

function getGrokStatusBadgeClass(status) {
  switch (status) {
    case 'valid': return 'status-valid'
    case 'invalid': return 'status-banned'
    case 'cooling': return 'status-exhausted'
    default: return 'status-unknown'
  }
}

function getGrokStatusText(status) {
  switch (status) {
    case 'valid': return t('grok.statusValid')
    case 'invalid': return t('grok.statusInvalid')
    case 'cooling': return t('grok.statusCooling')
    default: return t('grok.statusUnknown')
  }
}

function truncateSsoToken(token) {
  if (!token || token.length <= 16) return token || ''
  return token.substring(0, 8) + '...' + token.substring(token.length - 8)
}

function formatDate(dateString) {
  if (!dateString) return ''
  const date = new Date(dateString)
  if (isNaN(date.getTime())) return ''
  return date.toLocaleDateString('zh-CN', { year: 'numeric', month: '2-digit', day: '2-digit' })
}

function renderGrokAccountCard(account) {
  const isActive = account.ssoToken === activeSsoToken
  const quota = account.quota || 0
  const usage = account.currentUsage || 0
  const usagePct = quota > 0 ? Math.min((usage / quota) * 100, 100) : 0
  const tierLabel = account.tier === 'super' ? 'Super' : 'Basic'

  const encodedToken = btoa(account.ssoToken)
  const statusClass = getGrokStatusBadgeClass(account.status)
  const statusText = getGrokStatusText(account.status)
  const canActivate = !isActive && account.status !== 'invalid'

  const powerIcon = createIcon('power', { size: 14 })
  const copyIcon = createIcon('copy', { size: 14 })
  const refreshIcon = createIcon('refreshCw', { size: 14 })
  const trashIcon = createIcon('trash', { size: 14 })
  const editIcon = createIcon('edit', { size: 14 })

  return `
    <div class="kiro-account-card ${isActive ? 'active' : ''} ${account.status === 'invalid' ? 'banned' : ''}" data-sso-token="${encodedToken}">
      <div class="kiro-card-header" style="align-items: center;">
        <span class="kiro-account-email" style="flex: 1; min-width: 0; font-weight: 600; font-size: 13px; color: var(--text-primary); overflow: hidden; text-overflow: ellipsis; white-space: nowrap; margin-right: 10px;" title="${escapeHTML(account.ssoToken)}">${escapeHTML(truncateSsoToken(account.ssoToken))}</span>
        <span class="kiro-status-badge ${statusClass}" style="margin-left: auto; flex-shrink: 0;">${statusText}</span>
      </div>
      <div class="kiro-header-tags" style="display: flex; align-items: center;">
        <span class="kiro-tag kiro-tag-plan">${escapeHTML(tierLabel)}</span>
        ${account.isPlus ? '<span class="kiro-tag kiro-tag-auth">Plus</span>' : ''}
        ${isActive ? `<span class="kiro-badge-active" style="margin-left: auto;">${t('grok.currentActive')}</span>` : ''}
      </div>
      <div class="kiro-card-body">
        <div class="kiro-progress-section">
          <div class="kiro-progress-track">
            <div class="kiro-progress-fill" style="width: ${usagePct}%"></div>
          </div>
          <span class="kiro-progress-text">${usagePct.toFixed(0)}%</span>
        </div>
        <div class="kiro-usage-meta" style="display: flex; justify-content: space-between; align-items: center;">
          <span class="kiro-usage-nums">${usage} / ${quota}</span>
          ${account.createdAt ? `<span class="kiro-added-date" style="font-size: 11px; color: var(--text-tertiary);">${formatDate(account.createdAt)}</span>` : ''}
        </div>
      </div>
      <div class="kiro-card-footer">
        <div class="kiro-expire-info"></div>
        <div class="kiro-card-actions">
          ${canActivate ? `<button class="kiro-icon-btn primary" onclick="setActiveGrokAccountFromCard('${encodedToken}')" title="${t('grok.activate')}">${powerIcon}</button>` : ''}
          <button class="kiro-icon-btn" onclick="testGrokAccountFromCard('${encodedToken}')" title="${t('grok.test')}">${refreshIcon}</button>
          <button class="kiro-icon-btn" onclick="copyGrokAccountFromCard('${encodedToken}')" title="${t('grok.copy')}">${copyIcon}</button>
          <button class="kiro-icon-btn" onclick="editGrokAccountFromCard('${encodedToken}')" title="${t('grok.edit')}">${editIcon}</button>
          <button class="kiro-icon-btn kiro-icon-btn-danger" onclick="deleteGrokAccountFromCard('${encodedToken}')" title="${t('grok.deleteAccount')}">${trashIcon}</button>
        </div>
      </div>
    </div>`
}

export function renderGrokAccountCards() {
  const container = document.getElementById('grokAccountsGrid')
  if (!container) return
  if (grokAccounts.length === 0) {
    container.innerHTML = `<div class="kiro-no-accounts"><p>${t('grok.noAccounts')}</p></div>`
    return
  }
  container.innerHTML = grokAccounts.map((a) => renderGrokAccountCard(a)).join('')
}

function decodeSsoToken(encoded) {
  try { return atob(encoded) } catch (e) { return encoded }
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

async function withButtonLoading(fn) {
  const btn = getButtonElement(event?.target)
  if (btn?.disabled) return
  const orig = btn?.innerHTML
  if (btn) { btn.disabled = true; btn.classList.add('loading') }
  try { await fn() } finally {
    if (btn) { btn.disabled = false; btn.classList.remove('loading'); if (orig) btn.innerHTML = orig }
  }
}

window.setActiveGrokAccountFromCard = async (enc) => withButtonLoading(async () => {
  const token = decodeSsoToken(enc)
  try {
    await window.go.main.App.SetActiveGrokAccount(token)
    activeSsoToken = token
    showSuccess(t('grok.accountSwitched'))
  } catch (e) { showError(t('grok.switchAccountFailed') + (e?.message || e)) }
  await loadGrokAccounts(); renderGrokAccountCards()
})

window.testGrokAccountFromCard = async (enc) => withButtonLoading(async () => {
  const token = decodeSsoToken(enc)
  try {
    await window.go.main.App.TestGrokAccount(token)
    showSuccess(t('grok.testSuccess'))
  } catch (e) { showError(t('grok.testFailedPrefix') + (e?.message || e)) }
  await loadGrokAccounts(); renderGrokAccountCards()
})

window.copyGrokAccountFromCard = async (enc) => withButtonLoading(async () => {
  const token = decodeSsoToken(enc)
  const account = grokAccounts.find((a) => a.ssoToken === token)
  if (!account) { showError(t('grok.accountNotFound')); return }
  try {
    await navigator.clipboard.writeText(JSON.stringify({ ssoToken: account.ssoToken, tier: account.tier, proxyUrl: account.proxyUrl }, null, 2))
    showSuccess(t('grok.copySuccess'))
  } catch (e) { showError(t('grok.copyFailed') + (e?.message || e)) }
})

window.deleteGrokAccountFromCard = async (enc) => withButtonLoading(async () => {
  const confirmed = await confirmDialog(t('grok.deleteConfirm'), { danger: true })
  if (!confirmed) return
  const token = decodeSsoToken(enc)
  try {
    await window.go.main.App.DeleteGrokAccount(token)
    showSuccess(t('grok.accountDeleted'))
  } catch (e) { showError(t('grok.deleteAccountFailed') + (e?.message || e)) }
  await loadGrokAccounts(); renderGrokAccountCards()
})

// 编辑账号
let editingGrokToken = ''
let editingGrokAccount = null

window.editGrokAccountFromCard = function (enc) {
  const token = decodeSsoToken(enc)
  const account = grokAccounts.find((a) => a.ssoToken === token)
  if (!account) { showError(t('grok.accountNotFound')); return }
  editingGrokToken = token
  editingGrokAccount = account
  const set = (id, v) => { const el = document.getElementById(id); if (el) el.value = v || '' }
  set('editGrokSsoToken', account.ssoToken)
  set('editGrokTier', account.tier || 'basic')
  set('editGrokProxyUrl', account.proxyUrl || '')
  set('editGrokWeight', account.weight > 0 ? String(account.weight) : '')
  const modal = document.getElementById('grokAccountEditModal')
  if (modal) modal.classList.add('active')
}

window.closeGrokAccountEditModal = function () {
  const modal = document.getElementById('grokAccountEditModal')
  if (modal) modal.classList.remove('active')
  editingGrokToken = ''; editingGrokAccount = null
}

window.saveGrokAccountEdit = async function () {
  if (!editingGrokToken || !editingGrokAccount) return
  const val = (id) => (document.getElementById(id)?.value || '').trim()
  const dto = {
    ...editingGrokAccount,
    ssoToken: editingGrokToken,
    tier: val('editGrokTier') || 'basic',
    proxyUrl: val('editGrokProxyUrl'),
    weight: parseInt(val('editGrokWeight'), 10) || 0,
  }
  try {
    await window.go.main.App.UpdateGrokAccount(dto)
    showSuccess(t('grok.accountUpdated'))
    window.closeGrokAccountEditModal()
    await loadGrokAccounts(); renderGrokAccountCards()
  } catch (e) { showError(t('grok.updateAccountFailed') + (e?.message || e)) }
}

// 添加账号
export function showAddGrokAccountDialog() {
  const modal = document.getElementById('grokAccountAddModal')
  if (modal) modal.classList.add('active')
}

window.closeGrokAccountAddModal = function () {
  const modal = document.getElementById('grokAccountAddModal')
  if (modal) modal.classList.remove('active')
}

window.saveGrokAccountAdd = async function () {
  const val = (id) => (document.getElementById(id)?.value || '').trim()
  const ssoToken = val('addGrokSsoToken')
  if (!ssoToken) { showError(t('grok.ssoTokenRequired')); return }
  const dto = {
    ssoToken,
    tier: val('addGrokTier') || 'basic',
    proxyUrl: val('addGrokProxyUrl'),
    weight: parseInt(val('addGrokWeight'), 10) || 0,
  }
  try {
    await window.go.main.App.AddGrokAccount(dto)
    showSuccess(t('grok.accountAdded'))
    window.closeGrokAccountAddModal()
    await loadGrokAccounts(); renderGrokAccountCards()
  } catch (e) { showError(t('grok.addAccountFailed') + (e?.message || e)) }
}

// 全局配置
export function showGrokGlobalConfigModal() {
  loadGrokGlobalConfig()
  const modal = document.getElementById('grokGlobalConfigModal')
  if (modal) modal.classList.add('active')
}

window.closeGrokGlobalConfigModal = function () {
  const modal = document.getElementById('grokGlobalConfigModal')
  if (modal) modal.classList.remove('active')
}

async function loadGrokGlobalConfig() {
  try {
    if (!window.go?.main?.App?.GetGrokGlobalConfig) return
    const cfg = await window.go.main.App.GetGrokGlobalConfig()
    const set = (id, v) => { const el = document.getElementById(id); if (el) el.value = v || '' }
    set('grokGlobalRotationMode', cfg.rotationMode || 'fixed')
    set('grokGlobalProxyUrl', cfg.proxyUrl || '')
  } catch (e) { console.error('Failed to load grok global config:', e) }
}

window.saveGrokGlobalConfig = async function () {
  const val = (id) => (document.getElementById(id)?.value || '').trim()
  try {
    await window.go.main.App.SaveGrokGlobalConfig({
      rotationMode: val('grokGlobalRotationMode') || 'fixed',
      proxyUrl: val('grokGlobalProxyUrl'),
    })
    showSuccess(t('grok.globalConfigSaved'))
    window.closeGrokGlobalConfigModal()
  } catch (e) { showError(t('grok.globalConfigSaveFailed') + (e?.message || e)) }
}

// 下拉菜单控制
window.toggleGrokAddAccountDropdown = function (event) {
  if (event) {
    event.stopPropagation()
  }
  const menu = document.getElementById('grokAddAccountMenu')
  if (!menu) return
  const isVisible = menu.style.display !== 'none'
  menu.style.display = isVisible ? 'none' : 'block'
}

// 点击外部关闭下拉菜单
document.addEventListener('click', (e) => {
  const grokDropdown = e.target.closest('.kiro-add-account-dropdown')
  const grokMenu = document.getElementById('grokAddAccountMenu')

  // 如果点击的不是 Grok 下拉菜单区域，则关闭 Grok 菜单
  if (grokMenu && !grokDropdown) {
    grokMenu.style.display = 'none'
  }
})

// 批量导入
export function showGrokBulkImportDialog() {
  const menu = document.getElementById('grokAddAccountMenu')
  if (menu) menu.style.display = 'none'

  const modal = document.getElementById('grokBulkImportModal')
  if (modal) {
    // 重置表单
    const textarea = document.getElementById('grokBulkImportTokens')
    const tier = document.getElementById('grokBulkImportTier')
    const proxyUrl = document.getElementById('grokBulkImportProxyUrl')
    const progress = document.getElementById('grokBulkImportProgress')

    if (textarea) textarea.value = ''
    if (tier) tier.value = 'basic'
    if (proxyUrl) proxyUrl.value = ''
    if (progress) progress.style.display = 'none'

    modal.classList.add('active')
  }
}

window.closeGrokBulkImportModal = function () {
  const modal = document.getElementById('grokBulkImportModal')
  if (modal) modal.classList.remove('active')
}

window.executeGrokBulkImport = async function () {
  const textarea = document.getElementById('grokBulkImportTokens')
  const tier = document.getElementById('grokBulkImportTier')
  const proxyUrl = document.getElementById('grokBulkImportProxyUrl')
  const btn = document.getElementById('grokBulkImportBtn')
  const progress = document.getElementById('grokBulkImportProgress')
  const progressBar = document.getElementById('grokBulkImportProgressBar')
  const progressText = document.getElementById('grokBulkImportProgressText')
  const progressCount = document.getElementById('grokBulkImportProgressCount')

  if (!textarea) return

  const text = textarea.value.trim()
  if (!text) {
    showError(t('grok.bulkImportEmpty'))
    return
  }

  // 解析 token 列表（每行一个）
  const lines = text.split('\n').map(line => line.trim()).filter(line => line.length > 0)
  if (lines.length === 0) {
    showError(t('grok.bulkImportEmpty'))
    return
  }

  // 禁用按钮，显示进度
  if (btn) btn.disabled = true
  if (progress) progress.style.display = 'block'

  let successCount = 0
  let failCount = 0
  const total = lines.length

  try {
    for (let i = 0; i < lines.length; i++) {
      const token = lines[i]

      // 更新进度
      const percent = ((i + 1) / total * 100).toFixed(0)
      if (progressBar) progressBar.style.width = percent + '%'
      if (progressText) progressText.textContent = t('common.processing')
      if (progressCount) progressCount.textContent = `${i + 1}/${total}`

      try {
        const dto = {
          ssoToken: token,
          tier: tier?.value || 'basic',
          proxyUrl: proxyUrl?.value || '',
          weight: 0,
        }
        await window.go.main.App.AddGrokAccount(dto)
        successCount++
      } catch (e) {
        console.error(`Failed to import token ${i + 1}:`, e)
        failCount++
      }

      // 短暂延迟，避免过快
      await new Promise(resolve => setTimeout(resolve, 100))
    }

    // 显示结果
    if (failCount === 0) {
      showSuccess(t('grok.bulkImportSuccess').replace('{count}', successCount))
    } else {
      showSuccess(t('grok.bulkImportPartial').replace('{success}', successCount).replace('{fail}', failCount))
    }

    // 刷新列表
    await loadGrokAccounts()
    renderGrokAccountCards()

    // 关闭模态框
    window.closeGrokBulkImportModal()
  } catch (e) {
    showError(t('grok.bulkImportFailed') + (e?.message || e))
  } finally {
    if (btn) btn.disabled = false
    if (progress) progress.style.display = 'none'
  }
}

// 全部复活
window.reviveAllGrokAccounts = async function () {
  const confirmed = await confirmDialog(t('grok.reviveAllConfirm'), { danger: false })
  if (!confirmed) return

  try {
    await window.go.main.App.ReviveAllGrokAccounts()
    showSuccess(t('grok.reviveAllSuccess'))
    await loadGrokAccounts()
    renderGrokAccountCards()
  } catch (e) {
    showError(t('grok.reviveAllFailed') + (e?.message || e))
  }
}

// 批量删除对话框
window.showGrokBulkDeleteDialog = function () {
  if (!grokAccounts || grokAccounts.length === 0) {
    showError(t('grok.noAccounts') || 'No accounts')
    return
  }

  const modal = document.createElement('div')
  modal.className = 'modal active'
  modal.id = 'grokBulkDeleteModal'

  const listHtml = grokAccounts
    .map((acc) => {
      const isInvalid = acc.status === 'invalid'
      const label = truncateSsoToken(acc.ssoToken)
      const statusText = getGrokStatusText(acc.status)
      const statusClass = getGrokStatusBadgeClass(acc.status)
      const encodedToken = btoa(acc.ssoToken)

      return `
            <div class="kiro-bulk-item" style="display: flex; align-items: center; padding: 10px; border-bottom: 1px solid var(--border-light); hover: background-color: var(--bg-secondary);">
                <input type="checkbox" class="grok-bulk-checkbox" value="${encodedToken}" data-invalid="${isInvalid}" id="chk-${encodedToken}" style="margin-right: 12px; transform: scale(1.2);">
                <label for="chk-${encodedToken}" style="flex: 1; cursor: pointer; display: flex; justify-content: space-between; align-items: center;">
                    <span style="font-weight: 500; font-size: 13px;">${escapeHTML(label)}</span>
                    <span class="kiro-status-badge ${statusClass}">${statusText}</span>
                </label>
            </div>
        `
    })
    .join('')

  modal.innerHTML = `
        <div class="modal-content" style="max-width: 500px; display: flex; flex-direction: column; max-height: 85vh;">
            <div class="modal-header">
                <h2>${t('grok.bulkDelete') || '批量删除账号'}</h2>
                <button class="modal-close" onclick="this.closest('.modal').remove()">&times;</button>
            </div>
            <div class="modal-body" style="flex: 1; overflow: hidden; display: flex; flex-direction: column; padding: 0;">
                <div class="kiro-bulk-actions" style="padding: 15px; border-bottom: 1px solid var(--border-light); display: flex; gap: 10px; background: var(--bg-secondary);">
                    <button class="btn btn-sm btn-secondary" onclick="toggleGrokBulkDeleteAll(true)">${t('grok.selectAll') || '全选'}</button>
                    <button class="btn btn-sm btn-secondary" onclick="toggleGrokBulkDeleteAll(false)">${t('grok.deselectAll') || '全不选'}</button>
                    <button class="btn btn-sm btn-warning" onclick="selectGrokBulkDeleteInvalid()">
                        ${t('grok.selectInvalid') || '全选无效项'}
                    </button>
                </div>
                <div class="kiro-bulk-list" style="overflow-y: auto; padding: 0 15px;">
                    ${listHtml}
                </div>
            </div>
            <div class="modal-footer" style="padding: 15px; border-top: 1px solid var(--border-light);">
                <button class="btn btn-secondary" onclick="this.closest('.modal').remove()">${t('common.cancel')}</button>
                <button class="btn btn-danger" onclick="executeGrokBulkDelete()">${t('common.delete')}</button>
            </div>
        </div>
    `

  document.body.appendChild(modal)
}

// 批量全选/全不选
window.toggleGrokBulkDeleteAll = function (checked) {
  const checkboxes = document.querySelectorAll('#grokBulkDeleteModal .grok-bulk-checkbox')
  checkboxes.forEach((cb) => (cb.checked = checked))
}

// 选中所有无效账号
window.selectGrokBulkDeleteInvalid = function () {
  const checkboxes = document.querySelectorAll('#grokBulkDeleteModal .grok-bulk-checkbox')
  checkboxes.forEach((cb) => {
    if (cb.dataset.invalid === 'true') {
      cb.checked = true
    }
  })
}

// 执行批量删除
window.executeGrokBulkDelete = async function () {
  const checkboxes = document.querySelectorAll('#grokBulkDeleteModal .grok-bulk-checkbox:checked')
  if (checkboxes.length === 0) {
    showError(t('grok.noAccountSelected') || '请至少选择一个账号')
    return
  }

  const confirmed = await confirmDialog(`${t('grok.bulkDeleteConfirm') || '确定要删除选中的账号吗？'} (${checkboxes.length})`, {
    danger: true,
  })
  if (!confirmed) {
    return
  }

  const modal = document.getElementById('grokBulkDeleteModal')
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
        const ssoToken = atob(cb.value)
        if (window.go?.main?.App?.DeleteGrokAccount) {
          await window.go.main.App.DeleteGrokAccount(ssoToken)
          successCount++
        }
      } catch (e) {
        console.error('Delete failed for token', cb.value, e)
      }
    }

    showSuccess(`${t('grok.bulkDeleteSuccess') || '删除成功'} (${successCount}/${total})`)

    modal.remove()
    await loadGrokAccounts()
    renderGrokAccountCards()
  } catch (e) {
    console.error('Bulk delete error', e)
    showError(t('grok.bulkDeleteFailed') || '批量删除失败')
  } finally {
    if (document.body.contains(modal) && deleteBtn) {
      deleteBtn.disabled = false
      deleteBtn.innerHTML = originalText
    }
  }
}
