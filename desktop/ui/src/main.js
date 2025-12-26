/**
 * Cli Simple Hub Frontend
 * Main entry point with modular architecture
 */
import './style.css';

// Import modules
import { state } from './modules/state.js';
import { initUI } from './modules/ui.js';
import { waitForWails } from './modules/utils.js';
import { loadLanguage, changeLanguage, loadSettings, showSettingsModal, closeSettingsModal, saveSettings, refreshConfig } from './modules/settings.js';
import { switchTab, loadEndpoints, setActiveEndpoint, setActiveEndpointById, toggleEndpointEnabled, initEndpointsRealtimeUpdates, cleanupEndpointsRealtimeUpdates, pingSingleEndpoint, pingAllEndpoints } from './modules/endpoints.js';
import { loadRecentLogs, showLogDetail, closeLogDetailModal, initLogs, toggleRealtimeConnection } from './modules/logs.js';
import { loadTokenStats, showStatsModal, closeStatsModal, setStatsTimeRange, refreshStats, clearStatsData } from './modules/stats.js';
import { connectWebSocket } from './modules/websocket.js';
import { initRealTime, cleanupRealTime } from './modules/realtime.js';
import {
    showManageModal,
    closeManageModal,
    selectVendor,
    showVendorForm,
    closeVendorForm,
    editVendor,
    saveVendor,
    deleteVendor,
    deleteVendorById,
    showEndpointForm,
    closeEndpointForm,
    editEndpoint,
    editEndpointFromList,
    closeEndpointDetailModal,
    saveEndpoint,
    deleteEndpoint,
    deleteEndpointById,
    toggleApiKeyVisibility,
    toggleInterfaceTypeDropdown,
    onEndpointInterfaceTypeChange,
    updateTestButtonVisibility,
    testEndpoint,
    fetchModels,
    toggleModelDropdown,
    toggleTransformerDropdown,
    addModelMapping,
    removeModelMapping,
    applyQuickModelMappings
} from './modules/vendors.js';
import {
    toggleConsolePanel,
    toggleBottomConsole,
    changeConsoleLogLevel,
    copyConsoleLogs,
    clearConsoleLogs,
    initConsole,
    logInfo,
    logError
} from './modules/console.js';
import {
    openCLIConfigEditor,
    closeCLIConfigEditor,
    saveCLIConfig,
    processCLIConfig,
    updateCLIConfigEditorButton,
} from './modules/cliconfig.js';
import {
    showWebDAVModal,
    closeWebDAVModal,
    testWebDAVConnection,
    backupToWebDAV,
    loadBackupsList,
    loadConfigFromWebDAV,
    deleteBackupFromWebDAV
} from './modules/webdav.js';

// Initialize the application
document.addEventListener('DOMContentLoaded', async () => {
    // Wait for Wails runtime to be ready
    await waitForWails();
    
    // Load language settings
    await loadLanguage();
    
    // Initialize UI
    initUI();
    
    // Load initial data
    await loadSettings();
    await loadEndpoints(state.currentTab);
    await loadRecentLogs();
    await loadTokenStats();
    
    // Connect WebSocket for real-time updates
    connectWebSocket();
    
    // Initialize real-time request manager
    await initRealTime();
    initLogs();
    initEndpointsRealtimeUpdates();
    
    // Initialize console
    initConsole();
    
    // Set up periodic refresh as fallback
    setInterval(async () => {
        if (state.wsConnection && state.wsConnection.readyState === WebSocket.OPEN) return;
        await loadRecentLogs();
        await loadTokenStats();
    }, 5000);
    
    // Cleanup on page unload
    window.addEventListener('beforeunload', () => {
        cleanupEndpointsRealtimeUpdates();
        cleanupRealTime();
    });
});

// Expose functions to window for onclick handlers
window.switchTab = switchTab;
window.setActiveEndpoint = setActiveEndpoint;
window.setActiveEndpointById = setActiveEndpointById;
window.toggleEndpointEnabled = toggleEndpointEnabled;
window.pingSingleEndpoint = pingSingleEndpoint;
window.pingAllEndpoints = pingAllEndpoints;
window.showSettingsModal = showSettingsModal;
window.closeSettingsModal = closeSettingsModal;
window.saveSettings = saveSettings;
window.refreshConfig = refreshConfig;
window.changeLanguage = changeLanguage;
window.showManageModal = showManageModal;
window.closeManageModal = closeManageModal;
window.selectVendor = selectVendor;
window.showVendorForm = showVendorForm;
window.closeVendorForm = closeVendorForm;
window.editVendor = editVendor;
window.saveVendor = saveVendor;
window.deleteVendor = deleteVendor;
window.deleteVendorById = deleteVendorById;
window.showEndpointForm = showEndpointForm;
window.closeEndpointForm = closeEndpointForm;
window.editEndpoint = editEndpoint;
window.editEndpointFromList = editEndpointFromList;
window.closeEndpointDetailModal = closeEndpointDetailModal;
window.saveEndpoint = saveEndpoint;
window.deleteEndpoint = deleteEndpoint;
window.deleteEndpointById = deleteEndpointById;
window.showLogDetail = showLogDetail;
window.closeLogDetailModal = closeLogDetailModal;
window.toggleRealtimeConnection = toggleRealtimeConnection;
window.toggleApiKeyVisibility = toggleApiKeyVisibility;
window.toggleInterfaceTypeDropdown = toggleInterfaceTypeDropdown;
window.onEndpointInterfaceTypeChange = onEndpointInterfaceTypeChange;
window.toggleConsolePanel = toggleConsolePanel;
window.toggleBottomConsole = toggleBottomConsole;
window.changeConsoleLogLevel = changeConsoleLogLevel;
window.copyConsoleLogs = copyConsoleLogs;
window.clearConsoleLogs = clearConsoleLogs;
window.showStatsModal = showStatsModal;
window.closeStatsModal = closeStatsModal;
window.setStatsTimeRange = setStatsTimeRange;
window.refreshStats = refreshStats;
window.clearStatsData = clearStatsData;
window.updateTestButtonVisibility = updateTestButtonVisibility;
window.testEndpoint = testEndpoint;
window.fetchModels = fetchModels;
window.toggleModelDropdown = toggleModelDropdown;
window.toggleTransformerDropdown = toggleTransformerDropdown;
window.addModelMapping = addModelMapping;
window.removeModelMapping = removeModelMapping;
window.applyQuickModelMappings = applyQuickModelMappings;
window.openCLIConfigEditor = openCLIConfigEditor;
window.closeCLIConfigEditor = closeCLIConfigEditor;
window.saveCLIConfig = saveCLIConfig;
window.processCLIConfig = processCLIConfig;
window.showWebDAVModal = showWebDAVModal;
window.closeWebDAVModal = closeWebDAVModal;
window.testWebDAVConnection = testWebDAVConnection;
window.backupToWebDAV = backupToWebDAV;
window.loadBackupsList = loadBackupsList;
window.loadConfigFromWebDAV = loadConfigFromWebDAV;
window.deleteBackupFromWebDAV = deleteBackupFromWebDAV;
