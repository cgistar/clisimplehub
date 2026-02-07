import { t } from '../i18n/index.js'
import { showError, showSuccess } from './utils.js'
import { confirm } from './confirm.js'
import { createIcon } from './icons.js'

const state = {
    servers: [],
    editingIndex: -1, // -1 = add new, >=0 = editing existing
    formVisible: false
}

function errorMessage(e) {
    return e?.message ? e.message : String(e)
}

export async function loadServers() {
    try {
        const servers = await window.go.main.App.GetServers()
        state.servers = servers || []
    } catch (e) {
        console.error('Failed to load servers:', e)
        state.servers = []
    }
    renderServerSelect()
}

function renderServerSelect() {
    const select = document.getElementById('serverSyncSelect')
    const display = document.getElementById('serverSyncSelectDisplay')
    const dropdown = document.getElementById('serverSyncDropdown')

    if (!select) return

    select.innerHTML = ''

    if (state.servers.length === 0) {
        const opt = document.createElement('option')
        opt.value = ''
        opt.textContent = t('serverSync.noServers')
        select.appendChild(opt)
        select.disabled = true
        if (display) {
            display.value = t('serverSync.noServers')
            display.disabled = true
        }
    } else {
        select.disabled = false
        if (display) display.disabled = false

        state.servers.forEach((s, i) => {
            const opt = document.createElement('option')
            opt.value = i
            opt.textContent = s.name || s.url
            select.appendChild(opt)
        })

        // Update display input
        if (display && select.value !== '') {
            const selectedIndex = parseInt(select.value, 10)
            if (!isNaN(selectedIndex) && state.servers[selectedIndex]) {
                const s = state.servers[selectedIndex]
                display.value = s.name || s.url
            }
        }
    }

    // Render dropdown options
    if (dropdown && state.servers.length > 0) {
        dropdown.innerHTML = state.servers.map((s, i) => `
            <div class="model-dropdown-item" onclick="selectServerSyncOption(${i})">
                ${s.name || s.url}
            </div>
        `).join('')
    }
}

export function toggleServerSyncDropdown() {
    const dropdown = document.getElementById('serverSyncDropdown')
    if (!dropdown) return

    const isVisible = dropdown.classList.contains('show')

    // Close all other dropdowns first
    document.querySelectorAll('.model-dropdown.show').forEach(d => {
        if (d !== dropdown) d.classList.remove('show')
    })

    if (isVisible) {
        dropdown.classList.remove('show')
    } else {
        dropdown.classList.add('show')
    }
}

export function selectServerSyncOption(index) {
    const select = document.getElementById('serverSyncSelect')
    const display = document.getElementById('serverSyncSelectDisplay')
    const dropdown = document.getElementById('serverSyncDropdown')

    if (!select || index < 0 || index >= state.servers.length) return

    select.value = index
    const s = state.servers[index]
    if (display) {
        display.value = s.name || s.url
    }

    if (dropdown) {
        dropdown.classList.remove('show')
    }
}

// Close dropdown when clicking outside
document.addEventListener('click', (e) => {
    const dropdown = document.getElementById('serverSyncDropdown')
    if (!dropdown) return

    const container = dropdown.closest('.model-select-container')
    if (container && !container.contains(e.target)) {
        dropdown.classList.remove('show')
    }
})

// Export functions for global access
window.toggleServerSyncDropdown = toggleServerSyncDropdown
window.selectServerSyncOption = selectServerSyncOption
window.addServerAccount = addServerAccount
window.editServerAccount = editServerAccount
window.deleteServerAccount = deleteServerAccount
window.testServerAccount = testServerAccount
window.syncConfigToServer = syncConfigToServer
window.cancelServerForm = cancelServerForm
window.saveServerAccount = saveServerAccount

function getSelectedIndex() {
    const select = document.getElementById('serverSyncSelect')
    if (!select || select.disabled) return -1
    const idx = parseInt(select.value, 10)
    return Number.isNaN(idx) ? -1 : idx
}

export function addServerAccount() {
    state.editingIndex = -1
    showForm('', '', '')
}

export function editServerAccount() {
    const idx = getSelectedIndex()
    if (idx < 0) return
    const s = state.servers[idx]
    state.editingIndex = idx
    showForm(s.name || '', s.url || '', s.apiKey || '')
}

function showForm(name, url, apiKey) {
    state.formVisible = true
    const form = document.getElementById('serverSyncForm')
    if (!form) return
    form.style.display = ''
    const nameEl = document.getElementById('serverSyncName')
    const urlEl = document.getElementById('serverSyncUrl')
    const apiKeyEl = document.getElementById('serverSyncApiKey')
    if (nameEl) nameEl.value = name
    if (urlEl) urlEl.value = url
    if (apiKeyEl) apiKeyEl.value = apiKey
}

export function cancelServerForm() {
    state.formVisible = false
    state.editingIndex = -1
    const form = document.getElementById('serverSyncForm')
    if (form) form.style.display = 'none'
}

export async function saveServerAccount() {
    const nameEl = document.getElementById('serverSyncName')
    const urlEl = document.getElementById('serverSyncUrl')
    const apiKeyEl = document.getElementById('serverSyncApiKey')
    if (!nameEl || !urlEl || !apiKeyEl) return

    const name = nameEl.value.trim()
    const url = urlEl.value.trim()
    const apiKey = apiKeyEl.value.trim()

    if (!url) {
        showError(t('serverSync.urlRequired'))
        return
    }

    const entry = { name, url, apiKey }
    const servers = [...state.servers]

    const desiredIndex =
        state.editingIndex >= 0 && state.editingIndex < servers.length
            ? state.editingIndex
            : servers.length

    if (state.editingIndex >= 0 && state.editingIndex < servers.length) {
        servers[state.editingIndex] = entry
    } else {
        servers.push(entry)
    }

    try {
        await window.go.main.App.SaveServers(servers)
        showSuccess(t('serverSync.saveSuccess'))
        cancelServerForm()
        await loadServers()
        // Select the newly added/edited entry
        const select = document.getElementById('serverSyncSelect')
        if (select && !select.disabled && state.servers.length > 0) {
            const maxIdx = state.servers.length - 1
            select.value = Math.min(desiredIndex, maxIdx)
        }
    } catch (e) {
        showError(t('serverSync.saveFailed') + errorMessage(e))
    }
}

