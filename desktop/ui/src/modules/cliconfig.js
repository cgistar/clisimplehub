/**
 * CLI Config Editor module for Claude Code and Codex
 */
import { state } from './state.js';
import { t } from '../i18n/index.js';
import { showError, showSuccess } from './utils.js';

// Current editor state
let currentEditorType = null; // 'claude' or 'codex'
let editorFiles = {};
let localIPs = []; // Store local IP addresses

/**
 * Check if CLI config editor should be shown for current tab
 */
export function shouldShowCLIConfigEditor() {
    return state.currentTab === 'claude' || state.currentTab === 'codex';
}

/**
 * Update CLI config editor button visibility
 */
export function updateCLIConfigEditorButton() {
    const btn = document.getElementById('cliConfigEditorBtn');
    if (btn) {
        btn.style.display = shouldShowCLIConfigEditor() ? 'inline-flex' : 'none';
    }
}

/**
 * Open CLI config editor modal
 */
export async function openCLIConfigEditor() {
    currentEditorType = state.currentTab;
    
    if (currentEditorType !== 'claude' && currentEditorType !== 'codex') {
        return;
    }
    
    try {
        if (currentEditorType === 'claude') {
            await loadClaudeConfig();
        } else {
            await loadCodexConfig();
        }
        
        document.getElementById('cliConfigModal').classList.add('active');
    } catch (error) {
        showError(t('cliConfig.loadFailed') + ': ' + error.message);
    }
}

/**
 * Close CLI config editor modal
 */
export function closeCLIConfigEditor() {
    document.getElementById('cliConfigModal').classList.remove('active');
    currentEditorType = null;
    editorFiles = {};
}

/**
 * Load Claude Code config
 */
async function loadClaudeConfig() {
    if (!window.go?.main?.App?.GetClaudeConfig) {
        throw new Error('Backend not available');
    }

    // Get local IPs
    localIPs = await window.go.main.App.GetLocalIPs();

    const result = await window.go.main.App.GetClaudeConfig();
    if (!result.success) {
        throw new Error(result.message);
    }

    editorFiles = {};
    result.files.forEach(f => {
        editorFiles[f.name] = f.content;
    });

    await renderCLIConfigEditor('claude', result.files);
}

/**
 * Load Codex config
 */
async function loadCodexConfig() {
    if (!window.go?.main?.App?.GetCodexConfig) {
        throw new Error('Backend not available');
    }

    // Get local IPs
    localIPs = await window.go.main.App.GetLocalIPs();

    const result = await window.go.main.App.GetCodexConfig();
    if (!result.success) {
        throw new Error(result.message);
    }

    editorFiles = {};
    result.files.forEach(f => {
        editorFiles[f.name] = f.content;
    });

    await renderCLIConfigEditor('codex', result.files);
}

/**
 * Render CLI config editor content
 */
