/**
 * Real-time event manager using Server-Sent Events (SSE)
 */
import { state } from './state.js';
import { loadTokenStats } from './stats.js';
import { loadEndpoints } from './endpoints.js';
import { logInfo, logDebug } from './console.js';

let tokenStatsRefreshTimer = null;
let endpointsRefreshTimer = null;
let endpointsRestoreTimer = null;

class RealTimeManager {
    constructor() {
        this.eventSource = null;
        this.listeners = new Set();
        this.isDestroyed = false;
        this.connectionStatus = false;

        // Active requests map for real-time tracking
        this.activeRequests = new Map();
        this.maxActiveRequests = 50;
    }

    /**
     * Add event listener
     * @param {Function} callback
     * @returns {Function} Unsubscribe function
     */
    addListener(callback) {
        if (typeof callback !== 'function') {
            throw new Error('Callback must be a function');
        }
        this.listeners.add(callback);
        return () => this.listeners.delete(callback);
    }

    /**
     * Connect to SSE endpoint
     */
    async connect() {
        if (this.isDestroyed) return;
        if (this.eventSource) return;

        let sseUrl;
        try {
            if (window.go?.main?.App?.GetSSEURL) {
                sseUrl = await window.go.main.App.GetSSEURL();
            } else {
                sseUrl = `http://localhost:${state.settings.port || 5600}/sse`;
            }
        } catch {
            sseUrl = `http://localhost:5600/sse`;
        }

        console.log(`Connecting to SSE: ${sseUrl}`);

        this.eventSource = new EventSource(sseUrl);

        this.eventSource.onopen = () => {
            console.log('SSE connected');
            this.connectionStatus = true;
            logInfo(`SSE connected to ${sseUrl}`);
            this.notifyListeners({ type: 'connection', status: 'connected' });
        };

        this.eventSource.onerror = () => {
            // EventSource auto-reconnects; just update status
            if (this.connectionStatus) {
                console.log('SSE disconnected, auto-reconnecting...');
                logInfo('SSE disconnected, auto-reconnecting...');
                this.connectionStatus = false;
                this.notifyListeners({ type: 'connection', status: 'disconnected' });
            }
        };

        // Named event listeners — no JSON envelope parsing needed
        this.eventSource.addEventListener('request_log', (e) => {
            try {
                const log = JSON.parse(e.data);
                this.processRequestLog(log);
            } catch (err) {
                console.error('Failed to parse request_log:', err);
            }
        });

        this.eventSource.addEventListener('token_stats', (e) => {
            try {
                const data = JSON.parse(e.data);
                this.notifyListeners({ type: 'token_stats', data });
                this.debouncedRefreshTokenStats();
            } catch (err) {
                console.error('Failed to parse token_stats:', err);
            }
        });

        this.eventSource.addEventListener('debug_log', (e) => {
            try {
                const data = JSON.parse(e.data);
                this.notifyListeners({ type: 'debug_log', data });
            } catch (err) {
                console.error('Failed to parse debug_log:', err);
            }
        });

        this.eventSource.addEventListener('fallback_switch', (e) => {
            try {
                const payload = JSON.parse(e.data);
                this.handleFallbackSwitch(payload);
            } catch (err) {
                console.error('Failed to parse fallback_switch:', err);
            }
        });

        this.eventSource.addEventListener('endpoint_temp_disabled', (e) => {
            try {
                const payload = JSON.parse(e.data);
                this.handleEndpointTempDisabled(payload);
            } catch (err) {
                console.error('Failed to parse endpoint_temp_disabled:', err);
            }
        });
    }

    debouncedRefreshTokenStats() {
        if (tokenStatsRefreshTimer) return;
        tokenStatsRefreshTimer = setTimeout(async () => {
            tokenStatsRefreshTimer = null;
            try {
                await loadTokenStats();
            } catch (e) {
                logDebug(`Failed to refresh token stats: ${e?.message || e}`);
            }
        }, 500);
    }

    handleFallbackSwitch(payload) {
        if (!payload) return;
        const { fromEndpoint, toEndpoint, path, statusCode, errorMessage } = payload;
        const failureInfo = statusCode > 0
            ? `状态码: ${statusCode}`
            : (errorMessage || '请求失败');
        logInfo(`请求失败: ${fromEndpoint}, 路径: ${path}, ${failureInfo}`);
        logInfo(`当前端点故障，转到 ${toEndpoint}`);
    }

    handleEndpointTempDisabled(payload) {
        if (!payload) return;
        const { interfaceType, endpointName, disabledUntil } = payload;
        if (interfaceType && endpointName && disabledUntil) {
            const until = new Date(disabledUntil);
            logInfo(`端点临时禁用: ${interfaceType}-${endpointName}，恢复时间: ${until.toLocaleTimeString()}`);
        }

        if (interfaceType && interfaceType !== state.currentTab) return;

        this.refreshCurrentTabEndpointsDebounced();

        if (endpointsRestoreTimer) {
            clearTimeout(endpointsRestoreTimer);
            endpointsRestoreTimer = null;
        }
        const delayMs = Math.max(0, (disabledUntil || 0) - Date.now() + 1000);
        endpointsRestoreTimer = setTimeout(() => {
            this.refreshCurrentTabEndpointsDebounced();
            endpointsRestoreTimer = null;
        }, delayMs);
    }

