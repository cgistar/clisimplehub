/**
 * Token statistics display module (New Design)
 * - Time range tabs (today, yesterday, week, month, all)
 * - Grouped by vendor with endpoint details
 * - Clear token functionality
 */
import { state } from './state.js';
import { t } from '../i18n/index.js';
import { formatTokensWithUnit } from './utils.js';
import { confirm } from './confirm.js';

// Current time range selection
let currentTimeRange = 'today';

export async function loadTokenStats() {
    try {
        if (!window.go?.main?.App?.GetStatsByInterfaceType) return;
        
        const stats = await window.go.main.App.GetStatsByInterfaceType(currentTimeRange);
        state.tokenStats = stats || [];
        renderTokenStats(stats || []);
    } catch (error) {
        console.error('Failed to load token stats:', error);
    }
}

export function setTimeRange(range) {
    currentTimeRange = range;
    loadTokenStats();
}

export function getCurrentTimeRange() {
    return currentTimeRange;
}

export async function clearTokenStats() {
    const confirmed = await confirm(t('stats.clearConfirm'), { danger: true });
    if (!confirmed) return;
    
    try {
        if (!window.go?.main?.App?.ClearTokenStats) {
            alert('ClearTokenStats method not available');
            return;
        }
        
        await window.go.main.App.ClearTokenStats(currentTimeRange);
        await loadTokenStats();
        alert(t('stats.clearSuccess'));
    } catch (error) {
        console.error('Failed to clear token stats:', error);
        alert('Failed to clear stats: ' + (error?.message || error));
    }
}

export function renderTokenStats(stats) {
    const container = document.getElementById('statsContainer');
    if (!container) return;
    
    if (!stats || stats.length === 0) {
        container.innerHTML = `<div class="empty-state">${t('stats.noStats')}</div>`;
        return;
    }
    
    // Render interface type cards with endpoint tables
    container.innerHTML = stats.map(typeStats => renderInterfaceTypeCard(typeStats)).join('');
}

function renderInterfaceTypeCard(typeStats) {
    const totalFormatted = formatTokensWithUnit(typeStats.total);
    const displayName = typeStats.interfaceType || 'unknown';
    
    return `
        <div class="vendor-stats-card">
            <div class="vendor-stats-header">
                <h3 class="vendor-stats-name">${displayName}</h3>
                <span class="vendor-stats-total">${t('stats.total')} ${totalFormatted}</span>
            </div>
            <div class="vendor-stats-summary">
                <div class="summary-item">
                    <span class="summary-label">${t('stats.input')}:</span>
                    <span class="summary-value">${formatTokensWithUnit(typeStats.inputTokens)}</span>
                </div>
                <div class="summary-item">
                    <span class="summary-label">${t('stats.cachedCreate')}:</span>
                    <span class="summary-value">${formatTokensWithUnit(typeStats.cachedCreate)}</span>
                </div>
                <div class="summary-item">
                    <span class="summary-label">${t('stats.cachedRead')}:</span>
                    <span class="summary-value">${formatTokensWithUnit(typeStats.cachedRead)}</span>
                </div>
                <div class="summary-item">
                    <span class="summary-label">${t('stats.output')}:</span>
                    <span class="summary-value">${formatTokensWithUnit(typeStats.outputTokens)}</span>
                </div>
                <div class="summary-item">
                    <span class="summary-label">${t('stats.reasoning')}:</span>
                    <span class="summary-value">${formatTokensWithUnit(typeStats.reasoning)}</span>
                </div>
                <div class="summary-item summary-total">
                    <span class="summary-label">${t('stats.total')}:</span>
                    <span class="summary-value">${totalFormatted}</span>
                </div>
            </div>
            ${typeStats.endpoints && typeStats.endpoints.length > 0 ? renderEndpointTable(typeStats.endpoints) : ''}
        </div>
    `;
}

function renderEndpointTable(endpoints) {
    const showDate = currentTimeRange === 'all';
    
    return `
        <table class="endpoint-stats-table">
            <thead>
                <tr>
                    <th>${t('stats.endpoint')}</th>
                    ${showDate ? `<th>${t('stats.date')}</th>` : ''}
                    <th>${t('stats.requestCount')}</th>
                    <th>${t('stats.input')}</th>
                    <th>${t('stats.cachedCreate')}</th>
                    <th>${t('stats.cachedRead')}</th>
                    <th>${t('stats.output')}</th>
                    <th>${t('stats.reasoning')}</th>
                    <th>${t('stats.total')}</th>
                </tr>
            </thead>
            <tbody>
                ${endpoints.map(ep => `
                    <tr>
                        <td class="endpoint-name-cell">${ep.vendorName} - ${ep.endpointName}</td>
                        ${showDate ? `<td>${ep.date || ''}</td>` : ''}
                        <td>${ep.requestCount || 0}</td>
                        <td>${formatTokensWithUnit(ep.inputTokens)}</td>
                        <td>${formatTokensWithUnit(ep.cachedCreate)}</td>
                        <td>${formatTokensWithUnit(ep.cachedRead)}</td>
                        <td>${formatTokensWithUnit(ep.outputTokens)}</td>
                        <td>${formatTokensWithUnit(ep.reasoning)}</td>
                        <td class="endpoint-total-cell">${formatTokensWithUnit(ep.total)}</td>
                    </tr>
                `).join('')}
            </tbody>
        </table>
    `;
}

// Show stats modal with time range tabs
export function showStatsModal() {
    console.log('[showStatsModal] Called');
    let modal = document.getElementById('statsModal');
    if (modal) {
        console.log('[showStatsModal] Modal already exists, removing and recreating...');
        modal.remove();
    }
    modal = createStatsModal();
    document.body.appendChild(modal);
    modal.classList.add('active');
    loadStatsModalContent();
}

