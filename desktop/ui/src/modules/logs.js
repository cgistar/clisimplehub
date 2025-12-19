/**
 * Request logs display module with real-time support
 * Inspired by cli_proxy's real-time request system
 */
import { state } from './state.js';
import { t } from '../i18n/index.js';
import { getRealTimeManager } from './realtime.js';

// Real-time requests state
let realtimeRequests = new Map();
let unsubscribe = null;
let realtimeDurationInterval = null;
const REALTIME_DURATION_REFRESH_MS = 200;

/**
 * Initialize logs module with real-time support
 */
export function initLogs() {
    const manager = getRealTimeManager();
    
    // Subscribe to real-time events
    unsubscribe = manager.addListener(handleRealtimeEvent);
    
    // Initial render
    renderLogs(state.recentLogs || []);
    renderRealtimeSection();
}

/**
 * Cleanup logs module
 */
export function cleanupLogs() {
    if (unsubscribe) {
        unsubscribe();
        unsubscribe = null;
    }
    stopRealtimeDurationRefresh();
    realtimeRequests.clear();
}

/**
 * Handle real-time events from WebSocket
 */
function handleRealtimeEvent(event) {
    switch (event.type) {
        case 'connection':
            updateConnectionStatus(event.status === 'connected');
            break;
        case 'started':
        case 'progress':
            if (event.data) {
                // Only show in realtime section if still pending
                if (event.data.status === 'PENDING' || event.data.status === 'STREAMING') {
                    realtimeRequests.set(event.request_id, event.data);
                    renderRealtimeRequests();
                }
            }
            break;
        case 'completed':
        case 'failed':
            if (event.data) {
                // Remove from realtime section immediately
                realtimeRequests.delete(event.request_id);
                renderRealtimeRequests();
                // Add to recent logs
                updateRecentLog(event.data);
            }
            break;
        case 'removed':
            realtimeRequests.delete(event.request_id);
            renderRealtimeRequests();
            break;
        case 'token_stats':
            // Token stats are handled by stats module
            break;
    }
}

/**
 * Update connection status indicator
 */
function updateConnectionStatus(connected) {
    const indicator = document.getElementById('wsConnectionStatus');
    if (indicator) {
        indicator.className = `ws-status ${connected ? 'connected' : 'disconnected'}`;
        indicator.title = connected ? t('logs.wsConnected') : t('logs.wsDisconnected');
    }
}

/**
 * Update recent log entry (upsert by ID)
 */
function updateRecentLog(request) {
    if (!request || !request.request_id) return;
    
    const log = {
        id: request.request_id,
        interfaceType: request.interfaceType,
        vendorName: request.vendorName,
        endpointName: request.endpointName,
        path: request.path,
        runTime: request.runTime,
        status: request.status === 'COMPLETED' ? 'success' : 
                request.status === 'FAILED' ? 'error' : 'in_progress',
        statusCode: request.statusCode,
        timestamp: request.timestamp,
        method: request.method,
        targetUrl: request.targetUrl,
        upstreamAuth: request.upstreamAuth,
        requestHeaders: request.requestHeaders,
        responseStream: request.responseStream
    };
    
    // Update state
    const existingIndex = state.recentLogs.findIndex(l => l.id === log.id);
    if (existingIndex >= 0) {
        state.recentLogs[existingIndex] = log;
    } else {
        state.recentLogs.unshift(log);
        if (state.recentLogs.length > 5) {
            state.recentLogs = state.recentLogs.slice(0, 5);
        }
    }
    
    renderLogs(state.recentLogs);
}

/**
 * Load recent logs from backend
 */
export async function loadRecentLogs() {
    try {
        if (!window.go?.main?.App?.GetRecentLogs) return;
        
        const logs = await window.go.main.App.GetRecentLogs();
        state.recentLogs = logs || [];
        renderLogs(logs || []);
    } catch (error) {
        console.error('Failed to load logs:', error);
    }
}

/**
 * Render real-time section header
 */
