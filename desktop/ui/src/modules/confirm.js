/**
 * Custom confirm dialog module
 * Replaces window.confirm() which doesn't work properly in Wails WebView
 */
import { t } from '../i18n/index.js';

let confirmModal = null;
let resolvePromise = null;

/**
 * Show a custom confirm dialog
 * @param {string} message - The message to display
 * @param {Object} options - Optional settings
 * @param {string} options.title - Dialog title (default: 'Confirm')
 * @param {string} options.confirmText - Confirm button text (default: 'OK')
 * @param {string} options.cancelText - Cancel button text (default: 'Cancel')
 * @param {boolean} options.danger - Use danger style for confirm button
 * @returns {Promise<boolean>} - Resolves to true if confirmed, false if cancelled
 */
export function confirm(message, options = {}) {
    return new Promise((resolve) => {
        resolvePromise = resolve;
        
        const title = options.title || t('common.confirm') || 'Confirm';
        const confirmText = options.confirmText || t('common.ok') || 'OK';
        const cancelText = options.cancelText || t('common.cancel') || 'Cancel';
        const isDanger = options.danger || false;
        
        if (!confirmModal) {
            confirmModal = createConfirmModal();
            document.body.appendChild(confirmModal);
        }
        
        // Update content
        confirmModal.querySelector('.confirm-title').textContent = title;
        confirmModal.querySelector('.confirm-message').textContent = message;
        confirmModal.querySelector('.confirm-ok-btn').textContent = confirmText;
        confirmModal.querySelector('.confirm-cancel-btn').textContent = cancelText;
        
        // Update button style
        const okBtn = confirmModal.querySelector('.confirm-ok-btn');
        if (isDanger) {
            okBtn.classList.remove('btn-primary');
            okBtn.classList.add('btn-danger');
        } else {
            okBtn.classList.remove('btn-danger');
            okBtn.classList.add('btn-primary');
        }
        
        // Show modal
        confirmModal.classList.add('active');
        
        // Focus cancel button for safety
        confirmModal.querySelector('.confirm-cancel-btn').focus();
    });
}

function createConfirmModal() {
    const modal = document.createElement('div');
    modal.id = 'confirmModal';
    modal.className = 'modal confirm-modal';
    modal.innerHTML = `
        <div class="modal-content confirm-modal-content">
            <div class="modal-header">
                <h2 class="confirm-title">Confirm</h2>
            </div>
            <div class="modal-body">
                <p class="confirm-message"></p>
            </div>
            <div class="modal-footer">
                <button class="btn btn-secondary confirm-cancel-btn">Cancel</button>
                <button class="btn btn-primary confirm-ok-btn">OK</button>
            </div>
        </div>
    `;
    
    // Bind events
    const cancelBtn = modal.querySelector('.confirm-cancel-btn');
    const okBtn = modal.querySelector('.confirm-ok-btn');
    
    cancelBtn.addEventListener('click', () => handleConfirm(false));
    okBtn.addEventListener('click', () => handleConfirm(true));
    
    // Close on backdrop click
    modal.addEventListener('click', (e) => {
        if (e.target === modal) {
            handleConfirm(false);
        }
    });
    
    // Handle Escape key
    modal.addEventListener('keydown', (e) => {
        if (e.key === 'Escape') {
            handleConfirm(false);
        } else if (e.key === 'Enter') {
            handleConfirm(true);
        }
    });
    
    return modal;
}

function handleConfirm(result) {
    if (confirmModal) {
        confirmModal.classList.remove('active');
    }
    if (resolvePromise) {
        resolvePromise(result);
        resolvePromise = null;
    }
}
