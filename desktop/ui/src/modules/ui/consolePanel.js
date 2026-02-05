import { t } from '../../i18n/index.js'

export function consolePanelTemplate() {
    return `
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
        </div>`
}