function renderRealtimeSection() {
    const container = document.getElementById('logsContainer');
    if (!container) return;
    
    // Check if realtime section already exists
    let realtimeSection = document.getElementById('realtimeSection');
    if (!realtimeSection) {
        realtimeSection = document.createElement('div');
        realtimeSection.id = 'realtimeSection';
        realtimeSection.className = 'realtime-section';
        container.parentNode.insertBefore(realtimeSection, container);
    }
    
    const manager = getRealTimeManager();
    const connected = manager.isConnected();
    
    realtimeSection.innerHTML = `
        <div class="realtime-header">
            <div class="realtime-title">
                <span class="realtime-icon">⚡</span>
                <span>${t('logs.realtime')}</span>
                <span id="wsConnectionStatus" class="ws-status ${connected ? 'connected' : 'disconnected'}" 
                      title="${connected ? t('logs.wsConnected') : t('logs.wsDisconnected')}"></span>
            </div>
            <div class="realtime-actions">
                <button class="btn btn-xs" onclick="toggleRealtimeConnection()" id="wsToggleBtn">
                    ${connected ? t('logs.disconnect') : t('logs.connect')}
                </button>
            </div>
        </div>
        <div class="realtime-requests" id="realtimeRequests">
            <div class="empty-state-sm">${t('logs.noRealtime')}</div>
        </div>
    `;
    
    renderRealtimeRequests();
}

/**
 * Render real-time requests list (only active/pending requests)
 */
function renderRealtimeRequests() {
    const container = document.getElementById('realtimeRequests');
    if (!container) return;
    
    // Only show pending/streaming requests
    const activeRequests = Array.from(realtimeRequests.values())
        .filter(req => req.status === 'PENDING' || req.status === 'STREAMING')
        .sort((a, b) => new Date(b.startTime) - new Date(a.startTime));
    
    if (activeRequests.length === 0) {
        container.innerHTML = `<div class="empty-state-sm">${t('logs.noRealtime')}</div>`;
        stopRealtimeDurationRefresh();
        return;
    }
    
    container.innerHTML = activeRequests.map(req => `
        <div class="realtime-item ${getStatusClass(req.status)}" 
             onclick="showLogDetail('${req.request_id}')" 
             data-request-id="${req.request_id}">
            <div class="realtime-item-header">
                <span class="realtime-type badge-sm">${req.interfaceType || 'API'}</span>
                <span class="realtime-endpoint">${formatEndpoint(req)}</span>
                <span class="realtime-status ${getStatusClass(req.status)}">
                    ${getStatusIcon(req.status)} ${getStatusText(req.status)}
                </span>
            </div>
            <div class="realtime-item-details">
                <span class="realtime-path" title="${req.path}">${req.method || 'POST'} ${req.path}</span>
                <span class="realtime-duration">${formatDuration(req)}</span>
            </div>
            <div class="realtime-progress"><div class="progress-bar"></div></div>
        </div>
    `).join('');

    syncRealtimeDurationRefresh();
}

/**
 * Render logs list
 */
export function renderLogs(logs) {
    const container = document.getElementById('logsContainer');
    const countEl = document.getElementById('logCount');
    
    if (!container) return;
    
    if (countEl) {
        countEl.textContent = `${logs.length} / 5`;
    }
    
    if (!logs || logs.length === 0) {
        container.innerHTML = `<div class="empty-state">${t('logs.noLogs')}</div>`;
        return;
    }
    
    container.innerHTML = logs.map(log => `
        <div class="log-item ${getLogClass(log)}" onclick="showLogDetail('${log.id}')" style="cursor: pointer;">
            <div class="log-header">
                <span class="log-type badge">${log.interfaceType}</span>
                <span class="log-vendor">${formatService(log)}</span>
                <span class="log-status ${getLogStatusClass(log)}">${formatStatus(log)}</span>
            </div>
            <div class="log-details">
                <span class="log-path">${log.path}</span>
                <span class="log-time">${formatRunTime(log)}</span>
            </div>
            <div class="log-timestamp">${formatTimestamp(log.timestamp)}</div>
        </div>
    `).join('');
}

/**
 * Show log detail modal
 */
