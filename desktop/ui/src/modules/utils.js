/**
 * Utility functions
 */

export function formatNumber(num) {
    if (num === undefined || num === null) return '0';
    return num.toLocaleString();
}

// Format tokens with unit (k, m) like "105235 (105.2k)" or "57765938 (57.7m)"
export function formatTokensWithUnit(num) {
    if (num === undefined || num === null || num === 0) return '0';
    
    const formatted = num.toLocaleString();
    let unit = '';
    
    if (num >= 1000000) {
        unit = `${(num / 1000000).toFixed(1)}m`;
    } else if (num >= 1000) {
        unit = `${(num / 1000).toFixed(1)}k`;
    }
    
    return unit ? `${unit}` : formatted;
}

export function showError(message) {
    const toast = document.getElementById('errorToast');
    const msgEl = document.getElementById('errorMessage');
    if (!toast || !msgEl) return;
    msgEl.textContent = message;
    toast.classList.add('show', 'error');
    setTimeout(() => toast.classList.remove('show', 'error'), 3000);
}

export function showSuccess(message) {
    const toast = document.getElementById('errorToast');
    const msgEl = document.getElementById('errorMessage');
    if (!toast || !msgEl) return;
    msgEl.textContent = message;
    toast.classList.add('show', 'success');
    setTimeout(() => toast.classList.remove('show', 'success'), 3000);
}

export async function waitForWails() {
    let attempts = 0;
    while (!window.go && attempts < 50) {
        await new Promise(resolve => setTimeout(resolve, 100));
        attempts++;
    }
}
