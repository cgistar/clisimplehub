/**
 * Cli Simple Hub Frontend
 * Main entry point with modular architecture
 */
import './style.css';

// Import modules
import { state } from './modules/state.js';
import { initUI } from './modules/ui.js';
import { waitForWails } from './modules/utils.js';
import { loadLanguage, changeLanguage, loadSettings, showSettingsModal, closeSettingsModal, saveSettings, refreshConfig, toggleDebugModeDropdown } from './modules/settings.js';
import { switchTab, loadEndpoints, setActiveEndpoint, setActiveEndpointById, toggleEndpointEnabled, initEndpointsRealtimeUpdates, cleanupEndpointsRealtimeUpdates, pingSingleEndpoint, pingAllEndpoints, applyEndpointToConfig } from './modules/endpoints.js';
import { loadRecentLogs, showLogDetail, closeLogDetailModal, initLogs } from './modules/logs.js';
import { loadTokenStats, showStatsModal, closeStatsModal, setStatsTimeRange, refreshStats, clearStatsData } from './modules/stats.js';
import { initRealTime, getRealTimeManager, cleanupRealTime } from './modules/realtime.js';
import {
    showManageModal,
    closeManageModal,
    showVendorForm,
    closeVendorForm,
    editVendor,
    saveVendor,
    deleteVendor,
    deleteVendorById
} from './modules/vendors.js';
import {
    showEndpointForm,
    closeEndpointForm,
    editEndpoint,
    editEndpointFromList,
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
    toggleVendorDropdown,
    addModelMapping,
    removeModelMapping,
    addRoute,
    removeRoute,
    applyQuickModelMappings
} from './modules/endpoint-form.js';
import {
    toggleConsolePanel,
    toggleBottomConsole,
    changeConsoleLogLevel,
    copyConsoleLogs,
    clearConsoleLogs,
    initConsole,
} from './modules/console.js';
import {
    openCLIConfigEditor,
    closeCLIConfigEditor,
    saveCLIConfig,
    processCLIConfig,
    updateProxyStatus,
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
import {
    addServerAccount,
    editServerAccount,
    cancelServerForm,
    saveServerAccount,
    deleteServerAccount,
    testServerAccount,
    syncConfigToServer
} from './modules/serverSync.js';
import {
    showKiroConfigModal,
    closeKiroConfigModal,
    saveKiroConfig,
    testKiroRefreshToken,
    onKiroRefreshTokenInput,
    onKiroIdcFieldsInput,
    onKiroAuthMethodChange,
    fetchKiroUsage,
    onKiroRegionInput,
    toggleKiroRegionDropdown,
    toggleKiroAuthMethodDropdown,
    toggleKiroClientSecretVisibility,
    startIdcDeviceFlowLogin,
    closeIdcDeviceFlowDialog,
    copyIdcVerifyUrl,
    openIdcVerifyUrl,
    startIdcOrgLogin,
    closeIdcOrgLoginDialog,
    submitIdcOrgLogin,
    backToOrgLoginStep1,
    showKiroGlobalConfigModal,
    closeKiroGlobalConfigModal,
    addKiroModelMappingRow,
    resetKiroModelMappingDefaults,
    saveKiroGlobalConfig
} from './modules/kiro.js';
import {
    showKiroAccountsModal,
    closeKiroAccountsModal,
    hideAddKiroAccountOptions,
    toggleKiroAddAccountDropdown,
    hideKiroAddAccountDropdown,
    initKiroTabVisibility
} from './modules/kiroAccounts.js';
import {
    initCodexTabVisibility,
    toggleCodexAddAccountDropdown,
    hideCodexAddAccountDropdown
} from './modules/codexAccounts.js';
import { switchMainTab } from './modules/mainTabs.js';
import { initXRayTabVisibility } from './modules/xray.js';
import {
    showCodexGlobalConfigModal,
    closeCodexGlobalConfigModal,
    saveCodexGlobalConfig
} from './modules/codex.js';

function isVisibleModal(modal) {
    if (!(modal instanceof HTMLElement)) return false;
    const style = window.getComputedStyle(modal);
    return style.display !== 'none' && style.visibility !== 'hidden';
}

function getTopVisibleModal() {
    const modals = Array.from(document.querySelectorAll('.modal')).filter(isVisibleModal);
    let topModal = null;
    let topZIndex = Number.NEGATIVE_INFINITY;

    modals.forEach((modal) => {
        const zIndex = Number.parseInt(window.getComputedStyle(modal).zIndex, 10);
        const normalizedZIndex = Number.isNaN(zIndex) ? 0 : zIndex;
        if (!topModal || normalizedZIndex > topZIndex || normalizedZIndex === topZIndex) {
            topModal = modal;
            topZIndex = normalizedZIndex;
        }
    });

    return topModal;
}

function closeModalByEsc(modal) {
    if (!modal) return;

    const closeBtn = modal.querySelector(
        '.modal-header .modal-close, .modal-header .close-btn, .modal-header .btn-icon[onclick*="close"], .modal-header button[aria-label="close"], [data-role="close"], .confirm-cancel-btn'
    );
    if (closeBtn instanceof HTMLElement) {
        closeBtn.click();
        return;
    }

    // Let modal-specific handlers (if any) process Escape first.
    modal.dispatchEvent(new KeyboardEvent('keydown', {
        key: 'Escape',
        code: 'Escape',
        bubbles: false,
        cancelable: true
    }));

    if (!isVisibleModal(modal)) return;

    if (modal.classList.contains('active')) {
        modal.classList.remove('active');
        return;
    }

    if (modal.style.display && modal.style.display !== 'none') {
        modal.style.display = 'none';
    }
}

function initGlobalModalEscClose() {
    document.addEventListener('keydown', (event) => {
        if (event.key !== 'Escape') return;
        if (event.defaultPrevented || event.isComposing || event.ctrlKey || event.metaKey || event.altKey) return;

        const topModal = getTopVisibleModal();
        if (!topModal) return;

        event.preventDefault();
        event.stopPropagation();
        closeModalByEsc(topModal);
    }, true);
}

// Initialize the application
document.addEventListener('DOMContentLoaded', async () => {
    // Wait for Wails runtime to be ready
    await waitForWails();
    
    // Load language settings
    await loadLanguage();
    
    // Initialize UI
    initUI();
    initGlobalModalEscClose();
    await Promise.all([initXRayTabVisibility(), initKiroTabVisibility(), initCodexTabVisibility()]);

    // Load initial data
    await loadSettings();
    await loadEndpoints(state.currentTab);
    await loadRecentLogs();
    await loadTokenStats();
    
    // Initialize real-time SSE connection (replaces both WebSocket connections)
    await initRealTime();
    initLogs();
    initEndpointsRealtimeUpdates();
    
    // Initialize console
    initConsole();
    
    // Set up periodic refresh as fallback when SSE is disconnected
    setInterval(async () => {
        if (getRealTimeManager().isConnected()) return;
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
window.applyEndpointToConfig = applyEndpointToConfig;
window.showSettingsModal = showSettingsModal;
window.closeSettingsModal = closeSettingsModal;
window.saveSettings = saveSettings;
window.refreshConfig = refreshConfig;
window.changeLanguage = changeLanguage;
window.showManageModal = showManageModal;
window.closeManageModal = closeManageModal;
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
window.saveEndpoint = saveEndpoint;
window.deleteEndpoint = deleteEndpoint;
window.deleteEndpointById = deleteEndpointById;
window.showLogDetail = showLogDetail;
window.closeLogDetailModal = closeLogDetailModal;
window.toggleApiKeyVisibility = toggleApiKeyVisibility;
window.toggleInterfaceTypeDropdown = toggleInterfaceTypeDropdown;
window.onEndpointInterfaceTypeChange = onEndpointInterfaceTypeChange;
window.toggleVendorDropdown = toggleVendorDropdown;
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
window.addRoute = addRoute;
window.removeRoute = removeRoute;
window.applyQuickModelMappings = applyQuickModelMappings;
window.openCLIConfigEditor = openCLIConfigEditor;
window.closeCLIConfigEditor = closeCLIConfigEditor;
window.saveCLIConfig = saveCLIConfig;
window.processCLIConfig = processCLIConfig;
window.updateProxyStatus = updateProxyStatus;
window.showWebDAVModal = showWebDAVModal;
window.closeWebDAVModal = closeWebDAVModal;
window.testWebDAVConnection = testWebDAVConnection;
window.backupToWebDAV = backupToWebDAV;
window.loadBackupsList = loadBackupsList;
window.loadConfigFromWebDAV = loadConfigFromWebDAV;
window.deleteBackupFromWebDAV = deleteBackupFromWebDAV;
window.addServerAccount = addServerAccount;
window.editServerAccount = editServerAccount;
window.cancelServerForm = cancelServerForm;
window.saveServerAccount = saveServerAccount;
window.deleteServerAccount = deleteServerAccount;
window.testServerAccount = testServerAccount;
window.syncConfigToServer = syncConfigToServer;
window.showKiroConfigModal = showKiroConfigModal;
window.closeKiroConfigModal = closeKiroConfigModal;
window.saveKiroConfig = saveKiroConfig;
window.testKiroRefreshToken = testKiroRefreshToken;
window.onKiroRefreshTokenInput = onKiroRefreshTokenInput;
window.onKiroIdcFieldsInput = onKiroIdcFieldsInput;
window.onKiroAuthMethodChange = onKiroAuthMethodChange;
window.fetchKiroUsage = fetchKiroUsage;
window.onKiroRegionInput = onKiroRegionInput;
window.toggleKiroRegionDropdown = toggleKiroRegionDropdown;
window.toggleKiroAuthMethodDropdown = toggleKiroAuthMethodDropdown;
window.toggleKiroClientSecretVisibility = toggleKiroClientSecretVisibility;
window.startIdcDeviceFlowLogin = startIdcDeviceFlowLogin;
window.closeIdcDeviceFlowDialog = closeIdcDeviceFlowDialog;
window.copyIdcVerifyUrl = copyIdcVerifyUrl;
window.openIdcVerifyUrl = openIdcVerifyUrl;
window.startIdcOrgLogin = startIdcOrgLogin;
window.closeIdcOrgLoginDialog = closeIdcOrgLoginDialog;
window.submitIdcOrgLogin = submitIdcOrgLogin;
window.backToOrgLoginStep1 = backToOrgLoginStep1;
window.showKiroGlobalConfigModal = showKiroGlobalConfigModal;
window.closeKiroGlobalConfigModal = closeKiroGlobalConfigModal;
window.addKiroModelMappingRow = addKiroModelMappingRow;
window.resetKiroModelMappingDefaults = resetKiroModelMappingDefaults;
window.saveKiroGlobalConfig = saveKiroGlobalConfig;
window.toggleDebugModeDropdown = toggleDebugModeDropdown;
window.showKiroAccountsModal = showKiroAccountsModal;
window.closeKiroAccountsModal = closeKiroAccountsModal;
window.hideAddKiroAccountOptions = hideAddKiroAccountOptions;
window.toggleKiroAddAccountDropdown = toggleKiroAddAccountDropdown;
window.hideKiroAddAccountDropdown = hideKiroAddAccountDropdown;
window.switchMainTab = switchMainTab;
window.showCodexGlobalConfigModal = showCodexGlobalConfigModal;
window.closeCodexGlobalConfigModal = closeCodexGlobalConfigModal;
window.saveCodexGlobalConfig = saveCodexGlobalConfig;
window.toggleCodexAddAccountDropdown = toggleCodexAddAccountDropdown;
window.hideCodexAddAccountDropdown = hideCodexAddAccountDropdown;
