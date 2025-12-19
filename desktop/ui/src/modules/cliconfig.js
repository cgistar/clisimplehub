/**
 * CLI Config Editor module for Claude Code and Codex
 */
import { state } from './state.js';
import { t } from '../i18n/index.js';
import { showError, showSuccess } from './utils.js';

// Current editor state
let currentEditorType = null; // 'claude' or 'codex'
let editorFiles = {};

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
    
    const result = await window.go.main.App.GetClaudeConfig();
    if (!result.success) {
        throw new Error(result.message);
    }
    
    editorFiles = {};
    result.files.forEach(f => {
        editorFiles[f.name] = f.content;
    });
    
    renderCLIConfigEditor('claude', result.files);
}

/**
 * Load Codex config
 */
async function loadCodexConfig() {
    if (!window.go?.main?.App?.GetCodexConfig) {
        throw new Error('Backend not available');
    }
    
    const result = await window.go.main.App.GetCodexConfig();
    if (!result.success) {
        throw new Error(result.message);
    }
    
    editorFiles = {};
    result.files.forEach(f => {
        editorFiles[f.name] = f.content;
    });
    
    renderCLIConfigEditor('codex', result.files);
}

/**
 * Render CLI config editor content
 */
function renderCLIConfigEditor(type, files) {
    const modal = document.getElementById('cliConfigModal');
    const title = type === 'claude' ? 'Claude Code' : 'Codex';
    
    let editorsHtml = '';
    files.forEach((file, index) => {
        const isJson = file.name.endsWith('.json');
        const isToml = file.name.endsWith('.toml');
        const lang = isJson ? 'json' : (isToml ? 'toml' : 'text');
        
        editorsHtml += `
            <div class="cli-config-file">
                <div class="cli-config-file-header">
                    <span class="cli-config-file-name">${file.name}</span>
                    <span class="cli-config-file-status ${file.exists ? 'exists' : 'new'}">${file.exists ? t('cliConfig.fileExists') : t('cliConfig.fileNew')}</span>
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
                ${editorsHtml}
            </div>
            <div class="modal-footer">
                <div class="cli-footer-actions">
                    <button class="btn btn-secondary" onclick="processCLIConfig()" title="${t('cliConfig.processHelp')}">🔄 ${t('cliConfig.process')}</button>
                    <button class="btn btn-primary" onclick="saveCLIConfig()">💾 ${t('cliConfig.save')}</button>
                </div>
            </div>
        </div>
    `;
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
        const textareas = document.querySelectorAll('.cli-config-textarea');
        const files = {};
        
        textareas.forEach(ta => {
            files[ta.dataset.filename] = ta.value;
        });
        
        if (currentEditorType === 'claude') {
            const processed = await window.go.main.App.ProcessClaudeConfig(files['settings.json']);
            
            // Update textarea
            const ta = document.querySelector('[data-filename="settings.json"]');
            if (ta) {
                ta.value = processed;
            }
        } else if (currentEditorType === 'codex') {
            const result = await window.go.main.App.ProcessCodexConfig(files['config.toml'], files['auth.json']);
            
            // Update textareas
            const configTa = document.querySelector('[data-filename="config.toml"]');
            const authTa = document.querySelector('[data-filename="auth.json"]');
            
            if (configTa) configTa.value = result.configToml;
            if (authTa) authTa.value = result.authJson;
        }
        
        showSuccess(t('cliConfig.processSuccess'));
    } catch (error) {
        showError(t('cliConfig.processFailed') + ': ' + error.message);
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