export async function deleteServerAccount() {
    const idx = getSelectedIndex()
    if (idx < 0) return

    const s = state.servers[idx]
    const confirmed = await confirm(
        `${t('serverSync.deleteConfirm')} "${s.name || s.url}"?`,
        { title: t('serverSync.delete'), confirmText: t('common.delete'), cancelText: t('common.cancel'), danger: true }
    )
    if (!confirmed) return

    const servers = state.servers.filter((_, i) => i !== idx)
    try {
        await window.go.main.App.SaveServers(servers)
        showSuccess(t('serverSync.deleteSuccess'))
        await loadServers()
    } catch (e) {
        showError(t('serverSync.deleteFailed') + errorMessage(e))
    }
}

export async function testServerAccount() {
    const idx = getSelectedIndex()
    if (idx < 0) return

    const s = state.servers[idx]
    const btn = document.getElementById('serverSyncTestBtn')
    if (btn) btn.disabled = true

    try {
        await window.go.main.App.TestServerConnection(s.url, s.apiKey)
        showSuccess(t('serverSync.testSuccess'))
    } catch (e) {
        showError(t('serverSync.testFailed') + errorMessage(e))
    } finally {
        if (btn) btn.disabled = false
    }
}

export async function syncConfigToServer() {
    const idx = getSelectedIndex()
    if (idx < 0) return

    const s = state.servers[idx]
    const confirmed = await confirm(
        `${t('serverSync.syncConfirm')} "${s.name || s.url}"?`,
        { title: t('serverSync.title'), confirmText: t('serverSync.sync'), cancelText: t('common.cancel') }
    )
    if (!confirmed) return

    const btn = document.getElementById('serverSyncBtn')
    if (btn) btn.disabled = true

    try {
        await window.go.main.App.SyncConfigToServer(idx)
        showSuccess(t('serverSync.syncSuccess'))
    } catch (e) {
        showError(t('serverSync.syncFailed') + errorMessage(e))
    } finally {
        if (btn) btn.disabled = false
    }
}

export function serverSyncSectionHTML() {
    return `
        <div class="card-section">
            <h3>${createIcon('server', { size: 14 })} ${t('serverSync.title')}</h3>
            <div class="form-group">
                <label>${t('serverSync.selectServer')}</label>
                <div class="model-input-wrapper">
                    <div class="model-select-container standalone-select" style="flex: 1;">
                        <input type="text" id="serverSyncSelectDisplay" readonly onclick="toggleServerSyncDropdown()" placeholder="${t('serverSync.noServers')}">
                        <button type="button" class="model-dropdown-toggle" onclick="toggleServerSyncDropdown()">
                            <svg width="12" height="12" viewBox="0 0 12 12" fill="currentColor">
                                <path d="M2 4L6 8L10 4" stroke="currentColor" stroke-width="2" fill="none"/>
                            </svg>
                        </button>
                        <div class="model-dropdown" id="serverSyncDropdown"></div>
                    </div>
                    <select id="serverSyncSelect" style="display:none;"></select>
                    <button class="btn-icon" onclick="addServerAccount()" title="${t('serverSync.add')}">
                        ${createIcon('plus', { size: 14 })}
                    </button>
                    <button class="btn-icon" onclick="editServerAccount()" title="${t('serverSync.edit')}">
                        ${createIcon('edit', { size: 14 })}
                    </button>
                    <button class="btn-icon" id="serverSyncTestBtn" onclick="testServerAccount()" title="${t('serverSync.test')}">
                        ${createIcon('check', { size: 14 })}
                    </button>
                    <button class="btn-icon" onclick="deleteServerAccount()" title="${t('serverSync.delete')}">
                        ${createIcon('trash', { size: 14 })}
                    </button>
                </div>
            </div>
            <div id="serverSyncForm" style="display:none">
                <div class="form-group">
                    <label>${t('serverSync.name')}</label>
                    <input type="text" id="serverSyncName" placeholder="${t('serverSync.namePlaceholder')}">
                </div>
                <div class="form-group">
                    <label>${t('serverSync.url')}</label>
                    <input type="text" id="serverSyncUrl" placeholder="${t('serverSync.urlPlaceholder')}">
                </div>
                <div class="form-group">
                    <label>${t('serverSync.apiKey')}</label>
                    <input type="password" id="serverSyncApiKey" placeholder="${t('serverSync.apiKeyPlaceholder')}">
                </div>
                <div class="form-row" style="justify-content: flex-end; gap: 8px;">
                    <button class="btn btn-sm btn-secondary" onclick="cancelServerForm()">${t('common.cancel')}</button>
                    <button class="btn btn-sm btn-primary" onclick="saveServerAccount()">${t('settings.save')}</button>
                </div>
            </div>
            <div class="form-row" style="justify-content: flex-end; margin-top: 8px;">
                <button class="btn btn-primary" id="serverSyncBtn" onclick="syncConfigToServer()">
                    ${createIcon('upload', { size: 14 })} ${t('serverSync.sync')}
                </button>
            </div>
        </div>`
}
