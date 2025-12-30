/**
 * WebDAV Sync Module
 * Handles configuration backup and restore via WebDAV
 * All WebDAV requests are proxied through Go backend to avoid CORS issues
 */

import { state } from './state.js';
import { showError, showSuccess } from './utils.js';
import { confirm, confirmWithOptions } from './confirm.js';

// WebDAV configuration state
export const webdavState = {
    serverUrl: '',
    username: '',
    password: '',
    backups: [], // List of available backups
    isTesting: false,
    isSyncing: false
};

// Remote backup directory
const BACKUP_DIR = '/clisimplehub';

/**
 * Get WebDAV config object for backend calls
 */
function getWebDAVConfig() {
    return {
        serverUrl: webdavState.serverUrl || document.getElementById('webdavServerUrl')?.value?.trim() || '',
        username: webdavState.username || document.getElementById('webdavUsername')?.value?.trim() || '',
        password: webdavState.password || document.getElementById('webdavPassword')?.value || ''
    };
}

/**
 * Show WebDAV sync modal
 */
export async function showWebDAVModal() {
    // Load existing WebDAV settings from state or localStorage
    const savedSettings = localStorage.getItem('webdavSettings');
    if (savedSettings) {
        try {
            const settings = JSON.parse(savedSettings);
            webdavState.serverUrl = settings.serverUrl || '';
            webdavState.username = settings.username || '';
            webdavState.password = settings.password || '';
        } catch (e) {
            console.error('Failed to parse saved WebDAV settings:', e);
        }
    }

    // Populate form fields
    const serverUrlEl = document.getElementById('webdavServerUrl');
    const usernameEl = document.getElementById('webdavUsername');
    const passwordEl = document.getElementById('webdavPassword');

    if (serverUrlEl) serverUrlEl.value = webdavState.serverUrl;
    if (usernameEl) usernameEl.value = webdavState.username;
    if (passwordEl) passwordEl.value = webdavState.password;

    // Load backups list
    await loadBackupsList();

    // Show modal
    document.getElementById('webdavModal').classList.add('active');
}

/**
 * Close WebDAV sync modal
 */
export function closeWebDAVModal() {
    document.getElementById('webdavModal').classList.remove('active');
}

/**
 * Test WebDAV connection via Go backend proxy
 */
export async function testWebDAVConnection() {
    const serverUrl = document.getElementById('webdavServerUrl').value.trim();
    const username = document.getElementById('webdavUsername').value.trim();
    const password = document.getElementById('webdavPassword').value;

    if (!serverUrl) {
        showError('请输入WebDAV服务器地址');
        return;
    }

    webdavState.isTesting = true;
    updateTestButtonState();

    try {
        // Test connection via Go backend proxy using PROPFIND
        const result = await window.go.main.App.WebDAVList({
            config: { serverUrl, username, password },
            path: '/',
            depth: '0'
        });

        if (result.error) {
            showError('连接失败: ' + result.error);
        } else if (result.statusCode === 207 || result.statusCode === 200) {
            showSuccess('WebDAV连接成功！');
            // Save settings
            saveWebDAVSettings(serverUrl, username, password);
        } else if (result.statusCode === 401) {
            showError('认证失败，请检查用户名和密码');
        } else {
            showError(`连接失败: ${result.statusCode}`);
        }
    } catch (error) {
        console.error('WebDAV test error:', error);
        showError('连接失败: ' + error.message);
    } finally {
        webdavState.isTesting = false;
        updateTestButtonState();
    }
}

/**
 * Ensure backup directory exists on WebDAV server
 */
async function ensureBackupDir(config) {
    // Try to create the backup directory (MKCOL), ignore if already exists
    const result = await window.go.main.App.WebDAVMkcol({
        config,
        path: BACKUP_DIR
    });
    // 201 = created, 405 = already exists, both are OK
    return result.statusCode === 201 || result.statusCode === 405 || result.statusCode === 200;
}

/**
 * Backup current configuration to WebDAV via Go backend proxy
 */
