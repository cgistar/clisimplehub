import { t } from '../../i18n/index.js'

export function mainLayoutTemplate() {
    return `
        <div class="main-container">
            <div class="left-panel">
                <div class="card">
                    <div class="card-header">
                        <h2>${t('endpoints.title')} <button class="icon-btn" onclick="showManageModal()" title="${t('manage.vendors')}">🏢 ${t('manage.vendors')}</button></h2>
                    </div>
                    <div class="tabs" id="interfaceTabs">
                        <button class="tab-btn active" data-type="claude" onclick="switchTab('claude')">Claude</button>
                        <button class="tab-btn" data-type="codex" onclick="switchTab('codex')">Codex</button>
                        <button class="tab-btn" data-type="gemini" onclick="switchTab('gemini')">Gemini</button>
                        <button class="tab-btn" data-type="chat" onclick="switchTab('chat')">Chat</button>
                        <button class="tab-btn tab-btn-right" onclick="openCLIConfigEditor()" title="${t('cliConfig.title')}">📝</button>
                    </div>
                    <div class="active-selector">
                        <label>${t('endpoints.activeEndpoint')}:</label>
                        <select id="activeEndpointSelect" onchange="setActiveEndpoint()">
                            <option value="">${t('endpoints.selectActive')}</option>
                        </select>
                        <button class="icon-btn" onclick="refreshConfig()" title="${t('endpoints.refresh')}">🔄</button>
                        <button class="icon-btn" onclick="pingAllEndpoints()" title="${t('endpoints.pingAll')}">⚡</button>
                        <button class="icon-btn" onclick="showEndpointForm()" title="${t('manage.addEndpoint')}">➕</button>
                    </div>
                    <div class="endpoint-list" id="endpointList">
                        <div class="loading">${t('common.loading')}</div>
                    </div>
                </div>
            </div>

            <div class="right-panel">
                <div class="card logs-card">
                    <div class="card-header">
                        <h2>📋 ${t('logs.title')}</h2>
                        <div class="card-header-actions">
                            <button class="btn btn-sm btn-secondary" onclick="showStatsModal()" title="${t('stats.title')}">
                                📊 ${t('stats.title')}
                            </button>
                            <button class="toggle-btn" id="consoleToggleBtn" onclick="toggleBottomConsole()" title="${t('console.title')}">
                                🖥️
                            </button>
                        </div>
                    </div>
                    <div class="logs-container" id="logsContainer">
                        <div class="empty-state">${t('logs.noLogs')}</div>
                    </div>
                </div>
            </div>
        </div>`
}