async function renderCLIConfigEditor(type, files) {
    const modal = document.getElementById('cliConfigModal');
    const title = type === 'claude' ? 'Claude Code' : 'Codex';

    // Get saved listen address from settings
    let savedListenAddr = '127.0.0.1'; // Default
    try {
        const settings = await window.go.main.App.GetSettings();
        if (settings && settings.listenAddr) {
            savedListenAddr = settings.listenAddr;
        }
    } catch (error) {
        console.error('Failed to get settings:', error);
    }

    // Generate IP options with saved IP selected
    let ipOptionsHtml = localIPs.map(ip => {
        const label = ip.interface === 'localhost'
            ? `${ip.ip} (${t('cliConfig.localhost')})`
            : `${ip.ip} (${ip.interface})`;
        const selected = ip.ip === savedListenAddr ? ' selected' : '';
        return `<option value="${ip.ip}"${selected}>${label}</option>`;
    }).join('');

    let editorsHtml = '';
    files.forEach((file, index) => {
        const isJson = file.name.endsWith('.json');
        const isToml = file.name.endsWith('.toml');
        const lang = isJson ? 'json' : (isToml ? 'toml' : 'text');

        const proxyStatus = file.isProxyConfigured ? '✅' : '❌';
        const proxyStatusClass = file.isProxyConfigured ? 'proxy-configured' : 'proxy-not-configured';

        editorsHtml += `
            <div class="cli-config-file">
                <div class="cli-config-file-header">
                    <span class="cli-config-file-name">${file.name}</span>
                    <span class="cli-config-file-status ${proxyStatusClass}" data-filename="${file.name}">${proxyStatus} ${file.isProxyConfigured ? t('cliConfig.proxyConfigured') : t('cliConfig.proxyNotConfigured')}</span>
                </div>
                <textarea id="cliConfigEditor_${index}" class="cli-config-textarea" data-filename="${file.name}" data-lang="${lang}" spellcheck="false">${escapeHtml(file.content)}</textarea>
            </div>
        `;
    });

    modal.innerHTML = `
        <div class="modal-content modal-large">
            <div class="modal-header">
                <h2>⚙️ ${title} ${t('cliConfig.title')}</h2>
                <button class="modal-close" onclick="closeCLIConfigEditor()">&times;</button>
            </div>
            <div class="modal-body cli-config-body">
                <div class="cli-config-ip-selector">
                    <label>${t('cliConfig.selectIP')}</label>
                    <select id="cliConfigIPSelect" class="ip-select" onchange="updateProxyStatus()">
                        ${ipOptionsHtml}
                    </select>
                    <small class="ip-select-hint">${t('cliConfig.selectIPHint')}</small>
                </div>
                ${editorsHtml}
            </div>
            <div class="modal-footer">
                <div class="cli-footer-spacer"></div>
                <div class="cli-footer-actions">
                    <button class="btn btn-secondary" onclick="processCLIConfig()" title="${t('cliConfig.processHelp')}">🔄 ${t('cliConfig.process')}</button>
                    <button class="btn btn-primary" onclick="saveCLIConfig()">💾 ${t('cliConfig.save')}</button>
                </div>
            </div>
        </div>
    `;

    // Initial proxy status update
    updateProxyStatus();
}

/**
 * Save CLI config
 */
export async function saveCLIConfig() {
    try {
        const textareas = document.querySelectorAll('.cli-config-textarea');
        const files = {};

        textareas.forEach(ta => {
            files[ta.dataset.filename] = ta.value;
        });

        if (currentEditorType === 'claude') {
            // Validate JSON
            try {
                JSON.parse(files['settings.json']);
            } catch (e) {
                showError(t('cliConfig.invalidJson') + ': settings.json');
                return;
            }

            await window.go.main.App.SaveClaudeConfig(files['settings.json']);
        } else if (currentEditorType === 'codex') {
            // Validate JSON for auth.json
            try {
                JSON.parse(files['auth.json']);
            } catch (e) {
                showError(t('cliConfig.invalidJson') + ': auth.json');
                return;
            }

            await window.go.main.App.SaveCodexConfig(files['config.toml'], files['auth.json']);
        }

        // Save listen address based on selected IP
        const ipSelect = document.getElementById('cliConfigIPSelect');
        if (ipSelect && ipSelect.value) {
            const selectedIP = ipSelect.value;
            let listenAddr;

            // If 127.0.0.1 is selected, save as 127.0.0.1
            // Otherwise, save as 0.0.0.0 (listen on all interfaces)
            if (selectedIP === '127.0.0.1') {
                listenAddr = '127.0.0.1';
            } else if (selectedIP === '::1') {
                listenAddr = '::1';
            } else {
                // For any other IP (including LAN IPs), save as 0.0.0.0
                listenAddr = '0.0.0.0';
            }

            try {
                await window.go.main.App.SaveListenAddr(listenAddr);
            } catch (error) {
                console.error('Failed to save listen address:', error);
                // Don't fail the entire save operation if listen address save fails
            }
        }

        showSuccess(t('cliConfig.saveSuccess'));
        closeCLIConfigEditor();
    } catch (error) {
        showError(t('cliConfig.saveFailed') + ': ' + error.message);
    }
}

/**
 * Process CLI config with proxy settings
 */