export async function backupToWebDAV() {
    const config = getWebDAVConfig();

    if (!config.serverUrl) {
        showError('请先配置WebDAV服务器地址');
        return;
    }

    webdavState.isSyncing = true;
    updateBackupButtonState();

    try {
        // Ensure backup directory exists
        await ensureBackupDir(config);

        // Get current configuration from backend
        let configData;
        if (window.go?.main?.App?.GetFullConfig) {
            configData = await window.go.main.App.GetFullConfig();
        } else {
            // Fallback: try to get from localStorage
            configData = {
                settings: state.settings,
                vendors: state.vendors,
                endpoints: Object.values(state.endpoints).flat()
            };
        }

        if (!configData) {
            throw new Error('无法获取当前配置');
        }

        // Generate backup filename with computer name and timestamp
        const computerName = await getComputerName();
        const timestamp = new Date().toISOString().replace(/[:.]/g, '-').slice(0, -1);
        const filename = `${computerName}-${timestamp}.json`;

        // Upload to WebDAV via Go backend proxy (save to /clisimplehub/)
        const result = await window.go.main.App.WebDAVPut({
            config,
            path: `${BACKUP_DIR}/${filename}`,
            body: JSON.stringify(configData, null, 2)
        });

        if (result.error) {
            throw new Error(result.error);
        } else if (result.statusCode >= 200 && result.statusCode < 300) {
            showSuccess(`配置已备份: ${filename}`);
            // Refresh backups list
            await loadBackupsList();
        } else {
            throw new Error(`上传失败: ${result.statusCode}`);
        }
    } catch (error) {
        console.error('Backup error:', error);
        showError('备份失败: ' + error.message);
    } finally {
        webdavState.isSyncing = false;
        updateBackupButtonState();
    }
}

/**
 * Load backups list from WebDAV via Go backend proxy
 */
export async function loadBackupsList() {
    const config = getWebDAVConfig();

    if (!config.serverUrl) {
        webdavState.backups = [];
        renderBackupsList();
        return;
    }

    try {
        // List files via Go backend proxy (from /clisimplehub/)
        const result = await window.go.main.App.WebDAVList({
            config,
            path: BACKUP_DIR,
            depth: '1'
        });

        if (result.error || (result.statusCode !== 207 && result.statusCode !== 200)) {
            webdavState.backups = [];
            renderBackupsList();
            return;
        }

        // Parse XML response
        const parser = new DOMParser();
        const xml = parser.parseFromString(result.body, 'text/xml');
        const responses = xml.querySelectorAll('response');

        const backups = [];
        responses.forEach((resp, index) => {
            if (index === 0) return; // Skip the directory itself

            const href = resp.querySelector('href')?.textContent;
            const displayName = resp.querySelector('displayname')?.textContent;
            const lastModified = resp.querySelector('getlastmodified')?.textContent;

            // Get filename from href or displayName
            let filename = '';
            if (href) {
                filename = decodeURIComponent(href.split('/').filter(Boolean).pop() || '');
            }
            if (!filename && displayName) {
                filename = displayName;
            }

            if (filename && filename.endsWith('.json')) {
                backups.push({
                    filename,
                    displayName: displayName || filename,
                    href,
                    lastModified,
                    name: filename.replace('.json', '')
                });
            }
        });

        webdavState.backups = backups.sort((a, b) =>
            new Date(b.lastModified) - new Date(a.lastModified)
        );
    } catch (error) {
        console.error('Failed to load backups:', error);
        webdavState.backups = [];
    }

    renderBackupsList();
}

/**
 * Load and restore configuration from WebDAV backup via Go backend proxy
 */
