/**
 * UI initialization module
 */
import { state } from './state.js';
import { t, getAvailableLanguages } from '../i18n/index.js';

export function initUI() {
    const app = document.getElementById('app');
    app.innerHTML = `
        <div class="header">
            <div class="header-content">
                <div class="header-left">
                    <h1>${t('header.title')}</h1>
                    <p>${t('header.subtitle')}</p>
                </div>
                <div class="header-right">
                    <button class="header-btn" onclick="showWebDAVModal()" title="WebDAV同步">🔄</button>
                    <button class="header-btn" onclick="showSettingsModal()" title="${t('settings.title')}">⚙️</button>
                </div>
            </div>
        </div>

        <div class="main-container">
            <div class="left-panel">
                <div class="card">
                    <div class="card-header">
                        <h2>${t('endpoints.title')} <button class="icon-btn" onclick="showManageModal()" title="${t('endpoints.manage')}">📝端点配置</button></h2>
                    </div>
                    <div class="tabs" id="interfaceTabs">
                        <button class="tab-btn active" data-type="claude" onclick="switchTab('claude')">Claude</button>
                        <button class="tab-btn" data-type="codex" onclick="switchTab('codex')">Codex</button>
                        <button class="tab-btn" data-type="gemini" onclick="switchTab('gemini')">Gemini</button>
                        <button class="tab-btn" data-type="chat" onclick="switchTab('chat')">Chat</button>
                        <button class="icon-btn cli-config-btn" id="cliConfigEditorBtn" onclick="openCLIConfigEditor()" title="${t('cliConfig.title')}">📝Cli 配置</button>
                    </div>
                    <div class="active-selector">
                        <label>${t('endpoints.activeEndpoint')}:</label>
                        <select id="activeEndpointSelect" onchange="setActiveEndpoint()">
                            <option value="">${t('endpoints.selectActive')}</option>
                        </select>
                        <button class="icon-btn" onclick="refreshConfig()" title="${t('endpoints.refresh')}">🔄</button>
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
        </div>

        <!-- Console Logs Panel -->
        <div class="bottom-panel" id="bottomPanel" style="display: none;">
            <div class="card console-card">
                <div class="card-header">
                    <div class="console-header-left">
                        <h2>🖥️ ${t('console.title')}</h2>
                    </div>
                    <div class="console-header-right">
                        <select id="consoleLogLevel" class="console-level-select" onchange="changeConsoleLogLevel()">
                            <option value="0">🔍 ${t('console.levels.debug')}</option>
                            <option value="1" selected>ℹ️ ${t('console.levels.info')}</option>
                            <option value="2">⚠️ ${t('console.levels.warn')}</option>
                            <option value="3">❌ ${t('console.levels.error')}</option>
                        </select>
                        <button class="btn btn-sm btn-secondary" onclick="copyConsoleLogs()" title="${t('console.copy')}">📋</button>
                        <button class="btn btn-sm btn-secondary" onclick="clearConsoleLogs()" title="${t('console.clear')}">🗑️</button>
                    </div>
                </div>
                <div id="consolePanel" class="console-panel">
                    <textarea id="consoleContent" class="console-textarea" readonly placeholder="${t('console.placeholder')}"></textarea>
                </div>
            </div>
        </div>

        <!-- Settings Modal -->
        <div id="settingsModal" class="modal">
            <div class="modal-content">
                <div class="modal-header">
                    <h2>⚙️ ${t('settings.title')}</h2>
                    <button class="modal-close" onclick="closeSettingsModal()">&times;</button>
                </div>
                <div class="modal-body">
                    <div class="form-group">
                        <label>${t('header.language')}</label>
                        <div class="language-tabs" id="languageTabs">
                            ${getAvailableLanguages().map(lang => 
                                `<button class="lang-tab ${state.language === lang.code ? 'active' : ''}" 
                                    data-lang="${lang.code}" onclick="changeLanguage('${lang.code}')">${lang.name}</button>`
                            ).join('')}
                        </div>
                    </div>
                    <div class="form-group">
                        <label>${t('settings.port')}</label>
                        <input type="number" id="settingsPort" min="1" max="65535" placeholder="5600">
                        <small>${t('settings.portHelp')}</small>
                    </div>
                    <div class="form-group">
                        <label>${t('settings.apiKey')}</label>
                        <input type="password" id="settingsApiKey" placeholder="${t('settings.apiKeyPlaceholder')}">
                        <small>${t('settings.apiKeyHelp')}</small>
                    </div>
                    <div class="form-group switch-form-group">
                        <label class="switch-label-inline">${t('settings.fallback')}</label>
                        <label class="switch">
                            <input type="checkbox" id="settingsFallback">
                            <span class="slider"></span>
                        </label>
                        <small>${t('settings.fallbackHelp')}</small>
                    </div>
                    <div class="form-group">
                        <label>${t('settings.claudeConfigDir')}</label>
                        <input type="text" id="settingsClaudeConfigDir" placeholder="~/.claude">
                        <small>${t('settings.claudeConfigDirHelp')}</small>
                    </div>
                    <div class="form-group">
                        <label>${t('settings.codexConfigDir')}</label>
                        <input type="text" id="settingsCodexConfigDir" placeholder="~/.codex">
                        <small>${t('settings.codexConfigDirHelp')}</small>
                    </div>
                </div>
                <div class="modal-footer">
                    <button class="btn btn-secondary" onclick="closeSettingsModal()">${t('settings.cancel')}</button>
                    <button class="btn btn-primary" onclick="saveSettings()">${t('settings.save')}</button>
                </div>
            </div>
        </div>

        <!-- Manage Endpoints Modal -->
        <div id="manageModal" class="modal">
            <div class="modal-content modal-large">
                <div class="modal-header">
                    <h2>📝 ${t('manage.title')}</h2>
                    <button class="modal-close" onclick="closeManageModal()">&times;</button>
                </div>
                <div class="modal-body manage-body">
                    <div class="manage-section">
                        <div class="section-header">
                            <h3>${t('manage.vendors')}</h3>
                            <button class="btn btn-sm btn-primary" onclick="showVendorForm()">+ ${t('manage.addVendor')}</button>
                        </div>
                        <div class="vendor-list" id="vendorList">
                            <div class="empty-state">${t('manage.noVendors')}</div>
                        </div>
                    </div>
                    <div class="manage-section">
                        <div class="section-header">
                            <h3>${t('manage.endpoints')}</h3>
                            <button class="btn btn-sm btn-primary" onclick="showEndpointForm()" id="addEndpointBtn" disabled>+ ${t('manage.addEndpoint')}</button>
                        </div>
                        <div class="endpoint-manage-list" id="endpointManageList">
                            <div class="empty-state">${t('manage.selectVendorFirst')}</div>
                        </div>
                    </div>
                </div>
            </div>
        </div>

        <!-- Vendor Form Modal -->
        <div id="vendorFormModal" class="modal">
            <div class="modal-content">
                <div class="modal-header">
                    <h2 id="vendorFormTitle">${t('manage.addVendor')}</h2>
                    <button class="modal-close" onclick="closeVendorForm()">&times;</button>
                </div>
                <div class="modal-body">
                    <input type="hidden" id="vendorId">
                    <div class="form-group">
                        <label>${t('manage.vendorName')} *</label>
                        <input type="text" id="vendorName" placeholder="${t('manage.vendorNamePlaceholder')}">
                    </div>
                    <div class="form-group">
                        <label>${t('manage.homeUrl')} *</label>
                        <input type="text" id="vendorHomeUrl" placeholder="${t('manage.homeUrlPlaceholder')}">
                    </div>
                    <div class="form-group">
                        <label>${t('manage.apiUrl')} *</label>
                        <input type="text" id="vendorApiUrl" placeholder="${t('manage.apiUrlPlaceholder')}">
                    </div>
                    <div class="form-group">
                        <label>${t('manage.remark')}</label>
                        <input type="text" id="vendorRemark" placeholder="${t('manage.remarkPlaceholder')}">
                    </div>
                </div>
                <div class="modal-footer">
                    <button class="btn btn-danger" id="deleteVendorBtn" onclick="deleteVendor()" style="margin-right:auto;display:none;">${t('manage.delete')}</button>
                    <button class="btn btn-secondary" onclick="closeVendorForm()">${t('manage.cancel')}</button>
                    <button class="btn btn-primary" onclick="saveVendor()">${t('manage.save')}</button>
                </div>
            </div>
        </div>

        <!-- Endpoint Form Modal -->
        <div id="endpointFormModal" class="modal">
            <div class="modal-content">
                <div class="modal-header">
                    <h2 id="endpointFormTitle">${t('manage.addEndpoint')}</h2>
                    <button class="modal-close" onclick="closeEndpointForm()">&times;</button>
                </div>
                <div class="modal-body">
                    <input type="hidden" id="endpointId">
                    <input type="hidden" id="endpointVendorId">
                    <div class="form-group">
                        <label>${t('manage.endpointName')} *</label>
                        <input type="text" id="endpointName" placeholder="${t('manage.endpointNamePlaceholder')}">
                    </div>
                    <div class="form-group">
                        <label>${t('manage.apiUrl')} *</label>
                        <input type="text" id="endpointApiUrl" placeholder="${t('manage.apiUrlPlaceholder')}">
                    </div>
                    <div class="form-group">
                        <label>${t('manage.apiKey')} *</label>
                        <div class="input-with-icon">
                            <input type="password" id="endpointApiKey" placeholder="${t('manage.apiKeyPlaceholder')}">
                            <button type="button" class="input-icon-btn" id="toggleApiKeyVisibility" onclick="toggleApiKeyVisibility()" title="${t('manage.toggleVisibility')}">👁️</button>
                        </div>
                    </div>
                    <div class="form-group">
                        <label>${t('manage.interfaceType')} *</label>
                        <div class="model-select-container">
                            <input type="text" id="endpointInterfaceTypeDisplay" readonly onclick="toggleInterfaceTypeDropdown()">
                            <button type="button" class="model-dropdown-toggle" onclick="toggleInterfaceTypeDropdown()">
                                <svg width="12" height="12" viewBox="0 0 12 12" fill="currentColor">
                                    <path d="M2 4L6 8L10 4" stroke="currentColor" stroke-width="2" fill="none"/>
                                </svg>
                            </button>
                            <div class="model-dropdown" id="interfaceTypeDropdown"></div>
                        </div>
                        <select id="endpointInterfaceType" onchange="onEndpointInterfaceTypeChange()" style="display:none;">
                            <option value="claude">Claude</option>
                            <option value="codex">Codex</option>
                            <option value="gemini">Gemini</option>
                            <option value="chat">Chat</option>
                        </select>
                    </div>
                    <div class="form-group">
                        <label>${t('manage.model')}</label>
                        <div class="model-input-wrapper">
                            <div class="model-select-container">
                                <input type="text" id="endpointModel" placeholder="${t('manage.modelPlaceholder')}" autocomplete="off" oninput="updateTestButtonVisibility()">
                                <button type="button" class="model-dropdown-toggle" onclick="toggleModelDropdown()">
                                    <svg width="12" height="12" viewBox="0 0 12 12" fill="currentColor">
                                        <path d="M2 4L6 8L10 4" stroke="currentColor" stroke-width="2" fill="none"/>
                                    </svg>
                                </button>
                                <div class="model-dropdown" id="modelDropdown"></div>
                            </div>
                            <button type="button" class="btn btn-sm btn-secondary" id="fetchModelsBtn" onclick="fetchModels()" title="${t('manage.fetchModels')}">
                                <span id="fetchModelsIcon">${t('manage.fetchModelsBtn')}</span>
                            </button>
                            <button type="button" class="btn btn-sm btn-secondary" id="testEndpointBtn" onclick="testEndpoint()" style="display:none;">${t('manage.test')}</button>
                        </div>
                    </div>
                    <div class="form-group">
                        <label>${t('manage.transformer')}</label>
                        <div class="model-select-container">
                            <input type="text" id="endpointTransformerDisplay" readonly onclick="toggleTransformerDropdown()" placeholder="${t('manage.transformerPlaceholder')}">
                            <button type="button" class="model-dropdown-toggle" onclick="toggleTransformerDropdown()">
                                <svg width="12" height="12" viewBox="0 0 12 12" fill="currentColor">
                                    <path d="M2 4L6 8L10 4" stroke="currentColor" stroke-width="2" fill="none"/>
                                </svg>
                            </button>
                            <div class="model-dropdown" id="transformerDropdown"></div>
                        </div>
                        <input type="hidden" id="endpointTransformer">
                        <small>${t('manage.transformerHelp')}</small>
                    </div>
                    <div class="form-group">
                        <label>${t('manage.modelMappings')}</label>
                        <small>${t('manage.modelMappingsHelp')}</small>
                        <div class="model-mappings-container" id="modelMappingsContainer">
                            <div class="model-mapping-header">
                                <input type="text" placeholder="${t('manage.modelMappingAlias')}" disabled class="mapping-header-label">
                                <input type="text" placeholder="${t('manage.modelMappingName')}" disabled class="mapping-header-label">
                                <button type="button" class="btn btn-sm btn-primary" onclick="addModelMapping()">+</button>
                            </div>
                            <div id="modelMappingsList"></div>
                        </div>
                    </div>
                    <div class="form-group">
                        <label>${t('manage.proxyUrl')}</label>
                        <input type="text" id="endpointProxyUrl" placeholder="${t('manage.proxyUrlPlaceholder')}">
                        <small>${t('manage.proxyUrlHelp')}</small>
                    </div>
                    <div class="form-row">
                        <div class="form-group">
                            <label>${t('manage.priority')}</label>
                            <input type="number" id="endpointPriority" min="1" max="10" value="5" placeholder="${t('manage.priorityPlaceholder')}">
                            <small>${t('manage.priorityHelp')}</small>
                        </div>
                        <div class="form-group switch-form-group">
                            <label>${t('manage.enabled')}</label>
                            <label class="switch">
                                <input type="checkbox" id="endpointEnabled" checked>
                                <span class="slider"></span>
                            </label>
                        </div>
                    </div>
                    <div class="form-group">
                        <label>${t('manage.remark')}</label>
                        <input type="text" id="endpointRemark" placeholder="${t('manage.remarkPlaceholder')}">
                    </div>
                </div>
                <div class="modal-footer">
                    <button class="btn btn-danger" id="deleteEndpointBtn" onclick="deleteEndpoint()" style="margin-right:auto;display:none;">${t('manage.delete')}</button>
                    <button class="btn btn-secondary" onclick="closeEndpointForm()">${t('manage.cancel')}</button>
                    <button class="btn btn-primary" onclick="saveEndpoint()">${t('manage.save')}</button>
                </div>
            </div>
        </div>

        <!-- CLI Config Editor Modal -->
        <div id="cliConfigModal" class="modal">
            <!-- Content will be dynamically generated -->
        </div>

        <!-- Error Toast -->
        <div id="errorToast" class="error-toast">
            <span id="errorMessage"></span>
        </div>

        <!-- WebDAV Sync Modal -->
        <div id="webdavModal" class="modal">
            <div class="modal-content modal-large">
                <div class="modal-header">
                    <h2>🔄 WebDAV 配置同步</h2>
                    <button class="modal-close" onclick="closeWebDAVModal()">×</button>
                </div>
                <div class="modal-body">
                    <!-- WebDAV Server Configuration -->
                    <div class="card-section">
                        <h3>WebDAV 服务器配置</h3>
                        <div class="form-group">
                            <label>服务器地址</label>
                            <input type="text" id="webdavServerUrl" placeholder="https://dav.example.com/backup">
                            <small>请输入WebDAV服务器地址（支持https/http）</small>
                        </div>
                        <div class="form-row">
                            <div class="form-group">
                                <label>用户名</label>
                                <input type="text" id="webdavUsername" placeholder="用户名">
                            </div>
                            <div class="form-group">
                                <label>密码</label>
                                <input type="password" id="webdavPassword" placeholder="密码">
                            </div>
                        </div>
                        <div class="form-row">
                            <button class="btn btn-secondary" onclick="testWebDAVConnection()" id="webdavTestBtn">
                                🧪 测试连接
                            </button>
                            <button class="btn btn-primary" onclick="backupToWebDAV()" id="webdavBackupBtn">
                                💾 备份配置
                            </button>
                        </div>
                    </div>

                    <!-- Backup Records -->
                    <div class="card-section">
                        <h3>备份记录</h3>
                        <div class="backup-actions-bar">
                            <button class="btn btn-sm btn-secondary" onclick="loadBackupsList()">
                                🔄 刷新列表
                            </button>
                        </div>
                        <div class="webdav-backups-list" id="webdavBackupsList">
                            <div class="empty-state">暂无备份记录</div>
                        </div>
                    </div>
                </div>
            </div>
        </div>
    `;
}
