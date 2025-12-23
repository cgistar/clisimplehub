/**
 * Real-time request manager
 * Manages WebSocket connection and event distribution for live request updates
 * Inspired by cli_proxy's RealTimeManager pattern
 */
import { state } from './state.js';

class RealTimeManager {
    constructor() {
        this.connection = null;
        this.reconnectAttempts = 0;
        this.maxReconnectAttempts = 5;
        this.reconnectDelay = 1000;
        this.listeners = new Set();
        this.isDestroyed = false;
        this.heartbeatInterval = null;
        this.connectionStatus = false;
        
        // Active requests map for real-time tracking
        this.activeRequests = new Map();
        this.maxActiveRequests = 50;
    }

    /**
     * Add event listener
     * @param {Function} callback Event callback function
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
     * Connect to WebSocket server
     */
    async connect() {
        if (this.isDestroyed) {
            console.warn('RealTimeManager is destroyed, cannot connect');
            return;
        }

        if (this.connection && this.connection.readyState === WebSocket.OPEN) {
            console.log('WebSocket already connected');
            return;
        }

        // Get WebSocket URL from backend
        let wsUrl;
        try {
            if (window.go?.main?.App?.GetWebSocketURL) {
                wsUrl = await window.go.main.App.GetWebSocketURL();
            } else {
                // Fallback to default
                wsUrl = `ws://localhost:${state.settings.port || 5600}/ws`;
            }
        } catch (error) {
            console.error('Failed to get WebSocket URL:', error);
            wsUrl = `ws://localhost:5600/ws`;
        }

        console.log(`Connecting to WebSocket: ${wsUrl}`);

        try {
            this.connection = new WebSocket(wsUrl);

            // Connection timeout
            const connectTimeout = setTimeout(() => {
                if (this.connection && this.connection.readyState === WebSocket.CONNECTING) {
                    console.error('WebSocket connection timeout');
                    this.connection.close();
                    this.scheduleReconnect();
                }
            }, 5000);

            this.connection.onopen = () => {
                clearTimeout(connectTimeout);
                console.log('WebSocket connected');
                this.connectionStatus = true;
                this.reconnectAttempts = 0;
                this.startHeartbeat();
                this.notifyListeners({ type: 'connection', status: 'connected' });
            };

            this.connection.onmessage = (event) => {
                this.handleMessage(event);
            };

            this.connection.onclose = (event) => {
                clearTimeout(connectTimeout);
                console.log('WebSocket disconnected', event.code, event.reason);
                this.connectionStatus = false;
                this.stopHeartbeat();
                this.notifyListeners({ 
                    type: 'connection', 
                    status: 'disconnected',
                    code: event.code,
                    reason: event.reason
                });

                if (!this.isDestroyed && event.code !== 1000) {
                    this.scheduleReconnect();
                }
            };

            this.connection.onerror = (error) => {
                clearTimeout(connectTimeout);
                console.error('WebSocket error:', error);
                this.notifyListeners({ type: 'connection', status: 'error', error });
            };

        } catch (error) {
            console.error('Failed to create WebSocket connection:', error);
            this.scheduleReconnect();
        }
    }

    /**
     * Handle incoming WebSocket message
     */
    handleMessage(event) {
        try {
            // Handle multiple messages separated by newlines
            const messages = event.data.split('\n').filter(m => m.trim());
            
            for (const msgStr of messages) {
                const message = JSON.parse(msgStr);
                
                // Skip ping messages
                if (message.type === 'ping') {
                    return;
                }

                // Process request_log messages
                if (message.type === 'request_log' && message.payload) {
                    this.processRequestLog(message.payload);
                }

                // Process token_stats messages
                if (message.type === 'token_stats' && message.payload) {
                    this.notifyListeners({
                        type: 'token_stats',
                        data: message.payload
                    });
                }

                // Process debug_log messages (backend debug logs for UI console)
                if (message.type === 'debug_log' && message.payload) {
                    this.notifyListeners({
                        type: 'debug_log',
                        data: message.payload
                    });
                }
            }
        } catch (error) {
            console.error('Failed to parse WebSocket message:', error, event.data);
        }
    }

    /**
     * Process request log update
     */
    processRequestLog(log) {
        if (!log || !log.id) return;

        const requestId = log.id;
        const existingRequest = this.activeRequests.get(requestId);

        // Determine request status
        const status = this.determineStatus(log);
        
        // Build request object
        const request = {
            request_id: requestId,
            interfaceType: log.interfaceType || '',
            vendorName: log.vendorName || '',
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
            // Computed display values
            displayDuration: log.runTime || 0,
            startTime: existingRequest?.startTime || new Date().toISOString()
        };

        // Update or add to active requests
        this.activeRequests.set(requestId, request);

        // Cleanup old requests if needed
        this.cleanupOldRequests();

        // Determine event type
        let eventType = 'progress';
        if (!existingRequest) {
            eventType = 'started';
        } else if (status === 'COMPLETED' || status === 'FAILED') {
            eventType = status === 'COMPLETED' ? 'completed' : 'failed';
        }

        // Notify listeners
        this.notifyListeners({
            type: eventType,
            request_id: requestId,
            data: request
        });

        // Immediately remove completed requests from active map
        // They will be shown in the logs list instead
        if (status === 'COMPLETED' || status === 'FAILED') {
            this.activeRequests.delete(requestId);
            this.notifyListeners({
                type: 'removed',
                request_id: requestId
            });
        }
    }

