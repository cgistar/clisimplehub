/**
 * Vendor and endpoint management modal module
 */
import { state } from './state.js';
import { t } from '../i18n/index.js';
import { showError, showSuccess } from './utils.js';
import { loadEndpoints } from './endpoints.js';
import { renderLogs } from './logs.js';
import { logInfo, logError } from './console.js';
import { confirm as confirmDialog } from './confirm.js';

// =============================================================================
// Manage Modal
// =============================================================================

export function showManageModal() {
    document.getElementById('manageModal').classList.add('active');
    loadVendors();
}

export function closeManageModal() {
    document.getElementById('manageModal').classList.remove('active');
    state.selectedVendor = null;
    state.vendorEndpoints = [];
    loadEndpoints(state.currentTab);
}

export async function loadVendors() {
    try {
        if (!window.go?.main?.App?.GetVendors) return;
        
        const vendors = await window.go.main.App.GetVendors();
        state.vendors = vendors || [];
        renderVendorList(vendors || []);
    } catch (error) {
        console.error('Failed to load vendors:', error);
    }
}

export function renderVendorList(vendors) {
    const container = document.getElementById('vendorList');
    if (!container) return;
    
    if (!vendors || vendors.length === 0) {
        container.innerHTML = `<div class="empty-state">${t('manage.noVendors')}</div>`;
        return;
    }
    
    container.innerHTML = vendors.map(v => `
        <div class="vendor-item ${state.selectedVendor?.id === v.id ? 'selected' : ''}" onclick="selectVendor(${v.id})">
            <div class="vendor-info">
                <div class="vendor-name">${v.name}</div>
                <div class="vendor-url">${v.apiUrl}</div>
            </div>
            <div class="vendor-actions">
                <button class="btn btn-sm btn-icon" onclick="event.stopPropagation(); editVendor(${v.id})" title="Edit">✏️</button>
                <button class="btn btn-sm btn-icon" onclick="event.stopPropagation(); deleteVendorById(${v.id})" title="${t('manage.delete') || 'Delete'}">🗑️</button>
            </div>
        </div>
    `).join('');
}

export async function selectVendor(vendorId) {
    const vendor = state.vendors.find(v => v.id === vendorId);
    if (!vendor) return;
    
    state.selectedVendor = vendor;
    renderVendorList(state.vendors);
    const addBtn = document.getElementById('addEndpointBtn');
    if (addBtn) addBtn.disabled = false;
    
    await loadVendorEndpoints(vendorId);
}

export async function loadVendorEndpoints(vendorId) {
    try {
        if (!window.go?.main?.App?.GetEndpointsByVendorID) return;
        
        const endpoints = await window.go.main.App.GetEndpointsByVendorID(vendorId);
        state.vendorEndpoints = endpoints || [];
        renderVendorEndpoints(endpoints || []);
    } catch (error) {
        console.error('Failed to load vendor endpoints:', error);
    }
}

export function renderVendorEndpoints(endpoints) {
    const container = document.getElementById('endpointManageList');
    if (!container) return;
    
    if (!state.selectedVendor) {
        container.innerHTML = `<div class="empty-state">${t('manage.selectVendorFirst')}</div>`;
        return;
    }
    
    if (!endpoints || endpoints.length === 0) {
        container.innerHTML = `<div class="empty-state">${t('manage.noEndpointsForVendor')}</div>`;
        return;
    }
    
    container.innerHTML = endpoints.map(ep => `
        <div class="endpoint-manage-item">
            <div class="endpoint-actions-top">
                <button class="btn btn-sm btn-icon" onclick="editEndpoint(${ep.id})" title="Edit">✏️</button>
                <button class="btn btn-sm btn-icon" onclick="deleteEndpointById(${ep.id}, ${state.selectedVendor.id})" title="${t('manage.delete') || 'Delete'}">🗑️</button>
            </div>
            <div class="endpoint-info">
                <div class="endpoint-name">
                    ${ep.name}
                    ${!ep.enabled ? `<span class="badge disabled-badge">${t('common.disabled')}</span>` : ''}
                </div>
                <div class="endpoint-url">${ep.apiUrl}</div>
                <div class="endpoint-meta">
                    <span class="badge">${ep.interfaceType}</span>
                    ${ep.model ? `<span class="model-tag">${ep.model}</span>` : ''}
                </div>
            </div>
        </div>
    `).join('');
}

// =============================================================================
// Vendor Form
// =============================================================================

export function showVendorForm(vendor = null) {
    document.getElementById('vendorFormTitle').textContent = vendor ? t('manage.editVendor') : t('manage.addVendor');
    document.getElementById('vendorId').value = vendor?.id || '';
    document.getElementById('vendorName').value = vendor?.name || '';
    document.getElementById('vendorHomeUrl').value = vendor?.homeUrl || '';
    document.getElementById('vendorApiUrl').value = vendor?.apiUrl || '';
    document.getElementById('vendorRemark').value = vendor?.remark || '';
    document.getElementById('deleteVendorBtn').style.display = vendor ? 'block' : 'none';
    document.getElementById('vendorFormModal').classList.add('active');
}

export function closeVendorForm() {
    document.getElementById('vendorFormModal').classList.remove('active');
}

export function editVendor(vendorId) {
    const vendor = state.vendors.find(v => v.id === vendorId);
    if (vendor) {
        showVendorForm(vendor);
    }
}

export async function saveVendor() {
    const vendor = {
        id: parseInt(document.getElementById('vendorId').value) || 0,
        name: document.getElementById('vendorName').value.trim(),
        homeUrl: document.getElementById('vendorHomeUrl').value.trim(),
        apiUrl: document.getElementById('vendorApiUrl').value.trim(),
        remark: document.getElementById('vendorRemark').value.trim()
    };
    
    if (!vendor.name || !vendor.homeUrl || !vendor.apiUrl) {
        showError('Please fill in all required fields');
        return;
    }
    
    try {
        if (window.go?.main?.App?.SaveVendor) {
            await window.go.main.App.SaveVendor(vendor);
            closeVendorForm();
            await loadVendors();
            showSuccess('Vendor saved successfully');
        }
    } catch (error) {
        showError(t('manage.saveFailed') + ': ' + error.message);
    }
}