export async function processCLIConfig() {
    try {
        // Get selected IP from dropdown
        const ipSelect = document.getElementById('cliConfigIPSelect');
        if (!ipSelect) {
            showError(t('cliConfig.processFailed') + ': IP selector not found');
            return;
        }

        const selectedIP = ipSelect.value;
        if (!selectedIP) {
            showError(t('cliConfig.processFailed') + ': No IP selected');
            return;
        }

        const textareas = document.querySelectorAll('.cli-config-textarea');
        const files = {};

        textareas.forEach(ta => {
            files[ta.dataset.filename] = ta.value;
        });

        if (currentEditorType === 'claude') {
            const processed = await window.go.main.App.ProcessClaudeConfigWithIP(files['settings.json'], selectedIP);

            // Update editorFiles with processed content
            editorFiles['settings.json'] = processed;

            // Update textarea directly without re-rendering modal
            const ta = document.querySelector('.cli-config-textarea[data-filename="settings.json"]');
            if (ta) {
                // Store cursor position
                const cursorPos = ta.selectionStart;
                const scrollPos = ta.scrollTop;

                // Update content
                ta.value = processed;

                // Force browser to recognize the change
                ta.dispatchEvent(new Event('input', { bubbles: true }));
                ta.dispatchEvent(new Event('change', { bubbles: true }));

                // Restore cursor and scroll position
                ta.selectionStart = cursorPos;
                ta.selectionEnd = cursorPos;
                ta.scrollTop = scrollPos;
            } else {
                showError(t('cliConfig.processFailed') + ': Textarea element not found');
                return;
            }
        } else if (currentEditorType === 'codex') {
            const result = await window.go.main.App.ProcessCodexConfigWithIP(files['config.toml'], files['auth.json'], selectedIP);

            // Update editorFiles with processed content
            editorFiles['config.toml'] = result.configToml;
            editorFiles['auth.json'] = result.authJson;

            // Update textareas directly without re-rendering modal
            const configTa = document.querySelector('.cli-config-textarea[data-filename="config.toml"]');
            const authTa = document.querySelector('.cli-config-textarea[data-filename="auth.json"]');

            if (configTa) {
                const cursorPos = configTa.selectionStart;
                const scrollPos = configTa.scrollTop;
                configTa.value = result.configToml;
                configTa.dispatchEvent(new Event('input', { bubbles: true }));
                configTa.selectionStart = cursorPos;
                configTa.selectionEnd = cursorPos;
                configTa.scrollTop = scrollPos;
            }
            if (authTa) {
                const cursorPos = authTa.selectionStart;
                const scrollPos = authTa.scrollTop;
                authTa.value = result.authJson;
                authTa.dispatchEvent(new Event('input', { bubbles: true }));
                authTa.selectionStart = cursorPos;
                authTa.selectionEnd = cursorPos;
                authTa.scrollTop = scrollPos;
            }
        }

        // Update proxy status after processing
        updateProxyStatus();

        showSuccess(t('cliConfig.processSuccess'));
    } catch (error) {
        showError(t('cliConfig.processFailed') + ': ' + error.message);
    }
}

/**
 * Update proxy configuration status based on selected IP
 */
export async function updateProxyStatus() {
    try {
        const ipSelect = document.getElementById('cliConfigIPSelect');
        if (!ipSelect) return;

        const selectedIP = ipSelect.value;
        if (!selectedIP) return;

        // Get current settings to build proxy URL
        const settings = await window.go.main.App.GetSettings();
        const port = settings.port || 5600;

        // Build proxy URL based on editor type
        let proxyURL;
        if (currentEditorType === 'claude') {
            proxyURL = `http://${selectedIP}:${port}`;
        } else if (currentEditorType === 'codex') {
            proxyURL = `http://${selectedIP}:${port}/v1`;
        }

        // Check each file's content for proxy configuration
        const textareas = document.querySelectorAll('.cli-config-textarea');
        textareas.forEach(ta => {
            const filename = ta.dataset.filename;
            const content = ta.value;
            const isConfigured = content.includes(proxyURL);

            // Update status display
            const statusElement = document.querySelector(`.cli-config-file-status[data-filename="${filename}"]`);
            if (statusElement) {
                if (isConfigured) {
                    statusElement.className = 'cli-config-file-status proxy-configured';
                    statusElement.textContent = `✅ ${t('cliConfig.proxyConfigured')}`;
                } else {
                    statusElement.className = 'cli-config-file-status proxy-not-configured';
                    statusElement.textContent = `❌ ${t('cliConfig.proxyNotConfigured')}`;
                }
            }
        });
    } catch (error) {
        console.error('Failed to update proxy status:', error);
    }
}

/**
 * Escape HTML special characters
 */
function escapeHtml(text) {
    const div = document.createElement('div');
    div.textContent = text;
    return div.innerHTML;
}