export async function loadConfigFromWebDAV(filename) {
    const config = getWebDAVConfig();

    if (!config.serverUrl || !filename) {
        showError('无效的备份文件');
        return;
    }

    // 让用户选择载入模式
    const mode = await confirmWithOptions(
        '请选择配置载入方式：',
        {
            title: '载入配置',
            buttons: [
                { value: 'cancel', text: '取消' },
                { value: 'merge', text: '合并配置', primary: true },
                { value: 'replace', text: '清空并替换', danger: true }
            ]
        }
    );

    if (!mode || mode === 'cancel') {
        return;
    }

    try {
        // Download file via Go backend proxy (from /clisimplehub/)
        const result = await window.go.main.App.WebDAVGet({
            config,
            path: `${BACKUP_DIR}/${filename}`
        });

        if (result.error) {
            throw new Error(result.error);
        }

        if (result.statusCode !== 200) {
            throw new Error(`下载失败: ${result.statusCode}`);
        }

        const remoteConfig = JSON.parse(result.body);

        // 根据模式处理配置
        let finalConfig;
        if (mode === 'replace') {
            // 清空并替换：直接使用远程配置，设置 replaceMode 标志
            finalConfig = { ...remoteConfig, replaceMode: true };
        } else {
            // 合并模式：将远程配置合并到本地
            finalConfig = await mergeConfigs(remoteConfig);
        }

        // Save to config.json via backend
        if (window.go?.main?.App?.SaveFullConfig) {
            // SaveFullConfig returns error (null on success)
            await window.go.main.App.SaveFullConfig(finalConfig);
            showSuccess(mode === 'replace' ? '配置已替换，正在刷新...' : '配置已合并，正在刷新...');
            // Reload configuration
            setTimeout(async () => {
                if (window.go?.main?.App?.ReloadConfig) {
                    await window.go.main.App.ReloadConfig();
                }
                // Refresh UI
                location.reload();
            }, 1000);
        } else {
            // Fallback: save to localStorage and reload
            if (finalConfig.settings) {
                localStorage.setItem('settings', JSON.stringify(finalConfig.settings));
            }
            if (finalConfig.vendors) {
                localStorage.setItem('vendors', JSON.stringify(finalConfig.vendors));
            }
            if (finalConfig.endpoints) {
                localStorage.setItem('endpoints', JSON.stringify(finalConfig.endpoints));
            }
            showSuccess(mode === 'replace' ? '配置已替换，正在刷新...' : '配置已合并，正在刷新...');
            setTimeout(() => location.reload(), 1000);
        }
    } catch (error) {
        console.error('Load config error:', error);
        showError('载入配置失败: ' + error.message);
    }
}

/**
 * 合并远程配置到本地配置
 * @param {Object} remoteConfig - 远程配置
 * @returns {Object} - 合并后的配置
 */
async function mergeConfigs(remoteConfig) {
    // 获取当前本地配置
    let localConfig;
    if (window.go?.main?.App?.GetFullConfig) {
        localConfig = await window.go.main.App.GetFullConfig();
    } else {
        localConfig = {
            settings: state.settings || {},
            vendors: state.vendors || [],
            endpoints: Object.values(state.endpoints || {}).flat()
        };
    }

    // 合并 settings：远程覆盖本地（保留本地独有字段）
    const mergedSettings = { ...localConfig.settings, ...remoteConfig.settings };

    // 合并 vendors：按 name 去重，远程优先
    const localVendors = localConfig.vendors || [];
    const remoteVendors = remoteConfig.vendors || [];
    const vendorMap = new Map();
    localVendors.forEach(v => vendorMap.set(v.name, v));
    remoteVendors.forEach(v => vendorMap.set(v.name, v)); // 远程覆盖同名
    const mergedVendors = Array.from(vendorMap.values());

    // 合并 endpoints：按 vendor+name 去重，远程优先
    const localEndpoints = localConfig.endpoints || [];
    const remoteEndpoints = remoteConfig.endpoints || [];
    const endpointMap = new Map();
    localEndpoints.forEach(e => endpointMap.set(`${e.vendor}:${e.name}`, e));
    remoteEndpoints.forEach(e => endpointMap.set(`${e.vendor}:${e.name}`, e)); // 远程覆盖同名
    const mergedEndpoints = Array.from(endpointMap.values());

    return {
        settings: mergedSettings,
        vendors: mergedVendors,
        endpoints: mergedEndpoints
    };
}

/**
 * Delete backup from WebDAV via Go backend proxy
 */