export async function deleteVendor() {
    const vendorId = parseInt(document.getElementById('vendorId').value);
    if (!vendorId) return;

    await runVendorDeletion(vendorId, 'form');
}

export async function deleteVendorById(vendorId) {
    await runVendorDeletion(vendorId, 'list');
}

async function runVendorDeletion(vendorId, source) {
    const parsedVendorId = parseInt(vendorId, 10);
    logInfo(`[Vendor] delete requested (source=${source}, vendorId=${vendorId}, parsedId=${parsedVendorId})`);
    if (!parsedVendorId) return;

    const confirmMessage = t('manage.confirmDeleteVendor') || 'Confirm delete vendor?';
    const confirmed = await confirmDialog(confirmMessage, { danger: true });
    if (!confirmed) return;

    try {
        if (!window.go?.main?.App?.DeleteVendor) {
            logError('[Vendor] delete aborted: window.go.main.App.DeleteVendor not available');
            showError(t('manage.deleteFailed') + ': backend not available');
            return;
        }

        await window.go.main.App.DeleteVendor(parsedVendorId);
        if (source === 'form') {
            closeVendorForm();
        }

        if (state.selectedVendor?.id === parsedVendorId) {
            state.selectedVendor = null;
            state.vendorEndpoints = [];
            const addBtn = document.getElementById('addEndpointBtn');
            if (addBtn) addBtn.disabled = true;
            const epList = document.getElementById('endpointManageList');
            if (epList) epList.innerHTML = `<div class="empty-state">${t('manage.selectVendorFirst')}</div>`;
        }

        await loadVendors();
        await loadEndpoints(state.currentTab);
        showSuccess('Vendor deleted successfully');
    } catch (error) {
        logError(`[Vendor] delete failed: ${error?.message || error}`);
        showError(t('manage.deleteFailed') + ': ' + error.message);
    }
}

// =============================================================================
// Endpoint Form
// =============================================================================

export function showEndpointForm(endpoint = null) {
    if (!state.selectedVendor && !endpoint) {
        showError('Please select a vendor first');
        return;
    }

    document.getElementById('endpointFormTitle').textContent = endpoint ? t('manage.editEndpoint') : t('manage.addEndpoint');
    document.getElementById('endpointId').value = endpoint?.id || '';
    document.getElementById('endpointName').value = endpoint?.name || '';
    document.getElementById('endpointApiUrl').value = endpoint?.apiUrl || state.selectedVendor?.apiUrl || '';
    document.getElementById('endpointApiKey').value = endpoint?.apiKey || '';
    document.getElementById('endpointInterfaceType').value = endpoint?.interfaceType || 'claude';
    document.getElementById('endpointVendorId').value = endpoint?.vendorId || state.selectedVendor?.id || '';
    document.getElementById('endpointModel').value = endpoint?.model || '';
    document.getElementById('endpointRemark').value = endpoint?.remark || '';
    document.getElementById('endpointPriority').value = endpoint?.priority || 5;
    document.getElementById('endpointEnabled').checked = endpoint?.enabled !== false;

    // 初始化 proxyUrl
    document.getElementById('endpointProxyUrl').value = endpoint?.proxyUrl || '';

    // 初始化 transformer
    document.getElementById('endpointTransformer').value = endpoint?.transformer || '';
    syncTransformerDisplay();
    loadTransformersForInterfaceType();
    closeTransformerDropdown();

    // 初始化 models 映射
    renderModelMappings(endpoint?.models || []);

    const deleteBtn = document.getElementById('deleteEndpointBtn');
    const parsedEndpointId = parseInt(endpoint?.id, 10);
    const canDelete = Number.isFinite(parsedEndpointId) && parsedEndpointId > 0;
    if (deleteBtn) {
        deleteBtn.style.display = canDelete ? 'block' : 'none';
    }

    syncEndpointInterfaceTypeDisplay();
    closeInterfaceTypeDropdown();

    // Reset password visibility
    const apiKeyInput = document.getElementById('endpointApiKey');
    apiKeyInput.type = 'password';
    const toggleBtn = document.getElementById('toggleApiKeyVisibility');
    if (toggleBtn) {
        toggleBtn.textContent = '👁️';
    }

    // Update test button visibility based on interface type
    updateTestButtonVisibility();
    updateQuickMappingVisibility();

    document.getElementById('endpointFormModal').classList.add('active');
}

function syncEndpointInterfaceTypeDisplay() {
    const select = document.getElementById('endpointInterfaceType');
    const display = document.getElementById('endpointInterfaceTypeDisplay');
    if (!select || !display) return;

    const selectedOption = select.options[select.selectedIndex];
    display.value = selectedOption?.textContent || select.value || '';
}

function closeInterfaceTypeDropdown() {
    const dropdown = document.getElementById('interfaceTypeDropdown');
    if (!dropdown) return;
    dropdown.classList.remove('show');
}

function renderInterfaceTypeDropdown() {
    const select = document.getElementById('endpointInterfaceType');
    const dropdown = document.getElementById('interfaceTypeDropdown');
    if (!select || !dropdown) return;

    dropdown.innerHTML = '';
    Array.from(select.options).forEach(option => {
        const item = document.createElement('div');
        item.className = 'model-dropdown-item';
        item.textContent = option.textContent;
        item.onclick = () => {
            select.value = option.value;
            onEndpointInterfaceTypeChange();
        };
        dropdown.appendChild(item);
    });
}

export function toggleInterfaceTypeDropdown() {
    const dropdown = document.getElementById('interfaceTypeDropdown');
    const select = document.getElementById('endpointInterfaceType');
    if (!dropdown || !select) return;

    if (dropdown.classList.contains('show')) {
        dropdown.classList.remove('show');
        return;
    }

    renderInterfaceTypeDropdown();
    dropdown.classList.add('show');
}

export function onEndpointInterfaceTypeChange() {
    syncEndpointInterfaceTypeDisplay();
    closeInterfaceTypeDropdown();
    clearFetchedModels();
    updateTestButtonVisibility();
    updateQuickMappingVisibility();
    // interfaceType 变化时重置 transformer
    document.getElementById('endpointTransformer').value = '';
    syncTransformerDisplay();
    loadTransformersForInterfaceType();
}

