import { t } from '../../i18n/index.js'
import { createIcon } from '../icons.js'

export function codexGlobalConfigModalTemplate() {
  return `
        <div id="codexGlobalConfigModal" class="modal">
            <div class="modal-content" style="max-width: 600px;">
                <div class="modal-header">
                    <h2>${t('codex.configModalTitle')}</h2>
                    <button class="modal-close" onclick="closeCodexGlobalConfigModal()">&times;</button>
                </div>
                <div class="modal-body">
                    <div class="form-group">
                        <label>${t('codex.rotationModeLabel')}</label>
                        <div class="model-select-container codex-rotation-mode-select">
                            <input type="text" id="codexRotationModeDisplay" readonly onclick="toggleCodexRotationModeDropdown()">
                            <button type="button" class="model-dropdown-toggle" onclick="toggleCodexRotationModeDropdown()">
                                <svg width="12" height="12" viewBox="0 0 12 12" fill="currentColor">
                                    <path d="M2 4L6 8L10 4" stroke="currentColor" stroke-width="2" fill="none"/>
                                </svg>
                            </button>
                            <div class="model-dropdown" id="codexRotationModeDropdown"></div>
                        </div>
                        <select id="codexRotationMode" style="display:none;">
                            <option value="fixed">${t('codex.rotationModeFixed')}</option>
                            <option value="failover">${t('codex.rotationModeFailover')}</option>
                            <option value="loadbalance">${t('codex.rotationModeLoadBalance')}</option>
                        </select>
                        <small>${t('codex.rotationModeHelp2')}</small>
                    </div>
                    <div class="form-group">
                        <label>${t('codex.pluginProxyUrl')}</label>
                        <input type="text" id="codexGlobalProxyUrl" placeholder="${t('codex.pluginProxyUrlPlaceholder')}">
                        <small>${t('codex.pluginProxyUrlHelp')}</small>
                    </div>
                    <hr style="margin: 16px 0; border: none; border-top: 1px solid var(--border);">
                    <div class="form-group">
                        <label>${t('codex.baseUrl')}</label>
                        <input type="text" id="codexBaseURL" placeholder="${t('codex.baseUrlPlaceholder')}">
                        <small>${t('codex.baseUrlHelp')}</small>
                    </div>
                    <div class="form-group">
                        <label>${t('codex.clientVersion')}</label>
                        <input type="text" id="codexClientVersion" placeholder="${t('codex.clientVersionPlaceholder')}">
                        <small>${t('codex.clientVersionHelp')}</small>
                    </div>
                    <div class="form-group">
                        <label>${t('codex.userAgent')}</label>
                        <input type="text" id="codexUserAgent" placeholder="${t('codex.userAgentPlaceholder')}">
                        <small>${t('codex.userAgentHelp')}</small>
                    </div>
                    <div class="form-group">
                        <label>${t('codex.originator')}</label>
                        <input type="text" id="codexOriginator" placeholder="${t('codex.originatorPlaceholder')}">
                        <small>${t('codex.originatorHelp')}</small>
                    </div>
                </div>
                <div class="modal-footer">
                    <button class="btn btn-secondary" onclick="closeCodexGlobalConfigModal()">${t('settings.cancel')}</button>
                    <button class="btn btn-primary" onclick="saveCodexGlobalConfig()">${t('settings.save')}</button>
                </div>
            </div>
        </div>`
}

