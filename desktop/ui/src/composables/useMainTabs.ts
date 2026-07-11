import { ref } from 'vue'
import { useConsole } from '@/composables/useConsole'

export type MainTabName = 'home' | 'kiro-accounts' | 'codex-accounts' | 'xai-accounts' | 'clash' | 'settings'

const activeTab = ref<MainTabName>('home')
const showKiro = ref(false)
const showCodex = ref(false)
const showXai = ref(false)
const showClash = ref(false)
const { closeBottomConsole } = useConsole()

function setTab(tabName: MainTabName): void {
  activeTab.value = tabName
}

function setTabVisibility({
  kiro,
  codex,
  xai,
  clash
}: {
  kiro?: boolean
  codex?: boolean
  xai?: boolean
  clash?: boolean
}): void {
  if (kiro !== undefined) showKiro.value = !!kiro
  if (codex !== undefined) showCodex.value = !!codex
  if (xai !== undefined) showXai.value = !!xai
  if (clash !== undefined) showClash.value = !!clash
  if (showClash.value === false && activeTab.value === 'clash') {
    setTab('home')
    window.dispatchEvent(new Event('home:visible'))
  }
  if (showXai.value === false && activeTab.value === 'xai-accounts') {
    setTab('home')
    window.dispatchEvent(new Event('home:visible'))
  }
}

async function switchMainTab(tabName: MainTabName): Promise<void> {
  setTab(tabName)

  if (tabName !== 'home') {
    closeBottomConsole()
  }

  if (tabName === 'home') {
    window.dispatchEvent(new Event('home:visible'))
  }
}

export function useMainTabs() {
  return {
    activeTab,
    showKiro,
    showCodex,
    showXai,
    showClash,
    setTab,
    setTabVisibility,
    switchMainTab
  }
}