// 控制快捷映射按钮的显示/隐藏
function updateQuickMappingVisibility() {
    const interfaceType = document.getElementById('endpointInterfaceType')?.value || '';
    const quickMappingBtn = document.getElementById('quickMappingBtn');
    if (quickMappingBtn) {
        quickMappingBtn.style.display = interfaceType === 'claude' ? 'inline-block' : 'none';
    }
}

// Claude 快捷模型映射预设
const CLAUDE_QUICK_MAPPINGS = [
    { alias: 'claude-haiku-4-5-20251001', name: 'claude-4.5-haiku' },
    { alias: 'claude-opus-4-5-20251101', name: 'claude-4.5-opus' },
    { alias: 'claude-sonnet-4-5-20250929', name: 'claude-4.5-sonnet' }
];

// 应用快捷模型映射
export function applyQuickModelMappings() {
    const container = document.getElementById('modelMappingsList');
    if (!container) return;

    // 获取现有映射
    const existingAliases = new Set();
    container.querySelectorAll('.model-mapping-row .mapping-alias').forEach(input => {
        const alias = input.value?.trim();
        if (alias) existingAliases.add(alias);
    });

    // 添加不存在的映射
    let addedCount = 0;
    CLAUDE_QUICK_MAPPINGS.forEach(mapping => {
        if (!existingAliases.has(mapping.alias)) {
            const index = container.children.length;
            const row = createModelMappingRow(mapping.alias, mapping.name, index);
            container.appendChild(row);
            addedCount++;
        }
    });

    if (addedCount > 0) {
        showSuccess(`已添加 ${addedCount} 个映射`);
    } else {
        showSuccess('映射已存在，无需添加');
    }
}

// Update test button visibility based on interface type and model
export function updateTestButtonVisibility() {
    const interfaceType = document.getElementById('endpointInterfaceType').value;
    const model = document.getElementById('endpointModel').value.trim();
    const testBtn = document.getElementById('testEndpointBtn');
    
    if (testBtn) {
        // Claude: require model; Codex: show test dialog to select model/reasoning.
        const showTest = (interfaceType === 'claude' && model !== '') || interfaceType === 'codex';
        testBtn.style.display = showTest ? 'inline-block' : 'none';
    }
}

// Test endpoint connection using current form values
export async function testEndpoint() {
    const apiUrl = document.getElementById('endpointApiUrl').value.trim();
    const apiKey = document.getElementById('endpointApiKey').value.trim();
    const interfaceType = document.getElementById('endpointInterfaceType').value;
    const model = document.getElementById('endpointModel').value.trim();
    const endpointName = document.getElementById('endpointName').value.trim() || 'Unnamed';
    const testBtn = document.getElementById('testEndpointBtn');
    const originalText = testBtn.textContent;

    if (!apiUrl || !apiKey) {
        showError('Please fill in API URL and API Key first');
        return;
    }

    if (interfaceType === 'codex') {
        openCodexTestDialog({ apiUrl, apiKey, model, endpointName });
        return;
    }

    const startedAt = Date.now();
    logInfo(`[Test] Starting test for endpoint "${endpointName}" (${interfaceType}, model: ${model || 'default'})`);

    try {
        testBtn.textContent = t('manage.testing');
        testBtn.disabled = true;

        if (window.go?.main?.App?.TestEndpointWithParams) {
            const params = { apiUrl, apiKey, interfaceType, model };
            const resultStr = await window.go.main.App.TestEndpointWithParams(params);
            const result = JSON.parse(resultStr);
            logEndpointTestResult({ endpointName, interfaceType, model }, result);
            appendEndpointTestToMainLogs({ endpointName, interfaceType, model }, result, Date.now() - startedAt);

            if (result.success) {
                logInfo(`[Test] ✅ Success for "${endpointName}": ${result.message}`);
                showSuccess(t('manage.testSuccess') + ': ' + result.message);
            } else {
                logError(`[Test] ❌ Failed for "${endpointName}": ${result.message}`);
                showError(t('manage.testFailed') + ': ' + result.message);
            }
        }
    } catch (error) {
        logError(`[Test] ❌ Error for "${endpointName}": ${error.message}`);
        showError(t('manage.testFailed') + ': ' + error.message);
    } finally {
        testBtn.textContent = originalText;
        testBtn.disabled = false;
    }
}

function logEndpointTestResult(context, result) {
    const endpointName = context?.endpointName || 'Unnamed';
    const interfaceType = context?.interfaceType || 'unknown';
    const model = context?.model || '';
    const reasoning = context?.reasoning || '';

    const statusCode = result?.statusCode ?? '-';
    const targetUrl = result?.targetUrl || result?.target_url || '-';
    const success = !!result?.success;
    const errorMessage = result?.errorMessage || result?.error_message || '';
    const responseText = result?.responseText || result?.response_text || result?.message || '';
    const requestHeaders = result?.requestHeaders || result?.request_headers || null;

    const metaParts = [
        `endpoint="${endpointName}"`,
        `type=${interfaceType}`,
        model ? `model=${model}` : null,
        reasoning ? `reasoning=${reasoning}` : null,
        `status=${success ? 'ok' : 'fail'}`,
        `code=${statusCode}`,
        `target=${targetUrl}`
    ].filter(Boolean);

    logInfo(`[Test] ${metaParts.join(' ')}`);
    if (requestHeaders) {
        let headersText = '';
        try {
            headersText = JSON.stringify(requestHeaders);
        } catch (e) {
            headersText = String(requestHeaders);
        }
        if (headersText) {
            logInfo(`[Test] headers=${toSingleLine(headersText, 800)}`);
        }
    }
    if (errorMessage) {
        logInfo(`[Test] error=${toSingleLine(errorMessage, 300)}`);
    }
    if (responseText) {
        logInfo(`[Test] response=${toSingleLine(responseText, 800)}`);
    }
}

