import { t } from '../../i18n/index.js'

export function kiroConfigModalTemplate() {
  return `
        <div id="kiroConfigModal" class="modal">
            <div class="modal-content">
                <div class="modal-header">
                    <h2>${t('kiro.title')}</h2>
                    <button class="modal-close" onclick="closeKiroConfigModal()">&times;</button>
                </div>
                <div class="modal-body">
                    <div class="form-group">
                        <label>${t('kiro.authMethod')}</label>
                        <div class="model-select-container kiro-auth-method-select">
                            <input type="text" id="kiroAuthMethodDisplay" readonly onclick="toggleKiroAuthMethodDropdown()">
                            <button type="button" class="model-dropdown-toggle" onclick="toggleKiroAuthMethodDropdown()">
                                <svg width="12" height="12" viewBox="0 0 12 12" fill="currentColor">
                                    <path d="M2 4L6 8L10 4" stroke="currentColor" stroke-width="2" fill="none"/>
                                </svg>
                            </button>
                            <div class="model-dropdown" id="kiroAuthMethodDropdown"></div>
                        </div>
                        <select id="kiroAuthMethod" style="display:none;" onchange="onKiroAuthMethodChange()">
                            <option value="idc">Idc</option>
                        </select>
                        <small>${t('kiro.authMethodHelp')}</small>
                    </div>
                    <div class="form-group" id="kiroIdcLoginButton"> style="display:none;">
                        <div style="display: flex; gap: 10px;">
                            <button type="button" class="btn btn-primary" onclick="startIdcDeviceFlowLogin()" style="flex: 1;">
                                ${t('kiro.idcLoginButton')}
                            </button>
                            <button type="button" class="btn btn-primary" onclick="startIdcOrgLogin()" style="flex: 1;">
                                ${t('kiro.idcOrgLoginButton')}
                            </button>
                        </div>
                        <small>${t('kiro.idcLoginButtonHelp')}</small>
                    </div>
                    <div class="form-group">
                        <label>${t('kiro.refreshToken')}</label>
                        <div class="model-input-wrapper">
                            <input type="text" id="kiroRefreshToken" placeholder="${t('kiro.refreshTokenPlaceholder')}" oninput="onKiroRefreshTokenInput()">
                            <button type="button" class="btn btn-sm btn-secondary" id="testKiroRefreshTokenBtn" onclick="testKiroRefreshToken()">
                                <span id="testKiroRefreshTokenBtnText">${t('kiro.test')}</span>
                            </button>
                        </div>
                        <small>${t('kiro.refreshTokenHelp')}</small>
                    </div>
                    <div class="form-group" id="kiroIdcFields" style="display:none;">
                        <label>${t('kiro.clientId')}</label>
                        <input type="text" id="kiroClientId" placeholder="${t('kiro.clientIdPlaceholder')}" oninput="onKiroIdcFieldsInput()">
                        <small>${t('kiro.clientIdHelp')}</small>
                    </div>
                    <div class="form-group" id="kiroIdcSecretField" style="display:none;">
                        <label>${t('kiro.clientSecret')}</label>
                        <div class="input-with-icon">
                            <input type="password" id="kiroClientSecret" placeholder="${t('kiro.clientSecretPlaceholder')}" oninput="onKiroIdcFieldsInput()">
                            <button type="button" class="input-icon-btn" id="toggleKiroClientSecretVisibility" onclick="toggleKiroClientSecretVisibility()" title="${t('manage.toggleVisibility')}">👁️</button>
                        </div>
                        <small>${t('kiro.clientSecretHelp')}</small>
                    </div>
                    <div class="form-group">
                        <label>${t('kiro.accessToken')}</label>
                        <div class="model-input-wrapper">
                            <input type="text" id="kiroAccessToken" placeholder="${t('kiro.accessTokenPlaceholder')}" readonly>
                            <button type="button" class="btn btn-sm btn-secondary" id="fetchKiroUsageBtn" onclick="fetchKiroUsage()" disabled>
                                <span id="fetchKiroUsageBtnText">${t('kiro.usage')}</span>
                            </button>
                        </div>
                        <small>${t('kiro.accessTokenHelp')}</small>
                        <small id="kiroUsageInfo" style="display:none;"></small>
                    </div>
                    <div class="form-group">
                        <label>${t('kiro.profileArn')}</label>
                        <input type="text" id="kiroProfileArn" placeholder="${t('kiro.profileArnPlaceholder')}" readonly>
                        <small>${t('kiro.profileArnHelp')}</small>
                    </div>
                    <div class="form-group">
                        <label>${t('kiro.region')}</label>
                        <div class="model-select-container kiro-region-select">
                            <input type="text" id="kiroRegion" placeholder="us-east-1" autocomplete="off" oninput="onKiroRegionInput()">
                            <button type="button" class="model-dropdown-toggle" onclick="toggleKiroRegionDropdown()">
                                <svg width="12" height="12" viewBox="0 0 12 12" fill="currentColor">
                                    <path d="M2 4L6 8L10 4" stroke="currentColor" stroke-width="2" fill="none"/>
                                </svg>
                            </button>
                            <div class="model-dropdown" id="kiroRegionDropdown"></div>
                        </div>
                    </div>
                    <div class="form-group">
                        <label>${t('kiro.proxyUrl')}</label>
                        <input type="text" id="kiroProxyUrl" placeholder="${t('kiro.proxyUrlPlaceholder')}">
                        <small>${t('kiro.proxyUrlHelp')}</small>
                    </div>
                    <div class="form-group">
                        <label>${t('kiro.userAgent')}</label>
                        <input type="text" id="kiroUserAgent" placeholder="${t('kiro.userAgentPlaceholder')}">
                        <small>${t('kiro.userAgentHelp')}</small>
                    </div>
                    <div class="form-group">
                        <label>${t('kiro.version')}</label>
                        <input type="text" id="kiroVersion" placeholder="${t('kiro.versionPlaceholder')}">
                        <small>${t('kiro.versionHelp')}</small>
                    </div>
                    <div class="form-group switch-form-group">
                        <label class="switch-label-inline">${t('kiro.bufferedStream')}</label>
                        <label class="switch">
                            <input type="checkbox" id="kiroBufferedStream">
                            <span class="slider"></span>
                        </label>
                        <small>${t('kiro.bufferedStreamHelp')}</small>
                    </div>
                </div>
                <div class="modal-footer">
                    <button class="btn btn-secondary" onclick="closeKiroConfigModal()">${t('settings.cancel')}</button>
                    <button class="btn btn-primary" id="saveKiroConfigBtn" onclick="saveKiroConfig()">${t('settings.save')}</button>
                </div>
            </div>
        </div>`
}

