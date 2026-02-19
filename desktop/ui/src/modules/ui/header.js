import { t } from '../../i18n/index.js'
import { createIcon } from '../icons.js'

export function headerTemplate() {
  return `
        <div class="header">
            <div class="header-content">
                <div class="header-left">
                    <div class="header-tabs">
                        <button class="header-tab active" data-tab="home" onclick="switchMainTab('home')">${createIcon('home', { size: 14 })} ${t('header.home')}</button>
                        <button class="header-tab" data-tab="kiro-accounts" onclick="switchMainTab('kiro-accounts')">${createIcon('users', { size: 14 })} ${t('kiro.accountsTitle')}</button>
                        <button class="header-tab" data-tab="codex-accounts" onclick="switchMainTab('codex-accounts')">${createIcon('users', { size: 14 })} Codex</button>
                        <button class="header-tab" data-tab="xray" onclick="switchMainTab('xray')">${createIcon('network', { size: 14 })} 科学</button>
                    </div>
                </div>
                <div class="header-right">
                    <div class="header-btn-group">
                        <button class="header-btn" style="display:none;" onclick="showKiroConfigModal()" title="${t('kiro.title')}">Kiro</button>
                        <button class="header-btn" onclick="showWebDAVModal()" title="${t('webdav.title')}">${createIcon('cloudUpload', { size: 14 })}</button>
                        <button class="header-btn" onclick="showSettingsModal()" title="${t('settings.title')}">${createIcon('settings', { size: 14 })}</button>
                        <button class="header-btn" id="consoleToggleBtn" onclick="toggleBottomConsole()" title="${t('console.title')}">${createIcon('terminal', { size: 14 })}</button>
                    </div>
                </div>
            </div>
        </div>`
}