function appendEndpointTestToMainLogs(context, result, durationMs) {
    const endpointName = context?.endpointName || 'Unnamed';
    const interfaceType = context?.interfaceType || 'unknown';
    const targetUrl = result?.targetUrl || result?.target_url || '';
    const statusCode = Number(result?.statusCode || 0) || 0;

    let path = '/';
    if (targetUrl) {
        try {
            const u = new URL(targetUrl);
            path = u.pathname + (u.search || '');
        } catch {
            path = targetUrl;
        }
    }

    const success = !!result?.success;
    const status = success ? 'success' : (statusCode ? `error_${statusCode}` : 'error');

    const responseText = result?.responseText || result?.response_text || result?.message || '';
    const requestHeaders = result?.requestHeaders || result?.request_headers || {};

    const log = {
        id: `test_${Date.now()}_${Math.random().toString(16).slice(2)}`,
        interfaceType,
        vendorName: endpointName,
        endpointName,
        path,
        runTime: typeof durationMs === 'number' ? durationMs : 0,
        status,
        timestamp: new Date().toISOString(),
        method: 'POST',
        statusCode,
        targetUrl,
        requestHeaders,
        responseStream: toSingleLine(responseText, 5000)
    };

    state.recentLogs = [log, ...(state.recentLogs || [])].slice(0, 5);
    renderLogs(state.recentLogs);
}

function toSingleLine(text, maxLen) {
    const raw = (text ?? '').toString();
    if (!raw) return '';
    let s = raw.replace(/\s+/g, ' ').trim();
    if (maxLen && s.length > maxLen) {
        s = s.slice(0, maxLen) + '...(truncated)';
    }
    return s;
}

let codexTestModal = null;
let codexTestContext = null;

function syncCodexTestReasoningDisplay(modal) {
    const targetModal = modal || codexTestModal;
    if (!targetModal) return;

    const select = targetModal.querySelector('#codexTestReasoning');
    const display = targetModal.querySelector('#codexTestReasoningDisplay');
    if (!select || !display) return;

    const selectedOption = select.options[select.selectedIndex];
    display.value = selectedOption?.textContent || select.value || '';
}

function closeCodexTestReasoningDropdown(modal) {
    const targetModal = modal || codexTestModal;
    if (!targetModal) return;

    const dropdown = targetModal.querySelector('#codexReasoningDropdown');
    dropdown?.classList.remove('show');
}

function renderCodexTestReasoningDropdown(modal) {
    const targetModal = modal || codexTestModal;
    if (!targetModal) return;

    const select = targetModal.querySelector('#codexTestReasoning');
    const dropdown = targetModal.querySelector('#codexReasoningDropdown');
    if (!select || !dropdown) return;

    dropdown.innerHTML = '';
    Array.from(select.options).forEach(option => {
        const item = document.createElement('div');
        item.className = 'model-dropdown-item';
        item.textContent = option.textContent;
        item.onclick = () => {
            select.value = option.value;
            syncCodexTestReasoningDisplay(targetModal);
            closeCodexTestReasoningDropdown(targetModal);
        };
        dropdown.appendChild(item);
    });
}

function toggleCodexTestReasoningDropdown(modal) {
    const targetModal = modal || codexTestModal;
    if (!targetModal) return;

    const dropdown = targetModal.querySelector('#codexReasoningDropdown');
    if (!dropdown) return;

    if (dropdown.classList.contains('show')) {
        dropdown.classList.remove('show');
        return;
    }

    renderCodexTestReasoningDropdown(targetModal);
    dropdown.classList.add('show');
}

function openCodexTestDialog({ apiUrl, apiKey, model, endpointName }) {
    codexTestContext = { apiUrl, apiKey, endpointName };
    if (!codexTestModal) {
        codexTestModal = createCodexTestModal();
        document.body.appendChild(codexTestModal);
    }

    const titleEl = codexTestModal.querySelector('[data-role="title"]');
    if (titleEl) titleEl.textContent = t('manage.testDialogTitle');

    const endpointEl = codexTestModal.querySelector('[data-role="endpointName"]');
    if (endpointEl) endpointEl.textContent = endpointName || 'Codex';

    const modelInput = codexTestModal.querySelector('#codexTestModel');
    if (modelInput) modelInput.value = model || 'gpt-5-codex';

    const reasoningSelect = codexTestModal.querySelector('#codexTestReasoning');
    if (reasoningSelect) {
        if (!reasoningSelect.value) reasoningSelect.value = 'high';
        syncCodexTestReasoningDisplay(codexTestModal);
        closeCodexTestReasoningDropdown(codexTestModal);
    }

    const modelsDatalist = codexTestModal.querySelector('#codexModelOptions');
    if (modelsDatalist) {
        const uniqueModels = Array.from(new Set([model, 'gpt-5-codex', 'codex-mini-latest', ...(fetchedModels || [])].filter(Boolean)));
        modelsDatalist.innerHTML = uniqueModels.map(m => `<option value="${m}"></option>`).join('');
    }

    resetCodexTestResult();
    codexTestModal.classList.add('active');
}

function closeCodexTestDialog() {
    if (!codexTestModal) return;
    codexTestModal.classList.remove('active');
    closeCodexTestReasoningDropdown(codexTestModal);
}

function resetCodexTestResult() {
    if (!codexTestModal) return;
    const statusEl = codexTestModal.querySelector('[data-role="status"]');
    const statusCodeEl = codexTestModal.querySelector('[data-role="statusCode"]');
    const targetUrlEl = codexTestModal.querySelector('[data-role="targetUrl"]');
    const errorEl = codexTestModal.querySelector('[data-role="errorMessage"]');
    const respEl = codexTestModal.querySelector('[data-role="responseText"]');
    if (statusEl) statusEl.textContent = '-';
    if (statusCodeEl) statusCodeEl.textContent = '-';
    if (targetUrlEl) targetUrlEl.textContent = '-';
    if (errorEl) errorEl.textContent = '-';
    if (respEl) respEl.textContent = '';
}

