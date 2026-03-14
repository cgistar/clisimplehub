<script setup lang="ts">
import { onMounted } from 'vue'
import { NConfigProvider, NMessageProvider, NDialogProvider } from 'naive-ui'
import hljs from 'highlight.js/lib/core'
import bash from 'highlight.js/lib/languages/bash'
import javascript from 'highlight.js/lib/languages/javascript'
import json from 'highlight.js/lib/languages/json'
import AppHeader from './components/shell/AppHeader.vue'
import ConsolePanel from './components/shell/ConsolePanel.vue'
import CodexAccountsPage from './components/codex/CodexAccountsPage.vue'
import HomePage from './components/home/HomePage.vue'
import KiroAccountsPage from './components/kiro/KiroAccountsPage.vue'
import SettingsPage from './components/settings/SettingsPage.vue'
import ClashPage from './components/clash/ClashPage.vue'
import { useConsole } from './composables/useConsole'
import { useMainTabs, type MainTabName } from './composables/useMainTabs'

const {
  activeTab,
  showKiro,
  showCodex,
  showClash,
  switchMainTab
} = useMainTabs()
const { initConsole } = useConsole()

hljs.registerLanguage('bash', bash)
hljs.registerLanguage('javascript', javascript)
hljs.registerLanguage('json', json)

const themeOverrides = {
  common: {
    primaryColor: '#0284c7',
    primaryColorHover: '#0369a1',
    primaryColorPressed: '#075985',
    borderRadius: '4px',
  },
}

function onTabChange(tabName: MainTabName): void {
  void switchMainTab(tabName)
}

onMounted(() => {
  initConsole()
})
</script>

<template>
  <n-config-provider :theme-overrides="themeOverrides" :hljs="hljs">
    <n-message-provider>
      <n-dialog-provider>
        <div class="app-shell">
          <AppHeader
            :active-tab="activeTab"
            :show-kiro="showKiro"
            :show-codex="showCodex"
            :show-clash="showClash"
            @tab-change="onTabChange"
          />

          <!-- Home (Vue) -->
          <div
            v-show="activeTab === 'home'"
            class="main-container"
            id="homeView"
          >
            <HomePage />
          </div>

          <!-- Kiro Accounts (Vue) -->
          <div
            v-show="activeTab === 'kiro-accounts'"
            class="main-container kiro-accounts-view"
            id="kiroAccountsView"
          >
            <KiroAccountsPage />
          </div>

          <!-- Codex Accounts (Vue) -->
          <div
            v-show="activeTab === 'codex-accounts'"
            class="main-container"
            id="codexAccountsView"
          >
            <CodexAccountsPage />
          </div>

          <!-- Clash (Vue) -->
          <div
            v-show="showClash && activeTab === 'clash'"
            class="main-container clash-view"
            id="clashView"
          >
            <ClashPage :active="showClash && activeTab === 'clash'" />
          </div>

          <!-- Settings (Vue) -->
          <div
            v-show="activeTab === 'settings'"
            class="main-container settings-view"
            id="settingsView"
          >
            <SettingsPage />
          </div>

          <ConsolePanel />
        </div>
      </n-dialog-provider>
    </n-message-provider>
  </n-config-provider>
</template>

<style scoped>
.app-shell {
  height: 100dvh;
  max-height: 100dvh;
  flex: 1;
  min-height: 0;
  display: flex;
  flex-direction: column;
  overflow: hidden;
}

:deep(.n-config-provider),
:deep(.n-message-provider),
:deep(.n-dialog-provider) {
  display: flex;
  flex-direction: column;
  height: 100%;
  min-height: 0;
  overflow: hidden;
}
</style>