export function idcOrgLoginModalTemplate() {
  return `
        <div id="idcOrgLoginModal" class="modal">
            <div class="modal-content">
                <div class="modal-header">
                    <h2>${t('kiro.idcOrgDialogTitle')}</h2>
                    <button class="modal-close" onclick="closeIdcOrgLoginDialog()">&times;</button>
                </div>
                <div class="modal-body">
                    <!-- Step 1: Config -->
                    <div id="idcOrgConfigStep">
                        <div class="form-group">
                            <label>${t('kiro.idcOrgStartUrl')}</label>
                            <input type="text" id="idcOrgStartUrl" placeholder="${t('kiro.idcOrgStartUrlPlaceholder')}">
                            <small>${t('kiro.idcOrgStartUrlHelp')}</small>
                        </div>
                        <div class="form-group">
                            <label>${t('kiro.region')}</label>
                            <input type="text" id="idcOrgRegion" placeholder="us-east-1" value="us-east-1">
                            <small>${t('kiro.idcOrgRegionHelp')}</small>
                        </div>
                        <div class="form-group">
                            <button class="btn btn-primary" onclick="submitIdcOrgLogin()" style="width: 100%;">
                                ${t('kiro.idcOrgConnect')}
                            </button>
                        </div>
                    </div>

                    <!-- Step 2: Verification (Hidden initially) -->
                    <div id="idcOrgVerifyStep" style="display: none;">
                        <div class="form-group">
                            <label>${t('kiro.idcVerifyUrlLabel')}</label>
                            <div class="link-panel">
                                <div class="link-text" id="idcOrgVerifyUrl" title="">—</div>
                                <div class="link-actions">
                                    <button type="button" class="btn btn-sm btn-secondary" id="idcOrgCopyLinkBtn" onclick="copyIdcVerifyUrl()">
                                        ${t('kiro.idcCopyLink')}
                                    </button>
                                    <button type="button" class="btn btn-sm btn-secondary" id="idcOrgOpenLinkBtn" onclick="openIdcVerifyUrl()">
                                        ${t('kiro.idcOpenLink')}
                                    </button>
                                </div>
                            </div>
                            <small>${t('kiro.idcVerifyUrlHelp')}</small>
                        </div>
                        <div class="form-group">
                            <div class="status" id="idcOrgStatusBox">
                                <div>
                                    <div style="font-weight:700;font-size:13px">${t('kiro.idcStatusLabel')}</div>
                                    <div class="muted" id="idcOrgStatusText" style="margin-top:4px">${t('kiro.idcStatusIdle')}</div>
                                </div>
                                <div class="badge">
                                    <span class="dot" id="idcOrgStatusDot"></span>
                                    <span id="idcOrgStatusLabel">IDLE</span>
                                </div>
                            </div>
                        </div>
                        <div class="form-group">
                            <small class="muted">${t('kiro.idcDialogHelp')}</small>
                        </div>
                    </div>
                </div>
                <div class="modal-footer">
                    <button class="btn btn-secondary" id="idcOrgBackBtn" onclick="backToOrgLoginStep1()" style="display:none;">${t('kiro.idcOrgBack')}</button>
                    <button class="btn btn-secondary" onclick="closeIdcOrgLoginDialog()">${t('kiro.idcDialogClose')}</button>
                </div>
            </div>
        </div>`
}