async function runCodexEndpointTest() {
    if (!codexTestModal || !codexTestContext) return;

    const testBtn = codexTestModal.querySelector('[data-role="run"]');
    const closeBtn = codexTestModal.querySelector('[data-role="close"]');
    const copyBtn = codexTestModal.querySelector('[data-role="copy"]');

    const model = (codexTestModal.querySelector('#codexTestModel')?.value || '').trim();
    const reasoning = (codexTestModal.querySelector('#codexTestReasoning')?.value || '').trim();

    if (!model) {
        showError('Please select a model first');
        return;
    }

    const startedAt = Date.now();
    try {
        if (testBtn) {
            testBtn.disabled = true;
            testBtn.textContent = t('manage.testing');
        }
        if (closeBtn) closeBtn.disabled = true;
        if (copyBtn) copyBtn.disabled = true;

        const params = {
            apiUrl: codexTestContext.apiUrl,
            apiKey: codexTestContext.apiKey,
            interfaceType: 'codex',
            model,
            reasoning
        };

        const resultStr = await window.go.main.App.TestEndpointWithParams(params);
        const result = JSON.parse(resultStr);
        logEndpointTestResult(
            {
                endpointName: codexTestContext.endpointName || 'Codex',
                interfaceType: 'codex',
                model,
                reasoning
            },
            result
        );
        appendEndpointTestToMainLogs(
            {
                endpointName: codexTestContext.endpointName || 'Codex',
                interfaceType: 'codex',
                model,
                reasoning
            },
            result,
            Date.now() - startedAt
        );

        const statusEl = codexTestModal.querySelector('[data-role="status"]');
        const statusCodeEl = codexTestModal.querySelector('[data-role="statusCode"]');
        const targetUrlEl = codexTestModal.querySelector('[data-role="targetUrl"]');
        const errorEl = codexTestModal.querySelector('[data-role="errorMessage"]');
        const respEl = codexTestModal.querySelector('[data-role="responseText"]');

        if (statusEl) statusEl.textContent = result.success ? t('manage.testStatusSuccess') : t('manage.testStatusFailed');
        if (statusCodeEl) statusCodeEl.textContent = result.statusCode || '-';
        if (targetUrlEl) targetUrlEl.textContent = result.targetUrl || '-';
        if (errorEl) errorEl.textContent = result.errorMessage || (result.success ? '-' : (result.message || '-'));
        if (respEl) respEl.textContent = result.responseText || result.message || '';

        if (copyBtn) {
            copyBtn.disabled = !(respEl && respEl.textContent);
            copyBtn.dataset.payload = respEl ? respEl.textContent : '';
        }
    } catch (error) {
        showError(t('manage.testFailed') + ': ' + error.message);
    } finally {
        if (testBtn) {
            testBtn.disabled = false;
            testBtn.textContent = t('manage.retest');
        }
        if (closeBtn) closeBtn.disabled = false;
        if (copyBtn) copyBtn.disabled = false;
    }
}

async function copyCodexTestResponse() {
    if (!codexTestModal) return;
    const copyBtn = codexTestModal.querySelector('[data-role="copy"]');
    const payload = copyBtn?.dataset?.payload || '';
    if (!payload) return;
    try {
        await navigator.clipboard.writeText(payload);
        showSuccess(t('manage.copied'));
    } catch (e) {
        showError('Failed to copy: ' + (e?.message || e));
    }
}

function createCodexTestModal() {
    const modal = document.createElement('div');
    modal.id = 'codexTestModal';
    modal.className = 'modal';
    modal.innerHTML = `
        <div class="modal-content">
            <div class="modal-header">
                <h2 data-role="title">${t('manage.testDialogTitle')}</h2>
                <button class="modal-close" type="button" aria-label="close">&times;</button>
            </div>
            <div class="modal-body">
                <div class="detail-section">
                    <table class="log-detail-table">
                        <tr>
                            <td class="label">${t('manage.endpointName')}</td>
                            <td class="value" data-role="endpointName"></td>
                        </tr>
                    </table>
                </div>
                <div class="form-row">
                    <div class="form-group" style="flex: 1;">
                        <label>${t('manage.model')}</label>
                        <input id="codexTestModel" list="codexModelOptions" placeholder="${t('manage.modelPlaceholder')}" autocomplete="off">
                        <datalist id="codexModelOptions"></datalist>
                    </div>
                    <div class="form-group" style="flex: 1;">
                        <label>${t('manage.reasoning')}</label>
                        <div class="model-select-container" data-role="codexReasoningContainer">
                            <input type="text" id="codexTestReasoningDisplay" readonly>
                            <button type="button" class="model-dropdown-toggle" data-role="toggleCodexReasoning">
                                <svg width="12" height="12" viewBox="0 0 12 12" fill="currentColor">
                                    <path d="M2 4L6 8L10 4" stroke="currentColor" stroke-width="2" fill="none"/>
                                </svg>
                            </button>
                            <div class="model-dropdown" id="codexReasoningDropdown"></div>
                        </div>
                        <select id="codexTestReasoning" style="display:none;">
                            <option value="low">low</option>
                            <option value="medium">medium</option>
                            <option value="high" selected>high</option>
                        </select>
                    </div>
                </div>
                <div class="detail-section">
                    <h3 style="margin: 8px 0;">${t('manage.testResult')}</h3>
                    <table class="log-detail-table">
                        <tr>
                            <td class="label">${t('manage.testStatus')}</td>
                            <td class="value" data-role="status">-</td>
                        </tr>
                        <tr>
                            <td class="label">${t('logs.statusCode')}</td>
                            <td class="value" data-role="statusCode">-</td>
                        </tr>
                        <tr>
                            <td class="label">${t('logs.targetUrl')}</td>
                            <td class="value url-value" data-role="targetUrl">-</td>
                        </tr>
                        <tr>
                            <td class="label">${t('manage.errorMessage')}</td>
                            <td class="value" data-role="errorMessage">-</td>
                        </tr>
                    </table>
                    <div style="margin-top: 8px;">
                        <div style="font-size: 12px; margin-bottom: 6px;">${t('manage.responseData')}</div>
                        <pre data-role="responseText" style="max-height: 240px; overflow:auto; background:#0b1220; color:#d7e0ff; padding:10px; border-radius:8px; white-space: pre-wrap;"></pre>
                    </div>
                </div>
            </div>
            <div class="modal-footer">
                <button class="btn btn-secondary" type="button" data-role="copy" disabled>${t('manage.copyResponse')}</button>
                <button class="btn btn-secondary" type="button" data-role="close">${t('common.close')}</button>
                <button class="btn btn-primary" type="button" data-role="run">${t('manage.test')}</button>
            </div>
        </div>
    `;

    modal.querySelector('.modal-close')?.addEventListener('click', closeCodexTestDialog);
    modal.querySelector('[data-role="close"]')?.addEventListener('click', closeCodexTestDialog);
    modal.querySelector('[data-role="run"]')?.addEventListener('click', runCodexEndpointTest);
    modal.querySelector('[data-role="copy"]')?.addEventListener('click', copyCodexTestResponse);

    modal.querySelector('#codexTestReasoningDisplay')?.addEventListener('click', () => toggleCodexTestReasoningDropdown(modal));
    modal.querySelector('[data-role="toggleCodexReasoning"]')?.addEventListener('click', () => toggleCodexTestReasoningDropdown(modal));
    modal.addEventListener('click', (e) => {
        const container = modal.querySelector('[data-role="codexReasoningContainer"]');
        if (!container) return;
        if (!container.contains(e.target)) closeCodexTestReasoningDropdown(modal);
    });

    return modal;
}