export async function deleteBackupFromWebDAV(filename) {
    const config = getWebDAVConfig();

    const confirmed = await confirm(`确定要删除备份文件 ${filename} 吗？`, {
        title: '删除备份',
        confirmText: '删除',
        cancelText: '取消',
        danger: true
    });
    if (!confirmed) {
        return;
    }

    try {
        // Delete file via Go backend proxy (from /clisimplehub/)
        const result = await window.go.main.App.WebDAVDelete({
            config,
            path: `${BACKUP_DIR}/${filename}`
        });

        if (result.error) {
            throw new Error(result.error);
        }

        if (result.statusCode >= 200 && result.statusCode < 300) {
            showSuccess('备份已删除');
            await loadBackupsList();
        } else {
            throw new Error(`删除失败: ${result.statusCode}`);
        }
    } catch (error) {
        console.error('Delete backup error:', error);
        showError('删除备份失败: ' + error.message);
    }
}

/**
 * Save WebDAV settings to localStorage
 */
function saveWebDAVSettings(serverUrl, username, password) {
    webdavState.serverUrl = serverUrl;
    webdavState.username = username;
    webdavState.password = password;

    localStorage.setItem('webdavSettings', JSON.stringify({
        serverUrl,
        username,
        password
    }));
}

/**
 * Get computer name
 */
async function getComputerName() {
    try {
        if (window.go?.main?.App?.GetComputerName) {
            return await window.go.main.App.GetComputerName();
        }
    } catch (e) {
        console.error('Failed to get computer name:', e);
    }

    // Fallback: use hostname or generic name
    return 'Computer-' + Math.random().toString(36).slice(2, 8);
}

/**
 * Update test button state
 */
function updateTestButtonState() {
    const btn = document.getElementById('webdavTestBtn');
    if (btn) {
        btn.disabled = webdavState.isTesting;
        btn.textContent = webdavState.isTesting ? '测试中...' : '测试连接';
    }
}

/**
 * Update backup button state
 */
function updateBackupButtonState() {
    const btn = document.getElementById('webdavBackupBtn');
    if (btn) {
        btn.disabled = webdavState.isSyncing;
        btn.textContent = webdavState.isSyncing ? '备份中...' : '备份配置';
    }
}

/**
 * Render backups list in the modal
 */
function renderBackupsList() {
    const container = document.getElementById('webdavBackupsList');
    if (!container) return;

    if (webdavState.backups.length === 0) {
        container.innerHTML = '<div class="empty-state">暂无备份记录</div>';
        return;
    }

    container.innerHTML = webdavState.backups.map(backup => `
        <div class="backup-item">
            <div class="backup-info">
                <div class="backup-name">${backup.name}</div>
                <div class="backup-time">${formatBackupTime(backup.lastModified)}</div>
            </div>
            <div class="backup-actions">
                <button class="btn btn-sm btn-primary" onclick="loadConfigFromWebDAV('${backup.filename}')" title="载入配置">
                    📥 载入
                </button>
                <button class="btn btn-sm btn-danger" onclick="deleteBackupFromWebDAV('${backup.filename}')" title="删除备份">
                    🗑️
                </button>
            </div>
        </div>
    `).join('');
}

/**
 * Format backup time for display
 */
function formatBackupTime(isoTime) {
    try {
        const date = new Date(isoTime);
        const now = new Date();
        const diff = now - date;

        // If today
        if (diff < 86400000 && date.getDate() === now.getDate()) {
            return '今天 ' + date.toLocaleTimeString('zh-CN', { hour: '2-digit', minute: '2-digit' });
        }

        // If yesterday
        const yesterday = new Date(now);
        yesterday.setDate(yesterday.getDate() - 1);
        if (date.getDate() === yesterday.getDate()) {
            return '昨天 ' + date.toLocaleTimeString('zh-CN', { hour: '2-digit', minute: '2-digit' });
        }

        // Otherwise show full date
        return date.toLocaleString('zh-CN', {
            month: 'short',
            day: 'numeric',
            hour: '2-digit',
            minute: '2-digit'
        });
    } catch (e) {
        return isoTime;
    }
}

// Export functions to window for onclick handlers
window.showWebDAVModal = showWebDAVModal;
window.closeWebDAVModal = closeWebDAVModal;
window.testWebDAVConnection = testWebDAVConnection;
window.backupToWebDAV = backupToWebDAV;
window.loadBackupsList = loadBackupsList;
window.loadConfigFromWebDAV = loadConfigFromWebDAV;
window.deleteBackupFromWebDAV = deleteBackupFromWebDAV;