export function idcDeviceFlowModalTemplate() {
  return `
        <div id="idcDeviceFlowModal" class="modal">
            <div class="modal-content">
                <div class="modal-header">
                    <h2>${t('kiro.idcDialogTitle')}</h2>
                    <button class="modal-close" onclick="closeIdcDeviceFlowDialog()">&times;</button>
                </div>
                <div class="modal-body">
                    <div class="form-group">
                        <label>${t('kiro.idcVerifyUrlLabel')}</label>
                        <div class="link-panel">
                            <div class="link-text" id="idcVerifyUrl" title="">—</div>
                            <div class="link-actions">
                                <button type="button" class="btn btn-sm btn-secondary" id="idcCopyLinkBtn" onclick="copyIdcVerifyUrl()" disabled>
                                    ${t('kiro.idcCopyLink')}
                                </button>
                                <button type="button" class="btn btn-sm btn-secondary" id="idcOpenLinkBtn" onclick="openIdcVerifyUrl()" disabled>
                                    ${t('kiro.idcOpenLink')}
                                </button>
                            </div>
                        </div>
                        <small>${t('kiro.idcVerifyUrlHelp')}</small>
                    </div>
                    <div class="form-group">
                        <div class="status" id="idcStatusBox">
                            <div>
                                <div style="font-weight:700;font-size:13px">${t('kiro.idcStatusLabel')}</div>
                                <div class="muted" id="idcStatusText" style="margin-top:4px">${t('kiro.idcStatusIdle')}</div>
                            </div>
                            <div class="badge">
                                <span class="dot" id="idcStatusDot"></span>
                                <span id="idcStatusLabel">IDLE</span>
                            </div>
                        </div>
                    </div>
                    <div class="form-group">
                        <small class="muted">${t('kiro.idcDialogHelp')}</small>
                    </div>
                </div>
                <div class="modal-footer">
                    <button class="btn btn-secondary" onclick="closeIdcDeviceFlowDialog()">${t('kiro.idcDialogClose')}</button>
                </div>
            </div>
        </div>`
}