// Store fetched models
let fetchedModels = [];

// Fetch models from API
export async function fetchModels() {
    const apiUrl = document.getElementById('endpointApiUrl').value.trim();
    const apiKey = document.getElementById('endpointApiKey').value.trim();
    const interfaceType = document.getElementById('endpointInterfaceType').value;
    const fetchBtn = document.getElementById('fetchModelsBtn');
    const fetchIcon = document.getElementById('fetchModelsIcon');
    const modelInput = document.getElementById('endpointModel');
    const dropdown = document.getElementById('modelDropdown');

    if (!apiUrl) {
        showError(t('manage.fetchModelsNoUrl'));
        return;
    }
    if (!apiKey) {
        showError(t('manage.fetchModelsNoKey'));
        return;
    }

    fetchBtn.disabled = true;
    fetchIcon.textContent = '...';

    try {
        if (window.go?.main?.App?.FetchModels) {
            const resultStr = await window.go.main.App.FetchModels(apiUrl, apiKey, interfaceType);
            const result = JSON.parse(resultStr);

            if (result.success && result.models && result.models.length > 0) {
                fetchedModels = result.models;
                renderModelDropdown(fetchedModels, dropdown, modelInput);
                dropdown.classList.add('show');
                showSuccess(t('manage.fetchModelsSuccess').replace('{count}', result.models.length));
            } else {
                const msg = result.message?.includes('no_models_found') ? t('manage.fetchModelsEmpty') : t('manage.fetchModelsFailed');
                showError(msg);
            }
        }
    } catch (error) {
        console.error('Failed to fetch models:', error);
        showError(t('manage.fetchModelsFailed') + ': ' + error);
    } finally {
        fetchBtn.disabled = false;
        fetchIcon.textContent = t('manage.fetchModelsBtn');
    }
}

// Render model dropdown
function renderModelDropdown(models, dropdown, input) {
    dropdown.innerHTML = '';
    models.forEach(model => {
        const item = document.createElement('div');
        item.className = 'model-dropdown-item';
        item.textContent = model;
        item.onclick = () => {
            input.value = model;
            dropdown.classList.remove('show');
            updateTestButtonVisibility();
        };
        dropdown.appendChild(item);
    });
}

// Toggle model dropdown
export function toggleModelDropdown() {
    const dropdown = document.getElementById('modelDropdown');
    const modelInput = document.getElementById('endpointModel');
    if (!dropdown || fetchedModels.length === 0) return;

    if (dropdown.classList.contains('show')) {
        dropdown.classList.remove('show');
    } else {
        renderModelDropdown(fetchedModels, dropdown, modelInput);
        dropdown.classList.add('show');
    }
}

// Clear fetched models (call when form closes or interface type changes)
export function clearFetchedModels() {
    fetchedModels = [];
    const dropdown = document.getElementById('modelDropdown');
    if (dropdown) {
        dropdown.innerHTML = '';
        dropdown.classList.remove('show');
    }
}

export function closeEndpointForm() {
    document.getElementById('endpointFormModal').classList.remove('active');
    closeInterfaceTypeDropdown();
    clearFetchedModels();
}

export function toggleApiKeyVisibility() {
    const input = document.getElementById('endpointApiKey');
    const btn = document.getElementById('toggleApiKeyVisibility');
    if (input.type === 'password') {
        input.type = 'text';
        btn.textContent = '🙈';
    } else {
        input.type = 'password';
        btn.textContent = '👁️';
    }
}

export function editEndpoint(endpointId) {
    const endpoint = state.vendorEndpoints.find(ep => ep.id === endpointId);
    if (endpoint) {
        showEndpointForm(endpoint);
    }
}

// Edit endpoint directly from the main endpoint list (not from manage modal)
export async function editEndpointFromList(endpointId, vendorId) {
    try {
        // Load the endpoint data
        if (!window.go?.main?.App?.GetEndpointsByVendorID) return;

        const endpoints = await window.go.main.App.GetEndpointsByVendorID(vendorId);
        const endpoint = endpoints?.find(ep => ep.id === endpointId);

        if (!endpoint) {
            showError('Endpoint not found');
            return;
        }

        // Load vendor info
        let vendorName = '';
        if (window.go?.main?.App?.GetVendors) {
            const vendors = await window.go.main.App.GetVendors();
            const vendor = vendors?.find(v => v.id === vendorId);
            if (vendor) {
                state.selectedVendor = vendor;
                vendorName = vendor.name;
            }
        }

        // If endpoint is active, show read-only detail view
        if (endpoint.active) {
            showEndpointDetail(endpoint, vendorName);
        } else {
            // Show the editable endpoint form
            showEndpointForm(endpoint);
        }
    } catch (error) {
        console.error('Failed to edit endpoint:', error);
        showError('Failed to load endpoint: ' + error.message);
    }
}

