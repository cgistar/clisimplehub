/**
 * Settings management module
 */
import { state } from './state.js';
import { t, setLanguage } from '../i18n/index.js';
import { showError, showSuccess } from './utils.js';
import { initUI } from './ui.js';
import { loadEndpoints } from './endpoints.js';
import { loadRecentLogs } from './logs.js';
import { loadTokenStats } from './stats.js';

export async function loadLanguage() {
    try {
        if (window.go?.main?.App?.GetLanguage) {
            const lang = await window.go.main.App.GetLanguage();
            if (lang) {
                state.language = lang;
                setLanguage(lang);
            }
        }
    } catch (error) {
        console.error('Failed to load language:', error);
    }
}

export async function changeLanguage(lang) {
    try {
        if (window.go?.main?.App?.SetLanguage) {
            await window.go.main.App.SetLanguage(lang);
        }
        state.language = lang;
        setLanguage(lang);
        initUI();
        await loadEndpoints(state.currentTab);
        await loadRecentLogs();
        await loadTokenStats();
    } catch (error) {
        showError('Failed to change language: ' + error.message);
    }
}

export async function loadSettings() {
    try {
        if (window.go?.main?.App?.GetSettings) {
            const settings = await window.go.main.App.GetSettings();
            if (settings) {
                state.settings = settings;
                const portEl = document.getElementById('proxyPort');
                if (portEl) {
                    portEl.textContent = settings.port || 5600;
                }
            }
        }
    } catch (error) {
        console.error('Failed to load settings:', error);
    }
}

export async function showSettingsModal() {
    await loadSettings();
    document.getElementById('settingsPort').value = state.settings.port || 5600;
    document.getElementById('settingsApiKey').value = state.settings.apiKey || '';
    document.getElementById('settingsFallback').checked = state.settings.fallback || false;
    
    // Load CLI config directories
    try {
        if (window.go?.main?.App?.GetCLIConfigDirs) {
            const dirs = await window.go.main.App.GetCLIConfigDirs();
            document.getElementById('settingsClaudeConfigDir').value = dirs.claudeConfigDir || '';
            document.getElementById('settingsCodexConfigDir').value = dirs.codexConfigDir || '';
        }
    } catch (error) {
        console.error('Failed to load CLI config dirs:', error);
    }
    
    document.getElementById('settingsModal').classList.add('active');
}

export function closeSettingsModal() {
    document.getElementById('settingsModal').classList.remove('active');
}

export async function saveSettings() {
    const port = parseInt(document.getElementById('settingsPort').value, 10);
    const apiKey = document.getElementById('settingsApiKey').value;
    const fallback = document.getElementById('settingsFallback').checked;
    const claudeConfigDir = document.getElementById('settingsClaudeConfigDir').value;
    const codexConfigDir = document.getElementById('settingsCodexConfigDir').value;
    
    if (isNaN(port) || port < 1 || port > 65535) {
        showError(t('settings.portHelp'));
        return;
    }
    
    try {
        if (window.go?.main?.App?.SaveSettings) {
            console.log('Saving settings:', { port, apiKey: apiKey ? '***' : '', fallback });
            await window.go.main.App.SaveSettings({ port, apiKey, fallback });
        }
        
        // Save CLI config directories
        if (window.go?.main?.App?.SaveCLIConfigDirs) {
            await window.go.main.App.SaveCLIConfigDirs({
                claudeConfigDir: claudeConfigDir,
                codexConfigDir: codexConfigDir
            });
        }
        
        await loadSettings();
        closeSettingsModal();
        showSuccess(t('settings.saveSuccess'));
    } catch (error) {
        console.error('SaveSettings error:', error);
        showError(t('settings.saveFailed') + ': ' + (error?.message || error || 'Unknown error'));
    }
}

export async function refreshConfig() {
    try {
        if (window.go?.main?.App?.ReloadConfig) {
            await window.go.main.App.ReloadConfig();
        }
        await loadSettings();
        await loadEndpoints(state.currentTab);
        showSuccess(t('endpoints.refreshSuccess'));
    } catch (error) {
        showError(t('endpoints.refreshFailed') + ': ' + (error?.message || error || 'Unknown error'));
    }
}
