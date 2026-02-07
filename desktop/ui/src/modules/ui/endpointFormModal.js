import { t } from '../../i18n/index.js'

export function endpointFormModalTemplate() {
    return `
        <div id="endpointFormModal" class="modal">
            <div class="modal-content">
                <div class="modal-header">
                    <h2 id="endpointFormTitle">${t('manage.addEndpoint')}</h2>
                    <button class="modal-close" onclick="closeEndpointForm()">&times;</button>
                </div>
                <div class="modal-body">
                    <input type="hidden" id="endpointId">
                    <div class="form-group">
                        <label>${t('manage.vendor')}</label>
                        <div class="model-select-container vendor-select">
                            <input type="text" id="endpointVendorDisplay" readonly onclick="toggleVendorDropdown()" placeholder="${t('manage.selectVendor')}">
                            <button type="button" class="model-dropdown-toggle" onclick="toggleVendorDropdown()">
                                <svg width="12" height="12" viewBox="0 0 12 12" fill="currentColor">
                                    <path d="M2 4L6 8L10 4" stroke="currentColor" stroke-width="2" fill="none"/>
                                </svg>
                            </button>
                            <div class="model-dropdown" id="vendorDropdown"></div>
                        </div>
                        <input type="hidden" id="endpointVendor">
                        <small>${t('manage.vendorHelp')}</small>
                    </div>
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
                        <div class="model-select-container interface-type-select">
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
                        <div class="model-input-wrapper">
                            <div class="model-select-container">
                                <input type="text" id="endpointTransformerDisplay" readonly onclick="toggleTransformerDropdown()" placeholder="${t('manage.transformerPlaceholder')}">
                                <button type="button" class="model-dropdown-toggle" onclick="toggleTransformerDropdown()">
                                    <svg width="12" height="12" viewBox="0 0 12 12" fill="currentColor">
                                        <path d="M2 4L6 8L10 4" stroke="currentColor" stroke-width="2" fill="none"/>
                                    </svg>
                                </button>
                                <div class="model-dropdown" id="transformerDropdown"></div>
                            </div>
                            <button type="button" class="btn btn-sm btn-primary" id="quickMappingBtn" onclick="applyQuickModelMappings()" style="display:none;" title="${t('manage.quickMappingTitle')}">🚀 ${t('manage.quickMapping')}</button>
                        </div>
                        <input type="hidden" id="endpointTransformer">
                        <small>${t('manage.transformerHelp')}</small>
                    </div>
                    <div class="form-group">
                        <label>${t('manage.routes')}</label>
                        <small>${t('manage.routesHelp')}</small>
                        <div class="model-mappings-container" id="routesContainer">
                            <div class="model-mapping-header">
                                <input type="text" placeholder="${t('manage.routePlaceholder')}" disabled class="mapping-header-label" style="flex:1;">
                                <button type="button" class="btn btn-sm btn-primary" onclick="addRoute()">+</button>
                            </div>
                            <div id="routesList"></div>
                        </div>
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
        </div>`
}