// Show read-only endpoint detail modal (for active endpoints)
function showEndpointDetail(endpoint, vendorName) {
    let modal = document.getElementById('endpointDetailModal');
    if (!modal) {
        modal = createEndpointDetailModal();
        document.body.appendChild(modal);
    }

    // Populate detail content
    document.getElementById('detailEndpointName').textContent = endpoint.name;
    document.getElementById('detailVendorName').textContent = vendorName || '-';
    document.getElementById('detailApiUrl').textContent = endpoint.apiUrl;
    document.getElementById('detailInterfaceType').textContent = endpoint.interfaceType;
    document.getElementById('detailModel').textContent = endpoint.model || '-';
    document.getElementById('detailRemark').textContent = endpoint.remark || '-';
    document.getElementById('detailEnabled').textContent = endpoint.enabled ? t('common.active') : t('common.disabled');

    modal.classList.add('active');
}

function createEndpointDetailModal() {
    const modal = document.createElement('div');
    modal.id = 'endpointDetailModal';
    modal.className = 'modal';
    modal.innerHTML = `
        <div class="modal-content">
            <div class="modal-header">
                <h2>📋 ${t('logs.detailTitle')}</h2>
                <button class="modal-close" onclick="closeEndpointDetailModal()">&times;</button>
            </div>
            <div class="modal-body">
                <div class="detail-section">
                    <table class="log-detail-table">
                        <tr>
                            <td class="label">${t('manage.endpointName')}</td>
                            <td class="value" id="detailEndpointName"></td>
                        </tr>
                        <tr>
                            <td class="label">${t('manage.vendors')}</td>
                            <td class="value" id="detailVendorName"></td>
                        </tr>
                        <tr>
                            <td class="label">${t('manage.apiUrl')}</td>
                            <td class="value url-value" id="detailApiUrl"></td>
                        </tr>
                        <tr>
                            <td class="label">${t('manage.interfaceType')}</td>
                            <td class="value" id="detailInterfaceType"></td>
                        </tr>
                        <tr>
                            <td class="label">${t('manage.model')}</td>
                            <td class="value" id="detailModel"></td>
                        </tr>
                        <tr>
                            <td class="label">${t('manage.remark')}</td>
                            <td class="value" id="detailRemark"></td>
                        </tr>
                        <tr>
                            <td class="label">${t('manage.enabled')}</td>
                            <td class="value" id="detailEnabled"></td>
                        </tr>
                    </table>
                </div>
                <div class="detail-note">
                    <p>⚠️ ${t('manage.activeEndpointNote') || 'Active endpoint cannot be edited. Please switch to another endpoint first.'}</p>
                </div>
            </div>
            <div class="modal-footer">
                <button class="btn btn-secondary" onclick="closeEndpointDetailModal()">${t('common.close')}</button>
            </div>
        </div>
    `;
    return modal;
}

export function closeEndpointDetailModal() {
    const modal = document.getElementById('endpointDetailModal');
    if (modal) {
        modal.classList.remove('active');
    }
}

export async function saveEndpoint() {
    const endpointId = parseInt(document.getElementById('endpointId').value) || 0;
    const apiKey = document.getElementById('endpointApiKey').value.trim();
    const vendorId = parseInt(document.getElementById('endpointVendorId')?.value, 10) || state.selectedVendor?.id || 0;

    // 收集 models 映射
    const models = collectModelMappings();

    const endpoint = {
        id: endpointId,
        name: document.getElementById('endpointName').value.trim(),
        apiUrl: document.getElementById('endpointApiUrl').value.trim(),
        apiKey: apiKey,
        interfaceType: document.getElementById('endpointInterfaceType').value,
        vendorId: vendorId,
        model: document.getElementById('endpointModel').value.trim(),
        transformer: document.getElementById('endpointTransformer').value.trim(),
        transformerSet: true,
        proxyUrl: document.getElementById('endpointProxyUrl').value.trim(),
        models: models.length > 0 ? models : null,
        modelsSet: true,
        remark: document.getElementById('endpointRemark').value.trim(),
        priority: parseInt(document.getElementById('endpointPriority').value) || 5,
        enabled: document.getElementById('endpointEnabled').checked,
        active: false
    };

    if (!endpoint.name || !endpoint.apiUrl) {
        showError('Please fill in all required fields');
        return;
    }

    if (!endpointId && !apiKey) {
        showError('API Key is required for new endpoints');
        return;
    }

    try {
        if (window.go?.main?.App?.SaveEndpointData) {
            await window.go.main.App.SaveEndpointData(endpoint);
            closeEndpointForm();
            if (state.selectedVendor) {
                await loadVendorEndpoints(state.selectedVendor.id);
            }
            // Refresh main endpoint list
            await loadEndpoints(state.currentTab);
            showSuccess('Endpoint saved successfully');
        }
    } catch (error) {
        showError(t('manage.saveFailed') + ': ' + error.message);
    }
}

export async function deleteEndpoint() {
    const rawId = document.getElementById('endpointId')?.value;
    const endpointId = parseInt(rawId, 10);
    logInfo(`[Endpoint] deleteEndpoint invoked (rawId=${rawId}, parsedId=${endpointId})`);

    const rawVendorId = document.getElementById('endpointVendorId')?.value;
    const vendorId = parseInt(rawVendorId, 10) || 0;
    await runEndpointDeletion(endpointId, vendorId, 'form');
}

export async function deleteEndpointById(endpointId, vendorId = 0) {
    await runEndpointDeletion(endpointId, vendorId, 'list');
}