export function closeStatsModal() {
    const modal = document.getElementById('statsModal');
    if (modal) {
        modal.classList.remove('active');
    }
}

function createStatsModal() {
    const modal = document.createElement('div');
    modal.id = 'statsModal';
    modal.className = 'modal';
    modal.innerHTML = `
        <div class="modal-content modal-xlarge">
            <div class="modal-header">
                <h2>Token ${t('stats.title')}</h2>
                <button class="modal-close" id="statsModalCloseBtn">&times;</button>
            </div>
            <div class="stats-modal-body">
                <div class="stats-time-tabs">
                    <button class="time-tab active" data-range="today">
                        📅 ${t('stats.timeRange.today')}
                    </button>
                    <button class="time-tab" data-range="yesterday">
                        📆 ${t('stats.timeRange.yesterday')}
                    </button>
                    <button class="time-tab" data-range="week">
                        📊 ${t('stats.timeRange.week')}
                    </button>
                    <button class="time-tab" data-range="month">
                        📈 ${t('stats.timeRange.month')}
                    </button>
                    <button class="time-tab" data-range="all">
                        🗂️ ${t('stats.timeRange.all')}
                    </button>
                </div>
                <div class="stats-content" id="statsModalContent">
                    <div class="loading">${t('common.loading')}</div>
                </div>
            </div>
            <div class="modal-footer">
                <button class="btn btn-primary" id="statsRefreshBtn">
                    ${t('stats.refresh')}
                </button>
                <button class="btn btn-danger" id="statsClearBtn">
                    ${t('stats.clear')}
                </button>
                <button class="btn btn-secondary" id="statsCloseBtn">
                    ${t('stats.close')}
                </button>
            </div>
        </div>
    `;
    
    // Bind event listeners
    console.log('[createStatsModal] Binding event listeners...');
    
    const closeBtn = modal.querySelector('#statsModalCloseBtn');
    const closeBtnFooter = modal.querySelector('#statsCloseBtn');
    const refreshBtn = modal.querySelector('#statsRefreshBtn');
    const clearBtn = modal.querySelector('#statsClearBtn');
    
    console.log('[createStatsModal] Found buttons:', {
        closeBtn: !!closeBtn,
        closeBtnFooter: !!closeBtnFooter,
        refreshBtn: !!refreshBtn,
        clearBtn: !!clearBtn
    });
    
    if (closeBtn) closeBtn.addEventListener('click', closeStatsModal);
    if (closeBtnFooter) closeBtnFooter.addEventListener('click', closeStatsModal);
    if (refreshBtn) refreshBtn.addEventListener('click', refreshStats);
    if (clearBtn) {
        clearBtn.addEventListener('click', () => {
            console.log('[statsClearBtn] Click event fired!');
            clearStatsData();
        });
        console.log('[createStatsModal] Clear button event listener attached');
    }
    
    // Bind time tab clicks
    modal.querySelectorAll('.time-tab').forEach(tab => {
        tab.addEventListener('click', () => {
            const range = tab.dataset.range;
            if (range) setStatsTimeRange(range);
        });
    });
    
    console.log('[createStatsModal] Modal created and events bound');
    return modal;
}

// Update time range tab and reload stats
export function setStatsTimeRange(range) {
    currentTimeRange = range;
    
    // Update tab active state
    const tabs = document.querySelectorAll('.time-tab');
    tabs.forEach(tab => {
        if (tab.dataset.range === range) {
            tab.classList.add('active');
        } else {
            tab.classList.remove('active');
        }
    });
    
    loadStatsModalContent();
}

async function loadStatsModalContent() {
    const container = document.getElementById('statsModalContent');
    if (!container) return;
    
    container.innerHTML = `<div class="loading">${t('common.loading')}</div>`;
    
    try {
        if (!window.go?.main?.App?.GetStatsByInterfaceType) return;
        
        const stats = await window.go.main.App.GetStatsByInterfaceType(currentTimeRange);
        
        if (!stats || stats.length === 0) {
            container.innerHTML = `<div class="empty-state">${t('stats.noStats')}</div>`;
            return;
        }
        
        container.innerHTML = stats.map(typeStats => renderInterfaceTypeCard(typeStats)).join('');
    } catch (error) {
        console.error('Failed to load stats:', error);
        container.innerHTML = `<div class="empty-state">Error loading stats</div>`;
    }
}

export async function refreshStats() {
    await loadStatsModalContent();
}

export async function clearStatsData() {
    console.log('[clearStatsData] Function called');
    
    // Use custom confirm dialog
    const confirmed = await confirm(t('stats.clearConfirm'), {
        title: t('stats.clear'),
        confirmText: t('common.ok'),
        cancelText: t('common.cancel'),
        danger: true
    });
    
    console.log('[clearStatsData] Confirm result:', confirmed);
    if (!confirmed) return;
    
    try {
        if (!window.go?.main?.App?.ClearTokenStats) {
            console.error('ClearTokenStats method not available');
            alert('ClearTokenStats method not available');
            return;
        }
        
        console.log('[clearStatsData] Clearing stats for time range:', currentTimeRange);
        await window.go.main.App.ClearTokenStats(currentTimeRange);
        console.log('[clearStatsData] Stats cleared successfully');
        await Promise.all([loadTokenStats(), loadStatsModalContent()]);
        console.log('[clearStatsData] UI refreshed');
    } catch (error) {
        console.error('[clearStatsData] Failed to clear stats:', error);
        alert('Failed to clear stats: ' + error.message);
    }
}
