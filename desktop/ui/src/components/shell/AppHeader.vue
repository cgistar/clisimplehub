<script setup lang="ts">
import type { Component } from 'vue'
import { computed, ref } from 'vue'
import { NRadioButton, NRadioGroup, NTabPane, NTabs } from 'naive-ui'
import { House, Network, Settings, Users } from 'lucide-vue-next'
import { storeToRefs } from 'pinia'
import { useI18n } from 'vue-i18n'
import { useSettingsStore } from '@/stores/settingsStore'

type TabKey = 'home' | 'kiro-accounts' | 'codex-accounts' | 'clash' | 'settings'
type LabelKey = 'home' | 'kiro' | 'codex' | 'clash' | 'settings'
type VisibleProp = 'showKiro' | 'showCodex' | 'showClash'
type IconKey = 'home' | 'users' | 'network' | 'settings'

interface HeaderTab {
  key: TabKey
  icon: IconKey
  labelKey: LabelKey
  visibleProp?: VisibleProp
}

const props = withDefaults(defineProps<{
  activeTab?: TabKey
  showKiro?: boolean
  showCodex?: boolean
  showClash?: boolean
}>(), {
  activeTab: 'home',
  showKiro: false,
  showCodex: false,
  showClash: false
})

const emit = defineEmits<{
  'tab-change': [tab: TabKey]
}>()

const settingsStore = useSettingsStore()
const { t } = useI18n()
const { language } = storeToRefs(settingsStore)
const switchingLanguage = ref(false)

const iconComponents: Record<IconKey, Component> = {
  home: House,
  users: Users,
  network: Network,
  settings: Settings
}

const tabs: HeaderTab[] = [
  { key: 'home', icon: 'home', labelKey: 'home' },
  { key: 'kiro-accounts', icon: 'users', labelKey: 'kiro', visibleProp: 'showKiro' },
  { key: 'codex-accounts', icon: 'users', labelKey: 'codex', visibleProp: 'showCodex' },
  { key: 'clash', icon: 'network', labelKey: 'clash', visibleProp: 'showClash' },
  { key: 'settings', icon: 'settings', labelKey: 'settings' },
]

const tabLabels = computed<Record<LabelKey, string>>(() => ({
  home: t('header.home'),
  kiro: t('header.kiro'),
  codex: t('header.codex'),
  clash: t('header.clash'),
  settings: t('header.settings')
}))

function onTabClick(key: TabKey): void {
  emit('tab-change', key)
}

function isTabKey(value: string): value is TabKey {
  return value === 'home' ||
    value === 'kiro-accounts' ||
    value === 'codex-accounts' ||
    value === 'clash' ||
    value === 'settings'
}

function onTabUpdate(value: string): void {
  if (!isTabKey(value)) return
  onTabClick(value)
}

function isTabVisible(tab: HeaderTab): boolean {
  if (!tab.visibleProp) return true
  return props[tab.visibleProp]
}

const visibleTabs = computed(() => tabs.filter((tab) => isTabVisible(tab)))

async function handleLanguageChange(value: string | number | boolean | null): Promise<void> {
  if (value !== 'en' && value !== 'zh-CN') return
  if (value === language.value || switchingLanguage.value) return

  switchingLanguage.value = true
  try {
    await settingsStore.changeLanguage(value)
  } finally {
    switchingLanguage.value = false
  }
}
</script>

<template>
  <div class="app-header">
    <div class="app-header-content">
      <div class="app-header-tabs">
        <n-tabs
          type="line"
          size="small"
          :value="activeTab"
          @update:value="onTabUpdate"
        >
          <n-tab-pane
            v-for="tab in visibleTabs"
            :key="tab.key"
            :name="tab.key"
          >
            <template #tab>
              <span class="header-tab-label" :data-tab="tab.key">
                <span class="header-tab-icon">
                  <component :is="iconComponents[tab.icon]" :size="14" :stroke-width="2" />
                </span>
                <span>{{ tabLabels[tab.labelKey] }}</span>
              </span>
            </template>
          </n-tab-pane>
          <template #suffix>
            <n-radio-group
              class="header-language-radio"
              :value="language"
              :disabled="switchingLanguage"
              @update:value="handleLanguageChange"
            >
              <n-radio-button value="zh-CN">
                中
              </n-radio-button>
              <n-radio-button value="en">
                en
              </n-radio-button>
            </n-radio-group>
          </template>
        </n-tabs>
      </div>
    </div>
  </div>
</template>

<style scoped>
.app-header {
  position: sticky;
  top: 0;
  z-index: 40;
  border-bottom: 1px solid var(--border-light);
  background: color-mix(in srgb, #f7fbff 88%, white);
  backdrop-filter: blur(8px);
}

.app-header-content {
  height: 58px;
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 0 16px;
}

.app-header-tabs {
  flex: 1;
  min-width: 0;
}

.header-language-radio {
  min-width: 76px;
}

.header-tab-label {
  display: inline-flex;
  align-items: center;
  gap: 6px;
}

.header-tab-icon {
  display: inline-flex;
  align-items: center;
}

.app-header-tabs :deep(.n-tabs-nav) {
  margin-bottom: 0;
}

.app-header-tabs :deep(.n-tabs-nav-scroll-content) {
  align-items: center;
}
</style>
