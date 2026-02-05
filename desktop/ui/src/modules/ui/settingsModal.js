import { state } from '../state.js'
import { t, getAvailableLanguages } from '../../i18n/index.js'

export function settingsModalTemplate() {
    return `
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
                            ${getAvailableLanguages()
                              .map(
                                (lang) =>
                                  `<button class="lang-tab ${state.language === lang.code ? 'active' : ''}"
                                    data-lang="${lang.code}" onclick="changeLanguage('${lang.code}')">${lang.name}</button>`,
                              )
                              .join('')}
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
                        <label>${t('settings.debugMode')}</label>
                        <div class="model-select-container standalone-select">
                            <input type="text" id="settingsDebugModeDisplay" readonly onclick="toggleDebugModeDropdown()">
                            <button type="button" class="model-dropdown-toggle" onclick="toggleDebugModeDropdown()">
                                <svg width="12" height="12" viewBox="0 0 12 12" fill="currentColor">
                                    <path d="M2 4L6 8L10 4" stroke="currentColor" stroke-width="2" fill="none"/>
                                </svg>
                            </button>
                            <div class="model-dropdown" id="debugModeDropdown"></div>
                        </div>
                        <select id="settingsDebugMode" style="display:none;">
                            <option value="">${t('settings.debugModeNone')}</option>
                            <option value="db">${t('settings.debugModeDb')}</option>
                            <option value="file">${t('settings.debugModeFile')}</option>
                        </select>
                        <small>${t('settings.debugModeHelp')}</small>
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
        </div>`
}