export async function showLogDetail(logId) {
    try {
        // First check realtime requests
        let log = realtimeRequests.get(logId);
        
        if (!log) {
            // Try backend
            if (window.go?.main?.App?.GetLogDetail) {
                const detail = await window.go.main.App.GetLogDetail(logId);
                if (detail) {
                    log = {
                        request_id: detail.id,
                        interfaceType: detail.interfaceType,
                        vendorName: detail.vendorName,
                        endpointName: detail.endpointName,
                        method: detail.method,
                        path: detail.path,
                        status: detail.status === 'success' ? 'COMPLETED' : 
                                detail.status === 'in_progress' ? 'PENDING' : 'FAILED',
                        statusCode: detail.statusCode,
                        runTime: detail.runTime,
                        timestamp: detail.timestamp,
                        targetUrl: detail.targetUrl,
                        upstreamAuth: detail.upstreamAuth,
                        requestHeaders: detail.requestHeaders,
                        responseStream: detail.responseStream
                    };
                }
            }
        }
        
        if (!log) {
            // Fallback to state
            const stateLog = state.recentLogs.find(l => l.id === logId);
            if (stateLog) {
                log = {
                    request_id: stateLog.id,
                    interfaceType: stateLog.interfaceType,
                    vendorName: stateLog.vendorName,
                    endpointName: stateLog.endpointName,
                    method: stateLog.method || 'POST',
                    path: stateLog.path,
                    status: stateLog.status === 'success' ? 'COMPLETED' : 
                            stateLog.status === 'in_progress' ? 'PENDING' : 'FAILED',
                    statusCode: stateLog.statusCode,
                    runTime: stateLog.runTime,
                    timestamp: stateLog.timestamp,
                    targetUrl: stateLog.targetUrl,
                    upstreamAuth: stateLog.upstreamAuth,
                    requestHeaders: stateLog.requestHeaders,
                    responseStream: stateLog.responseStream
                };
            }
        }
        
        if (log) {
            renderLogDetailModal(log);
        }
    } catch (error) {
        console.error('Failed to load log detail:', error);
    }
}

/**
 * Render log detail modal
 */
function renderLogDetailModal(log) {
    const existingModal = document.getElementById('logDetailModal');
    if (existingModal) existingModal.remove();
    
    const statusText = getStatusText(log.status);
    const statusClass = getStatusClass(log.status);
    
    const modal = document.createElement('div');
    modal.id = 'logDetailModal';
    modal.className = 'modal active';
    modal.setAttribute('data-request-id', log.request_id || '');
    modal.innerHTML = `
        <div class="modal-content modal-large">
            <div class="modal-header">
                <h2>📋 ${t('logs.detailTitle')}</h2>
                <button class="modal-close" onclick="closeLogDetailModal()">&times;</button>
            </div>
            <div class="modal-body log-detail-body">
                <div class="log-detail-section">
                    <h3>${t('logs.basicInfo')}</h3>
                    <table class="log-detail-table">
                        <tr>
                            <td class="label">${t('logs.requestId')}</td>
                            <td class="value">${log.request_id || '-'}</td>
                            <td class="label">${t('logs.service')}</td>
                            <td class="value">${log.interfaceType || '-'}</td>
                        </tr>
                        <tr>
                            <td class="label">${t('logs.vendor')}</td>
                            <td class="value">${log.vendorName || log.endpointName || '-'}</td>
                            <td class="label">${t('logs.statusLabel')}</td>
                            <td class="value"><span class="log-status-badge ${statusClass}">${statusText}</span></td>
                        </tr>
                        <tr>
                            <td class="label">${t('logs.method')}</td>
                            <td class="value">${log.method || 'POST'}</td>
                            <td class="label">${t('logs.path')}</td>
                            <td class="value">${log.path || '-'}</td>
                        </tr>
                        <tr>
                            <td class="label">${t('logs.duration')}</td>
                            <td class="value" id="logDetailDuration">${formatDetailDuration(log)}</td>
                            <td class="label">${t('logs.statusCode')}</td>
                            <td class="value">${log.statusCode || '-'}</td>
                        </tr>
                        <tr>
                            <td class="label">${t('logs.startTime')}</td>
                            <td class="value">${formatTimestamp(log.timestamp || log.startTime)}</td>
                            <td class="label">${t('logs.targetUrl')}</td>
                            <td class="value url-value">${log.targetUrl || '-'}</td>
                        </tr>
                        <tr>
                            <td class="label">${t('logs.upstreamAuth')}</td>
                            <td class="value">${log.upstreamAuth || '-'}</td>
                            <td class="label"></td>
                            <td class="value"></td>
                        </tr>
                    </table>
                </div>
                ${log.responseStream ? `
                <div class="log-detail-section">
                    <h3>${t('logs.responseStream')}</h3>
                    <pre class="log-stream-content">${escapeHtml(log.responseStream)}</pre>
                </div>` : ''}
                ${log.requestHeaders && Object.keys(log.requestHeaders).length > 0 ? `
                <div class="log-detail-section">
                    <h3>${t('logs.requestHeaders')}</h3>
                    <pre class="log-headers-content">${formatHeaders(log.requestHeaders)}</pre>
                </div>` : ''}
            </div>
        </div>
    `;
    
    document.body.appendChild(modal);
    modal.addEventListener('click', (e) => {
        if (e.target === modal) closeLogDetailModal();
    });
}

