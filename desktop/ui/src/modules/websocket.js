/**
 * WebSocket client module
 * Handles basic WebSocket connection for token stats updates.
 * Request logs are now handled by the realtime.js module for enhanced real-time tracking
 */
import { state } from './state.js';
import { loadTokenStats } from './stats.js';
import { loadEndpoints } from './endpoints.js';
import { logInfo, logError, logDebug } from './console.js';

let tokenStatsRefreshTimer = null;
let endpointsRefreshTimer = null;
let endpointsRestoreTimer = null;

export function connectWebSocket() {
    try {
        const port = state.settings.port || 5600;
        const wsUrl = `ws://localhost:${port}/ws`;
        
        state.wsConnection = new WebSocket(wsUrl);
        
        state.wsConnection.onopen = () => {
            console.log('WebSocket (stats) connected');
            logInfo(`WebSocket connected to ${wsUrl}`);
        };
        
        state.wsConnection.onmessage = (event) => {
            try {
                // Handle multiple messages separated by newlines
                const messages = event.data.split('\n').filter(m => m.trim());
                for (const msgStr of messages) {
                    const message = JSON.parse(msgStr);
                    handleWebSocketMessage(message);
                }
            } catch (e) {
                console.error('Failed to parse WebSocket message:', e);
                logError(`Failed to parse WebSocket message: ${e.message}`);
            }
        };
        
        state.wsConnection.onclose = () => {
            console.log('WebSocket (stats) disconnected, reconnecting in 3s...');
            logInfo('WebSocket disconnected, reconnecting in 3s...');
            setTimeout(connectWebSocket, 3000);
        };
        
        state.wsConnection.onerror = (error) => {
            console.error('WebSocket (stats) error:', error);
            logError('WebSocket connection error');
        };
    } catch (error) {
        console.error('Failed to connect WebSocket:', error);
        logError(`Failed to connect WebSocket: ${error.message}`);
        setTimeout(connectWebSocket, 3000);
    }
}

function handleWebSocketMessage(message) {
    switch (message.type) {
        case 'request_log':
            // Request logs are now handled by realtime.js module
            // This is kept for backward compatibility but realtime.js provides enhanced tracking
            break;
            
        case 'token_stats':
            // Token usage is persisted into SQLite; refresh aggregated stats with a small debounce.
            if (tokenStatsRefreshTimer) return;
            tokenStatsRefreshTimer = setTimeout(async () => {
                tokenStatsRefreshTimer = null;
                try {
                    await loadTokenStats();
                } catch (e) {
                    logDebug(`Failed to refresh token stats: ${e?.message || e}`);
                }
            }, 500);
            break;
            
        case 'fallback_switch':
            // Handle endpoint fallback switch notification
            handleFallbackSwitch(message.payload);
            break;

        case 'endpoint_temp_disabled':
            handleEndpointTempDisabled(message.payload);
            break;
    }
}

/**
 * Handle fallback switch notification
 * Logs the switch event to console with details
 */
function handleFallbackSwitch(payload) {
    if (!payload) return;
    
    const { fromVendor, fromEndpoint, toVendor, toEndpoint, path, statusCode, errorMessage } = payload;
    
    // Log failure info
    const failureInfo = statusCode > 0 
        ? `状态码: ${statusCode}` 
        : (errorMessage || '请求失败');
    
    logInfo(`请求失败: ${fromVendor}-${fromEndpoint}, 路径: ${path}, ${failureInfo}`);
    logInfo(`当前端点故障，转到 ${toVendor}-${toEndpoint}`);
}

async function refreshCurrentTabEndpointsDebounced() {
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

function handleEndpointTempDisabled(payload) {
    if (!payload) return;

    const { interfaceType, endpointName, disabledUntil } = payload;
    if (interfaceType && endpointName && disabledUntil) {
        const until = new Date(disabledUntil);
        logInfo(`端点临时禁用: ${interfaceType}-${endpointName}，恢复时间: ${until.toLocaleTimeString()}`);
    }

    // Only refresh current tab UI to avoid rendering other interface types into the same DOM.
    if (interfaceType && interfaceType !== state.currentTab) return;

    refreshCurrentTabEndpointsDebounced();

    // Refresh once more around the restore time to reflect automatic recovery.
    if (endpointsRestoreTimer) {
        clearTimeout(endpointsRestoreTimer);
        endpointsRestoreTimer = null;
    }
    const delayMs = Math.max(0, (disabledUntil || 0) - Date.now() + 1000);
    endpointsRestoreTimer = setTimeout(() => {
        refreshCurrentTabEndpointsDebounced();
        endpointsRestoreTimer = null;
    }, delayMs);
}
