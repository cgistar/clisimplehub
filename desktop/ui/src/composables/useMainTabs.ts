import { ref } from 'vue'
import { useConsole } from '@/composables/useConsole'

export type MainTabName = 'home' | 'kiro-accounts' | 'codex-accounts' | 'xray' | 'settings'

const activeTab = ref<MainTabName>('home')
const showKiro = ref(false)
const showCodex = ref(false)
const showXray = ref(false)
const { closeBottomConsole } = useConsole()

function setTab(tabName: MainTabName): void {
  activeTab.value = tabName
}

function setTabVisibility({
  kiro,
  codex,
  xray
}: {
  kiro?: boolean
  codex?: boolean
  xray?: boolean
}): void {
  if (kiro !== undefined) showKiro.value = !!kiro
  if (codex !== undefined) showCodex.value = !!codex
  if (xray !== undefined) showXray.value = !!xray
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
    showXray,
    setTab,
    setTabVisibility,
    switchMainTab
  }
}