export function codexAccountEditModalTemplate() {
  return `
        <div id="codexAccountEditModal" class="modal">
            <div class="modal-content" style="max-width: 500px;">
                <div class="modal-header">
                    <h2>${t('codex.editAccountModalTitle')}</h2>
                    <button class="modal-close" onclick="closeCodexAccountEditModal()">&times;</button>
                </div>
                <div class="modal-body">
                    <div class="form-group">
                        <label>${t('codex.refreshTokenLabel')}</label>
                        <input type="text" id="editCodexRefreshToken" readonly style="opacity:.7;">
                    </div>
                    <div class="form-group">
                        <label>${t('codex.passwordLabel')}</label>
                        <input type="text" id="editCodexPassword" placeholder="${t('codex.passwordPlaceholder')}">
                        <small>${t('codex.passwordHelp2')}</small>
                    </div>
                    <div class="form-group">
                        <label>${t('codex.mfaCodeLabel')}</label>
                        <input type="text" id="editCodexMFACode" placeholder="${t('codex.mfaCodePlaceholder')}">
                        <small>${t('codex.mfaCodeHelp2')}</small>
                    </div>
                    <hr style="margin: 12px 0; border: none; border-top: 1px solid var(--border);">
                    <div class="form-group">
                        <label>${t('codex.proxyUrlLabel')}</label>
                        <input type="text" id="editCodexProxyUrl" placeholder="${t('codex.pluginProxyUrlPlaceholder')}">
                        <small>${t('codex.proxyUrlHelp2')}</small>
                    </div>
                    <div class="form-group">
                        <label>${t('codex.weightLabel')}</label>
                        <input type="number" id="editCodexWeight" min="1" placeholder="1">
                        <small>${t('codex.weightHelp2')}</small>
                    </div>
                </div>
                <div class="modal-footer">
                    <button class="btn btn-secondary" onclick="closeCodexAccountEditModal()">${t('settings.cancel')}</button>
                    <button class="btn btn-primary" onclick="saveCodexAccountEdit()">${t('settings.save')}</button>
                </div>
            </div>
        </div>`
}

export function codexOAuthLoginModalTemplate() {
  const copyIcon = createIcon('copy', { size: 16 })
  const externalLinkIcon = createIcon('externalLink', { size: 16 })
  const eyeOffIcon = createIcon('eyeOff', { size: 16 })

  return `
        <div id="codexOAuthLoginModal" class="modal">
            <div class="modal-content" style="max-width: 600px;">
                <div class="modal-header">
                    <h2>${t('codex.oauthLoginModalTitle')}</h2>
                    <button class="modal-close" onclick="closeCodexOAuthLoginModal()">&times;</button>
                </div>
                <div class="modal-body" style="padding: 24px;">
                    <div class="form-group">
                        <label style="font-weight: 600; margin-bottom: 8px; display: block;">${t('codex.loginUrlLabel')}</label>
                        <div style="display: flex; gap: 8px; align-items: center;">
                            <input type="text" id="codexOAuthLoginUrl" readonly
                                   style="flex: 1; font-family: monospace; font-size: 12px; padding: 10px; background: var(--bg-secondary); border: 1px solid var(--border-color); border-radius: 4px; cursor: text;"
                                   onclick="this.select()">
                            <button class="btn btn-sm btn-secondary" onclick="copyCodexOAuthLoginUrl()" title="${t('codex.copyLink')}">
                                ${copyIcon}
                            </button>
                            <button class="btn btn-sm btn-primary" onclick="openCodexOAuthLoginUrl(false)" title="${t('codex.openLink')}">
                                ${externalLinkIcon}
                            </button>
                            <button class="btn btn-sm btn-primary" onclick="openCodexOAuthLoginUrl(true)" title="${t('codex.openIncognito')}">
                                ${eyeOffIcon}
                            </button>
                        </div>
                        <small style="color: var(--text-tertiary); margin-top: 8px; display: block;">${t('codex.oauthLoginHelp')}</small>
                    </div>

                    <div style="margin-top: 20px; padding: 16px; background: var(--bg-secondary); border-radius: 8px; border-left: 4px solid var(--primary-color);">
                        <div style="display: flex; align-items: center; gap: 8px; margin-bottom: 8px;">
                            <div class="spinner" style="width: 16px; height: 16px; border-width: 2px;"></div>
                            <span style="font-weight: 600; color: var(--text-primary);" id="codexOAuthLoginStatus">${t('codex.oauthLoginWaiting')}</span>
                        </div>
                        <p style="margin: 0; font-size: 13px; color: var(--text-secondary);" id="codexOAuthLoginInstruction">${t('codex.oauthLoginInstruction')}</p>
                    </div>
                </div>
                <div class="modal-footer">
                    <button class="btn btn-secondary" onclick="closeCodexOAuthLoginModal()">${t('common.close')}</button>
                </div>
            </div>
        </div>`
}
