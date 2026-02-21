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

import { waitForWails } from './utils/helper'

// --- Bootstrap ---

let settingsStore: ReturnType<typeof useSettingsStore> | null = null
const { setTabVisibility } = useMainTabs()

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

    // Query backend for tab visibility
    try {
        const safeBool = async (fn: () => Promise<boolean>) => { try { return !!(await fn()) } catch { return false } }
        const [kiro, codex, xray] = await Promise.all([
            safeBool(() => window.go.main.App.IsKiroAvailable()),
            safeBool(() => window.go.main.App.IsCodexAccountsAvailable()),
            safeBool(() => window.go.main.App.IsXRayAvailable()),
        ])
        setTabVisibility({ kiro, codex, xray })
    } catch (e) {
        console.error('Tab visibility check failed:', e)
    }

    await settingsStore.loadSettings()
})