/**
 * Close log detail modal
 */
export function closeLogDetailModal() {
    const modal = document.getElementById('logDetailModal');
    if (modal) modal.remove();
}

/**
 * Toggle WebSocket connection
 */
export function toggleRealtimeConnection() {
    const manager = getRealTimeManager();
    if (manager.isConnected()) {
        manager.disconnect();
    } else {
        manager.reconnect();
    }
    
    // Update button text
    setTimeout(() => {
        const btn = document.getElementById('wsToggleBtn');
        if (btn) {
            btn.textContent = manager.isConnected() ? t('logs.disconnect') : t('logs.connect');
        }
        updateConnectionStatus(manager.isConnected());
    }, 100);
}

// Helper functions
function escapeHtml(text) {
    if (!text) return '';
    const div = document.createElement('div');
    div.textContent = text;
    return div.innerHTML;
}

function formatHeaders(headers) {
    if (!headers) return '';
    return Object.entries(headers)
        .map(([key, value]) => `"${key}": "${value}"`)
        .join(',\n');
}

function formatService(log) {
    const endpointName = log.endpointName || '';
    const vendorName = log.vendorName || '';
    if (endpointName && vendorName) return `${endpointName} (${vendorName})`;
    return endpointName || vendorName || 'Unknown';
}

function formatEndpoint(req) {
    if (req.endpointName && req.vendorName) {
        return `${req.endpointName} (${req.vendorName})`;
    }
    return req.endpointName || req.vendorName || 'Unknown';
}

function formatStatus(log) {
    if (log.status === 'in_progress') return t('logs.inProgress');
    const statusCode = Number(log.statusCode || 0);
    if (statusCode && statusCode !== 200) return `${t('logs.done')} (${statusCode})`;
    return t('logs.done');
}

function formatRunTime(log) {
    if (log.status === 'in_progress') return '--';
    return `${log.runTime}ms`;
}

function formatDuration(req) {
    const durationMs = getDisplayDurationMs(req);
    if (typeof durationMs !== 'number') return '...';
    return `${durationMs}ms`;
}

function formatDetailDuration(log) {
    const durationMs = getDisplayDurationMs(log);
    if (log.status === 'PENDING' && typeof durationMs !== 'number') return '--';
    if (typeof durationMs !== 'number') return `${log.runTime || log.displayDuration || 0}ms`;
    return `${durationMs}ms`;
}

function formatTimestamp(timestamp) {
    if (!timestamp) return '-';
    if (typeof timestamp === 'string') {
        // If already formatted, return as is
        if (timestamp.includes(':') && !timestamp.includes('T')) {
            return timestamp;
        }
        const date = new Date(timestamp);
        return date.toLocaleTimeString();
    }
    return new Date(timestamp).toLocaleTimeString();
}