async function runEndpointDeletion(endpointId, vendorId, source) {
    const parsedEndpointId = parseInt(endpointId, 10);
    const parsedVendorId = parseInt(vendorId, 10) || 0;
    logInfo(`[Endpoint] delete requested (source=${source}, endpointId=${endpointId}, parsedId=${parsedEndpointId}, vendorId=${parsedVendorId || '-'})`);

    if (!parsedEndpointId) {
        logError('[Endpoint] delete aborted: invalid endpointId');
        showError(t('manage.deleteFailed') + ': invalid endpoint id');
        return;
    }

    const confirmMessage = t('manage.confirmDeleteEndpoint') || 'Confirm delete endpoint?';
    const confirmed = await confirmDialog(confirmMessage, { danger: true });
    if (!confirmed) return;

    try {
        if (!window.go?.main?.App?.DeleteEndpoint) {
            logError('[Endpoint] delete aborted: window.go.main.App.DeleteEndpoint not available');
            showError(t('manage.deleteFailed') + ': backend not available');
            return;
        }

        await window.go.main.App.DeleteEndpoint(parsedEndpointId);
        logInfo(`[Endpoint] backend DeleteEndpoint(${parsedEndpointId}) done`);

        if (source === 'form') {
            closeEndpointForm();
        }

        if (parsedVendorId) {
            if (!state.selectedVendor || state.selectedVendor.id !== parsedVendorId) {
                const vendor = state.vendors?.find(v => v.id === parsedVendorId);
                if (vendor) {
                    state.selectedVendor = vendor;
                    renderVendorList(state.vendors);
                    const addBtn = document.getElementById('addEndpointBtn');
                    if (addBtn) addBtn.disabled = false;
                }
            }
            await loadVendorEndpoints(parsedVendorId);
        } else if (state.selectedVendor) {
            await loadVendorEndpoints(state.selectedVendor.id);
        }

        await loadEndpoints(state.currentTab);
        showSuccess('Endpoint deleted successfully');
    } catch (error) {
        logError(`[Endpoint] delete failed: ${error?.message || error}`);
        showError(t('manage.deleteFailed') + ': ' + error.message);
    }
}

// =============================================================================
// Transformer 相关函数
// =============================================================================

// 缓存转换器列表
let cachedTransformers = null;

// 加载当前 interfaceType 对应的转换器列表
export async function loadTransformersForInterfaceType() {
    const interfaceType = document.getElementById('endpointInterfaceType')?.value || '';
    const dropdown = document.getElementById('transformerDropdown');
    if (!dropdown) return;

    // 获取转换器列表（带缓存）
    if (!cachedTransformers && window.go?.main?.App?.GetTransformers) {
        try {
            cachedTransformers = await window.go.main.App.GetTransformers();
        } catch (e) {
            console.error('Failed to load transformers:', e);
            cachedTransformers = {};
        }
    }

    const transformers = cachedTransformers?.[interfaceType] || [];
    renderTransformerDropdown(transformers);
}

function renderTransformerDropdown(transformers) {
    const dropdown = document.getElementById('transformerDropdown');
    const currentValue = document.getElementById('endpointTransformer')?.value || '';
    if (!dropdown) return;

    dropdown.innerHTML = '';

    // 添加 "无" 选项
    const noneItem = document.createElement('div');
    noneItem.className = 'model-dropdown-item' + (currentValue === '' ? ' selected' : '');
    noneItem.textContent = t('manage.transformerNone');
    noneItem.onclick = () => {
        document.getElementById('endpointTransformer').value = '';
        syncTransformerDisplay();
        closeTransformerDropdown();
    };
    dropdown.appendChild(noneItem);

    // 添加转换器选项
    transformers.forEach(tf => {
        const item = document.createElement('div');
        item.className = 'model-dropdown-item' + (currentValue === tf ? ' selected' : '');
        item.textContent = tf;
        item.onclick = () => {
            document.getElementById('endpointTransformer').value = tf;
            syncTransformerDisplay();
            closeTransformerDropdown();
        };
        dropdown.appendChild(item);
    });
}

function syncTransformerDisplay() {
    const value = document.getElementById('endpointTransformer')?.value || '';
    const display = document.getElementById('endpointTransformerDisplay');
    if (display) {
        display.value = value || t('manage.transformerNone');
    }
}

export function toggleTransformerDropdown() {
    const dropdown = document.getElementById('transformerDropdown');
    if (!dropdown) return;

    if (dropdown.classList.contains('show')) {
        dropdown.classList.remove('show');
    } else {
        loadTransformersForInterfaceType();
        dropdown.classList.add('show');
    }
}

function closeTransformerDropdown() {
    const dropdown = document.getElementById('transformerDropdown');
    if (dropdown) {
        dropdown.classList.remove('show');
    }
}

// =============================================================================
// Model Mappings 相关函数
// =============================================================================

// 渲染 model mappings 列表
export function renderModelMappings(models) {
    const container = document.getElementById('modelMappingsList');
    if (!container) return;

    container.innerHTML = '';

    if (!models || models.length === 0) {
        return;
    }

    models.forEach((mapping, index) => {
        const row = createModelMappingRow(mapping.alias || '', mapping.name || '', index);
        container.appendChild(row);
    });
}

function createModelMappingRow(alias, name, index) {
    const row = document.createElement('div');
    row.className = 'model-mapping-row';
    row.dataset.index = index;
    row.innerHTML = `
        <input type="text" class="mapping-alias" value="${escapeHtml(alias)}" placeholder="${t('manage.modelMappingAlias')}">
        <input type="text" class="mapping-name" value="${escapeHtml(name)}" placeholder="${t('manage.modelMappingName')}">
        <button type="button" class="btn btn-sm btn-icon btn-danger" onclick="removeModelMapping(this)" title="${t('manage.delete')}">×</button>
    `;
    return row;
}

export function addModelMapping() {
    const container = document.getElementById('modelMappingsList');
    if (!container) return;

    const index = container.children.length;
    const row = createModelMappingRow('', '', index);
    container.appendChild(row);
}

export function removeModelMapping(btn) {
    const row = btn.closest('.model-mapping-row');
    if (row) {
        row.remove();
    }
}

// 收集 model mappings
function collectModelMappings() {
    const container = document.getElementById('modelMappingsList');
    if (!container) return [];

    const rows = container.querySelectorAll('.model-mapping-row');
    const models = [];

    rows.forEach(row => {
        const alias = row.querySelector('.mapping-alias')?.value?.trim() || '';
        const name = row.querySelector('.mapping-name')?.value?.trim() || '';
        if (alias || name) {
            models.push({ alias, name });
        }
    });

    return models;
}

function escapeHtml(text) {
    const div = document.createElement('div');
    div.textContent = text;
    return div.innerHTML;
}
