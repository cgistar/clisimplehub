/**
 * Vendor management modal module
 */
import { state } from './state.js';
import { t } from '../i18n/index.js';
import { showError, showSuccess } from './utils.js';
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
        <div class="vendor-item">
            <div class="vendor-info">
                <div class="vendor-name">${v.name}</div>
                <div class="vendor-url">${v.apiUrl}</div>
            </div>
            <div class="vendor-actions">
                <button class="btn btn-sm btn-icon" onclick="editVendor(${v.id})" title="Edit">✏️</button>
                <button class="btn btn-sm btn-icon" onclick="deleteVendorById(${v.id})" title="${t('manage.delete') || 'Delete'}">🗑️</button>
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

        await loadVendors();
        showSuccess('Vendor deleted successfully');
    } catch (error) {
        logError(`[Vendor] delete failed: ${error?.message || error}`);
        showError(t('manage.deleteFailed') + ': ' + error.message);
    }
}