export function kiroGlobalConfigModalTemplate() {
  return `
        <div id="kiroGlobalConfigModal" class="modal">
            <div class="modal-content" style="max-width: 600px;">
                <div class="modal-header">
                    <h2>${t('kiro.globalConfig')}</h2>
                    <button class="modal-close" onclick="closeKiroGlobalConfigModal()">&times;</button>
                </div>
                <div class="modal-body">
                    <div class="form-group">
                        <label>${t('kiro.region')}</label>
                        <div class="model-select-container kiro-global-region-select">
                            <input type="text" id="kiroGlobalRegion" placeholder="us-east-1" autocomplete="off" oninput="onKiroGlobalRegionInput()">
                            <button type="button" class="model-dropdown-toggle" onclick="toggleKiroGlobalRegionDropdown()">
                                <svg width="12" height="12" viewBox="0 0 12 12" fill="currentColor">
                                    <path d="M2 4L6 8L10 4" stroke="currentColor" stroke-width="2" fill="none"/>
                                </svg>
                            </button>
                            <div class="model-dropdown" id="kiroGlobalRegionDropdown"></div>
                        </div>
                    </div>
                    <div class="form-group">
                        <label>${t('kiro.proxyUrl')}</label>
                        <input type="text" id="kiroGlobalProxyUrl" placeholder="${t('kiro.proxyUrlPlaceholder')}">
                    </div>
                    <div class="form-group">
                        <label>${t('kiro.userAgent')}</label>
                        <input type="text" id="kiroGlobalUserAgent" placeholder="${t('kiro.userAgentPlaceholder')}">
                    </div>
                    <div class="form-group">
                        <label>${t('kiro.version')}</label>
                        <input type="text" id="kiroGlobalVersion" placeholder="${t('kiro.versionPlaceholder')}">
                    </div>
                    <div class="form-group switch-form-group">
                        <label class="switch-label-inline">${t('kiro.bufferedStream')}</label>
                        <label class="switch">
                            <input type="checkbox" id="kiroGlobalBufferedStream">
                            <span class="slider"></span>
                        </label>
                        <small>${t('kiro.bufferedStreamHelp')}</small>
                    </div>
                    <div class="form-group">
                        <label>${t('kiro.rotationMode')}</label>
                        <div class="model-select-container kiro-rotation-mode-select">
                            <input type="text" id="kiroRotationModeDisplay" readonly onclick="toggleKiroRotationModeDropdown()">
                            <button type="button" class="model-dropdown-toggle" onclick="toggleKiroRotationModeDropdown()">
                                <svg width="12" height="12" viewBox="0 0 12 12" fill="currentColor">
                                    <path d="M2 4L6 8L10 4" stroke="currentColor" stroke-width="2" fill="none"/>
                                </svg>
                            </button>
                            <div class="model-dropdown" id="kiroRotationModeDropdown"></div>
                        </div>
                        <select id="kiroRotationMode" style="display:none;" onchange="onKiroRotationModeChange()">
                            <option value="fixed">${t('kiro.rotationFixed')}</option>
                            <option value="failover">${t('kiro.rotationFailover')}</option>
                            <option value="loadbalance">${t('kiro.rotationLoadBalance')}</option>
                        </select>
                        <small>${t('kiro.rotationModeHelp')}</small>
                    </div>
                    <hr style="margin: 16px 0; border: none; border-top: 1px solid var(--border);">
                    <div class="form-group">
                        <label>${t('kiro.modelMapping')}</label>
                        <small style="display:block;margin-bottom:8px;">${t('kiro.modelMappingHelp')}</small>
                        <div id="kiroModelMappingList"></div>
                        <div style="margin-top: 8px; display: flex; gap: 8px;">
                            <button type="button" class="btn btn-sm btn-secondary" onclick="addKiroModelMappingRow()">
                                ${t('kiro.addMapping')}
                            </button>
                            <button type="button" class="btn btn-sm btn-secondary" onclick="resetKiroModelMappingDefaults()">
                                ${t('kiro.resetDefaults')}
                            </button>
                        </div>
                    </div>
                </div>
                <div class="modal-footer">
                    <button class="btn btn-secondary" onclick="closeKiroGlobalConfigModal()">${t('settings.cancel')}</button>
                    <button class="btn btn-primary" onclick="saveKiroGlobalConfig()">${t('settings.save')}</button>
                </div>
            </div>
        </div>`
}

