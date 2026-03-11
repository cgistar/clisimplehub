/**
 * Cli Simple Hub Frontend — Vue hybrid shell entry
 */
import './index.css'
import { createApp } from 'vue'
import { createPinia } from 'pinia'
import { i18n } from './i18n/vue-i18n'
import App from './App.vue'
import { useSettingsStore } from './stores/settingsStore'
import { useMainTabs } from './composables/useMainTabs'
import { useHomeEndpointsStore } from './stores/homeEndpointsStore'
import { useKiroAccountsStore } from './stores/kiroAccountsStore'
import { useKiroConfigStore } from './stores/kiroConfigStore'
import { useCodexAccountsStore } from './stores/codexAccountsStore'
import { useClashStore } from './stores/clashStore'

import { waitForWails } from './utils/helper'

// --- Bootstrap ---

let settingsStore: ReturnType<typeof useSettingsStore> | null = null
const { setTabVisibility, showKiro, showCodex, showClash } = useMainTabs()

document.addEventListener('DOMContentLoaded', async () => {
    await waitForWails()

    // Create & mount Vue app
    const app = createApp(App)
    const pinia = createPinia()
    app.use(pinia)
    settingsStore = useSettingsStore(pinia)
    await settingsStore.loadLanguage()
    app.use(i18n)
    app.mount('#app')

    const endpointsStore = useHomeEndpointsStore(pinia)
    const kiroAccountsStore = useKiroAccountsStore(pinia)
    const kiroConfigStore = useKiroConfigStore(pinia)
    const codexAccountsStore = useCodexAccountsStore(pinia)
    const clashStore = useClashStore(pinia)

    const refreshAfterConfigReload = async (): Promise<void> => {
        try {
            await endpointsStore.refreshCurrent()
        } catch {
            // ignore
        }

        if (showKiro.value) {
            try {
                await Promise.all([
                    kiroAccountsStore.loadAccounts(),
                    kiroConfigStore.loadConfig(),
                    kiroConfigStore.loadGlobalConfig()
                ])
            } catch {
                // ignore
            }
        }

        if (showCodex.value) {
            try {
                await codexAccountsStore.loadAccounts(true)
            } catch {
                // ignore
            }
        }

        if (showClash.value) {
            try {
                await clashStore.loadAll(true)
            } catch {
                // ignore
            }
        }
    }

    try {
        const runtime = (window as any).runtime
        if (runtime?.EventsOn) {
            runtime.EventsOn('config:reloaded', () => {
                void refreshAfterConfigReload()
            })
        }
    } catch {
        // ignore
    }

    // Query backend for tab visibility
    try {
        const safeBool = async (fn: () => Promise<boolean>) => { try { return !!(await fn()) } catch { return false } }
        const [kiro, codex, clash] = await Promise.all([
            safeBool(() => window.go.main.App.IsKiroAvailable()),
            safeBool(() => window.go.main.App.IsCodexAccountsAvailable()),
            safeBool(() => window.go.main.App.IsClashAvailable()),
        ])
        setTabVisibility({ kiro, codex, clash })
    } catch (e) {
        console.error('Tab visibility check failed:', e)
    }

    await settingsStore.loadSettings()
})
