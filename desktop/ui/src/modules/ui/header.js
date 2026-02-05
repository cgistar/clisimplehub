import { t } from '../../i18n/index.js'

export function headerTemplate() {
    return `
        <div class="header">
            <div class="header-content">
                <div class="header-left">
                    <div class="header-tabs">
                        <button class="header-tab active" data-tab="home">🏠 ${t('header.home')}</button>
                    </div>
                </div>
                <div class="header-right">
                    <div class="header-btn-group">
                        <button class="header-btn" onclick="showKiroConfigModal()" title="${t('kiro.title')}">Kiro</button>
                        <button class="header-btn" onclick="showWebDAVModal()" title="${t('webdav.title')}">☁️</button>
                        <button class="header-btn" onclick="showSettingsModal()" title="${t('settings.title')}">⚙️</button>
                    </div>
                </div>
            </div>
        </div>`
}