export function kiroSignLoginModalTemplate() {
  return `
        <div id="kiroSignLoginModal" class="modal">
            <div class="modal-content">
                <div class="modal-header">
                    <h2>${t('kiro.kiroSignLoginTitle')}</h2>
                    <button class="modal-close" onclick="closeKiroSignLoginModal()">&times;</button>
                </div>
                <div class="modal-body">
                    <div class="form-group" style="text-align: center; padding: 20px 0;">
                        <div class="loading-spinner"></div>
                        <p style="margin-top: 15px;">${t('kiro.kiroSignLoginWaiting')}</p>
                        <small class="muted">${t('kiro.kiroSignLoginInstruction')}</small>
                    </div>

                    <div class="form-group">
                        <label>${t('kiro.loginUrlLabel')}</label>
                        <div class="link-panel">
                            <div class="link-text" id="kiroSignLoginUrl" title="">—</div>
                            <div class="link-actions">
                                <button type="button" class="btn btn-sm btn-secondary" onclick="copyKiroSignLoginUrl()">
                                    ${t('kiro.copyLink')}
                                </button>
                                <button type="button" class="btn btn-sm btn-secondary" onclick="openKiroSignLoginUrl()">
                                    ${t('kiro.openLink')}
                                </button>
                            </div>
                        </div>
                    </div>
                </div>
                <div class="modal-footer">
                    <button class="btn btn-secondary" onclick="closeKiroSignLoginModal()">${t('settings.cancel')}</button>
                </div>
            </div>
        </div>`
}