    refreshCurrentTabEndpointsDebounced() {
        if (endpointsRefreshTimer) return;
        endpointsRefreshTimer = setTimeout(async () => {
            endpointsRefreshTimer = null;
            try {
                await loadEndpoints(state.currentTab);
            } catch (e) {
                logDebug(`Failed to refresh endpoints: ${e?.message || e}`);
            }
        }, 200);
    }

    processRequestLog(log) {
        if (!log || !log.id) return;

        const requestId = log.id;
        const existingRequest = this.activeRequests.get(requestId);
        const status = this.determineStatus(log);

        const request = {
            request_id: requestId,
            interfaceType: log.interfaceType || '',
            providerName: log.providerName || '',
            endpointName: log.endpointName || '',
            transformer: log.transformer || '',
            method: log.method || 'POST',
            path: log.path || '',
            status: status,
            statusCode: log.statusCode || 0,
            runTime: log.runTime || 0,
            timestamp: log.timestamp || new Date().toISOString(),
            targetUrl: log.targetUrl || '',
            upstreamAuth: log.upstreamAuth || '',
            requestHeaders: log.requestHeaders || {},
            requestStream: log.requestStream || '',
            responseStream: log.responseStream || '',
            displayDuration: log.runTime || 0,
            startTime: existingRequest?.startTime || new Date().toISOString()
        };

        this.activeRequests.set(requestId, request);
        this.cleanupOldRequests();

        let eventType = 'progress';
        if (!existingRequest) {
            eventType = 'started';
        } else if (status === 'COMPLETED' || status === 'FAILED') {
            eventType = status === 'COMPLETED' ? 'completed' : 'failed';
        }

        this.notifyListeners({ type: eventType, request_id: requestId, data: request });

        if (status === 'COMPLETED' || status === 'FAILED') {
            this.activeRequests.delete(requestId);
            this.notifyListeners({ type: 'removed', request_id: requestId });
        }
    }

    determineStatus(log) {
        if (log.status === 'in_progress') return 'PENDING';
        if (log.status === 'success' || log.statusCode === 200) return 'COMPLETED';
        if (log.status === 'canceled') return 'FAILED';
        if (log.status && log.status.startsWith('error')) return 'FAILED';
        return 'PENDING';
    }

    cleanupOldRequests() {
        if (this.activeRequests.size <= this.maxActiveRequests) return;
        const sorted = Array.from(this.activeRequests.entries())
            .sort((a, b) => new Date(b[1].startTime) - new Date(a[1].startTime));
        sorted.slice(this.maxActiveRequests).forEach(([id]) => this.activeRequests.delete(id));
    }

    notifyListeners(event) {
        this.listeners.forEach(listener => {
            try {
                listener(event);
            } catch (error) {
                console.error('Listener error:', error);
            }
        });
    }

    isConnected() {
        return this.connectionStatus;
    }

    getActiveRequests() {
        return Array.from(this.activeRequests.values())
            .sort((a, b) => new Date(b.startTime) - new Date(a.startTime));
    }

    reconnect() {
        this.disconnect();
        this.connect();
    }

    disconnect() {
        if (this.eventSource) {
            this.eventSource.close();
            this.eventSource = null;
        }
        if (tokenStatsRefreshTimer) {
            clearTimeout(tokenStatsRefreshTimer);
            tokenStatsRefreshTimer = null;
        }
        if (endpointsRefreshTimer) {
            clearTimeout(endpointsRefreshTimer);
            endpointsRefreshTimer = null;
        }
        if (endpointsRestoreTimer) {
            clearTimeout(endpointsRestoreTimer);
            endpointsRestoreTimer = null;
        }
        this.connectionStatus = false;
    }

    destroy() {
        this.isDestroyed = true;
        this.disconnect();
        this.listeners.clear();
        this.activeRequests.clear();
    }

    getStatus() {
        return {
            isDestroyed: this.isDestroyed,
            connected: this.connectionStatus,
            activeRequests: this.activeRequests.size,
            listeners: this.listeners.size
        };
    }
}

// Singleton instance
let realtimeManager = null;

export function getRealTimeManager() {
    if (!realtimeManager) {
        realtimeManager = new RealTimeManager();
    }
    return realtimeManager;
}

export async function initRealTime() {
    const manager = getRealTimeManager();
    await manager.connect();
    return manager;
}

export function cleanupRealTime() {
    if (realtimeManager) {
        realtimeManager.destroy();
        realtimeManager = null;
    }
}

export { RealTimeManager };