    /**
     * Determine request status from log
     */
    determineStatus(log) {
        if (log.status === 'in_progress') {
            return 'PENDING';
        }
        if (log.status === 'success' || log.statusCode === 200) {
            return 'COMPLETED';
        }
        if (log.status && log.status.startsWith('error')) {
            return 'FAILED';
        }
        return 'PENDING';
    }

    /**
     * Cleanup old requests to prevent memory bloat
     */
    cleanupOldRequests() {
        if (this.activeRequests.size <= this.maxActiveRequests) return;

        // Sort by timestamp and remove oldest
        const sorted = Array.from(this.activeRequests.entries())
            .sort((a, b) => new Date(b[1].startTime) - new Date(a[1].startTime));

        const toRemove = sorted.slice(this.maxActiveRequests);
        toRemove.forEach(([id]) => this.activeRequests.delete(id));
    }

    /**
     * Start heartbeat to keep connection alive
     */
    startHeartbeat() {
        this.stopHeartbeat();
        this.heartbeatInterval = setInterval(() => {
            if (this.connection && this.connection.readyState === WebSocket.OPEN) {
                try {
                    this.connection.send('{"type":"ping"}');
                } catch (error) {
                    console.error('Heartbeat failed:', error);
                    this.stopHeartbeat();
                }
            }
        }, 30000);
    }

    /**
     * Stop heartbeat
     */
    stopHeartbeat() {
        if (this.heartbeatInterval) {
            clearInterval(this.heartbeatInterval);
            this.heartbeatInterval = null;
        }
    }

    /**
     * Schedule reconnection with exponential backoff
     */
    scheduleReconnect() {
        if (this.isDestroyed) return;

        if (this.reconnectAttempts >= this.maxReconnectAttempts) {
            console.error(`Max reconnect attempts (${this.maxReconnectAttempts}) reached`);
            return;
        }

        // Fast reconnect for first 3 attempts, then exponential backoff
        let delay;
        if (this.reconnectAttempts < 3) {
            delay = 2000;
        } else {
            delay = this.reconnectDelay * Math.pow(2, this.reconnectAttempts - 3);
        }

        this.reconnectAttempts++;
        console.log(`Reconnecting in ${delay}ms (attempt ${this.reconnectAttempts}/${this.maxReconnectAttempts})`);

        setTimeout(() => {
            if (!this.isDestroyed) {
                this.connect();
            }
        }, delay);
    }

    /**
     * Notify all listeners
     */
    notifyListeners(event) {
        this.listeners.forEach(listener => {
            try {
                listener(event);
            } catch (error) {
                console.error('Listener error:', error);
            }
        });
    }

    /**
     * Get connection status
     */
    isConnected() {
        return this.connectionStatus;
    }

    /**
     * Get all active requests
     */
    getActiveRequests() {
        return Array.from(this.activeRequests.values())
            .sort((a, b) => new Date(b.startTime) - new Date(a.startTime));
    }

    /**
     * Manual reconnect
     */
    reconnect() {
        if (this.connection) {
            this.connection.close();
        }
        this.reconnectAttempts = 0;
        this.connect();
    }

    /**
     * Disconnect
     */
    disconnect() {
        if (this.connection) {
            this.connection.close(1000, 'User disconnect');
        }
        this.stopHeartbeat();
        this.connectionStatus = false;
    }

    /**
     * Destroy manager
     */
    destroy() {
        console.log('Destroying RealTimeManager...');
        this.isDestroyed = true;
        this.disconnect();
        this.listeners.clear();
        this.activeRequests.clear();
        console.log('RealTimeManager destroyed');
    }

    /**
     * Get manager status
     */
    getStatus() {
        return {
            isDestroyed: this.isDestroyed,
            connected: this.connectionStatus,
            reconnectAttempts: this.reconnectAttempts,
            activeRequests: this.activeRequests.size,
            listeners: this.listeners.size
        };
    }
}

// Singleton instance
let realtimeManager = null;

/**
 * Get or create RealTimeManager instance
 */
export function getRealTimeManager() {
    if (!realtimeManager) {
        realtimeManager = new RealTimeManager();
    }
    return realtimeManager;
}

/**
 * Initialize real-time connection
 */
export async function initRealTime() {
    const manager = getRealTimeManager();
    await manager.connect();
    return manager;
}

/**
 * Cleanup real-time manager
 */
export function cleanupRealTime() {
    if (realtimeManager) {
        realtimeManager.destroy();
        realtimeManager = null;
    }
}

export { RealTimeManager };