export function kiroAccountEditModalTemplate() {
  return `
        <div id="kiroAccountEditModal" class="modal">
            <div class="modal-content" style="max-width: 560px;">
                <div class="modal-header">
                    <h2>${t('kiro.editAccountTitle')}</h2>
                    <button class="modal-close" onclick="closeKiroAccountEditModal()">&times;</button>
                </div>
                <div class="modal-body">
                    <div class="form-group">
                        <label>${t('kiro.refreshToken')}</label>
                        <input type="text" id="editKiroRefreshToken" readonly style="opacity:.7;">
                    </div>
                    <div class="form-group">
                        <label>${t('kiro.region')}</label>
                        <div class="model-select-container edit-kiro-region-select">
                            <input type="text" id="editKiroRegion" placeholder="us-east-1" autocomplete="off" oninput="onEditKiroRegionInput()">
                            <button type="button" class="model-dropdown-toggle" onclick="toggleEditKiroRegionDropdown()">
                                <svg width="12" height="12" viewBox="0 0 12 12" fill="currentColor">
                                    <path d="M2 4L6 8L10 4" stroke="currentColor" stroke-width="2" fill="none"/>
                                </svg>
                            </button>
                            <div class="model-dropdown" id="editKiroRegionDropdown"></div>
                        </div>
                    </div>
                    <div class="form-group">
                        <label>${t('kiro.authMethod')}</label>
                        <div class="model-select-container edit-kiro-auth-method-select">
                            <input type="text" id="editKiroAuthMethodDisplay" readonly onclick="toggleEditKiroAuthMethodDropdown()">
                            <button type="button" class="model-dropdown-toggle" onclick="toggleEditKiroAuthMethodDropdown()">
                                <svg width="12" height="12" viewBox="0 0 12 12" fill="currentColor">
                                    <path d="M2 4L6 8L10 4" stroke="currentColor" stroke-width="2" fill="none"/>
                                </svg>
                            </button>
                            <div class="model-dropdown" id="editKiroAuthMethodDropdown"></div>
                        </div>
                        <select id="editKiroAuthMethod" style="display:none;" onchange="onEditKiroAuthMethodChange()">
                            <option value="idc">Idc</option>
                        </select>
                    </div>
                    <div class="form-group">
                        <label>${t('kiro.provider')}</label>
                        <input type="text" id="editKiroProvider" placeholder="${t('kiro.providerPlaceholder') || 'Google, Github, BuilderId, Enterprise'}">
                        <small>${t('kiro.providerHelp') || 'Provider name (e.g., Google, Github, BuilderId, Enterprise)'}</small>
                    </div>
                    <div class="form-group" id="editKiroProfileArnGroup">
                        <label>${t('kiro.profileArn')}</label>
                        <input type="text" id="editKiroProfileArn" placeholder="${t('kiro.profileArnPlaceholder')}">
                    </div>
                    <div class="form-group" id="editKiroClientIdGroup" style="display:none;">
                        <label>${t('kiro.clientId')}</label>
                        <input type="text" id="editKiroClientId" placeholder="${t('kiro.clientIdPlaceholder')}">
                    </div>
                    <div class="form-group" id="editKiroClientSecretGroup" style="display:none;">
                        <label>${t('kiro.clientSecret')}</label>
                        <div class="input-with-icon">
                            <input type="password" id="editKiroClientSecret" placeholder="${t('kiro.clientSecretPlaceholder')}">
                            <button type="button" class="input-icon-btn" onclick="toggleEditKiroClientSecretVisibility()" title="${t('manage.toggleVisibility')}">&#128065;&#65039;</button>
                        </div>
                    </div>
                    <hr style="margin: 12px 0; border: none; border-top: 1px solid var(--border);">
                    <small style="display:block;margin-bottom:8px;color:var(--text-secondary);">${t('kiro.perAccountConfig')}</small>
                    <div class="form-group">
                        <label>${t('kiro.proxyUrl')}</label>
                        <input type="text" id="editKiroProxyUrl" placeholder="${t('kiro.proxyUrlPlaceholder')}">
                    </div>
                    <div class="form-group">
                        <label>${t('kiro.userAgent')}</label>
                        <input type="text" id="editKiroUserAgent" placeholder="${t('kiro.userAgentPlaceholder')}">
                    </div>
                    <div class="form-group">
                        <label>${t('kiro.version')}</label>
                        <input type="text" id="editKiroVersion" placeholder="${t('kiro.versionPlaceholder')}">
                    </div>
                    <div class="form-group">
                        <label>${t('kiro.weight')}</label>
                        <input type="number" id="editKiroWeight" min="1" placeholder="1">
                        <small>${t('kiro.weightHelp')}</small>
                    </div>
                    <div class="form-group">
                        <label>MachineId</label>
                        <input type="text" id="editKiroMachineId" placeholder="Optional unique machine identifier">
                    </div>
                </div>
                <div class="modal-footer">
                    <button class="btn btn-secondary" onclick="closeKiroAccountEditModal()">${t('settings.cancel')}</button>
                    <button class="btn btn-primary" onclick="saveKiroAccountEdit()">${t('settings.save')}</button>
                </div>
            </div>
        </div>`
}