function getLogClass(log) {
    if (log.status === 'in_progress') return 'in-progress';
    return log.status === 'success' ? 'success' : 'error';
}

function getLogStatusClass(log) {
    if (log.status === 'in_progress') return 'in-progress';
    return log.status === 'success' ? 'success' : 'error';
}

function getStatusClass(status) {
    switch (status) {
        case 'PENDING':
        case 'STREAMING':
            return 'in-progress';
        case 'COMPLETED':
            return 'success';
        case 'FAILED':
            return 'error';
        default:
            return '';
    }
}

function getStatusText(status) {
    switch (status) {
        case 'PENDING':
            return t('logs.pending');
        case 'STREAMING':
            return t('logs.streaming');
        case 'COMPLETED':
            return t('logs.completed');
        case 'FAILED':
            return t('logs.failed');
        default:
            return status || '-';
    }
}

function getStatusIcon(status) {
    switch (status) {
        case 'PENDING':
        case 'STREAMING':
            return '⏳';
        case 'COMPLETED':
            return '✓';
        case 'FAILED':
            return '✗';
        default:
            return '';
    }
}

function getRequestStartMs(req) {
    const startCandidate = req?.startTime || req?.timestamp;
    if (!startCandidate) return null;
    const ms = new Date(startCandidate).getTime();
    if (!Number.isFinite(ms)) return null;
    return ms;
}

function getDisplayDurationMs(req) {
    if (!req) return null;
    if (req.status === 'COMPLETED' || req.status === 'FAILED') {
        const completedMs = Number(req.runTime || req.displayDuration || 0);
        return Number.isFinite(completedMs) ? completedMs : 0;
    }

    const startMs = getRequestStartMs(req);
    if (startMs === null) return null;
    const elapsedMs = Date.now() - startMs;
    return Math.max(0, Math.floor(elapsedMs));
}

function updateRealtimeDurations() {
    const container = document.getElementById('realtimeRequests');
    if (!container) {
        stopRealtimeDurationRefresh();
        return;
    }

    container.querySelectorAll('.realtime-item[data-request-id]').forEach(item => {
        const requestId = item.getAttribute('data-request-id');
        const req = realtimeRequests.get(requestId);
        if (!req) return;

        const durationEl = item.querySelector('.realtime-duration');
        if (!durationEl) return;

        const durationMs = getDisplayDurationMs(req);
        if (typeof durationMs !== 'number') return;
        req.displayDuration = durationMs;
        durationEl.textContent = `${durationMs}ms`;
    });

    const detailModal = document.getElementById('logDetailModal');
    const detailDuration = document.getElementById('logDetailDuration');
    if (detailModal && detailDuration) {
        const detailRequestId = detailModal.getAttribute('data-request-id');
        const req = detailRequestId ? realtimeRequests.get(detailRequestId) : null;
        if (req && (req.status === 'PENDING' || req.status === 'STREAMING')) {
            const durationMs = getDisplayDurationMs(req);
            if (typeof durationMs === 'number') {
                detailDuration.textContent = `${durationMs}ms`;
            }
        }
    }
}

function syncRealtimeDurationRefresh() {
    const hasActive = Array.from(realtimeRequests.values())
        .some(req => req?.status === 'PENDING' || req?.status === 'STREAMING');

    if (!hasActive) {
        stopRealtimeDurationRefresh();
        return;
    }
    startRealtimeDurationRefresh();
}

function startRealtimeDurationRefresh() {
    if (realtimeDurationInterval) return;
    realtimeDurationInterval = setInterval(updateRealtimeDurations, REALTIME_DURATION_REFRESH_MS);
    updateRealtimeDurations();
}

function stopRealtimeDurationRefresh() {
    if (!realtimeDurationInterval) return;
    clearInterval(realtimeDurationInterval);
    realtimeDurationInterval = null;
}

// Export for global access
window.showLogDetail = showLogDetail;
window.closeLogDetailModal = closeLogDetailModal;
window.toggleRealtimeConnection = toggleRealtimeConnection;
