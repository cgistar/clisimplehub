/**
 * Global state management
 */
export const state = {
    currentTab: 'claude',
    endpoints: {
        claude: [],
        codex: [],
        gemini: [],
        chat: []
    },
    recentLogs: [],
    tokenStats: [],
    settings: {
        port: 5600,
        configPath: '',
        apiKey: '',
        fallback: false,
        debugMode: ''
    },
    language: 'en',
    wsConnection: null,
    // Manage modal state
    vendors: [],
    selectedVendor: null,
    vendorEndpoints: []
};
