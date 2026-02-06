import { t } from '../../i18n/index.js'
import { createIcon } from '../icons.js'

export function mainLayoutTemplate() {
  return `
        <div class="main-container" id="homeView">
            <div class="left-panel">
                <div class="card">
                    <div class="card-header">
                        <h2>${t('endpoints.title')} <button class="icon-btn" onclick="showManageModal()" title="${t('manage.vendors')}">${createIcon('building', { size: 14 })} ${t('manage.vendors')}</button></h2>
                    </div>
                    <div class="tabs" id="interfaceTabs">
                        <button class="tab-btn active" data-type="claude" onclick="switchTab('claude')">Claude</button>
                        <button class="tab-btn" data-type="codex" onclick="switchTab('codex')">Codex</button>
                        <button class="tab-btn" data-type="gemini" onclick="switchTab('gemini')">Gemini</button>
                        <button class="tab-btn" data-type="chat" onclick="switchTab('chat')">Chat</button>
                        <button class="tab-btn tab-btn-right" onclick="openCLIConfigEditor()" title="${t('cliConfig.title')}">${createIcon('edit', { size: 14 })}</button>
                    </div>
                    <div class="active-selector">
                        <label>${t('endpoints.activeEndpoint')}:</label>
                        <select id="activeEndpointSelect" onchange="setActiveEndpoint()">
                            <option value="">${t('endpoints.selectActive')}</option>
                        </select>
                        <button class="icon-btn" onclick="refreshConfig()" title="${t('endpoints.refresh')}">${createIcon('refresh', { size: 14 })}</button>
                        <button class="icon-btn" onclick="pingAllEndpoints()" title="${t('endpoints.pingAll')}">${createIcon('zap', { size: 14 })}</button>
                        <button class="icon-btn" onclick="showEndpointForm()" title="${t('manage.addEndpoint')}">${createIcon('plus', { size: 14 })}</button>
                    </div>
                    <div class="endpoint-list" id="endpointList">
                        <div class="loading">${t('common.loading')}</div>
                    </div>
                </div>
            </div>

            <div class="right-panel">
                <div class="card logs-card">
                    <div class="card-header">
                        <h2>${createIcon('list', { size: 16 })} ${t('logs.title')}</h2>
                        <div class="card-header-actions">
                            <button class="btn btn-sm btn-secondary" onclick="showStatsModal()" title="${t('stats.title')}">
                                ${createIcon('chart', { size: 14 })} ${t('stats.title')}
                            </button>
                            <button class="toggle-btn" id="consoleToggleBtn" onclick="toggleBottomConsole()" title="${t('console.title')}">
                                ${createIcon('terminal', { size: 14 })}
                            </button>
                        </div>
                    </div>
                    <div class="logs-container" id="logsContainer">
                        <div class="empty-state">${t('logs.noLogs')}</div>
                    </div>
                </div>
            </div>
        </div>

        <div class="main-container kiro-accounts-view" id="kiroAccountsView" style="display: none;">
            <div class="kiro-accounts-page">
                <div class="card">
                    <div class="card-header">
                        <h2>${createIcon('users', { size: 16 })} ${t('kiro.accountsTitle')}</h2>
                        <div class="card-header-actions">
                            <button class="btn btn-sm btn-danger" style="margin-right: 8px;" onclick="showBulkDeleteDialog()" title="${t('kiro.bulkDelete')}">
                                ${createIcon('trash', { size: 14 })} ${t('common.delete')}
                            </button>
                            <div class="kiro-add-account-dropdown">
                                <button class="btn btn-sm btn-primary" onclick="toggleKiroAddAccountDropdown()" title="${t('kiro.addAccount')}">
                                    ${createIcon('plus', { size: 14 })} ${t('kiro.addAccount')} ${createIcon('chevronDown', { size: 14 })}
                                </button>
                                <div class="kiro-add-account-menu" id="kiroAddAccountMenu" style="display: none;">
                                    <button class="dropdown-item" onclick="importKiroAccountsFromJson()">
                                        <span class="dropdown-icon">${createIcon('download', { size: 14 })}</span>
                                        <span class="dropdown-text">${t('kiro.jsonImport')}</span>
                                    </button>
                                    <button class="dropdown-item" onclick="startAddAccountLogin('social', 'Google')">
                                        <span class="dropdown-icon">${createIcon('key', { size: 14 })}</span>
                                        <span class="dropdown-text">Google</span>
                                    </button>
                                    <button class="dropdown-item" onclick="startAddAccountLogin('social', 'Github')">
                                        <span class="dropdown-icon">${createIcon('key', { size: 14 })}</span>
                                        <span class="dropdown-text">GitHub</span>
                                    </button>
                                    <button class="dropdown-item" onclick="startAddAccountLogin('idc', 'builder')">
                                        <span class="dropdown-icon">${createIcon('hammer', { size: 14 })}</span>
                                        <span class="dropdown-text">AWS Builder ID</span>
                                    </button>
                                    <button class="dropdown-item" onclick="startAddAccountLogin('idc', 'org')">
                                        <span class="dropdown-icon">${createIcon('building', { size: 14 })}</span>
                                        <span class="dropdown-text">IAM Identity Center</span>
                                    </button>
                                </div>
                            </div>
                        </div>
                    </div>
                    <div class="kiro-accounts-page-body">
                        <div class="kiro-accounts-grid" id="kiroAccountsGrid">
                            <div class="loading">${t('common.loading')}</div>
                        </div>
                    </div>
                </div>
            </div>
        </div>`
}
